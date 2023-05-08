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

func TestDelayRankerQUICDelay(t *testing.T) {
	q1 := ma.StringCast("/ip4/1.2.3.4/udp/1/quic")
	q1v1 := ma.StringCast("/ip4/1.2.3.4/udp/1/quic-v1")
	wt1 := ma.StringCast("/ip4/1.2.3.4/udp/1/quic-v1/webtransport/")
	q2 := ma.StringCast("/ip4/1.2.3.4/udp/2/quic")
	q2v1 := ma.StringCast("/ip4/1.2.3.4/udp/2/quic-v1")
	q3 := ma.StringCast("/ip4/1.2.3.4/udp/3/quic")
	q3v1 := ma.StringCast("/ip4/1.2.3.4/udp/3/quic-v1")
	q4 := ma.StringCast("/ip4/1.2.3.4/udp/4/quic")

	q1v16 := ma.StringCast("/ip6/1::2/udp/1/quic-v1")
	q2v16 := ma.StringCast("/ip6/1::2/udp/2/quic-v1")
	q3v16 := ma.StringCast("/ip6/1::2/udp/3/quic-v1")

	testCase := []struct {
		name   string
		addrs  []ma.Multiaddr
		output []network.AddrDelay
	}{
		{
			name:  "single quic dialed first",
			addrs: []ma.Multiaddr{q1, q2, q3, q4},
			output: []network.AddrDelay{
				{Addr: q1, Delay: 0},
				{Addr: q2, Delay: publicQUICDelay},
				{Addr: q3, Delay: publicQUICDelay},
				{Addr: q4, Delay: publicQUICDelay},
			},
		},
		{
			name:  "quicv1 dialed before quic",
			addrs: []ma.Multiaddr{q1, q2v1, q3, q4},
			output: []network.AddrDelay{
				{Addr: q2v1, Delay: 0},
				{Addr: q1, Delay: publicQUICDelay},
				{Addr: q3, Delay: publicQUICDelay},
				{Addr: q4, Delay: publicQUICDelay},
			},
		},
		{
			name:  "quic+webtrans filtered when quicv1",
			addrs: []ma.Multiaddr{q1, q2, q3, q4, q1v1, q2v1, q3v1, wt1},
			output: []network.AddrDelay{
				{Addr: q1v1, Delay: 0},
				{Addr: q2v1, Delay: publicQUICDelay},
				{Addr: q3v1, Delay: publicQUICDelay},
				{Addr: q4, Delay: publicQUICDelay},
			},
		},
		{
			name:  "ipv6",
			addrs: []ma.Multiaddr{q1v16, q2v16, q3v16, q1},
			output: []network.AddrDelay{
				{Addr: q1, Delay: 0},
				{Addr: q1v16, Delay: 0},
				{Addr: q2v16, Delay: publicQUICDelay},
				{Addr: q3v16, Delay: publicQUICDelay},
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
					t.Fatalf("expected %+v got %+v", tc.output, res)
				}
			}
		})
	}
}

func TestDelayRankerTCPDelay(t *testing.T) {

	q1 := ma.StringCast("/ip4/1.2.3.4/udp/1/quic")
	q2 := ma.StringCast("/ip4/1.2.3.4/udp/2/quic")

	t1 := ma.StringCast("/ip4/1.2.3.5/tcp/1/")
	t2 := ma.StringCast("/ip4/1.2.3.4/tcp/2")

	testCase := []struct {
		name   string
		addrs  []ma.Multiaddr
		output []network.AddrDelay
	}{
		{
			name:  "2 quic with tcp",
			addrs: []ma.Multiaddr{q1, q2, t1, t2},
			output: []network.AddrDelay{
				{Addr: q1, Delay: 0},
				{Addr: q2, Delay: publicQUICDelay},
				{Addr: t1, Delay: publicQUICDelay + publicTCPDelay},
				{Addr: t2, Delay: publicQUICDelay + publicTCPDelay},
			},
		},
		{
			name:  "1 quic with tcp",
			addrs: []ma.Multiaddr{q1, t1, t2},
			output: []network.AddrDelay{
				{Addr: q1, Delay: 0},
				{Addr: t1, Delay: publicTCPDelay},
				{Addr: t2, Delay: publicTCPDelay},
			},
		},
		{
			name:  "no quic",
			addrs: []ma.Multiaddr{t1, t2},
			output: []network.AddrDelay{
				{Addr: t1, Delay: 0},
				{Addr: t2, Delay: 0},
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
					t.Fatalf("expected %+v got %+v", tc.output, res)
				}
			}
		})
	}
}

func TestDelayRankerRelay(t *testing.T) {
	q1 := ma.StringCast("/ip4/1.2.3.4/udp/1/quic")
	q2 := ma.StringCast("/ip4/1.2.3.4/udp/2/quic")

	pid := test.RandPeerIDFatal(t)
	r1 := ma.StringCast(fmt.Sprintf("/ip4/1.2.3.4/tcp/1/p2p-circuit/p2p/%s", pid))
	r2 := ma.StringCast(fmt.Sprintf("/ip4/1.2.3.4/udp/1/quic/p2p-circuit/p2p/%s", pid))

	testCase := []struct {
		name   string
		addrs  []ma.Multiaddr
		output []network.AddrDelay
	}{
		{
			name:  "relay address delayed",
			addrs: []ma.Multiaddr{q1, q2, r1, r2},
			output: []network.AddrDelay{
				{Addr: q1, Delay: 0},
				{Addr: q2, Delay: publicQUICDelay},
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
