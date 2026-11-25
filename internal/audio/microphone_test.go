package audio

import (
	"errors"
	"testing"
	"time"
)

type fakeMicSink struct {
	accept  int // frames accepted per write; -1 accepts all
	err     error
	written [][]int16
	closed  bool
}

func (s *fakeMicSink) WriteFrames(pcm []int16) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	frames := len(pcm) / 2
	if s.accept >= 0 && s.accept < frames {
		frames = s.accept
	}
	s.written = append(s.written, append([]int16(nil), pcm[:frames*2]...))
	return frames, nil
}

func (s *fakeMicSink) Close() error { s.closed = true; return nil }

func monoFrame(samples ...int16) []byte {
	out := []byte{1}
	for _, s := range samples {
		out = append(out, byte(uint16(s)), byte(uint16(s)>>8))
	}
	return out
}

func TestParseMicrophoneFrameMonoBecomesStereo(t *testing.T) {
	pcm, err := parseMicrophoneFrame(monoFrame(1, -2, 300), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []int16{1, 1, -2, -2, 300, 300}
	if len(pcm) != len(want) {
		t.Fatalf("got %v, want %v", pcm, want)
	}
	for i := range want {
		if pcm[i] != want[i] {
			t.Fatalf("got %v, want %v", pcm, want)
		}
	}
}

func TestParseMicrophoneFrameStereoKeepsChannels(t *testing.T) {
	data := []byte{2, 0x01, 0x00, 0xFF, 0xFF, 0x02, 0x00, 0xFE, 0xFF}
	pcm, err := parseMicrophoneFrame(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []int16{1, -1, 2, -2}
	for i := range want {
		if pcm[i] != want[i] {
			t.Fatalf("got %v, want %v", pcm, want)
		}
	}
}

func TestParseMicrophoneFrameRejectsBadInput(t *testing.T) {
	big := make([]byte, 1+2*(microphoneMaxFrames+1))
	big[0] = 1
	for name, data := range map[string][]byte{
		"empty":         nil,
		"header only":   {1},
		"three channel": {3, 0, 0, 0, 0, 0, 0},
		"odd bytes":     {1, 0, 0, 0},
		"half stereo":   {2, 0, 0, 0, 0, 0, 0},
		"too long":      big,
	} {
		if _, err := parseMicrophoneFrame(data, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func newTestMic(sink *fakeMicSink, enabled *bool) (*Microphone, *int) {
	opens := 0
	m := NewMicrophone(func() bool { return *enabled }, func() (MicrophoneSink, error) {
		opens++
		return sink, nil
	})
	return m, &opens
}

func TestMicrophonePlaysOnlyTheActiveChannelWhileEnabled(t *testing.T) {
	sink := &fakeMicSink{accept: -1}
	enabled := true
	m, opens := newTestMic(sink, &enabled)

	first := m.BeginChannel()
	second := m.BeginChannel() // replaces the first
	if err := m.HandleChannelFrameData(first, monoFrame(1, 2)); err != nil || len(sink.written) != 0 {
		t.Fatalf("replaced channel played: err=%v written=%d", err, len(sink.written))
	}
	if err := m.HandleChannelFrameData(second, monoFrame(1, 2)); err != nil || len(sink.written) != 1 {
		t.Fatalf("active channel: err=%v written=%d", err, len(sink.written))
	}
	enabled = false
	_ = m.HandleChannelFrameData(second, monoFrame(1, 2))
	if len(sink.written) != 1 || *opens != 1 {
		t.Fatalf("played while the microphone is off: written=%d opens=%d", len(sink.written), *opens)
	}
	m.StopChannel(second)
	if m.State().Streaming {
		t.Fatal("still streaming after the channel closed")
	}
}

func TestMicrophoneCountsDroppedFrames(t *testing.T) {
	sink := &fakeMicSink{accept: 1} // the device takes one frame per write
	enabled := true
	m, _ := newTestMic(sink, &enabled)
	id := m.BeginChannel()
	if err := m.HandleChannelFrameData(id, monoFrame(1, 2, 3, 4)); err != nil {
		t.Fatal(err)
	}
	state := m.State()
	if state.FramesWritten != 1 || state.FramesDropped != 3 || !state.DeviceReady {
		t.Fatalf("state %+v", state)
	}
}

func TestMicrophoneReopensAfterWriteErrorAndWaits(t *testing.T) {
	sink := &fakeMicSink{accept: -1, err: errors.New("device gone")}
	enabled := true
	m, opens := newTestMic(sink, &enabled)
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	id := m.BeginChannel()

	if err := m.HandleChannelFrameData(id, monoFrame(1)); err == nil {
		t.Fatal("write error not reported")
	}
	if !sink.closed || m.State().DeviceReady || m.State().LastError == "" {
		t.Fatalf("sink not released: %+v closed=%v", m.State(), sink.closed)
	}
	sink.err = nil
	sink.closed = false
	if err := m.HandleChannelFrameData(id, monoFrame(1)); err != nil || *opens != 1 {
		t.Fatalf("reopened too early: err=%v opens=%d", err, *opens)
	}
	now = now.Add(microphoneReopenWait + time.Millisecond)
	if err := m.HandleChannelFrameData(id, monoFrame(1)); err != nil || *opens != 2 || m.State().FramesWritten != 1 {
		t.Fatalf("not reopened: err=%v opens=%d state=%+v", err, *opens, m.State())
	}
}
