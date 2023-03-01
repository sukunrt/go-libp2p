package relay

import (
	"time"

	"github.com/libp2p/go-libp2p/p2p/metricshelper"
	"github.com/prometheus/client_golang/prometheus"
)

const metricNamespace = "libp2p_relaysvc"

var (
	status = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "status",
			Help:      "Relay Current Status",
		},
	)

	reservationTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "reservation_total",
			Help:      "Relay Reservation Request",
		},
		[]string{"type"},
	)
	reservationRequestStatusTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "reservation_request_status_total",
			Help:      "Relay Reservation Request Status",
		},
		[]string{"status"},
	)
	reservationRejectedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "reservation_rejected_total",
			Help:      "Relay Reservation Rejected Reason",
		},
		[]string{"reason"},
	)

	connectionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "connection_total",
			Help:      "Relay Connection Total",
		},
		[]string{"type"},
	)
	connectionRequestStatusTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "connection_request_status_total",
			Help:      "Relay Connection Request Status",
		},
		[]string{"status"},
	)
	connectionRejectionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "connection_rejected_total",
			Help:      "Relay Connection Rejected Reason",
		},
		[]string{"reason"},
	)
	connectionDurationSeconds = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Name:      "connection_duration_seconds",
			Help:      "Relay Connection Duration",
		},
	)

	bytesTransferredTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "bytes_transferred_total",
			Help:      "Bytes Transferred Total",
		},
	)

	collectors = []prometheus.Collector{
		status,
		reservationTotal,
		reservationRequestStatusTotal,
		reservationRejectedTotal,
		connectionTotal,
		connectionRequestStatusTotal,
		connectionRejectionTotal,
		connectionDurationSeconds,
		bytesTransferredTotal,
	}
)

const (
	requestStatusOK       = "ok"
	requestStatusRejected = "rejected"
	requestStatusError    = "error"
)

const (
	typeReceived = "received"
	typeOpened   = "opened"
	typeClosed   = "closed"
)

const (
	rejectionReasonAttemptOverRelay      = "attempt over relay"
	rejectionReasonDisallowed            = "disallowed"
	rejectionReasonIPConstraintViolation = "ip constraint violation"
	rejectionReasonResourceLimitExceeded = "resource limit exceeded"
	rejectionReasonBadRequest            = "bad request"
	rejectionReasonNoReservation         = "no reservation"
	rejectionReasonClosed                = "closed"
)

// MetricsTracer is the interface for tracking metrics for relay service
type MetricsTracer interface {
	// RelayStatus tracks whether the service is currently active
	RelayStatus(enabled bool)

	// ConnectionRequestReceived tracks a new relay connect request
	ConnectionRequestReceived()
	// ConnectionOpened tracks metrics on opening a relay connection
	ConnectionOpened()
	// ConnectionClosed tracks metrics on closing a relay connection
	ConnectionClosed(d time.Duration)
	// ConnectionRequestHandled tracks metrics on handling a relay connection request
	// rejectionReason is ignored for status other than `requestStatusRejected`
	ConnectionRequestHandled(status string, rejectionReason string)

	// ReservationRequestReceived tracks a new relay reservation request
	ReservationRequestReceived()
	// ReservationOpened tracks metrics on Opening a relay reservation
	ReservationOpened()
	// ReservationRequestClosed tracks metrics on closing a relay reservation
	ReservationClosed(cnt int)
	// ReservationRequestHandled tracks metrics on handling a relay reservation request
	// rejectionReason is ignored for status other than `requestStatusRejected`
	ReservationRequestHandled(status string, rejectionReason string)

	// BytesTransferred tracks the total bytes transferred(incoming + outgoing) by the relay service
	BytesTransferred(cnt int)
}

type metricsTracer struct{}

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
	return &metricsTracer{}
}

func (mt *metricsTracer) RelayStatus(enabled bool) {
	if enabled {
		status.Set(1)
	} else {
		status.Set(0)
	}
}

func (mt *metricsTracer) ConnectionRequestReceived() {
	connectionTotal.WithLabelValues(typeReceived).Add(1)
}

func (mt *metricsTracer) ConnectionOpened() {
	connectionTotal.WithLabelValues(typeOpened).Add(1)
}

func (mt *metricsTracer) ConnectionClosed(d time.Duration) {
	connectionTotal.WithLabelValues(typeClosed).Add(1)
	connectionDurationSeconds.Observe(d.Seconds())
}

func (mt *metricsTracer) ConnectionRequestHandled(status string, rejectionReason string) {
	connectionRequestStatusTotal.WithLabelValues(status).Add(1)
	if status == requestStatusRejected {
		connectionRejectionTotal.WithLabelValues(rejectionReason).Add(1)
	}
}

func (mt *metricsTracer) ReservationRequestReceived() {
	reservationTotal.WithLabelValues(typeReceived).Add(1)
}

func (mt *metricsTracer) ReservationOpened() {
	reservationTotal.WithLabelValues(typeOpened).Add(1)
}

func (mt *metricsTracer) ReservationClosed(cnt int) {
	reservationTotal.WithLabelValues(typeClosed).Add(float64(cnt))
}

func (mt *metricsTracer) ReservationRequestHandled(status string, rejectionReason string) {
	reservationRequestStatusTotal.WithLabelValues(status).Add(1)
	if status == requestStatusRejected {
		reservationRejectedTotal.WithLabelValues(rejectionReason).Add(1)
	}
}

func (mt *metricsTracer) BytesTransferred(cnt int) {
	bytesTransferredTotal.Add(float64(cnt))
}
