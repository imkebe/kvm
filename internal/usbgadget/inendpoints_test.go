package usbgadget

import (
	"sort"
	"strings"
	"testing"
)

func planGadget(devices Devices, spareOK bool) *UsbGadget {
	return &UsbGadget{
		configMap:       deepCopyConfigMap(defaultGadgetConfig),
		enabledDevices:  devices,
		spareINOverride: &spareOK,
	}
}

// inNumbers lists the IN endpoint number of each endpoint, by function key,
// as the controller would give them out.
func inNumbers(u *UsbGadget) map[string][]int {
	type linked struct {
		key   string
		order uint
	}
	var functions []linked
	for key, item := range u.configMap {
		if !u.isGadgetConfigItemEnabled(key) || item.configPath == nil {
			continue
		}
		if len(u.inEndpointsOf(key)) == 0 {
			if _, _, spare := parseSpareKey(key); !spare {
				continue
			}
		}
		functions = append(functions, linked{key, item.order})
	}
	sort.Slice(functions, func(i, j int) bool { return functions[i].order < functions[j].order })

	numbers := make(map[string][]int)
	next := 1
	for _, f := range functions {
		eps := u.inEndpointsOf(f.key)
		if _, _, spare := parseSpareKey(f.key); spare {
			eps = []inEndpointKind{inAux}
		}
		for range eps {
			numbers[f.key] = append(numbers[f.key], next)
			next++
		}
	}
	return numbers
}

func dataEndpointsAreReliable(t *testing.T, u *UsbGadget) {
	t.Helper()
	for key, numbers := range inNumbers(u) {
		if _, _, spare := parseSpareKey(key); spare {
			continue
		}
		for i, kind := range u.inEndpointsOf(key) {
			if kind == inData && unreliableINEndpoint(numbers[i]) {
				t.Errorf("%s: data IN endpoint %d is unreliable (all: %v)", key, numbers[i], inNumbers(u))
			}
		}
	}
}

var allDefault = Devices{AbsoluteMouse: true, RelativeMouse: true, Keyboard: true, MassStorage: true}

func TestSparesNotNeededForDefaultDevices(t *testing.T) {
	u := planGadget(allDefault, true)
	if plan := u.plannedSpares(); len(plan) != 0 {
		t.Fatalf("spares planned for the default devices: %v", plan)
	}
	dataEndpointsAreReliable(t, u)
}

func TestMicrophoneGetsSpareBeforeAudio(t *testing.T) {
	// HID x4 take 1-4 and the audio function would take 5, which pushes mass
	// storage onto IN 6. The microphone is fine on 5; mass storage must not
	// land on 6, so a spare goes before mass storage.
	d := allDefault
	d.Microphone = true
	u := planGadget(d, true)
	dataEndpointsAreReliable(t, u)
	if got := u.plannedSpares()[2990]; got != 1 {
		t.Fatalf("spares before mass storage = %d, want 1; plan %v", got, u.plannedSpares())
	}
	if !u.isGadgetConfigItemEnabled(spareKey(2990, 1)) || u.isGadgetConfigItemEnabled(spareKey(2990, 2)) {
		t.Fatal("expected exactly the first spare in slot 2990 to be enabled")
	}
}

func TestAudioAttrsFollowDevices(t *testing.T) {
	cases := []struct {
		devices      Devices
		capture, mic string
	}{
		{Devices{Audio: true}, "3", "0"},
		{Devices{Microphone: true}, "0", "3"},
		{Devices{Audio: true, Microphone: true}, "3", "3"},
	}
	for _, c := range cases {
		u := planGadget(c.devices, false)
		u.syncAudioAttrs()
		attrs := u.configMap["audio"].attrs
		if attrs["c_chmask"] != c.capture || attrs["p_chmask"] != c.mic {
			t.Errorf("%+v: c_chmask=%s p_chmask=%s, want %s %s", c.devices, attrs["c_chmask"], attrs["p_chmask"], c.capture, c.mic)
		}
		if !u.isGadgetConfigItemEnabled("audio") {
			t.Errorf("%+v: audio item disabled", c.devices)
		}
	}
}

// Upstream counts a function's IN endpoints in gadgetConfigItem.inEndpoints.
// Where it sets a count, the count must match inEndpointsByKey.
func TestINEndpointCountsMatchUpstream(t *testing.T) {
	for key, item := range defaultGadgetConfig {
		if item.inEndpoints == 0 {
			continue
		}
		if got := len(inEndpointsByKey[key]); got != item.inEndpoints {
			t.Errorf("%s: inEndpointsByKey has %d IN endpoints, the item has %d", key, got, item.inEndpoints)
		}
	}
}

// The controller has nine IN endpoints. The default devices with the camera
// and OS Services fit; adding the network does not.
func TestINEndpointBudget(t *testing.T) {
	previous := registeredExternalFunction()
	t.Cleanup(func() {
		externalFunctionLock.Lock()
		externalFunction = previous
		externalFunctionLock.Unlock()
	})
	RegisterExternalFunction(ExternalFunction{Key: "external", Instance: "external", Order: 9000})

	spareOK := true
	u := &UsbGadget{configMap: defaultGadgetConfig, spareINOverride: &spareOK}

	devices := defaultUsbGadgetDevices
	if got := u.INEndpointsNeeded(devices, false); got != 5 {
		t.Fatalf("default devices: %d IN endpoints, want 5", got)
	}
	// OS Services would start on 6: spare on 6, its endpoint on 7.
	if got := u.INEndpointsNeeded(devices, true); got != 7 {
		t.Fatalf("default devices with the external function: %d, want 7", got)
	}
	devices.Video = true
	// Camera on 6 (control) and 7 (streaming); OS Services would start on
	// 8: spare on 8, its endpoint on 9.
	if err := u.CheckINEndpoints(devices, true); err != nil {
		t.Fatalf("camera and the external function: %v", err)
	}
}

// The usage split adds up to the count the gadget checks.
func TestINEndpointUsageMatchesNeeded(t *testing.T) {
	previous := registeredExternalFunction()
	t.Cleanup(func() {
		externalFunctionLock.Lock()
		externalFunction = previous
		externalFunctionLock.Unlock()
	})
	RegisterExternalFunction(ExternalFunction{Key: "external", Instance: "external", Order: 9000})
	spareOK := true
	u := &UsbGadget{configMap: defaultGadgetConfig, spareINOverride: &spareOK}
	for _, devices := range []Devices{
		defaultUsbGadgetDevices,
		{Keyboard: true},
		{Keyboard: true, AbsoluteMouse: true, RelativeMouse: true, Video: true, Microphone: true, Audio: true},
	} {
		for _, external := range []bool{false, true} {
			usage := u.INEndpointUsage(devices, external)
			sum := 0
			for _, item := range usage.Items {
				sum += item.Endpoints
			}
			if want := u.INEndpointsNeeded(devices, external); usage.Used != want || sum != want {
				t.Errorf("%+v external=%v: used %d, sum %d, want %d (%+v)", devices, external, usage.Used, sum, want, usage.Items)
			}
			if usage.Total != MaxINEndpoints {
				t.Errorf("total %d", usage.Total)
			}
		}
	}
}

// New camera sizes keep the old frame indexes (directory creation order
// follows order) and every rate maps to a UVC interval.
func TestVideoFrameTable(t *testing.T) {
	wantFirst := []string{"480", "720", "360", "1080"}
	sorted := append([]VideoFrameSize(nil), VideoFrameSizes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].order < sorted[j].order })
	for i, key := range wantFirst {
		if sorted[i].Key != key {
			t.Fatalf("frame index %d is %s, want %s", i+1, sorted[i].Key, key)
		}
	}
	for _, f := range VideoFrameSizes {
		if f.order <= videoStreamingMJPEGConfig.order || f.order >= videoStreamingHeaderConfig.order {
			t.Errorf("%s: order %d outside the frame range", f.Key, f.order)
		}
		if _, ok := defaultGadgetConfig["video_streaming_mjpeg_"+f.Key]; !ok {
			t.Errorf("%s: no gadget item", f.Key)
		}
	}
	list, first := videoIntervals(0)
	if first != "333333" || len(strings.Fields(list)) != len(VideoFrameRates) {
		t.Fatalf("all rates: %q first %q", list, first)
	}
	if list, first := videoIntervals(24); list != "416666" || first != "416666" {
		t.Fatalf("24 fps: %q %q", list, first)
	}
	if !ValidVideoSize("900") || ValidVideoSize("800") || !ValidVideoFPS(25) || ValidVideoFPS(60) {
		t.Fatal("size or rate validation")
	}
}

func TestFunctionNames(t *testing.T) {
	if (FunctionNames{}).inquiryString() != "JetKVM  Virtual Media" {
		t.Fatalf("default inquiry %q", (FunctionNames{}).inquiryString())
	}
	if got := (FunctionNames{StorageVendor: "Generic", StorageModel: "Flash Disk"}).inquiryString(); got != "Generic Flash Disk" {
		t.Fatalf("inquiry %q", got)
	}
	for _, bad := range []FunctionNames{{Camera: strings.Repeat("x", 32)}, {StorageVendor: "123456789"},
		{StorageModel: strings.Repeat("y", 17)}, {Audio: "naïve"}} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	u := planGadget(Devices{Video: true, Audio: true, MassStorage: true}, true)
	u.customConfig = Config{Names: &FunctionNames{Camera: "Desk Camera"}}
	u.syncFunctionNames()
	if u.configMap["video_base"].attrs["function_name"] != "Desk Camera" ||
		u.configMap["audio"].attrs["function_name"] != DefaultAudioName {
		t.Fatalf("names %v %v", u.configMap["video_base"].attrs, u.configMap["audio"].attrs)
	}
}
