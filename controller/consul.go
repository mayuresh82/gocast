package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/mayuresh82/gocast/config"
)

const (
	consulNodeEnv        = "CONSUL_NODE"
	allowStale           = "CONSUL_STALE"
	matchTag             = "enable_gocast"
	nodeURL              = "/catalog/node"
	remoteHealthCheckurl = "/health/checks"
	localHealthCheckurl  = "/agent/checks"
)

type Clienter interface {
	Do(url, method string, body io.Reader) (*http.Response, error)
}

type httpClient struct {
	*http.Client
}

func (c *httpClient) Do(url, method string, body io.Reader) (*http.Response, error) {
	req, _ := http.NewRequest(method, url, body)
	resp, err := c.Client.Do(req)
	if err != nil {
		glog.Errorf("Failed to query : %v", err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			body = []byte{}
		}
		return nil, fmt.Errorf("Failed to query, Got %v: %v", resp.StatusCode, string(body))
	}
	return resp, nil
}

type ConsulMon struct {
	addr   string
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

func NewConsulMon(addr string) (*ConsulMon, error) {

	node := os.Getenv(consulNodeEnv)
	if node == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("%s env variable not set and couldnt fetch hostname", consulNodeEnv)
		}
		node = hostname
	}
	return &ConsulMon{addr: addr, node: node, client: &httpClient{&http.Client{Timeout: 10 * time.Second}}}, nil
}

func (c *ConsulMon) queryServices() ([]*App, error) {
	var apps []*App
	var stale string
	if os.Getenv(allowStale) == "true" {
		stale = "stale"
	}
	addr := c.addr + fmt.Sprintf("%s/%s?%s", nodeURL, c.node, stale)
	resp, err := c.client.Do(addr, "GET", nil)
	if err != nil {
		return apps, err
	}
	defer resp.Body.Close()
	var consulData ConsulServiceData
	if err := json.NewDecoder(resp.Body).Decode(&consulData); err != nil {
		return apps, fmt.Errorf("Unable to decode consul data: %v", err)
	}
	glog.V(2).Infof("queryServices: Got %v services", len(consulData.Services))
	for _, service := range consulData.Services {
		if !contains(service.Tags, matchTag) {
			continue
		}
		glog.V(2).Infof("queryServices: service %v id %v tags %v", service.Service, service.Service, service.Tags)
		var (
			vip       string
			monitors  []string
			nats      []string
			vipChecks []string
		)
		// VIP (BGP announce service) name defaults to service name with "-vip" appended
		var vipServiceName = service.Service + "-vip"
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
			case "gocast_consul_vip_service":
				vipServiceName = parts[1]
			case "gocast_consul_vip_check":
				vipChecks = append(vipChecks, parts[1])
			}
		}
		if vip == "" {
			glog.Errorf("No vip Tag found in matched service :%s", service.Service)
			continue
		}
		app, err := NewApp(service.Service, vip, vipConf, monitors, nats, "consul", vipServiceName, vipChecks)
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
	resp, err := c.client.Do(addr, "GET", nil)
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
	return false, fmt.Errorf("No local healcheck info found for service %s on node %s in consul", service, c.node)
}

// healthCheckRemote queries the consul cluster's healthcheck endpoint to perform service healthchecks
// This is the underlying api call: https://www.consul.io/api/health.html
func (c *ConsulMon) healthCheckRemote(service string) (bool, error) {
	addr := c.addr + fmt.Sprintf("%s/%s", remoteHealthCheckurl, service)
	resp, err := c.client.Do(addr, "GET", nil)
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
	return false, fmt.Errorf("No healcheck info found for node %s in consul", c.node)
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

// Register new vip health check after BRP announce
func (c *ConsulMon) RegisterVIPServiceCheck(name string, checks map[string]string) {
	if name == "" || checks == nil || len(checks) == 0 {
		glog.Info("No vip service check to be added")
		return
	}

	// Only TCP health check is supported. HTTP health check TBD if needed.
	if protocol, ok := checks["protocol"]; !ok || protocol != "tcp" {
		glog.Errorf("Invalid check protocol. Only TCP supported")
		return
	}

	// Use following as the default interval & timeout if not specified
	interval := "15s"
	timeout := "2s"
	if val, ok := checks["interval"]; ok {
		interval = val
	}
	if val, ok := checks["timeout"]; ok {
		timeout = val
	}
	portInt, _ := strconv.Atoi(checks["port"])

	type Check struct {
		DeregisterCriticalServiceAfter string `json:"DeregisterCriticalServiceAfter"`
		TCP                            string `json:"TCP"`
		Interval                       string `json:"Interval"`
		Timeout                        string `json:"Timeout"`
	}
	type Payload struct {
		ID    string `json:"ID"`
		Name  string `json:"Name"`
		Port  int    `json:"Port"`
		Check Check  `json:"Check"`
	}

	data := &Payload{
		ID:   name + "-" + c.node,
		Name: name,
		Port: portInt,
		Check: Check{
			DeregisterCriticalServiceAfter: "10m",
			TCP:                            "localhost:" + checks["port"],
			Interval:                       interval,
			Timeout:                        timeout,
		},
	}

	body, _ := json.Marshal(&data)
	if _, err := c.client.Do("http://127.0.0.1:8500/v1/agent/service/register?replace-existing-checks=true", "PUT", bytes.NewBuffer(body)); err != nil {
		glog.Errorf("HTTP PUT failed while registering VIP service: %v", err)
	}
	glog.V(2).Infof("Registered VIP service check to consul: %s", name+"-"+c.node)

}

// Deregister vip health check before BGP withdraw
func (c *ConsulMon) DeregisterVIPServiceCheck(name string, checks map[string]string) {
	if name == "" || checks == nil || len(checks) == 0 {
		glog.Infof("No vip service check to be removed")
		return
	}

	// Service Deregister
	svcId := name + "-" + c.node
	glog.V(4).Infof("Service Dreg URL: http://127.0.0.1:8500/v1/agent/service/deregister/%s", svcId)
	if _, err := c.client.Do("http://127.0.0.1:8500/v1/agent/service/deregister/"+svcId, "PUT", nil); err != nil {
		glog.Errorf("HTTP PUT failed while deregistering VIP service: %v", err)
	} else {
		glog.V(2).Infof("De-registered VIP service check: %s", svcId)
	}

	// Catalog Deregister
	type Payload struct {
		Node      string `json:"Node"`
		ServiceID string `json:"ServiceID"`
	}
	data := &Payload{
		Node:      c.node,
		ServiceID: svcId,
	}
	body, _ := json.Marshal(&data)
	glog.V(4).Infof("Catalog Dreg URL: http://127.0.0.1:8500/v1/catalog/deregister, Body: %v", bytes.NewBuffer(body))
	if _, err := c.client.Do("http://127.0.0.1:8500/v1/catalog/deregister", "PUT", bytes.NewBuffer(body)); err != nil {
		glog.Errorf("HTTP PUT failed during catalog deregister VIP service: %v", err)
	} else {
		glog.V(2).Infof("Catalog De-registered VIP service check: %s", svcId)
	}
}
