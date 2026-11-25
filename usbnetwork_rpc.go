package kvm

import "github.com/jetkvm/kvm/internal/usbgadget"

func initUSBNetwork() {
	usbNetwork = newUSBNetworkManager()
	if config.USBNetworkConfig != nil {
		if _, err := usbNetwork.SetConfig(*config.USBNetworkConfig); err != nil {
			logger.Error().Err(err).Msg("invalid saved USB network configuration")
			return
		}
	}
	if usbNetwork.config.Enabled && config.UsbDevices.Network {
		if _, err := usbNetwork.Start(); err != nil {
			logger.Error().Err(err).Msg("failed to start saved USB network")
		}
	}
}

func rpcGetUSBNetworkState() USBNetworkState {
	return usbNetwork.State()
}

func rpcSetUSBNetworkConfig(cfg USBNetworkConfig) (USBNetworkState, error) {
	cfg = normalizeUSBNetworkConfig(cfg)
	if err := validateUSBNetworkConfig(cfg); err != nil {
		return usbNetwork.State(), err
	}
	previous := *config.UsbConfig
	host, dev := gadgetNetworkAddresses(cfg)
	// Stop clears Enabled, so neither the gadget change nor SetConfig restarts it.
	restart := false
	if previous.NetworkFunction != cfg.Function ||
		orDefault(previous.NetworkHostAddr, usbgadget.DefaultNetworkHostAddr) != host ||
		orDefault(previous.NetworkDevAddr, usbgadget.DefaultNetworkDevAddr) != dev {
		restart = usbNetwork.State().Running
		usbNetwork.Stop()
		config.UsbConfig.NetworkFunction = cfg.Function
		config.UsbConfig.NetworkHostAddr, config.UsbConfig.NetworkDevAddr = host, dev
		gadget.SetGadgetConfig(config.UsbConfig)
		if err := updateUsbRelatedConfig(); err != nil {
			*config.UsbConfig = previous
			gadget.SetGadgetConfig(config.UsbConfig)
			_ = gadget.UpdateGadgetConfig()
			return usbNetwork.State(), err
		}
	}
	state, err := usbNetwork.SetConfig(cfg)
	if err != nil {
		return state, err
	}
	if restart && cfg.Enabled {
		state, err = usbNetwork.Start()
	}
	config.USBNetworkConfig = &cfg
	refreshNetbirdTarget()
	if saveErr := SaveConfig(); err == nil {
		err = saveErr
	}
	return state, err
}

func rpcStartUSBNetwork() (USBNetworkState, error) {
	state, err := usbNetwork.Start()
	cfg := usbNetwork.Config()
	config.USBNetworkConfig = &cfg
	if err == nil {
		err = SaveConfig()
	}
	return state, err
}

func rpcStopUSBNetwork() USBNetworkState {
	state := usbNetwork.Stop()
	if config.USBNetworkConfig != nil {
		config.USBNetworkConfig.Enabled = false
		_ = SaveConfig()
	}
	return state
}
