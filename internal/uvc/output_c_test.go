//go:build linux && cgo

package uvc

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestOutputC builds and runs the C unit test of output_linux.c, which mocks
// the V4L2 ioctls.
func TestOutputC(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	bin := filepath.Join(t.TempDir(), "uvc-output-test")
	if out, err := exec.Command(cc, "-pthread", "-o", bin, "tests/output_queue_test.c").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
