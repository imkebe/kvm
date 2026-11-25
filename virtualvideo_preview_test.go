package kvm

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
	"time"
)

func previewTestJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 210, G: 45, B: 20, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestVirtualVideoPreview(t *testing.T) {
	data := previewTestJPEG(t, 640, 480)
	frameAt := time.Now()
	manager := newVirtualVideoManager()
	manager.config.Enabled = true
	manager.previewUntil = time.Now().Add(time.Minute)
	manager.latestFrame = data
	manager.state.LastFrameAt = frameAt
	preview, err := manager.Preview()
	if err != nil {
		t.Fatal(err)
	}
	if !preview.FrameAt.Equal(frameAt) {
		t.Fatal("incorrect frame timestamp")
	}
	decoded, err := base64.StdEncoding.DecodeString(preview.JPEG)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) > 24*1024 {
		t.Fatalf("preview exceeds channel budget: %d", len(decoded))
	}
	img, err := jpeg.Decode(bytes.NewReader(decoded))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 240 || img.Bounds().Dy() != 180 {
		t.Fatalf("preview dimensions: %v", img.Bounds())
	}
	r, g, b, _ := img.At(100, 100).RGBA()
	if r/256 < 190 || g/256 > 65 || b/256 > 40 {
		t.Fatalf("preview differs from incoming footage: %d %d %d", r/256, g/256, b/256)
	}
	manager.Stop()
	preview, err = manager.Preview()
	if err != nil || preview.JPEG != "" || len(manager.latestFrame) != 0 {
		t.Fatal("stopped camera retained footage")
	}
}

func TestVirtualVideoPreviewRejectsInvalidFrame(t *testing.T) {
	for _, data := range [][]byte{[]byte("not jpeg"), previewTestJPEG(t, 720, 400)} {
		if _, err := encodeVirtualVideoPreview(data); err == nil {
			t.Fatal("accepted invalid/unsupported JPEG")
		}
	}
}

// Simulate a USB camera that is armed but has no capture application reading it.
type idleVideoOutput struct{ resets int }

func (d *idleVideoOutput) Write([]byte, int, int) (int, error) { return 0, nil }
func (d *idleVideoOutput) Mode() (int, int, int)               { return 640, 480, 30 }
func (d *idleVideoOutput) WriteH264([]byte, bool) (int, error) { return 0, nil }
func (d *idleVideoOutput) Reset()                              { d.resets++ }
func (d *idleVideoOutput) Close() error                        { return nil }
func (d *idleVideoOutput) Status() (bool, uint64, error)       { return false, 0, nil }
func (d *idleVideoOutput) Recovery() (bool, uint64)            { return true, 0 }

// A USB camera that the target reads: every frame is written.
type readVideoOutput struct{ idleVideoOutput }

func (d *readVideoOutput) Write(jpeg []byte, _, _ int) (int, error) { return len(jpeg), nil }

// The frame is kept for the preview only while the preview is polled.
func TestPreviewNoCopyWhenNotRequested(t *testing.T) {
	m := newVirtualVideoManager()
	m.config.Enabled = true
	m.device = &readVideoOutput{}
	frame := makeVirtualVideoTestFrame(previewTestJPEG(t, 640, 480))
	if err := m.HandleFrameData(frame, "websocket"); err != nil {
		t.Fatal(err)
	}
	if m.state.FramesWritten != 1 || m.latestFrame != nil {
		t.Fatalf("frames %d, kept %d bytes without a preview poll", m.state.FramesWritten, len(m.latestFrame))
	}
	if preview, err := m.Preview(); err != nil || preview.JPEG != "" {
		t.Fatalf("first poll: %v %q", err, preview.JPEG)
	}
	if err := m.HandleFrameData(frame, "websocket"); err != nil {
		t.Fatal(err)
	}
	if preview, err := m.Preview(); err != nil || preview.JPEG == "" {
		t.Fatalf("no preview while polled: %v", err)
	}
	// The poll lapsed: an old frame is not shown.
	m.previewUntil = time.Now().Add(-time.Second)
	if preview, _ := m.Preview(); preview.JPEG != "" {
		t.Fatal("stale frame shown after the poll lapsed")
	}
}
