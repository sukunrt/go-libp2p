package identify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/record"
	ma "github.com/multiformats/go-multiaddr"
)

var errProtocolNotSupported = errors.New("protocol not supported")

type identifySnapshot struct {
	protocols []string
	addrs     []ma.Multiaddr
	record    *record.Envelope
}

type peerHandler struct {
	ids *idService

	cancel context.CancelFunc

	pid peer.ID

	pushCh chan struct{}
}

func newPeerHandler(pid peer.ID, ids *idService) *peerHandler {
	return &peerHandler{
		ids:    ids,
		pid:    pid,
		pushCh: make(chan struct{}, 1),
	}
}

// start starts a handler. This may only be called on a stopped handler, and must
// not be called concurrently with start/stop.
//
// This may _not_ be called on a _canceled_ handler. I.e., a handler where the
// passed in context expired.
func (ph *peerHandler) start(ctx context.Context, onExit func()) {
	if ph.cancel != nil {
		// If this happens, we have a bug. It means we tried to start
		// before we stopped.
		panic("peer handler already running")
	}

	ctx, cancel := context.WithCancel(ctx)
	ph.cancel = cancel

	go func() {
		ph.loop(ctx)
		onExit()
	}()
}

// stop stops a handler. This may not be called concurrently with any
// other calls to stop/start.
func (ph *peerHandler) stop() error {
	if ph.cancel != nil {
		ph.cancel()
		ph.cancel = nil
	}
	return nil
}

// per peer loop for pushing updates
func (ph *peerHandler) loop(ctx context.Context) {
	for {
		select {
		// our listen addresses have changed, send an IDPush.
		case <-ph.pushCh:
			if err := ph.sendPush(ctx); err != nil {
				log.Warnw("failed to send Identify Push", "peer", ph.pid, "error", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (ph *peerHandler) sendPush(ctx context.Context) error {
	dp, err := ph.openStream(ctx, IDPush)
	if err == errProtocolNotSupported {
		log.Debugw("not sending push as peer does not support protocol", "peer", ph.pid)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to open push stream: %w", err)
	}
	defer dp.Close()

	if err := ph.ids.writeChunkedIdentifyMsg(dp.Conn(), dp); err != nil {
		_ = dp.Reset()
		return fmt.Errorf("failed to send push message: %w", err)
	}
	return nil
}

func (ph *peerHandler) openStream(ctx context.Context, proto string) (network.Stream, error) {
	// wait for the other peer to send us an Identify response on "all" connections we have with it
	// so we can look at it's supported protocols and avoid a multistream-select roundtrip to negotiate the protocol
	// if we know for a fact that it doesn't support the protocol.
	conns := ph.ids.Host.Network().ConnsToPeer(ph.pid)
	for _, c := range conns {
		select {
		case <-ph.ids.IdentifyWait(c):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if sup, err := ph.ids.Host.Peerstore().SupportsProtocols(ph.pid, protocol.ID(proto)); err != nil || len(sup) == 0 {
		return nil, errProtocolNotSupported
	}
	ph.ids.pushSemaphore <- struct{}{}
	defer func() {
		<-ph.ids.pushSemaphore
	}()

	// negotiate a stream without opening a new connection as we "should" already have a connection.
	ctx, cancel := context.WithTimeout(network.WithNoDial(ctx, "should already have connection"), 30*time.Second)
	defer cancel()
	return ph.ids.Host.NewStream(ctx, ph.pid, protocol.ID(proto))
}
