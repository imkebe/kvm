package kvm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jetkvm/kvm/internal/usbgadget"
)

// fakeBrowserDisk answers folder reads from an in-memory image, like the
// browser does.
type fakeBrowserDisk struct {
	image []byte
	conn  *browserDiskConn

	mu       sync.Mutex
	silent   bool // swallow requests
	fileErr  bool // answer with a read error
	texts    []string
	closed   bool
	requests int
}

func (f *fakeBrowserDisk) Send(req []byte) error {
	f.mu.Lock()
	f.requests++
	silent, fileErr := f.silent, f.fileErr
	f.mu.Unlock()
	if silent {
		return nil
	}
	id := binary.LittleEndian.Uint32(req[0:])
	length := binary.LittleEndian.Uint32(req[4:])
	off := binary.LittleEndian.Uint64(req[8:])
	go func() {
		reply := make([]byte, 5, 5+length)
		binary.LittleEndian.PutUint32(reply, id)
		if fileErr {
			reply[4] = 1
		} else {
			reply = append(reply, f.image[off:off+uint64(length)]...)
		}
		f.conn.onMessage(false, reply)
	}()
	return nil
}

func (f *fakeBrowserDisk) SendText(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	return nil
}

func (f *fakeBrowserDisk) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeBrowserDisk) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

func newFakeBrowserDisk(image []byte, id string) *fakeBrowserDisk {
	f := &fakeBrowserDisk{image: image}
	f.conn = newBrowserDiskConn(f)
	if id != "" {
		f.conn.onMessage(true, []byte(`{"id":"`+id+`"}`))
	}
	return f
}

func setupBrowserDiskTest(t *testing.T, id string, size int) []byte {
	t.Helper()
	prevTimeout, prevGone := browserDiskReadTimeout, browserDiskGone
	browserDiskReadTimeout, browserDiskGone = 2*time.Second, time.Hour
	image := make([]byte, size)
	for i := range image {
		image[i] = byte(i*7 + i/4096)
	}
	virtualMediaStateMutex.Lock()
	currentVirtualMediaState = &VirtualMediaState{Source: Browser, Mode: Disk, Filename: "folder", Size: int64(size), ImageID: id}
	virtualMediaStateMutex.Unlock()
	t.Cleanup(func() {
		browserDiskReadTimeout, browserDiskGone = prevTimeout, prevGone
		virtualMediaStateMutex.Lock()
		currentVirtualMediaState = nil
		virtualMediaStateMutex.Unlock()
		browserDiskMu.Lock()
		browserDiskCur = nil
		if browserDiskTimer != nil {
			browserDiskTimer.Stop()
			browserDiskTimer = nil
		}
		browserDiskMu.Unlock()
	})
	return image
}

func TestBrowserDiskRead(t *testing.T) {
	image := setupBrowserDiskTest(t, "img1", 1<<20)
	f := newFakeBrowserDisk(image, "img1")

	// A read larger than one chunk, through the NBD backend.
	p := make([]byte, 100*1024+17)
	n, err := remoteImageBackend{}.ReadAt(p, 12345)
	if err != nil || n != len(p) {
		t.Fatalf("read: %d %v", n, err)
	}
	if !bytes.Equal(p, image[12345:12345+len(p)]) {
		t.Fatal("read returned the wrong bytes")
	}
	f.mu.Lock()
	requests := f.requests
	f.mu.Unlock()
	if requests != 4 {
		t.Fatalf("%d requests, want 4 chunks", requests)
	}
	size, _ := remoteImageBackend{}.Size()
	if size != 1<<20 {
		t.Fatalf("size %d", size)
	}

	f.mu.Lock()
	f.fileErr = true
	f.mu.Unlock()
	if _, err := browserDiskReadAt(make([]byte, 512), 0, "img1"); !errors.Is(err, errBrowserDiskFile) {
		t.Fatalf("file error: %v", err)
	}
}

func TestBrowserDiskReconnect(t *testing.T) {
	image := setupBrowserDiskTest(t, "img1", 256*1024)
	first := newFakeBrowserDisk(image, "img1")
	first.mu.Lock()
	first.silent = true
	first.mu.Unlock()

	done := make(chan error, 1)
	p := make([]byte, 64*1024)
	go func() {
		_, err := browserDiskReadAt(p, 4096, "img1")
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	first.conn.onClose() // the session went away mid-read

	select {
	case err := <-done:
		t.Fatalf("read finished without a browser: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	newFakeBrowserDisk(image, "img1") // the same tab reconnects
	if err := <-done; err != nil {
		t.Fatalf("read after reconnect: %v", err)
	}
	if !bytes.Equal(p, image[4096:4096+len(p)]) {
		t.Fatal("wrong bytes after reconnect")
	}
	browserDiskMu.Lock()
	timer := browserDiskTimer
	browserDiskMu.Unlock()
	if timer != nil {
		t.Fatal("the unmount timer survived the reconnect")
	}
}

func TestBrowserDiskRefusesOtherImage(t *testing.T) {
	image := setupBrowserDiskTest(t, "img1", 4096)
	owner := newFakeBrowserDisk(image, "img1")
	other := newFakeBrowserDisk(image, "img2")
	if texts := other.sentTexts(); len(texts) != 1 || texts[0] != "unmounted" || !other.closed {
		t.Fatalf("other image: texts %v closed %v", texts, other.closed)
	}
	browserDiskMu.Lock()
	cur := browserDiskCur
	browserDiskMu.Unlock()
	if cur != owner.conn {
		t.Fatal("another image replaced the mounted one")
	}
}

func TestBrowserDiskGoneAndUnmounted(t *testing.T) {
	setupBrowserDiskTest(t, "img1", 4096)
	browserDiskReadTimeout = 100 * time.Millisecond
	if _, err := browserDiskReadAt(make([]byte, 512), 0, "img1"); !errors.Is(err, errBrowserDiskGone) {
		t.Fatalf("no browser: %v", err)
	}

	f := newFakeBrowserDisk(make([]byte, 4096), "img1")
	browserDiskUnmounted()
	if texts := f.sentTexts(); len(texts) != 1 || texts[0] != "unmounted" {
		t.Fatalf("unmount notice: %v", texts)
	}
	if err := f.conn.read(make([]byte, 512), 0, time.Now().Add(time.Second)); err == nil {
		t.Fatal("a read went through after the unmount")
	}
}

func TestBrowserMountNeedsMassStorage(t *testing.T) {
	prev := config
	t.Cleanup(func() { config = prev })
	config = &Config{UsbDevices: &usbgadget.Devices{MassStorage: false}}
	if err := rpcMountWithBrowser("folder", 4096, "img1"); !errors.Is(err, errMassStorageOff) {
		t.Fatalf("mount with mass storage off: %v", err)
	}
	if err := ejectMassStorageImage(); err != nil {
		t.Fatalf("eject with mass storage off: %v", err)
	}
	if err := rpcMountWithBrowser("folder", 2<<40+4096, "img1"); err == nil || errors.Is(err, errMassStorageOff) {
		t.Fatalf("a folder image above 2 TiB: %v", err)
	}
}
