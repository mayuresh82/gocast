package controller

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/golang/glog"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/any"
	api "github.com/osrg/gobgp/api"
	gobgp "github.com/osrg/gobgp/pkg/server"

	"github.com/mayuresh82/gocast/config"
)

type Route struct {
	Net         *net.IPNet
	Communities []string
}

type Controller struct {
	peerAS          int
	localIP, peerIP net.IP
	communities     []string
	origin          uint32
	multiHop        bool
	s               *gobgp.BgpServer
}

func NewController(bgpConfig config.BgpConfig) (*Controller, error) {
	c := &Controller{}
	if bgpConfig.PeerIP == "" {
		gw, err := gateway()
		if err != nil {
			return nil, fmt.Errorf("unable to get gw ip: %v", err)
		}
		c.peerIP = gw
	} else {
		c.peerIP = net.ParseIP(bgpConfig.PeerIP)
	}
	if bgpConfig.LocalIP == "" {
		gw, err := via(c.peerIP)
		glog.Infof("via: gw %q - err %v", gw, err)
		if err != nil {
			return nil, fmt.Errorf("unable to get gw ip: %v", err)
		}
		c.localIP, err = localAddress(gw)
		glog.Infof("via: local %q - err %v", c.localIP, err)
		if err != nil {
			return nil, err
		}
	} else {
		c.localIP = net.ParseIP(bgpConfig.LocalIP)
	}
	c.communities = bgpConfig.Communities
	switch bgpConfig.Origin {
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
			As:         uint32(bgpConfig.LocalAS),
			RouterId:   c.localIP.String(),
			ListenPort: -1, // gobgp won't listen on tcp:179
		},
	}); err != nil {
		return nil, fmt.Errorf("unable to start bgp: %v", err)
	}
	c.s = s
	c.peerAS = bgpConfig.PeerAS
	// set mh by default for all ebgp peers
	if c.peerAS != bgpConfig.LocalAS {
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
	attrs := make([]*any.Any, 2, 3)

	afi := api.Family_AFI_IP
	if route.Net.IP.To4() == nil {
		afi = api.Family_AFI_IP6
	}
	prefixlen, _ := route.Net.Mask.Size()
	nlri, _ := ptypes.MarshalAny(&api.IPAddressPrefix{
		Prefix:    route.Net.IP.String(),
		PrefixLen: uint32(prefixlen),
	})
	attrs[0], _ = ptypes.MarshalAny(&api.OriginAttribute{
		Origin: c.origin,
	})
	attrs[1], _ = ptypes.MarshalAny(&api.NextHopAttribute{
		NextHop: c.localIP.String(),
	})
	var communities []uint32
	for _, comm := range append(c.communities, route.Communities...) {
		communities = append(communities, convertCommunity(comm))
	}
	if len(communities) > 0 {
		a3, _ := ptypes.MarshalAny(&api.CommunitiesAttribute{
			Communities: communities,
		})
		attrs = append(attrs, a3)
	}
	return &api.Path{
		Family: &api.Family{Afi: afi, Safi: api.Family_SAFI_UNICAST},
		Nlri:   nlri,
		Pattrs: attrs,
	}
}

func (c *Controller) Announce(route *Route) error {
	var found bool
	err := c.s.ListPeer(context.Background(), &api.ListPeerRequest{}, func(p *api.Peer) {
		if p.Conf.NeighborAddress == c.peerIP.String() {
			found = true
		}
	})
	if err != nil {
		return err
	}
	if !found {
		if err := c.AddPeer(c.peerIP.String()); err != nil {
			return err
		}
	}
	_, err = c.s.AddPath(context.Background(), &api.AddPathRequest{Path: c.getApiPath(route)})
	return err
}

func (c *Controller) Withdraw(route *Route) error {
	return c.s.DeletePath(context.Background(), &api.DeletePathRequest{Path: c.getApiPath(route)})
}

func (c *Controller) PeerInfo() (*api.Peer, error) {
	var peer *api.Peer
	err := c.s.ListPeer(context.Background(), &api.ListPeerRequest{}, func(p *api.Peer) {
		if p.Conf.NeighborAddress == c.peerIP.String() {
			peer = p
		}
	})
	if err != nil {
		return nil, err
	}
	return peer, nil
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
