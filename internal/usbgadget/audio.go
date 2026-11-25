package usbgadget

var audioConfig = gadgetConfigItem{
	order:      2500,
	device:     "uac1.usb0",
	path:       []string{"functions", "uac1.usb0"},
	configPath: []string{"uac1.usb0"},
	attrs: gadgetAttributes{
		"c_chmask": "3",
		"c_srate":  "48000",
		"c_ssize":  "2",
		"p_chmask": "0",
		// We capture PCM without applying the gadget's ALSA mixer controls.
		// Do not advertise hardware volume/mute that we cannot implement;
		// hosts can apply software volume instead. Disable both directions:
		// f_uac1 allocates a status IN endpoint if either feature unit is
		// enabled, even when p_chmask is zero. Avoiding that endpoint also
		// keeps mass storage on EP5 IN, below the RV1106 FIFO-reset bug
		// affecting EP6 IN on existing system images (#1634).
		"c_mute_present":   "0",
		"c_volume_present": "0",
		"p_mute_present":   "0",
		"p_volume_present": "0",
	},
	// The host only sends audio (p_chmask 0), and no feature unit asks for
	// the status endpoint.
	inEndpoints: 0,
}

// syncAudioAttrs sets the directions of the audio function from the enabled
// devices. c_chmask is the host-to-KVM direction (the KVM captures what the
// host plays); p_chmask is the KVM-to-host direction, the virtual
// microphone. f_uac1 needs at least one direction. Both stay off the status
// endpoint: the feature units are disabled above.
func (u *UsbGadget) syncAudioAttrs() {
	item, ok := u.configMap["audio"]
	if !ok || item.attrs == nil {
		return
	}
	capture, microphone := "0", "0"
	if u.enabledDevices.Audio || !u.enabledDevices.Microphone {
		capture = "3"
	}
	if u.enabledDevices.Microphone {
		microphone = "3"
		item.attrs["p_srate"] = "48000"
		item.attrs["p_ssize"] = "2"
	}
	item.attrs["c_chmask"] = capture
	item.attrs["p_chmask"] = microphone
}
