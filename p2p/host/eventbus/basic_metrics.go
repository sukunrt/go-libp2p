package eventbus

import (
	"reflect"
	"strings"
	"sync"
	"time"

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
	notificationTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Name:      "notification_time_microseconds",
			Help:      "Time taken to notify all subscribers",
			Buckets:   prometheus.ExponentialBuckets(1.0, 2, 10),
		},
		[]string{"event"},
	)
	numSubscribers = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "num_subscribers",
			Help:      "Number of subscribers for an event type",
		},
		[]string{"event"},
	)
	subscriberQueueLength = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Name:      "subscriber_queue_length",
			Help:      "Subscriber queue length",
			Buckets:   prometheus.ExponentialBuckets(1.0, 2.0, 10),
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

	// NotificationTime is the time taken from calling Emit to the time it
	// took to push the message to all subscriber channels
	NotificationTime(typ reflect.Type, d time.Duration)

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

var initMetricsOnce sync.Once

func initMetrics() {
	prometheus.MustRegister(eventsEmitted, notificationTime, numSubscribers, subscriberQueueLength, subscriberQueueFull, subscriberEventQueued)
}

func NewMetricsTracer() MetricsTracer {
	initMetricsOnce.Do(initMetrics)
	return &metricsTracer{}
}

func (m *metricsTracer) EventEmitted(typ reflect.Type) {
	eventsEmitted.WithLabelValues(strings.TrimPrefix(typ.String(), "event.")).Inc()
}

func (m *metricsTracer) NotificationTime(typ reflect.Type, d time.Duration) {
	notificationTime.WithLabelValues(strings.TrimPrefix(typ.String(), "event.")).Observe(float64(d.Microseconds()))
}

func (m *metricsTracer) AddSubscriber(typ reflect.Type) {
	numSubscribers.WithLabelValues(strings.TrimPrefix(typ.String(), "event.")).Inc()
}

func (m *metricsTracer) RemoveSubscriber(typ reflect.Type) {
	numSubscribers.WithLabelValues(strings.TrimPrefix(typ.String(), "event.")).Dec()
}

func (m *metricsTracer) SubscriberQueueLength(name string, n int) {
	subscriberQueueLength.WithLabelValues(name).Observe(float64(n))
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
