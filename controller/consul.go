package controller

import (
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	consulNodeEnv        = "CONSUL_NODE"
	allowStale           = "CONSUL_STALE"
	matchTag             = "enable_gocast"
	remoteNodeURL        = "/catalog/node"
	localServicesURL     = "/agent/services"
	remoteHealthCheckurl = "/health/checks"
	localHealthCheckurl  = "/agent/checks"
)

type ConsulMon struct {
	addr   string
	client *http.Client
	node   string
}

// spec provided by https://www.consul.io/api/agent/service.html
type ConsulServiceData struct {
	ID      string
	Service string
	Tags    []string
}

// spec provided by https://www.consul.io/api/catalog.html
// Since the underlying structure of the json object returned via the catalog is
// identical to the structure from the agent, we can use the ConsulServiceData representation
type ConsulServicesData struct {
	Services map[string]ConsulServiceData
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
	return &ConsulMon{addr: addr, node: node, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

// generateApp generates an app from a given consul service data
func (c *ConsulServiceData) generateApp() (*App, error) {
	var (
		vip      string
		monitors []string
		nats     []string
	)

	for _, tag := range c.Tags {
		// try to find the required tags.
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
		// vip is mandatory, bail if it is absent
		glog.Errorf("No vip Tag found in matched service :%s", service.Service)
		return nil, err
	}

	app, err := NewApp(service.Service, vip, monitors, nats, "consul")
	if err != nil {
		glog.Errorf("Unable to generate consul app: %v", err)
		return nil, err
	}

	return app, err
}

// localQueryServices writes the requests node service data from the local agent and uses it to generate an app
func (c *ConsulMon) localQueryServices() ([]*App, error) {
	addr := c.addr + fmt.Sprintf("%s/%s", localNodeURL, c.node)
	resp, err := c.client.Get(addr)
	if err != nil {
		return apps, err
	}
	defer resp.Body.Close()

	var services map[string]ConsulServiceData
	var apps []*App
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return apps, fmt.Errorf("Unable to decode local consul data %v", err)
	}

	for _, service in range services {
        if !contains(service.Tags, matchTag) {
			continue
		}
		app, err := service.generateApp()
		if err != nil {
			glog.Errorf("Unable to add consul app: %v", err)
			continue
		}
		apps = append(apps, app)
	}

	return apps, nil
}

// remoteQueryServices writes the requests node service data from the catalog and uses it to generate an app
func (c *ConsulMon) remoteQueryServices() ([]*App, error) {
	// Retrieve data from remoteNodeURL
	// stale queries consume less resources
	var stale string
	if os.Getenv(allowStale) == "true" {
		stale = "stale"
	}
	addr := c.addr + fmt.Sprintf("%s/%s?%s", nodeURL, c.node, stale)

	resp, err := c.client.Get(addr)
	if err != nil {
		return apps, err
	}
	defer resp.Body.Close()

	// Parse data per ConsulServicesData specification
	var apps []*App // Declare app array here to short circuit, or populate it within the following block
	var consulData ConsulServicesData
	if err := json.NewDecoder(resp.Body).Decode(&consulData); err != nil {
		return apps, fmt.Errorf("Unable to decode consul data: %v", err)
	}

	for _, service := range consulData.Services {
		if !contains(service.Tags, matchTag) {
			continue
		}
		app, err := service.generateApp()
		if err != nil {
			glog.Errorf("Unable to add consul app: %v", err)
			continue
		}
		apps = append(apps, app)

	}
	return apps, nil
}

// queryServices queries the services from a local agent if the address is localhost.
// If using another address, it will presume it's a remote consul node, and call the node catalog directly
func (c *ConsulMon) queryServices() ([]*App, error) {
	usingLocalAgent := strings.Contains(c.addr, "localhost")
	if usingLocalAgent {
		return c.localQueryServices()
	}
	c.remoteQueryServices()
}

// healthCheckLocal queries a node's local consul agent to perform service healthchecks
// This is the underlying api call: https://www.consul.io/api/agent/check.html
func (c *ConsulMon) healthCheckLocal(service string) (bool, error) {
	params := url.Values{}
	params.Add("filter", fmt.Sprintf("%s in ServiceTags", matchTag))
	addr := c.addr + fmt.Sprintf("%s?%s", localHealthCheckurl, params.Encode())
	resp, err := c.client.Get(addr)
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
	resp, err := c.client.Get(addr)
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
	usingLocalAgent := strings.Contains(c.addr, "localhost")
	if usingLocalAgent {
		return c.healthCheckLocal(service)
	}
	return c.healthCheckRemote(service)
}
