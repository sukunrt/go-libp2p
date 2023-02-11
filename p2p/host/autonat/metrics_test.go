package autonat

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/p2p/host/autonat/pb"
)

func BenchmarkReachabilityStatus(b *testing.B) {
	b.ReportAllocs()
	mt := NewMetricsTracer()
	for i := 0; i < b.N; i++ {
		mt.ReachabilityStatus(network.Reachability(i % 3))
	}
}

func BenchmarkClientDialResponse(b *testing.B) {
	b.ReportAllocs()
	mt := NewMetricsTracer()
	statuses := []pb.Message_ResponseStatus{
		pb.Message_OK, pb.Message_E_DIAL_ERROR, pb.Message_E_DIAL_REFUSED, pb.Message_E_BAD_REQUEST}
	for i := 0; i < b.N; i++ {
		mt.ClientDialResponse(statuses[i%len(statuses)])
	}
}

func BenchmarkServerDialResponse(b *testing.B) {
	b.ReportAllocs()
	mt := NewMetricsTracer()
	statuses := []pb.Message_ResponseStatus{
		pb.Message_OK, pb.Message_E_DIAL_ERROR, pb.Message_E_DIAL_REFUSED, pb.Message_E_BAD_REQUEST}
	for i := 0; i < b.N; i++ {
		mt.ServerDialResponse(statuses[i%len(statuses)])
	}
}

func BenchmarkServerDialRefused(b *testing.B) {
	b.ReportAllocs()
	mt := NewMetricsTracer()
	for i := 0; i < b.N; i++ {
		mt.ServerDialRefused(RATE_LIMIT)
	}
}
