package kvm

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jetkvm/kvm/internal/osservices"
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

// DeviceStats is one sample of the KVM itself.
type DeviceStats struct {
	At int64 `json:"at"` // Unix ms
	// CPUPercent is the busy share since the previous call (or since boot
	// on the first one).
	CPUPercent   float64 `json:"cpu_percent"`
	TemperatureC int     `json:"temperature_c"`
	MemTotalKB   uint64  `json:"mem_total_kb"`
	MemAvailKB   uint64  `json:"mem_available_kb"`
	MemCachedKB  uint64  `json:"mem_cached_kb"`
	Load1        float64 `json:"load1"`
	UptimeSec    float64 `json:"uptime_sec"`
}

var (
	deviceStatsMu                   sync.Mutex
	deviceStatsBusy, deviceStatsAll uint64
	deviceStatsCPU                  float64
)

// deviceStatsMinTicks: below this many clock ticks between calls (a quarter
// second at 100 Hz) the share is noise, so the previous value stays.
const deviceStatsMinTicks = 25

func rpcGetDeviceStats() (DeviceStats, error) {
	busy, total := homeCPUTimes()
	deviceStatsMu.Lock()
	switch {
	case deviceStatsAll == 0 || total < deviceStatsAll || busy < deviceStatsBusy:
		// First call, or the counters went back: start from boot.
		if total > 0 {
			deviceStatsCPU = 100 * float64(busy) / float64(total)
		}
		deviceStatsBusy, deviceStatsAll = busy, total
	case total-deviceStatsAll >= deviceStatsMinTicks:
		deviceStatsCPU = 100 * float64(busy-deviceStatsBusy) / float64(total-deviceStatsAll)
		deviceStatsBusy, deviceStatsAll = busy, total
	}
	cpu := deviceStatsCPU
	deviceStatsMu.Unlock()
	memTotal, memAvail := homeMemory()
	stats := DeviceStats{
		At:           time.Now().UnixMilli(),
		CPUPercent:   cpu,
		TemperatureC: homeTemperature(),
		MemTotalKB:   memTotal,
		MemAvailKB:   memAvail,
		MemCachedKB:  memInfoField("Cached:"),
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(data)); len(f) > 0 {
			stats.Load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(data)); len(f) > 0 {
			stats.UptimeSec, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	return stats, nil
}

func memInfoField(name string) uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == name {
			v, _ := strconv.ParseUint(fields[1], 10, 64)
			return v
		}
	}
	return 0
}

// Device API use, counted since the app started.
var (
	deviceAPICalls    atomic.Uint64
	deviceAPILastCall atomic.Int64 // Unix ms
)

func noteDeviceAPICall() {
	deviceAPICalls.Add(1)
	deviceAPILastCall.Store(time.Now().UnixMilli())
}

// McpActivity is what the device status popover shows about the APIs.
type McpActivity struct {
	MCPEnabled         bool   `json:"mcp_enabled"`
	MCPSessions        int    `json:"mcp_sessions"`
	DeviceAPIEnabled   bool   `json:"device_api_enabled"`
	TerminalAPIEnabled bool   `json:"terminal_api_enabled"`
	TerminalReady      bool   `json:"terminal_ready"`
	Tokens             int    `json:"tokens"`
	TokensExpired      int    `json:"tokens_expired"`
	TokensActive       int    `json:"tokens_active"` // used in the last hour
	LastTokenUse       int64  `json:"last_token_use"`
	LastTokenName      string `json:"last_token_name,omitempty"`
	DeviceAPICalls     uint64 `json:"device_api_calls"`
	DeviceAPILastCall  int64  `json:"device_api_last_call"`
	APIShells          int    `json:"api_shells"`
	Shells             int    `json:"shells"`
}

func rpcGetMcpActivity() (McpActivity, error) {
	mcpMu.Lock()
	sessions := len(mcpClosers)
	mcpMu.Unlock()
	a := McpActivity{
		MCPEnabled:        config.MCPServerEnabled,
		MCPSessions:       sessions,
		DeviceAPIEnabled:  config.DeviceAPIEnabled,
		DeviceAPICalls:    deviceAPICalls.Load(),
		DeviceAPILastCall: deviceAPILastCall.Load(),
	}
	tokens, _ := rpcGetAPITokens()
	now := apiTokenNow().Unix()
	for _, t := range tokens {
		a.Tokens++
		if t.Expired {
			a.TokensExpired++
		}
		if t.LastUsedAt > 0 && now-t.LastUsedAt < 3600 {
			a.TokensActive++
		}
		if t.LastUsedAt > a.LastTokenUse {
			a.LastTokenUse, a.LastTokenName = t.LastUsedAt, t.Name
		}
	}
	if s := osServices; s != nil {
		cfg := s.config()
		a.TerminalAPIEnabled = cfg.TerminalAPIEnabled
		s.do(func(session *osservices.Session) {
			st := session.State()
			a.TerminalReady = cfg.Enabled && cfg.TerminalEnabled && st.Connected && st.Terminal
			a.Shells = len(s.terminals)
			for _, t := range s.terminals {
				if t.api {
					a.APIShells++
				}
			}
		})
	}
	return a, nil
}
