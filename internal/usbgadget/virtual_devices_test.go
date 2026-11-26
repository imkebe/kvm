package usbgadget

import (
	"github.com/sourcegraph/tf-dag/dag"
	"os"
	"path/filepath"
	"testing"
)

func TestVirtualDevicesTransactionHasNoDuplicateChanges(t *testing.T) {
	u := &UsbGadget{configMap: deepCopyConfigMap(defaultGadgetConfig), enabledDevices: Devices{Video: true, Network: true, Keyboard: true, Audio: true}, kvmGadgetPath: "/nonexistent/jetkvm", configC1Path: "/nonexistent/jetkvm/configs/c.1", log: defaultLogger}
	if err := u.newUsbGadgetTransaction(false); err != nil {
		t.Fatal(err)
	}
	u.tx.WriteGadgetConfig()
	if u.tx.reorderSymlinkChanges != nil {
		u.tx.addFileChange("gadget-finalize", *u.tx.reorderSymlinkChanges)
	}
	r := ChangeSetResolver{changeset: u.tx.c, g: &dag.AcyclicGraph{}, l: defaultLogger}
	if _, err := r.GetChanges(); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkFunctionSelection(t *testing.T) {
	u := &UsbGadget{enabledDevices: Devices{Network: true}}
	for _, function := range []string{"", "ncm", "ecm", "rndis"} {
		u.customConfig.NetworkFunction = function
		want := function
		if want == "" {
			want = "ncm"
		}
		for _, candidate := range []string{"ncm", "ecm", "rndis"} {
			if got := u.isGadgetConfigItemEnabled("network_" + candidate); got != (candidate == want) {
				t.Fatalf("function %q enabled %s = %v", function, candidate, got)
			}
		}
	}
}

// UVC configfs renders bcdUVC as decimal and rejects writes once class links
// reference the header, even after the camera is disabled. Re-enabling must
// reuse this descriptor rather than issue a formatting-only update.
func TestLinkedUVCHeaderDoesNotNeedVersionRewrite(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bcdUVC")
	if err := os.WriteFile(file, []byte("272\n"), 0444); err != nil {
		t.Fatal(err)
	}
	change := FileChange{RequestedFileChange: RequestedFileChange{
		Path: file, ExpectedState: FileStateFileContentMatch,
		ExpectedContent: []byte(videoControlHeaderConfig.attrs["bcdUVC"]),
	}}
	if action := change.Action(); action != FileChangeResolvedActionDoNothing {
		t.Fatalf("redundant linked header write: %v", FileChangeResolvedActionString[action])
	}
}

// Removing NCM can leave the remaining links correctly ordered, although the
// implicit UDC unbind has already unregistered UVC's embedded video_device.
// A live layout change must recreate UVC even in this case.
func TestUVCFunctionRecreatedDespiteMatchingLinkOrder(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "configs")
	function := filepath.Join(root, "uvc.usb0")
	if err := os.Mkdir(configPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(function, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(configPath, "uvc.usb0")
	if err := os.Symlink(function, link); err != nil {
		t.Fatal(err)
	}
	requested := RequestedFileChange{Path: configPath, ExpectedState: FileStateSymlinkInOrderConfigFS, ParamSymlinks: []symlink{{Path: link, Target: function}}}
	adopted := FileChange{RequestedFileChange: requested}
	if adopted.Action() != FileChangeResolvedActionDoNothing {
		t.Fatal("unchanged startup layout should remain attached")
	}
	requested.ForceRecreate = true
	rebound := FileChange{RequestedFileChange: requested}
	if rebound.Action() != FileChangeResolvedActionReorderSymlinks {
		t.Fatal("live layout change reused old UVC function")
	}
	if err := (&ChangeSet{}).applyChange(&rebound); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != function {
		t.Fatalf("UVC link not restored: %q %v", target, err)
	}
}

func TestCameraCodecProfiles(t *testing.T) {
	for _, codec := range []string{"mjpeg", "h264"} {
		u := &UsbGadget{configMap: deepCopyConfigMap(defaultGadgetConfig), customConfig: Config{VideoCodec: codec}, enabledDevices: Devices{Video: true, Network: true, Keyboard: true, Audio: true}, kvmGadgetPath: "/nonexistent/jetkvm", configC1Path: "/nonexistent/jetkvm/configs/c.1", log: defaultLogger}
		if u.isGadgetConfigItemEnabled("video_base") != (codec == "mjpeg") || u.isGadgetConfigItemEnabled("video_h264_base") != (codec == "h264") {
			t.Fatal("both camera profiles enabled")
		}
		if err := u.newUsbGadgetTransaction(false); err != nil {
			t.Fatal(err)
		}
		u.tx.WriteGadgetConfig()
		if u.tx.reorderSymlinkChanges != nil {
			u.tx.addFileChange("gadget-finalize", *u.tx.reorderSymlinkChanges)
		}
		resolver := ChangeSetResolver{changeset: u.tx.c, g: &dag.AcyclicGraph{}, l: defaultLogger}
		if _, err := resolver.GetChanges(); err != nil {
			t.Fatal(codec, err)
		}
	}
	h264 := defaultGadgetConfig["video_h264_format"]
	if string(h264.attrs["guidFormat"][:4]) != "H264" || h264.path[3] != "framebased" {
		t.Fatal("invalid H264 descriptor")
	}
	if _, present := defaultGadgetConfig["video_h264_480"].attrs["dwMaxVideoFrameBufferSize"]; present {
		t.Fatal("MJPEG attribute on framebased descriptor")
	}
}
