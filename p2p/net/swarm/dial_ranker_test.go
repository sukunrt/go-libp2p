package swarm

import (
	"sort"
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
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
	q1 := ma.StringCast("/ip4/1.2.3.4/udp/1/quic")
	q16 := ma.StringCast("/ip6/::/udp/1/quic")
	qv1 := ma.StringCast("/ip4/1.2.3.4/udp/1/quic-v1")
	qv2 := ma.StringCast("/ip4/1.2.3.4/udp/2/quic-v1")
	t1 := ma.StringCast("/ip4/1.2.3.5/tcp/1/")
	t2 := ma.StringCast("/ip4/1.2.3.5/tcp/2")

	testCase := []struct {
		name   string
		addrs  []ma.Multiaddr
		output []*network.AddrDelay
	}{
		{
			name:  "ranking",
			addrs: []ma.Multiaddr{q1, qv1, qv2, t1, t2},
			output: []*network.AddrDelay{
				{Addr: qv1, Delay: 0},
				{Addr: qv2, Delay: quicDelay},
				{Addr: t1, Delay: 2 * quicDelay},
				{Addr: t2, Delay: 2*quicDelay + publicTCPDelay},
			},
		},
		{
			name:  "ranking 2",
			addrs: []ma.Multiaddr{q1, t1, t2},
			output: []*network.AddrDelay{
				{Addr: q1, Delay: 0},
				{Addr: t1, Delay: quicDelay},
				{Addr: t2, Delay: quicDelay + publicTCPDelay},
			},
		},
		{
			name:  "ranking 3",
			addrs: []ma.Multiaddr{t1, t2},
			output: []*network.AddrDelay{
				{Addr: t1, Delay: 0},
				{Addr: t2, Delay: publicTCPDelay},
			},
		},
		{
			name:  "ranking 4",
			addrs: []ma.Multiaddr{q16, qv1, qv2, t1, t2},
			output: []*network.AddrDelay{
				{Addr: q16, Delay: 0},
				{Addr: qv1, Delay: quicDelay},
				{Addr: qv2, Delay: quicDelay},
				{Addr: t1, Delay: 2 * quicDelay},
				{Addr: t2, Delay: 2*quicDelay + publicTCPDelay},
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
