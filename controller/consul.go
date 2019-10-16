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
	consulNodeEnv  = "CONSUL_NODE"
	matchTag       = "enable_gocast"
	nodeUrl        = "/catalog/node"
	healthCheckurl = "/health/checks"
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
	addr := c.addr + fmt.Sprintf("%s/%s", nodeUrl, c.node)
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

func (c *ConsulMon) healthCheck(service string) (bool, error) {
	addr := c.addr + fmt.Sprintf("%s/%s", healthCheckurl, service)
	resp, err := http.Get(addr)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var data []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return false, err
	}
	for _, nodeInfo := range data {
		n := nodeInfo.(map[string]interface{})
		if n["Node"] == c.node {
			if n["Status"].(string) == "passing" {
				return true, nil
			} else {
				glog.V(2).Infof("Consul Healthcheck returned %s status", n["Status"].(string))
				return false, nil
			}
		}
	}
	return false, fmt.Errorf("No healcheck info found for node %s in consul", c.node)
}
