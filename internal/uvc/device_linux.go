//go:build linux && cgo

// Package uvc implements the Linux USB video gadget's V4L2 output protocol.
package uvc

/*
#cgo LDFLAGS: -lpthread
#include <stdlib.h>
#include "output.h"
*/
import "C"

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

type Device struct {
	handle *C.struct_uvc_output
	Path   string
	codec  string
}

func Open(path string, width, height, fps int, codec string) (*Device, error) {
	function := "uvc.usb0"
	h264 := C.int(0)
	if codec == "h264" {
		function = "uvc.h264"
		h264 = 1
	}
	interfaces := make([]C.uint, 2)
	for i, class := range []string{"control", "streaming"} {
		data, err := os.ReadFile("/sys/kernel/config/usb_gadget/jetkvm/functions/" + function + "/" + class + "/bInterfaceNumber")
		if err != nil {
			return nil, fmt.Errorf("read USB video %s interface: %w", class, err)
		}
		number, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 8)
		if err != nil {
			return nil, fmt.Errorf("invalid USB video %s interface: %w", class, err)
		}
		interfaces[i] = C.uint(number)
	}
	if codec != "h264" {
		setOfferedModes()
	}
	paths := []string{path}
	if path == "auto" || path == "" {
		paths, _ = filepath.Glob("/dev/video*")
	}
	for _, candidate := range paths {
		name := C.CString(candidate)
		handle := C.uvc_output_open(name, C.uint(width), C.uint(height), C.uint(fps), interfaces[0], interfaces[1], h264)
		C.free(unsafe.Pointer(name))
		if handle != nil {
			device := &Device{handle: handle, Path: candidate, codec: codec}
			if codec == "h264" {
				return device, nil
			}
			// One blank frame per frame size the camera offers: the host
			// gets it until the browser sends a frame of the size it chose.
			for i := 0; i < int(C.uvc_output_mode_count()); i++ {
				var w, h C.uint
				C.uvc_output_mode_at(C.uint(i), &w, &h)
				// The blank frame is a colour JPEG like the browser's frames.
				// A host decoder that sets itself up from a one-component
				// (grayscale) first frame can keep showing the camera
				// without colour.
				picture := image.NewYCbCr(image.Rect(0, 0, int(w), int(h)), image.YCbCrSubsampleRatio420)
				for p := range picture.Cb {
					picture.Cb[p], picture.Cr[p] = 128, 128
				}
				var blank bytes.Buffer
				_ = jpeg.Encode(&blank, picture, &jpeg.Options{Quality: 75})
				data := blank.Bytes()
				C.uvc_output_set_blank(handle, C.uint(i), unsafe.Pointer(&data[0]), C.size_t(len(data)))
			}
			return device, nil
		}
	}
	return nil, fmt.Errorf("no UVC output device found at %s; enable USB Video in Hardware settings", path)
}

// setOfferedModes reads the MJPEG frame sizes and the frame rates that the
// gadget offers from its configfs descriptors, and gives them to the output:
// the host names a frame size by its index.
func setOfferedModes() {
	const frames = "/sys/kernel/config/usb_gadget/jetkvm/functions/uvc.usb0/streaming/mjpeg/m/"
	number := func(file string) int {
		data, _ := os.ReadFile(file)
		value, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return value
	}
	var widths, heights [C.UVC_OUTPUT_MODES]C.uint
	var intervals [C.UVC_OUTPUT_INTERVALS]C.uint
	count, intervalCount := 0, 0
	indexes, _ := filepath.Glob(frames + "*/bFrameIndex")
	for _, file := range indexes {
		dir := filepath.Dir(file)
		index, width, height := number(file), number(dir+"/wWidth"), number(dir+"/wHeight")
		if index < 1 || index > len(widths) || width <= 0 || height <= 0 {
			return
		}
		widths[index-1], heights[index-1] = C.uint(width), C.uint(height)
		count = max(count, index)
		// Every frame size offers the same intervals; keep each once.
		data, _ := os.ReadFile(dir + "/dwFrameInterval")
	fields:
		for _, field := range strings.Fields(string(data)) {
			interval, err := strconv.Atoi(field)
			if err != nil || interval <= 0 {
				continue
			}
			for i := 0; i < intervalCount; i++ {
				if intervals[i] == C.uint(interval) {
					continue fields
				}
			}
			if intervalCount < len(intervals) {
				intervals[intervalCount] = C.uint(interval)
				intervalCount++
			}
		}
	}
	if count == 0 || count != len(indexes) || intervalCount == 0 {
		return
	}
	C.uvc_output_set_modes(C.uint(count), &widths[0], &heights[0], C.uint(intervalCount), &intervals[0])
}

// Write sends a JPEG frame of the given size. It returns 0 without an error
// when the host has negotiated another frame size: see Mode.
func (d *Device) Write(jpeg []byte, width, height int) (int, error) {
	if len(jpeg) < 4 || jpeg[0] != 0xff || jpeg[1] != 0xd8 {
		return 0, fmt.Errorf("invalid JPEG frame")
	}
	result := C.uvc_output_jpeg(d.handle, unsafe.Pointer(&jpeg[0]), C.size_t(len(jpeg)), C.uint(width), C.uint(height))
	if result < 0 {
		return 0, fmt.Errorf("UVC frame exceeds the negotiated buffer or output failed")
	}
	if result > 0 {
		return 0, nil
	}
	return len(jpeg), nil
}

// Mode is the frame size and rate the host negotiated (the default one until
// it does).
func (d *Device) Mode() (width, height, fps int) {
	var w, h, f C.uint
	C.uvc_output_mode(d.handle, &w, &h, &f)
	return int(w), int(h), int(f)
}

func (d *Device) Close() error {
	if d.handle != nil {
		C.uvc_output_close(d.handle)
		d.handle = nil
	}
	return nil
}

func (d *Device) Reset() {
	if d.codec == "h264" {
		C.uvc_output_reset(d.handle)
	} else {
		C.uvc_output_blank(d.handle)
	}
}
func (d *Device) WriteH264(data []byte, keyframe bool) (int, error) {
	key := C.int(0)
	if keyframe {
		key = 1
	}
	result := C.uvc_output_frame(d.handle, unsafe.Pointer(&data[0]), C.size_t(len(data)), key)
	if result < 0 {
		return 0, fmt.Errorf("UVC H.264 frame exceeds the buffer or output failed")
	}
	if result > 0 {
		return 0, nil
	} // Waiting for host/keyframe or bounded queue recovery.
	return len(data), nil
}
func (d *Device) Recovery() (bool, uint64) {
	var needs C.int
	var dropped C.ulonglong
	C.uvc_output_recovery(d.handle, &needs, &dropped)
	return needs != 0, uint64(dropped)
}

func (d *Device) Status() (bool, uint64, error) {
	var streaming, outputError C.int
	var frames C.ulonglong
	C.uvc_output_status(d.handle, &streaming, &outputError, &frames)
	if outputError != 0 {
		return streaming != 0, uint64(frames), syscall.Errno(outputError)
	}
	return streaming != 0, uint64(frames), nil
}
