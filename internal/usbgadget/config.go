package usbgadget

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"
)

type gadgetConfigItem struct {
	order       uint
	device      string
	path        []string
	attrs       gadgetAttributes
	configAttrs gadgetAttributes
	configPath  []string
	reportDesc  []byte
	symlinks    []gadgetSymlink
	linksOnly   bool
	// inEndpoints is upstream's count of the IN endpoints the function
	// takes. This build counts them with inEndpointsByKey instead, which
	// also knows which of them carry data (see inendpoints.go); a test
	// keeps the two in step.
	inEndpoints int
}

type gadgetAttributes map[string]string

type gadgetSymlink struct {
	path   []string
	target []string
}

type gadgetConfigItemWithKey struct {
	key  string
	item gadgetConfigItem
}

type orderedGadgetConfigItems []gadgetConfigItemWithKey

var defaultGadgetConfig = map[string]gadgetConfigItem{
	"base": {
		order: 0,
		attrs: gadgetAttributes{
			"bcdUSB":    "0x0200", // USB 2.0
			"idVendor":  "0x1d6b", // The Linux Foundation
			"idProduct": "0x0104", // Multifunction Composite Gadget
			"bcdDevice": "0x0100", // USB2
		},
		configAttrs: gadgetAttributes{
			"MaxPower":     "250",  // in unit of 2mA
			"bmAttributes": "0xa0", // bus-powered + remote wakeup
		},
	},
	"base_info": {
		order:      1,
		path:       []string{"strings", "0x409"},
		configPath: []string{"strings", "0x409"},
		attrs: gadgetAttributes{
			"serialnumber": "",
			"manufacturer": "JetKVM",
			"product":      "JetKVM USB Emulation Device",
		},
		configAttrs: gadgetAttributes{
			"configuration": "Config 1: HID",
		},
	},
	// keyboard HID
	"keyboard": keyboardConfig,
	"wake_hid": wakeHIDConfig,
	// mouse HID
	"absolute_mouse": absoluteMouseConfig,
	// relative mouse HID
	"relative_mouse": relativeMouseConfig,
	// USB audio sink
	"audio": audioConfig,
	// mass storage
	"mass_storage_base": massStorageBaseConfig,
	"mass_storage_lun0": massStorageLun0Config,
	// serial console (CDC-ACM)
	"serial_console": serialConsoleConfig,
	// virtual camera (UVC)
	"video_base":                  videoBaseConfig,
	"video_control_header":        videoControlHeaderConfig,
	"video_control_class_fs":      videoControlClassFSConfig,
	"video_streaming_mjpeg":       videoStreamingMJPEGConfig,
	"video_streaming_header":      videoStreamingHeaderConfig,
	"video_streaming_class_fs":    videoStreamingClassFSConfig,
	"video_links":                 videoLinksConfig,
	"video_h264_base":             h264VideoConfig(videoBaseConfig),
	"video_h264_control_header":   h264VideoConfig(videoControlHeaderConfig),
	"video_h264_control_class_fs": h264VideoConfig(videoControlClassFSConfig),
	"video_h264_format":           h264VideoConfig(videoStreamingH264Config),
	"video_h264_480":              h264VideoConfig(videoStreamingH264480Config),
	"video_h264_header":           h264VideoConfig(videoStreamingHeaderConfig),
	"video_h264_class_fs":         h264VideoConfig(videoStreamingClassFSConfig),
	"video_h264_links":            h264VideoConfig(videoH264LinksConfig),
	// virtual USB network
	"network_ncm":   networkConfig("ncm", 6000),
	"network_ecm":   networkConfig("ecm", 6001),
	"network_rndis": networkConfig("rndis", 6002),
}

func init() {
	for key, item := range spareINConfigs() {
		defaultGadgetConfig[key] = item
	}
	for _, f := range VideoFrameSizes {
		defaultGadgetConfig["video_streaming_mjpeg_"+f.Key] = videoFrameConfig(f)
	}
}

func (u *UsbGadget) isGadgetConfigItemEnabled(itemKey string) bool {
	if _, _, isSpare := parseSpareKey(itemKey); isSpare {
		return u.isSpareItemEnabled(itemKey)
	}
	return u.isBaseGadgetConfigItemEnabled(itemKey)
}

// isBaseGadgetConfigItemEnabled is the enabled state of an item that does
// not depend on the endpoint plan.
func (u *UsbGadget) isBaseGadgetConfigItemEnabled(itemKey string) bool {
	if strings.HasPrefix(itemKey, "video_h264_") {
		return u.enabledDevices.Video && u.customConfig.VideoCodec == "h264"
	}
	// The MJPEG frame items, one for each of VideoFrameSizes.
	if strings.HasPrefix(itemKey, "video_streaming_mjpeg_") {
		size := u.customConfig.VideoSize
		return u.enabledDevices.Video && u.customConfig.VideoCodec != "h264" &&
			(!ValidVideoSize(size) || itemKey == "video_streaming_mjpeg_"+size)
	}
	switch itemKey {
	case "absolute_mouse":
		return u.enabledDevices.AbsoluteMouse
	case "relative_mouse":
		return u.enabledDevices.RelativeMouse
	case "keyboard":
		return u.enabledDevices.Keyboard
	case "mass_storage_base":
		return u.enabledDevices.MassStorage
	case "mass_storage_lun0":
		return u.enabledDevices.MassStorage
	case "serial_console":
		return u.enabledDevices.SerialConsole
	case "video_base",
		"video_control_header",
		"video_control_class_fs",
		"video_streaming_mjpeg",
		"video_streaming_header",
		"video_streaming_class_fs",
		"video_links":
		return u.enabledDevices.Video && u.customConfig.VideoCodec != "h264"
	case "network_ncm", "network_ecm", "network_rndis":
		function := u.customConfig.NetworkFunction
		if function == "" {
			function = "ncm"
		}
		return u.enabledDevices.Network && itemKey == "network_"+function
	case "audio":
		return u.enabledDevices.Audio || u.enabledDevices.Microphone
	default:
		if f := registeredExternalFunction(); f != nil && itemKey == f.Key {
			return u.externalReady
		}
		return true
	}
}

// ValidVideoSize reports whether size names one of VideoFrameSizes.
func ValidVideoSize(size string) bool {
	for _, f := range VideoFrameSizes {
		if f.Key == size {
			return true
		}
	}
	return false
}

// ValidVideoFPS reports whether fps is one of VideoFrameRates.
func ValidVideoFPS(fps int) bool {
	for _, r := range VideoFrameRates {
		if r == fps {
			return true
		}
	}
	return false
}

// videoIntervals is the dwFrameInterval list for a VideoFPS: the one rate,
// or every rate (fastest first) for 0.
func videoIntervals(fps int) (list, first string) {
	if ValidVideoFPS(fps) {
		i := uvcFrameInterval(fps)
		return i, i
	}
	var parts []string
	for _, r := range VideoFrameRates {
		parts = append(parts, uvcFrameInterval(r))
	}
	return strings.Join(parts, "\n"), parts[0]
}

// syncVideoAttrs sets the frame rates of the MJPEG frame sizes.
func (u *UsbGadget) syncVideoAttrs() {
	intervals, first := videoIntervals(u.customConfig.VideoFPS)
	for _, f := range VideoFrameSizes {
		size := f.Key
		if item, ok := u.configMap["video_streaming_mjpeg_"+size]; ok {
			item.attrs["dwDefaultFrameInterval"] = first
			item.attrs["dwFrameInterval"] = intervals
		}
	}
}

func (u *UsbGadget) loadGadgetConfig() {
	u.syncAudioAttrs()
	u.syncVideoAttrs()
	u.syncFunctionNames()
	u.syncNetworkAddresses()
	if u.customConfig.isEmpty {
		u.log.Trace().Msg("using default gadget config")
		return
	}

	u.configMap["base"].attrs["idVendor"] = u.customConfig.VendorId
	u.configMap["base"].attrs["idProduct"] = u.customConfig.ProductId

	u.configMap["base_info"].attrs["serialnumber"] = u.customConfig.SerialNumber
	u.configMap["base_info"].attrs["manufacturer"] = u.customConfig.Manufacturer
	u.configMap["base_info"].attrs["product"] = u.customConfig.Product
	u.configMap["base_info"].configAttrs["configuration"] = u.customConfig.Configuration
}

func (u *UsbGadget) SetGadgetConfig(config *Config) {
	u.configLock.Lock()
	defer u.configLock.Unlock()

	if config == nil {
		return // nothing to do
	}

	u.customConfig = *config
	u.loadGadgetConfig()
}

func (u *UsbGadget) SetGadgetDevices(devices *Devices) {
	u.configLock.Lock()
	defer u.configLock.Unlock()

	if devices == nil {
		return // nothing to do
	}

	u.enabledDevices = *devices
}

// GetConfigPath returns the path to the config item.
func (u *UsbGadget) GetConfigPath(itemKey string) (string, error) {
	item, ok := u.configMap[itemKey]
	if !ok {
		return "", fmt.Errorf("config item %s not found", itemKey)
	}
	return joinPath(u.kvmGadgetPath, item.configPath), nil
}

// GetPath returns the path to the item.
func (u *UsbGadget) GetPath(itemKey string) (string, error) {
	item, ok := u.configMap[itemKey]
	if !ok {
		return "", fmt.Errorf("config item %s not found", itemKey)
	}
	return joinPath(u.kvmGadgetPath, item.path), nil
}

// OverrideGadgetConfig overrides the gadget config for the given item and attribute.
// It returns an error if the item is not found or the attribute is not found.
// It returns true if the attribute is overridden, false otherwise.
func (u *UsbGadget) OverrideGadgetConfig(itemKey string, itemAttr string, value string) (error, bool) {
	u.configLock.Lock()
	defer u.configLock.Unlock()

	// get it as a pointer
	_, ok := u.configMap[itemKey]
	if !ok {
		return fmt.Errorf("config item %s not found", itemKey), false
	}

	if u.configMap[itemKey].attrs[itemAttr] == value {
		return nil, false
	}

	u.configMap[itemKey].attrs[itemAttr] = value
	u.log.Info().Str("itemKey", itemKey).Str("itemAttr", itemAttr).Str("value", value).Msg("overriding gadget config")

	return nil, true
}

func mountConfigFS(path string) error {
	err := exec.Command("mount", "-t", "configfs", "none", path).Run()
	if err != nil {
		return fmt.Errorf("failed to mount configfs: %w", err)
	}
	return nil
}

func (u *UsbGadget) Init() error {
	u.configLock.Lock()
	defer u.configLock.Unlock()

	u.loadGadgetConfig()

	udcs := getUdcs()
	if len(udcs) < 1 {
		return u.logWarn("no udc found, skipping USB stack init", nil)
	}

	u.udc = udcs[0]

	u.syncMassStorageImageFromKernel()

	// A restarted process inherits the previous instance's gadget. Leave it
	// alone when it already matches, instead of dropping the host session.
	adopted, err := u.configureUsbGadget(true, false)
	if err != nil {
		return u.logError("unable to initialize USB stack", err)
	}
	if adopted {
		u.adoptLiveGadget()
	}

	return nil
}

func (u *UsbGadget) UpdateGadgetConfig() error {
	u.configLock.Lock()
	defer u.configLock.Unlock()

	u.loadGadgetConfig()

	// Callers ask for this on config changes and as the recovery fallback,
	// so always rebind here even if the configfs tree looks untouched.
	_, err := u.configureUsbGadget(true, true)
	if err != nil {
		u.log.Error().Err(err).Msg("unable to update gadget config")
		return err
	}

	return nil
}

// configureUsbGadget reports whether it left an already matching, attached
// gadget bound instead of rebinding it.
func (u *UsbGadget) configureUsbGadget(resetUsb bool, forceRebind bool) (bool, error) {
	var adopted bool
	// Recovery can fall back here after a public rebind fails. Keep the entire
	// transaction behind the same drain gate, including its controller writes.
	err := u.withHIDRebind(func() error {
		var err error
		adopted, err = u.configureUsbGadgetLocked(resetUsb, forceRebind)
		return err
	})
	return adopted, err
}

// videoFramesWanted is the MJPEG frame directories the configuration asks
// for, and the frame intervals of each.
func (u *UsbGadget) videoFramesWanted() (map[string]bool, string) {
	wanted := map[string]bool{}
	for _, f := range VideoFrameSizes {
		if u.isBaseGadgetConfigItemEnabled("video_streaming_mjpeg_" + f.Key) {
			wanted[f.Key+"p"] = true
		}
	}
	intervals := ""
	if item, ok := u.configMap["video_streaming_mjpeg_480"]; ok {
		intervals = item.attrs["dwFrameInterval"]
	}
	return wanted, intervals
}

// dropStaleVideoFrames removes the MJPEG camera's frame descriptors when
// they are not the ones the configuration asks for, so that the transaction
// makes them again. configfs lets a frame change only while nothing links
// to its format: it detaches the gadget and removes the function's links
// first. It reports whether it removed the frames.
func (u *UsbGadget) dropStaleVideoFrames() (bool, error) {
	wanted, intervals := u.videoFramesWanted()
	function := path.Join(u.kvmGadgetPath, "functions", "uvc.usb0")
	frames := path.Join(function, "streaming", "mjpeg", "m")
	entries, _ := os.ReadDir(frames)
	existing := []string{}
	stale := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		existing = append(existing, entry.Name())
		current, _ := os.ReadFile(path.Join(frames, entry.Name(), "dwFrameInterval"))
		if !wanted[entry.Name()] || strings.TrimSpace(string(current)) != strings.TrimSpace(intervals) {
			stale = true
		}
	}
	if len(wanted) == 0 || len(existing) == 0 || (!stale && len(existing) == len(wanted)) {
		return false, nil
	}
	u.log.Info().Strs("frames", existing).Msg("rebuilding the USB camera's frame descriptors")
	u.ResetHIDFiles()
	_ = softDisconnect(u.udc)
	if err := os.WriteFile(path.Join(u.kvmGadgetPath, "UDC"), []byte("\n"), 0644); err != nil &&
		!os.IsNotExist(err) && !errors.Is(err, syscall.ENODEV) {
		return false, fmt.Errorf("detach gadget to rebuild camera frames: %w", err)
	}
	for _, link := range []string{
		path.Join(u.configC1Path, "uvc.usb0"),
		path.Join(function, "streaming", "class", "fs", "h"),
		path.Join(function, "streaming", "class", "hs", "h"),
		path.Join(function, "streaming", "class", "ss", "h"),
		path.Join(function, "streaming", "header", "h", "m"),
	} {
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("unlink %s: %w", link, err)
		}
	}
	for _, name := range existing {
		if err := syscall.Rmdir(path.Join(frames, name)); err != nil {
			return false, fmt.Errorf("remove camera frame %s: %w", name, err)
		}
	}
	return true, nil
}

func (u *UsbGadget) configureUsbGadgetLocked(resetUsb bool, forceRebind bool) (bool, error) {
	if resetUsb && forceRebind {
		if _, err := u.dropStaleVideoFrames(); err != nil {
			return false, err
		}
	}
	u.externalReady = u.prepareExternalFunction()
	u.setExternalDeviceRevision()
	disconnected := false
	adopted := false
	err := u.WithTransaction(func() error {
		u.tx.MountConfigFS()
		u.tx.CreateConfigPath()
		u.tx.WriteGadgetConfig()
		if !resetUsb {
			return nil
		}

		if !forceRebind {
			pending, err := u.tx.HasPendingChanges()
			if err != nil {
				return err
			}
			if !pending && u.isGadgetLive() {
				// The configfs tree already matches and the gadget is bound and
				// attached. Leave the host session alone: every disconnect races
				// the mass storage function's kernel thread (#1360), so only pay
				// for it when the configuration actually changes.
				u.log.Info().Msg("USB gadget already configured and attached, skipping rebind")
				adopted = true
				return nil
			}
		}

		// Close HID descriptors before the unbind for the same reason as in
		// rebindUsb, then quiesce the host session before the controller
		// teardown so function drivers shut their endpoints down against a
		// live controller.
		// Unlinking any other function unbinds UVC too. Its embedded
		// video_device cannot be registered a second time. Even when removing
		// the other link makes the remaining order correct, recreate the links
		// so configfs frees that UVC function and allocates a fresh instance.
		if u.enabledDevices.Video && u.tx.reorderSymlinkChanges != nil {
			u.tx.reorderSymlinkChanges.ForceRecreate = true
		}
		u.ResetHIDFiles()
		_ = softDisconnect(u.udc)
		disconnected = true
		// Reset stale endpoint state before attaching a fresh UVC function.
		// Resetting DWC3 after WriteGadgetConfig would bind the same UVC
		// video_device twice and fault the vendor kernel.
		if u.enabledDevices.Video {
			if err := resetControllerForVideo(u.udc, u.kvmGadgetPath); err != nil {
				return err
			}
		} else {
			u.tx.RebindUsb(true)
		}
		return nil
	})
	if err != nil && disconnected && !u.enabledDevices.Video {
		_ = softConnect(u.udc)
	}
	// UVC binds deactivated and activates the composite gadget when its
	// userspace control handler subscribes to SETUP events. Forcing the
	// pull-up here exposes the camera before that handler is ready and can
	// leave macOS unable to start capture after a layout change.
	// Layout changes invalidate HID handover even when UVC activates USB later.
	if disconnected {
		u.resetHidHandover()
	}
	return adopted && err == nil, err
}

// isGadgetLive reports whether the host can see the device as currently
// configured: the UDC driver is bound, the gadget is attached to it and the
// pull-up is on. The last check matters for a process that died between its
// soft disconnect and the rebind: the first two still hold, but the host
// sees nothing until something reconnects.
func (u *UsbGadget) isGadgetLive() bool {
	bound, err := u.IsUDCBound()
	return err == nil && bound && u.IsGadgetAttachedToUDC() && isPullupEnabled(u.udc)
}
