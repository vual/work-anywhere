package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"testing"

	"github.com/hurf/work-anywhere/desktop/internal/ipc"
	"github.com/hurf/work-anywhere/desktop/internal/protocol"
	"github.com/hurf/work-anywhere/desktop/internal/store"
	"github.com/hurf/work-anywhere/desktop/internal/traffic"
)

type bufferWriteCloser struct{ bytes.Buffer }

func (*bufferWriteCloser) Close() error { return nil }

func testInstance(t *testing.T) (*Instance, *bufferWriteCloser, func()) {
	t.Helper()
	dir := t.TempDir()
	state, err := store.Open(filepath.Join(dir, "state.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{store: state, traffic: traffic.New(filepath.Join(dir, "traffic")), subscribers: make(map[uint64]chan protocol.Event)}
	instance := NewInstance(manager, protocol.Hello{Instance: protocol.InstanceDescriptor{ID: "test-instance"}})
	writer := &bufferWriteCloser{}
	instance.stdin = writer
	instance.state = "waiting_initialize"
	return instance, writer, func() { state.Close() }
}

func TestDesktopRequestIDIsRestoredAndInitializeIsVirtualized(t *testing.T) {
	instance, writer, cleanup := testInstance(t)
	defer cleanup()
	serverSide, readerSide := net.Pipe()
	defer serverSide.Close()
	defer readerSide.Close()
	desktop := &clientConn{id: "desktop-a", conn: serverSide}
	instance.AddDesktop(desktop)
	initialize := []byte(`{"jsonrpc":"2.0","id":"original-string-id","method":"initialize","params":{"capabilities":{"x":true}}}` + "\n")
	if err := instance.HandleDesktop(desktop, initialize); err != nil {
		t.Fatal(err)
	}
	forwarded := bytes.TrimSpace(writer.Bytes())
	var request map[string]json.RawMessage
	if err := json.Unmarshal(forwarded, &request); err != nil {
		t.Fatal(err)
	}
	if string(request["id"]) == `"original-string-id"` {
		t.Fatal("desktop id was not rewritten")
	}
	response := append([]byte(`{"jsonrpc":"2.0","id":`), request["id"]...)
	response = append(response, []byte(`,"result":{"server":"fake"}}`+"\n")...)
	done := make(chan struct{})
	go func() { instance.handleServerFrame(0, response); close(done) }()
	frame, err := ipc.ReadFrame(readerSide)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	var restored map[string]json.RawMessage
	if err = json.Unmarshal(bytes.TrimSpace(frame.Payload), &restored); err != nil {
		t.Fatal(err)
	}
	if string(restored["id"]) != `"original-string-id"` {
		t.Fatalf("id not restored: %s", restored["id"])
	}
	before := writer.Len()
	secondServer, secondReader := net.Pipe()
	defer secondServer.Close()
	defer secondReader.Close()
	second := &clientConn{id: "desktop-b", conn: secondServer}
	go func() {
		_ = instance.HandleDesktop(second, []byte(`{"jsonrpc":"2.0","id":27,"method":"initialize","params":{"capabilities":{"x":true}}}`+"\n"))
	}()
	virtual, err := ipc.ReadFrame(secondReader)
	if err != nil {
		t.Fatal(err)
	}
	var virtualMessage map[string]json.RawMessage
	if err = json.Unmarshal(bytes.TrimSpace(virtual.Payload), &virtualMessage); err != nil {
		t.Fatal(err)
	}
	if string(virtualMessage["id"]) != "27" {
		t.Fatalf("numeric id not restored: %s", virtualMessage["id"])
	}
	if writer.Len() != before {
		t.Fatal("virtualized initialize was forwarded to App Server")
	}
}

func TestApprovalFirstResponseWins(t *testing.T) {
	instance, writer, cleanup := testInstance(t)
	defer cleanup()
	aServer, aReader := net.Pipe()
	defer aServer.Close()
	defer aReader.Close()
	bServer, bReader := net.Pipe()
	defer bServer.Close()
	defer bReader.Close()
	a := &clientConn{id: "a", conn: aServer}
	b := &clientConn{id: "b", conn: bServer}
	instance.AddDesktop(a)
	instance.AddDesktop(b)
	request := []byte(`{"jsonrpc":"2.0","id":42,"method":"item/commandExecution/requestApproval","params":{"threadId":"thread-1"}}` + "\n")
	done := make(chan struct{})
	go func() { instance.handleServerFrame(0, request); close(done) }()
	if _, err := ipc.ReadFrame(aReader); err != nil {
		t.Fatal(err)
	}
	if _, err := ipc.ReadFrame(bReader); err != nil {
		t.Fatal(err)
	}
	<-done
	writer.Reset()
	if err := instance.HandleDesktop(a, []byte(`{"jsonrpc":"2.0","id":42,"result":{"decision":"accept"}}`+"\n")); err != nil {
		t.Fatal(err)
	}
	first := writer.String()
	if err := instance.HandleDesktop(b, []byte(`{"jsonrpc":"2.0","id":42,"result":{"decision":"decline"}}`+"\n")); err != nil {
		t.Fatal(err)
	}
	if writer.String() != first {
		t.Fatalf("second approval was forwarded: %s", writer.String())
	}
	if !bytes.Contains([]byte(first), []byte("accept")) {
		t.Fatalf("first approval missing: %s", first)
	}
}

var _ io.WriteCloser = (*bufferWriteCloser)(nil)
