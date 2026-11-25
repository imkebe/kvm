package hostconsole

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tools that manage the KVM itself through its device API.

const rpcTimeout = 30 * time.Second

func pretty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		return string(raw)
	}
	return b.String()
}

// compact writes a result on one line, or the error.
func compact(c rpcCall) string {
	if c.Err != nil {
		return "error: " + c.Err.Error()
	}
	var b bytes.Buffer
	if json.Compact(&b, c.Result) != nil {
		return string(c.Result)
	}
	return b.String()
}

// networkSummary keeps the useful part of getNetworkState, which also lists
// every IPv6 address with its lifetimes.
func networkSummary(raw json.RawMessage) json.RawMessage {
	var full map[string]any
	if json.Unmarshal(raw, &full) != nil {
		return raw
	}
	keep := map[string]any{}
	for _, k := range []string{"interface_name", "hostname", "mac_address", "up", "online",
		"ipv4_address", "ipv6_address", "ipv4_addresses"} {
		if v, ok := full[k]; ok {
			keep[k] = v
		}
	}
	if lease, ok := full["dhcp_lease"].(map[string]any); ok {
		for _, k := range []string{"routers", "dns_servers", "domain"} {
			if v, ok := lease[k]; ok {
				keep[k] = v
			}
		}
	}
	out, err := json.Marshal(keep)
	if err != nil {
		return raw
	}
	return out
}

func (s *server) report(ctx context.Context, calls []rpcCall) (string, error) {
	if err := s.kvm.batch(ctx, calls, rpcTimeout); err != nil {
		return "", err
	}
	var lines []string
	for _, c := range calls {
		lines = append(lines, c.Method+": "+compact(c))
	}
	return strings.Join(lines, "\n"), nil
}

type powerIn struct {
	Action string `json:"action" jsonschema:"status, press (short power button press), hold (5 s press: forced off), reset, dc_on or dc_off"`
}

type wolIn struct {
	MAC         string `json:"mac,omitempty" jsonschema:"MAC address, or the name of a saved Wake-on-LAN device; empty lists the saved devices"`
	BroadcastIP string `json:"broadcast_ip,omitempty" jsonschema:"broadcast address, default the KVM's"`
}

type rebootIn struct {
	Force bool `json:"force,omitempty" jsonschema:"reboot even while an update is pending"`
}

type mediaIn struct {
	Action   string `json:"action" jsonschema:"status, list, mount_url, mount_file, unmount, download, download_status, cancel_download or delete"`
	URL      string `json:"url,omitempty" jsonschema:"image URL for mount_url and download"`
	Filename string `json:"filename,omitempty" jsonschema:"file in the KVM's storage for mount_file, download and delete"`
	Mode     string `json:"mode,omitempty" jsonschema:"cdrom (default) or disk"`
}

type usbIn struct {
	Action string `json:"action" jsonschema:"list, enable or disable"`
	Device string `json:"device,omitempty" jsonschema:"video (camera), microphone, audio, network, massStorage or serialConsole"`
}

type keyboardIn struct {
	Text    string   `json:"text,omitempty" jsonschema:"text to type, as on a US keyboard; a line break presses Enter"`
	Keys    []string `json:"keys,omitempty" jsonschema:"key combinations pressed one after another after the text, e.g. ctrl+alt+delete, enter, f12, win+r"`
	Macro   string   `json:"macro,omitempty" jsonschema:"name or ID of a keyboard macro saved on the KVM, run instead of text and keys"`
	DelayMS int      `json:"delay_ms,omitempty" jsonschema:"milliseconds between key events, default 20"`
}

type clipboardIn struct {
	Text *string `json:"text,omitempty" jsonschema:"text to put on the target's clipboard; leave out to read the clipboard"`
}

type hostInfoIn struct {
	Section string `json:"section,omitempty" jsonschema:"summary (default), processes or usb"`
}

type rpcIn struct {
	Method string         `json:"method" jsonschema:"device API method, e.g. getUsbConfig, getJigglerState, setJigglerState"`
	Params map[string]any `json:"params,omitempty" jsonschema:"named parameters"`
}

var usbDevicesAllowed = map[string]bool{
	"video": true, "microphone": true, "audio": true, "network": true, "massStorage": true, "serialConsole": true,
}

func (s *server) registerDevice(m *mcp.Server) {
	mcp.AddTool(m, &mcp.Tool{
		Name:        "kvm_status",
		Description: "The KVM's state: firmware version, network, HDMI video input, USB link to the target, power (ATX or DC extension), virtual media and USB functions.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ statusIn) (*mcp.CallToolResult, any, error) {
		calls := []rpcCall{
			{Method: "getLocalVersion"}, {Method: "getDeviceID"}, {Method: "getNetworkState"},
			{Method: "getVideoState"}, {Method: "getUSBState"}, {Method: "getUsbDevices"},
			{Method: "getActiveExtension"}, {Method: "getATXState"}, {Method: "getDCPowerState"},
			{Method: "getVirtualMediaState"}, {Method: "getKeyboardLayout"},
		}
		if err := s.kvm.batch(ctx, calls, rpcTimeout); err != nil {
			return nil, nil, err
		}
		var lines []string
		for _, c := range calls {
			if c.Method == "getNetworkState" && c.Err == nil {
				c.Result = networkSummary(c.Result)
			}
			lines = append(lines, c.Method+": "+compact(c))
		}
		if note := s.kvm.expiryNote(time.Now()); note != "" {
			lines = append(lines, note)
		}
		return text("%s", strings.Join(lines, "\n")), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name: "kvm_power",
		Description: "Power of the target computer through the KVM's power extension: the ATX extension (press, hold, reset) or the DC extension (dc_on, dc_off). " +
			"status shows the active extension and power state. hold forces the computer off and reset restarts it without shutting down: data can be lost.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in powerIn) (*mcp.CallToolResult, any, error) {
		var call rpcCall
		switch in.Action {
		case "", "status":
			out, err := s.report(ctx, []rpcCall{{Method: "getActiveExtension"}, {Method: "getATXState"}, {Method: "getDCPowerState"}})
			if err != nil {
				return nil, nil, err
			}
			return text("%s", out), nil, nil
		case "press":
			call = rpcCall{Method: "setATXPowerAction", Params: map[string]any{"action": "power-short"}}
		case "hold":
			call = rpcCall{Method: "setATXPowerAction", Params: map[string]any{"action": "power-long"}}
		case "reset":
			call = rpcCall{Method: "setATXPowerAction", Params: map[string]any{"action": "reset"}}
		case "dc_on", "dc_off":
			call = rpcCall{Method: "setDCPowerState", Params: map[string]any{"enabled": in.Action == "dc_on"}}
		default:
			return nil, nil, fmt.Errorf("unknown action %q", in.Action)
		}
		if _, err := s.kvm.rpc(ctx, call.Method, call.Params, rpcTimeout); err != nil {
			return nil, nil, err
		}
		return text("%s done; check with kvm_power status in a few seconds", in.Action), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name:        "kvm_wake_on_lan",
		Description: "Send a Wake-on-LAN packet from the KVM, to a MAC address or a saved device; without one, list the saved devices.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in wolIn) (*mcp.CallToolResult, any, error) {
		saved, err := s.kvm.rpc(ctx, "getWakeOnLanDevices", nil, rpcTimeout)
		if err != nil {
			return nil, nil, err
		}
		if in.MAC == "" {
			return text("saved devices:\n%s", pretty(saved)), nil, nil
		}
		mac := in.MAC
		var devices []struct {
			Name       string `json:"name"`
			MacAddress string `json:"macAddress"`
		}
		_ = json.Unmarshal(saved, &devices)
		for _, d := range devices {
			if strings.EqualFold(d.Name, in.MAC) {
				mac = d.MacAddress
			}
		}
		params := map[string]any{"macAddress": mac}
		if in.BroadcastIP != "" {
			params["broadcastIP"] = in.BroadcastIP
		}
		if _, err := s.kvm.rpc(ctx, "sendWOLMagicPacket", params, rpcTimeout); err != nil {
			return nil, nil, err
		}
		return text("magic packet sent to %s", mac), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name:        "kvm_reboot",
		Description: "Reboot the KVM itself (not the target). Video, keyboard and the target's USB devices drop for about a minute, and open console sessions end.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in rebootIn) (*mcp.CallToolResult, any, error) {
		if _, err := s.kvm.rpc(ctx, "reboot", map[string]any{"force": in.Force}, rpcTimeout); err != nil {
			return nil, nil, err
		}
		return text("the KVM is rebooting"), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name: "kvm_virtual_media",
		Description: "Virtual media: the KVM presents an image file to the target as a USB CD-ROM or disk. " +
			"status, list (files in the KVM's storage), mount_url (stream an image from a URL), mount_file, unmount, " +
			"download (copy a URL into the storage), download_status, cancel_download, delete (a stored file; it cannot be undone).",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mediaIn) (*mcp.CallToolResult, any, error) {
		mode := "CDROM"
		switch strings.ToLower(in.Mode) {
		case "", "cdrom", "cd":
		case "disk":
			mode = "Disk"
		default:
			return nil, nil, fmt.Errorf("mode is cdrom or disk, not %q", in.Mode)
		}
		need := func(field, value string) error {
			if value == "" {
				return fmt.Errorf("%s needs %s", in.Action, field)
			}
			return nil
		}
		var calls []rpcCall
		switch in.Action {
		case "", "status":
			calls = []rpcCall{{Method: "getVirtualMediaState"}, {Method: "getStorageDownloadState"}}
		case "list":
			calls = []rpcCall{{Method: "listStorageFiles"}, {Method: "getStorageSpace"}}
		case "mount_url":
			if err := need("url", in.URL); err != nil {
				return nil, nil, err
			}
			calls = []rpcCall{{Method: "mountWithHTTP", Params: map[string]any{"url": in.URL, "mode": mode}}, {Method: "getVirtualMediaState"}}
		case "mount_file":
			if err := need("filename", in.Filename); err != nil {
				return nil, nil, err
			}
			calls = []rpcCall{{Method: "mountWithStorage", Params: map[string]any{"filename": in.Filename, "mode": mode}}, {Method: "getVirtualMediaState"}}
		case "unmount":
			calls = []rpcCall{{Method: "unmountImage"}, {Method: "getVirtualMediaState"}}
		case "download":
			if err := need("url", in.URL); err != nil {
				return nil, nil, err
			}
			if err := need("filename", in.Filename); err != nil {
				return nil, nil, err
			}
			calls = []rpcCall{{Method: "startStorageDownload", Params: map[string]any{"url": in.URL, "filename": in.Filename}}}
		case "download_status":
			calls = []rpcCall{{Method: "getStorageDownloadState"}}
		case "cancel_download":
			calls = []rpcCall{{Method: "cancelStorageDownload"}}
		case "delete":
			if err := need("filename", in.Filename); err != nil {
				return nil, nil, err
			}
			calls = []rpcCall{{Method: "deleteStorageFile", Params: map[string]any{"filename": in.Filename}}}
		default:
			return nil, nil, fmt.Errorf("unknown action %q", in.Action)
		}
		out, err := s.report(ctx, calls)
		if err != nil {
			return nil, nil, err
		}
		if calls[0].Err != nil {
			return nil, nil, fmt.Errorf("%s: %w", calls[0].Method, calls[0].Err)
		}
		return text("%s", out), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name: "kvm_usb_devices",
		Description: "List or switch the USB functions the KVM presents to the target: video (camera), microphone, audio, network, massStorage, serialConsole. " +
			"Any change reconnects USB on the target; a set that exceeds the KVM's USB endpoints is refused. Keyboard and mouse are not switched here.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in usbIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "", "list":
			out, err := s.report(ctx, []rpcCall{{Method: "getUsbDevices"}, {Method: "getUSBState"}})
			if err != nil {
				return nil, nil, err
			}
			return text("%s", out), nil, nil
		case "enable", "disable":
			if !usbDevicesAllowed[in.Device] {
				return nil, nil, fmt.Errorf("device must be video, microphone, audio, network, massStorage or serialConsole")
			}
			if _, err := s.kvm.rpc(ctx, "setUsbDeviceState", map[string]any{"device": in.Device, "enabled": in.Action == "enable"}, rpcTimeout); err != nil {
				return nil, nil, err
			}
			out, err := s.report(ctx, []rpcCall{{Method: "getUsbDevices"}})
			if err != nil {
				return nil, nil, err
			}
			return text("%s %sd; the target sees USB reconnect\n%s", in.Device, in.Action, out), nil, nil
		default:
			return nil, nil, fmt.Errorf("unknown action %q", in.Action)
		}
	})

	mcp.AddTool(m, &mcp.Tool{
		Name: "kvm_keyboard",
		Description: "Type on the target through the KVM's USB keyboard, as a person at the keyboard would: works in the BIOS, boot menus and login screens. " +
			"text is typed as on a US keyboard layout; keys are combinations such as ctrl+alt+delete, enter, f12 or win+r; macro runs a keyboard macro saved on the KVM. " +
			"For commands in a running OS, console_run is more reliable.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in keyboardIn) (*mcp.CallToolResult, any, error) {
		if in.Macro != "" {
			if _, err := s.kvm.rpc(ctx, "runKeyboardMacro", map[string]any{"macro": in.Macro}, 5*time.Minute); err != nil {
				return nil, nil, err
			}
			return text("macro %q ran", in.Macro), nil, nil
		}
		delay := in.DelayMS
		if delay <= 0 {
			delay = 20
		}
		delay = min(delay, 5000)
		steps, err := textSteps(in.Text, delay)
		if err != nil {
			return nil, nil, err
		}
		for _, k := range in.Keys {
			combo, err := comboSteps(k, max(delay, 50))
			if err != nil {
				return nil, nil, err
			}
			steps = append(steps, combo...)
		}
		if len(steps) == 0 {
			return nil, nil, errors.New("give text, keys or macro")
		}
		if len(steps) > 20000 {
			return nil, nil, errors.New("too much at once: type at most about 10,000 characters a call")
		}
		list := make([]map[string]any, len(steps))
		total := 0
		for i, st := range steps {
			list[i] = st.toJSON()
			total += st.Delay
		}
		timeout := time.Duration(total)*time.Millisecond + 30*time.Second
		if _, err := s.kvm.rpc(ctx, "executeKeyboardMacro", map[string]any{"steps": list}, timeout); err != nil {
			return nil, nil, err
		}
		note := ""
		if in.Text != "" {
			if layout, err := s.kvm.rpc(ctx, "getKeyboardLayout", nil, rpcTimeout); err == nil {
				var l string
				if json.Unmarshal(layout, &l) == nil && l != "" && !strings.EqualFold(strings.ReplaceAll(l, "_", "-"), "en-US") {
					note = fmt.Sprintf("; the KVM's keyboard layout is %s, and text was typed as US keys", l)
				}
			}
		}
		return text("typed %d key events%s", len(steps)/2, note), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name:        "kvm_clipboard",
		Description: "Read or set the target computer's clipboard through IP-KVM OS Services.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in clipboardIn) (*mcp.CallToolResult, any, error) {
		if in.Text == nil {
			r, err := s.kvm.rpc(ctx, "guestClipboardGet", nil, rpcTimeout)
			if err != nil {
				return nil, nil, err
			}
			return text("%s", pretty(r)), nil, nil
		}
		r, err := s.kvm.rpc(ctx, "guestClipboardSet", map[string]any{"text": *in.Text}, rpcTimeout)
		if err != nil {
			return nil, nil, err
		}
		return text("%s", pretty(r)), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name:        "kvm_host_info",
		Description: "The target computer's system information reported by IP-KVM OS Services: summary (OS, CPU, memory, disks, network, metrics), processes or usb.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in hostInfoIn) (*mcp.CallToolResult, any, error) {
		method := map[string]string{"": "getHostInfo", "summary": "getHostInfo", "processes": "getHostProcesses", "usb": "getHostUsbDevices"}[in.Section]
		if method == "" {
			return nil, nil, fmt.Errorf("section is summary, processes or usb")
		}
		r, err := s.kvm.rpc(ctx, method, nil, rpcTimeout)
		if err != nil {
			return nil, nil, err
		}
		out, cut := clip(pretty(r), maxOutput)
		if cut {
			out = "[output shortened]\n" + out
		}
		return text("%s", out), nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name: "kvm_rpc",
		Description: "Call one method of the KVM's device API directly, for what the other tools do not cover. " +
			"Reads: getUsbConfig, getNetworkSettings, getJigglerState, getJigglerConfig, getEDID, getAudioConfig, getVirtualVideoState, getUsbNetworkState, getSerialSettings, getKeyboardMacros, getOsServicesSettings, getTailscaleStatus, getMqttStatus. " +
			"Changes: setJigglerState {enabled}, setOsServicesSettings {settings}, wakeHost, keyboardReport, absMouseReport {x, y, buttons} (0-32767). " +
			"The KVM refuses methods that change its network, access, firmware or cloud registration.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in rpcIn) (*mcp.CallToolResult, any, error) {
		if in.Method == "" {
			return nil, nil, errors.New("method is empty")
		}
		r, err := s.kvm.rpc(ctx, in.Method, in.Params, 2*time.Minute)
		if err != nil {
			return nil, nil, err
		}
		out, cut := clip(pretty(r), maxOutput)
		if cut {
			out = "[output shortened]\n" + out
		}
		return text("%s", out), nil, nil
	})
}
