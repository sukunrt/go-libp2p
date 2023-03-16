//go:build nocover

package autorelay

import (
	"math/rand"
	"testing"
	"time"
)

func getRandScheduledWork() scheduledWorkTimes {
	randTime := func() time.Time {
		return time.Now().Add(time.Duration(rand.Intn(10)) * time.Second)
	}
	return scheduledWorkTimes{
		leastFrequentInterval:       0,
		nextRefresh:                 randTime(),
		nextBackoff:                 randTime(),
		nextOldCandidateCheck:       randTime(),
		nextAllowedCallToPeerSource: randTime(),
	}
}

func TestMetricsNoAllocNoCover(t *testing.T) {
	scheduledWork := []scheduledWorkTimes{}
	for i := 0; i < 10; i++ {
		scheduledWork = append(scheduledWork, getRandScheduledWork())
	}
	tr := NewMetricsTracer()
	tests := map[string]func(){
		"RelayFinderStatus":          func() { tr.RelayFinderStatus(rand.Intn(2) == 1) },
		"ReservationEnded":           func() { tr.ReservationEnded() },
		"ReservationRequestFinished": func() { tr.ReservationRequestFinished(rand.Intn(2) == 1, rand.Intn(2) == 1) },
		"RelayAddressCount":          func() { tr.RelayAddressCount(rand.Intn(10)) },
		"RelayAddressUpdated":        func() { tr.RelayAddressUpdated() },
		"CandidateChecked":           func() { tr.CandidateChecked(rand.Intn(2) == 1) },
		"CandidateAdded":             func() { tr.CandidateAdded() },
		"CandidateRemoved":           func() { tr.CandidateRemoved() },
		"ScheduledWorkUpdated":       func() { tr.ScheduledWorkUpdated(&scheduledWork[rand.Intn(len(scheduledWork))]) },
	}
	for method, f := range tests {
		allocs := testing.AllocsPerRun(1000, f)

		if allocs > 0 {
			t.Fatalf("Alloc Test: %s, got: %0.2f, expected: 0 allocs", method, allocs)
		}
	}
}
