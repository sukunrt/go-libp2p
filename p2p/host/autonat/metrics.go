package autonat

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/p2p/host/autonat/pb"
	"github.com/libp2p/go-libp2p/p2p/metricshelper"
	"github.com/prometheus/client_golang/prometheus"
)

const metricNamespace = "libp2p_autonat"

var (
	reachabilityStatus = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "reachability_status",
			Help:      "Current node reachability",
		},
	)
	reachabilityStatusConfidence = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "reachability_status_confidnce",
			Help:      "Node reachability status confidence",
		},
	)
	clientDialResponseTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "client_dialresponse_total",
			Help:      "Count of dial responses for client",
		},
		[]string{"response_status"},
	)
	serverDialResponseTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "server_dialresponse_total",
			Help:      "Count of dial responses for server",
		},
		[]string{"response_status"},
	)
	serverDialRefusedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "server_dialrefused_total",
			Help:      "Count of dial requests refused by server",
		},
		[]string{"refusal_reason"},
	)
)

var initMetricsOnce sync.Once

func initMetrics(reg prometheus.Registerer) {
	reg.MustRegister(
		reachabilityStatus,
		reachabilityStatusConfidence,
		clientDialResponseTotal,
		serverDialResponseTotal,
		serverDialRefusedTotal,
	)
}

type MetricsTracer interface {
	ReachabilityStatus(status network.Reachability)
	ReachabilityStatusConfidence(confidence int)
	ClientDialResponse(status pb.Message_ResponseStatus)
	ServerDialResponse(status pb.Message_ResponseStatus)
	ServerDialRefused(reason string)
}

func getResponseStatus(status pb.Message_ResponseStatus) string {
	var s string
	switch status {
	case pb.Message_OK:
		s = "ok"
	case pb.Message_E_DIAL_ERROR:
		s = "dial error"
	case pb.Message_E_DIAL_REFUSED:
		s = "dial refused"
	case pb.Message_E_BAD_REQUEST:
		s = "bad request"
	case pb.Message_E_INTERNAL_ERROR:
		s = "internal error"
	default:
		s = "unknown"
	}
	return s
}

const (
	RATE_LIMIT = "rate limit"
)

type metricsTracerSetting struct {
	reg prometheus.Registerer
}

type metricsTracer struct {
}

var _ MetricsTracer = &metricsTracer{}

func (mt *metricsTracer) ReachabilityStatus(status network.Reachability) {
	reachabilityStatus.Set(float64(status))
}

func (mt *metricsTracer) ReachabilityStatusConfidence(confidence int) {
	reachabilityStatusConfidence.Set(float64(confidence))
}

func (mt *metricsTracer) ClientDialResponse(status pb.Message_ResponseStatus) {
	tags := metricshelper.GetStringSlice()
	defer metricshelper.PutStringSlice(tags)
	*tags = append(*tags, getResponseStatus(status))
	clientDialResponseTotal.WithLabelValues(*tags...).Inc()
}

func (mt *metricsTracer) ServerDialResponse(status pb.Message_ResponseStatus) {
	tags := metricshelper.GetStringSlice()
	defer metricshelper.PutStringSlice(tags)
	*tags = append(*tags, getResponseStatus(status))
	serverDialResponseTotal.WithLabelValues(*tags...).Inc()
}

func (mt *metricsTracer) ServerDialRefused(reason string) {
	tags := metricshelper.GetStringSlice()
	defer metricshelper.PutStringSlice(tags)
	*tags = append(*tags, reason)
	serverDialRefusedTotal.WithLabelValues(*tags...).Inc()
}

type MetricsTracerOption = func(*metricsTracerSetting)

func MustRegisterWith(reg prometheus.Registerer) MetricsTracerOption {
	return func(s *metricsTracerSetting) {
		s.reg = reg
	}
}

func NewMetricsTracer(opts ...MetricsTracerOption) MetricsTracer {
	settings := &metricsTracerSetting{reg: prometheus.DefaultRegisterer}
	for _, opt := range opts {
		opt(settings)
	}
	initMetricsOnce.Do(func() { initMetrics(settings.reg) })
	return &metricsTracer{}
}
