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
	r := &Route{Net: ipnet}
	if err := ctrl.Announce(r); err != nil {
		a.FailNow(err.Error())
	}

	path := <-listener.recvdPaths
	a.Equal("20.30.40.0/24", path)
	ctrl.Shutdown()
}

// This test verifies that NewController and Announce work end-to-end when
// the peer is configured via PeerIPs instead of the legacy singular PeerIP.
// It has the same requirements/caveats as TestBgpNew above.
//
// NewBgpListener always binds its own BGP server to the default port
// (0.0.0.0:179), so a second concurrent listener process in the same test
// binary cannot be started to prove out two simultaneous live sessions here.
// The peer-list merging and de-duplication logic itself (which is what
// actually differs when more than one peer is configured) is covered
// separately, and directly, by TestPeerAddrs below.
func TestBgpNewMultiPeer(t *testing.T) {
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
		PeerIPs:     []string{"127.0.0.1"},
		LocalIP:     "192.168.1.100",
		Communities: []string{"100:100"},
		Origin:      "igp",
	}
	ctrl, err := NewController(c)
	if err != nil {
		a.FailNow(err.Error())
	}
	a.Equal(1, len(ctrl.peerIPs))

	_, ipnet, _ := net.ParseCIDR("20.30.40.0/24")
	r := &Route{Net: ipnet}
	if err := ctrl.Announce(r); err != nil {
		a.FailNow(err.Error())
	}
	path := <-listener.recvdPaths
	a.Equal("20.30.40.0/24", path)

	peers, err := ctrl.PeerInfo()
	if err != nil {
		a.FailNow(err.Error())
	}
	a.Equal(1, len(peers))
	ctrl.Shutdown()
}

func TestPeerAddrs(t *testing.T) {
	a := assert.New(t)
	// PeerIP alone still works
	a.Equal([]string{"10.0.0.1"}, peerAddrs(config.BgpConfig{PeerIP: "10.0.0.1"}))
	// PeerIPs alone
	a.Equal([]string{"10.0.0.1", "10.0.0.2"}, peerAddrs(config.BgpConfig{PeerIPs: []string{"10.0.0.1", "10.0.0.2"}}))
	// both set: PeerIP is folded into the list
	a.Equal([]string{"10.0.0.1", "10.0.0.2"}, peerAddrs(config.BgpConfig{PeerIP: "10.0.0.1", PeerIPs: []string{"10.0.0.1", "10.0.0.2"}}))
	// neither set
	a.Equal(0, len(peerAddrs(config.BgpConfig{})))
}

func TestNewControllerInvalidPeerIP(t *testing.T) {
	_, err := NewController(config.BgpConfig{PeerIPs: []string{"10.0.0.1", "not-an-ip"}})
	assert.EqualError(t, err, `Invalid peer ip: "not-an-ip"`)
}
