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

type Peer struct {
	peerAS          int
	localIP, peerIP net.IP
	communities     []string
	origin          uint32
	multiHop        bool
	family          string
	announced       []string
}

type Controller struct {
	peers []*Peer
	s     *gobgp.BgpServer
}

func NewController(config *c.Config) (*Controller, error) {
	c := &Controller{s: gobgp.NewBgpServer()}
	go c.s.Serve()
	for _, bgpConf := range config.Bgp {
		p := &Peer{}
		var err error
		if bgpConf.PeerIP == "" {
			p.peerIP, err = gateway(bgpConf.AddrFamily)
		} else {
			p.peerIP = net.ParseIP(bgpConf.PeerIP)
		}
		if err != nil || p.peerIP == nil {
			return nil, fmt.Errorf("Unable to get peer IP : %v", err)
		}
		p.communities = bgpConf.Communities
		switch bgpConf.Origin {
		case "igp":
			p.origin = 0
		case "egp":
			p.origin = 1
		case "unknown":
			p.origin = 2
		}
		dev, err := via(p.peerIP)
		if err != nil {
			return nil, err
		}
		localAddr, err := localAddress(dev, bgpConf.AddrFamily)
		if err != nil {
			return nil, err
		}
		p.localIP = localAddr
		p.peerAS = bgpConf.PeerAS
		// set mh by default for all ebgp peers
		if p.peerAS != bgpConf.LocalAS {
			p.multiHop = true
		}
		localAddr4, _ := localAddress(dev, "4") // for router-id
		if err := c.s.StartBgp(context.Background(), &api.StartBgpRequest{
			Global: &api.Global{
				As:         uint32(bgpConf.LocalAS),
				RouterId:   localAddr4.String(),
				ListenPort: -1, // gobgp won't listen on tcp:179
			},
		}); err != nil {
			return nil, fmt.Errorf("Unable to start bgp: %v", err)
		}
		p.family = "6"
		if p.peerIP.To4() != nil {
			p.family = "4"
		}
		c.peers = append(c.peers, p)
	}
	return c, nil
}

func (c *Controller) localIP(family string) net.IP {
	for _, peer := range c.peers {
		if peer.family == family {
			return peer.localIP
		}
	}
	return nil
}

func (c *Controller) AddPeer(p *Peer) error {
	n := &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: p.peerIP.String(),
			PeerAs:          uint32(p.peerAS),
		},
	}
	if p.multiHop {
		n.EbgpMultihop = &api.EbgpMultihop{Enabled: true, MultihopTtl: uint32(255)}
	}
	return c.s.AddPeer(context.Background(), &api.AddPeerRequest{Peer: n})
}

func (c *Controller) getApiPath(p *Peer, route *net.IPNet, withdraw bool) *api.Path {
	afi := api.Family_AFI_IP
	if route.IP.To4() == nil {
		afi = api.Family_AFI_IP6
	}
	family := &api.Family{Afi: afi, Safi: api.Family_SAFI_UNICAST}
	prefixlen, _ := route.Mask.Size()
	nlri, _ := ptypes.MarshalAny(&api.IPAddressPrefix{
		Prefix:    route.IP.String(),
		PrefixLen: uint32(prefixlen),
	})
	a1, _ := ptypes.MarshalAny(&api.OriginAttribute{
		Origin: p.origin,
	})
	var communities []uint32
	for _, comm := range p.communities {
		communities = append(communities, convertCommunity(comm))
	}
	a2, _ := ptypes.MarshalAny(&api.CommunitiesAttribute{
		Communities: communities,
	})
	attrs := []*any.Any{a1, a2}
	path := &api.Path{AnyNlri: nlri, Family: family, AnyPattrs: attrs, IsWithdraw: withdraw}
	switch afi {
	case api.Family_AFI_IP:
		nh, _ := ptypes.MarshalAny(&api.NextHopAttribute{
			NextHop: p.localIP.String(),
		})
		path.AnyPattrs = append(path.AnyPattrs, nh)
	case api.Family_AFI_IP6:
		mpReachAttr, _ := ptypes.MarshalAny(&api.MpReachNLRIAttribute{
			Family:   family,
			NextHops: []string{p.localIP.String()},
			Nlris:    []*any.Any{nlri},
		})
		mpUnreachAttr, _ := ptypes.MarshalAny(&api.MpUnreachNLRIAttribute{
			Family: family,
			Nlris:  []*any.Any{nlri},
		})
		if withdraw {
			path.AnyPattrs = append(path.AnyPattrs, mpUnreachAttr)
		} else {
			path.AnyPattrs = append(path.AnyPattrs, mpReachAttr)
		}
	}
	return path
}

func (c *Controller) Announce(route *net.IPNet) error {
	family := "6"
	if route.IP.To4() != nil {
		family = "4"
	}
	for _, peer := range c.peers {
		if peer.family != family {
			continue
		}
		peers, err := c.s.ListPeer(context.Background(), &api.ListPeerRequest{})
		if err != nil {
			return err
		}
		var found bool
		for _, p := range peers {
			if p.Conf.NeighborAddress == peer.peerIP.String() {
				found = true
				break
			}
		}
		if !found {
			if err := c.AddPeer(peer); err != nil {
				return err
			}
		}
		if _, err := c.s.AddPath(context.Background(), &api.AddPathRequest{Path: c.getApiPath(peer, route, false)}); err != nil {
			return err
		}
		peer.announced = append(peer.announced, route.String())
	}
	return nil
}

func (c *Controller) Withdraw(route *net.IPNet) error {
	for _, peer := range c.peers {
		if !contains(peer.announced, route.String()) {
			continue
		}
		if err := c.s.DeletePath(context.Background(), &api.DeletePathRequest{Path: c.getApiPath(peer, route, true)}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) PeerInfo() ([]*api.Peer, error) {
	return c.s.ListPeer(context.Background(), &api.ListPeerRequest{})
}

func (c *Controller) Shutdown() error {
	for _, peer := range c.peers {
		if err := c.s.ShutdownPeer(context.Background(), &api.ShutdownPeerRequest{
			Address: peer.peerIP.String(),
		}); err != nil {
			return err
		}
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
