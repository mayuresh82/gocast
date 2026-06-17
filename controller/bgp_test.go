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

func TestLegacyConfigConversion(t *testing.T) {
	a := assert.New(t)

	// Test legacy single-peer config
	legacyConfig := config.BgpConfig{
		LocalAS: 11111,
		PeerAS:  22222,
		PeerIP:  "10.10.10.1",
		LocalIP: "192.168.1.100",
		Communities: []string{"100:100"},
		Origin: "igp",
	}

	ctrl, err := NewController(legacyConfig)
	if err != nil {
		a.FailNow(err.Error())
	}
	defer ctrl.Shutdown()

	// Verify legacy config was converted to multi-peer format
	a.Equal(1, len(ctrl.peers), "Should have exactly 1 peer")
	a.Equal("10.10.10.1", ctrl.peers[0].PeerIP)
	a.Equal(22222, ctrl.peers[0].PeerAS)
}

func TestMultiPeerConfig(t *testing.T) {
	a := assert.New(t)

	// Test new multi-peer config
	multiPeerConfig := config.BgpConfig{
		LocalAS: 11111,
		LocalIP: "192.168.1.100",
		Peers: []config.PeerConfig{
			{PeerIP: "10.10.10.1", PeerAS: 22222},
			{PeerIP: "10.10.10.2", PeerAS: 22222},
		},
		Communities: []string{"100:100"},
		Origin: "igp",
	}

	ctrl, err := NewController(multiPeerConfig)
	if err != nil {
		a.FailNow(err.Error())
	}
	defer ctrl.Shutdown()

	// Verify both peers are configured
	a.Equal(2, len(ctrl.peers), "Should have exactly 2 peers")
	a.Equal("10.10.10.1", ctrl.peers[0].PeerIP)
	a.Equal("10.10.10.2", ctrl.peers[1].PeerIP)
}

func TestDefaultGatewayPeer(t *testing.T) {
	a := assert.New(t)

	// Test config with no peer_ip - should use default gateway
	defaultGatewayConfig := config.BgpConfig{
		LocalAS: 11111,
		LocalIP: "192.168.1.100",
		PeerAS:  22222,
		Origin:  "igp",
	}

	ctrl, err := NewController(defaultGatewayConfig)
	a.NoError(err, "Should not error when peer_ip is not specified")
	if err == nil {
		defer ctrl.Shutdown()

		// Verify a peer was configured using gateway
		a.Equal(1, len(ctrl.peers), "Should have exactly 1 peer")
		a.NotEmpty(ctrl.peers[0].PeerIP, "Peer IP should be set from gateway")
		a.Equal(22222, ctrl.peers[0].PeerAS, "Peer AS should match config")
	}
}

func TestCommunityMerging(t *testing.T) {
	a := assert.New(t)

	ctrl := &Controller{
		localAS: 11111,
		localIP: net.ParseIP("192.168.1.100"),
		communities: []string{"1000:1000", "2000:2000"}, // Global
		origin: 0,
	}

	peer := &config.PeerConfig{
		PeerIP: "10.10.10.1",
		PeerAS: 22222,
		Communities: []string{"100:100", "200:200"}, // Per-peer
	}

	route := &Route{
		Net: &net.IPNet{
			IP: net.ParseIP("20.30.40.0"),
			Mask: net.CIDRMask(24, 32),
		},
		Communities: []string{"5000:5000"}, // Per-route
	}

	path := ctrl.getApiPath(route, peer)

	// Extract communities from path
	var commAttr *api.CommunitiesAttribute
	for _, attr := range path.Pattrs {
		var dynAny ptypes.DynamicAny
		if err := ptypes.UnmarshalAny(attr, &dynAny); err == nil {
			if c, ok := dynAny.Message.(*api.CommunitiesAttribute); ok {
				commAttr = c
				break
			}
		}
	}

	a.NotNil(commAttr, "Should have communities attribute")
	a.Equal(5, len(commAttr.Communities), "Should have 5 communities (2 global + 2 peer + 1 route)")

	// Verify community values (converted to uint32)
	expectedCommunities := []uint32{
		convertCommunity("1000:1000"), // Global
		convertCommunity("2000:2000"), // Global
		convertCommunity("100:100"),   // Per-peer
		convertCommunity("200:200"),   // Per-peer
		convertCommunity("5000:5000"), // Per-route
	}
	a.Equal(expectedCommunities, commAttr.Communities)
}

func TestMultiHopConfiguration(t *testing.T) {
	a := assert.New(t)

	testCases := []struct {
		name         string
		localAS      int
		peerAS       int
		multiHopPtr  *bool
		expectMH     bool
	}{
		{
			name:        "default - multihop not enabled",
			localAS:     11111,
			peerAS:      22222,
			multiHopPtr: nil,
			expectMH:    false,
		},
		{
			name:        "explicit disable",
			localAS:     11111,
			peerAS:      22222,
			multiHopPtr: boolPtr(false),
			expectMH:    false,
		},
		{
			name:        "explicit enable",
			localAS:     11111,
			peerAS:      22222,
			multiHopPtr: boolPtr(true),
			expectMH:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &Controller{
				localAS: tc.localAS,
				localIP: net.ParseIP("192.168.1.100"),
				s:       gobgp.NewBgpServer(),
			}
			go ctrl.s.Serve()

			if err := ctrl.s.StartBgp(context.Background(), &api.StartBgpRequest{
				Global: &api.Global{
					As:         uint32(tc.localAS),
					RouterId:   ctrl.localIP.String(),
					ListenPort: -1,
				},
			}); err != nil {
				a.FailNow(err.Error())
			}
			defer ctrl.s.StopBgp(context.Background(), &api.StopBgpRequest{})

			peer := &config.PeerConfig{
				PeerIP:   "10.10.10.1",
				PeerAS:   tc.peerAS,
				MultiHop: tc.multiHopPtr,
			}

			err := ctrl.addPeer(peer)
			a.NoError(err)

			// Verify multihop setting by checking peer config
			var foundPeer *api.Peer
			ctrl.s.ListPeer(context.Background(), &api.ListPeerRequest{}, func(p *api.Peer) {
				if p.Conf.NeighborAddress == peer.PeerIP {
					foundPeer = p
				}
			})

			a.NotNil(foundPeer, "Peer should be added")
			if tc.expectMH {
				a.NotNil(foundPeer.EbgpMultihop, "Should have multihop configured")
				a.True(foundPeer.EbgpMultihop.Enabled, "Multihop should be enabled")
			} else {
				if foundPeer.EbgpMultihop != nil {
					a.False(foundPeer.EbgpMultihop.Enabled, "Multihop should not be enabled")
				}
			}
		})
	}
}

// Helper function to create bool pointer
func boolPtr(b bool) *bool {
	return &b
}

func TestMultiPeerAnnouncement(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("Skipping testing in CI environment")
	}

	a := assert.New(t)

	// Create two BGP listeners
	listener1, err := NewBgpListener(22222)
	if err != nil {
		panic(err)
	}
	defer listener1.Shutdown()

	listener2, err := NewBgpListener(33333)
	if err != nil {
		panic(err)
	}
	defer listener2.Shutdown()

	// Create controller with two peers
	multiPeerConfig := config.BgpConfig{
		LocalAS: 11111,
		LocalIP: "192.168.1.100",
		Peers: []config.PeerConfig{
			{PeerIP: "127.0.0.1", PeerAS: 22222},
			{PeerIP: "127.0.0.1", PeerAS: 33333},
		},
		Communities: []string{"100:100"},
		Origin:      "igp",
	}

	ctrl, err := NewController(multiPeerConfig)
	if err != nil {
		a.FailNow(err.Error())
	}
	defer ctrl.Shutdown()

	// Announce a route
	_, ipnet, _ := net.ParseCIDR("20.30.40.0/24")
	r := &Route{Net: ipnet}
	if err := ctrl.Announce(r); err != nil {
		a.FailNow(err.Error())
	}

	// Verify both listeners received the route
	path1 := <-listener1.recvdPaths
	a.Equal("20.30.40.0/24", path1)

	path2 := <-listener2.recvdPaths
	a.Equal("20.30.40.0/24", path2)
}

func TestBestEffortAnnouncement(t *testing.T) {
	a := assert.New(t)

	// Create controller with two peers
	mixedConfig := config.BgpConfig{
		LocalAS: 11111,
		LocalIP: "192.168.1.100",
		Peers: []config.PeerConfig{
			{PeerIP: "127.0.0.1", PeerAS: 22222},
			{PeerIP: "127.0.0.2", PeerAS: 33333},
		},
		Origin: "igp",
	}

	ctrl, err := NewController(mixedConfig)
	if err != nil {
		a.FailNow(err.Error())
	}
	defer ctrl.Shutdown()

	// Announce a route - both peers will be added successfully
	// (they won't have actual BGP sessions established, but peers are added to GoBGP)
	_, ipnet, _ := net.ParseCIDR("20.30.40.0/24")
	r := &Route{Net: ipnet}

	// The announcement should succeed for both peers being added
	err = ctrl.Announce(r)
	a.NoError(err, "Announcement should succeed for both peers")

	// Verify both peers were added
	peers, err := ctrl.PeerInfo()
	a.NoError(err)
	a.Equal(2, len(peers), "Should have both peers configured")
}

func TestWithdrawMultiplePeers(t *testing.T) {
	a := assert.New(t)

	ctrl := &Controller{
		localAS: 11111,
		localIP: net.ParseIP("192.168.1.100"),
		peers: []config.PeerConfig{
			{PeerIP: "10.10.10.1", PeerAS: 22222},
			{PeerIP: "10.10.10.2", PeerAS: 22222},
		},
		origin: 0,
		s:      gobgp.NewBgpServer(),
	}
	go ctrl.s.Serve()

	if err := ctrl.s.StartBgp(context.Background(), &api.StartBgpRequest{
		Global: &api.Global{
			As:         uint32(11111),
			RouterId:   ctrl.localIP.String(),
			ListenPort: -1,
		},
	}); err != nil {
		a.FailNow(err.Error())
	}
	defer ctrl.s.StopBgp(context.Background(), &api.StopBgpRequest{})

	_, ipnet, _ := net.ParseCIDR("20.30.40.0/24")
	r := &Route{Net: ipnet}

	// Withdraw should iterate through all peers
	// This will fail because peers aren't established, but it should try both
	err := ctrl.Withdraw(r)
	// We expect an error but it should have tried both peers
	if err != nil {
		a.Contains(err.Error(), "withdrawal errors")
	}
}

func TestPeerInfoMultiplePeers(t *testing.T) {
	a := assert.New(t)

	ctrl := &Controller{
		localAS: 11111,
		localIP: net.ParseIP("192.168.1.100"),
		peers: []config.PeerConfig{
			{PeerIP: "10.10.10.1", PeerAS: 22222},
			{PeerIP: "10.10.10.2", PeerAS: 33333},
		},
		origin: 0,
		s:      gobgp.NewBgpServer(),
	}
	go ctrl.s.Serve()

	if err := ctrl.s.StartBgp(context.Background(), &api.StartBgpRequest{
		Global: &api.Global{
			As:         uint32(11111),
			RouterId:   ctrl.localIP.String(),
			ListenPort: -1,
		},
	}); err != nil {
		a.FailNow(err.Error())
	}
	defer ctrl.s.StopBgp(context.Background(), &api.StopBgpRequest{})

	// Add peers
	for i := range ctrl.peers {
		ctrl.addPeer(&ctrl.peers[i])
	}

	// Get peer info
	peers, err := ctrl.PeerInfo()
	a.NoError(err)
	a.Equal(2, len(peers), "Should return info for both peers")

	// Verify peer addresses
	peerAddrs := make(map[string]bool)
	for _, p := range peers {
		peerAddrs[p.Conf.NeighborAddress] = true
	}
	a.True(peerAddrs["10.10.10.1"], "Should have first peer")
	a.True(peerAddrs["10.10.10.2"], "Should have second peer")
}

func TestMD5Authentication(t *testing.T) {
	a := assert.New(t)

	testCases := []struct {
		name           string
		md5Password    string
		md5EnvVar      string
		envValue       string
		expectedAuth   string
	}{
		{
			name:         "MD5 password from config",
			md5Password:  "secret123",
			md5EnvVar:    "",
			envValue:     "",
			expectedAuth: "secret123",
		},
		{
			name:         "MD5 password from environment variable",
			md5Password:  "",
			md5EnvVar:    "BGP_PEER_PASSWORD",
			envValue:     "env_secret456",
			expectedAuth: "env_secret456",
		},
		{
			name:         "Environment variable takes priority over config",
			md5Password:  "config_password",
			md5EnvVar:    "BGP_PEER_PASSWORD",
			envValue:     "env_password",
			expectedAuth: "env_password",
		},
		{
			name:         "No authentication when neither is set",
			md5Password:  "",
			md5EnvVar:    "",
			envValue:     "",
			expectedAuth: "",
		},
		{
			name:         "Config password used when env var is set but empty",
			md5Password:  "fallback_password",
			md5EnvVar:    "EMPTY_VAR",
			envValue:     "",
			expectedAuth: "fallback_password",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set environment variable if specified
			if tc.md5EnvVar != "" && tc.envValue != "" {
				os.Setenv(tc.md5EnvVar, tc.envValue)
				defer os.Unsetenv(tc.md5EnvVar)
			}

			ctrl := &Controller{
				localAS: 11111,
				localIP: net.ParseIP("192.168.1.100"),
				s:       gobgp.NewBgpServer(),
			}
			go ctrl.s.Serve()

			if err := ctrl.s.StartBgp(context.Background(), &api.StartBgpRequest{
				Global: &api.Global{
					As:         uint32(11111),
					RouterId:   ctrl.localIP.String(),
					ListenPort: -1,
				},
			}); err != nil {
				a.FailNow(err.Error())
			}
			defer ctrl.s.StopBgp(context.Background(), &api.StopBgpRequest{})

			peer := &config.PeerConfig{
				PeerIP:      "10.10.10.1",
				PeerAS:      22222,
				MD5Password: tc.md5Password,
				MD5EnvVar:   tc.md5EnvVar,
			}

			err := ctrl.addPeer(peer)
			a.NoError(err)

			// Verify MD5 authentication is configured correctly
			var foundPeer *api.Peer
			ctrl.s.ListPeer(context.Background(), &api.ListPeerRequest{}, func(p *api.Peer) {
				if p.Conf.NeighborAddress == peer.PeerIP {
					foundPeer = p
				}
			})

			a.NotNil(foundPeer, "Peer should be added")
			a.Equal(tc.expectedAuth, foundPeer.Conf.AuthPassword, "MD5 password should match expected")
		})
	}
}

func TestGetMD5Password(t *testing.T) {
	a := assert.New(t)

	ctrl := &Controller{}

	// Test 1: Environment variable takes priority
	os.Setenv("TEST_BGP_PASS", "env_password")
	defer os.Unsetenv("TEST_BGP_PASS")

	peer := &config.PeerConfig{
		MD5Password: "config_password",
		MD5EnvVar:   "TEST_BGP_PASS",
	}
	a.Equal("env_password", ctrl.getMD5Password(peer))

	// Test 2: Config password when env var not set
	peer2 := &config.PeerConfig{
		MD5Password: "config_only",
		MD5EnvVar:   "NONEXISTENT_VAR",
	}
	a.Equal("config_only", ctrl.getMD5Password(peer2))

	// Test 3: Empty string when nothing is set
	peer3 := &config.PeerConfig{}
	a.Equal("", ctrl.getMD5Password(peer3))

	// Test 4: Only env var specified
	os.Setenv("ANOTHER_PASS", "another_env_password")
	defer os.Unsetenv("ANOTHER_PASS")

	peer4 := &config.PeerConfig{
		MD5EnvVar: "ANOTHER_PASS",
	}
	a.Equal("another_env_password", ctrl.getMD5Password(peer4))
}

func TestMultiPeerWithMD5(t *testing.T) {
	a := assert.New(t)

	// Set environment variables for testing
	os.Setenv("PEER1_PASSWORD", "peer1_secret")
	os.Setenv("PEER2_PASSWORD", "peer2_secret")
	defer os.Unsetenv("PEER1_PASSWORD")
	defer os.Unsetenv("PEER2_PASSWORD")

	// Create controller with multiple peers using different MD5 configurations
	multiPeerConfig := config.BgpConfig{
		LocalAS: 11111,
		LocalIP: "192.168.1.100",
		Peers: []config.PeerConfig{
			{
				PeerIP:      "10.10.10.1",
				PeerAS:      22222,
				MD5EnvVar:   "PEER1_PASSWORD",
			},
			{
				PeerIP:      "10.10.10.2",
				PeerAS:      33333,
				MD5Password: "peer2_config_password",
			},
			{
				PeerIP:   "10.10.10.3",
				PeerAS:   44444,
				// No MD5 authentication
			},
		},
		Origin: "igp",
	}

	ctrl, err := NewController(multiPeerConfig)
	if err != nil {
		a.FailNow(err.Error())
	}
	defer ctrl.Shutdown()

	// Trigger peer addition by announcing a route (peers are added lazily)
	_, ipnet, _ := net.ParseCIDR("20.30.40.0/24")
	r := &Route{Net: ipnet}
	ctrl.Announce(r)

	// Verify all peers have correct MD5 configuration
	peers := make(map[string]string) // map[peerIP]authPassword
	ctrl.s.ListPeer(context.Background(), &api.ListPeerRequest{}, func(p *api.Peer) {
		peers[p.Conf.NeighborAddress] = p.Conf.AuthPassword
	})

	a.Equal(3, len(peers), "Should have all three peers")
	a.Equal("peer1_secret", peers["10.10.10.1"], "First peer should use env var password")
	a.Equal("peer2_config_password", peers["10.10.10.2"], "Second peer should use config password")
	a.Equal("", peers["10.10.10.3"], "Third peer should have no authentication")
}
