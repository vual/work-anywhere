package ipc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	Magic           = "WAI1"
	ProtocolVersion = uint16(1)
	HeaderSize      = 16
	DefaultMaxFrame = 64 << 20
)

type Kind uint8

const (
	KindHello Kind = iota + 1
	KindHelloAck
	KindDesktopFrame
	KindServerFrame
	KindDesktopEOF
	KindServerExit
	KindError
	KindPing
	KindPong
	KindControlRequest
	KindControlResponse
)

type Frame struct {
	Kind     Kind
	Flags    uint8
	Metadata json.RawMessage
	Payload  []byte
}

func WriteFrame(w io.Writer, frame Frame) error {
	if len(frame.Metadata)+len(frame.Payload) > DefaultMaxFrame {
		return fmt.Errorf("frame exceeds %d bytes", DefaultMaxFrame)
	}
	header := make([]byte, HeaderSize)
	copy(header[:4], Magic)
	binary.BigEndian.PutUint16(header[4:6], ProtocolVersion)
	header[6] = byte(frame.Kind)
	header[7] = frame.Flags
	binary.BigEndian.PutUint32(header[8:12], uint32(len(frame.Metadata)))
	binary.BigEndian.PutUint32(header[12:16], uint32(len(frame.Payload)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	if _, err := w.Write(frame.Metadata); err != nil {
		return err
	}
	_, err := w.Write(frame.Payload)
	return err
}

func ReadFrame(r io.Reader) (Frame, error) {
	header := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return Frame{}, err
	}
	if string(header[:4]) != Magic {
		return Frame{}, errors.New("invalid IPC frame magic")
	}
	if version := binary.BigEndian.Uint16(header[4:6]); version != ProtocolVersion {
		return Frame{}, fmt.Errorf("unsupported IPC protocol version %d", version)
	}
	metadataLen := int(binary.BigEndian.Uint32(header[8:12]))
	payloadLen := int(binary.BigEndian.Uint32(header[12:16]))
	if metadataLen < 0 || payloadLen < 0 || metadataLen+payloadLen > DefaultMaxFrame {
		return Frame{}, errors.New("IPC frame is too large")
	}
	metadata := make([]byte, metadataLen)
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, metadata); err != nil {
		return Frame{}, err
	}
	if _, err := io.ReadFull(r, payload); err != nil {
		return Frame{}, err
	}
	return Frame{Kind: Kind(header[6]), Flags: header[7], Metadata: metadata, Payload: payload}, nil
}

func Metadata(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
