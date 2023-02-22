//go:build nocover

package identify

import (
	"math/rand"
	"testing"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
)

func TestMetricsNoAllocNoCover(t *testing.T) {
	events := []any{
		event.EvtLocalAddressesUpdated{},
		event.EvtLocalProtocolsUpdated{},
		event.EvtNATDeviceTypeChanged{},
	}

	pushSupport := []identifyPushSupport{
		identifyPushSupportUnknown,
		identifyPushSupported,
		identifyPushUnsupported,
	}
	dirs := []network.Direction{network.DirInbound, network.DirOutbound, network.DirUnknown}
	tr := NewMetricsTracer()
	tests := map[string]func(){
		"TriggeredPushes":      func() { tr.TriggeredPushes(events[rand.Intn(len(events))]) },
		"Identify":             func() { tr.Identify(dirs[rand.Intn(len(dirs))]) },
		"IdentifyPush":         func() { tr.IdentifyPush(dirs[rand.Intn(len(dirs))]) },
		"IncrementPushSupport": func() { tr.IncrementPushSupport(pushSupport[rand.Intn(len(pushSupport))]) },
		"DecrementPushSupport": func() { tr.DecrementPushSupport(pushSupport[rand.Intn(len(pushSupport))]) },
		"NumProtocols":         func() { tr.NumProtocols(rand.Intn(100)) },
		"NumAddrs":             func() { tr.NumAddrs(rand.Intn(100)) },
		"NumProtocolsReceived": func() { tr.NumProtocolsReceived(rand.Intn(100)) },
		"NumAddrsReceived":     func() { tr.NumAddrsReceived(rand.Intn(100)) },
	}
	for method, f := range tests {
		allocs := testing.AllocsPerRun(1000, f)
		if allocs > 0 {
			t.Fatalf("Alloc Test: %s, got: %0.2f, expected: 0 allocs", method, allocs)
		}
	}
}
