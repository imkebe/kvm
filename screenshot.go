package kvm

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sync"

	"github.com/jetkvm/kvm/internal/native"
)

// Screenshots of the HDMI input for the device API (MCP tool kvm_screenshot).
// The native process captures one frame, crops, scales and encodes it
// (internal/native/snapshot.go).

type screenshotResult struct {
	// Image is the encoded image in base64.
	Image        string `json:"image"`
	MimeType     string `json:"mimeType"`
	ScreenWidth  int    `json:"screenWidth"`
	ScreenHeight int    `json:"screenHeight"`
	// The region taken, in screen pixels.
	X           int `json:"x"`
	Y           int `json:"y"`
	Width       int `json:"width"`
	Height      int `json:"height"`
	ImageWidth  int `json:"imageWidth"`
	ImageHeight int `json:"imageHeight"`
}

// screenshotMu lets one screenshot run at a time (the next one waits): each
// holds several MiB of frame and image buffers.
var screenshotMu sync.Mutex

func apiGetScreenshot(x, y, width, height, maxWidth, maxHeight int, format string, quality int) (screenshotResult, error) {
	screenshotMu.Lock()
	defer screenshotMu.Unlock()
	if nativeInstance == nil {
		return screenshotResult{}, errors.New("video is not available")
	}
	if osServicesOwnsVideo() {
		return screenshotResult{}, errors.New("the KVM shows USB Video from OS Services, which it cannot take a screenshot of; switch the video to HDMI first")
	}
	if !lastVideoState.Ready {
		reason := lastVideoState.Error
		if reason == "" {
			reason = "not ready"
		}
		return screenshotResult{}, fmt.Errorf("no HDMI picture (%s)", reason)
	}
	if x < 0 || y < 0 || width < 0 || height < 0 || maxWidth < 0 || maxHeight < 0 {
		return screenshotResult{}, errors.New("x, y, width, height and the limits must not be negative")
	}
	shot, err := nativeInstance.VideoSnapshot(native.SnapshotRequest{
		X: x, Y: y, Width: width, Height: height,
		MaxWidth: maxWidth, MaxHeight: maxHeight,
		Format: format, Quality: quality,
	})
	if err != nil {
		return screenshotResult{}, err
	}
	return screenshotResult{
		Image:        base64.StdEncoding.EncodeToString(shot.Image),
		MimeType:     shot.MimeType,
		ScreenWidth:  shot.ScreenWidth,
		ScreenHeight: shot.ScreenHeight,
		X:            shot.X,
		Y:            shot.Y,
		Width:        shot.Width,
		Height:       shot.Height,
		ImageWidth:   shot.ImageWidth,
		ImageHeight:  shot.ImageHeight,
	}, nil
}
