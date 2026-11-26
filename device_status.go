package kvm

import (
	"errors"
	"strings"

	"github.com/jetkvm/kvm/internal/usbgadget"
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

func rpcGetUsbFunctionNames() (usbgadget.FunctionNames, error) {
	if config.UsbConfig == nil || config.UsbConfig.Names == nil {
		return usbgadget.FunctionNames{}, nil
	}
	return *config.UsbConfig.Names, nil
}

// rpcSetUsbFunctionNames sets the camera, audio and storage names. It
// reconnects the USB devices on the target when anything changed.
func rpcSetUsbFunctionNames(names usbgadget.FunctionNames) error {
	names.Camera = strings.TrimSpace(names.Camera)
	names.Audio = strings.TrimSpace(names.Audio)
	names.StorageVendor = strings.TrimSpace(names.StorageVendor)
	names.StorageModel = strings.TrimSpace(names.StorageModel)
	if err := names.Validate(); err != nil {
		return err
	}
	if config.UsbConfig == nil {
		return errors.New("the USB configuration is not loaded")
	}
	current := usbgadget.FunctionNames{}
	if config.UsbConfig.Names != nil {
		current = *config.UsbConfig.Names
	}
	if current == names {
		return nil
	}
	osServicesNoteKVMRestart()
	previous := config.UsbConfig.Names
	config.UsbConfig.Names = &names
	gadget.SetGadgetConfig(config.UsbConfig)
	if err := updateUsbRelatedConfig(); err != nil {
		config.UsbConfig.Names = previous
		gadget.SetGadgetConfig(config.UsbConfig)
		_ = updateUsbRelatedConfig()
		return err
	}
	return SaveConfig()
}
