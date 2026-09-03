package ipc

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	want := Frame{Kind: KindDesktopFrame, Metadata: Metadata(map[string]string{"id": "x"}), Payload: []byte(`{"id":"a","method":"thread/list"}`)}
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != want.Kind || !bytes.Equal(got.Metadata, want.Metadata) || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func TestRejectsOversizedFrameBeforeAllocation(t *testing.T) {
	header := make([]byte, HeaderSize)
	copy(header[:4], Magic)
	binary.BigEndian.PutUint16(header[4:6], ProtocolVersion)
	header[6] = byte(KindDesktopFrame)
	binary.BigEndian.PutUint32(header[12:16], DefaultMaxFrame+1)
	if _, err := ReadFrame(bytes.NewReader(header)); err == nil {
		t.Fatal("expected oversized frame to be rejected")
	}
}

func TestBoundedJSONLReader(t *testing.T) {
	reader := bufio.NewReaderSize(bytes.NewReader([]byte("123456\n")), 2)
	if _, err := ReadBoundedLine(reader, 5); err == nil {
		t.Fatal("expected oversized line error")
	}
	reader = bufio.NewReaderSize(bytes.NewReader([]byte("123\n")), 2)
	line, err := ReadBoundedLine(reader, 5)
	if err != nil || string(line) != "123\n" {
		t.Fatalf("unexpected line %q: %v", line, err)
	}
}
