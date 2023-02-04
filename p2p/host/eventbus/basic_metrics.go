package eventbus

import (
	"reflect"
	"strings"
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
	totalSubscribers = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "subscribers_total",
			Help:      "Number of subscribers for an event type",
		},
		[]string{"event"},
	)
	subscriberQueueLength = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "subscriber_queue_length",
			Help:      "Subscriber queue length",
		},
		[]string{"subscriber_name"},
	)
	subscriberQueueFull = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "subscriber_queue_full",
			Help:      "Subscriber Queue completely full",
		},
		[]string{"subscriber_name"},
	)
	subscriberEventQueued = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "subscriber_event_queued",
			Help:      "Event Queued for subscriber",
		},
		[]string{"subscriber_name"},
	)
)

// MetricsTracer tracks metrics for the eventbus subsystem
type MetricsTracer interface {

	// EventEmitted counts the total number of events grouped by event type
	EventEmitted(typ reflect.Type)

	// AddSubscriber adds a subscriber for the event type
	AddSubscriber(typ reflect.Type)

	// RemoveSubscriber removes a subscriber for the event type
	RemoveSubscriber(typ reflect.Type)

	// SubscriberQueueLength is the length of the subscribers channel
	SubscriberQueueLength(name string, n int)

	// SubscriberQueueFull tracks whether a subscribers channel if full
	SubscriberQueueFull(name string, isFull bool)

	// SubscriberEventQueued counts the total number of events grouped by subscriber
	SubscriberEventQueued(name string)
}

type metricsTracer struct{}

var _ MetricsTracer = &metricsTracer{}

type MetricsTracerOption = func(*metricsTracerSetting)

type metricsTracerSetting struct {
	reg prometheus.Registerer
}

var initMetricsOnce sync.Once

func initMetrics(reg prometheus.Registerer) {
	reg.MustRegister(eventsEmitted, totalSubscribers, subscriberQueueLength, subscriberQueueFull, subscriberEventQueued)
}

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

func (m *metricsTracer) EventEmitted(typ reflect.Type) {
	eventsEmitted.WithLabelValues(strings.TrimPrefix(typ.String(), "event.")).Inc()
}

func (m *metricsTracer) AddSubscriber(typ reflect.Type) {
	totalSubscribers.WithLabelValues(strings.TrimPrefix(typ.String(), "event.")).Inc()
}

func (m *metricsTracer) RemoveSubscriber(typ reflect.Type) {
	totalSubscribers.WithLabelValues(strings.TrimPrefix(typ.String(), "event.")).Dec()
}

func (m *metricsTracer) SubscriberQueueLength(name string, n int) {
	subscriberQueueLength.WithLabelValues(name).Set(float64(n))
}

func (m *metricsTracer) SubscriberQueueFull(name string, isFull bool) {
	observer := subscriberQueueFull.WithLabelValues(name)
	if isFull {
		observer.Set(1)
	} else {
		observer.Set(0)
	}
}

func (m *metricsTracer) SubscriberEventQueued(name string) {
	subscriberEventQueued.WithLabelValues(name).Inc()
}
