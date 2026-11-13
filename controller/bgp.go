package controller

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/any"
	c "github.com/mayuresh82/gocast/config"
	api "github.com/osrg/gobgp/api"
	gobgp "github.com/osrg/gobgp/pkg/server"
)

type Route struct {
	Net         *net.IPNet
	Communities []string
}

type Controller struct {
	peerAS      int
	localIP     net.IP
	peerIPs     []net.IP
	communities []string
	origin      uint32
	multiHop    bool
	s           *gobgp.BgpServer
}

// peerAddrs collects the configured peer IPs from both the legacy singular
// PeerIP field and the PeerIPs list, de-duplicated and in a stable order.
func peerAddrs(config c.BgpConfig) []string {
	var addrs []string
	seen := make(map[string]bool)
	add := func(addr string) {
		if addr == "" || seen[addr] {
			return
		}
		seen[addr] = true
		addrs = append(addrs, addr)
	}
	add(config.PeerIP)
	for _, addr := range config.PeerIPs {
		add(addr)
	}
	return addrs
}

func NewController(config c.BgpConfig) (*Controller, error) {
	c := &Controller{}
	addrs := peerAddrs(config)
	if len(addrs) == 0 {
		gw, err := gateway()
		if err != nil {
			return nil, fmt.Errorf("Unable to get gw ip: %v", err)
		}
		c.peerIPs = []net.IP{gw}
	} else {
		for _, addr := range addrs {
			ip := net.ParseIP(addr)
			if ip == nil {
				return nil, fmt.Errorf("Invalid peer ip: %q", addr)
			}
			c.peerIPs = append(c.peerIPs, ip)
		}
	}
	if config.LocalIP == "" {
		// derive the local address from the route to the first configured
		// peer. When peering with multiple route servers reachable via
		// different local interfaces, set local_ip explicitly instead.
		gw, err := via(c.peerIPs[0])
		if err != nil {
			return nil, fmt.Errorf("Unable to get gw ip: %v", err)
		}
		localIP, err := localAddress(gw)
		if err != nil {
			return nil, err
		}
		c.localIP = localIP
	} else {
		c.localIP = net.ParseIP(config.LocalIP)
	}
	c.communities = config.Communities
	switch config.Origin {
	case "igp":
		c.origin = 0
	case "egp":
		c.origin = 1
	case "unknown":
		c.origin = 2
	}
	s := gobgp.NewBgpServer()
	go s.Serve()
	if err := s.StartBgp(context.Background(), &api.StartBgpRequest{
		Global: &api.Global{
			As:         uint32(config.LocalAS),
			RouterId:   c.localIP.String(),
			ListenPort: -1, // gobgp won't listen on tcp:179
		},
	}); err != nil {
		return nil, fmt.Errorf("Unable to start bgp: %v", err)
	}
	c.s = s
	c.peerAS = config.PeerAS
	// set mh by default for all ebgp peers
	if c.peerAS != config.LocalAS {
		c.multiHop = true
	}
	return c, nil
}

func (c *Controller) AddPeer(peer string) error {
	n := &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: peer,
			PeerAs:          uint32(c.peerAS),
		},
	}
	if c.multiHop {
		n.EbgpMultihop = &api.EbgpMultihop{Enabled: true, MultihopTtl: uint32(255)}
	}
	return c.s.AddPeer(context.Background(), &api.AddPeerRequest{Peer: n})
}

func (c *Controller) getApiPath(route *Route) *api.Path {
	afi := api.Family_AFI_IP
	if route.Net.IP.To4() == nil {
		afi = api.Family_AFI_IP6
	}
	prefixlen, _ := route.Net.Mask.Size()
	nlri, _ := ptypes.MarshalAny(&api.IPAddressPrefix{
		Prefix:    route.Net.IP.String(),
		PrefixLen: uint32(prefixlen),
	})
	a1, _ := ptypes.MarshalAny(&api.OriginAttribute{
		Origin: c.origin,
	})
	a2, _ := ptypes.MarshalAny(&api.NextHopAttribute{
		NextHop: c.localIP.String(),
	})
	var communities []uint32
	for _, comm := range append(c.communities, route.Communities...) {
		communities = append(communities, convertCommunity(comm))
	}
	a3, _ := ptypes.MarshalAny(&api.CommunitiesAttribute{
		Communities: communities,
	})
	attrs := []*any.Any{a1, a2, a3}
	return &api.Path{
		Family: &api.Family{Afi: afi, Safi: api.Family_SAFI_UNICAST},
		Nlri:   nlri,
		Pattrs: attrs,
	}
}

// ensurePeers makes sure every configured peer IP is an established (or
// pending) GoBGP neighbor. It is safe to call repeatedly; peers already
// added are left alone. A peer that fails to add does not stop the others
// from being added; the first such error is returned.
func (c *Controller) ensurePeers() error {
	established := make(map[string]bool)
	err := c.s.ListPeer(context.Background(), &api.ListPeerRequest{}, func(p *api.Peer) {
		established[p.Conf.NeighborAddress] = true
	})
	if err != nil {
		return err
	}
	var addErr error
	for _, peerIP := range c.peerIPs {
		if established[peerIP.String()] {
			continue
		}
		if err := c.AddPeer(peerIP.String()); err != nil && addErr == nil {
			addErr = fmt.Errorf("Unable to add peer %s: %v", peerIP, err)
		}
	}
	return addErr
}

// Announce adds the route to the local RIB. GoBGP advertises paths in the
// RIB to every established peer on its own, so one AddPath call reaches all
// configured peers, there is no per-peer targeting to do here. The route is
// added even if some peers failed to add, so one bad peer doesn't block the
// announcement to the rest; the peer error is still returned.
func (c *Controller) Announce(route *Route) error {
	peerErr := c.ensurePeers()
	if _, err := c.s.AddPath(context.Background(), &api.AddPathRequest{Path: c.getApiPath(route)}); err != nil {
		return err
	}
	return peerErr
}

func (c *Controller) Withdraw(route *Route) error {
	return c.s.DeletePath(context.Background(), &api.DeletePathRequest{Path: c.getApiPath(route)})
}

// PeerInfo returns BGP session info for every configured peer.
func (c *Controller) PeerInfo() ([]*api.Peer, error) {
	want := make(map[string]bool)
	for _, peerIP := range c.peerIPs {
		want[peerIP.String()] = true
	}
	var peers []*api.Peer
	err := c.s.ListPeer(context.Background(), &api.ListPeerRequest{}, func(p *api.Peer) {
		if want[p.Conf.NeighborAddress] {
			peers = append(peers, p)
		}
	})
	if err != nil {
		return nil, err
	}
	return peers, nil
}

// Shutdown gracefully shuts down every established peer session and stops
// the local BGP server. Only peers GoBGP knows about are shut down, since
// ShutdownPeer errors on a peer that was never added (eg. no route was ever
// announced). StopBgp always runs, even if listing or shutting down peers
// fails, so the local server is never left running; the first error is
// returned.
func (c *Controller) Shutdown() error {
	established := make(map[string]bool)
	shutdownErr := c.s.ListPeer(context.Background(), &api.ListPeerRequest{}, func(p *api.Peer) {
		established[p.Conf.NeighborAddress] = true
	})
	for _, peerIP := range c.peerIPs {
		if !established[peerIP.String()] {
			continue
		}
		if err := c.s.ShutdownPeer(context.Background(), &api.ShutdownPeerRequest{
			Address: peerIP.String(),
		}); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
	}
	if err := c.s.StopBgp(context.Background(), &api.StopBgpRequest{}); err != nil {
		return err
	}
	return shutdownErr
}

func convertCommunity(comm string) uint32 {
	parts := strings.Split(comm, ":")
	first, _ := strconv.ParseUint(parts[0], 10, 32)
	second, _ := strconv.ParseUint(parts[1], 10, 32)
	return uint32(first)<<16 | uint32(second)
}
