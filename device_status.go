package kvm

import (

)

// RPCs behind the browser's device status popover and the USB settings. The
// popover asks only while it is open, so nothing here samples in the
// background.

// notifyUsbDevices tells the browser which USB functions are on, so the
// controls that need one follow at once.
func notifyUsbDevices() {
	if currentSession == nil || config.UsbDevices == nil {
		return
	}
	devices := *config.UsbDevices
	session := currentSession
	go writeJSONRPCEvent("usbDevices", devices, session)
}
