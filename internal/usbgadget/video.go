package usbgadget

const (
	uvcFrameInterval15FPS = "666666"
	uvcFrameInterval30FPS = "333333"
)

var videoBaseConfig = gadgetConfigItem{
	order:      5000,
	device:     "uvc.usb0",
	path:       []string{"functions", "uvc.usb0"},
	configPath: []string{"uvc.usb0"},
	attrs: gadgetAttributes{
		"streaming_interval":  "1",
		"streaming_maxpacket": "1024",
	},
}

var videoControlHeaderConfig = gadgetConfigItem{
	order:  5001,
	device: "uvc.usb0",
	path:   []string{"functions", "uvc.usb0", "control", "header", "h"},
	attrs: gadgetAttributes{
		"bcdUVC":           "0x0110",
		"dwClockFrequency": "48000000",
	},
}

var videoControlClassFSConfig = gadgetConfigItem{
	order:  5002,
	device: "uvc.usb0",
	path:   []string{"functions", "uvc.usb0", "control", "class", "fs"},
}

var videoStreamingMJPEGConfig = gadgetConfigItem{
	order:  5003,
	device: "uvc.usb0",
	path:   []string{"functions", "uvc.usb0", "streaming", "mjpeg", "m"},
	attrs: gadgetAttributes{
		"bDefaultFrameIndex": "1",
		"bAspectRatioX":      "0",
		"bAspectRatioY":      "0",
		"bInterfaceNumber":   "0",
	},
}

var videoStreamingMJPEG480Config = gadgetConfigItem{
	order:  5004,
	device: "uvc.usb0",
	path:   []string{"functions", "uvc.usb0", "streaming", "mjpeg", "m", "480p"},
	attrs: gadgetAttributes{
		"wWidth":                    "640",
		"wHeight":                   "480",
		"dwMinBitRate":              "18432000",
		"dwMaxBitRate":              "55296000",
		"dwMaxVideoFrameBufferSize": "614400",
		"dwDefaultFrameInterval":    uvcFrameInterval15FPS,
		"dwFrameInterval":           uvcFrameInterval15FPS + "\n" + uvcFrameInterval30FPS,
	},
}

var videoStreamingMJPEG720Config = gadgetConfigItem{
	order:  5005,
	device: "uvc.usb0",
	path:   []string{"functions", "uvc.usb0", "streaming", "mjpeg", "m", "720p"},
	attrs: gadgetAttributes{
		"wWidth":                    "1280",
		"wHeight":                   "720",
		"dwMinBitRate":              "55296000",
		"dwMaxBitRate":              "110592000",
		"dwMaxVideoFrameBufferSize": "1843200",
		"dwDefaultFrameInterval":    uvcFrameInterval15FPS,
		"dwFrameInterval":           uvcFrameInterval15FPS + "\n" + uvcFrameInterval30FPS,
	},
}

var videoStreamingHeaderConfig = gadgetConfigItem{
	order:  5006,
	device: "uvc.usb0",
	path:   []string{"functions", "uvc.usb0", "streaming", "header", "h"},
}

var videoStreamingClassFSConfig = gadgetConfigItem{
	order:  5007,
	device: "uvc.usb0",
	path:   []string{"functions", "uvc.usb0", "streaming", "class", "fs"},
}

var videoLinksConfig = gadgetConfigItem{
	order:  5008,
	device: "uvc.usb0",
	path:   []string{"functions", "uvc.usb0"},
	symlinks: []gadgetSymlink{
		{
			path:   []string{"control", "class", "fs", "h"},
			target: []string{"control", "header", "h"},
		},
		{
			path:   []string{"streaming", "header", "h", "m"},
			target: []string{"streaming", "mjpeg", "m"},
		},
		{
			path:   []string{"streaming", "class", "fs", "h"},
			target: []string{"streaming", "header", "h"},
		},
	},
}
