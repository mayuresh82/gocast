package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/mayuresh82/gocast/config"
)

const (
	consulNodeEnv        = "CONSUL_NODE"
	consulToken          = "CONSUL_TOKEN"
	allowStale           = "CONSUL_STALE"
	matchTag             = "enable_gocast"
	nodeURL              = "/catalog/node"
	remoteHealthCheckurl = "/health/checks"
	localHealthCheckurl  = "/agent/checks"
)

type Clienter interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	*http.Client
}

type ConsulMon struct {
	addr   string
	token  string
	node   string
	client Clienter
}

type ConsulServiceData struct {
	Services map[string]struct {
		ID      string
		Service string
		Tags    []string
	}
}

func contains(inp []string, elem string) bool {
	for _, a := range inp {
		if a == elem {
			return true
		}
	}
	return false
}

func NewConsulMon(addr string, token string) (*ConsulMon, error) {
	node := os.Getenv(consulNodeEnv)
	if node == "" {
		return nil, fmt.Errorf("%s env variable not set", consulNodeEnv)
	}
	return &ConsulMon{addr: addr, token: token, node: node, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

func getHTTPReq(httpMethod string, addr string, tokenFrmCfg string) (*http.Request, error) {
	req, err := http.NewRequest(httpMethod, addr, nil)
	if err != nil {
		return nil, err
	}
	tokenFrmEnv := os.Getenv(consulToken)
	if tokenFrmEnv != "" {
		req.Header.Set("X-Consul-Token", tokenFrmEnv)
	} else if tokenFrmCfg != "" {
		req.Header.Set("X-Consul-Token", tokenFrmCfg)
	}
	return req, nil
}

func (c *ConsulMon) queryServices() ([]*App, error) {
	var apps []*App
	var stale string
	if os.Getenv(allowStale) == "true" {
		stale = "stale"
	}
	addr := c.addr + fmt.Sprintf("%s/%s?%s", nodeURL, c.node, stale)
	req, err := getHTTPReq(http.MethodGet, addr, c.token)
	if err != nil {
		return apps, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return apps, err
	}
	defer resp.Body.Close()
	var consulData ConsulServiceData
	if err := json.NewDecoder(resp.Body).Decode(&consulData); err != nil {
		return apps, fmt.Errorf("Unable to decode consul data: %v", err)
	}
	for _, service := range consulData.Services {
		if !contains(service.Tags, matchTag) {
			continue
		}
		var (
			vip      string
			monitors []string
			nats     []string
		)
		var vipConf config.VipConfig
		for _, tag := range service.Tags {
			// try to find the requires tags. Only vip is mandatory
			parts := strings.Split(tag, "=")
			if len(parts) != 2 {
				continue
			}
			switch parts[0] {
			case "gocast_vip":
				vip = parts[1]
			case "gocast_vip_communities":
				vipConf.BgpCommunities = strings.Split(parts[1], ",")
			case "gocast_monitor":
				monitors = append(monitors, parts[1])
			case "gocast_nat":
				nats = append(nats, parts[1])
			}
		}
		if vip == "" {
			glog.Errorf("No vip Tag found in matched service :%s", service.Service)
			continue
		}
		app, err := NewApp(service.Service, vip, vipConf, monitors, nats, "consul")
		if err != nil {
			glog.Errorf("Unable to add consul app: %v", err)
			continue
		}
		apps = append(apps, app)

	}
	return apps, nil
}

// healthCheckLocal queries a node's local consul agent to perform service healthchecks
// This is the underlying api call: https://www.consul.io/api/agent/check.html
func (c *ConsulMon) healthCheckLocal(service string) (bool, error) {
	params := url.Values{}
	params.Add("filter", "enable_gocast in ServiceTags")
	addr := c.addr + fmt.Sprintf("%s?%s", localHealthCheckurl, params.Encode())
	req, err := getHTTPReq(http.MethodGet, addr, c.token)
	if err != nil {
		return false, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		glog.V(2).Infof("Error getting %s with %s", addr, err)
		return false, err
	}
	defer resp.Body.Close()
	var services map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return false, err
	}
	for _, sInfo := range services {
		serviceInfo := sInfo.(map[string]interface{})
		if serviceInfo["ServiceName"].(string) == service {
			status := serviceInfo["Status"].(string)
			if status == "passing" {
				return true, nil
			}
			glog.V(2).Infof("Consul local healthcheck returned %s status", status)
			return false, nil
		}
	}
	return false, fmt.Errorf("No local healthcheck info found for service %s on node %s in consul", service, c.node)
}

// healthCheckRemote queries the consul cluster's healthcheck endpoint to perform service healthchecks
// This is the underlying api call: https://www.consul.io/api/health.html
func (c *ConsulMon) healthCheckRemote(service string) (bool, error) {
	addr := c.addr + fmt.Sprintf("%s/%s", remoteHealthCheckurl, service)
	req, err := getHTTPReq(http.MethodGet, addr, c.token)
	if err != nil {
		return false, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		glog.V(2).Infof("Error getting %s with %s", addr, err)
		return false, err
	}
	defer resp.Body.Close()
	var data []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return false, err
	}

	for _, nodeInfo := range data {
		n := nodeInfo.(map[string]interface{})
		if n["Node"].(string) == c.node {
			if n["Status"].(string) == "passing" {
				return true, nil
			}
			glog.V(2).Infof("Consul healthcheck returned %s status", n["Status"].(string))
			return false, nil
		}
	}
	return false, fmt.Errorf("No healthcheck info found for node %s in consul", c.node)
}

// healthCheck determines if we should use the local agent
// If the address contains "localhost", then it presumes that the local agent is to be used.
func (c *ConsulMon) healthCheck(service string) (bool, error) {
	usingLocalAgent := strings.Contains(c.addr, "localhost") || strings.Contains(c.addr, "127.0.0.1")
	if usingLocalAgent {
		return c.healthCheckLocal(service)
	}
	return c.healthCheckRemote(service)
}
