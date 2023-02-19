package metricshelper

import (
	"github.com/prometheus/client_golang/prometheus"
)

// RegisterCollectors registers the collectors with reg ignoring
// reregistration error and panics on any other error
func RegisterCollectors(reg prometheus.Registerer, collectors ...prometheus.Collector) {
	for _, c := range collectors {
		err := reg.Register(c)
		if err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				panic(err)
			}
		}
	}
}

type Registerer interface {
	UseRegisterer(reg prometheus.Registerer)
	Registerer() prometheus.Registerer
}

type MetricsSetting struct {
	reg prometheus.Registerer
}

func (s *MetricsSetting) UseRegisterer(reg prometheus.Registerer) {
	s.reg = reg
}

func (s *MetricsSetting) Registerer() prometheus.Registerer {
	if s.reg == nil {
		return prometheus.DefaultRegisterer
	}
	return s.reg
}

func WithRegisterer[T Registerer](reg prometheus.Registerer) func(T) {
	return func(r T) {
		r.UseRegisterer(reg)
	}
}

func NewTracer[T any, K Registerer](f func() K, collectors ...prometheus.Collector) func(opts ...func(K)) *T {
	return func(opts ...func(K)) *T {
		s := f()
		for _, opt := range opts {
			opt(s)
		}
		reg := s.Registerer()
		RegisterCollectors(reg, collectors...)
		t := new(T)
		return t
	}
}
