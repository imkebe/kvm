package kvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jetkvm/kvm/internal/hidrpc"
)

// The device API: JSON-RPC over plain HTTP for programs such as the
// hostconsole MCP server. The browser's RPC runs over the WebRTC data
// channel, and a program that opened its own WebRTC session would take the
// browser's place; this endpoint needs no session.
//
// POST /api/rpc with Content-Type application/json and one JSON-RPC 2.0
// request, or an array of them (a batch, answered in order). It is off until
// the setting device_api_enabled is on, takes an API token with the device
// scope or the browser's sign-in cookie (api_tokens.go), and runs only the methods in
// deviceAPIMethods: none that changes the network, the access to the KVM,
// its firmware or its cloud registration, and none that needs the browser's
// data channels.
const (
	deviceAPIPath     = "/api/rpc"
	deviceAPIBodyMax  = 1 << 20
	deviceAPIBatchMax = 4096
	deviceAPIOff      = "The device API is off in the advanced settings"
)

// deviceAPIMethods are the rpcHandlers methods the device API runs.
var deviceAPIMethods = map[string]bool{
	// Device and state.
	"ping": true, "getDeviceID": true, "getDeviceCapabilities": true, "getLocalVersion": true,
	"getNetworkState": true, "getNetworkSettings": true, "getNetworkNeighbors": true,
	"getNetworkScanDefaults": true, "scanNetwork": true, "checkIpv4Address": true, "getCloudState": true, "getVideoState": true,
	"getUSBState": true, "getUsbEmulationState": true, "getUsbConfig": true, "getUsbDevices": true,
	"getUsbEndpointUsage": true, "getUsbFunctionNames": true, "getDeviceStats": true,
	"getAutoUpdateState": true, "isUpdatePending": true, "getDevModeState": true,
	"getTailscaleStatus": true, "getNetbirdState": true, "getMqttStatus": true, "getDefaultLogLevel": true,
	"getEDID": true, "getVideoCodecPreference": true, "getStreamQualityFactor": true,
	"getBacklightSettings": true, "getDisplayRotation": true, "getAudioConfig": true,
	"getVirtualVideoState": true, "getVirtualAudioState": true, "getUsbNetworkState": true,
	"getSerialSettings": true, "getKeyboardLayout": true, "getKeyboardMacros": true,
	"getKeyboardLedState": true, "getKeyDownState": true, "getJigglerState": true, "getJigglerConfig": true,
	// Power of the target and of the KVM.
	"getATXState": true, "setATXPowerAction": true, "getDCPowerState": true, "setDCPowerState": true,
	"getActiveExtension": true, "reboot": true,
	"getWakeOnLanDevices": true, "sendWOLMagicPacket": true, "wakeHost": true,
	// Virtual media.
	"getVirtualMediaState": true, "getMassStorageMode": true, "getStorageSpace": true,
	"listStorageFiles": true, "mountWithHTTP": true, "mountWithStorage": true, "unmountImage": true,
	"checkMountUrl": true, "mountBuiltinInstaller": true, "startStorageDownload": true,
	"getStorageDownloadState": true, "cancelStorageDownload": true, "deleteStorageFile": true,
	// USB functions (rpcSetUsbDevices checks the endpoint budget).
	"setUsbDeviceState": true,
	// Keyboard and mouse.
	"keyboardReport": true, "keypressReport": true, "absMouseReport": true, "relMouseReport": true,
	"runKeyboardMacro": true, "setJigglerState": true,
	// OS Services.
	"getOsServicesSettings": true,
	"guestClipboardGet":     true, "guestClipboardSet": true,
	"getHostInfo": true, "getHostProcesses": true, "getHostUsbDevices": true,
}

// deviceAPIHandlers are methods of the device API only.
var deviceAPIHandlers = map[string]RPCHandler{
	"executeKeyboardMacro":  {Func: apiExecuteKeyboardMacro, Params: []string{"steps"}},
	"setOsServicesSettings": {Func: apiSetOsServicesSettings, Params: []string{"settings"}},
	"getScreenshot": {Func: apiGetScreenshot, OptionalParams: []string{
		"x", "y", "width", "height", "maxWidth", "maxHeight", "format", "quality"}},
	"wheelReport": {Func: rpcWheelReport, Params: []string{"wheelY"}, OptionalParams: []string{"wheelX"}},
}

// deviceAPIOsServicesSettings are the OS Services settings a device API client
// may change. The others (shell and startup commands typed into the target,
// the update repository, the switches) give shell access or more; only the
// owner changes them, in the settings.
var deviceAPIOsServicesSettings = map[string]bool{
	"video_enabled": true, "clipboard_enabled": true, "capture_display": true,
	"resolution": true, "display_mode": true, "adaptation": true,
}

// apiSetOsServicesSettings is setOsServicesSettings limited to
// deviceAPIOsServicesSettings; other members pass only with their current value.
func apiSetOsServicesSettings(settings map[string]any) (OSServicesConfig, error) {
	current, err := rpcGetOsServicesSettings()
	if err != nil {
		return current, err
	}
	for name, value := range settings {
		if deviceAPIOsServicesSettings[name] {
			continue
		}
		if merged, err := mergeOSServicesConfig(current, map[string]any{name: value}); err != nil || merged != current {
			return OSServicesConfig{}, fmt.Errorf("the device API cannot change %s; only the settings page can", name)
		}
	}
	return rpcSetOsServicesSettings(settings)
}

func deviceAPIHandler(method string) (RPCHandler, bool) {
	if h, ok := deviceAPIHandlers[method]; ok {
		return h, true
	}
	if deviceAPIMethods[method] {
		h, ok := rpcHandlers[method]
		return h, ok
	}
	return RPCHandler{}, false
}

// deviceAPIQuiet: methods not logged one by one (reads and keystrokes).
func deviceAPIQuiet(method string) bool {
	return strings.HasPrefix(method, "get") || strings.HasPrefix(method, "list") || method == "ping" ||
		strings.HasSuffix(method, "Report") || method == "isUpdatePending" || method == "checkMountUrl"
}

func handleDeviceAPI(c *gin.Context) {
	if !config.DeviceAPIEnabled {
		sendErrorJsonThenAbort(c, http.StatusForbidden, deviceAPIOff)
		return
	}
	// A JSON body makes a browser ask before it sends a request from another
	// site, and the Origin check refuses one that does send it.
	if c.ContentType() != "application/json" {
		sendErrorJsonThenAbort(c, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	if !apiOriginAllowed(c.Request) {
		sendErrorJsonThenAbort(c, http.StatusForbidden, "Requests from other origins are refused")
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, deviceAPIBodyMax+1))
	if err != nil || len(body) > deviceAPIBodyMax {
		sendErrorJsonThenAbort(c, http.StatusRequestEntityTooLarge, "The request body is too large")
		return
	}
	remote := c.ClientIP()
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			c.JSON(http.StatusOK, rpcError(nil, -32700, "Parse error", err.Error()))
			return
		}
		if len(batch) == 0 || len(batch) > deviceAPIBatchMax {
			c.JSON(http.StatusOK, rpcError(nil, -32600, "Invalid Request", fmt.Sprintf("a batch holds 1 to %d requests", deviceAPIBatchMax)))
			return
		}
		responses := make([]JSONRPCResponse, len(batch))
		for i, raw := range batch {
			responses[i] = deviceAPICallLogged(c, raw, remote)
		}
		c.JSON(http.StatusOK, responses)
		return
	}
	c.JSON(http.StatusOK, deviceAPICallLogged(c, trimmed, remote))
}

// apiOriginAllowed: a request without Origin (a program), or from a page of
// this host. Used by the device API and the MCP server.
func apiOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// deviceAPICallLogged runs a call and notes it in the API call log.
func deviceAPICallLogged(c *gin.Context, raw []byte, remote string) JSONRPCResponse {
	var request struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(raw, &request)
	if request.Method == "executeKeyboardMacro" {
		// A client that goes away stops its typing.
		defer context.AfterFunc(c.Request.Context(), cancelKeyboardMacro)()
	}
	started := time.Now()
	response := deviceAPICall(raw, remote)
	entry := APICallEntry{
		At: started.UnixMilli(), Source: apiCallSourceAPI, Name: orDefault(request.Method, "?"),
		DurationMs: sinceMs(started), OK: response.Error == nil, Args: summarizeAPIArgs(request.Params),
		Token: apiTokenName(c.GetString("apiToken")), Remote: remote, ViaMCP: c.GetBool(viaMCPKey),
	}
	if e, ok := response.Error.(map[string]any); ok {
		entry.Error = shorten(fmt.Sprint(orAny(e["data"], e["message"])), apiCallErrorMax)
	}
	recordAPICall(entry)
	return response
}

func orAny(value, fallback any) any {
	if value == nil || value == "" {
		return fallback
	}
	return value
}

func rpcError(id any, code int, message, data string) JSONRPCResponse {
	e := map[string]any{"code": code, "message": message}
	if data != "" {
		e["data"] = data
	}
	return JSONRPCResponse{JSONRPC: "2.0", Error: e, ID: id}
}

func deviceAPICall(raw []byte, remote string) JSONRPCResponse {
	var request JSONRPCRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return rpcError(nil, -32700, "Parse error", err.Error())
	}
	handler, ok := deviceAPIHandler(request.Method)
	if !ok {
		return rpcError(request.ID, -32601, "Method not found",
			"the device API does not run "+request.Method)
	}
	noteDeviceAPICall()
	scoped := jsonRpcLogger.With().Str("method", request.Method).Str("remote", remote).Str("via", "device API").Logger()
	if !deviceAPIQuiet(request.Method) {
		scoped.Info().Str("params", summarizeAPIArgMap(request.Params)).Msg("device API call")
	}
	if request.Params == nil {
		request.Params = map[string]any{}
	}
	// No browser session: handlers that take one run as MQTT commands do.
	result, err := callRPCHandler(scoped, handler, request.Params, nil)
	if err != nil {
		return rpcError(request.ID, -32603, "Internal error", err.Error())
	}
	return JSONRPCResponse{JSONRPC: "2.0", Result: result, ID: request.ID}
}

// apiExecuteKeyboardMacro runs keyboard reports with delays, for typing:
// steps [{"modifier": m, "keys": [k…], "delay": ms}], at most 6 keys a step.
// It returns when the last step has run.
func apiExecuteKeyboardMacro(steps []any) error {
	if len(steps) == 0 || len(steps) > 20000 {
		return errors.New("steps must hold 1 to 20000 steps")
	}
	macro := make([]hidrpc.KeyboardMacroStep, 0, len(steps))
	totalDelay := 0.0
	for i, s := range steps {
		step, ok := s.(map[string]any)
		if !ok {
			return fmt.Errorf("step %d is not an object", i)
		}
		number := func(name string, max float64) (float64, error) {
			v, present := step[name]
			if !present {
				return 0, nil
			}
			f, isNumber := v.(float64)
			if !isNumber || f < 0 || f > max || f != float64(int64(f)) {
				return 0, fmt.Errorf("step %d: %s must be a whole number from 0 to %v", i, name, max)
			}
			return f, nil
		}
		modifier, err := number("modifier", 255)
		if err != nil {
			return err
		}
		delay, err := number("delay", 5000)
		if err != nil {
			return err
		}
		// The call holds the keyboard until the last step: at most 10 minutes.
		if totalDelay += delay; totalDelay > 600000 {
			return errors.New("the delays of the steps add up to more than 10 minutes")
		}
		keys := make([]byte, hidrpc.HidKeyBufferSize)
		if list, present := step["keys"]; present {
			items, isList := list.([]any)
			if !isList || len(items) > hidrpc.HidKeyBufferSize {
				return fmt.Errorf("step %d: keys must be a list of at most %d key codes", i, hidrpc.HidKeyBufferSize)
			}
			for j, item := range items {
				f, isNumber := item.(float64)
				if !isNumber || f < 0 || f > 255 || f != float64(int64(f)) {
					return fmt.Errorf("step %d: key codes are whole numbers from 0 to 255", i)
				}
				keys[j] = byte(f)
			}
		}
		macro = append(macro, hidrpc.KeyboardMacroStep{Modifier: byte(modifier), Keys: keys, Delay: uint16(delay)})
	}
	// End with every key up, whatever the steps left down.
	macro = append(macro, hidrpc.KeyboardMacroStep{Keys: make([]byte, hidrpc.HidKeyBufferSize)})
	return rpcExecuteKeyboardMacro(nil, macro)
}

func rpcGetDeviceAPIEnabled() (bool, error) {
	return config.DeviceAPIEnabled, nil
}

func rpcSetDeviceAPIEnabled(enabled bool) error {
	if config.DeviceAPIEnabled == enabled {
		return nil
	}
	config.DeviceAPIEnabled = enabled
	if err := SaveConfig(); err != nil {
		config.DeviceAPIEnabled = !enabled
		return fmt.Errorf("failed to save config: %w", err)
	}
	return nil
}
