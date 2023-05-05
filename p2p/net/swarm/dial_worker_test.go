package swarm

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/sec"
	"github.com/libp2p/go-libp2p/core/sec/insecure"
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

func newPeer(t *testing.T) (crypto.PrivKey, peer.ID) {
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	id, err := peer.IDFromPrivateKey(priv)
	require.NoError(t, err)
	return priv, id
}

func makeSwarm(t *testing.T) *Swarm {
	priv, id := newPeer(t)

	ps, err := pstoremem.NewPeerstore()
	require.NoError(t, err)
	ps.AddPubKey(id, priv.GetPublic())
	ps.AddPrivKey(id, priv)
	t.Cleanup(func() { ps.Close() })

	s, err := NewSwarm(id, ps, eventbus.NewBus(), WithDialTimeout(time.Second))
	require.NoError(t, err)

	upgrader := makeUpgrader(t, s)

	var tcpOpts []tcp.Option
	tcpOpts = append(tcpOpts, tcp.DisableReuseport())
	tcpTransport, err := tcp.NewTCPTransport(upgrader, nil, tcpOpts...)
	require.NoError(t, err)
	if err := s.AddTransport(tcpTransport); err != nil {
		t.Fatal(err)
	}
	if err := s.Listen(ma.StringCast("/ip4/127.0.0.1/tcp/0")); err != nil {
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
	if err := s.Listen(ma.StringCast("/ip4/127.0.0.1/udp/0/quic")); err != nil {
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

func makeTcpListener(t *testing.T) (net.Listener, ma.Multiaddr) {
	t.Helper()
	lst, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Error(err)
	}
	addr, err := manet.FromNetAddr(lst.Addr())
	if err != nil {
		t.Error(err)
	}
	return lst, addr
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
	worker := newDialWorker(s1, s2.LocalPeer(), reqch)
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
	worker := newDialWorker(s1, s2.LocalPeer(), reqch)
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
	worker := newDialWorker(s1, p2, reqch)
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
	worker := newDialWorker(s1, p2, reqch)
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
	worker := newDialWorker(s1, s2.LocalPeer(), reqch)
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
	worker := newDialWorker(s1, p2, reqch)
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

func TestDialWorkerLoopRanking(t *testing.T) {
	s1 := makeSwarm(t)
	s2 := makeSwarm(t)
	defer s1.Close()
	defer s2.Close()

	var quicAddr, tcpAddr ma.Multiaddr
	for _, a := range s2.ListenAddresses() {
		if _, err := a.ValueForProtocol(ma.P_QUIC); err == nil {
			quicAddr = a
		}
		if _, err := a.ValueForProtocol(ma.P_TCP); err == nil {
			tcpAddr = a
		}
	}

	tcpL1, silAddr1 := makeTcpListener(t)
	ch1 := make(chan struct{})
	defer tcpL1.Close()
	tcpL2, silAddr2 := makeTcpListener(t)
	ch2 := make(chan struct{})
	defer tcpL2.Close()
	tcpL3, silAddr3 := makeTcpListener(t)
	ch3 := make(chan struct{})
	defer tcpL3.Close()

	acceptAndIgnore := func(ch chan struct{}, l net.Listener) func() {
		return func() {
			for {
				_, err := l.Accept()
				if err != nil {
					break
				}
				ch <- struct{}{}
			}
		}
	}
	go acceptAndIgnore(ch1, tcpL1)()
	go acceptAndIgnore(ch2, tcpL2)()
	go acceptAndIgnore(ch3, tcpL3)()

	ranker := func(addrs []ma.Multiaddr) []network.AddrDelay {
		res := make([]network.AddrDelay, 0)
		for _, a := range addrs {
			switch {
			case a.Equal(silAddr1):
				res = append(res, network.AddrDelay{Addr: a, Delay: 0})
			case a.Equal(silAddr2):
				res = append(res, network.AddrDelay{Addr: a, Delay: 1 * time.Second})
			case a.Equal(tcpAddr):
				res = append(res, network.AddrDelay{Addr: a, Delay: 2 * time.Second})
			case a.Equal(silAddr3):
				res = append(res, network.AddrDelay{Addr: a, Delay: 3 * time.Second})
			default:
				t.Errorf("unexpected address %s", a)
			}
		}
		return res
	}

	// should connect to quic with both tcp and quic address
	s1.dialRanker = ranker
	s2addrs := []ma.Multiaddr{tcpAddr, silAddr1, silAddr2, silAddr3}
	s1.Peerstore().AddAddrs(s2.LocalPeer(), s2addrs, peerstore.PermanentAddrTTL)
	reqch := make(chan dialRequest)
	resch := make(chan dialResponse)
	worker1 := newDialWorker(s1, s2.LocalPeer(), reqch)
	go worker1.loop()
	defer worker1.wg.Wait()

	reqch <- dialRequest{ctx: context.Background(), resch: resch}
	select {
	case <-ch1:
	case <-time.After(1 * time.Second):
		t.Fatal("expected dial to tcp1")
	case <-resch:
		t.Fatalf("didn't expect connection to succeed")
	}
	select {
	case <-ch2:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected dial to tcp2")
	case <-resch:
		t.Fatalf("didn't expect connection to succeed")
	}
	select {
	case res := <-resch:
		if !res.conn.RemoteMultiaddr().Equal(tcpAddr) {
			log.Errorf("invalid connection address. expected %s got %s", tcpAddr, res.conn.RemoteMultiaddr())
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected dial to succeed")
	}
	close(reqch)
	s1.ClosePeer(s2.LocalPeer())
	s1.peers.ClearAddrs(s2.LocalPeer())
	select {
	case <-ch3:
		t.Errorf("didn't expect tcp call")
	case <-time.After(2 * time.Second):
	}

	quicFirstRanker := func(addrs []ma.Multiaddr) []network.AddrDelay {
		m := make([]network.AddrDelay, 0)
		for _, a := range addrs {
			if _, err := a.ValueForProtocol(ma.P_TCP); err == nil {
				m = append(m, network.AddrDelay{Addr: a, Delay: 500 * time.Millisecond})
			} else {
				m = append(m, network.AddrDelay{Addr: a, Delay: 0})
			}
		}
		return m
	}

	// tcp should connect after delay
	s1.dialRanker = quicFirstRanker
	s2.ListenClose(quicAddr)
	s1.Peerstore().AddAddrs(s2.LocalPeer(), []ma.Multiaddr{quicAddr, tcpAddr}, peerstore.PermanentAddrTTL)
	reqch = make(chan dialRequest)
	resch = make(chan dialResponse)
	worker2 := newDialWorker(s1, s2.LocalPeer(), reqch)
	go worker2.loop()
	defer worker2.wg.Wait()

	reqch <- dialRequest{ctx: context.Background(), resch: resch}
	select {
	case res := <-resch:
		t.Fatalf("expected a delay before connecting %s", res.conn.LocalMultiaddr())
	case <-time.After(400 * time.Millisecond):
	}
	select {
	case res := <-resch:
		require.NoError(t, res.err)
		if _, err := res.conn.LocalMultiaddr().ValueForProtocol(ma.P_TCP); err != nil {
			t.Fatalf("expected tcp connection %s", res.conn.LocalMultiaddr())
		}
	case <-time.After(1 * time.Second):
		t.Fatal("dial didn't complete")
	}
	close(reqch)
	s1.ClosePeer(s2.LocalPeer())
	s1.peers.ClearAddrs(s2.LocalPeer())
	s2.Listen(quicAddr)

	// should dial tcp immediately if there's no quic address available
	s1.Peerstore().AddAddrs(s2.LocalPeer(), []ma.Multiaddr{tcpAddr}, peerstore.PermanentAddrTTL)
	reqch = make(chan dialRequest)
	resch = make(chan dialResponse)
	worker3 := newDialWorker(s1, s2.LocalPeer(), reqch)
	go worker3.loop()
	defer worker3.wg.Wait()

	reqch <- dialRequest{ctx: context.Background(), resch: resch}
	select {
	case res := <-resch:
		require.NoError(t, res.err)
		if _, err := res.conn.LocalMultiaddr().ValueForProtocol(ma.P_TCP); err != nil {
			t.Fatalf("expected tcp connection, got: %s", res.conn.LocalMultiaddr())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("dial didn't complete")
	}
	close(reqch)
	s1.ClosePeer(s2.LocalPeer())
	s1.peers.ClearAddrs(s2.LocalPeer())

	// should dial next immediately when one connection errors after timeout
	quicFirstLargeDelayRanker := func(addrs []ma.Multiaddr) []network.AddrDelay {
		m := make([]network.AddrDelay, 0)
		for _, a := range addrs {
			if _, err := a.ValueForProtocol(ma.P_TCP); err == nil {
				m = append(m, network.AddrDelay{Addr: a, Delay: 10 * time.Second})
			} else {
				m = append(m, network.AddrDelay{Addr: a, Delay: 0})
			}
		}
		return m
	}

	s1.dialRanker = quicFirstLargeDelayRanker
	s2.ListenClose(quicAddr)
	s1.Peerstore().AddAddrs(s2.LocalPeer(), []ma.Multiaddr{tcpAddr, quicAddr}, peerstore.PermanentAddrTTL)
	reqch = make(chan dialRequest)
	resch = make(chan dialResponse)
	worker4 := newDialWorker(s1, s2.LocalPeer(), reqch)
	go worker4.loop()
	defer worker4.wg.Wait()

	reqch <- dialRequest{ctx: context.Background(), resch: resch}
	select {
	case res := <-resch:
		require.NoError(t, res.err)
		if _, err := res.conn.LocalMultiaddr().ValueForProtocol(ma.P_TCP); err != nil {
			t.Fatal("expected tcp connection")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dial didn't complete")
	}
	close(reqch)
	s1.ClosePeer(s2.LocalPeer())
	s1.peers.ClearAddrs(s2.LocalPeer())
}
