//go:build nocover

package relay

import (
	"math/rand"
	"testing"
	"time"
)

func TestNoCoverNoAlloc(t *testing.T) {
	statuses := []string{requestStatusOK, requestStatusRejected, requestStatusError}
	rejectionReason := []string{"", rejectionReasonAttemptOverRelay, rejectionReasonBadRequest, rejectionReasonDisallowed}
	mt := NewMetricsTracer()
	tests := map[string]func(){
		"RelayStatus":               func() { mt.RelayStatus(rand.Intn(2) == 1) },
		"ConnectionRequestReceived": func() { mt.ConnectionRequestReceived() },
		"ConnectionOpened":          func() { mt.ConnectionOpened() },
		"ConnectionClosed":          func() { mt.ConnectionClosed(time.Duration(rand.Intn(10)) * time.Second) },
		"ConnectionRequestHandled": func() {
			mt.ConnectionRequestHandled(statuses[rand.Intn(len(statuses))], rejectionReason[rand.Intn(len(rejectionReason))])
		},
		"ReservationRequestReceived": func() { mt.ReservationRequestReceived() },
		"ReservationOpened":          func() { mt.ReservationOpened() },
		"ReservationClosed":          func() { mt.ReservationClosed(rand.Intn(10)) },
		"ReservationRequestHandled": func() {
			mt.ReservationRequestHandled(statuses[rand.Intn(len(statuses))], rejectionReason[rand.Intn(len(rejectionReason))])
		},
		"BytesTransferred": func() { mt.BytesTransferred(rand.Intn(1000)) },
	}
	for method, f := range tests {
		allocs := testing.AllocsPerRun(1000, f)
		if allocs > 0 {
			t.Fatalf("Alloc Test: %s, got: %0.2f, expected: 0 allocs", method, allocs)
		}
	}
}
