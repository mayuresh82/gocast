package controller

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	"github.com/mayuresh82/gocast/config"
	"github.com/stretchr/testify/assert"
)

var mockConsulData = map[string]string{
	"single-app": `{"Services": {
		"test-app-1": {
			"ID": "test-app-1",
			"Service": "test-service",
			"Tags": [
				"enable_gocast", "gocast_vip=1.1.1.1/32", "gocast_monitor=consul", "gocast_vip_communities=111:222,333:444"
			]
		}
	}}`,
	"single-app-no-match": `{"Services": {
		"test-app-1": {
			"ID": "test-app-1",
			"Service": "test-service",
			"Tags": [
				"foo"
			]
		}
	}}`,
	"single-app-no-vip": `{"Services": {
		"test-app-1": {
			"ID": "test-app-1",
			"Service": "test-service",
			"Tags": [
				"enable_gocast", "gocast_monitor=consul"
			]
		}
	}}`,
}

var mockConsulCheckData = map[string]string{
	"remote-pass": `[
		{
		"Node": "test-node1",
		"Status": "passing",
		"ServiceName": "test-service"
		},
		{
		"Node": "test-node2",
		"Status": "passing",
		"ServiceName": "test-service"
		}
	]`,
	"remote-fail": `[
		{
		"Node": "test-node1",
		"Status": "failed",
		"ServiceName": "test-service"
		}
	]`,
	"local-pass": `{
  		"service:test-service": {
  		  "Node": "test-node1",
  		  "Status": "passing",
  		  "ServiceName": "test-service"
  		}
	}`,
	"local-fail": `{
  		"service:test-service": {
  		  "Node": "test-node1",
  		  "Status": "failed",
  		  "ServiceName": "test-service"
  		}
	}`,
}

type MockClient struct {
	do func(*http.Request) (*http.Response, error)
}

func (c *MockClient) Do(*http.Request) (*http.Response, error) {
	if c.do != nil {
		return c.do(&http.Request{})
	}
	return nil, nil
}

func TestGetNewHTTPReq(t *testing.T) {
	a := assert.New(t)

	// test with consul token from config file
	req, err := getHTTPReq("GET", "1.1.1.1", "3333-3333")
	if err != nil {
		t.Fatal(err)
	}
	a.Equal(req.Header.Get("X-Consul-Token"), "3333-3333")

	// test with consul token from env variable
	os.Setenv("CONSUL_TOKEN", "4444-4444")
	req, err = getHTTPReq("GET", "1.1.1.1", "3333-3333")
	os.Unsetenv("CONSUL_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	a.Equal(req.Header.Get("X-Consul-Token"), "4444-4444")

	// test without consul token
	req, err = getHTTPReq("GET", "1.1.1.1", "")
	if err != nil {
		t.Fatal(err)
	}
	a.Equal(req.Header.Get("X-Consul-Token"), "")
}

func TestQueryServices(t *testing.T) {
	a := assert.New(t)
	client := &MockClient{}
	cm := &ConsulMon{
		addr: "foo", node: "test", client: client,
	}

	// test valid app
	client.do = func(*http.Request) (*http.Response, error) {
		b := bytes.NewBuffer([]byte(mockConsulData["single-app"]))
		return &http.Response{Body: ioutil.NopCloser(b), StatusCode: http.StatusOK}, nil
	}
	apps, err := cm.queryServices()
	if err != nil {
		a.FailNow(err.Error())
	}
	a.Equal(1, len(apps))
	a.Equal([]string{"111:222", "333:444"}, apps[0].Vip.Communities)

	app, _ := NewApp("test-service", "1.1.1.1/32", config.VipConfig{}, []string{"consul"}, []string{}, "consul")
	a.True(app.Equal(apps[0]))

	// test no match
	client.do = func(*http.Request) (*http.Response, error) {
		b := bytes.NewBuffer([]byte(mockConsulData["single-app-no-match"]))
		return &http.Response{Body: ioutil.NopCloser(b), StatusCode: http.StatusOK}, nil
	}
	apps, err = cm.queryServices()
	if err != nil {
		a.FailNow(err.Error())
	}
	a.Equal(0, len(apps))

	// test missing vip
	client.do = func(*http.Request) (*http.Response, error) {
		b := bytes.NewBuffer([]byte(mockConsulData["single-app-no-vip"]))
		return &http.Response{Body: ioutil.NopCloser(b), StatusCode: http.StatusOK}, nil
	}
	apps, _ = cm.queryServices()
	a.Equal(0, len(apps))
}

func TestHealthCheck(t *testing.T) {
	a := assert.New(t)
	client := &MockClient{}
	cm := &ConsulMon{node: "test-node1", client: client}

	// test remote checks
	cm.addr = "http://remote/check"
	client.do = func(*http.Request) (*http.Response, error) {
		b := bytes.NewBuffer([]byte(mockConsulCheckData["remote-pass"]))
		return &http.Response{Body: ioutil.NopCloser(b), StatusCode: http.StatusOK}, nil
	}
	check, err := cm.healthCheck("test-service")
	if err != nil {
		a.FailNow(err.Error())
	}
	a.True(check)
	client.do = func(*http.Request) (*http.Response, error) {
		b := bytes.NewBuffer([]byte(mockConsulCheckData["remote-fail"]))
		return &http.Response{Body: ioutil.NopCloser(b), StatusCode: http.StatusOK}, nil
	}
	check, _ = cm.healthCheck("test-service")
	a.False(check)

	// test local checks
	cm.addr = "http://localhost/check"
	client.do = func(*http.Request) (*http.Response, error) {
		b := bytes.NewBuffer([]byte(mockConsulCheckData["local-pass"]))
		return &http.Response{Body: ioutil.NopCloser(b), StatusCode: http.StatusOK}, nil
	}
	check, _ = cm.healthCheck("test-service")
	if err != nil {
		a.FailNow(err.Error())
	}
	a.True(check)
	cm.addr = "http://127.0.0.1/check"
	client.do = func(*http.Request) (*http.Response, error) {
		b := bytes.NewBuffer([]byte(mockConsulCheckData["local-fail"]))
		return &http.Response{Body: ioutil.NopCloser(b), StatusCode: http.StatusOK}, nil
	}
	check, _ = cm.healthCheck("test-service")
	a.False(check)
}
