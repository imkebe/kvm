package kvm

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// A folder mounted from the browser. The browser builds a read-only FAT32
// disk from a folder the user picked and answers block reads over the data
// channel "vmedia-disk"; the KVM serves those reads through the NBD device
// to the USB mass storage function, like an image from a URL.
//
// Protocol, little-endian:
//   - browser, once open: text {"id": "<image id>"}
//   - KVM request:  id u32 | length u32 | offset u64
//   - browser reply: id u32 | status u8 (0 = ok) | length bytes when ok
//   - KVM, after an unmount: text "unmounted"
//
// The channel dies with the WebRTC session. Reads wait for the same browser
// tab to reconnect (it reopens the channel with the same image id); the
// folder is unmounted when it has not come back after browserDiskGone.

const (
	browserDiskChannelLabel = "vmedia-disk"
	browserDiskChunk        = 32 * 1024
	browserDiskMaxIDLen     = 64
)

// Variables so tests can shorten them.
var (
	browserDiskReadTimeout = 20 * time.Second
	browserDiskGone        = 60 * time.Second
)

// browserDiskChannel is the part of a data channel the folder reads use.
type browserDiskChannel interface {
	Send(data []byte) error
	SendText(text string) error
	Close() error
}

var (
	errBrowserDiskGone    = errors.New("the browser serving the folder is not connected")
	errBrowserDiskTimeout = errors.New("the browser did not answer a folder read in time")
	errBrowserDiskFile    = errors.New("the browser could not read a file of the folder")
	errBrowserDiskShort   = errors.New("the browser answered a folder read with the wrong length")
)

type browserDiskReply struct {
	data []byte
	err  error
}

type browserDiskConn struct {
	d browserDiskChannel

	mu      sync.Mutex
	id      string
	nextID  uint32
	pending map[uint32]chan browserDiskReply
	closed  bool
}

var (
	browserDiskMu      sync.Mutex
	browserDiskCur     *browserDiskConn
	browserDiskChanged = make(chan struct{})
	browserDiskTimer   *time.Timer
)

// broadcastBrowserDiskLocked wakes readers waiting for a channel.
func broadcastBrowserDiskLocked() {
	close(browserDiskChanged)
	browserDiskChanged = make(chan struct{})
}

// mountedBrowserImageID is the image id of the mounted folder, or "".
func mountedBrowserImageID() string {
	virtualMediaStateMutex.RLock()
	defer virtualMediaStateMutex.RUnlock()
	if currentVirtualMediaState == nil || currentVirtualMediaState.Source != Browser {
		return ""
	}
	return currentVirtualMediaState.ImageID
}

func handleBrowserDiskChannel(d *webrtc.DataChannel) {
	c := newBrowserDiskConn(d)
	d.OnMessage(func(msg webrtc.DataChannelMessage) { c.onMessage(msg.IsString, msg.Data) })
	d.OnClose(c.onClose)
}

func newBrowserDiskConn(d browserDiskChannel) *browserDiskConn {
	return &browserDiskConn{d: d, pending: map[uint32]chan browserDiskReply{}}
}

func (c *browserDiskConn) onMessage(isString bool, data []byte) {
	if !isString {
		c.deliver(data)
		return
	}
	var hello struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &hello); err != nil || hello.ID == "" || len(hello.ID) > browserDiskMaxIDLen {
		logger.Warn().Msg("folder channel: bad hello")
		return
	}
	c.mu.Lock()
	c.id = hello.ID
	c.mu.Unlock()
	setBrowserDisk(c)
}

func (c *browserDiskConn) onClose() {
	c.fail()
	clearBrowserDisk(c)
}

func (c *browserDiskConn) imageID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.id
}

// setBrowserDisk makes c the channel serving folder reads. A channel for a
// different image than the mounted one is refused.
func setBrowserDisk(c *browserDiskConn) {
	mounted := mountedBrowserImageID()
	id := c.imageID()
	if mounted != "" && mounted != id {
		logger.Warn().Str("image", id).Msg("folder channel for an image that is not mounted")
		_ = c.d.SendText("unmounted")
		_ = c.d.Close()
		return
	}
	browserDiskMu.Lock()
	old := browserDiskCur
	browserDiskCur = c
	if browserDiskTimer != nil {
		browserDiskTimer.Stop()
		browserDiskTimer = nil
	}
	broadcastBrowserDiskLocked()
	browserDiskMu.Unlock()
	if old != nil && old != c {
		old.fail()
		_ = old.d.Close()
	}
	logger.Info().Str("image", id).Msg("folder channel ready")
}

func clearBrowserDisk(c *browserDiskConn) {
	mounted := mountedBrowserImageID()
	browserDiskMu.Lock()
	defer browserDiskMu.Unlock()
	if browserDiskCur != c {
		return
	}
	browserDiskCur = nil
	broadcastBrowserDiskLocked()
	if mounted == "" {
		return
	}
	logger.Info().Str("image", mounted).Msg("folder channel closed, waiting for the browser to come back")
	if browserDiskTimer != nil {
		browserDiskTimer.Stop()
	}
	browserDiskTimer = time.AfterFunc(browserDiskGone, func() {
		browserDiskMu.Lock()
		back := browserDiskCur != nil
		browserDiskMu.Unlock()
		if back || mountedBrowserImageID() != mounted {
			return
		}
		logger.Warn().Str("image", mounted).Msg("browser did not come back, unmounting the folder")
		if err := rpcUnmountImage(); err != nil {
			logger.Warn().Err(err).Msg("failed to unmount the folder")
		}
	})
}

// browserDiskUnmounted tells the browser the folder is no longer mounted, so
// it lets go of the files.
func browserDiskUnmounted() {
	browserDiskMu.Lock()
	c := browserDiskCur
	browserDiskCur = nil
	if browserDiskTimer != nil {
		browserDiskTimer.Stop()
		browserDiskTimer = nil
	}
	broadcastBrowserDiskLocked()
	browserDiskMu.Unlock()
	if c != nil {
		c.fail()
		_ = c.d.SendText("unmounted")
	}
}

func (c *browserDiskConn) request(off int64, length int) (uint32, chan browserDiskReply, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, nil, errBrowserDiskGone
	}
	id := c.nextID
	c.nextID++
	ch := make(chan browserDiskReply, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	var req [16]byte
	binary.LittleEndian.PutUint32(req[0:], id)
	binary.LittleEndian.PutUint32(req[4:], uint32(length))
	binary.LittleEndian.PutUint64(req[8:], uint64(off))
	if err := c.d.Send(req[:]); err != nil {
		c.forget(id)
		return 0, nil, errBrowserDiskGone
	}
	return id, ch, nil
}

func (c *browserDiskConn) forget(id uint32) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *browserDiskConn) deliver(msg []byte) {
	if len(msg) < 5 {
		return
	}
	id := binary.LittleEndian.Uint32(msg[0:])
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if msg[4] != 0 {
		ch <- browserDiskReply{err: errBrowserDiskFile}
		return
	}
	ch <- browserDiskReply{data: append([]byte(nil), msg[5:]...)}
}

// fail answers every waiting read with errBrowserDiskGone.
func (c *browserDiskConn) fail() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, ch := range c.pending {
		ch <- browserDiskReply{err: errBrowserDiskGone}
		delete(c.pending, id)
	}
}

// read fills p from the browser, in chunks sent at once.
func (c *browserDiskConn) read(p []byte, off int64, deadline time.Time) error {
	type part struct {
		id  uint32
		ch  chan browserDiskReply
		buf []byte
	}
	parts := make([]part, 0, (len(p)+browserDiskChunk-1)/browserDiskChunk)
	defer func() {
		for _, pt := range parts {
			c.forget(pt.id)
		}
	}()
	for start := 0; start < len(p); start += browserDiskChunk {
		end := min(start+browserDiskChunk, len(p))
		id, ch, err := c.request(off+int64(start), end-start)
		if err != nil {
			return err
		}
		parts = append(parts, part{id, ch, p[start:end]})
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for _, pt := range parts {
		select {
		case r := <-pt.ch:
			if r.err != nil {
				return r.err
			}
			if len(r.data) != len(pt.buf) {
				return errBrowserDiskShort
			}
			copy(pt.buf, r.data)
		case <-timer.C:
			return errBrowserDiskTimeout
		}
	}
	return nil
}

// waitBrowserDisk returns the channel serving image id, waiting for the
// browser to (re)connect until deadline.
func waitBrowserDisk(id string, deadline time.Time) (*browserDiskConn, error) {
	for {
		browserDiskMu.Lock()
		c, changed := browserDiskCur, browserDiskChanged
		browserDiskMu.Unlock()
		if c != nil && c.imageID() == id {
			return c, nil
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return nil, errBrowserDiskGone
		}
		select {
		case <-changed:
		case <-time.After(wait):
			return nil, errBrowserDiskGone
		}
	}
}

func browserDiskReadAt(p []byte, off int64, id string) (int, error) {
	deadline := time.Now().Add(browserDiskReadTimeout)
	for {
		c, err := waitBrowserDisk(id, deadline)
		if err != nil {
			return 0, err
		}
		err = c.read(p, off, deadline)
		if err == nil {
			return len(p), nil
		}
		// A lost channel is retried on the next one; anything else is final.
		if !errors.Is(err, errBrowserDiskGone) || !time.Now().Before(deadline) {
			return 0, err
		}
	}
}

func prepareBrowserMount(name string, size int64, imageID string) error {
	virtualMediaStateMutex.Lock()
	defer virtualMediaStateMutex.Unlock()
	if currentVirtualMediaState != nil {
		return fmt.Errorf("another virtual media is already mounted")
	}
	if err := requireMassStorage(); err != nil {
		return err
	}
	// Same as in prepareHTTPMount: eject before changing the mode.
	if err := ejectMassStorageImage(); err != nil {
		return fmt.Errorf("failed to eject before setting mass storage mode: %w", err)
	}
	if err := setMassStorageMode(false); err != nil {
		return fmt.Errorf("failed to set mass storage mode: %w", err)
	}
	currentVirtualMediaState = &VirtualMediaState{
		Source:   Browser,
		Mode:     Disk,
		Filename: name,
		Size:     size,
		ImageID:  imageID,
	}
	return nil
}

// rpcMountWithBrowser mounts a folder served by this browser as a read-only
// USB disk. The browser opens the folder channel before calling it.
func rpcMountWithBrowser(name string, size int64, imageID string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "" || len(name) > 255:
		return errors.New("invalid folder name")
	case size <= 0 || size%4096 != 0 || size > 2<<40: // 2 TiB, the most FAT32 holds
		return errors.New("invalid folder image size")
	case imageID == "" || len(imageID) > browserDiskMaxIDLen:
		return errors.New("invalid folder image id")
	}
	if err := requireMassStorage(); err != nil {
		return err
	}
	if _, err := waitBrowserDisk(imageID, time.Now().Add(5*time.Second)); err != nil {
		return errors.New("the folder channel from this browser is not open")
	}
	if err := prepareBrowserMount(name, size, imageID); err != nil {
		return err
	}
	if err := startNBDMount(); err != nil {
		if unmountErr := unmountImageLocked(); unmountErr != nil {
			logger.Warn().Err(unmountErr).Msg("failed to clean up after a failed folder mount")
		}
		return err
	}
	logger.Info().Str("folder", name).Int64("size", size).Msg("folder from the browser mounted")
	if mqttManager != nil {
		mqttManager.publishVirtualMediaState()
	}
	return nil
}
