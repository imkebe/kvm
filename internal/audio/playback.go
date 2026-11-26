package audio

import "errors"

// The virtual microphone plays 48 kHz stereo, the format of the UAC1
// gadget's playback direction (see usbgadget.syncAudioAttrs).
const (
	PlaybackSampleRate   = 48000
	PlaybackChannels     = 2
	PlaybackPeriodFrames = 480 // 10 ms
	PlaybackPeriods      = 8   // 80 ms of buffer
)

// ErrPlaybackClosed is returned for a write to a closed playback device.
var ErrPlaybackClosed = errors.New("audio playback is closed")
