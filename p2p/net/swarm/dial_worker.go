package swarm

import (
	"context"
	"math"
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

type pendRequest struct {
	req   dialRequest               // the original request
	err   *DialError                // dial error accumulator
	addrs map[ma.Multiaddr]struct{} // pending addr dials
}

// addrDial tracks dials to a particular multiaddress.
type addrDial struct {
	// addr is the address dialed
	addr ma.Multiaddr
	// ctx is the context used for dialing the address
	ctx context.Context
	// conn is the established connection on success
	conn *Conn
	// err is the err on dialing the address
	err error
	// requests is the list of dialRequests interested in this dial
	requests []int
	// dialed indicates whether we have triggered the dial to the address
	dialed bool
}

type dialWorker struct {
	s        *Swarm
	peer     peer.ID
	reqch    <-chan dialRequest
	reqno    int
	requests map[int]*pendRequest
	pending  map[ma.Multiaddr]*addrDial
	resch    chan dialResult

	connected bool // true when a connection has been successfully established

	// for testing
	wg sync.WaitGroup
	cl Clock
}

func newDialWorker(s *Swarm, p peer.ID, reqch <-chan dialRequest, cl Clock) *dialWorker {
	if cl == nil {
		cl = RealClock{}
	}
	return &dialWorker{
		s:        s,
		peer:     p,
		reqch:    reqch,
		requests: make(map[int]*pendRequest),
		pending:  make(map[ma.Multiaddr]*addrDial),
		resch:    make(chan dialResult),
		cl:       cl,
	}
}

func (w *dialWorker) loop() {
	w.wg.Add(1)
	defer w.wg.Done()
	defer w.s.limiter.clearAllPeerDials(w.peer)

	// dq is used to pace dials to different addresses of the peer
	dq := newDialQueue()
	// currDials is the number of dials in flight
	currDials := 0
	st := w.cl.Now()
	// timer is the timer used to trigger dials
	timer := w.cl.InstantTimer(st.Add(math.MaxInt64))
	timerRunning := true
	// scheduleNextDial updates timer for triggering the next dial
	scheduleNextDial := func() {
		if timerRunning && !timer.Stop() {
			<-timer.Ch()
		}
		timerRunning = false
		if dq.len() > 0 {
			// if there are no dials in flight, trigger the next dials immediately
			if currDials == 0 {
				timer.Reset(st)
			} else {
				timer.Reset(st.Add(dq.top().Delay))
			}
			timerRunning = true
		}
	}
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
			// ranke them to process in order
			// at this point, len(addrs) > 0 or else it would be error from addrsForDial
			// ranke them to process in order
			simConnect, _, _ := network.GetSimultaneousConnect(req.ctx)
			addrRanking := w.rankAddrs(addrs, simConnect)
			addrDelay := make(map[ma.Multiaddr]time.Duration)

			// create the pending request object
			pr := &pendRequest{
				req:   req,
				err:   &DialError{Peer: w.peer},
				addrs: make(map[ma.Multiaddr]struct{}),
			}
			for _, adelay := range addrRanking {
				pr.addrs[adelay.Addr] = struct{}{}
				addrDelay[adelay.Addr] = adelay.Delay
			}

			// check if any of the addrs has been successfully dialed and accumulate
			// errors from complete dials while collecting new addrs to dial/join
			var todial []ma.Multiaddr
			var tojoin []*addrDial

			for _, adelay := range addrRanking {
				ad, ok := w.pending[adelay.Addr]
				if !ok {
					todial = append(todial, adelay.Addr)
					continue
				}

				if ad.conn != nil {
					// dial to this addr was successful, complete the request
					req.resch <- dialResponse{conn: ad.conn}
					continue loop
				}

				if ad.err != nil {
					// dial to this addr errored, accumulate the error
					pr.err.recordErr(ad.addr, ad.err)
					delete(pr.addrs, ad.addr)
					continue
				}

				// dial is still pending, add to the join list
				tojoin = append(tojoin, ad)
			}

			if len(todial) == 0 && len(tojoin) == 0 {
				// all request applicable addrs have been dialed, we must have errored
				req.resch <- dialResponse{err: pr.err}
				continue loop
			}

			// the request has some pending or new dials, track it and schedule new dials
			w.reqno++
			w.requests[w.reqno] = pr

			for _, ad := range tojoin {
				if !ad.dialed {
					if simConnect, isClient, reason := network.GetSimultaneousConnect(req.ctx); simConnect {
						if simConnect, _, _ := network.GetSimultaneousConnect(ad.ctx); !simConnect {
							ad.ctx = network.WithSimultaneousConnect(ad.ctx, isClient, reason)
							dq.Add(network.AddrDelay{Addr: ad.addr, Delay: addrDelay[ad.addr]})
						}
					}
				}
				ad.requests = append(ad.requests, w.reqno)
			}

			if len(todial) > 0 {
				for _, a := range todial {
					w.pending[a] = &addrDial{addr: a, ctx: req.ctx, requests: []int{w.reqno}}
					dq.Add(network.AddrDelay{Addr: a, Delay: addrDelay[a]})
				}
			}
			scheduleNextDial()

		case <-timer.Ch():
			// we dont check the delay here because an early trigger means all in flight
			// dials have completed
			for _, adelay := range dq.NextBatch() {
				// spawn the dial
				ad := w.pending[adelay.Addr]
				ad.dialed = true
				err := w.s.dialNextAddr(ad.ctx, w.peer, ad.addr, w.resch)
				if err != nil {
					w.dispatchError(ad, err)
				} else {
					currDials++
				}
			}
			timerRunning = false
			scheduleNextDial()

		case res := <-w.resch:
			if res.Conn != nil {
				w.connected = true
			}
			currDials--
			ad := w.pending[res.Addr]

			if res.Conn != nil {
				// we got a connection, add it to the swarm
				conn, err := w.s.addConn(res.Conn, network.DirOutbound)
				if err != nil {
					// oops no, we failed to add it to the swarm
					res.Conn.Close()
					w.dispatchError(ad, err)
					continue loop
				}

				// dispatch to still pending requests
				for _, reqno := range ad.requests {
					pr, ok := w.requests[reqno]
					if !ok {
						// it has already dispatched a connection
						continue
					}

					pr.req.resch <- dialResponse{conn: conn}
					delete(w.requests, reqno)
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
			// only schedule next dial on error
			scheduleNextDial()
		}
	}
}

// dispatches an error to a specific addr dial
func (w *dialWorker) dispatchError(ad *addrDial, err error) {
	ad.err = err
	for _, reqno := range ad.requests {
		pr, ok := w.requests[reqno]
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
			delete(w.requests, reqno)
		}
	}

	ad.requests = nil

	// if it was a backoff, clear the address dial so that it doesn't inhibit new dial requests.
	// this is necessary to support active listen scenarios, where a new dial comes in while
	// another dial is in progress, and needs to do a direct connection without inhibitions from
	// dial backoff.
	if err == ErrDialBackoff {
		delete(w.pending, ad.addr)
	}
}

// rankAddrs ranks addresses for dialing. if it's a simConnect request we
// dial all addresses immediately without any delay
func (w *dialWorker) rankAddrs(addrs []ma.Multiaddr, isSimConnect bool) []network.AddrDelay {
	if isSimConnect {
		return noDelayRanker(addrs)
	}
	return w.s.dialRanker(addrs)
}

// dialQueue is a priority queue used to schedule dials
type dialQueue struct {
	// q is the queue maintained as a heap
	q []network.AddrDelay
}

// newDialQueue returns a new dialQueue
func newDialQueue() *dialQueue {
	return &dialQueue{}
}

// Add adds adelay to the queue. if another elements exists in the queue with
// the same address, it replaces that element.
func (dq *dialQueue) Add(adelay network.AddrDelay) {
	for i := 0; i < dq.len(); i++ {
		if dq.q[i].Addr.Equal(adelay.Addr) {
			if dq.q[i].Delay == adelay.Delay {
				// existing element is the same. nothing to do
				return
			}
			dq.remove(i)
			break
		}
	}
	// i is the position in sorted order of the new element
	var i int
	for i = 0; i < dq.len(); i++ {
		if dq.q[i].Delay > adelay.Delay {
			break
		}
	}
	for ; i < dq.len(); i++ {
		dq.q[i], adelay = adelay, dq.q[i]
	}
	dq.q = append(dq.q, adelay)
}

// NextBatch returns all the elements in the queue with the highest priority
func (dq *dialQueue) NextBatch() []network.AddrDelay {
	if dq.len() == 0 {
		return nil
	}
	res := make([]network.AddrDelay, 0)
	top := dq.top()
	var i int
	for i = 0; i < len(dq.q); i++ {
		if dq.q[i].Delay != top.Delay {
			break
		}
		res = append(res, dq.q[i])
	}
	dq.q = dq.q[i:]
	return res
}

func (dq *dialQueue) remove(i int) {
	for j := i + 1; j < dq.len(); j++ {
		dq.q[j-1] = dq.q[j]
	}
	dq.q = dq.q[:len(dq.q)-1]
}

// len is the length of the queue. Calling top on an empty queue panics.
func (dq *dialQueue) len() int {
	return len(dq.q)
}

// top returns the top element of the queue
func (dq *dialQueue) top() network.AddrDelay {
	return dq.q[0]
}

// pop removes the top element of the queue. This is useful for testing the
// priority queue property
func (dq *dialQueue) pop() network.AddrDelay {
	v := dq.q[0]
	dq.q = dq.q[1:]
	return v
}
