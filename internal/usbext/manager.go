package usbext

import (
	"context"
	"sync"
	"time"

	"github.com/jetkvm/kvm/internal/logging"
	"github.com/jetkvm/kvm/internal/usbgadget"
	"github.com/rs/zerolog"
)

// Status represents a snapshot of the extended USB gadget functionality state.
type Status struct {
	Ethernet EthernetStatus `json:"ethernet"`
	Camera   CameraStatus   `json:"camera"`
}

// DesiredState captures the intended enablement for optional USB extensions.
type DesiredState struct {
	UsbEthernet bool `json:"usb_ethernet"`
	UsbCamera   bool `json:"usb_camera"`
}

// Controller applies desired states to the underlying firmware implementation and
// can report back with the most recent status snapshot.
type Controller interface {
	ApplyDesiredState(ctx context.Context, desired DesiredState) (Status, error)
	FetchStatus(ctx context.Context) (Status, error)
}

// EthernetStatus tracks the desired enablement and observed link state of the USB Ethernet gadget.
type EthernetStatus struct {
	Enabled   bool      `json:"enabled"`
	LinkUp    bool      `json:"link_up"`
	LastError string    `json:"last_error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CameraStatus tracks the desired enablement and streaming state of the USB camera gadget.
type CameraStatus struct {
	Enabled   bool      `json:"enabled"`
	Streaming bool      `json:"streaming"`
	LastError string    `json:"last_error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Manager coordinates desired configuration for the extended USB functions (Ethernet, camera).
type Manager struct {
	mu           sync.RWMutex
	log          *zerolog.Logger
	ethernet     EthernetStatus
	camera       CameraStatus
	controller   Controller
	applyTimeout time.Duration
}

var defaultLogger = logging.GetSubsystemLogger("usbext")

// NewManager creates a new Manager instance.
func NewManager(controller Controller, logger *zerolog.Logger) *Manager {
	if logger == nil {
		logger = defaultLogger
	}
	now := time.Now()
	return &Manager{
		log:          logger,
		controller:   controller,
		applyTimeout: 15 * time.Second,
		ethernet: EthernetStatus{
			Enabled:   false,
			LinkUp:    false,
			UpdatedAt: now,
		},
		camera: CameraStatus{
			Enabled:   false,
			Streaming: false,
			UpdatedAt: now,
		},
	}
}

// ApplyDeviceConfig updates the desired state based on the provided USB gadget device configuration.
func (m *Manager) ApplyDeviceConfig(devices usbgadget.Devices) error {
	desired := DesiredState{
		UsbEthernet: devices.UsbEthernet,
		UsbCamera:   devices.UsbCamera,
	}

	previous := m.Status()

	m.SetEthernetEnabled(desired.UsbEthernet)
	m.SetCameraEnabled(desired.UsbCamera)

	if m.controller == nil {
		m.log.Debug().Msg("no usb extension controller configured; skipping apply")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.applyTimeout)
	defer cancel()

	status, err := m.controller.ApplyDesiredState(ctx, desired)
	if err != nil {
		m.handleApplyError(previous, desired, err)
		return err
	}

	m.mergeControllerStatus(status)
	return nil
}

// SetEthernetEnabled updates the desired state for the USB Ethernet gadget.
func (m *Manager) SetEthernetEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ethernet.Enabled == enabled {
		m.ethernet.UpdatedAt = time.Now()
		return
	}

	m.log.Info().Bool("enabled", enabled).Msg("setting USB Ethernet state")
	m.ethernet.Enabled = enabled
	m.ethernet.UpdatedAt = time.Now()

	if !enabled {
		m.ethernet.LinkUp = false
		m.ethernet.LastError = ""
	}
}

// SetEthernetLinkState records the observed link state for the USB Ethernet gadget.
func (m *Manager) SetEthernetLinkState(linkUp bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ethernet.LinkUp == linkUp {
		m.ethernet.UpdatedAt = time.Now()
		return
	}

	m.log.Debug().Bool("link_up", linkUp).Msg("updating USB Ethernet link state")
	m.ethernet.LinkUp = linkUp
	m.ethernet.UpdatedAt = time.Now()
}

// ReportEthernetError stores the latest error string for USB Ethernet operations.
func (m *Manager) ReportEthernetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err == nil {
		m.ethernet.LastError = ""
	} else {
		m.ethernet.LastError = err.Error()
		m.log.Warn().Err(err).Msg("usb ethernet error")
	}
	m.ethernet.UpdatedAt = time.Now()
}

// SetCameraEnabled updates the desired state for the USB camera gadget.
func (m *Manager) SetCameraEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.camera.Enabled == enabled {
		m.camera.UpdatedAt = time.Now()
		return
	}

	m.log.Info().Bool("enabled", enabled).Msg("setting USB camera state")
	m.camera.Enabled = enabled
	m.camera.UpdatedAt = time.Now()

	if !enabled {
		m.camera.Streaming = false
		m.camera.LastError = ""
	}
}

// SetCameraStreaming updates the observed streaming flag for the USB camera gadget.
func (m *Manager) SetCameraStreaming(streaming bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.camera.Streaming == streaming {
		m.camera.UpdatedAt = time.Now()
		return
	}

	m.log.Debug().Bool("streaming", streaming).Msg("updating USB camera streaming state")
	m.camera.Streaming = streaming
	m.camera.UpdatedAt = time.Now()
}

// ReportCameraError stores the latest error string for USB camera operations.
func (m *Manager) ReportCameraError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err == nil {
		m.camera.LastError = ""
	} else {
		m.camera.LastError = err.Error()
		m.log.Warn().Err(err).Msg("usb camera error")
	}
	m.camera.UpdatedAt = time.Now()
}

// Status returns a snapshot of the current state for external consumption.
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return Status{
		Ethernet: m.ethernet,
		Camera:   m.camera,
	}
}

func (m *Manager) mergeControllerStatus(status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.mergeEthernetStatusLocked(status.Ethernet)
	m.mergeCameraStatusLocked(status.Camera)
}

func (m *Manager) mergeEthernetStatusLocked(status EthernetStatus) {
	if status.LastError != "" {
		m.ethernet.LastError = status.LastError
	} else {
		m.ethernet.LastError = ""
	}
	m.ethernet.LinkUp = status.LinkUp
	if !status.UpdatedAt.IsZero() {
		m.ethernet.UpdatedAt = status.UpdatedAt
	} else {
		m.ethernet.UpdatedAt = time.Now()
	}
}

func (m *Manager) mergeCameraStatusLocked(status CameraStatus) {
	if status.LastError != "" {
		m.camera.LastError = status.LastError
	} else {
		m.camera.LastError = ""
	}
	m.camera.Streaming = status.Streaming
	if !status.UpdatedAt.IsZero() {
		m.camera.UpdatedAt = status.UpdatedAt
	} else {
		m.camera.UpdatedAt = time.Now()
	}
}

func (m *Manager) handleApplyError(previous Status, desired DesiredState, applyErr error) {
	if applyErr == nil {
		return
	}

	if previous.Ethernet.Enabled != desired.UsbEthernet || desired.UsbEthernet {
		m.ReportEthernetError(applyErr)
	}

	if previous.Camera.Enabled != desired.UsbCamera || desired.UsbCamera {
		m.ReportCameraError(applyErr)
	}
}

// RefreshStatus queries the controller for the latest status snapshot.
func (m *Manager) RefreshStatus(ctx context.Context) error {
	if m.controller == nil {
		return nil
	}

	status, err := m.controller.FetchStatus(ctx)
	if err != nil {
		return err
	}

	m.mergeControllerStatus(status)
	return nil
}
