package usbgadget

import (
	"errors"
	"os"
	"path"
	"syscall"
	"testing"
)

// Register dumps taken on an RV1106 (kernel 5.10) with the host attached.
const (
	regdumpConnected    = "DCFG = 0x00080804\nDCTL = 0x80f00a00\nDSTS = 0x00826600\n"
	regdumpDisconnected = "DCFG = 0x00080804\nDCTL = 0x00f00a00\nDSTS = 0x00d35cd9\n"
)

func TestDwc3RunStop(t *testing.T) {
	for _, tc := range []struct {
		name    string
		regdump string
		want    bool
		wantErr bool
	}{
		{"connected", regdumpConnected, true, false},
		{"soft disconnected", regdumpDisconnected, false, false},
		{"no DCTL", "DCFG = 0x00080804\n", false, true},
		{"garbage", "DCTL = zz\n", false, true},
	} {
		got, err := dwc3RunStop([]byte(tc.regdump))
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestVideoControllerResetNeverBindsAnAttachedGadget(t *testing.T) {
	attached := true
	unbound := false
	write := func(p string, data []byte, mode os.FileMode) error {
		switch p {
		case "/gadget/UDC":
			if string(data) != "\n" {
				t.Fatal("zero-byte sysfs writes do not detach the gadget")
			}
			attached = false
		case path.Join(dwc3Path, "unbind"):
			if attached {
				t.Fatal("controller reset would unbind a live UVC video_device")
			}
			unbound = true
		case path.Join(dwc3Path, "bind"):
			if attached || !unbound {
				t.Fatal("controller rebound with an attached or unreset gadget")
			}
		default:
			t.Fatalf("unexpected write %s", p)
		}
		return nil
	}
	if err := resetControllerForVideoWith("test.usb", "/gadget", write); err != nil {
		t.Fatal(err)
	}
	if attached || !unbound {
		t.Fatal("gadget must remain detached until the new configuration is ready")
	}
}

func TestVideoControllerResetStopsWhenDetachFails(t *testing.T) {
	writes := 0
	want := errors.New("busy")
	err := resetControllerForVideoWith("test.usb", "/gadget", func(p string, data []byte, mode os.FileMode) error {
		writes++
		return want
	})
	if !errors.Is(err, want) || writes != 1 {
		t.Fatalf("failed detach must prevent controller reset, err=%v writes=%d", err, writes)
	}
}

func TestVideoControllerResetRecoversAlreadyDetachedGadget(t *testing.T) {
	for _, bound := range []bool{false, true} {
		root := t.TempDir()
		content := "\n"
		if bound {
			content = "test.usb\n"
		}
		if err := os.WriteFile(path.Join(root, "UDC"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		writes := 0
		err := resetControllerForVideoWith("test.usb", root, func(p string, data []byte, mode os.FileMode) error {
			writes++
			if p == path.Join(root, "UDC") {
				return &os.PathError{Op: "write", Path: p, Err: syscall.ENODEV}
			}
			return nil
		})
		if bound {
			if !errors.Is(err, syscall.ENODEV) || writes != 1 {
				t.Fatalf("reset continued with a bound gadget: %v, %d writes", err, writes)
			}
		} else if err != nil || writes != 3 {
			t.Fatalf("failed to recover detached gadget: %v, %d writes", err, writes)
		}
	}
}
