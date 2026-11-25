package hostconsole

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Screenshots and the mouse, for computer use: kvm_screenshot takes a still
// of the HDMI input, kvm_mouse moves and clicks the KVM's absolute USB mouse.
// kvm_mouse takes screen pixels, or pixels of the last screenshot of this
// MCP session (coords "screenshot"), which may be a scaled region.

// absMax is the largest absolute mouse coordinate (the HID descriptor's
// logical maximum).
const absMax = 32767

// defaultShotWidth: a whole-screen screenshot is scaled down to this width
// unless the call asks otherwise, which keeps images small and below the
// size at which models scale them down themselves.
const defaultShotWidth = 1280

// shotGeometry maps a screenshot's pixels to the screen's.
type shotGeometry struct {
	screenW, screenH int
	x, y, w, h       int
	imageW, imageH   int
}

type screenshotIn struct {
	X         int    `json:"x,omitempty" jsonschema:"left edge of the region, in screen pixels"`
	Y         int    `json:"y,omitempty" jsonschema:"top edge of the region, in screen pixels"`
	Width     int    `json:"width,omitempty" jsonschema:"region width in screen pixels; 0 reaches the right edge"`
	Height    int    `json:"height,omitempty" jsonschema:"region height in screen pixels; 0 reaches the bottom edge"`
	MaxWidth  int    `json:"max_width,omitempty" jsonschema:"scale the image down to at most this width; default 1280; -1 keeps full resolution"`
	MaxHeight int    `json:"max_height,omitempty" jsonschema:"scale the image down to at most this height; default no limit"`
	Format    string `json:"format,omitempty" jsonschema:"jpeg (default) or png (sharper text, larger)"`
	Quality   int    `json:"quality,omitempty" jsonschema:"JPEG quality 1-100, default 80"`
}

type mouseIn struct {
	Action    string   `json:"action" jsonschema:"move, click, double_click, down (press and hold), up (release), drag or scroll"`
	X         *int     `json:"x,omitempty" jsonschema:"pointer x; leave out to stay where this session last put the pointer"`
	Y         *int     `json:"y,omitempty" jsonschema:"pointer y"`
	ToX       *int     `json:"to_x,omitempty" jsonschema:"drag: where to release, x"`
	ToY       *int     `json:"to_y,omitempty" jsonschema:"drag: where to release, y"`
	Button    string   `json:"button,omitempty" jsonschema:"left (default), right or middle"`
	Scroll    int      `json:"scroll,omitempty" jsonschema:"scroll: wheel steps, positive scrolls up (content moves down), negative down"`
	ScrollX   int      `json:"scroll_x,omitempty" jsonschema:"scroll: horizontal wheel steps, positive right"`
	Modifiers []string `json:"modifiers,omitempty" jsonschema:"keys held during the action: ctrl, shift, alt, win/cmd"`
	Coords    string   `json:"coords,omitempty" jsonschema:"screen (default): x and y are screen pixels; screenshot: pixels of the last kvm_screenshot image of this session"`
}

var mouseButtons = map[string]uint8{"": 1, "left": 1, "right": 2, "middle": 4}

func (s *server) registerScreen(m *mcp.Server) {
	mcp.AddTool(m, &mcp.Tool{
		Name: "kvm_screenshot",
		Description: "Take a screenshot of the target computer's screen as the KVM sees it on HDMI: works in the BIOS, boot menus and login screens. " +
			"Give x, y, width and height (screen pixels) for a region. The whole screen is scaled to 1280 wide by default; the answer says how image pixels map to screen pixels. " +
			"Use with kvm_mouse (coords screenshot clicks on image pixels) and kvm_keyboard. Not available while the KVM shows USB Video from OS Services.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in screenshotIn) (*mcp.CallToolResult, any, error) {
		maxWidth := in.MaxWidth
		switch {
		case maxWidth == 0:
			maxWidth = defaultShotWidth
		case maxWidth < 0:
			maxWidth = 0
		}
		params := map[string]any{
			"x": in.X, "y": in.Y, "width": in.Width, "height": in.Height,
			"maxWidth": maxWidth, "maxHeight": max(in.MaxHeight, 0),
			"format": strings.ToLower(in.Format), "quality": in.Quality,
		}
		raw, err := s.kvm.rpc(ctx, "getScreenshot", params, time.Minute)
		if err != nil {
			return nil, nil, err
		}
		var shot struct {
			Image        string `json:"image"`
			MimeType     string `json:"mimeType"`
			ScreenWidth  int    `json:"screenWidth"`
			ScreenHeight int    `json:"screenHeight"`
			X            int    `json:"x"`
			Y            int    `json:"y"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			ImageWidth   int    `json:"imageWidth"`
			ImageHeight  int    `json:"imageHeight"`
		}
		if err := json.Unmarshal(raw, &shot); err != nil {
			return nil, nil, fmt.Errorf("screenshot answer: %w", err)
		}
		data, err := base64.StdEncoding.DecodeString(shot.Image)
		if err != nil || shot.ImageWidth <= 0 || shot.Width <= 0 {
			return nil, nil, errors.New("the KVM sent no image")
		}
		s.shotMu.Lock()
		s.shot = &shotGeometry{
			screenW: shot.ScreenWidth, screenH: shot.ScreenHeight,
			x: shot.X, y: shot.Y, w: shot.Width, h: shot.Height,
			imageW: shot.ImageWidth, imageH: shot.ImageHeight,
		}
		s.shotMu.Unlock()
		scale := float64(shot.Width) / float64(shot.ImageWidth)
		note := fmt.Sprintf("Screen %dx%d. The %dx%d image shows x %d-%d, y %d-%d",
			shot.ScreenWidth, shot.ScreenHeight, shot.ImageWidth, shot.ImageHeight,
			shot.X, shot.X+shot.Width-1, shot.Y, shot.Y+shot.Height-1)
		if scale != 1 {
			note += fmt.Sprintf(", scaled down %.3gx: screen x = %d + image x * %.4g", scale, shot.X, scale)
		}
		note += fmt.Sprintf(". kvm_mouse with coords \"screenshot\" takes this image's pixels. %d bytes %s.", len(data), shot.MimeType)
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.ImageContent{Data: data, MIMEType: shot.MimeType},
			&mcp.TextContent{Text: note},
		}}, nil, nil
	})

	mcp.AddTool(m, &mcp.Tool{
		Name: "kvm_mouse",
		Description: "Move and click the target's mouse through the KVM's absolute USB mouse, as a person would: works in the BIOS and any OS. " +
			"Actions: move, click, double_click, down, up, drag (x,y to to_x,to_y), scroll (scroll / scroll_x wheel steps at x,y). " +
			"x and y are screen pixels, or with coords \"screenshot\" pixels of the last kvm_screenshot image (scaling and region applied). " +
			"Take a screenshot afterwards to check the result.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mouseIn) (*mcp.CallToolResult, any, error) {
		return s.mouse(ctx, in)
	})
}

// screenSize is the size of the picture the KVM shows.
func (s *server) screenSize(ctx context.Context) (int, int, error) {
	raw, err := s.kvm.rpc(ctx, "getVideoState", nil, rpcTimeout)
	if err != nil {
		return 0, 0, err
	}
	var v struct {
		Ready  bool   `json:"ready"`
		Error  string `json:"error"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, 0, err
	}
	if v.Width <= 1 || v.Height <= 1 {
		reason := v.Error
		if reason == "" {
			reason = "not ready"
		}
		return 0, 0, fmt.Errorf("the screen size is unknown: no video (%s)", reason)
	}
	return v.Width, v.Height, nil
}

// toAbs maps a point to absolute mouse coordinates.
func (s *server) toAbs(x, y int, coords string, screenW, screenH int) (int, int, error) {
	switch coords {
	case "", "screen":
	case "screenshot", "image":
		s.shotMu.Lock()
		g := s.shot
		s.shotMu.Unlock()
		if g == nil {
			return 0, 0, errors.New("coords \"screenshot\" needs a kvm_screenshot first in this session")
		}
		if x < 0 || y < 0 || x >= g.imageW || y >= g.imageH {
			return 0, 0, fmt.Errorf("%d,%d is outside the %dx%d screenshot", x, y, g.imageW, g.imageH)
		}
		// The centre of the image pixel, in screen pixels.
		x = g.x + (2*x+1)*g.w/(2*g.imageW)
		y = g.y + (2*y+1)*g.h/(2*g.imageH)
		if g.screenW > 0 && g.screenH > 0 && (g.screenW != screenW || g.screenH != screenH) {
			// The resolution changed since the screenshot: keep the proportion.
			x = x * screenW / g.screenW
			y = y * screenH / g.screenH
		}
	default:
		return 0, 0, fmt.Errorf("coords is screen or screenshot, not %q", coords)
	}
	if x < 0 || y < 0 || x >= screenW || y >= screenH {
		return 0, 0, fmt.Errorf("%d,%d is outside the %dx%d screen", x, y, screenW, screenH)
	}
	return x * absMax / (screenW - 1), y * absMax / (screenH - 1), nil
}

func (s *server) mouse(ctx context.Context, in mouseIn) (*mcp.CallToolResult, any, error) {
	action := strings.ToLower(in.Action)
	button, ok := mouseButtons[strings.ToLower(in.Button)]
	if !ok {
		return nil, nil, fmt.Errorf("button is left, right or middle, not %q", in.Button)
	}
	var modifier byte
	for _, name := range in.Modifiers {
		bit, ok := hidModifiers[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return nil, nil, fmt.Errorf("unknown modifier %q (ctrl, shift, alt, win)", name)
		}
		modifier |= bit
	}
	if (in.X == nil) != (in.Y == nil) {
		return nil, nil, errors.New("give both x and y, or neither")
	}

	raw, err := s.kvm.rpc(ctx, "getUsbDevices", nil, rpcTimeout)
	if err != nil {
		return nil, nil, err
	}
	var devices struct {
		AbsoluteMouse bool `json:"absolute_mouse"`
	}
	if json.Unmarshal(raw, &devices) == nil && !devices.AbsoluteMouse {
		return nil, nil, errors.New("the KVM's absolute mouse is off (USB devices); kvm_usb_devices cannot switch it, the KVM settings can")
	}
	screenW, screenH, err := s.screenSize(ctx)
	if err != nil {
		return nil, nil, err
	}

	s.mouseMu.Lock()
	defer s.mouseMu.Unlock()
	ax, ay := s.mouseX, s.mouseY
	if in.X != nil {
		if ax, ay, err = s.toAbs(*in.X, *in.Y, in.Coords, screenW, screenH); err != nil {
			return nil, nil, err
		}
	} else if !s.mouseKnown && action != "up" {
		return nil, nil, errors.New("give x and y: this session has not placed the pointer yet")
	}

	report := func(x, y int, buttons uint8) error {
		_, err := s.kvm.rpc(ctx, "absMouseReport", map[string]any{"x": x, "y": y, "buttons": buttons}, rpcTimeout)
		if err == nil {
			s.mouseX, s.mouseY, s.mouseKnown, s.buttons = x, y, true, buttons
		}
		return err
	}
	pause := func(ms int) {
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(ms) * time.Millisecond):
		}
	}
	if modifier != 0 {
		if _, err := s.kvm.rpc(ctx, "keyboardReport", map[string]any{"modifier": int(modifier), "keys": []int{}}, rpcTimeout); err != nil {
			return nil, nil, err
		}
		defer func() {
			_, _ = s.kvm.rpc(context.WithoutCancel(ctx), "keyboardReport", map[string]any{"modifier": 0, "keys": []int{}}, rpcTimeout)
		}()
		pause(30)
	}
	click := func() error {
		if err := report(ax, ay, s.buttons|button); err != nil {
			return err
		}
		pause(60)
		return report(ax, ay, s.buttons&^button)
	}

	where := func(x, y int) string {
		return fmt.Sprintf("%d,%d", (x*(screenW-1)+absMax/2)/absMax, (y*(screenH-1)+absMax/2)/absMax)
	}
	switch action {
	case "move":
		if in.X == nil {
			return nil, nil, errors.New("move needs x and y")
		}
		err = report(ax, ay, s.buttons)
	case "click", "double_click":
		if err = report(ax, ay, s.buttons); err == nil {
			pause(40)
			err = click()
			if err == nil && action == "double_click" {
				pause(80)
				err = click()
			}
		}
	case "down":
		err = report(ax, ay, s.buttons|button)
	case "up":
		err = report(ax, ay, s.buttons&^button)
	case "drag":
		if in.ToX == nil || in.ToY == nil {
			return nil, nil, errors.New("drag needs to_x and to_y")
		}
		var tx, ty int
		if tx, ty, err = s.toAbs(*in.ToX, *in.ToY, in.Coords, screenW, screenH); err != nil {
			return nil, nil, err
		}
		if err = report(ax, ay, s.buttons); err != nil {
			break
		}
		pause(40)
		if err = report(ax, ay, s.buttons|button); err != nil {
			break
		}
		const steps = 12
		for i := 1; i <= steps && err == nil; i++ {
			pause(25)
			err = report(ax+(tx-ax)*i/steps, ay+(ty-ay)*i/steps, s.buttons)
		}
		if err == nil {
			pause(60)
			err = report(tx, ty, s.buttons&^button)
		}
		if err == nil {
			return text("dragged from %s to %s (screen %dx%d)", where(ax, ay), where(tx, ty), screenW, screenH), nil, nil
		}
	case "scroll":
		if in.Scroll == 0 && in.ScrollX == 0 {
			return nil, nil, errors.New("scroll needs scroll or scroll_x")
		}
		if max(in.Scroll, -in.Scroll, in.ScrollX, -in.ScrollX) > 100 {
			return nil, nil, errors.New("scroll at most 100 steps a call")
		}
		if in.X != nil {
			if err = report(ax, ay, s.buttons); err != nil {
				break
			}
			pause(30)
		}
		dy, dx := in.Scroll, in.ScrollX
		for (dy != 0 || dx != 0) && err == nil {
			// One step per report reads as one wheel notch everywhere.
			sy, sx := sign(dy), sign(dx)
			_, err = s.kvm.rpc(ctx, "wheelReport", map[string]any{"wheelY": sy, "wheelX": sx}, rpcTimeout)
			dy -= sy
			dx -= sx
			pause(15)
		}
	default:
		return nil, nil, fmt.Errorf("action is move, click, double_click, down, up, drag or scroll, not %q", in.Action)
	}
	if err != nil {
		return nil, nil, err
	}
	return text("%s at %s (screen %dx%d)", action, where(ax, ay), screenW, screenH), nil, nil
}

func sign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}
