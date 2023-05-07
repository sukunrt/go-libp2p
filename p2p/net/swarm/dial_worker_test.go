package swarm

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	mrand "math/rand"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/sec"
	"github.com/libp2p/go-libp2p/core/sec/insecure"
	"github.com/libp2p/go-libp2p/core/test"
	"github.com/libp2p/go-libp2p/core/transport"
	"github.com/libp2p/go-libp2p/p2p/host/eventbus"
	"github.com/libp2p/go-libp2p/p2p/host/peerstore/pstoremem"
	"github.com/libp2p/go-libp2p/p2p/muxer/yamux"
	tptu "github.com/libp2p/go-libp2p/p2p/net/upgrader"
	quic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/quicreuse"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"

	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/stretchr/testify/require"
)

type mockClock struct {
	*test.MockClock
}

func (m *mockClock) InstantTimer(when time.Time) InstantTimer {
	return m.MockClock.InstantTimer(when)
}

func newMockClock() *mockClock {
	return &mockClock{test.NewMockClock()}
}

func newPeer(t *testing.T) (crypto.PrivKey, peer.ID) {
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	id, err := peer.IDFromPrivateKey(priv)
	require.NoError(t, err)
	return priv, id
}

func makeSwarm(t *testing.T) *Swarm {
	s := makeSwarmWithNoListenAddrs(t, WithDialTimeout(1*time.Second))
	if err := s.Listen(ma.StringCast("/ip4/127.0.0.1/tcp/0")); err != nil {
		t.Fatal(err)
	}

	if err := s.Listen(ma.StringCast("/ip4/127.0.0.1/udp/0/quic")); err != nil {
		t.Fatal(err)
	}

	return s
}

func makeSwarmWithNoListenAddrs(t *testing.T, opts ...Option) *Swarm {
	priv, id := newPeer(t)

	ps, err := pstoremem.NewPeerstore()
	require.NoError(t, err)
	ps.AddPubKey(id, priv.GetPublic())
	ps.AddPrivKey(id, priv)
	t.Cleanup(func() { ps.Close() })

	s, err := NewSwarm(id, ps, eventbus.NewBus(), opts...)
	require.NoError(t, err)

	upgrader := makeUpgrader(t, s)
	var tcpOpts []tcp.Option
	tcpOpts = append(tcpOpts, tcp.DisableReuseport())
	tcpTransport, err := tcp.NewTCPTransport(upgrader, nil, tcpOpts...)
	require.NoError(t, err)
	if err := s.AddTransport(tcpTransport); err != nil {
		t.Fatal(err)
	}
	reuse, err := quicreuse.NewConnManager([32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	quicTransport, err := quic.NewTransport(priv, reuse, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddTransport(quicTransport); err != nil {
		t.Fatal(err)
	}
	return s
}

func makeUpgrader(t *testing.T, n *Swarm) transport.Upgrader {
	id := n.LocalPeer()
	pk := n.Peerstore().PrivKey(id)
	st := insecure.NewWithIdentity(insecure.ID, id, pk)

	u, err := tptu.New([]sec.SecureTransport{st}, []tptu.StreamMuxer{{ID: yamux.ID, Muxer: yamux.DefaultTransport}}, nil, nil, nil)
	require.NoError(t, err)
	return u
}

// makeTCPListener listens on tcp address a. Sending a message to channel ch will close
// an accepted connection
func makeTCPListener(t *testing.T, a ma.Multiaddr) (list manet.Listener, ch chan struct{}) {
	t.Helper()
	list, err := manet.Listen(a)
	if err != nil {
		t.Error(err)
	}
	ch = make(chan struct{})
	go func() {
		for {
			c, err := list.Accept()
			if err != nil {
				break
			}
			<-ch
			err = c.Close()
			if err != nil {
				t.Error(err)
			}

		}
	}()
	return list, ch
}

func TestDialWorkerLoopBasic(t *testing.T) {
	s1 := makeSwarm(t)
	s2 := makeSwarm(t)
	defer s1.Close()
	defer s2.Close()

	// Only pass in a single address here, otherwise we might end up with a TCP and QUIC connection dialed.
	s1.Peerstore().AddAddrs(s2.LocalPeer(), []ma.Multiaddr{s2.ListenAddresses()[0]}, peerstore.PermanentAddrTTL)

	reqch := make(chan dialRequest)
	resch := make(chan dialResponse)
	worker := newDialWorker(s1, s2.LocalPeer(), reqch, nil)
	go worker.loop()

	var conn *Conn
	reqch <- dialRequest{ctx: context.Background(), resch: resch}
	select {
	case res := <-resch:
		require.NoError(t, res.err)
		conn = res.conn
	case <-time.After(10 * time.Second):
		t.Fatal("dial didn't complete")
	}

	s, err := conn.NewStream(context.Background())
	require.NoError(t, err)
	s.Close()

	var conn2 *Conn
	reqch <- dialRequest{ctx: context.Background(), resch: resch}
	select {
	case res := <-resch:
		require.NoError(t, res.err)
		conn2 = res.conn
	case <-time.After(10 * time.Second):
		t.Fatal("dial didn't complete")
	}

	// can't use require.Equal here, as this does a deep comparison
	if conn != conn2 {
		t.Fatalf("expecting the same connection from both dials. %s <-> %s vs. %s <-> %s", conn.LocalMultiaddr(), conn.RemoteMultiaddr(), conn2.LocalMultiaddr(), conn2.RemoteMultiaddr())
	}

	close(reqch)
	worker.wg.Wait()
}

func TestDialWorkerLoopConcurrent(t *testing.T) {
	s1 := makeSwarm(t)
	s2 := makeSwarm(t)
	defer s1.Close()
	defer s2.Close()

	s1.Peerstore().AddAddrs(s2.LocalPeer(), s2.ListenAddresses(), peerstore.PermanentAddrTTL)

	reqch := make(chan dialRequest)
	worker := newDialWorker(s1, s2.LocalPeer(), reqch, nil)
	go worker.loop()

	const dials = 100
	var wg sync.WaitGroup
	resch := make(chan dialResponse, dials)
	for i := 0; i < dials; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reschgo := make(chan dialResponse, 1)
			reqch <- dialRequest{ctx: context.Background(), resch: reschgo}
			select {
			case res := <-reschgo:
				resch <- res
			case <-time.After(time.Minute):
				resch <- dialResponse{err: errors.New("timed out!")}
			}
		}()
	}
	wg.Wait()

	for i := 0; i < dials; i++ {
		res := <-resch
		require.NoError(t, res.err)
	}

	t.Log("all concurrent dials done")

	close(reqch)
	worker.wg.Wait()
}

func TestDialWorkerLoopFailure(t *testing.T) {
	s1 := makeSwarm(t)
	defer s1.Close()

	_, p2 := newPeer(t)

	s1.Peerstore().AddAddrs(p2, []ma.Multiaddr{ma.StringCast("/ip4/11.0.0.1/tcp/1234"), ma.StringCast("/ip4/11.0.0.1/udp/1234/quic")}, peerstore.PermanentAddrTTL)

	reqch := make(chan dialRequest)
	resch := make(chan dialResponse)
	worker := newDialWorker(s1, p2, reqch, nil)
	go worker.loop()

	reqch <- dialRequest{ctx: context.Background(), resch: resch}
	select {
	case res := <-resch:
		require.Error(t, res.err)
	case <-time.After(time.Minute):
		t.Fatal("dial didn't complete")
	}

	close(reqch)
	worker.wg.Wait()
}

func TestDialWorkerLoopConcurrentFailure(t *testing.T) {
	s1 := makeSwarm(t)
	defer s1.Close()

	_, p2 := newPeer(t)

	s1.Peerstore().AddAddrs(p2, []ma.Multiaddr{ma.StringCast("/ip4/11.0.0.1/tcp/1234"), ma.StringCast("/ip4/11.0.0.1/udp/1234/quic")}, peerstore.PermanentAddrTTL)

	reqch := make(chan dialRequest)
	worker := newDialWorker(s1, p2, reqch, nil)
	go worker.loop()

	const dials = 100
	var errTimeout = errors.New("timed out!")
	var wg sync.WaitGroup
	resch := make(chan dialResponse, dials)
	for i := 0; i < dials; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reschgo := make(chan dialResponse, 1)
			reqch <- dialRequest{ctx: context.Background(), resch: reschgo}

			select {
			case res := <-reschgo:
				resch <- res
			case <-time.After(time.Minute):
				resch <- dialResponse{err: errTimeout}
			}
		}()
	}
	wg.Wait()

	for i := 0; i < dials; i++ {
		res := <-resch
		require.Error(t, res.err)
		if res.err == errTimeout {
			t.Fatal("dial response timed out")
		}
	}

	t.Log("all concurrent dials done")

	close(reqch)
	worker.wg.Wait()
}

func TestDialWorkerLoopConcurrentMix(t *testing.T) {
	s1 := makeSwarm(t)
	s2 := makeSwarm(t)
	defer s1.Close()
	defer s2.Close()

	s1.Peerstore().AddAddrs(s2.LocalPeer(), s2.ListenAddresses(), peerstore.PermanentAddrTTL)
	s1.Peerstore().AddAddrs(s2.LocalPeer(), []ma.Multiaddr{ma.StringCast("/ip4/11.0.0.1/tcp/1234"), ma.StringCast("/ip4/11.0.0.1/udp/1234/quic")}, peerstore.PermanentAddrTTL)

	reqch := make(chan dialRequest)
	worker := newDialWorker(s1, s2.LocalPeer(), reqch, nil)
	go worker.loop()

	const dials = 100
	var wg sync.WaitGroup
	resch := make(chan dialResponse, dials)
	for i := 0; i < dials; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reschgo := make(chan dialResponse, 1)
			reqch <- dialRequest{ctx: context.Background(), resch: reschgo}
			select {
			case res := <-reschgo:
				resch <- res
			case <-time.After(time.Minute):
				resch <- dialResponse{err: errors.New("timed out!")}
			}
		}()
	}
	wg.Wait()

	for i := 0; i < dials; i++ {
		res := <-resch
		require.NoError(t, res.err)
	}

	t.Log("all concurrent dials done")

	close(reqch)
	worker.wg.Wait()
}

func TestDialWorkerLoopConcurrentFailureStress(t *testing.T) {
	s1 := makeSwarm(t)
	defer s1.Close()

	_, p2 := newPeer(t)

	var addrs []ma.Multiaddr
	for i := 0; i < 16; i++ {
		addrs = append(addrs, ma.StringCast(fmt.Sprintf("/ip4/11.0.0.%d/tcp/%d", i%256, 1234+i)))
	}
	s1.Peerstore().AddAddrs(p2, addrs, peerstore.PermanentAddrTTL)

	reqch := make(chan dialRequest)
	worker := newDialWorker(s1, p2, reqch, nil)
	go worker.loop()

	const dials = 100
	var errTimeout = errors.New("timed out!")
	var wg sync.WaitGroup
	resch := make(chan dialResponse, dials)
	for i := 0; i < dials; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reschgo := make(chan dialResponse, 1)
			reqch <- dialRequest{ctx: context.Background(), resch: reschgo}
			select {
			case res := <-reschgo:
				t.Log("received result")
				resch <- res
			case <-time.After(15 * time.Second):
				resch <- dialResponse{err: errTimeout}
			}
		}()
	}
	wg.Wait()

	for i := 0; i < dials; i++ {
		res := <-resch
		require.Error(t, res.err)
		if res.err == errTimeout {
			t.Fatal("dial response timed out")
		}
	}

	t.Log("all concurrent dials done")

	close(reqch)
	worker.wg.Wait()
}

// timedDial is a dial to a single address of the peer
type timedDial struct {
	// addr is the address to dial
	addr ma.Multiaddr
	// delay is the delay after which this address should be dialed
	delay time.Duration
	// success indicates whether the dial should succeed
	success bool
	// failAfter is the time after which this dial should fail
	failAfter time.Duration
}

// dialState is used to track the dials for testing dialWorker ranking logic
type dialState struct {
	// ch is the chan used to trigger dial failure.
	ch chan struct{}
	// at is the time at which this dial should fail
	at time.Time
	// addr is the address of the dial
	addr ma.Multiaddr
	// delay is the delay after which this address should be dialed
	delay time.Duration
	// success indicates whether the dial should succeed
	success bool
	// failAfter is the timer after which this dial should fail
	failAfter time.Duration
}

func TestDialWorkerLoopRanking(t *testing.T) {
	addrs := make([]ma.Multiaddr, 0)
	ports := make(map[int]struct{})
	for i := 0; i < 10; i++ {
		for {
			p := 5000 + mrand.Intn(10000)
			if _, ok := ports[p]; ok {
				continue
			}
			ports[p] = struct{}{}
			addrs = append(addrs, ma.StringCast(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", p)))
			break
		}
	}

	// makeRanker takes a slice of timedDial objects and returns a DialRanker
	// which will trigger dials to addresses at the specified delays in the timedDials
	makeRanker := func(tc []timedDial) network.DialRanker {
		return func(addrs []ma.Multiaddr) []network.AddrDelay {
			res := make([]network.AddrDelay, len(tc))
			for i := 0; i < len(tc); i++ {
				res[i] = network.AddrDelay{Addr: tc[i].addr, Delay: tc[i].delay}
			}
			return res
		}
	}

	testcases := []struct {
		name        string
		input       []timedDial
		maxDuration time.Duration
	}{
		{
			name: "first success",
			input: []timedDial{
				{
					addr:    addrs[1],
					delay:   0,
					success: true,
				},
				{
					addr:      addrs[0],
					delay:     100 * time.Millisecond,
					success:   false,
					failAfter: 50 * time.Millisecond,
				},
			},
			maxDuration: 20 * time.Millisecond,
		},
		{
			name: "delayed dials",
			input: []timedDial{
				{
					addr:      addrs[0],
					delay:     0,
					success:   false,
					failAfter: 5 * time.Millisecond,
				},
				{
					addr:      addrs[1],
					delay:     100 * time.Millisecond,
					success:   false,
					failAfter: 105 * time.Millisecond,
				},
				{
					addr:      addrs[2],
					delay:     1 * time.Second,
					success:   false,
					failAfter: 10 * time.Millisecond,
				},
				{
					addr:    addrs[3],
					delay:   2 * time.Second,
					success: true,
				},
				{
					addr:    addrs[4],
					delay:   2*time.Second + 1*time.Millisecond,
					success: true,
				},
			},
			maxDuration: 200 * time.Millisecond,
		},
		{
			name: "delayed dials 2",
			input: []timedDial{
				{
					addr:      addrs[0],
					delay:     0,
					success:   false,
					failAfter: 105 * time.Millisecond,
				},
				{
					addr:      addrs[1],
					delay:     100 * time.Millisecond,
					success:   false,
					failAfter: 20 * time.Millisecond,
				},
				{
					addr:      addrs[2],
					delay:     1 * time.Second,
					success:   false,
					failAfter: 10 * time.Millisecond,
				},
				{
					addr:    addrs[3],
					delay:   2 * time.Second,
					success: true,
				},
			},
			maxDuration: 200 * time.Millisecond,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			s1 := makeSwarmWithNoListenAddrs(t)
			defer s1.Close()
			s2 := makeSwarmWithNoListenAddrs(t)
			defer s2.Close()

			// failDials is used to track dials which should fail in the future
			// at appropriate moment a message is sent to dialState.ch to trigger
			// failure
			failDials := make(map[ma.Multiaddr]dialState)
			// allDials tracks all pending dials
			allDials := make(map[ma.Multiaddr]dialState)
			addrs := make([]ma.Multiaddr, 0)
			for _, inp := range tc.input {
				var failCh chan struct{}
				if inp.success {
					// add the address as a listen address if this dial should succeed
					err := s2.AddListenAddr(inp.addr)
					if err != nil {
						t.Errorf("failed to listen on %s %s", inp.addr, err)
					}
				} else {
					// make a listener which will fail on sending a message to ch
					l, ch := makeTCPListener(t, inp.addr)
					f := func() {
						err := l.Close()
						if err != nil {
							t.Error(err)
						}
					}
					failCh = ch
					t.Cleanup(f)
				}
				addrs = append(addrs, inp.addr)
				// add to pending dials
				allDials[inp.addr] = dialState{
					ch:        failCh,
					addr:      inp.addr,
					delay:     inp.delay,
					success:   inp.success,
					failAfter: inp.failAfter,
				}
			}
			// setup the peers addresses
			s1.Peerstore().AddAddrs(s2.LocalPeer(), addrs, peerstore.PermanentAddrTTL)

			// setup the ranker to trigger dials appropriately
			s1.dialRanker = makeRanker(tc.input)

			reqch := make(chan dialRequest)
			resch := make(chan dialResponse)
			cl := newMockClock()
			st := cl.Now()
			worker1 := newDialWorker(s1, s2.LocalPeer(), reqch, cl)
			go worker1.loop()
			defer worker1.wg.Wait()
			// trigger the request
			reqch <- dialRequest{ctx: context.Background(), resch: resch}

			// we advance the clock by 10 ms every iteration
			// at every iteration:
			// check if any dial should fail. if it should, trigger the failure
			// if there are no dials in flight check the most urgent dials
			// if there are dials in flight check that the relevant dials have been triggered
		loop:
			for {
				// fail any dials that should fail at this instant
				for a, p := range failDials {
					if p.at.Before(cl.Now()) {
						p.ch <- struct{}{}
						delete(failDials, a)
					}
				}
				// if there are no pending dials, next dial should have been triggered
				trigger := len(failDials) == 0

				// mi is the minDelay of pending dials
				// if trigger is true, all dials with miDelay should have been triggered
				mi := time.Duration(math.MaxInt64)
				for _, ds := range allDials {
					if ds.delay < mi {
						mi = ds.delay
					}
				}
				for a, ds := range allDials {
					if (trigger && mi == ds.delay) || cl.Now().After(st.Add(ds.delay)) {
						delete(allDials, a)
						if ds.success {
							// check for success and exit
							select {
							case r := <-resch:
								if r.conn == nil {
									t.Error("expected a connection")
								}
							case <-time.After(1 * time.Second):
								t.Error("expected a connection")
							}
							break loop
						} else {
							// ensure that a failing dial didn't succeed
							select {
							case <-resch:
								t.Error("didn't expect a connection")
							case <-time.After(100 * time.Millisecond):
							}
							failDials[a] = dialState{
								ch:    ds.ch,
								at:    cl.Now().Add(ds.failAfter),
								addr:  a,
								delay: ds.delay,
							}
						}
					}
				}
				// advance the clock
				cl.AdvanceBy(10 * time.Millisecond)

				// nothing more to do. exit
				if len(failDials) == 0 && len(allDials) == 0 {
					break
				}
			}
			// ensure we don't receive any extra connections
			select {
			case <-resch:
				t.Error("didn't expect a connection")
			case <-time.After(100 * time.Millisecond):
			}
			// check if this test didn't take too much time
			if cl.Now().Sub(st) > tc.maxDuration {
				t.Errorf("expected test to finish early: expected %d, took: %d", tc.maxDuration, cl.Now().Sub(st))
			}
			close(reqch)
		})
	}

}

func TestDialQueuePriority(t *testing.T) {
	addrs := make([]ma.Multiaddr, 0)
	ports := make(map[int]struct{})
	for i := 0; i < 10; i++ {
		for {
			p := 1 + mrand.Intn(10000)
			if _, ok := ports[p]; ok {
				continue
			}
			ports[p] = struct{}{}
			addrs = append(addrs, ma.StringCast(fmt.Sprintf("/ip4/1.2.3.4/tcp/%d", p)))
			break
		}
	}
	testcase := []struct {
		name   string
		input  []network.AddrDelay
		output []ma.Multiaddr
	}{
		{
			name: "priority queue property",
			input: []network.AddrDelay{
				{Addr: addrs[0], Delay: 100},
				{Addr: addrs[1], Delay: 200},
				{Addr: addrs[2], Delay: 20},
			},
			output: []ma.Multiaddr{
				addrs[2], addrs[0], addrs[1],
			},
		},
		{
			name: "priority queue property 2",
			input: []network.AddrDelay{
				{Addr: addrs[0], Delay: 8},
				{Addr: addrs[1], Delay: 9},
				{Addr: addrs[2], Delay: 6},
				{Addr: addrs[3], Delay: 7},
				{Addr: addrs[4], Delay: 4},
				{Addr: addrs[5], Delay: 5},
				{Addr: addrs[6], Delay: 2},
				{Addr: addrs[7], Delay: 3},
				{Addr: addrs[8], Delay: 1},
			},
			output: []ma.Multiaddr{
				addrs[8], addrs[6], addrs[7], addrs[4], addrs[5], addrs[2], addrs[3], addrs[0], addrs[1],
			},
		},
		{
			name: "updates",
			input: []network.AddrDelay{
				{Addr: addrs[0], Delay: 5},
				{Addr: addrs[1], Delay: 4},
				{Addr: addrs[2], Delay: 3},
				{Addr: addrs[3], Delay: 2},
				{Addr: addrs[4], Delay: 1},
				{Addr: addrs[0], Delay: 1},
				{Addr: addrs[1], Delay: 2},
				{Addr: addrs[2], Delay: 3},
				{Addr: addrs[3], Delay: 4},
				{Addr: addrs[4], Delay: 5},
				{Addr: addrs[3], Delay: 0},
			},
			output: []ma.Multiaddr{
				addrs[3], addrs[0], addrs[1], addrs[2], addrs[4],
			},
		},
	}
	for _, tc := range testcase {
		t.Run(tc.name, func(t *testing.T) {
			q := newDialQueue()
			for i := 0; i < len(tc.input); i++ {
				q.add(tc.input[i])
			}
			for i := 0; i < len(tc.output); i++ {
				v := q.pop()
				if !tc.output[i].Equal(v.Addr) {
					t.Errorf("failed priority queue property: expected: %s got: %s", tc.output[i], v.Addr)
				}
			}
			if q.len() != 0 {
				t.Errorf("expected queue to be empty at end. got: %d", q.len())
			}
		})
	}
}

func TestDialQueueNextBatch(t *testing.T) {
	addrs := make([]ma.Multiaddr, 0)
	for i := 0; i < 10; i++ {
		addrs = append(addrs, ma.StringCast(fmt.Sprintf("/ip4/1.2.3.4/tcp/%d", i)))
	}
	testcase := []struct {
		name   string
		input  []network.AddrDelay
		output [][]ma.Multiaddr
	}{
		{
			name: "next batch",
			input: []network.AddrDelay{
				{Addr: addrs[0], Delay: 3},
				{Addr: addrs[1], Delay: 2},
				{Addr: addrs[2], Delay: 1},
				{Addr: addrs[3], Delay: 1},
			},
			output: [][]ma.Multiaddr{
				{addrs[2], addrs[3]},
				{addrs[1]},
				{addrs[0]},
			},
		},
		{
			name: "priority queue property 2",
			input: []network.AddrDelay{
				{Addr: addrs[0], Delay: 5},
				{Addr: addrs[1], Delay: 3},
				{Addr: addrs[2], Delay: 2},
				{Addr: addrs[3], Delay: 1},
				{Addr: addrs[4], Delay: 1},
			},

			output: [][]ma.Multiaddr{
				{addrs[3], addrs[4]},
				{addrs[2]},
				{addrs[1]},
				{addrs[0]},
			},
		},
		{
			name: "updates",
			input: []network.AddrDelay{
				{Addr: addrs[0], Delay: 5}, // is last
				{Addr: addrs[1], Delay: 4},
				{Addr: addrs[2], Delay: 1},
				{Addr: addrs[0], Delay: 0}, // is not first
				{Addr: addrs[1], Delay: 5},
				{Addr: addrs[3], Delay: 5},
			},
			output: [][]ma.Multiaddr{
				{addrs[0]},
				{addrs[2]},
				{addrs[1], addrs[3]},
			},
		},
		{
			name:  "null input",
			input: []network.AddrDelay{},
			output: [][]ma.Multiaddr{
				{},
				{},
			},
		},
	}
	for _, tc := range testcase {
		t.Run(tc.name, func(t *testing.T) {
			q := newDialQueue()
			for i := 0; i < len(tc.input); i++ {
				q.add(tc.input[i])
			}
			for _, batch := range tc.output {
				b := q.nextBatch()
				if len(batch) != len(b) {
					t.Errorf("expected %d elements got %d", len(batch), len(b))
				}
				sort.Slice(b, func(i, j int) bool { return b[i].Addr.String() < b[j].Addr.String() })
				sort.Slice(batch, func(i, j int) bool { return batch[i].String() < batch[j].String() })
				for i := 0; i < len(b); i++ {
					if !b[i].Addr.Equal(batch[i]) {
						log.Errorf("expected %s got %s", batch[i], b[i].Addr)
					}
				}
			}
			if q.len() != 0 {
				t.Errorf("expected queue to be empty at end. got: %d", q.len())
			}
		})
	}
}

func BenchmarkDialQueue(b *testing.B) {
	b.ReportAllocs()
	genInput := func(size int) []network.AddrDelay {
		res := make([]network.AddrDelay, size)
		for j := 0; j < size; j++ {
			res[j] = network.AddrDelay{
				Addr:  ma.StringCast(fmt.Sprintf("/ip4/1.2.3.4/udp/%d/quic-v1", j+100)),
				Delay: time.Duration(mrand.Intn(1000_000_000)),
			}
		}
		return res
	}
	for i := 100; i <= 10000; i *= 10 {
		b.Run(fmt.Sprintf("size %d", i), func(b *testing.B) {
			initInp := genInput(i)
			dq := newDialQueue()
			for _, x := range initInp {
				dq.add(x)
			}
			inp := genInput(1000)
			for j := 0; j < b.N; j++ {
				x := inp[mrand.Intn(len(inp))]
				dq.pop()
				dq.add(x)
			}
		})
	}

}
