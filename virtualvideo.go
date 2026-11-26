package kvm

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/jetkvm/kvm/internal/usbgadget"
	"github.com/jetkvm/kvm/internal/uvc"
)

const (
	virtualVideoFrameMagic          uint32 = 0x4a4b4d4a // JKMJ
	virtualVideoFrameVersion        uint16 = 1
	virtualVideoHeaderSize                 = 28
	defaultVirtualVideoDevice              = "auto"
	defaultVirtualVideoMaxFrameSize        = 2 * 1024 * 1024
)

// The device path is opened read-write as root: only a video node is allowed.
var virtualVideoDevicePath = regexp.MustCompile(`^/dev/video[0-9]{1,2}$`)

type VirtualVideoConfig struct {
	Enabled      bool   `json:"enabled"`
	Transport    string `json:"transport"`
	Codec        string `json:"codec"`
	DevicePath   string `json:"device_path"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FramesPerSec int    `json:"fps"`
	// USBSize is the one frame size the MJPEG USB camera offers (a key of
	// usbgadget.VideoFrameSizes), and USBFPS its one frame rate (one of
	// usbgadget.VideoFrameRates). Empty and 0: the camera offers all of them
	// and the target computer picks.
	USBSize string `json:"usb_size"`
	USBFPS  int    `json:"usb_fps"`
}

type VirtualVideoState struct {
	Enabled       bool      `json:"enabled"`
	Streaming     bool      `json:"streaming"`
	USBStreaming  bool      `json:"usb_streaming"`
	USBFramesSent uint64    `json:"usb_frames_sent"`
	Transport     string    `json:"transport"`
	Codec         string    `json:"codec"`
	NeedsKeyFrame bool      `json:"needs_keyframe"`
	DroppedFrames uint64    `json:"dropped_frames"`
	DevicePath    string    `json:"device_path"`
	Width         int       `json:"width"`
	Height        int       `json:"height"`
	FramesPerSec  int       `json:"fps"`
	USBSize       string    `json:"usb_size"`
	USBFPS        int       `json:"usb_fps"`
	FramesWritten uint64    `json:"frames_written"`
	BytesWritten  uint64    `json:"bytes_written"`
	LastFrameAt   time.Time `json:"last_frame_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

type virtualVideoFrame struct {
	Flags     uint16
	Sequence  uint32
	Timestamp uint64
	Width     int
	Height    int
	Payload   []byte
}

type virtualVideoOutput interface {
	Write(jpeg []byte, width, height int) (int, error)
	// Mode is the frame size and rate the USB host negotiated.
	Mode() (width, height, fps int)
	WriteH264([]byte, bool) (int, error)
	Reset()
	Close() error
	Status() (bool, uint64, error)
	Recovery() (bool, uint64)
}

type VirtualVideoManager struct {
	mu                  sync.Mutex
	config              VirtualVideoConfig
	state               VirtualVideoState
	device              virtualVideoOutput
	latestFrame         []byte
	previewAt           time.Time
	lastSequence        uint32
	haveSequence        bool
	receivedAt          time.Time
	previewSequence     uint32
	havePreviewSequence bool
	channelID           uint64
	// previewUntil: frames are kept for the preview only while it is polled.
	previewUntil time.Time
}

var virtualVideo *VirtualVideoManager

// defaultVirtualVideoConfig is the camera's default. 640x480 at 15 fps is the
// one H.264 profile; SetConfig sets the MJPEG camera to 30 fps.
func defaultVirtualVideoConfig() VirtualVideoConfig {
	return VirtualVideoConfig{
		Codec:        "mjpeg",
		Transport:    "rtc-datachannel",
		DevicePath:   defaultVirtualVideoDevice,
		Width:        640,
		Height:       480,
		FramesPerSec: 15,
	}
}

func newVirtualVideoManager() *VirtualVideoManager {
	m := &VirtualVideoManager{}
	_, _ = m.SetConfig(defaultVirtualVideoConfig())
	return m
}

func normalizeVirtualVideoConfig(cfg VirtualVideoConfig) VirtualVideoConfig {
	def := defaultVirtualVideoConfig()
	if cfg.Codec == "" {
		cfg.Codec = def.Codec
	}
	if cfg.Transport == "" {
		cfg.Transport = def.Transport
	}
	if cfg.Transport == "webtransport-quic" {
		cfg.Transport = "auto" // the WebTransport transport was removed
	}
	if !virtualVideoDevicePath.MatchString(cfg.DevicePath) {
		cfg.DevicePath = def.DevicePath
	}
	if cfg.Width <= 0 {
		cfg.Width = def.Width
	}
	if cfg.Height <= 0 {
		cfg.Height = def.Height
	}
	if cfg.FramesPerSec <= 0 {
		cfg.FramesPerSec = def.FramesPerSec
	}
	if !usbgadget.ValidVideoSize(cfg.USBSize) {
		cfg.USBSize = ""
	}
	if !usbgadget.ValidVideoFPS(cfg.USBFPS) {
		cfg.USBFPS = 0
	}
	return cfg
}

func (m *VirtualVideoManager) SetConfig(cfg VirtualVideoConfig) (VirtualVideoState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg = normalizeVirtualVideoConfig(cfg)
	if cfg.Codec == "h264" {
		if cfg.Width != 640 || cfg.Height != 480 || cfg.FramesPerSec != 15 {
			return m.state, fmt.Errorf("the H.264 USB camera supports 640x480 at 15 fps")
		}
	} else {
		// The USB host picks the MJPEG frame size and rate: see State.
		cfg.Width, cfg.Height, cfg.FramesPerSec = 640, 480, 30
	}
	switch cfg.Transport {
	case "auto", "rtc-datachannel", "websocket":
	default:
		return m.state, fmt.Errorf("invalid virtual video transport: %s", cfg.Transport)
	}

	if cfg.Codec != "mjpeg" && cfg.Codec != "h264" {
		return m.state, fmt.Errorf("invalid camera codec: %s", cfg.Codec)
	}
	if cfg.Codec != m.config.Codec {
		m.closeDeviceLocked()
		m.latestFrame = nil
		m.previewAt = time.Time{}
		m.state.FramesWritten = 0
		m.state.BytesWritten = 0
		m.state.USBFramesSent = 0
		m.state.USBStreaming = false
		m.state.NeedsKeyFrame = false
		m.state.DroppedFrames = 0
		m.state.LastFrameAt = time.Time{}
		m.haveSequence = false
		m.havePreviewSequence = false
		m.receivedAt = time.Time{}
	}
	wasStreaming := m.state.Streaming

	m.config = cfg
	m.state.Enabled = cfg.Enabled
	m.state.Streaming = false
	m.state.Codec = cfg.Codec
	m.state.Transport = cfg.Transport
	m.state.DevicePath = cfg.DevicePath
	m.state.Width = cfg.Width
	m.state.Height = cfg.Height
	m.state.FramesPerSec = cfg.FramesPerSec
	m.state.USBSize, m.state.USBFPS = cfg.USBSize, cfg.USBFPS
	m.state.LastError = ""

	if wasStreaming && cfg.Enabled {
		if err := m.openDeviceLocked(); err != nil {
			m.state.LastError = err.Error()
			return m.state, err
		}
	}

	return m.state, nil
}

func (m *VirtualVideoManager) State() VirtualVideoState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.device != nil {
		streaming, frames, err := m.device.Status()
		m.state.USBStreaming = streaming
		m.state.USBFramesSent = frames
		m.state.NeedsKeyFrame, m.state.DroppedFrames = m.device.Recovery()
		m.syncModeLocked()
		if err != nil {
			m.state.LastError = fmt.Sprintf("camera output: %v", err)
		}
	}
	return m.state
}

// syncModeLocked copies the frame size and rate the USB host negotiated into
// the state, which the browser follows. Only the MJPEG camera offers more
// than one.
func (m *VirtualVideoManager) syncModeLocked() {
	if m.device == nil || m.config.Codec != "mjpeg" {
		return
	}
	if width, height, fps := m.device.Mode(); width > 0 && height > 0 && fps > 0 {
		m.config.Width, m.config.Height, m.config.FramesPerSec = width, height, fps
		m.state.Width, m.state.Height, m.state.FramesPerSec = width, height, fps
	}
}

func (m *VirtualVideoManager) Start() (VirtualVideoState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config.Enabled = true
	m.state.Enabled = true
	if err := m.openDeviceLocked(); err != nil {
		m.config.Enabled = false
		m.state.Enabled = false
		m.state.LastError = err.Error()
		return m.state, err
	}
	m.state.LastError = ""
	return m.state, nil
}

func (m *VirtualVideoManager) Stop() VirtualVideoState {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config.Enabled = false
	m.state.Enabled = false
	// Keep the UVC control handler armed. Closing it deactivates the entire
	// composite USB gadget in the vendor kernel, including HID and Ethernet.
	m.state.Streaming = false
	m.state.LastError = ""
	m.latestFrame = nil
	m.previewAt = time.Time{}
	m.haveSequence = false
	m.havePreviewSequence = false
	m.receivedAt = time.Time{}
	if m.device != nil {
		m.device.Reset()
	}
	return m.state
}

func (m *VirtualVideoManager) openDeviceLocked() error {
	if m.device != nil {
		m.state.Streaming = true
		return nil
	}
	f, err := uvc.Open(m.config.DevicePath, m.config.Width, m.config.Height, m.config.FramesPerSec, m.config.Codec)
	if err != nil {
		return fmt.Errorf("open virtual video device %s: %w", m.config.DevicePath, err)
	}
	m.device = f
	m.state.DevicePath = f.Path
	m.state.Streaming = true
	return nil
}

func (m *VirtualVideoManager) closeDeviceLocked() {
	if m.device != nil {
		_ = m.device.Close()
		m.device = nil
	}
	m.state.Streaming = false
}

func parseVirtualVideoFrame(data []byte, maxFrameBytes int) (virtualVideoFrame, error) {
	if len(data) < virtualVideoHeaderSize {
		return virtualVideoFrame{}, fmt.Errorf("virtual video frame too short: %d", len(data))
	}
	if binary.BigEndian.Uint32(data[0:4]) != virtualVideoFrameMagic {
		return virtualVideoFrame{}, fmt.Errorf("invalid virtual video frame magic")
	}
	version := binary.BigEndian.Uint16(data[4:6])
	flags := binary.BigEndian.Uint16(data[6:8])
	if (version == 1 && flags != 0) || (version == 2 && flags != 1 && flags != 3 && flags != 4) {
		return virtualVideoFrame{}, fmt.Errorf("invalid camera frame flags")
	}
	if version != 1 && version != 2 {
		return virtualVideoFrame{}, fmt.Errorf("unsupported virtual video frame version")
	}

	payloadLen := int(binary.BigEndian.Uint32(data[24:28]))
	if payloadLen <= 0 {
		return virtualVideoFrame{}, fmt.Errorf("empty virtual video frame")
	}
	if payloadLen > maxFrameBytes {
		return virtualVideoFrame{}, fmt.Errorf("virtual video frame too large: %d", payloadLen)
	}
	if len(data)-virtualVideoHeaderSize != payloadLen {
		return virtualVideoFrame{}, fmt.Errorf("virtual video frame length mismatch")
	}

	tsHigh := uint64(binary.BigEndian.Uint32(data[12:16]))
	tsLow := uint64(binary.BigEndian.Uint32(data[16:20]))

	return virtualVideoFrame{
		Flags:     flags,
		Sequence:  binary.BigEndian.Uint32(data[8:12]),
		Timestamp: tsHigh<<32 | tsLow,
		Width:     int(binary.BigEndian.Uint16(data[20:22])),
		Height:    int(binary.BigEndian.Uint16(data[22:24])),
		Payload:   data[virtualVideoHeaderSize:],
	}, nil
}

func (m *VirtualVideoManager) HandleFrameData(data []byte, transport string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.handleFrameDataLocked(data, transport)
}
func (m *VirtualVideoManager) HandleChannelFrameData(id uint64, data []byte, transport string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != m.channelID {
		return nil
	}
	return m.handleFrameDataLocked(data, transport)
}
func (m *VirtualVideoManager) handleFrameDataLocked(data []byte, transport string) error {

	frame, err := parseVirtualVideoFrame(data, defaultVirtualVideoMaxFrameSize)
	if err != nil {
		m.state.LastError = err.Error()
		return err
	}
	if !m.config.Enabled {
		return nil
	}
	if m.config.Codec == "h264" && (frame.Width != m.config.Width || frame.Height != m.config.Height) {
		return fmt.Errorf("frame dimensions do not match the negotiated USB video format")
	}

	if frame.Flags == 4 {
		// JPEG encoding can finish after subsequent H.264 frames arrive. Accept a
		// recent sample even when no target app has opened the USB camera.
		if m.config.Codec != "h264" || transport != "rtc-datachannel" || !m.haveSequence ||
			time.Since(m.receivedAt) > 4*time.Second || m.lastSequence-frame.Sequence > 60 ||
			(m.havePreviewSequence && int32(frame.Sequence-m.previewSequence) <= 0) {
			return nil
		}
		if !time.Now().Before(m.previewUntil) {
			return nil
		}
		// Preview sidecar is separate from the H.264 USB payload, never re-encoded on the device.
		if _, err := encodeVirtualVideoPreview(frame.Payload); err != nil {
			return err
		}
		m.latestFrame = append(m.latestFrame[:0], frame.Payload...)
		m.previewAt = time.Now()
		m.previewSequence = frame.Sequence
		m.havePreviewSequence = true
		return nil
	}
	if (frame.Flags&1 != 0) != (m.config.Codec == "h264") {
		return fmt.Errorf("camera frame codec differs from the USB profile")
	}
	if m.device == nil {
		if err := m.openDeviceLocked(); err != nil {
			m.state.LastError = err.Error()
			return err
		}
	}
	var n int
	if m.config.Codec == "h264" {
		if transport != "rtc-datachannel" {
			return fmt.Errorf("H.264 requires ordered WebRTC transport")
		}
		keyframe := frame.Flags&2 != 0
		if err := uvc.ValidateH264(frame.Payload, keyframe); err != nil {
			return err
		}
		if m.haveSequence && frame.Sequence != m.lastSequence+1 && !keyframe {
			m.device.Reset()
		}
		m.lastSequence = frame.Sequence
		m.haveSequence = true
		m.receivedAt = time.Now()
		n, err = m.device.WriteH264(frame.Payload, keyframe)
	} else {
		// A frame of another size than the host negotiated is dropped: the
		// browser reads the new size from the state and follows.
		m.syncModeLocked()
		if frame.Width != m.config.Width || frame.Height != m.config.Height {
			m.state.DroppedFrames++
			return nil
		}
		n, err = m.device.Write(frame.Payload, frame.Width, frame.Height)
	}
	if err != nil {
		m.state.LastError = err.Error()
		return fmt.Errorf("write virtual video frame: %w", err)
	}
	m.state.Transport = transport
	if n == 0 {
		return nil
	}
	if m.config.Codec == "mjpeg" && time.Now().Before(m.previewUntil) {
		m.latestFrame = append(m.latestFrame[:0], frame.Payload...)
		m.previewAt = time.Now()
	}
	m.state.FramesWritten++
	m.state.BytesWritten += uint64(n)
	m.state.LastFrameAt = time.Now()
	m.state.LastError = ""
	return nil
}

// A closing older channel must not stop a camera started on its replacement.
func (m *VirtualVideoManager) BeginChannel() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channelID++
	m.haveSequence = false
	m.havePreviewSequence = false
	m.receivedAt = time.Time{}
	m.latestFrame = nil
	m.previewAt = time.Time{}
	return m.channelID
}
func (m *VirtualVideoManager) StopChannel(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != m.channelID {
		return
	}
	m.config.Enabled = false
	m.state.Enabled = false
	m.state.Streaming = false
	m.latestFrame = nil
	m.previewAt = time.Time{}
	m.haveSequence = false
	m.havePreviewSequence = false
	m.receivedAt = time.Time{}
	if m.device != nil {
		m.device.Reset()
	}
}
