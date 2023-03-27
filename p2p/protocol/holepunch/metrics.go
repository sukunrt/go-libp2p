package holepunch

import (
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/p2p/metricshelper"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/prometheus/client_golang/prometheus"
)

const metricNamespace = "libp2p_holepunch"

var (
	directDialsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "direct_dials_total",
			Help:      "Direct Dials Total",
		},
		[]string{"outcome"},
	)
	holePunchOutcomesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "outcomes_total",
			Help:      "Hole Punch Outcomes",
		},
		[]string{"side", "num_attempts", "ipv", "transport", "outcome"},
	)
	holePunchNoSuitableAddressTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "no_suitable_address_total",
			Help:      "Hole Punch Failures because address mismatch",
		},
		[]string{"side"},
	)
	publicAddrsCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "local_addresses_count",
			Help:      "Public Address Count for ipversion, transport",
		},
		[]string{"ipv", "transport"},
	)

	collectors = []prometheus.Collector{
		directDialsTotal,
		holePunchOutcomesTotal,
		holePunchNoSuitableAddressTotal,
		publicAddrsCount,
	}
)

type MetricsTracer interface {
	HolePunchFinished(side string, attemptNum int, theirAddrs []ma.Multiaddr, ourAddr []ma.Multiaddr, directConn network.ConnMultiaddrs)
	DirectDialFinished(success bool)
}

type metricsTracer struct {
	addrCounts map[string]map[string]int
}

var _ MetricsTracer = &metricsTracer{}

type metricsTracerSetting struct {
	reg prometheus.Registerer
}

type MetricsTracerOption func(*metricsTracerSetting)

func WithRegisterer(reg prometheus.Registerer) MetricsTracerOption {
	return func(s *metricsTracerSetting) {
		if reg != nil {
			s.reg = reg
		}
	}
}

func NewMetricsTracer(opts ...MetricsTracerOption) MetricsTracer {
	setting := &metricsTracerSetting{reg: prometheus.DefaultRegisterer}
	for _, opt := range opts {
		opt(setting)
	}
	metricshelper.RegisterCollectors(setting.reg, collectors...)
	addrCounts := make(map[string]map[string]int)
	for _, ipv := range []string{"ip4", "ip6", "unknown"} {
		addrCounts[ipv] = make(map[string]int)
	}
	return &metricsTracer{addrCounts: addrCounts}
}

func (mt *metricsTracer) HolePunchFinished(side string, numAttempts int,
	remoteAddrs []ma.Multiaddr, localAddrs []ma.Multiaddr, directConn network.ConnMultiaddrs) {
	tags := metricshelper.GetStringSlice()
	defer metricshelper.PutStringSlice(tags)

	*tags = append(*tags, side, getNumAttemptString(numAttempts))
	var dipv, dtransport string
	if directConn != nil {
		dipv = metricshelper.GetIPVersion(directConn.LocalMultiaddr())
		dtransport = metricshelper.GetTransport(directConn.LocalMultiaddr())
	}

	// Refresh Address Counts
	for _, m := range mt.addrCounts {
		for transport := range m {
			m[transport] = 0
		}
	}

	match := false
	for _, la := range localAddrs {
		lipv := metricshelper.GetIPVersion(la)
		ltransport := metricshelper.GetTransport(la)
		for _, ra := range remoteAddrs {
			ripv := metricshelper.GetIPVersion(ra)
			rtransport := metricshelper.GetTransport(ra)
			if ripv == lipv && rtransport == ltransport {
				match = true
				*tags = append(*tags, ripv, rtransport)
				if directConn != nil && dipv == ripv && dtransport == rtransport {
					*tags = append(*tags, "success")
				} else {
					*tags = append(*tags, "failed")
				}
				holePunchOutcomesTotal.WithLabelValues(*tags...).Inc()
				*tags = (*tags)[:2]
				break
			}
		}
		mt.addrCounts[lipv][ltransport]++
	}

	if !match {
		*tags = (*tags)[:1]
		holePunchNoSuitableAddressTotal.WithLabelValues(*tags...).Inc()
	}

	for ipv, m := range mt.addrCounts {
		for transport, cnt := range m {
			*tags = (*tags)[:0]
			*tags = append(*tags, ipv, transport)
			publicAddrsCount.WithLabelValues(*tags...).Set(float64(cnt))
		}
	}
}

func getNumAttemptString(numAttempt int) string {
	switch numAttempt {
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	default:
		return ">=4"
	}
}

func (mt *metricsTracer) DirectDialFinished(success bool) {
	tags := metricshelper.GetStringSlice()
	defer metricshelper.PutStringSlice(tags)
	if success {
		*tags = append(*tags, "success")
	} else {
		*tags = append(*tags, "failed")
	}
	directDialsTotal.WithLabelValues(*tags...).Inc()
}
