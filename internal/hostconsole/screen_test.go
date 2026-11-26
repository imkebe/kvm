package hostconsole

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestToAbs(t *testing.T) {
	s := &server{}
	if x, y, err := s.toAbs(0, 0, "", 1920, 1080); err != nil || x != 0 || y != 0 {
		t.Fatalf("origin %d %d %v", x, y, err)
	}
	if x, y, err := s.toAbs(1919, 1079, "screen", 1920, 1080); err != nil || x != absMax || y != absMax {
		t.Fatalf("corner %d %d %v", x, y, err)
	}
	if _, _, err := s.toAbs(1920, 0, "", 1920, 1080); err == nil {
		t.Fatal("outside the screen should be refused")
	}
	if _, _, err := s.toAbs(0, 0, "screenshot", 1920, 1080); err == nil {
		t.Fatal("screenshot coords need a screenshot")
	}
	// A 1280x720 image of the whole 1920x1080 screen: image 640,360 is screen 960,540.
	s.shot = &shotGeometry{screenW: 1920, screenH: 1080, w: 1920, h: 1080, imageW: 1280, imageH: 720}
	x, y, err := s.toAbs(640, 360, "screenshot", 1920, 1080)
	if err != nil || x != 960*absMax/1919 || y != 540*absMax/1079 {
		t.Fatalf("scaled %d %d %v", x, y, err)
	}
	// An unscaled region at 100,200: image 10,20 is screen 110,220.
	s.shot = &shotGeometry{screenW: 1920, screenH: 1080, x: 100, y: 200, w: 300, h: 100, imageW: 300, imageH: 100}
	x, y, err = s.toAbs(10, 20, "screenshot", 1920, 1080)
	if err != nil || x != 110*absMax/1919 || y != 220*absMax/1079 {
		t.Fatalf("region %d %d %v", x, y, err)
	}
	if _, _, err := s.toAbs(300, 0, "screenshot", 1920, 1080); err == nil {
		t.Fatal("outside the screenshot should be refused")
	}
}

// tinyPNG is a 1x1 PNG.
var tinyPNG = func() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))
	return b.Bytes()
}()

func TestScreenTools(t *testing.T) {
	call, f := deviceClient(t, &fakeDeviceAPI{})

	out, isErr := call("kvm_screenshot", nil)
	if isErr || !strings.Contains(out, "Screen 1920x1080") || !strings.Contains(out, "scaled down 1.5x") {
		t.Fatalf("screenshot: %q", out)
	}
	c := f.take()
	if len(c) != 1 || c[0].Method != "getScreenshot" || c[0].Params["maxWidth"] != float64(1280) {
		t.Fatalf("screenshot sent %+v", c)
	}

	// Click the middle of the screenshot: screen 960,540.
	out, isErr = call("kvm_mouse", map[string]any{"action": "click", "x": 640, "y": 360, "coords": "screenshot", "modifiers": []string{"shift"}})
	if isErr || !strings.Contains(out, "click at 960,540") {
		t.Fatalf("click: %q", out)
	}
	var sent []string
	for _, r := range f.take() {
		switch r.Method {
		case "absMouseReport":
			sent = append(sent, fmt.Sprintf("abs %v,%v %v", r.Params["x"], r.Params["y"], r.Params["buttons"]))
		case "keyboardReport":
			sent = append(sent, fmt.Sprintf("kbd %v", r.Params["modifier"]))
		}
	}
	x, y := 960*absMax/1919, 540*absMax/1079
	want := fmt.Sprintf("kbd 2|abs %d,%d 0|abs %d,%d 1|abs %d,%d 0|kbd 0", x, y, x, y, x, y)
	if strings.Join(sent, "|") != want {
		t.Fatalf("click sent\n got %s\nwant %s", strings.Join(sent, "|"), want)
	}

	if out, isErr := call("kvm_mouse", map[string]any{"action": "scroll", "scroll": -3}); isErr {
		t.Fatalf("scroll: %q", out)
	}
	wheels := 0
	for _, r := range f.take() {
		if r.Method == "wheelReport" && r.Params["wheelY"] == float64(-1) {
			wheels++
		}
	}
	if wheels != 3 {
		t.Fatalf("scroll sent %d wheel steps", wheels)
	}
	if _, isErr := call("kvm_mouse", map[string]any{"action": "drag", "x": 1, "y": 1}); !isErr {
		t.Fatal("drag without to_x accepted")
	}
	if _, isErr := call("kvm_mouse", map[string]any{"action": "click", "x": 5000, "y": 1}); !isErr {
		t.Fatal("click outside the screen accepted")
	}
}
