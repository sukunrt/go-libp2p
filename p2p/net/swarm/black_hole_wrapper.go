package swarm

import (
	"context"
	"errors"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/transport"
	ma "github.com/multiformats/go-multiaddr"
)

type outcome int

const (
	outcomeUnknown outcome = iota
	outcomeSuccess
	outcomeFailed
)

type blackholeState int

const (
	blackholeStateAllowed blackholeState = iota
	blackholeStateBlocked
)

var ErrDialRefusedBlackHole error = errors.New("dial refused because of blackhole")

// blackHoleWrapper provides black hole detection for dials to addresses selected by the
// selector.
type blackHoleWrapper struct {
	// selector selects addresses for blackhole detection. Addresses for which selector
	// returns false are ignored
	selector func(addr ma.Multiaddr) bool
	mu       sync.Mutex
	// every nth dial to the address is permitted irrespective of the wrapper status
	n int
	// threshold is the minimum number of completed dials required before dials are blocked
	threshold int
	// minSuccessFraction is the minimum success fraction required to allow dials
	minSuccessFraction float64
	// outcomes is the sliding window of last x outcomes
	outcomes []outcome
	// outcomeIdx is the index of the next outcome
	outcomeIdx                    int
	successCnt, failureCnt, total int
	// full is used to check whether we have a full sliding window worth of outcomes.
	// Keeping this as a separate variable helps avoid clearing out the entire outcomes sliding
	// window on reset.
	full bool
	// state is the current state of the blackhole wrapper
	state blackholeState
	// name of the detector. Useful for debugging
	name string
}

func newBlackHoleWrapper(selector func(addr ma.Multiaddr) bool, n int, threshold int, minSuccessFraction float64, slidingWindowSize int, name string) *blackHoleWrapper {
	return &blackHoleWrapper{
		selector:           selector,
		n:                  n,
		threshold:          threshold,
		minSuccessFraction: minSuccessFraction,
		outcomes:           make([]outcome, slidingWindowSize),
		name:               name,
	}
}

func newIPv6BlackHoleWrapper() *blackHoleWrapper {
	return newBlackHoleWrapper(
		func(addr ma.Multiaddr) bool {
			isIPv6 := false
			ma.ForEach(addr, func(c ma.Component) bool {
				if c.Protocol().Code == ma.P_IP6 {
					isIPv6 = true
				}
				return false
			})
			return isIPv6
		},
		100,
		100,
		0.01,
		1000,
		"IPv6",
	)
}

func newUDPBlackHoleWrapper() *blackHoleWrapper {
	return newBlackHoleWrapper(
		func(addr ma.Multiaddr) bool {
			isUDP := false
			ma.ForEach(addr, func(c ma.Component) bool {
				if c.Protocol().Code == ma.P_UDP {
					isUDP = true
				}
				return true
			})
			return isUDP
		},
		100,
		100,
		0.01,
		1000,
		"UDP",
	)
}

func (b *blackHoleWrapper) Wrap(f dialfunc) dialfunc {
	return func(ctx context.Context, i peer.ID, m ma.Multiaddr) (transport.CapableConn, error) {
		if !b.selector(m) {
			return f(ctx, i, m)
		}

		b.mu.Lock()
		if !b.isAllowed() {
			b.mu.Unlock()
			return nil, ErrDialRefusedBlackHole
		}
		b.mu.Unlock()

		conn, err := f(ctx, i, m)

		b.mu.Lock()
		defer b.mu.Unlock()

		if b.state == blackholeStateBlocked && err == nil {
			b.reset()
			return conn, err
		}

		// discard the earliest outcome
		if b.full {
			switch b.outcomes[b.outcomeIdx] {
			case outcomeSuccess:
				b.successCnt--
			case outcomeFailed:
				b.failureCnt--
			}
		}

		switch {
		case err == nil:
			b.successCnt++
			b.outcomes[b.outcomeIdx] = outcomeSuccess
		case err != nil:
			b.failureCnt++
			b.outcomes[b.outcomeIdx] = outcomeFailed
		}

		b.outcomeIdx++
		if b.outcomeIdx == len(b.outcomes) {
			b.outcomeIdx = 0
			b.full = true
		}
		b.updateState()
		return conn, err
	}
}

func (b *blackHoleWrapper) isAllowed() bool {
	b.total++
	if b.total%b.n == 0 {
		return true
	}
	return b.state == blackholeStateAllowed
}

func (b *blackHoleWrapper) reset() {
	b.successCnt = 0
	b.failureCnt = 0
	b.outcomeIdx = 0
	b.full = false
	b.updateState()
}

func (b *blackHoleWrapper) updateState() {
	st := b.state
	if b.successCnt+b.failureCnt < b.threshold {
		b.state = blackholeStateAllowed
	} else if float64(b.successCnt)/float64(b.successCnt+b.failureCnt) >= b.minSuccessFraction {
		b.state = blackholeStateAllowed
	} else {
		b.state = blackholeStateBlocked
	}
	if st != b.state {
		if b.state == blackholeStateAllowed {
			log.Debugf("blackholeWrapper %s state changed to %s", b.name, "Allowed")
		} else {
			log.Debugf("blackholeWrapper %s state changed to %s", b.name, "Blocked")
		}
	}
}
