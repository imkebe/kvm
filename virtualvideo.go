package kvm

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"time"
)

const (
	virtualVideoFrameMagic          uint32 = 0x4a4b4d4a // JKMJ
	virtualVideoFrameVersion        uint16 = 1
	virtualVideoHeaderSize                 = 28
	defaultVirtualVideoDevice              = "/dev/video1"
	defaultVirtualVideoMaxFrameSize        = 2 * 1024 * 1024
)

type VirtualVideoConfig struct {
	Enabled       bool   `json:"enabled"`
	Transport     string `json:"transport"`
	DevicePath    string `json:"device_path"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FramesPerSec  int    `json:"fps"`
	MaxFrameBytes int    `json:"max_frame_bytes"`
}

type VirtualVideoState struct {
	Enabled       bool      `json:"enabled"`
	Streaming     bool      `json:"streaming"`
	Transport     string    `json:"transport"`
	DevicePath    string    `json:"device_path"`
	Width         int       `json:"width"`
	Height        int       `json:"height"`
	FramesPerSec  int       `json:"fps"`
	FramesWritten uint64    `json:"frames_written"`
	BytesWritten  uint64    `json:"bytes_written"`
	LastFrameAt   time.Time `json:"last_frame_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

type virtualVideoFrame struct {
	Sequence  uint32
	Timestamp uint64
	Width     int
	Height    int
	Payload   []byte
}

type VirtualVideoManager struct {
	mu     sync.Mutex
	config VirtualVideoConfig
	state  VirtualVideoState
	device *os.File
}

var virtualVideo *VirtualVideoManager

func defaultVirtualVideoConfig() VirtualVideoConfig {
	return VirtualVideoConfig{
		Enabled:       false,
		Transport:     "rtc-datachannel",
		DevicePath:    defaultVirtualVideoDevice,
		Width:         640,
		Height:        480,
		FramesPerSec:  15,
		MaxFrameBytes: defaultVirtualVideoMaxFrameSize,
	}
}

func newVirtualVideoManager() *VirtualVideoManager {
	cfg := defaultVirtualVideoConfig()
	return &VirtualVideoManager{
		config: cfg,
		state: VirtualVideoState{
			Enabled:      cfg.Enabled,
			Transport:    cfg.Transport,
			DevicePath:   cfg.DevicePath,
			Width:        cfg.Width,
			Height:       cfg.Height,
			FramesPerSec: cfg.FramesPerSec,
		},
	}
}

func initVirtualVideo() {
	virtualVideo = newVirtualVideoManager()
}

func normalizeVirtualVideoConfig(cfg VirtualVideoConfig) VirtualVideoConfig {
	if cfg.Transport == "" {
		cfg.Transport = "rtc-datachannel"
	}
	if cfg.DevicePath == "" {
		cfg.DevicePath = defaultVirtualVideoDevice
	}
	if cfg.Width <= 0 {
		cfg.Width = 640
	}
	if cfg.Height <= 0 {
		cfg.Height = 480
	}
	if cfg.FramesPerSec <= 0 {
		cfg.FramesPerSec = 15
	}
	if cfg.MaxFrameBytes <= 0 {
		cfg.MaxFrameBytes = defaultVirtualVideoMaxFrameSize
	}
	return cfg
}

func (m *VirtualVideoManager) SetConfig(cfg VirtualVideoConfig) (VirtualVideoState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg = normalizeVirtualVideoConfig(cfg)
	switch cfg.Transport {
	case "auto", "rtc-datachannel", "webtransport-quic":
	default:
		return m.state, fmt.Errorf("invalid virtual video transport: %s", cfg.Transport)
	}

	wasStreaming := m.state.Streaming
	if wasStreaming {
		m.closeDeviceLocked()
	}

	m.config = cfg
	m.state.Enabled = cfg.Enabled
	m.state.Streaming = false
	m.state.Transport = cfg.Transport
	m.state.DevicePath = cfg.DevicePath
	m.state.Width = cfg.Width
	m.state.Height = cfg.Height
	m.state.FramesPerSec = cfg.FramesPerSec
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
	return m.state
}

func (m *VirtualVideoManager) Start() (VirtualVideoState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config.Enabled = true
	m.state.Enabled = true
	if err := m.openDeviceLocked(); err != nil {
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
	m.closeDeviceLocked()
	m.state.LastError = ""
	return m.state
}

func (m *VirtualVideoManager) openDeviceLocked() error {
	if m.device != nil {
		m.state.Streaming = true
		return nil
	}
	f, err := os.OpenFile(m.config.DevicePath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open virtual video device %s: %w", m.config.DevicePath, err)
	}
	m.device = f
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
	if binary.BigEndian.Uint16(data[4:6]) != virtualVideoFrameVersion {
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

	frame, err := parseVirtualVideoFrame(data, m.config.MaxFrameBytes)
	if err != nil {
		m.state.LastError = err.Error()
		return err
	}
	if !m.config.Enabled {
		return nil
	}
	if m.device == nil {
		if err := m.openDeviceLocked(); err != nil {
			m.state.LastError = err.Error()
			return err
		}
	}
	if frame.Width != m.config.Width || frame.Height != m.config.Height {
		m.state.Width = frame.Width
		m.state.Height = frame.Height
	}

	n, err := m.device.Write(frame.Payload)
	if err != nil {
		m.state.LastError = err.Error()
		return fmt.Errorf("write virtual video frame: %w", err)
	}
	m.state.Transport = transport
	m.state.FramesWritten++
	m.state.BytesWritten += uint64(n)
	m.state.LastFrameAt = time.Now()
	m.state.LastError = ""
	return nil
}

func rpcGetVirtualVideoState() VirtualVideoState {
	return virtualVideo.State()
}

func rpcSetVirtualVideoConfig(cfg VirtualVideoConfig) (VirtualVideoState, error) {
	return virtualVideo.SetConfig(cfg)
}

func rpcStartVirtualVideo() (VirtualVideoState, error) {
	return virtualVideo.Start()
}

func rpcStopVirtualVideo() VirtualVideoState {
	return virtualVideo.Stop()
}
