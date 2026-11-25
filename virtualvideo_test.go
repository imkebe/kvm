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
func TestVideoFrameProtocolFlags(t *testing.T) {
	for _, tc := range []struct {
		version, flags uint16
		ok             bool
	}{{1, 0, true}, {1, 1, false}, {2, 1, true}, {2, 3, true}, {2, 4, true}, {2, 0, false}, {2, 7, false}, {3, 1, false}} {
		data := makeVirtualVideoTestFrame([]byte{1, 2, 3, 4})
		binary.BigEndian.PutUint16(data[4:6], tc.version)
		binary.BigEndian.PutUint16(data[6:8], tc.flags)
		if _, err := parseVirtualVideoFrame(data, 1024); (err == nil) != tc.ok {
			t.Fatalf("version=%d flags=%d err=%v", tc.version, tc.flags, err)
		}
	}
}
func TestOldCameraChannelCannotStopReplacement(t *testing.T) {
	m := newVirtualVideoManager()
	old := m.BeginChannel()
	current := m.BeginChannel()
	m.config.Enabled = true
	m.state.Enabled = true
	m.StopChannel(old)
	if !m.State().Enabled {
		t.Fatal("old channel stopped current camera")
	}
	m.StopChannel(current)
	if m.State().Enabled {
		t.Fatal("current channel failed to stop")
	}
}

// Only a video node may be opened: anything else becomes "auto".
func TestNormalizeVirtualVideoDevicePath(t *testing.T) {
	for path, want := range map[string]string{
		"": "auto", "auto": "auto", "/dev/video0": "/dev/video0", "/dev/video12": "/dev/video12",
		"/dev/watchdog": "auto", "/dev/video123": "auto", "/dev/video0/../watchdog": "auto", "video0": "auto",
	} {
		cfg := defaultVirtualVideoConfig()
		cfg.DevicePath = path
		if got := normalizeVirtualVideoConfig(cfg).DevicePath; got != want {
			t.Errorf("%q became %q, want %q", path, got, want)
		}
	}
}
