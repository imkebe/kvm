package kvm

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"slices"
	"time"

	"github.com/jetkvm/kvm/internal/usbgadget"
)

type VirtualVideoPreview struct {
	JPEG    string    `json:"jpeg,omitempty"`
	FrameAt time.Time `json:"frame_at,omitempty"`
}

// Preview the source frame received by the device, including H.264 sidecars
// when no target app is reading USB video. Send a bounded
// thumbnail through the authenticated RPC channel, including cloud sessions.
func (m *VirtualVideoManager) Preview() (VirtualVideoPreview, error) {
	m.mu.Lock()
	now := time.Now()
	if now.After(m.previewUntil) {
		// Nobody polled for a while: the kept frame is old.
		m.latestFrame = nil
	}
	m.previewUntil = now.Add(5 * time.Second)
	frame := append([]byte(nil), m.latestFrame...)
	frameAt := m.previewAt
	if frameAt.IsZero() {
		frameAt = m.state.LastFrameAt
	}
	active := m.config.Enabled
	m.mu.Unlock()
	if !active || len(frame) == 0 {
		return VirtualVideoPreview{}, nil
	}
	encoded, err := encodeVirtualVideoPreview(frame)
	if err != nil {
		return VirtualVideoPreview{}, err
	}
	return VirtualVideoPreview{JPEG: encoded, FrameAt: frameAt}, nil
}

func encodeVirtualVideoPreview(frame []byte) (string, error) {
	config, err := jpeg.DecodeConfig(bytes.NewReader(frame))
	if err != nil {
		return "", fmt.Errorf("decode camera preview: %w", err)
	}
	// The frame sizes the USB camera offers.
	if !slices.ContainsFunc(usbgadget.VideoFrameSizes, func(f usbgadget.VideoFrameSize) bool {
		return f.Width == config.Width && f.Height == config.Height
	}) {
		return "", fmt.Errorf("camera preview requires a JPEG of a size the USB camera offers")
	}
	// Decoding a 1080p frame takes the KVM's processor too long to do for a
	// thumbnail, so that size has no preview.
	if config.Width > 1280 {
		return "", nil
	}
	source, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		return "", fmt.Errorf("decode camera preview: %w", err)
	}
	thumbHeight := 240 * config.Height / config.Width
	thumbnail := image.NewRGBA(image.Rect(0, 0, 240, thumbHeight))
	for y := 0; y < thumbHeight; y++ {
		for x := 0; x < 240; x++ {
			thumbnail.Set(x, y, source.At(x*config.Width/240, y*config.Height/thumbHeight))
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, thumbnail, &jpeg.Options{Quality: 55}); err != nil {
		return "", err
	}
	if out.Len() > 24*1024 {
		return "", fmt.Errorf("camera preview exceeds the RPC limit")
	}
	return base64.StdEncoding.EncodeToString(out.Bytes()), nil
}
