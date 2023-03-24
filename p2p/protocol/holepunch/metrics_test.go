package holepunch

import (
	"testing"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func getCounterValue(t *testing.T, counter *prometheus.CounterVec, labels ...string) int {
	t.Helper()
	m := &dto.Metric{}
	if err := counter.WithLabelValues(labels...).Write(m); err != nil {
		t.Errorf("failed to extract counter value %s", err)
		return 0
	}
	return int(*m.Counter.Value)

}

func TestHolePunchOutcomeCounter(t *testing.T) {
	tcpAddr1 := ma.StringCast("/ip4/1.2.3.4/tcp/1")
	tcpAddr2 := ma.StringCast("/ip4/1.2.3.4/tcp/2")
	quicAddr1 := ma.StringCast("/ip4/1.2.3.4/udp/1/quic")
	quicAddr2 := ma.StringCast("/ip4/1.2.3.4/udp/2/quic")
	quicV1Addr := ma.StringCast("/ip6/1:2:3:4:5:6:7:8/udp/1/quic-v1")
	quicV2Addr := ma.StringCast("/ip6/11:22:33:44:55:66:77:88/udp/2/quic-v1")

	type testcase struct {
		name       string
		theirAddrs []ma.Multiaddr
		ourAddrs   []ma.Multiaddr
		conn       *mockConnMultiaddrs
		result     map[[3]string]int
	}
	testcases := []testcase{
		{
			name:       "same address connection success",
			theirAddrs: []ma.Multiaddr{tcpAddr1},
			ourAddrs:   []ma.Multiaddr{tcpAddr2},
			conn:       &mockConnMultiaddrs{local: tcpAddr1, remote: tcpAddr2},
			result: map[[3]string]int{
				[...]string{"ip4", "tcp", "success"}: 1,
			},
		},
		{
			name:       "multiple similars address should increment correct transport conn",
			theirAddrs: []ma.Multiaddr{tcpAddr1, quicAddr1},
			ourAddrs:   []ma.Multiaddr{tcpAddr2, quicAddr2},
			conn:       &mockConnMultiaddrs{local: quicAddr1, remote: quicAddr2},
			result: map[[3]string]int{
				[...]string{"ip4", "tcp", "failed"}:   1,
				[...]string{"ip4", "quic", "success"}: 1,
			},
		},
		{
			name:       "dissimilar addresses shouldn't count",
			theirAddrs: []ma.Multiaddr{tcpAddr1, quicAddr1},
			ourAddrs:   []ma.Multiaddr{tcpAddr2, quicAddr2},
			conn:       &mockConnMultiaddrs{local: quicV1Addr, remote: quicV2Addr},
			result: map[[3]string]int{
				[...]string{"ip4", "tcp", "failed"}:   1,
				[...]string{"ip4", "quic", "failed"}:  1,
				[...]string{"ip4", "quic", "success"}: 0,
				[...]string{"ip4", "tcp", "success"}:  0,
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			holePunchOutcomesTotal.Reset()
			mt := NewMetricsTracer(WithRegisterer(reg))
			for _, side := range []string{"receiver", "initiator"} {
				mt.HolePunchFinished(side, 1, tc.theirAddrs, tc.ourAddrs, tc.conn)
				for labels, value := range tc.result {
					v := getCounterValue(t, holePunchOutcomesTotal, side, "1", labels[0], labels[1], labels[2])
					if v != value {
						t.Errorf("Invalid metric value: expected: %d got: %d", value, v)
					}
				}
			}
		})
	}
}

type mockConnMultiaddrs struct {
	local, remote ma.Multiaddr
}

func (cma *mockConnMultiaddrs) LocalMultiaddr() ma.Multiaddr {
	return cma.local
}

func (cma *mockConnMultiaddrs) RemoteMultiaddr() ma.Multiaddr {
	return cma.remote
}
