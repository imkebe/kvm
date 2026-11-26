package usbgadget

import (
	"fmt"
	"strings"
)

// Names the target computer sees for single USB functions. The gadget-wide
// identity (vendor, product, manufacturer) is Config's other fields; HID has
// no name of its own, so the keyboard and mice show that identity.
const (
	// DefaultCameraName and DefaultAudioName are the kernel's defaults
	// (f_uvc, f_uac1), so an empty setting keeps what targets saw before.
	DefaultCameraName    = "UVC Camera"
	DefaultAudioName     = "Source/Sink"
	DefaultStorageVendor = "JetKVM"
	DefaultStorageModel  = "Virtual Media"

	functionNameMax  = 31 // char function_name[32]
	storageVendorMax = 8  // SCSI INQUIRY vendor
	storageModelMax  = 16 // SCSI INQUIRY product
)

// FunctionNames are the per-function names of Config.
type FunctionNames struct {
	Camera        string `json:"camera_name"`
	Audio         string `json:"audio_name"`
	StorageVendor string `json:"storage_vendor"`
	StorageModel  string `json:"storage_model"`
}

func printableASCII(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

// Validate checks lengths and characters; empty fields mean the default.
func (n FunctionNames) Validate() error {
	for _, f := range []struct {
		label, value string
		max          int
	}{
		{"camera name", n.Camera, functionNameMax},
		{"audio name", n.Audio, functionNameMax},
		{"storage vendor", n.StorageVendor, storageVendorMax},
		{"storage model", n.StorageModel, storageModelMax},
	} {
		if len(f.value) > f.max {
			return fmt.Errorf("the %s has at most %d characters", f.label, f.max)
		}
		if !printableASCII(f.value) {
			return fmt.Errorf("the %s takes plain ASCII characters only", f.label)
		}
	}
	return nil
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// inquiryString is the mass storage INQUIRY vendor (8 characters, padded)
// followed by the product.
func (n FunctionNames) inquiryString() string {
	vendor := orDefault(n.StorageVendor, DefaultStorageVendor)
	return fmt.Sprintf("%-8s%s", vendor, orDefault(n.StorageModel, DefaultStorageModel))
}

// syncFunctionNames writes the names into the items that carry them. The
// camera and audio names can change only while the function is unlinked,
// which the transaction does before writing a linked item's attributes.
func (u *UsbGadget) syncFunctionNames() {
	names := FunctionNames{}
	if u.customConfig.Names != nil {
		names = *u.customConfig.Names
	}
	camera := orDefault(names.Camera, DefaultCameraName)
	for _, key := range []string{"video_base", "video_h264_base"} {
		if item, ok := u.configMap[key]; ok && item.attrs != nil {
			item.attrs["function_name"] = camera
		}
	}
	if item, ok := u.configMap["audio"]; ok && item.attrs != nil {
		item.attrs["function_name"] = orDefault(names.Audio, DefaultAudioName)
	}
	if item, ok := u.configMap["mass_storage_lun0"]; ok && item.attrs != nil {
		item.attrs["inquiry_string"] = names.inquiryString()
	}
}
