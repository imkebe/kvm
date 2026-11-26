package usbgadget

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// The RV1106 USB controller loses data on two of its IN endpoints: after the
// host enumerates the device again, IN endpoint 6 sends nothing and IN
// endpoint 8 sends stale data, until the controller is bound again (#1634).
// The controller hands out IN endpoints in the order the functions are
// linked, and each function's driver claims its own in a fixed order.
//
// A function that carries data must not get one of those numbers. When one
// would, the gadget links a spare function first. The spare is a vendor
// defined HID function with no OUT endpoint, so it takes exactly one IN
// endpoint, and the host ignores it. Nothing opens its device node.

// inEndpointKind says what a function does with one of its IN endpoints.
type inEndpointKind uint8

const (
	// inData carries traffic that must arrive: it must not take a number
	// from unreliableINEndpoint.
	inData inEndpointKind = iota
	// inAux is a status or notification endpoint. A function can live with
	// it on a bad number.
	inAux
)

// spareINMarker exists on system images whose kernel allows more than four
// HID functions (HIDG_MINORS is 8). Without it a fifth HID function cannot
// be created, so the gadget adds no spare.
const spareINMarker = "/etc/jetkvm-usb-spare-in"

const (
	// maxSparesPerSlot is how many spares can go before one function.
	maxSparesPerSlot = 2
	spareKeyPrefix   = "spare_in_"
)

// inEndpointsByKey lists the IN endpoints of the item that links each
// function, in the order the driver claims them (usb_ep_autoconfig).
var inEndpointsByKey = map[string][]inEndpointKind{
	"keyboard":       {inData},
	"absolute_mouse": {inData},
	"relative_mouse": {inData},
	"wake_hid":       {inData},
	// With p_chmask set, f_uac1 claims one isochronous IN endpoint. The
	// feature units stay off, so it has no status endpoint.
	"audio":             {inData},
	"mass_storage_base": {inData},
	// f_acm: bulk IN, bulk OUT, then the notification endpoint.
	"serial_console": {inData, inAux},
	// f_uvc: the control endpoint first, then the streaming endpoint.
	"video_base":      {inAux, inData},
	"video_h264_base": {inAux, inData},
	// f_ncm, f_ecm and f_rndis: bulk IN, bulk OUT, then notification.
	"network_ncm":   {inData, inAux},
	"network_ecm":   {inData, inAux},
	"network_rndis": {inData, inAux},
}

// spareSlots maps a function's order to the order where its spares go. The
// slot sits just before the function and after the one before it.
var spareSlots = map[uint]uint{
	2500: 2490, // audio
	3000: 2990, // mass storage
	4000: 3990, // serial console
	5000: 4990, // video
	6000: 5990, // network (ncm)
	6001: 5990, // network (ecm)
	6002: 5990, // network (rndis)
}

func spareKey(slot uint, n int) string {
	return spareKeyPrefix + strconv.FormatUint(uint64(slot), 10) + "_" + strconv.Itoa(n)
}

// parseSpareKey returns the slot and the number (1-based) of a spare item.
func parseSpareKey(key string) (uint, int, bool) {
	rest, ok := strings.CutPrefix(key, spareKeyPrefix)
	if !ok {
		return 0, 0, false
	}
	slotText, nText, ok := strings.Cut(rest, "_")
	if !ok {
		return 0, 0, false
	}
	slot, err := strconv.ParseUint(slotText, 10, 32)
	if err != nil {
		return 0, 0, false
	}
	n, err := strconv.Atoi(nText)
	if err != nil || n < 1 {
		return 0, 0, false
	}
	return uint(slot), n, true
}

var spareINConfigAttrs = gadgetAttributes{
	"protocol":        "0",
	"subclass":        "0",
	"report_length":   "1",
	"no_out_endpoint": "1",
}

// spareINConfigs returns the spare items, two for each slot. They are
// enabled only as far as the plan needs them.
func spareINConfigs() map[string]gadgetConfigItem {
	items := make(map[string]gadgetConfigItem)
	seen := make(map[uint]bool)
	for _, slot := range spareSlots {
		if seen[slot] {
			continue
		}
		seen[slot] = true
		for n := 1; n <= maxSparesPerSlot; n++ {
			name := "hid.spare" + strconv.FormatUint(uint64(slot), 10) + "n" + strconv.Itoa(n)
			attrs := gadgetAttributes{}
			for k, v := range spareINConfigAttrs {
				attrs[k] = v
			}
			items[spareKey(slot, n)] = gadgetConfigItem{
				order:      slot + uint(n) - 1,
				device:     name,
				path:       []string{"functions", name},
				configPath: []string{name},
				attrs:      attrs,
				reportDesc: wakeReportDesc,
			}
		}
	}
	return items
}

func spareINSupported() bool {
	_, err := os.Stat(spareINMarker)
	return err == nil
}

// inEndpointsOf returns the IN endpoints of the function that an item links.
func (u *UsbGadget) inEndpointsOf(key string) []inEndpointKind {
	// The audio function takes an IN endpoint only for the microphone
	// direction; the host-to-KVM direction needs an OUT endpoint.
	if key == "audio" && !u.enabledDevices.Microphone {
		return nil
	}
	return inEndpointsByKey[key]
}

// plannedSpares returns how many spare functions go in each slot, so that no
// function puts a data endpoint on an unreliable number. A function whose
// order has no slot gets none. When spares are not supported the plan is
// empty and the gadget keeps the endpoint numbers it would have had.
func (u *UsbGadget) plannedSpares() map[uint]int {
	plan := make(map[uint]int)
	if !u.spareINAvailable() {
		return plan
	}

	type function struct {
		order uint
		eps   []inEndpointKind
	}
	var functions []function
	for key, item := range u.configMap {
		if _, _, isSpare := parseSpareKey(key); isSpare {
			continue
		}
		eps := u.inEndpointsOf(key)
		if len(eps) == 0 || !u.isBaseGadgetConfigItemEnabled(key) {
			continue
		}
		functions = append(functions, function{order: item.order, eps: eps})
	}
	sort.Slice(functions, func(i, j int) bool { return functions[i].order < functions[j].order })

	used := 0 // IN endpoints taken so far
	for _, f := range functions {
		slot, hasSlot := spareSlots[f.order]
		spares := 0
		if hasSlot {
			for spares < maxSparesPerSlot && !inEndpointsReliable(used+spares, f.eps) {
				spares++
			}
			if spares > 0 {
				plan[slot] = spares
			}
		}
		used += spares + len(f.eps)
	}
	return plan
}

// inEndpointsReliable reports whether no data endpoint of a function that
// starts after `before` IN endpoints has an unreliable number.
func inEndpointsReliable(before int, eps []inEndpointKind) bool {
	for i, kind := range eps {
		if kind == inData && unreliableINEndpoint(before+i+1) {
			return false
		}
	}
	return true
}

// isSpareItemEnabled reports whether the plan needs the spare item.
func (u *UsbGadget) isSpareItemEnabled(key string) bool {
	slot, n, ok := parseSpareKey(key)
	if !ok {
		return false
	}
	return u.plannedSpares()[slot] >= n
}

func (u *UsbGadget) spareINAvailable() bool {
	if u.spareINOverride != nil {
		return *u.spareINOverride
	}
	return spareINSupported()
}

// inEndpointsBefore is the number of IN endpoints taken before a function at
// order: those of the enabled functions that link earlier, and the spares
// the plan puts before them. The controller numbers IN endpoints from 1 in
// link order, so the function's first IN endpoint is this number plus 1.
// The external (FunctionFS) function uses it to decide whether its own
// descriptors need a spare IN endpoint.
func (u *UsbGadget) inEndpointsBefore(order uint) int {
	count := 0
	for key, item := range u.configMap {
		if item.order >= order {
			continue
		}
		if _, _, isSpare := parseSpareKey(key); isSpare {
			continue
		}
		if !u.isBaseGadgetConfigItemEnabled(key) {
			continue
		}
		count += len(u.inEndpointsOf(key))
	}
	for slot, spares := range u.plannedSpares() {
		if slot < order {
			count += spares
		}
	}
	return count
}

// MaxINEndpoints is the number of IN endpoints of the RV1106 USB
// controller, besides the control endpoint. A gadget that needs more does
// not bind, and then the host has no USB device at all.
const MaxINEndpoints = 9

// INEndpointsNeeded is the number of IN endpoints the gadget takes with the
// given devices: those of the functions, the spares the plan adds, and with
// external also the external (FunctionFS) function's endpoint and its spare.
func (u *UsbGadget) INEndpointsNeeded(devices Devices, external bool) int {
	u.configLock.Lock()
	plan := &UsbGadget{
		enabledDevices:  devices,
		customConfig:    u.customConfig,
		configMap:       u.configMap,
		spareINOverride: u.spareINOverride,
	}
	u.configLock.Unlock()

	needed := plan.inEndpointsBefore(^uint(0))
	if f := registeredExternalFunction(); external && f != nil {
		if plan.externalNeedsSpareIN(f) {
			needed++
		}
		needed++
	}
	return needed
}

// CheckINEndpoints returns an error when the devices need more IN endpoints
// than the controller has.
func (u *UsbGadget) CheckINEndpoints(devices Devices, external bool) error {
	needed := u.INEndpointsNeeded(devices, external)
	if needed <= MaxINEndpoints {
		return nil
	}
	return fmt.Errorf(
		"these USB devices need %d IN endpoints and the USB controller has %d; turn off a device (camera, network and serial console take 2 each; OS Services takes 1 or 2)",
		needed, MaxINEndpoints)
}

// INEndpointUse is how many IN endpoints one USB function takes.
type INEndpointUse struct {
	Name      string `json:"name"`
	Endpoints int    `json:"endpoints"`
}

// INEndpointUsage is the gadget's IN endpoint budget for a set of devices.
type INEndpointUsage struct {
	Total int             `json:"total"`
	Used  int             `json:"used"`
	Items []INEndpointUse `json:"items"`
}

// inEndpointGroups names the items as the settings show them.
var inEndpointGroups = map[string]string{
	"keyboard": "keyboard", "absolute_mouse": "absolute_mouse", "relative_mouse": "relative_mouse",
	"wake_hid": "wake", "audio": "microphone", "mass_storage_base": "mass_storage",
	"serial_console": "serial_console", "video_base": "video", "video_h264_base": "video",
	"network_ncm": "network", "network_ecm": "network", "network_rndis": "network",
}

var inEndpointGroupOrder = []string{"keyboard", "absolute_mouse", "relative_mouse", "wake",
	"microphone", "mass_storage", "serial_console", "video", "network", "os_services", "spare"}

// INEndpointUsage returns what each function takes with the given devices:
// the same count as INEndpointsNeeded, split by function. "spare" holds the
// spare functions that keep data off IN 6 and IN 8, "wake" the always-on
// wake-up HID function.
func (u *UsbGadget) INEndpointUsage(devices Devices, external bool) INEndpointUsage {
	u.configLock.Lock()
	plan := &UsbGadget{
		enabledDevices:  devices,
		customConfig:    u.customConfig,
		configMap:       u.configMap,
		spareINOverride: u.spareINOverride,
	}
	u.configLock.Unlock()

	counts := map[string]int{}
	for key := range plan.configMap {
		group, ok := inEndpointGroups[key]
		if !ok || !plan.isBaseGadgetConfigItemEnabled(key) {
			continue
		}
		counts[group] += len(plan.inEndpointsOf(key))
	}
	for _, spares := range plan.plannedSpares() {
		counts["spare"] += spares
	}
	if f := registeredExternalFunction(); external && f != nil {
		counts["os_services"]++
		if plan.externalNeedsSpareIN(f) {
			counts["spare"]++
		}
	}
	usage := INEndpointUsage{Total: MaxINEndpoints}
	for _, name := range inEndpointGroupOrder {
		if n := counts[name]; n > 0 {
			usage.Items = append(usage.Items, INEndpointUse{Name: name, Endpoints: n})
			usage.Used += n
		}
	}
	return usage
}
