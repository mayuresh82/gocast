package controller

import (
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"net/http"
	"os"
	"strings"
)

const (
	consulNodeEnv        = "CONSUL_NODE"
	matchTag             = "enable_gocast"
	nodeURL              = "/catalog/node"
	remoteHealthCheckurl = "/health/checks"
	localHealthCheckurl  = "/agent/checks"
)

type ConsulMon struct {
	addr string
	node string
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

func NewConsulMon(addr string) (*ConsulMon, error) {
	node := os.Getenv(consulNodeEnv)
	if node == "" {
		return nil, fmt.Errorf("%s env variable not set", consulNodeEnv)
	}
	return &ConsulMon{addr: addr, node: node}, nil
}

func (c *ConsulMon) queryServices() ([]*App, error) {
	var apps []*App
	addr := c.addr + fmt.Sprintf("%s/%s", nodeURL, c.node)
	resp, err := http.Get(addr)
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
		for _, tag := range service.Tags {
			// try to find the requires tags. Only vip is mandatory
			parts := strings.Split(tag, "=")
			if len(parts) != 2 {
				continue
			}
			switch parts[0] {
			case "gocast_vip":
				vip = parts[1]
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
		app, err := NewApp(service.Service, vip, monitors, nats, "consul")
		if err != nil {
			glog.Errorf("Unable to add consul app: %v", err)
			continue
		}
		apps = append(apps, app)

	}
	return apps, nil
}

// Returns a *Response to mimic http.Get with some minimal error handling.
// https://golang.org/pkg/net/http/#Get for docs on Get.
func getResp(addr) (resp *Response) {
	resp, err := http.Get(addr)
	if err != nil {
		glog.V(2).Errorf("Error getting %s with %s", addr, err)
	}
	defer resp.Body.Close()
	return resp
}

// healthCheckLocal queries a node's local consul agent to perform service healthchecks
// This is the underlying api call: https://www.consul.io/api/agent/check.html
func (c *ConsulMon) healthCheckLocal(service string) (bool, error) {
	addr := c.addr + fmt.Sprintf("%s/%s", localHealthCheckurl, service)
	resp, err = getResp(addr)

	var data interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return false, err
	}

	services := data.(map[string]interface{})
	for _, serviceInfo := range services {
		s := serviceInfo.(map[string]interface{})
		serviceName := s["ServiceName"].(string)
		if serviceName == service {
			status := s["Status"].(string)
			if status == "passing" {
				return true, nil
			}
			glog.V(2).Infof("Consul local healthcheck returned %s status", status)
			return false, nil
		}
		node := serviceReport["Node"].(string)
		return false, fmt.Errorf("No local healcheck info found for service %s on node %s in consul", service, node)
	}
}

// healthCheckRemote queries the consul cluster's healthcheck endpoint to perform service healthchecks
// This is the underlying api call: https://www.consul.io/api/health.html
func (c *ConsulMon) healthCheckRemote(service string) (bool, error) {
	addr := c.addr + fmt.Sprintf("%s/%s", remoteHealthCheckurl, service)
	resp, err = getResp(addr)

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
	return false, fmt.Errorf("No healcheck info found for node %s in consul", c.node)
}

// healthCheck determines if we should use the local agent
// If the address contains "localhost", then it presumes that the local agent is to be used.
func (c *ConsulMon) healthCheck(service string) (bool, error) {
	usingLocalAgent := strings.Contains(c.addr, "localhost")
	if (usingLocalAgent) {
		return c.healthCheckLocal(service)
	}
	return c.healthCheckRemote(service)
}