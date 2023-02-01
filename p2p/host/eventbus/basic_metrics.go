package eventbus

import (
	"reflect"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const metricNamespace = "libp2p_eventbus"

var (
	eventsEmitted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "events_emitted_total",
			Help:      "Events Emitted",
		},
		[]string{"event"},
	)
)

type MetricsTracer interface {
	EventEmitted(typ reflect.Type)
}

type metricsTracer struct{}

var _ MetricsTracer = &metricsTracer{}

var initMetricsOnce sync.Once

func initMetrics() {
	prometheus.MustRegister(eventsEmitted)
}

func NewMetricsTracer() MetricsTracer {
	initMetricsOnce.Do(initMetrics)
	return &metricsTracer{}
}

func (m *metricsTracer) EventEmitted(typ reflect.Type) {
	eventsEmitted.WithLabelValues(typ.String()).Inc()
}
