package usbext

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jetkvm/kvm/internal/usbgadget"
	"github.com/rs/zerolog"
)

type stubController struct {
	status      Status
	applyErr    error
	fetchErr    error
	lastDesired DesiredState
}

func (s *stubController) ApplyDesiredState(ctx context.Context, desired DesiredState) (Status, error) {
	s.lastDesired = desired
	return s.status, s.applyErr
}

func (s *stubController) FetchStatus(ctx context.Context) (Status, error) {
	return s.status, s.fetchErr
}

func newTestLogger() *zerolog.Logger {
	logger := zerolog.New(io.Discard)
	return &logger
}

func TestManagerApplyDeviceConfigInvokesController(t *testing.T) {
	ctrl := &stubController{
		status: Status{
			Ethernet: EthernetStatus{LinkUp: true, UpdatedAt: time.Now()},
			Camera:   CameraStatus{Streaming: false, UpdatedAt: time.Now()},
		},
	}
	mgr := NewManager(ctrl, newTestLogger())

	devices := usbgadget.Devices{UsbEthernet: true, UsbCamera: false}
	if err := mgr.ApplyDeviceConfig(devices); err != nil {
		t.Fatalf("ApplyDeviceConfig returned error: %v", err)
	}

	if !ctrl.lastDesired.UsbEthernet || ctrl.lastDesired.UsbCamera {
		t.Fatalf("unexpected desired state: %+v", ctrl.lastDesired)
	}

	status := mgr.Status()
	if !status.Ethernet.Enabled {
		t.Fatalf("expected ethernet enabled to reflect desired state")
	}
	if !status.Ethernet.LinkUp {
		t.Fatalf("expected ethernet link to reflect controller status")
	}
}

func TestManagerApplyDeviceConfigErrorUpdatesLastError(t *testing.T) {
	ctrl := &stubController{
		status: Status{
			Ethernet: EthernetStatus{LinkUp: false, UpdatedAt: time.Now()},
		},
		applyErr: errors.New("apply failed"),
	}
	mgr := NewManager(ctrl, newTestLogger())

	devices := usbgadget.Devices{UsbEthernet: true}
	err := mgr.ApplyDeviceConfig(devices)
	if err == nil {
		t.Fatalf("expected error when controller fails")
	}

	status := mgr.Status()
	if status.Ethernet.LastError == "" {
		t.Fatalf("expected ethernet last error to be recorded")
	}
	if status.Camera.LastError != "" {
		t.Fatalf("expected camera last error to remain empty")
	}
}

func TestManagerRefreshStatusUpdatesSnapshot(t *testing.T) {
	ctrl := &stubController{
		status: Status{
			Ethernet: EthernetStatus{LinkUp: true, UpdatedAt: time.Now()},
			Camera:   CameraStatus{Streaming: true, UpdatedAt: time.Now()},
		},
	}
	mgr := NewManager(ctrl, newTestLogger())

	if err := mgr.RefreshStatus(context.Background()); err != nil {
		t.Fatalf("RefreshStatus returned error: %v", err)
	}

	status := mgr.Status()
	if !status.Camera.Streaming {
		t.Fatalf("expected camera streaming flag to be set from controller status")
	}
}
