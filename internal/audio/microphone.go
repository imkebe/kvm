package audio

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// The virtual microphone carries sound from the browser to the target
// computer: the browser captures its microphone and sends PCM over the
// "microphone" data channel, and the KVM plays it into the UAC1 gadget's
// playback stream, which the target records as a USB microphone.
//
// A message is one byte with the channel count (1 or 2), then 48 kHz signed
// 16-bit little-endian samples, interleaved. The KVM turns mono into stereo.
const (
	microphoneMaxFrames  = 4800 // 100 ms per message
	microphoneReopenWait = 500 * time.Millisecond
)

// MicrophoneState is what the browser shows for the microphone.
type MicrophoneState struct {
	// Enabled: the microphone USB device is on.
	Enabled bool `json:"enabled"`
	// Streaming: a browser is sending audio.
	Streaming bool `json:"streaming"`
	// DeviceReady: the gadget's playback stream is open.
	DeviceReady   bool   `json:"deviceReady"`
	SampleRate    int    `json:"sampleRate"`
	Channels      int    `json:"channels"`
	FramesWritten uint64 `json:"framesWritten"`
	// FramesDropped counts frames that did not fit: the target is not
	// recording, or the browser sent faster than real time.
	FramesDropped uint64 `json:"framesDropped"`
	LastError     string `json:"lastError,omitempty"`
}

type MicrophoneSink interface {
	WriteFrames(pcm []int16) (int, error)
	Close() error
}

type Microphone struct {
	mu      sync.Mutex
	enabled func() bool
	open    func() (MicrophoneSink, error)
	now     func() time.Time

	sink          MicrophoneSink
	nextChannel   uint64
	activeChannel uint64
	lastOpen      time.Time
	stereo        []int16
	state         MicrophoneState
}

func NewMicrophone(enabled func() bool, open func() (MicrophoneSink, error)) *Microphone {
	return &Microphone{
		enabled: enabled,
		open:    open,
		now:     time.Now,
		state:   MicrophoneState{SampleRate: PlaybackSampleRate, Channels: PlaybackChannels},
	}
}

// parseMicrophoneFrame returns interleaved stereo samples in dst.
func parseMicrophoneFrame(data []byte, dst []int16) ([]int16, error) {
	if len(data) < 3 {
		return nil, errors.New("audio frame is too short")
	}
	channels := int(data[0])
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("unsupported channel count %d", channels)
	}
	body := data[1:]
	frameBytes := 2 * channels
	if len(body)%frameBytes != 0 {
		return nil, errors.New("audio frame is not a whole number of samples")
	}
	frames := len(body) / frameBytes
	if frames == 0 || frames > microphoneMaxFrames {
		return nil, fmt.Errorf("audio frame has %d frames; want 1 to %d", frames, microphoneMaxFrames)
	}
	if cap(dst) < frames*2 {
		dst = make([]int16, frames*2)
	}
	dst = dst[:frames*2]
	for i := 0; i < frames; i++ {
		left := int16(uint16(body[i*frameBytes]) | uint16(body[i*frameBytes+1])<<8)
		right := left
		if channels == 2 {
			right = int16(uint16(body[i*frameBytes+2]) | uint16(body[i*frameBytes+3])<<8)
		}
		dst[i*2] = left
		dst[i*2+1] = right
	}
	return dst, nil
}

// BeginChannel makes a new browser channel the one that is played. An older
// channel stops being played, so two tabs do not mix.
func (m *Microphone) BeginChannel() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextChannel++
	m.activeChannel = m.nextChannel
	m.state.Streaming = true
	return m.activeChannel
}

func (m *Microphone) StopChannel(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeChannel == id {
		m.activeChannel = 0
		m.state.Streaming = false
	}
}

// HandleChannelFrameData plays one message from channel id.
func (m *Microphone) HandleChannelFrameData(id uint64, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id != m.activeChannel {
		return nil // a replaced channel: ignore its audio
	}
	if !m.enabled() {
		return nil // microphone off: nothing to play to
	}
	pcm, err := parseMicrophoneFrame(data, m.stereo)
	if err != nil {
		m.state.LastError = err.Error()
		return err
	}
	m.stereo = pcm
	frames := len(pcm) / PlaybackChannels

	if m.sink == nil {
		if now := m.now(); now.Sub(m.lastOpen) < microphoneReopenWait {
			m.state.FramesDropped += uint64(frames)
			return nil
		} else {
			m.lastOpen = now
		}
		sink, err := m.open()
		if err != nil {
			m.state.LastError = err.Error()
			m.state.FramesDropped += uint64(frames)
			return nil // the gadget may still be re-enumerating; try again soon
		}
		m.sink = sink
		m.state.DeviceReady = true
		m.state.LastError = ""
	}

	written, err := m.sink.WriteFrames(pcm)
	if err != nil {
		m.closeLocked()
		// Wait before the next open: the device just failed.
		m.lastOpen = m.now()
		m.state.LastError = err.Error()
		m.state.FramesDropped += uint64(frames)
		return err
	}
	m.state.FramesWritten += uint64(written)
	m.state.FramesDropped += uint64(frames - written)
	return nil
}

// Close releases the playback device, for example before the gadget is
// rebuilt. The next frame opens it again.
func (m *Microphone) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
}

func (m *Microphone) closeLocked() {
	if m.sink != nil {
		_ = m.sink.Close()
		m.sink = nil
	}
	m.state.DeviceReady = false
	m.lastOpen = time.Time{}
}

func (m *Microphone) State() MicrophoneState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state
	state.Enabled = m.enabled()
	return state
}

// OpenGadgetPlayback returns an open function for NewMicrophone that opens
// the ALSA playback device.
func OpenGadgetPlayback(device func() string) func() (MicrophoneSink, error) {
	return func() (MicrophoneSink, error) {
		return OpenALSAPlayback(device())
	}
}
