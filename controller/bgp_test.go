package controller

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/golang/protobuf/ptypes"
	"github.com/mayuresh82/gocast/config"
	api "github.com/osrg/gobgp/api"
	gobgp "github.com/osrg/gobgp/pkg/server"
	"github.com/stretchr/testify/assert"
)

type BgpListener struct {
	s          *gobgp.BgpServer
	recvdPaths chan string
}

// NewBgpListener starts a local BGP server for testing purposes
func NewBgpListener(localAS int) (*BgpListener, error) {
	s := gobgp.NewBgpServer()
	go s.Serve()
	if err := s.StartBgp(context.Background(), &api.StartBgpRequest{
		Global: &api.Global{
			As:       uint32(localAS),
			RouterId: "100.100.100.100",
		},
	}); err != nil {
		return nil, fmt.Errorf("Unable to start bgp: %v", err)
	}
	n := &BgpListener{s: s, recvdPaths: make(chan string)}
	err := s.MonitorTable(context.Background(), &api.MonitorTableRequest{TableType: api.TableType_ADJ_IN}, func(p *api.Path) {
		// assumes v4 only paths !
		var value ptypes.DynamicAny
		if err := ptypes.UnmarshalAny(p.Nlri, &value); err != nil {
			return
		}
		nlri := value.Message.(*api.IPAddressPrefix)
		n.recvdPaths <- fmt.Sprintf("%s/%d", nlri.Prefix, nlri.PrefixLen)
	})
	if err != nil {
		return nil, err
	}
	if err := s.AddPeer(context.Background(), &api.AddPeerRequest{
		Peer: &api.Peer{
			Conf: &api.PeerConf{
				NeighborAddress: "127.0.0.1",
				PeerAs:          11111,
			},
			Transport: &api.Transport{PassiveMode: true},
		},
	}); err != nil {
		return nil, err
	}
	return n, nil
}

func (l *BgpListener) Shutdown() error {
	if err := l.s.StopBgp(context.Background(), &api.StopBgpRequest{}); err != nil {
		return err
	}
	return nil
}

// This test tests the BGP controller talking to a local BGP
// listener. It needs a few seconds to pass and *may* time out
// if the test timeouts are very small. It also needs to be run as
// root (sudo)
// Disabling this test in CI currently due to https://github.com/osrg/gobgp/issues/2366
func TestBgpNew(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("Skipping testing in CI environment")
	}
	listener, err := NewBgpListener(22222)
	if err != nil {
		panic(err)
	}
	defer listener.Shutdown()
	a := assert.New(t)
	c := config.BgpConfig{
		LocalAS:     11111,
		PeerAS:      22222,
		PeerIP:      "127.0.0.1",
		LocalIP:     "192.168.1.100",
		Communities: []string{"100:100"},
		Origin:      "igp",
	}
	ctrl, err := NewController(c)
	if err != nil {
		a.FailNow(err.Error())
	}
	_, ipnet, _ := net.ParseCIDR("20.30.40.0/24")
	if err := ctrl.Announce(ipnet); err != nil {
		a.FailNow(err.Error())
	}

	path := <-listener.recvdPaths
	a.Equal("20.30.40.0/24", path)
	ctrl.Shutdown()
}
