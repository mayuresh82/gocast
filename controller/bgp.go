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
	localAS     int
	localIP     net.IP
	peers       []c.PeerConfig
	communities []string
	origin      uint32
	s           *gobgp.BgpServer
}

func NewController(config c.BgpConfig) (*Controller, error) {
	ctrl := &Controller{}
	var gw net.IP
	var err error

	// Normalize config: convert legacy single-peer to new multi-peer format
	peers := config.Peers
	if len(peers) == 0 {
		// Backward compatibility: convert legacy config
		if config.PeerIP != "" {
			// Explicit peer IP configured
			peers = []c.PeerConfig{{
				PeerIP: config.PeerIP,
				PeerAS: config.PeerAS,
			}}
		} else {
			// No peer IP configured - use default gateway
			gw, err = gateway()
			if err != nil {
				return nil, fmt.Errorf("Unable to get gateway ip: %v", err)
			}
			peers = []c.PeerConfig{{
				PeerIP: gw.String(),
				PeerAS: config.PeerAS,
			}}
		}
	}

	// Determine local IP
	if config.LocalIP == "" {
		// Use first peer to determine local IP
		firstPeerIP := net.ParseIP(peers[0].PeerIP)
		if firstPeerIP == nil {
			gw, err = gateway()
			if err != nil {
				return nil, fmt.Errorf("Unable to get gw ip: %v", err)
			}
			firstPeerIP = gw
		}
		gw, err = via(firstPeerIP)
		if err != nil {
			return nil, fmt.Errorf("Unable to get gw ip: %v", err)
		}
		ctrl.localIP, err = localAddress(gw)
		if err != nil {
			return nil, err
		}
	} else {
		ctrl.localIP = net.ParseIP(config.LocalIP)
	}

	ctrl.localAS = config.LocalAS
	ctrl.peers = peers
	ctrl.communities = config.Communities

	switch config.Origin {
	case "igp":
		ctrl.origin = 0
	case "egp":
		ctrl.origin = 1
	case "unknown":
		ctrl.origin = 2
	}

	s := gobgp.NewBgpServer()
	go s.Serve()
	if err := s.StartBgp(context.Background(), &api.StartBgpRequest{
		Global: &api.Global{
			As:         uint32(config.LocalAS),
			RouterId:   ctrl.localIP.String(),
			ListenPort: -1, // gobgp won't listen on tcp:179
		},
	}); err != nil {
		return nil, fmt.Errorf("Unable to start bgp: %v", err)
	}
	ctrl.s = s

	return ctrl, nil
}

func (c *Controller) addPeer(peer *c.PeerConfig) error {
	n := &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: peer.PeerIP,
			PeerAs:          uint32(peer.PeerAS),
		},
	}

	// Enable multihop only if explicitly configured
	if peer.MultiHop != nil && *peer.MultiHop {
		n.EbgpMultihop = &api.EbgpMultihop{Enabled: true, MultihopTtl: uint32(255)}
	}
	return c.s.AddPeer(context.Background(), &api.AddPeerRequest{Peer: n})
}

func (c *Controller) getApiPath(route *Route, peer *c.PeerConfig) *api.Path {
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

	// Merge communities: global + per-peer + per-route
	var allCommunities []string
	allCommunities = append(allCommunities, c.communities...)
	allCommunities = append(allCommunities, peer.Communities...)
	allCommunities = append(allCommunities, route.Communities...)

	var communities []uint32
	for _, comm := range allCommunities {
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

func (c *Controller) Announce(route *Route) error {
	var errs []error

	for i := range c.peers {
		peer := &c.peers[i]

		// Check if peer exists
		var found bool
		err := c.s.ListPeer(context.Background(), &api.ListPeerRequest{}, func(p *api.Peer) {
			if p.Conf.NeighborAddress == peer.PeerIP {
				found = true
			}
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("peer %s: list error: %v", peer.PeerIP, err))
			continue
		}

		// Add peer if not found
		if !found {
			if err := c.addPeer(peer); err != nil {
				errs = append(errs, fmt.Errorf("peer %s: add error: %v", peer.PeerIP, err))
				continue
			}
		}

		// Announce route to this peer
		path := c.getApiPath(route, peer)
		if _, err := c.s.AddPath(context.Background(), &api.AddPathRequest{Path: path}); err != nil {
			errs = append(errs, fmt.Errorf("peer %s: announce error: %v", peer.PeerIP, err))
			continue
		}
	}

	// Return aggregated errors if any peer failed
	if len(errs) > 0 {
		return fmt.Errorf("announcement errors: %v", errs)
	}
	return nil
}

func (c *Controller) Withdraw(route *Route) error {
	var errs []error

	for i := range c.peers {
		peer := &c.peers[i]
		path := c.getApiPath(route, peer)
		if err := c.s.DeletePath(context.Background(), &api.DeletePathRequest{Path: path}); err != nil {
			errs = append(errs, fmt.Errorf("peer %s: withdraw error: %v", peer.PeerIP, err))
			continue
		}
	}

	// Return aggregated errors if any peer failed
	if len(errs) > 0 {
		return fmt.Errorf("withdrawal errors: %v", errs)
	}
	return nil
}

func (c *Controller) PeerInfo() ([]*api.Peer, error) {
	var peers []*api.Peer
	peerMap := make(map[string]bool)

	// Build map of configured peer IPs
	for _, peer := range c.peers {
		peerMap[peer.PeerIP] = true
	}

	// Collect info for all configured peers
	err := c.s.ListPeer(context.Background(), &api.ListPeerRequest{}, func(p *api.Peer) {
		if peerMap[p.Conf.NeighborAddress] {
			peers = append(peers, p)
		}
	})
	if err != nil {
		return nil, err
	}
	return peers, nil
}

func (c *Controller) Shutdown() error {
	var errs []error

	// Shutdown all peer sessions
	for _, peer := range c.peers {
		if err := c.s.ShutdownPeer(context.Background(), &api.ShutdownPeerRequest{
			Address: peer.PeerIP,
		}); err != nil {
			errs = append(errs, fmt.Errorf("peer %s: shutdown error: %v", peer.PeerIP, err))
		}
	}

	// Stop BGP server
	if err := c.s.StopBgp(context.Background(), &api.StopBgpRequest{}); err != nil {
		errs = append(errs, fmt.Errorf("stop bgp error: %v", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %v", errs)
	}
	return nil
}

func convertCommunity(comm string) uint32 {
	parts := strings.Split(comm, ":")
	first, _ := strconv.ParseUint(parts[0], 10, 32)
	second, _ := strconv.ParseUint(parts[1], 10, 32)
	return uint32(first)<<16 | uint32(second)
}
