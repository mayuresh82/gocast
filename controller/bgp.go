package controller

import (
	"context"
	"fmt"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/any"
	c "github.com/mayuresh82/gocast/config"
	api "github.com/osrg/gobgp/api"
	gobgp "github.com/osrg/gobgp/pkg/server"
	"net"
	"strconv"
	"strings"
)

type Controller struct {
	peerAS          int
	localIP, peerIP net.IP
	communities     []string
	origin          uint32
	s               *gobgp.BgpServer
}

func NewController(config *c.Config) (*Controller, error) {
	c := &Controller{}
	if config.Bgp.PeerIP == "" {
		gw, err := gateway()
		if err != nil {
			return nil, err
		}
		c.peerIP = gw
	} else {
		c.peerIP = net.ParseIP(config.Bgp.PeerIP)
	}
	if c.peerIP == nil {
		return nil, fmt.Errorf("Unable to get peer IP")
	}
	c.communities = config.Bgp.Communities
	switch config.Bgp.Origin {
	case "igp":
		c.origin = 0
	case "egp":
		c.origin = 1
	case "unknown":
		c.origin = 2
	}
	s := gobgp.NewBgpServer()
	go s.Serve()
	localAddr, err := localAddress(c.peerIP)
	if err != nil {
		return nil, err
	}
	c.localIP = localAddr
	if err := s.StartBgp(context.Background(), &api.StartBgpRequest{
		Global: &api.Global{
			As:         uint32(config.Bgp.LocalAS),
			RouterId:   localAddr.String(),
			ListenPort: -1, // gobgp won't listen on tcp:179
		},
	}); err != nil {
		return nil, fmt.Errorf("Unable to start bgp: %v", err)
	}
	c.s = s
	c.peerAS = config.Bgp.PeerAS
	return c, nil
}

func (c *Controller) AddPeer(peer string) error {
	n := &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: peer,
			PeerAs:          uint32(c.peerAS),
		},
	}
	return c.s.AddPeer(context.Background(), &api.AddPeerRequest{Peer: n})
}

func (c *Controller) getApiPath(route *net.IPNet) *api.Path {
	afi := api.Family_AFI_IP
	if route.IP.To4() == nil {
		afi = api.Family_AFI_IP6
	}
	prefixlen, _ := route.Mask.Size()
	nlri, _ := ptypes.MarshalAny(&api.IPAddressPrefix{
		Prefix:    route.IP.String(),
		PrefixLen: uint32(prefixlen),
	})
	a1, _ := ptypes.MarshalAny(&api.OriginAttribute{
		Origin: c.origin,
	})
	a2, _ := ptypes.MarshalAny(&api.NextHopAttribute{
		NextHop: c.localIP.String(),
	})
	var communities []uint32
	for _, comm := range c.communities {
		communities = append(communities, convertCommunity(comm))
	}
	a3, _ := ptypes.MarshalAny(&api.CommunitiesAttribute{
		Communities: communities,
	})
	attrs := []*any.Any{a1, a2, a3}
	return &api.Path{
		Family:    &api.Family{Afi: afi, Safi: api.Family_SAFI_UNICAST},
		AnyNlri:   nlri,
		AnyPattrs: attrs,
	}
}

func (c *Controller) Announce(route *net.IPNet) error {
	peers, err := c.s.ListPeer(context.Background(), &api.ListPeerRequest{})
	if err != nil {
		return err
	}
	var found bool
	for _, p := range peers {
		if p.Conf.NeighborAddress == c.peerIP.String() {
			found = true
			break
		}
	}
	if !found {
		if err := c.AddPeer(c.peerIP.String()); err != nil {
			return err
		}
	}
	_, err = c.s.AddPath(context.Background(), &api.AddPathRequest{Path: c.getApiPath(route)})
	return err
}

func (c *Controller) Withdraw(route *net.IPNet) error {
	return c.s.DeletePath(context.Background(), &api.DeletePathRequest{Path: c.getApiPath(route)})
}

func (c *Controller) PeerInfo() (*api.Peer, error) {
	peers, err := c.s.ListPeer(context.Background(), &api.ListPeerRequest{})
	if err != nil {
		return nil, err
	}
	for _, p := range peers {
		if p.Conf.NeighborAddress == c.peerIP.String() {
			return p, nil
		}
	}
	return nil, nil
}

func (c *Controller) Shutdown() error {
	if err := c.s.ShutdownPeer(context.Background(), &api.ShutdownPeerRequest{
		Address: c.peerIP.String(),
	}); err != nil {
		return err
	}
	if err := c.s.StopBgp(context.Background(), &api.StopBgpRequest{}); err != nil {
		return err
	}
	return nil
}

func convertCommunity(comm string) uint32 {
	parts := strings.Split(comm, ":")
	first, _ := strconv.ParseUint(parts[0], 10, 32)
	second, _ := strconv.ParseUint(parts[1], 10, 32)
	return uint32(first)<<16 | uint32(second)
}
