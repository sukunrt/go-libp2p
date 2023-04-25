package swarm

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	ma "github.com/multiformats/go-multiaddr"
)

// /////////////////////////////////////////////////////////////////////////////////
// lo and behold, The Dialer
// TODO explain how all this works
// ////////////////////////////////////////////////////////////////////////////////

type dialRequest struct {
	ctx   context.Context
	resch chan dialResponse
}

type dialResponse struct {
	conn *Conn
	err  error
}

// pendRequest tracks a pending dial request
type pendRequest struct {
	req dialRequest // the original request
	err *DialError
	// addrs is the set of addresses relevant to this request for which
	// there are pending dials. Request is completed if any dial succeeds or
	// this map is empty.
	addrs map[ma.Multiaddr]struct{}
}

// addrDial tracks dial on a single address.
// we track pendRequests per address and not on dial worker to support direct dial
// requests.
type addrDial struct {
	addr     ma.Multiaddr
	ctx      context.Context
	conn     *Conn
	err      error
	requests []*pendRequest
	delay    time.Duration
}

type dialWorker struct {
	s         *Swarm
	peer      peer.ID
	reqch     <-chan dialRequest
	requests  map[*pendRequest]struct{}
	resch     chan dialResult
	connected bool

	// trackedDials tracks all dials made by the worker loop.
	// an item is only removed from the map in case of a backoff error which is
	// required to support simultaneous connect requests.
	trackedDials map[ma.Multiaddr]*addrDial
	// dialQueue is the list of addresses that will be dialed.
	dialQueue []*network.AddrDelay
	// dialTimer is used to trigger dials to addresses from dialQueue.
	dialTimer *time.Timer
	// currDials is the number of dials inflight.
	currDials int
	// loopStTime is the starting time of dial loop. Delays on addresses in
	// dialQueue are wrt this time.
	loopStTime time.Time
	// timerRunning indicates whether dialTimer is running.
	timerRunning bool

	// for testing
	wg sync.WaitGroup
}

func newDialWorker(s *Swarm, p peer.ID, reqch <-chan dialRequest) *dialWorker {
	w := &dialWorker{
		s:            s,
		peer:         p,
		reqch:        reqch,
		requests:     make(map[*pendRequest]struct{}),
		trackedDials: make(map[ma.Multiaddr]*addrDial),
		resch:        make(chan dialResult),
	}
	return w
}

func (w *dialWorker) loop() {
	w.wg.Add(1)
	defer w.wg.Done()
	defer w.s.limiter.clearAllPeerDials(w.peer)

	w.loopStTime = time.Now()
	w.dialTimer = time.NewTimer(math.MaxInt64)
	w.timerRunning = true
loop:
	for {
		select {
		case req, ok := <-w.reqch:
			if !ok {
				return
			}

			c, err := w.s.bestAcceptableConnToPeer(req.ctx, w.peer)
			if c != nil || err != nil {
				req.resch <- dialResponse{conn: c, err: err}
				continue loop
			}

			addrs, err := w.s.addrsForDial(req.ctx, w.peer)

			if err != nil {
				req.resch <- dialResponse{err: err}
				continue loop
			}

			// at this point, len(addrs) > 0 or else it would be error from addrsForDial

			// rank them to process in order
			simConnect, _, _ := network.GetSimultaneousConnect(req.ctx)
			addrDelays := w.rankAddrs(addrs, simConnect)

			// create the pending request object
			pr := &pendRequest{
				req:   req,
				err:   &DialError{Peer: w.peer},
				addrs: make(map[ma.Multiaddr]struct{}),
			}
			for _, a := range addrDelays {
				pr.addrs[a.Addr] = struct{}{}
			}

			// check if any of the addrs has been successfully dialed and accumulate
			// errors from complete dials while collecting new addrs to dial/join
			tojoin := 0
			newdials := 0
			for _, nad := range addrDelays {
				ad, ok := w.trackedDials[nad.Addr]
				if !ok {
					w.dialQueue = append(w.dialQueue, nad)
					w.trackedDials[nad.Addr] = &addrDial{
						addr:     nad.Addr,
						ctx:      req.ctx,
						requests: []*pendRequest{pr},
						delay:    nad.Delay,
					}
					newdials++
					continue
				}

				// check if this dial has already errored.
				// this dial couldn't have succeeded because bestAcceptableConnToPeer
				// didn't return a connection
				if ad.err != nil {
					// dial to this addr errored, accumulate the error
					pr.err.recordErr(nad.Addr, ad.err)
					delete(pr.addrs, nad.Addr)
					continue
				}

				// dial is still pending
				tojoin++

				// update delay for pending dials
				// we only decrease the delay to not override a simulteneous connect
				// 0 delay with a higher delay for a normal dial request
				if ad.delay > nad.Delay {
					for _, aa := range w.dialQueue {
						if aa.Addr.Equal(nad.Addr) {
							aa.Delay = nad.Delay
							ad.delay = nad.Delay
							break
						}
					}
				}

				// update dial context for simulataneous connect request
				if simConnect, isClient, reason := network.GetSimultaneousConnect(req.ctx); simConnect {
					if simConnect, _, _ := network.GetSimultaneousConnect(ad.ctx); !simConnect {
						ad.ctx = network.WithSimultaneousConnect(ad.ctx, isClient, reason)
					}
				}

				ad.requests = append(ad.requests, pr)
			}

			if newdials+tojoin == 0 {
				// all request applicable addrs have been dialed, we must have errored
				req.resch <- dialResponse{err: pr.err}
				continue loop
			}

			w.requests[pr] = struct{}{}
			sort.Slice(w.dialQueue, func(i, j int) bool { return w.dialQueue[i].Delay < w.dialQueue[j].Delay })
			w.scheduleNextDial()

		case <-w.dialTimer.C:
			// Dial the highest priority addresses without checking
			// delay timer. An early trigger means there are no inflight
			// dials.
			var i int
			for i = 0; i < len(w.dialQueue); i++ {
				a := w.dialQueue[i]
				if a.Delay != w.dialQueue[0].Delay {
					break
				}
				ad := w.trackedDials[a.Addr]
				err := w.s.dialNextAddr(ad.ctx, w.peer, a.Addr, w.resch)
				if err != nil {
					w.dispatchError(ad, err)
				} else {
					w.currDials++
				}
			}
			w.dialQueue = w.dialQueue[i:]
			w.timerRunning = false
			w.scheduleNextDial()

		case res := <-w.resch:
			w.currDials--
			ad := w.trackedDials[res.Addr]

			if res.Conn != nil {
				// we got a connection, add it to the swarm
				conn, err := w.s.addConn(res.Conn, network.DirOutbound)
				if err != nil {
					// oops no, we failed to add it to the swarm
					res.Conn.Close()
					w.dispatchError(ad, err)
					w.scheduleNextDial()
					continue loop
				}
				w.connected = true

				// dispatch to still pending requests
				for _, pr := range ad.requests {
					_, ok := w.requests[pr]
					if !ok {
						// it has already dispatched a connection
						continue
					}
					pr.req.resch <- dialResponse{conn: conn}
					delete(w.requests, pr)
				}

				ad.conn = conn
				ad.requests = nil

				continue loop
			}

			// it must be an error -- add backoff if applicable and dispatch
			if res.Err != context.Canceled && !w.connected {
				// we only add backoff if there has not been a successful connection
				// for consistency with the old dialer behavior.
				w.s.backf.AddBackoff(w.peer, res.Addr)
			}

			w.dispatchError(ad, res.Err)
			w.scheduleNextDial()
		}
	}
}

func (w *dialWorker) scheduleNextDial() {
	if len(w.dialQueue) > 0 {
		d := w.dialQueue[0].Delay
		if w.currDials == 0 {
			// no active dials, dial next address immediately
			d = 0
		}
		if w.timerRunning && !w.dialTimer.Stop() {
			<-w.dialTimer.C
		}
		w.dialTimer.Reset(time.Until(w.loopStTime.Add(d)))
		w.timerRunning = true
	}
}

// dispatches an error to a specific addr dial
func (w *dialWorker) dispatchError(ad *addrDial, err error) {
	ad.err = err
	for _, pr := range ad.requests {
		_, ok := w.requests[pr]
		if !ok {
			// has already been dispatched
			continue
		}

		// accumulate the error
		pr.err.recordErr(ad.addr, err)

		delete(pr.addrs, ad.addr)
		if len(pr.addrs) == 0 {
			// all addrs have erred, dispatch dial error
			// but first do a last one check in case an acceptable connection has landed from
			// a simultaneous dial that started later and added new acceptable addrs
			c, _ := w.s.bestAcceptableConnToPeer(pr.req.ctx, w.peer)
			if c != nil {
				pr.req.resch <- dialResponse{conn: c}
			} else {
				pr.req.resch <- dialResponse{err: pr.err}
			}
			delete(w.requests, pr)
		}
	}

	ad.requests = nil

	// if it was a backoff, clear the address dial so that it doesn't inhibit new dial requests.
	// this is necessary to support active listen scenarios, where a new dial comes in while
	// another dial is in progress, and needs to do a direct connection without inhibitions from
	// dial backoff.
	// it is also necessary to preserve consisent behaviour with the old dialer -- TestDialBackoff
	// regresses without this.
	if err == ErrDialBackoff {
		delete(w.trackedDials, ad.addr)
	}
}

func (w *dialWorker) rankAddrs(addrs []ma.Multiaddr, simConnect bool) []*network.AddrDelay {
	if simConnect {
		return noDelayRanker(addrs)
	}
	return w.s.dialRanker(addrs)
}
