package swarm

import (
	"context"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

type dialScheduler struct {
	q         []dialTask
	tasks     map[ma.Multiaddr]*taskState
	reqCh     chan dialTask
	st        time.Time
	triggerCh chan struct{}
	s         *Swarm
}

type dialTask struct {
	addr         ma.Multiaddr
	delay        time.Duration
	dialFunc     func()
	isSimConnect bool
	peer         peer.ID
	ctx          context.Context
	resCh        chan dialResult
}

type taskStatus int

const (
	scheduled taskStatus = iota
	dialed
	completed
)

type taskState struct {
	status       taskStatus
	delay        time.Duration
	isSimConnect bool
}

func newDialScheduler(s *Swarm) *dialScheduler {
	return &dialScheduler{
		reqCh:     make(chan dialTask, 1),
		tasks:     make(map[ma.Multiaddr]*taskState),
		triggerCh: make(chan struct{}),
		s:         s,
	}
}

func (ds *dialScheduler) start() {
	go ds.loop()
}

func (ds *dialScheduler) close() {
	close(ds.reqCh)
}

func (ds *dialScheduler) maybeTrigger() {
	select {
	case ds.triggerCh <- struct{}{}:
	default:
	}
}

func (ds *dialScheduler) loop() {
	ds.st = time.Now()
	timer := time.NewTimer(math.MaxInt64)
	timerRunning := true
	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan ma.Multiaddr)
	var currDials atomic.Int32
	trigger := true
	for {
		select {
		case <-timer.C:
			var i int
			for i = 0; i < len(ds.q); i++ {
				if ds.q[i].delay != ds.q[0].delay {
					break
				}
				st, ok := ds.tasks[ds.q[i].addr]
				if !ok {
					// shouldn't happen but for safety
					log.Errorf("no dial scheduled for %s", ds.q[i].addr)
					continue
				}
				st.status = dialed
				currDials.Add(-1)
				go func(task dialTask) {
					respCh := make(chan dialResult)
					err := ds.s.dialNextAddr(task.ctx, task.peer, task.addr, respCh)
					var r dialResult
					if err != nil {
						r = dialResult{
							Conn: nil,
							Addr: task.addr,
							Err:  err,
						}
					} else {
						select {
						case r = <-respCh:
						case <-ctx.Done():
							return
						}
					}
					currDials.Add(1)
					select {
					case task.resCh <- r:
					case <-ctx.Done():
						return
					case <-task.ctx.Done():
						return
					}

				}(ds.q[i])
			}
			ds.q = ds.q[i:]
			timerRunning = false
		case task, ok := <-ds.reqCh:
			if !ok {
				cancel()
				return
			}
			st, ok := ds.tasks[task.addr]
			if !ok {
				ds.q = append(ds.q, task)
				ds.tasks[task.addr] = &taskState{
					status:       scheduled,
					delay:        task.delay,
					isSimConnect: task.isSimConnect,
				}
			} else if !st.isSimConnect && task.isSimConnect && st.status != dialed {
				st.isSimConnect = true
				st.delay = task.delay
				st.status = scheduled
				for i, a := range ds.q {
					if a.addr.Equal(task.addr) {
						ds.q[i] = task
						break
					}
				}
			}
			sort.Slice(ds.q, func(i, j int) bool { return ds.q[i].delay < ds.q[j].delay })
		case a := <-doneCh:
			delete(ds.tasks, a)
		case <-ds.triggerCh:
			trigger = true
		}

		if timerRunning && !timer.Stop() {
			<-timer.C
		}
		timerRunning = false
		if len(ds.q) > 0 {
			if trigger && currDials.Load() == 0 {
				timer.Reset(-1)
			} else {
				timer.Reset(time.Until(ds.st.Add(ds.q[0].delay)))
			}
			timerRunning = true
			trigger = false
		}
	}
}
