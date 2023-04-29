package swarm

import (
	"sort"
	"strconv"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

const (
	publicTCPDelay  = 300 * time.Millisecond
	quicDelay       = 300 * time.Millisecond
	privateTCPDelay = 30 * time.Millisecond
	relayDelay      = 500 * time.Millisecond
)

func noDelayRanker(addrs []ma.Multiaddr) []*network.AddrDelay {
	res := make([]*network.AddrDelay, len(addrs))
	for i, a := range addrs {
		res[i] = &network.AddrDelay{Addr: a, Delay: 0}
	}
	return res
}

// defaultDialRanker is the default ranking logic.
//
// we consider private, public ip4, public ip6, relay addresses separately.
//
// In each group, if a quic address is present, we delay tcp addresses.
//
//	private:     30 ms delay.
//	public ip4: 300 ms delay.
//	public ip6: 300 ms delay.
//
// If a quic-v1 address is present we don't dial quic or webtransport address on the same (ip,port) combination.
// If a tcp address is present we don't dial ws or wss address on the same (ip, port) combination.
// If direct addresses are present we delay all relay addresses by 500 millisecond
func defaultDialRanker(addrs []ma.Multiaddr) []*network.AddrDelay {
	ip4 := make([]ma.Multiaddr, 0, len(addrs))
	ip6 := make([]ma.Multiaddr, 0, len(addrs))
	pvt := make([]ma.Multiaddr, 0, len(addrs))
	relay := make([]ma.Multiaddr, 0, len(addrs))

	res := make([]*network.AddrDelay, 0, len(addrs))
	for _, a := range addrs {
		switch {
		case !manet.IsPublicAddr(a):
			pvt = append(pvt, a)
		case isRelayAddr(a):
			relay = append(relay, a)
		case isProtocolAddr(a, ma.P_IP4):
			ip4 = append(ip4, a)
		case isProtocolAddr(a, ma.P_IP6):
			ip6 = append(ip6, a)
		default:
			res = append(res, &network.AddrDelay{Addr: a, Delay: 0})
		}
	}
	var roffset time.Duration = 0
	if len(ip4) > 0 || len(ip6) > 0 {
		roffset = relayDelay
	}

	res = append(res, getAddrDelay(pvt, privateTCPDelay, 0)...)
	res = append(res, getAddrDelay(ip4, publicTCPDelay, 0)...)
	res = append(res, getAddrDelay(ip6, publicTCPDelay, 0)...)
	res = append(res, getAddrDelay(relay, publicTCPDelay, roffset)...)
	return res
}

func getAddrDelay(addrs []ma.Multiaddr, tcpDelay time.Duration, offset time.Duration) []*network.AddrDelay {
	var hasQuicV1 bool
	quicV1Addr := make(map[string]struct{})
	tcpAddr := make(map[string]struct{})
	for _, a := range addrs {
		switch {
		case isProtocolAddr(a, ma.P_WEBTRANSPORT):
		case isProtocolAddr(a, ma.P_QUIC_V1):
			hasQuicV1 = true
			quicV1Addr[addrPort(a, ma.P_UDP)] = struct{}{}
		case isProtocolAddr(a, ma.P_WS) || isProtocolAddr(a, ma.P_WSS):
		case isProtocolAddr(a, ma.P_TCP):
			tcpAddr[addrPort(a, ma.P_TCP)] = struct{}{}
		}
	}

	var na []ma.Multiaddr
	for _, a := range addrs {
		switch {
		case isProtocolAddr(a, ma.P_WEBTRANSPORT):
			if hasQuicV1 {
				if _, ok := quicV1Addr[addrPort(a, ma.P_UDP)]; ok {
					continue
				}
			}
		case isProtocolAddr(a, ma.P_QUIC):
			if hasQuicV1 {
				if _, ok := quicV1Addr[addrPort(a, ma.P_UDP)]; ok {
					continue
				}
			}
		case isProtocolAddr(a, ma.P_WS) || isProtocolAddr(a, ma.P_WSS):
			if _, ok := tcpAddr[addrPort(a, ma.P_TCP)]; ok {
				continue
			}
		}
		na = append(na, a)
	}

	getScore := func(a ma.Multiaddr) int {
		score := 1000_000_000
		if p, err := a.ValueForProtocol(ma.P_UDP); err == nil {
			pp, _ := strconv.Atoi(p)
			score = pp
		} else if p, err := a.ValueForProtocol(ma.P_TCP); err == nil {
			pp, _ := strconv.Atoi(p)
			score = 1000_000 + pp
		}
		return score
	}
	res := make([]*network.AddrDelay, 0)
	sort.Slice(na, func(i, j int) bool { return getScore(na[i]) < getScore(na[j]) })
	qDelay := 0 * time.Millisecond
	tDelay := 0 * time.Millisecond
	quicCount := 0
	for _, a := range na {
		delay := 0 * time.Millisecond
		switch {
		case isProtocolAddr(a, ma.P_QUIC) || isProtocolAddr(a, ma.P_QUIC_V1):
			delay = qDelay
			qDelay = quicDelay
			quicCount++
		case isProtocolAddr(a, ma.P_TCP):
			if quicCount == 1 {
				delay = qDelay
			} else if quicCount > 1 {
				delay = 2 * qDelay
			}
			delay += tDelay
			tDelay = tcpDelay
		}
		res = append(res, &network.AddrDelay{Addr: a, Delay: delay})
	}
	return res
}

func addrPort(a ma.Multiaddr, p int) string {
	c, _ := ma.SplitFirst(a)
	port, _ := a.ValueForProtocol(p)
	return c.Value() + ":" + port
}

func isProtocolAddr(a ma.Multiaddr, p int) bool {
	_, err := a.ValueForProtocol(p)
	return err == nil
}
