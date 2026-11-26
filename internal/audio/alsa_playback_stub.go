//go:build !linux || !cgo

package audio

import "fmt"

type ALSAPlayback struct{}

func OpenALSAPlayback(device string) (*ALSAPlayback, error) {
	return nil, fmt.Errorf("ALSA audio playback is not available for this build: %s", device)
}

func (*ALSAPlayback) WriteFrames([]int16) (int, error) { return 0, ErrPlaybackClosed }

func (*ALSAPlayback) Close() error { return nil }
