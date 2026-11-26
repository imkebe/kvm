package kvm

import (
	"fmt"
	"sync"
)

var virtualVideoConfigMu sync.Mutex

func initVirtualVideo() {
	virtualVideo = newVirtualVideoManager()
	if config.UsbConfig != nil {
		cfg := defaultVirtualVideoConfig()
		if config.UsbConfig.VideoCodec == "h264" {
			cfg.Codec = "h264"
		}
		cfg.USBSize, cfg.USBFPS = config.UsbConfig.VideoSize, config.UsbConfig.VideoFPS
		_, _ = virtualVideo.SetConfig(cfg)
	}
	if config.UsbDevices.Video {
		if err := virtualVideo.openDeviceLocked(); err != nil {
			logger.Error().Err(err).Msg("failed to arm USB video output")
		}
		virtualVideo.state.Streaming = false
	}
}

func rpcGetVirtualVideoState() VirtualVideoState {
	return virtualVideo.State()
}

func rpcGetVirtualVideoPreview() (VirtualVideoPreview, error) {
	return virtualVideo.Preview()
}

func rpcSetVirtualVideoConfig(cfg VirtualVideoConfig) (VirtualVideoState, error) {
	virtualVideoConfigMu.Lock()
	defer virtualVideoConfigMu.Unlock()
	cfg = normalizeVirtualVideoConfig(cfg)
	virtualVideo.mu.Lock()
	previous := virtualVideo.config
	virtualVideo.mu.Unlock()
	// The USB camera's descriptors follow the codec.
	gadgetCodec := "mjpeg"
	if config.UsbConfig != nil && config.UsbConfig.VideoCodec == "h264" {
		gadgetCodec = "h264"
	}
	// The MJPEG USB camera's frame descriptors follow the size and rate.
	sameFrames := cfg.Codec != "mjpeg" || config.UsbConfig == nil ||
		(cfg.USBSize == config.UsbConfig.VideoSize && cfg.USBFPS == config.UsbConfig.VideoFPS)
	if cfg.Codec == gadgetCodec && sameFrames {
		return virtualVideo.SetConfig(cfg)
	}
	if cfg.Codec == gadgetCodec && !config.UsbDevices.Video {
		// The USB camera is off: its descriptors are made when it goes on.
		config.UsbConfig.VideoSize, config.UsbConfig.VideoFPS = cfg.USBSize, cfg.USBFPS
		gadget.SetGadgetConfig(config.UsbConfig)
		_ = SaveConfig()
		return virtualVideo.SetConfig(cfg)
	}
	stopped := cfg
	stopped.Enabled = false
	// Validate before changing the live gadget; quiesce input while USB re-enumerates.
	if _, err := virtualVideo.SetConfig(stopped); err != nil {
		return virtualVideo.State(), err
	}
	previousGadget := *config.UsbConfig
	config.UsbConfig.VideoCodec = cfg.Codec
	if cfg.Codec == "mjpeg" {
		config.UsbConfig.VideoSize, config.UsbConfig.VideoFPS = cfg.USBSize, cfg.USBFPS
	}
	gadget.SetGadgetConfig(config.UsbConfig)
	if err := updateUsbRelatedConfig(); err != nil {
		config.UsbConfig = &previousGadget
		gadget.SetGadgetConfig(config.UsbConfig)
		previous.Enabled = false
		_, _ = virtualVideo.SetConfig(previous)
		if restoreErr := updateUsbRelatedConfig(); restoreErr != nil {
			return virtualVideo.State(), fmt.Errorf("%w; restore camera profile: %v", err, restoreErr)
		}
		return virtualVideo.State(), err
	}
	return virtualVideo.SetConfig(cfg)
}

func rpcStartVirtualVideo() (VirtualVideoState, error) {
	return virtualVideo.Start()
}

func rpcStopVirtualVideo() VirtualVideoState {
	return virtualVideo.Stop()
}
