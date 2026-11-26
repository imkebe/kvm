package usbgadget

import (
	"sort"
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
