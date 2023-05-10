package swarm

import (
	"net/netip"
	"sort"
	"strconv"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// The 250ms value is from happy eyeballs RFC 8305. This is a rough estimate of 1 RTT
const (
	// duration by which tcp dials are delayed relative to quic dial
	publicTCPDelay  = 250 * time.Millisecond
	privateTCPDelay = 30 * time.Millisecond

	// duration by which quic dials are delayed relative to first quic dial
	publicQUICDelay  = 250 * time.Millisecond
	privateQUICDelay = 30 * time.Millisecond

	// relayDelay is the duration by which relay dials are delayed relative to direct addresses
	relayDelay = 250 * time.Millisecond
)

// noDelayRanker ranks addresses with no delay. This is useful for simultaneous connect requests.
func noDelayRanker(addrs []ma.Multiaddr) []network.AddrDelay {
	res := make([]network.AddrDelay, len(addrs))
	for i, a := range addrs {
		res[i] = network.AddrDelay{Addr: a, Delay: 0}
	}
	return res
}

// defaultDialRanker is the default ranking logic.
//
// We rank private, public ip4, public ip6, relay addresses separately.
// In each group we apply the following logic:
//
// First we filter the addresses we don't want to dial. We are filtering these addresses because we
// have an address that we prefer more than that address and which has the same reachability
//
//	If a quic-v1 address is present we don't dial quic or webtransport address on the same (ip,port)
//	combination. If a quicDraft29 or webtransport address is reachable, quic-v1 will definitely be
//	reachable. quicDraft29 is deprecated in favour of quic-v1 and quic-v1 is more performant than
//	webtransport
//
//	If a tcp address is present we don't dial ws or wss address on the same (ip, port) combination.
//	If a ws address is reachable, tcp will definitely be reachable and it'll be more performant
//
// Then we rank the addresses:
//
//	If two quic addresses are present, we dial the quic address with the lowest port first. This is more
//	likely to be the listen port. After this we dial the rest of the quic addresses delayed by QUICDelay.
//
//	If a quic or webtransport address is present, tcp address dials are delayed by TCPDelay relative to
//	the last quic dial.
//
//	TCPDelay for public ip4 and public ip6 is publicTCPDelay
//	TCPDelay for private addresses is privateTCPDelay
//	QUICDelay for public addresses is publicQUICDelay
//	QUICDelay for private addresses is privateQUICDelay
//
// If direct addresses are present we delay all relay addresses by 500 millisecond
func defaultDialRanker(addrs []ma.Multiaddr) []network.AddrDelay {
	ip4 := make([]ma.Multiaddr, 0, len(addrs))
	ip6 := make([]ma.Multiaddr, 0, len(addrs))
	pvt := make([]ma.Multiaddr, 0, len(addrs))
	relay := make([]ma.Multiaddr, 0, len(addrs))

	res := make([]network.AddrDelay, 0, len(addrs))
	for _, a := range addrs {
		switch {
		case isRelayAddr(a):
			relay = append(relay, a)
		case !manet.IsPublicAddr(a):
			pvt = append(pvt, a)
		case isProtocolAddr(a, ma.P_IP4):
			ip4 = append(ip4, a)
		case isProtocolAddr(a, ma.P_IP6):
			ip6 = append(ip6, a)
		default:
			res = append(res, network.AddrDelay{Addr: a, Delay: 0})
		}
	}

	var relayOffset time.Duration = 0
	if len(ip4) > 0 || len(ip6) > 0 {
		// if there is a public direct address available delay relay dials
		relayOffset = relayDelay
	}

	res = append(res, getAddrDelay(pvt, privateTCPDelay, privateQUICDelay, 0)...)
	res = append(res, getAddrDelay(ip4, publicTCPDelay, publicQUICDelay, 0)...)
	res = append(res, getAddrDelay(ip6, publicTCPDelay, publicQUICDelay, 0)...)
	res = append(res, getAddrDelay(relay, publicTCPDelay, publicQUICDelay, relayOffset)...)
	return res
}

// getAddrDelay ranks a group of addresses(private, ip4, ip6) according to the ranking logic
// explained in defaultDialRanker.
// offset is used to delay all addresses by a fixed duration. This is useful for delaying all relay
// addresses relative to direct addresses
func getAddrDelay(addrs []ma.Multiaddr, tcpDelay time.Duration, quicDelay time.Duration,
	offset time.Duration) []network.AddrDelay {

	// First make a map of quicV1 and tcp AddrPorts.
	quicV1Addr := make(map[netip.AddrPort]struct{})
	tcpAddr := make(map[netip.AddrPort]struct{})
	for _, a := range addrs {
		switch {
		case isProtocolAddr(a, ma.P_WEBTRANSPORT):
		case isProtocolAddr(a, ma.P_QUIC_V1):
			quicV1Addr[addrPort(a, ma.P_UDP)] = struct{}{}
		case isProtocolAddr(a, ma.P_WS) || isProtocolAddr(a, ma.P_WSS):
		case isProtocolAddr(a, ma.P_TCP):
			tcpAddr[addrPort(a, ma.P_TCP)] = struct{}{}
		}
	}

	// Filter addresses we are sure we don't want to dial
	selectedAddrs := addrs
	i := 0
	for _, a := range addrs {
		switch {
		// If a quicDraft29 or webtransport address is reachable, quic-v1 will also be reachable. So we
		// drop the quicDraft29 or webtransport address
		// We prefer quic-v1 over the older quic-draft29 address.
		// We prefer quic-v1 over webtransport as it is more performant.
		case isProtocolAddr(a, ma.P_WEBTRANSPORT) || isProtocolAddr(a, ma.P_QUIC):
			if _, ok := quicV1Addr[addrPort(a, ma.P_UDP)]; ok {
				continue
			}
		// If a ws address is reachable, tcp will also be reachable and it'll be more performant
		case isProtocolAddr(a, ma.P_WS) || isProtocolAddr(a, ma.P_WSS):
			if _, ok := tcpAddr[addrPort(a, ma.P_TCP)]; ok {
				continue
			}
		}
		selectedAddrs[i] = a
		i++
	}
	selectedAddrs = selectedAddrs[:i]
	sort.Slice(selectedAddrs, func(i, j int) bool { return score(selectedAddrs[i]) < score(selectedAddrs[j]) })

	res := make([]network.AddrDelay, 0, len(addrs))
	quicCount := 0
	for _, a := range selectedAddrs {
		delay := offset
		switch {
		case isProtocolAddr(a, ma.P_QUIC) || isProtocolAddr(a, ma.P_QUIC_V1):
			// For quic addresses we dial a single address first and then wait for quicDelay
			// After quicDelay we dial rest of the quic addresses
			if quicCount > 0 {
				delay += quicDelay
			}
			quicCount++
		case isProtocolAddr(a, ma.P_TCP):
			if quicCount >= 2 {
				delay += 2 * quicDelay
			} else if quicCount == 1 {
				delay += tcpDelay
			}
		}
		res = append(res, network.AddrDelay{Addr: a, Delay: delay})
	}
	return res
}

// score scores a multiaddress for dialing delay. lower is better
func score(a ma.Multiaddr) int {
	// the lower 16 bits of the result are the relavant port
	// the higher bits rank the protocol
	// low ports are ranked higher because they're more likely to
	// be listen addresses
	if _, err := a.ValueForProtocol(ma.P_WEBTRANSPORT); err == nil {
		p, _ := a.ValueForProtocol(ma.P_UDP)
		pi, _ := strconv.Atoi(p) // cannot error
		return pi + (1 << 18)
	}
	if _, err := a.ValueForProtocol(ma.P_QUIC); err == nil {
		p, _ := a.ValueForProtocol(ma.P_UDP)
		pi, _ := strconv.Atoi(p) // cannot error
		return pi + (1 << 17)
	}
	if _, err := a.ValueForProtocol(ma.P_QUIC_V1); err == nil {
		p, _ := a.ValueForProtocol(ma.P_UDP)
		pi, _ := strconv.Atoi(p) // cannot error
		return pi
	}

	if p, err := a.ValueForProtocol(ma.P_TCP); err == nil {
		pi, _ := strconv.Atoi(p) // cannot error
		return pi + (1 << 19)
	}
	return (1 << 30)
}

// addrPort returns the ip and port for a. p should be either ma.P_TCP or ma.P_UDP.
// a must be an (ip, tcp) or (ip, udp) address.
func addrPort(a ma.Multiaddr, p int) netip.AddrPort {
	ip, _ := manet.ToIP(a)
	port, _ := a.ValueForProtocol(p)
	pi, _ := strconv.Atoi(port)
	addr, _ := netip.AddrFromSlice(ip)
	return netip.AddrPortFrom(addr, uint16(pi))
}

func isProtocolAddr(a ma.Multiaddr, p int) bool {
	_, err := a.ValueForProtocol(p)
	return err == nil
}
