package swarm

import (
	"context"
	"math"
	"sort"
	"time"

	ma "github.com/multiformats/go-multiaddr"
)

type dialScheduler struct {
	q         []dialTask
	tasks     map[ma.Multiaddr]*taskState
	reqCh     chan dialTask
	st        time.Time
	triggerCh chan struct{}
}

type dialTask struct {
	addr         ma.Multiaddr
	delay        time.Duration
	dialFunc     func()
	isSimConnect bool
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

func newDialScheduler() *dialScheduler {
	return &dialScheduler{
		reqCh:     make(chan dialTask, 1),
		tasks:     make(map[ma.Multiaddr]*taskState),
		triggerCh: make(chan struct{}),
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
	trigger := false
	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan ma.Multiaddr, 1)
	currDials := 0
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
				currDials++
				go func(task dialTask) {
					task.dialFunc()
					select {
					case doneCh <- task.addr:
					case <-ctx.Done():
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
				for x := range ds.tasks {
					log.Errorf("state %s", x)
				}
				log.Errorf("\n")
			} else if !st.isSimConnect && task.isSimConnect && st.status == scheduled {
				st.isSimConnect = true
				st.delay = task.delay
				st.status = scheduled
				for i, a := range ds.q {
					if a.addr.Equal(task.addr) {
						ds.q[i] = task
						break
					}
				}
			} else {
				log.Errorf("dropping %s", task.addr)
			}
			sort.Slice(ds.q, func(i, j int) bool { return ds.q[i].delay < ds.q[j].delay })
		case a := <-doneCh:
			currDials--
			log.Errorf("completed %s", a)
		case <-ds.triggerCh:
			trigger = true
			log.Errorf("triggering dials")
		}
		if timerRunning && !timer.Stop() {
			<-timer.C
		}
		timerRunning = false
		if len(ds.q) > 0 {
			if trigger && currDials == 0 {
				timer.Reset(-1)
			} else {
				timer.Reset(time.Until(ds.st.Add(ds.q[0].delay)))
			}
			timerRunning = true
		}
		trigger = false
	}
}
