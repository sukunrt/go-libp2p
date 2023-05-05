package swarm

import (
	"math"
	"sort"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	ma "github.com/multiformats/go-multiaddr"
)

type dialScheduler struct {
	q            []network.AddrDelay
	pos          map[ma.Multiaddr]int
	ranker       network.DialRanker
	dialCh       chan ma.Multiaddr
	triggerCh    chan struct{}
	reqCh        chan dialSchedule
	timer        *time.Timer
	timerRunning bool
	st           time.Time
}

type dialSchedule struct {
	addrs      []ma.Multiaddr
	simConnect bool
}

func newDialScheduler() *dialScheduler {
	return &dialScheduler{
		dialCh:    make(chan ma.Multiaddr, 1),
		reqCh:     make(chan dialSchedule, 1),
		pos:       make(map[ma.Multiaddr]int),
		triggerCh: make(chan struct{}),
	}
}

func (ds *dialScheduler) triggerNext() {
	select {
	case ds.triggerCh <- struct{}{}:
	default:
	}
}

func (ds *dialScheduler) start() {
	go ds.loop()
}

func (ds *dialScheduler) close() {
	close(ds.reqCh)
}

func (ds *dialScheduler) loop() {
	ds.st = time.Now()
	ds.timer = time.NewTimer(math.MaxInt64)
	ds.timerRunning = true
	trigger := false
	for {
		select {
		case <-ds.timer.C:
			var i int
			for i = 0; i < len(ds.q); i++ {
				if ds.q[i].Delay == ds.q[0].Delay {
					ds.dialCh <- ds.q[i].Addr
					delete(ds.pos, ds.q[i].Addr)
				}
			}
			ds.q = ds.q[i:]
		case req, ok := <-ds.reqCh:
			if !ok {
				return
			}
			var ranking []network.AddrDelay
			if req.simConnect {
				ranking = noDelayRanker(req.addrs)
			} else {
				ranking = defaultDialRanker(req.addrs)
			}
			for _, ad := range ranking {
				pos, ok := ds.pos[ad.Addr]
				if !ok {
					ds.q = append(ds.q, ad)
				}
				if ds.q[pos].Delay < ad.Delay {
					ds.q[pos].Delay = ad.Delay
				}
			}
			sort.Slice(ds.q, func(i, j int) bool { return ds.q[i].Delay < ds.q[j].Delay })
			for i, a := range ds.q {
				ds.pos[a.Addr] = i
			}
		case <-ds.triggerCh:
			trigger = true
		}
		ds.resetTimer(trigger)
		trigger = false
	}
}

func (ds *dialScheduler) resetTimer(trigger bool) {
	if ds.timerRunning && !ds.timer.Stop() {
		<-ds.timer.C
	}
	ds.timerRunning = false
	if len(ds.q) > 0 {
		if trigger {
			ds.timer.Reset(-1)
		} else {
			ds.timer.Reset(time.Until(ds.st.Add(ds.q[0].Delay)))
		}
		ds.timerRunning = true
	}
}
