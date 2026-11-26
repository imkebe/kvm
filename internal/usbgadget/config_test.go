package usbgadget

import "testing"

func TestAudioGadgetConfigFollowsEnabledDevice(t *testing.T) {
	u := &UsbGadget{enabledDevices: Devices{Audio: false}}
	if u.isGadgetConfigItemEnabled("audio") {
		t.Fatal("audio gadget should be disabled when audio device is disabled")
	}

	u.enabledDevices.Audio = true
	if !u.isGadgetConfigItemEnabled("audio") {
		t.Fatal("audio gadget should be enabled when audio device is enabled")
	}
}

func TestBaseGadgetConfigItemsAlwaysEnabled(t *testing.T) {
	u := &UsbGadget{}
	for _, item := range []string{"base", "base_info", "wake_hid"} {
		if !u.isGadgetConfigItemEnabled(item) {
			t.Fatalf("%s should always be enabled", item)
		}
	}
}

// The MJPEG camera offers the one chosen frame size, or all of them.
func TestVideoFrameItemsFollowSize(t *testing.T) {
	u := &UsbGadget{enabledDevices: Devices{Video: true}}
	for _, size := range []string{"", "720", "bogus"} {
		u.customConfig.VideoSize = size
		for _, f := range VideoFrameSizes {
			want := size != "720" || f.Key == "720"
			if got := u.isGadgetConfigItemEnabled("video_streaming_mjpeg_" + f.Key); got != want {
				t.Fatalf("size %q: %s enabled %v", size, f.Key, got)
			}
		}
		if !u.isGadgetConfigItemEnabled("video_streaming_mjpeg") {
			t.Fatalf("size %q: format item off", size)
		}
	}
	u.customConfig.VideoCodec = "h264"
	if u.isGadgetConfigItemEnabled("video_streaming_mjpeg_480") {
		t.Fatal("MJPEG frame on with H.264")
	}
}
