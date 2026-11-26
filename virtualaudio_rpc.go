package kvm

import "github.com/jetkvm/kvm/internal/audio"

// virtualAudio is the virtual USB microphone: sound from the browser, played
// to the target through the gadget's audio function.
var virtualAudio *audio.Microphone

func initVirtualAudio() {
	virtualAudio = audio.NewMicrophone(
		func() bool { return config != nil && config.UsbDevices != nil && config.UsbDevices.Microphone },
		audio.OpenGadgetPlayback(alsaCaptureDevice),
	)
}

func rpcGetVirtualAudioState() audio.MicrophoneState {
	return virtualAudio.State()
}
