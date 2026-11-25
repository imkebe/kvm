package kvm

import (
	"strings"
	"testing"
	"time"
)

// R3: a second screenshot while one runs waits for it instead of failing.
func TestScreenshotOneAtATime(t *testing.T) {
	screenshotMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := apiGetScreenshot(0, 0, 0, 0, 0, 0, "", 0)
		done <- err
	}()
	select {
	case err := <-done:
		screenshotMu.Unlock()
		t.Fatalf("a second screenshot did not wait for the first: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	screenshotMu.Unlock()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "video is not available") {
			t.Fatalf("after the first ended: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second screenshot still waits after the first ended")
	}
}
