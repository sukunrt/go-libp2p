package autorelay

import (
	"github.com/libp2p/go-libp2p/p2p/metricshelper"
	"github.com/prometheus/client_golang/prometheus"
)

const metricNamespace = "libp2p_autorelay"

var (
	status = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricNamespace,
		Name:      "status",
		Help:      "relay finder active",
	})
	reservationsOpenedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "reservations_opened_total",
			Help:      "Reservations Opened",
		},
	)
	reservationsClosedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "reservations_closed_total",
			Help:      "Reservations Closed",
		},
	)
	reservationRequestsOutcomeTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "reservation_requests_outcome_total",
			Help:      "Reservation Request Outcome",
		},
		[]string{"request_type", "outcome"},
	)

	relayAddressesUpdatedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "relay_addresses_updated_total",
			Help:      "Relay Addresses Updated Count",
		},
	)
	relayAddressesCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "relay_addresses_count",
			Help:      "Relay Addresses Count",
		},
	)

	candidatesCircuitV2SupportTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "candidates_circuit_v2_support_total",
			Help:      "Candidiates supporting circuit v2",
		},
		[]string{"support"},
	)
	candidatesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "candidates_total",
			Help:      "Candidates Total",
		},
		[]string{"type"},
	)

	scheduledWorkTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "scheduled_work_time",
			Help:      "Scheduled Work Times",
		},
		[]string{"work_type"},
	)

	collectors = []prometheus.Collector{
		status,
		reservationsOpenedTotal,
		reservationsClosedTotal,
		reservationRequestsOutcomeTotal,
		relayAddressesUpdatedTotal,
		relayAddressesCount,
		candidatesCircuitV2SupportTotal,
		candidatesTotal,
		scheduledWorkTime,
	}
)

// MetricsTracer is the interface for tracking metrics for autorelay
type MetricsTracer interface {
	RelayFinderStatus(isActive bool)

	ReservationEnded()
	ReservationRequestFinished(isRefresh bool, success bool)

	RelayAddressCount(int)
	RelayAddressUpdated()

	CandidateChecked(supportsCircuitV2 bool)
	CandidateAdded()
	CandidateRemoved()

	ScheduledWorkUpdated(scheduledWork *scheduledWorkTimes)
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

	// Initialise these counters to 0 otherwise the first reservation requests aren't handled
	// correctly when using promql increse function
	reservationRequestsOutcomeTotal.WithLabelValues("refresh", "success")
	reservationRequestsOutcomeTotal.WithLabelValues("refresh", "failed")
	reservationRequestsOutcomeTotal.WithLabelValues("new", "success")
	reservationRequestsOutcomeTotal.WithLabelValues("new", "failed")
	return &metricsTracer{}
}

func (mt *metricsTracer) RelayFinderStatus(isActive bool) {
	if isActive {
		status.Set(1)
	} else {
		status.Set(0)
	}
}

func (mt *metricsTracer) ReservationEnded() {
	reservationsClosedTotal.Inc()
}

func (mt *metricsTracer) ReservationRequestFinished(isRefresh bool, success bool) {
	tags := metricshelper.GetStringSlice()
	defer metricshelper.PutStringSlice(tags)

	if isRefresh {
		*tags = append(*tags, "refresh")
	} else {
		*tags = append(*tags, "new")
	}

	if success {
		*tags = append(*tags, "success")
	} else {
		*tags = append(*tags, "failed")
	}
	reservationRequestsOutcomeTotal.WithLabelValues(*tags...).Inc()

	if !isRefresh && success {
		reservationsOpenedTotal.Inc()
	}
}

func (mt *metricsTracer) RelayAddressUpdated() {
	relayAddressesUpdatedTotal.Inc()
}

func (mt *metricsTracer) RelayAddressCount(cnt int) {
	relayAddressesCount.Set(float64(cnt))
}

func (mt *metricsTracer) CandidateChecked(supportsCircuitV2 bool) {
	tags := metricshelper.GetStringSlice()
	defer metricshelper.PutStringSlice(tags)
	if supportsCircuitV2 {
		*tags = append(*tags, "yes")
	} else {
		*tags = append(*tags, "no")
	}
	candidatesCircuitV2SupportTotal.WithLabelValues(*tags...).Inc()
}

func (mt *metricsTracer) CandidateAdded() {
	tags := metricshelper.GetStringSlice()
	defer metricshelper.PutStringSlice(tags)
	*tags = append(*tags, "added")
	candidatesTotal.WithLabelValues(*tags...).Inc()
}

func (mt *metricsTracer) CandidateRemoved() {
	tags := metricshelper.GetStringSlice()
	defer metricshelper.PutStringSlice(tags)
	*tags = append(*tags, "removed")
	candidatesTotal.WithLabelValues(*tags...).Inc()
}

func (mt *metricsTracer) ScheduledWorkUpdated(scheduledWork *scheduledWorkTimes) {
	tags := metricshelper.GetStringSlice()
	defer metricshelper.PutStringSlice(tags)

	*tags = append(*tags, "allowed peer source call")
	scheduledWorkTime.WithLabelValues(*tags...).Set(float64(scheduledWork.nextAllowedCallToPeerSource.Unix()))
	*tags = (*tags)[:0]

	*tags = append(*tags, "reservation refresh")
	scheduledWorkTime.WithLabelValues(*tags...).Set(float64(scheduledWork.nextRefresh.Unix()))
	*tags = (*tags)[:0]

	*tags = append(*tags, "clear backoff")
	scheduledWorkTime.WithLabelValues(*tags...).Set(float64(scheduledWork.nextBackoff.Unix()))
	*tags = (*tags)[:0]

	*tags = append(*tags, "old candidate check")
	scheduledWorkTime.WithLabelValues(*tags...).Set(float64(scheduledWork.nextOldCandidateCheck.Unix()))
}

// wrappedMetricsTracer wraps MetricsTracer and ignores all calls when mt is nil
type wrappedMetricsTracer struct {
	mt MetricsTracer
}

var _ MetricsTracer = &wrappedMetricsTracer{}

func (mt *wrappedMetricsTracer) RelayFinderStatus(isActive bool) {
	if isActive {
		status.Set(1)
	} else {
		status.Set(2)
	}
}

func (mt *wrappedMetricsTracer) ReservationEnded() {
	if mt.mt != nil {
		mt.mt.ReservationEnded()
	}
}

func (mt *wrappedMetricsTracer) ReservationRequestFinished(isRefresh bool, success bool) {
	if mt.mt != nil {
		mt.mt.ReservationRequestFinished(isRefresh, success)
	}
}

func (mt *wrappedMetricsTracer) RelayAddressUpdated() {
	if mt.mt != nil {
		mt.mt.RelayAddressUpdated()
	}
}

func (mt *wrappedMetricsTracer) RelayAddressCount(cnt int) {
	if mt.mt != nil {
		mt.mt.RelayAddressCount(cnt)
	}
}

func (mt *wrappedMetricsTracer) CandidateChecked(supportsCircuitV2 bool) {
	if mt.mt != nil {
		mt.mt.CandidateChecked(supportsCircuitV2)
	}
}

func (mt *wrappedMetricsTracer) CandidateAdded() {
	if mt.mt != nil {
		mt.mt.CandidateAdded()
	}
}

func (mt *wrappedMetricsTracer) CandidateRemoved() {
	if mt.mt != nil {
		mt.mt.CandidateRemoved()
	}
}

func (mt *wrappedMetricsTracer) ScheduledWorkUpdated(scheduledWork *scheduledWorkTimes) {
	if mt.mt != nil {
		mt.mt.ScheduledWorkUpdated(scheduledWork)
	}
}
