package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang/glog"

	"github.com/mayuresh82/gocast/config"
)

const (
	defaultNomadAddr = "http://127.0.0.1:4646"
	nomadUnixAddrEnv = "NOMAD_UNIX_ADDR"
	nomadTokenEnv    = "NOMAD_TOKEN"

	nomadAgentSelfUrl   = "/v1/agent/self"
	nomadServiceListUrl = "/v1/services"
	nomadServiceUrl     = "/v1/service/%s"
)

type NomadMonitor struct {
	addr      string
	namespace string
	node      string
	client    Clienter
}

type nomadClient struct {
	token  string
	client Clienter
}

func newNomadClient(addr, token string) (*nomadClient, string) {
	c := &nomadClient{
		token: token,
		client: &http.Client{
			Timeout: monitorTimeout,
		},
	}

	if unixPath, found := strings.CutPrefix(addr, "unix://"); found {
		addr = "http://localhost"
		c.client.(*http.Client).Transport = &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", unixPath)
			},
		}
	}

	return c, addr
}

func (c *nomadClient) Do(req *http.Request) (resp *http.Response, err error) {
	if c.token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	}

	resp, err = c.client.Do(req)
	if err != nil {
		return
	}

	if resp.StatusCode != http.StatusOK {
		return resp, fmt.Errorf("nomad API returned status code %d", resp.StatusCode)
	}

	return
}

func NewNomadMonitor(addr, namespace, nodeID, token string) (*NomadMonitor, error) {
	if addr == "" {
		addr = defaultNomadAddr
		if unixAddr := os.Getenv(nomadUnixAddrEnv); unixAddr != "" {
			addr = unixAddr
			glog.Infof("detected Nomad environment, using unix socket at: %s", addr)
			if token == "" {
				glog.Infof("using nomad token from environment")
				token = os.Getenv(nomadTokenEnv)
			}
			if token == "" {
				return nil, fmt.Errorf("missing token value, the Task API will not work")
			}
		}
	}
	client, addr := newNomadClient(addr, token)
	n := &NomadMonitor{addr: addr, namespace: namespace, client: client}

	if nodeID == "" {
		u := fmt.Sprintf("%s%s", n.addr, nomadAgentSelfUrl)
		glog.V(4).Infof("querying nomad agent for node_id at %s", u)
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := n.client.Do(req)
		if err != nil {
			return nil, err
		}

		var selfData struct {
			Stats struct {
				Client struct {
					NodeId string `json:"node_id"`
				} `json:"client"`
			} `json:"stats"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&selfData); err != nil {
			return nil, fmt.Errorf("failed to decode nomad data: %v", err)
		}
		n.node = selfData.Stats.Client.NodeId
		if n.node == "" {
			return nil, fmt.Errorf("unable to identify a node ID")
		}
		glog.Infof("got node ID %q", n.node)
	} else {
		n.node = nodeID
	}

	return n, nil
}

func (m *NomadMonitor) Monitor(mm *MonitorMgr) {
	for {
		apps, err := m.queryServices()
		if err != nil {
			glog.Errorf("Failed to query nomad: %v", err)
		} else {
			for _, app := range apps {
				mm.Add(app)
			}
			// remove currently running apps that are not discovered in this pass
			var toRemove []string
			mm.monMu.Lock()
			for name, mon := range mm.monitors {
				if mon.app.Source != nomadAppSource {
					continue
				}
				var found bool
				for _, app := range apps {
					if name == app.Name {
						found = true
						break
					}
				}
				if !found {
					glog.V(2).Infof("Removing app: %s as it was not found in nomad", name)
					toRemove = append(toRemove, name)
				}
			}
			mm.monMu.Unlock()
			for _, tr := range toRemove {
				mm.Remove(tr)
			}
		}
		<-time.After(mm.config.Agent.Nomad.QueryInterval)
	}
}

type nomadMiniServiceInfo struct {
	Name string `json:"ServiceName"`
	Tags []string
}

func (m *NomadMonitor) queryServices() ([]*App, error) {
	var apps []*App

	u := fmt.Sprintf("%s%s?namespace=%s", m.addr, nomadServiceListUrl, url.QueryEscape(m.namespace))
	glog.V(4).Infof("querying nomad services at %s", u)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	//goland:noinspection GoUnhandledErrorResult
	defer resp.Body.Close()
	var nomadData []struct {
		Namespace string
		Services  []nomadMiniServiceInfo
	}
	if err := json.NewDecoder(resp.Body).Decode(&nomadData); err != nil {
		return apps, fmt.Errorf("failed to decode nomad data: %v", err)
	}
	glog.V(5).Infof("got nomad data: %+v", nomadData)
	for _, nsBlock := range nomadData {
		for _, service := range nsBlock.Services {
			if !contains(service.Tags, matchTag) {
				continue
			}

			app, err := m.serviceToApp(service, nsBlock.Namespace)
			if err != nil {
				glog.Errorf("unable to add nomad app: %v", err)
				continue
			}
			if app == nil {
				continue
			}

			apps = append(apps, app)
		}
	}

	return apps, nil
}

func (m *NomadMonitor) serviceToApp(s nomadMiniServiceInfo, ns string) (*App, error) {
	var (
		vip      string
		monitors []string
		nats     []string
	)
	u := fmt.Sprintf(
		"%s%s?namespace=%s&filter=%s",
		m.addr,
		fmt.Sprintf(nomadServiceUrl, url.PathEscape(s.Name)),
		url.QueryEscape(ns),
		url.QueryEscape(fmt.Sprintf("NodeID == \"%s\"", m.node)),
	)
	glog.V(4).Infof("Querying nomad service %s at %s", s.Name, u)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	//goland:noinspection GoUnhandledErrorResult
	defer resp.Body.Close()

	var services []struct {
		Name    string   `json:"ServiceName"`
		Tags    []string `json:"Tags"`
		Address string   `json:"Address"`
		Port    int      `json:"Port"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return nil, err
	}
	if len(services) < 1 {
		return nil, nil
	}
	service := services[0]

	glog.V(3).Infof("found service %s (ns: %s) for this node", service.Name, ns)

	var vipConf config.VipConfig
	for _, tag := range service.Tags {
		// try to find the required tags. Only vip is mandatory
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
		return nil, fmt.Errorf("no \"gocast_vip\" tag found in matched service: %s", s.Name)
	}

	endpointIP := net.ParseIP(service.Address)

	for i := range nats {
		parts := strings.Split(nats[i], ":")
		switch len(parts) {
		case 4:
			continue
		case 3:
			// format -> protocol:vipPort:servicePort
			nats[i] = fmt.Sprintf("%s:%s:%s:%s", parts[0], parts[1], endpointIP, parts[2])
		case 2:
			// format -> protocol:combinedPort
			nats[i] = fmt.Sprintf("%s:%s:%s:%s", parts[0], parts[1], endpointIP, parts[1])
		default:
			return nil, fmt.Errorf("invalid NAT rule: %s", nats[i])
		}
	}

	app, err := NewApp(fmt.Sprintf("%s@%s", service.Name, ns), vip, vipConf, monitors, nats, nomadAppSource)
	if err != nil {
		return nil, err
	}

	return app, nil
}
