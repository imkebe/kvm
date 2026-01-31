package kvm

import (
	"context"

	"github.com/jetkvm/kvm/internal/usbext"
)

var usbExtensionManager *usbext.Manager

func initUsbExtensions() {
	controller, err := usbext.NewDefaultController(usbLogger)
	if err != nil {
		usbLogger.Warn().Err(err).Msg("failed to initialize usb extension controller")
	}

	usbExtensionManager = usbext.NewManager(controller, usbLogger)
	if config == nil || config.UsbDevices == nil {
		return
	}

	if err := usbExtensionManager.ApplyDeviceConfig(*config.UsbDevices); err != nil {
		usbLogger.Warn().Err(err).Msg("failed to apply initial USB extension configuration")
	}

	if err := usbExtensionManager.RefreshStatus(context.Background()); err != nil {
		usbLogger.Debug().Err(err).Msg("initial usb extension status refresh failed")
	}
}

func applyUsbExtensionDeviceConfig() error {
	if usbExtensionManager == nil {
		return nil
	}
	if config == nil || config.UsbDevices == nil {
		return nil
	}

	return usbExtensionManager.ApplyDeviceConfig(*config.UsbDevices)
}

func getUsbExtensionStatus() usbext.Status {
	if usbExtensionManager == nil {
		return usbext.Status{}
	}
	return usbExtensionManager.Status()
}
