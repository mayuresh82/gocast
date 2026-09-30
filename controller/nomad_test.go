package controller

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mayuresh82/gocast/config"
)

var mockNomadData = map[string]struct {
	list    string
	service string
}{
	"single-app-default": {
		`[{
  "Namespace": "default",
  "Services": [{
      "ServiceName": "test-svc",
      "Tags": ["enable_gocast", "gocast_nat=tcp:443", "gocast_vip=1.1.1.1/32"]
    }]
}]`,
		`[{
  "ServiceName": "test-svc",
  "Tags": ["enable_gocast", "gocast_nat=tcp:443", "gocast_vip=1.1.1.1/32"],
  "NodeID": "test-node",
  "Address": "127.0.0.1",
  "Port": 12345
}]`,
	},
	"single-app-wrong-node": {
		`[{
  "Namespace": "default",
  "Services": [{
      "ServiceName": "test-svc",
      "Tags": ["enable_gocast", "gocast_nat=tcp:443", "gocast_vip=1.1.1.1/32"]
    }]
}]`,
		`[]`,
	},
	"single-app-no-vip": {
		`[{
  "Namespace": "default",
  "Services": [{
      "ServiceName": "test-svc",
      "Tags": ["enable_gocast"]
    }]
}]`,
		`[{
  "ServiceName": "test-svc",
  "Tags": ["enable_gocast"],
  "NodeID": "test-node",
  "Address": "127.0.0.1",
  "Port": 12345
}]`,
	},
}

func makeNomadDoMethod(value struct{ list, service string }, status int) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		var b *bytes.Buffer
		if r.URL.Path == "/v1/services" {
			b = bytes.NewBuffer([]byte(value.list))
		} else {
			b = bytes.NewBuffer([]byte(value.service))
		}
		return &http.Response{Body: ioutil.NopCloser(b), StatusCode: status}, nil
	}
}

func TestNewNomadMonitor(t *testing.T) {
	a := assert.New(t)

	var mon *NomadMonitor
	var err error

	mon, err = NewNomadMonitor("http://test", "*", "1", "")
	a.NotNil(mon)
	a.NoError(err)

	oldEnv, hadOldEnv := os.LookupEnv(nomadUnixAddrEnv)

	a.NoError(os.Setenv(nomadUnixAddrEnv, ""))
	mon, err = NewNomadMonitor("", "*", "1", "")
	a.NotNil(mon)
	a.NoError(err)
	a.Equal(defaultNomadAddr, mon.addr)

	a.NoError(os.Setenv(nomadUnixAddrEnv, "unix:///non-existing-dir/api.sock"))
	mon, err = NewNomadMonitor("", "*", "1", "1")
	a.NotNil(mon)
	a.NoError(err)
	a.Equal("http://localhost", mon.addr)

	mon, err = NewNomadMonitor("", "*", "1", "")
	a.Nil(mon)
	a.Error(err)

	if hadOldEnv {
		a.NoError(os.Setenv(nomadUnixAddrEnv, oldEnv))
	} else {
		a.NoError(os.Unsetenv(nomadUnixAddrEnv))
	}
}

func TestNomadQueryServices(t *testing.T) {
	a := assert.New(t)
	client := &MockClient{}
	nomadMonitor := &NomadMonitor{
		addr:      "http://nomad-server.localhost",
		node:      "test-node",
		namespace: "default",
		client:    client,
	}

	// test valid app
	client.do = makeNomadDoMethod(mockNomadData["single-app-default"], 200)

	apps, err := nomadMonitor.queryServices()
	if err != nil {
		a.FailNow(err.Error())
	}
	a.Len(apps, 1)

	app, err := NewApp("test-svc@default", "1.1.1.1/32", config.VipConfig{}, []string{}, []string{"tcp:443"}, nomadAppSource)
	if err != nil {
		a.FailNow(err.Error())
	}
	a.NotNil(app)
	a.True(app.Equal(apps[0]))

	// test svc with filtered result (represents a gocast-enabled service on a different node)
	client.do = makeNomadDoMethod(mockNomadData["single-app-wrong-node"], 200)

	apps, err = nomadMonitor.queryServices()
	if err != nil {
		a.FailNow(err.Error())
	}
	a.Len(apps, 0)

	// test svc with filtered result
	client.do = makeNomadDoMethod(mockNomadData["single-app-no-vip"], 200)

	fmt.Print("!!!!! Expect an error message on the next line\n")
	apps, err = nomadMonitor.queryServices()
	if err != nil {
		a.FailNow(err.Error())
	}
	a.Len(apps, 0)

}

func TestNewNomadClient(t *testing.T) {
	a := assert.New(t)

	var client *nomadClient
	var returnedAddr string

	client, returnedAddr = newNomadClient("http://normal-ip", "")
	a.NotNil(client)
	a.Equal("http://normal-ip", returnedAddr)

	client, returnedAddr = newNomadClient("unix:///does-not-exist", "")
	a.NotNil(client)
	a.Equal("http://localhost", returnedAddr)
}
