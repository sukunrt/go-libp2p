package swarm

import (
	"fmt"
	"sort"
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/test"
	ma "github.com/multiformats/go-multiaddr"
)

func TestNoDelayRanker(t *testing.T) {
	addrs := []ma.Multiaddr{
		ma.StringCast("/ip4/1.2.3.4/tcp/1"),
		ma.StringCast("/ip4/1.2.3.4/udp/1/quic-v1"),
	}
	addrDelays := noDelayRanker(addrs)
	if len(addrs) != len(addrDelays) {
		t.Errorf("addrDelay should have the same number of elements as addr")
	}

	for _, a := range addrs {
		for _, ad := range addrDelays {
			if a.Equal(ad.Addr) {
				if ad.Delay != 0 {
					t.Errorf("expected 0 delay, got %s", ad.Delay)
				}
			}
		}
	}
}

func TestDelayRankerTCPDelay(t *testing.T) {
	pquicv1 := ma.StringCast("/ip4/192.168.0.100/udp/1/quic-v1")
	ptcp := ma.StringCast("/ip4/192.168.0.100/tcp/1/")

	quic := ma.StringCast("/ip4/1.2.3.4/udp/1/quic")
	quicv1 := ma.StringCast("/ip4/1.2.3.4/udp/1/quic-v1")
	tcp := ma.StringCast("/ip4/1.2.3.5/tcp/1/")

	tcp6 := ma.StringCast("/ip6/1::1/tcp/1")
	quicv16 := ma.StringCast("/ip6/1::2/udp/1/quic-v1")

	testCase := []struct {
		name   string
		addrs  []ma.Multiaddr
		output []*network.AddrDelay
	}{
		{
			name:  "quic prioritised over tcp",
			addrs: []ma.Multiaddr{quic, tcp},
			output: []*network.AddrDelay{
				{Addr: quic, Delay: 0},
				{Addr: tcp, Delay: publicTCPDelay},
			},
		},
		{
			name:  "quic-v1 prioritised over tcp",
			addrs: []ma.Multiaddr{quicv1, tcp},
			output: []*network.AddrDelay{
				{Addr: quicv1, Delay: 0},
				{Addr: tcp, Delay: publicTCPDelay},
			},
		},
		{
			name:  "ip6 treated separately",
			addrs: []ma.Multiaddr{quicv16, tcp6, quic},
			output: []*network.AddrDelay{
				{Addr: quicv16, Delay: 0},
				{Addr: quic, Delay: 0},
				{Addr: tcp6, Delay: publicTCPDelay},
			},
		},
		{
			name:  "private addrs treated separately",
			addrs: []ma.Multiaddr{pquicv1, ptcp},
			output: []*network.AddrDelay{
				{Addr: pquicv1, Delay: 0},
				{Addr: ptcp, Delay: privateTCPDelay},
			},
		},
	}
	for _, tc := range testCase {
		t.Run(tc.name, func(t *testing.T) {
			res := defaultDialRanker(tc.addrs)
			if len(res) != len(tc.output) {
				for _, a := range res {
					log.Errorf("%v", a)
				}
				for _, a := range tc.output {
					log.Errorf("%v", a)
				}
				t.Errorf("expected elems: %d got: %d", len(tc.output), len(res))
			}
			sort.Slice(res, func(i, j int) bool {
				if res[i].Delay == res[j].Delay {
					return res[i].Addr.String() < res[j].Addr.String()
				}
				return res[i].Delay < res[j].Delay
			})
			sort.Slice(tc.output, func(i, j int) bool {
				if tc.output[i].Delay == tc.output[j].Delay {
					return tc.output[i].Addr.String() < tc.output[j].Addr.String()
				}
				return tc.output[i].Delay < tc.output[j].Delay
			})
			for i := 0; i < len(tc.output); i++ {
				if !tc.output[i].Addr.Equal(res[i].Addr) || tc.output[i].Delay != res[i].Delay {
					t.Errorf("expected %+v got %+v", tc.output[i], res[i])
				}
			}
		})
	}
}

func TestDelayRankerAddrDropped(t *testing.T) {
	pquic := ma.StringCast("/ip4/192.168.0.100/udp/1/quic")
	pquicv1 := ma.StringCast("/ip4/192.168.0.100/udp/1/quic-v1")

	quicAddr := ma.StringCast("/ip4/1.2.3.4/udp/1/quic")
	quicAddr2 := ma.StringCast("/ip4/1.2.3.4/udp/2/quic")
	quicv1Addr := ma.StringCast("/ip4/1.2.3.4/udp/1/quic-v1")
	wt := ma.StringCast("/ip4/1.2.3.4/udp/1/quic-v1/webtransport/")
	wt2 := ma.StringCast("/ip4/1.2.3.4/udp/2/quic-v1/webtransport/")

	quic6 := ma.StringCast("/ip6/1::1/udp/1/quic")
	quicv16 := ma.StringCast("/ip6/1::1/udp/1/quic-v1")

	tcp := ma.StringCast("/ip4/1.2.3.5/tcp/1/")
	ws := ma.StringCast("/ip4/1.2.3.5/tcp/1/ws")
	ws2 := ma.StringCast("/ip4/1.2.3.4/tcp/1/ws")
	wss := ma.StringCast("/ip4/1.2.3.5/tcp/1/wss")

	testCase := []struct {
		name   string
		addrs  []ma.Multiaddr
		output []*network.AddrDelay
	}{
		{
			name:  "quic dropped when quic-v1 present",
			addrs: []ma.Multiaddr{quicAddr, quicv1Addr, quicAddr2},
			output: []*network.AddrDelay{
				{Addr: quicv1Addr, Delay: 0},
				{Addr: quicAddr2, Delay: 0},
			},
		},
		{
			name:  "webtransport dropped when quicv1 present",
			addrs: []ma.Multiaddr{quicv1Addr, wt, wt2, quicAddr},
			output: []*network.AddrDelay{
				{Addr: quicv1Addr, Delay: 0},
				{Addr: wt2, Delay: 0},
			},
		},
		{
			name:  "ip6 quic dropped when quicv1 present",
			addrs: []ma.Multiaddr{quicv16, quic6},
			output: []*network.AddrDelay{
				{Addr: quicv16, Delay: 0},
			},
		},
		{
			name:  "web socket removed when tcp present",
			addrs: []ma.Multiaddr{quicAddr, tcp, ws, wss, ws2},
			output: []*network.AddrDelay{
				{Addr: quicAddr, Delay: 0},
				{Addr: tcp, Delay: publicTCPDelay},
				{Addr: ws2, Delay: publicTCPDelay},
			},
		},
		{
			name:  "private quic dropped when quiv1 present",
			addrs: []ma.Multiaddr{pquic, pquicv1},
			output: []*network.AddrDelay{
				{Addr: pquicv1, Delay: 0},
			},
		},
	}
	for _, tc := range testCase {
		t.Run(tc.name, func(t *testing.T) {
			res := defaultDialRanker(tc.addrs)
			if len(res) != len(tc.output) {
				for _, a := range res {
					log.Errorf("%v", a)
				}
				for _, a := range tc.output {
					log.Errorf("%v", a)
				}
				t.Errorf("expected elems: %d got: %d", len(tc.output), len(res))
			}
			sort.Slice(res, func(i, j int) bool {
				if res[i].Delay == res[j].Delay {
					return res[i].Addr.String() < res[j].Addr.String()
				}
				return res[i].Delay < res[j].Delay
			})
			sort.Slice(tc.output, func(i, j int) bool {
				if tc.output[i].Delay == tc.output[j].Delay {
					return tc.output[i].Addr.String() < tc.output[j].Addr.String()
				}
				return tc.output[i].Delay < tc.output[j].Delay
			})

			for i := 0; i < len(tc.output); i++ {
				if !tc.output[i].Addr.Equal(res[i].Addr) || tc.output[i].Delay != res[i].Delay {
					t.Errorf("expected %+v got %+v", tc.output[i], res[i])
				}
			}
		})
	}
}

func TestDelayRankerRelay(t *testing.T) {
	quicAddr := ma.StringCast("/ip4/1.2.3.4/udp/1/quic")
	quicAddr2 := ma.StringCast("/ip4/1.2.3.4/udp/2/quic")

	pid := test.RandPeerIDFatal(t)
	r1 := ma.StringCast(fmt.Sprintf("/ip4/1.2.3.4/tcp/1/p2p-circuit/p2p/%s", pid))
	r2 := ma.StringCast(fmt.Sprintf("/ip4/1.2.3.4/udp/1/quic/p2p-circuit/p2p/%s", pid))

	testCase := []struct {
		name   string
		addrs  []ma.Multiaddr
		output []*network.AddrDelay
	}{
		{
			name:  "relay address delayed",
			addrs: []ma.Multiaddr{quicAddr, quicAddr2, r1, r2},
			output: []*network.AddrDelay{
				{Addr: quicAddr, Delay: 0},
				{Addr: quicAddr2, Delay: 0},
				{Addr: r2, Delay: relayDelay},
				{Addr: r1, Delay: publicTCPDelay + relayDelay},
			},
		},
	}
	for _, tc := range testCase {
		t.Run(tc.name, func(t *testing.T) {
			res := defaultDialRanker(tc.addrs)
			if len(res) != len(tc.output) {
				for _, a := range res {
					log.Errorf("%v", a)
				}
				for _, a := range tc.output {
					log.Errorf("%v", a)
				}
				t.Errorf("expected elems: %d got: %d", len(tc.output), len(res))
			}
			sort.Slice(res, func(i, j int) bool {
				if res[i].Delay == res[j].Delay {
					return res[i].Addr.String() < res[j].Addr.String()
				}
				return res[i].Delay < res[j].Delay
			})
			sort.Slice(tc.output, func(i, j int) bool {
				if tc.output[i].Delay == tc.output[j].Delay {
					return tc.output[i].Addr.String() < tc.output[j].Addr.String()
				}
				return tc.output[i].Delay < tc.output[j].Delay
			})

			for i := 0; i < len(tc.output); i++ {
				if !tc.output[i].Addr.Equal(res[i].Addr) || tc.output[i].Delay != res[i].Delay {
					t.Errorf("expected %+v got %+v", tc.output[i], res[i])
				}
			}
		})
	}
}
