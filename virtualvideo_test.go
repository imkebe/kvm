package kvm

import (
	"encoding/binary"
	"testing"
)

func makeVirtualVideoTestFrame(payload []byte) []byte {
	buf := make([]byte, virtualVideoHeaderSize+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], virtualVideoFrameMagic)
	binary.BigEndian.PutUint16(buf[4:6], virtualVideoFrameVersion)
	binary.BigEndian.PutUint32(buf[8:12], 7)
	binary.BigEndian.PutUint32(buf[16:20], 1234)
	binary.BigEndian.PutUint16(buf[20:22], 640)
	binary.BigEndian.PutUint16(buf[22:24], 480)
	binary.BigEndian.PutUint32(buf[24:28], uint32(len(payload)))
	copy(buf[virtualVideoHeaderSize:], payload)
	return buf
}

func TestParseVirtualVideoFrame(t *testing.T) {
	payload := []byte{0xff, 0xd8, 0xff, 0xd9}
	frame, err := parseVirtualVideoFrame(makeVirtualVideoTestFrame(payload), 1024)
	if err != nil {
		t.Fatalf("parse frame: %v", err)
	}
	if frame.Sequence != 7 {
		t.Fatalf("sequence = %d", frame.Sequence)
	}
	if frame.Width != 640 || frame.Height != 480 {
		t.Fatalf("size = %dx%d", frame.Width, frame.Height)
	}
	if string(frame.Payload) != string(payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestParseVirtualVideoFrameRejectsOversize(t *testing.T) {
	_, err := parseVirtualVideoFrame(makeVirtualVideoTestFrame([]byte{1, 2, 3, 4}), 2)
	if err == nil {
		t.Fatalf("expected oversize error")
	}
}
