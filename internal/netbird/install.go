package netbird

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	tarballName = "netbird.tgz.incomplete"
	newBinary   = "netbird.new"
	// maxBinarySize caps the extracted client (0.83.2 is 40.8 MB).
	maxBinarySize = 128 << 20
	// requiredSpace covers the tarball and the extracted binary at once.
	requiredSpace = 96 << 20
	stallTimeout  = 60 * time.Second
)

var (
	ErrChecksum = errors.New("the downloaded file does not match the pinned sha256")
	ErrTooLarge = errors.New("the download is larger than the pinned release")
	ErrNoBinary = errors.New("the release archive has no netbird program")
	ErrNoSpace  = errors.New("not enough free space in /userdata")
	ErrStalled  = errors.New("the server stopped sending data")
)

// Installer downloads a pinned release and installs its binary into Dir.
type Installer struct {
	Dir    string
	Client *http.Client
	// FreeSpace reports free bytes on Dir's filesystem; nil skips the check.
	FreeSpace func() (int64, error)
	// Progress is called with bytes received and the expected total.
	Progress func(received, total int64)
}

// Install fetches release, checks its size and sha256, then extracts only the
// `netbird` program and moves it into place as Dir/netbird.
func (i *Installer) Install(ctx context.Context, release Release) error {
	if err := os.MkdirAll(i.Dir, 0o755); err != nil {
		return err
	}
	if i.FreeSpace != nil {
		free, err := i.FreeSpace()
		if err != nil {
			return err
		}
		if free < requiredSpace {
			return fmt.Errorf("%w: %d MiB free, %d MiB needed", ErrNoSpace, free>>20, requiredSpace>>20)
		}
	}

	tarball := filepath.Join(i.Dir, tarballName)
	defer os.Remove(tarball)
	if err := i.download(ctx, release, tarball); err != nil {
		return err
	}

	staged := filepath.Join(i.Dir, newBinary)
	defer os.Remove(staged)
	if err := extractBinary(tarball, staged); err != nil {
		return err
	}
	return os.Rename(staged, filepath.Join(i.Dir, "netbird"))
}

func (i *Installer) download(ctx context.Context, release Release, path string) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.URL, nil)
	if err != nil {
		return err
	}
	client := i.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	if release.Size > 0 && resp.ContentLength > release.Size {
		return ErrTooLarge
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	stall := time.AfterFunc(stallTimeout, func() { cancel(ErrStalled) })
	defer stall.Stop()

	hash := sha256.New()
	limit := release.Size
	if limit <= 0 {
		limit = maxBinarySize
	}
	var received int64
	buf := make([]byte, 64<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			stall.Reset(stallTimeout)
			received += int64(n)
			if received > limit {
				return ErrTooLarge
			}
			hash.Write(buf[:n])
			if _, err := file.Write(buf[:n]); err != nil {
				return err
			}
			if i.Progress != nil {
				i.Progress(received, release.Size)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
			return readErr
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != release.SHA256 {
		return ErrChecksum
	}
	return nil
}

func extractBinary(tarball, dest string) error {
	file, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("release archive: %w", err)
	}
	defer gz.Close()

	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return ErrNoBinary
		}
		if err != nil {
			return fmt.Errorf("release archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Clean(header.Name) != "netbird" {
			continue
		}
		if header.Size > maxBinarySize {
			return ErrTooLarge
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(archive, maxBinarySize)); err != nil {
			out.Close()
			return err
		}
		if err := out.Sync(); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chmod(dest, 0o755)
	}
}
