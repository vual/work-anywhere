package daemon

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/hurf/work-anywhere/desktop/internal/ipc"
)

func TestClientConnQueueClosesWhenByteBoundIsExceeded(t *testing.T) {
	server, peer := net.Pipe()
	defer peer.Close()
	conn := &clientConn{id: "slow", conn: server}
	conn.StartWriter()
	defer conn.Close()

	frame := ipc.Frame{Kind: ipc.KindServerFrame, Payload: bytes.Repeat([]byte("x"), clientOutboundBytes)}
	if err := conn.Enqueue(frame); err == nil {
		t.Fatal("oversized outbound queue frame was accepted")
	}
	if !conn.closed.Load() {
		t.Fatal("queue overflow did not close the client connection")
	}
}

func TestClientConnQueueKeepsHealthyConnectionIndependent(t *testing.T) {
	slowServer, slowPeer := net.Pipe()
	defer slowPeer.Close()
	slow := &clientConn{id: "slow", conn: slowServer}
	slow.StartWriter()
	defer slow.Close()

	fastServer, fastPeer := net.Pipe()
	defer fastPeer.Close()
	fast := &clientConn{id: "fast", conn: fastServer}
	fast.StartWriter()
	defer fast.Close()

	slow.Enqueue(ipc.Frame{Kind: ipc.KindServerFrame, Payload: []byte("blocked\n")})
	want := []byte("healthy\n")
	if err := fast.Enqueue(ipc.Frame{Kind: ipc.KindServerFrame, Payload: want}); err != nil {
		t.Fatal(err)
	}
	_ = fastPeer.SetReadDeadline(time.Now().Add(time.Second))
	got, err := ipc.ReadFrame(fastPeer)
	if err != nil {
		t.Fatalf("healthy client did not receive its frame: %v", err)
	}
	if !bytes.Equal(got.Payload, want) {
		t.Fatalf("healthy client received %q, want %q", got.Payload, want)
	}
}
