package relaysvc

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	bhost "github.com/libp2p/go-libp2p/p2p/host/blank"
	swarmt "github.com/libp2p/go-libp2p/p2p/net/swarm/testing"
	"github.com/stretchr/testify/require"
)

func TestReachabilityChangeEvent(t *testing.T) {
	h := bhost.NewBlankHost(swarmt.GenSwarm(t))
	rmgr := NewRelayManager(h)
	rmgr.reachabilityChanged(network.ReachabilityPublic)
	require.NotNil(t, rmgr.relay, "relay should be set on public reachability")

	rmgr.reachabilityChanged(network.ReachabilityPrivate)
	require.Nil(t, rmgr.relay, "relay should be nil on private reachability")

	rmgr.reachabilityChanged(network.ReachabilityPublic)
	rmgr.reachabilityChanged(network.ReachabilityUnknown)
	require.Nil(t, rmgr.relay, "relay should be nil on unknown reachability")

	rmgr.reachabilityChanged(network.ReachabilityPublic)
	relay := rmgr.relay
	rmgr.reachabilityChanged(network.ReachabilityPublic)
	require.Equal(t, relay, rmgr.relay, "relay should not be started on receiving the same event")
}
