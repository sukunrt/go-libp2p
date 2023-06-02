package swarm

import (
	"context"
	"errors"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/transport"
	"github.com/multiformats/go-multiaddr"
	ma "github.com/multiformats/go-multiaddr"
)

func TestBlackHoleWrapperIrrelevantAddress(t *testing.T) {
	ch := make(chan struct{}, 1)
	var f dialfunc = func(ctx context.Context, i peer.ID, m multiaddr.Multiaddr) (transport.CapableConn, error) {
		ch <- struct{}{}
		return nil, nil
	}
	bhd := newBlackHoleWrapper(func(_ ma.Multiaddr) bool { return false },
		10, 10, 1.0, 10, "")
	wrappedF := bhd.Wrap(f)
	addr := ma.StringCast("/ip4/127.0.0.1/tcp/1024")
	for i := 0; i < 1000; i++ {
		wrappedF(context.Background(), "peerID", addr)
		<-ch
	}

}

func TestBlackHoleWrapperReset(t *testing.T) {
	successCh := make(chan struct{}, 1)
	failCh := make(chan struct{}, 1)

	errFailedDial := errors.New("failed dial")
	var f dialfunc = func(ctx context.Context, i peer.ID, m multiaddr.Multiaddr) (transport.CapableConn, error) {
		select {
		case <-successCh:
			return nil, nil
		case <-failCh:
			return nil, errFailedDial
		}
	}
	n := 10
	threshold := 10
	bhd := newBlackHoleWrapper(func(_ ma.Multiaddr) bool { return true },
		n, threshold, 0.2, 100, "")
	wrappedF := bhd.Wrap(f)
	addr := ma.StringCast("/ip4/127.0.0.1/tcp/1024")
	var i = 0
	// calls up to threshold should be allowed
	for i = 1; i <= threshold; i++ {
		failCh <- struct{}{}
		_, err := wrappedF(context.Background(), "peerID", addr)
		if err != errFailedDial {
			t.Fatalf("expected to receive errFailedDial got %s", err)
		}
	}
	// after threshold calls every nth call should be allowed and everything else should
	// be refused
	for i = threshold + 1; i < 1000; i++ {
		select {
		case failCh <- struct{}{}:
		default:
		}
		_, err := wrappedF(context.Background(), "peerID", addr)
		if i%n == 0 {
			if err != errFailedDial {
				t.Fatalf("expected to receive errFailedDial: %s", err)
			}
		} else {
			if err != ErrDialRefusedBlackHole {
				t.Fatalf("epxected blackhole detector to block call: %s", err)
			}
		}
	}
	// remove any left over items from failCh
	select {
	case <-failCh:
	default:
	}
	// on a successful call, the wrapper should reset
	successCh <- struct{}{}
	for i = 1000; ; i++ {
		_, err := wrappedF(context.Background(), "peerID", addr)
		if i%n == 0 {
			// only nth calls are allowed in blocked state
			if err != nil {
				t.Fatalf("expected err nil, got: %s", err)
			}
			break
		} else {
			if err != ErrDialRefusedBlackHole {
				t.Fatalf("epxected blackhole detector to block call: %s", err)
			}
		}
	}
	// check if calls up to threshold are allowed
	for i = 0; i < threshold; i++ {
		failCh <- struct{}{}
		_, err := wrappedF(context.Background(), "peerID", addr)
		if err != errFailedDial {
			t.Fatalf("expected to receive errFailedDial got %s", err)
		}
	}

	// next call should be refused
	_, err := wrappedF(context.Background(), "peerID", addr)
	if err != ErrDialRefusedBlackHole {
		t.Fatalf("expected to receive %s got %s", ErrDialRefusedBlackHole, err)
	}
}

func TestBlackHoleWrapperSuccessFraction(t *testing.T) {
	successCh := make(chan struct{}, 1)
	failCh := make(chan struct{}, 1)

	errFailedDial := errors.New("failed dial")
	var f dialfunc = func(ctx context.Context, i peer.ID, m multiaddr.Multiaddr) (transport.CapableConn, error) {
		select {
		case <-successCh:
			return nil, nil
		case <-failCh:
			return nil, errFailedDial
		}
	}
	n := 100
	threshold := 10
	windowSize := 10
	bhd := newBlackHoleWrapper(func(_ ma.Multiaddr) bool { return true },
		n, threshold, 0.4, windowSize, "")
	wrappedF := bhd.Wrap(f)
	addr := ma.StringCast("/ip4/127.0.0.1/tcp/1024")
	var i = 0
	// 5 success and 5 fails
	for i = 1; i <= 5; i++ {
		successCh <- struct{}{}
		wrappedF(context.Background(), "peerID", addr)
	}
	for i = 1; i <= 5; i++ {
		failCh <- struct{}{}
		wrappedF(context.Background(), "peerID", addr)
	}

	// 4 success and 6 fails
	failCh <- struct{}{}
	_, err := wrappedF(context.Background(), "peerID", addr)
	if err != errFailedDial {
		t.Fatalf("expected to receive errFailedDial: %s", err)
	}

	// 3 success and 7 fails
	failCh <- struct{}{}
	_, err = wrappedF(context.Background(), "peerID", addr)
	if err != errFailedDial {
		t.Fatalf("expected to receive errFailedDial: %s", err)
	}

	// should be blocked
	failCh <- struct{}{}
	_, err = wrappedF(context.Background(), "peerID", addr)
	if err != ErrDialRefusedBlackHole {
		t.Fatalf("expected to receive ErrDialRefusedBlackHole: %s", err)
	}
}
