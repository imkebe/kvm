package usbgadget

import "strconv"

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
		// configfs reads this attribute in decimal. Match that representation
		// to avoid rewriting a header that is locked by its class links.
		"bcdUVC":           "272", // UVC 1.1 (0x0110)
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
	},
}

// VideoFrameSize is one MJPEG frame size of the USB camera. Key is the
// height, as Config.VideoSize names it.
type VideoFrameSize struct {
	Key        string `json:"key"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	minBitRate int
	maxBitRate int
	order      uint
}

// VideoFrameSizes are the MJPEG frame sizes. The frame directories are
// created in order, which gives them their frame index: 480p is 1, 720p 2,
// 360p 3, 1080p 4, then 540p, 600p, 768p and 900p. internal/uvc reads the
// index back from configfs. New sizes go at the end, so the old indexes stay.
// The browser sends sizes above 960x540 over the camera WebSocket; the
// WebRTC data channel is too slow for them.
var VideoFrameSizes = []VideoFrameSize{
	{"480", 640, 480, 18432000, 55296000, 5004},
	{"720", 1280, 720, 55296000, 110592000, 5005},
	{"360", 640, 360, 13824000, 55296000, 5006},
	{"1080", 1920, 1080, 55296000, 110592000, 5007},
	{"540", 960, 540, 31104000, 93312000, 5011},
	{"600", 800, 600, 28800000, 86400000, 5012},
	{"768", 1024, 768, 47185920, 110592000, 5013},
	{"900", 1600, 900, 55296000, 110592000, 5014},
}

// VideoFrameRates are the frame rates the MJPEG camera can offer, fastest
// first. Config.VideoFPS picks one; 0 offers all.
var VideoFrameRates = []int{30, 25, 24, 20, 15, 10, 5}

// uvcFrameInterval is a frame rate as UVC writes it: 100 ns units.
func uvcFrameInterval(fps int) string {
	return strconv.Itoa(10000000 / fps)
}

func videoFrameConfig(f VideoFrameSize) gadgetConfigItem {
	return gadgetConfigItem{
		order:  f.order,
		device: "uvc.usb0",
		path:   []string{"functions", "uvc.usb0", "streaming", "mjpeg", "m", f.Key + "p"},
		attrs: gadgetAttributes{
			"wWidth":                    strconv.Itoa(f.Width),
			"wHeight":                   strconv.Itoa(f.Height),
			"dwMinBitRate":              strconv.Itoa(f.minBitRate),
			"dwMaxBitRate":              strconv.Itoa(f.maxBitRate),
			"dwMaxVideoFrameBufferSize": strconv.Itoa(f.Width * f.Height * 2),
			"dwDefaultFrameInterval":    uvcFrameInterval30FPS,
			"dwFrameInterval":           uvcFrameInterval30FPS + "\n" + uvcFrameInterval15FPS,
		},
	}
}

var videoStreamingHeaderConfig = gadgetConfigItem{
	order:  5020,
	device: "uvc.usb0",
	path:   []string{"functions", "uvc.usb0", "streaming", "header", "h"},
}

var videoStreamingClassFSConfig = gadgetConfigItem{
	order:  5021,
	device: "uvc.usb0",
	path:   []string{"functions", "uvc.usb0", "streaming", "class", "fs"},
}

var videoLinksConfig = gadgetConfigItem{
	order:     5022,
	device:    "uvc.usb0",
	path:      []string{"functions", "uvc.usb0"},
	linksOnly: true,
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
		{
			path:   []string{"control", "class", "ss", "h"},
			target: []string{"control", "header", "h"},
		},
		{
			path:   []string{"streaming", "class", "hs", "h"},
			target: []string{"streaming", "header", "h"},
		},
		{
			path:   []string{"streaming", "class", "ss", "h"},
			target: []string{"streaming", "header", "h"},
		},
	},
}

// Separate function instances avoid rewriting descriptors locked by configfs
// class links. Only one UVC function is linked into the composite gadget.
func h264VideoConfig(item gadgetConfigItem) gadgetConfigItem {
	item.device = "uvc.h264"
	item.path = append([]string(nil), item.path...)
	if len(item.path) > 1 {
		item.path[1] = "uvc.h264"
	}
	if item.configPath != nil {
		item.configPath = []string{"uvc.h264"}
	}
	return item
}

var videoStreamingH264Config = gadgetConfigItem{
	order: 5003, device: "uvc.usb0",
	path: []string{"functions", "uvc.usb0", "streaming", "framebased", "h"},
	attrs: gadgetAttributes{
		"bDefaultFrameIndex": "1",
		// Standard H264 GUID; the vendor kernel defaults to this value too.
		"guidFormat": "H264\x00\x00\x10\x00\x80\x00\x00\xaa\x00\x38\x9b\x71",
	},
}
var videoStreamingH264480Config = gadgetConfigItem{
	order: 5004, device: "uvc.usb0",
	path: []string{"functions", "uvc.usb0", "streaming", "framebased", "h", "480p"},
	attrs: gadgetAttributes{
		"wWidth": "640", "wHeight": "480",
		"dwMinBitRate": "100000", "dwMaxBitRate": "8000000",
		"dwDefaultFrameInterval": uvcFrameInterval15FPS,
		"dwFrameInterval":        uvcFrameInterval15FPS, "dwBytesPerLine": "0",
	},
}
var videoH264LinksConfig = func() gadgetConfigItem {
	item := videoLinksConfig
	item.symlinks = append([]gadgetSymlink(nil), item.symlinks...)
	item.symlinks[1] = gadgetSymlink{
		path:   []string{"streaming", "header", "h", "m"},
		target: []string{"streaming", "framebased", "h"},
	}
	return item
}()
