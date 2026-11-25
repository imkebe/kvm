package native

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"time"
)

// Screenshots of the HDMI input. The capture loop copies one raw frame
// (YUYV 4:2:2, limited range, BT.709 for HD and BT.601 for SD, as the
// capture is configured in video.c); this file crops it, scales it down and
// encodes it, in the native process so that only the encoded image crosses
// gRPC.

var errSnapshotTimeout = errors.New("no frame came from the HDMI capture in time")

// yuyvFrame is one captured frame.
type yuyvFrame struct {
	data          []byte
	width, height int
	stride        int
}

// SnapshotRequest says what part of the screen to take and how to encode it.
type SnapshotRequest struct {
	// The region in screen pixels; a zero width or height reaches the
	// right or bottom edge.
	X, Y, Width, Height int
	// The image is scaled down, keeping its aspect, to fit these; zero
	// means no limit.
	MaxWidth, MaxHeight int
	// "jpeg" (default) or "png".
	Format string
	// JPEG quality 1-100, default 80.
	Quality int
}

// Snapshot is an encoded screenshot and where it comes from.
type Snapshot struct {
	Image                     []byte
	MimeType                  string
	ScreenWidth, ScreenHeight int
	// The region taken, in screen pixels.
	X, Y, Width, Height int
	// The size of the image, after scaling.
	ImageWidth, ImageHeight int
}

// VideoSnapshot takes a screenshot of the HDMI input. While no browser
// watches, the capture is stopped: it starts for one frame and stops again
// (waking the capture chip first if it sleeps, which takes a few seconds).
func (n *Native) VideoSnapshot(req SnapshotRequest) (Snapshot, error) {
	frame, err := n.captureFrame()
	if err != nil {
		return Snapshot{}, err
	}
	return encodeSnapshot(frame, req)
}

func (n *Native) captureFrame() (yuyvFrame, error) {
	n.videoLock.Lock()
	defer n.videoLock.Unlock()

	if videoGetStreamingStatus() == VideoStreamingStatusActive {
		return videoSnapshot(3 * time.Second)
	}
	wasSleeping, _ := n.getSleepMode()
	if wasSleeping {
		_ = n.setSleepMode(false)
		time.Sleep(extraLockTimeout)
	}
	videoStart()
	frame, err := videoSnapshot(5 * time.Second)
	videoStop()
	if wasSleeping {
		_ = n.setSleepMode(true)
	}
	return frame, err
}

// encodeSnapshot crops, scales (area average) and encodes a frame.
func encodeSnapshot(frame yuyvFrame, req SnapshotRequest) (Snapshot, error) {
	if frame.width <= 0 || frame.height <= 0 || frame.stride < frame.width*2 ||
		len(frame.data) < frame.stride*frame.height {
		return Snapshot{}, fmt.Errorf("bad frame %dx%d", frame.width, frame.height)
	}
	x0, y0 := clampInt(req.X, 0, frame.width-1), clampInt(req.Y, 0, frame.height-1)
	cw, ch := req.Width, req.Height
	if cw <= 0 || x0+cw > frame.width {
		cw = frame.width - x0
	}
	if ch <= 0 || y0+ch > frame.height {
		ch = frame.height - y0
	}
	ow, oh := cw, ch
	if req.MaxWidth > 0 && ow > req.MaxWidth {
		oh = max(1, oh*req.MaxWidth/ow)
		ow = req.MaxWidth
	}
	if req.MaxHeight > 0 && oh > req.MaxHeight {
		ow = max(1, ow*req.MaxHeight/oh)
		oh = req.MaxHeight
	}

	img := image.NewRGBA(image.Rect(0, 0, ow, oh))
	convert := yuvToRGB601
	if frame.height > 576 {
		convert = yuvToRGB709
	}
	// The output column of each source column and how many source columns
	// each output column averages.
	column := make([]int, cw)
	widths := make([]int, ow)
	for sx := range cw {
		ox := sx * ow / cw
		column[sx] = ox
		widths[ox]++
	}
	sums := make([]uint32, ow*3)
	for oy := range oh {
		sy0, sy1 := oy*ch/oh, (oy+1)*ch/oh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		clear(sums)
		for sy := sy0; sy < sy1; sy++ {
			row := frame.data[(y0+sy)*frame.stride:]
			for sx := range cw {
				px := x0 + sx
				base := (px >> 1) * 4
				r, g, b := convert(row[base+(px&1)*2], row[base+1], row[base+3])
				o := column[sx] * 3
				sums[o] += uint32(r)
				sums[o+1] += uint32(g)
				sums[o+2] += uint32(b)
			}
		}
		rows := uint32(sy1 - sy0)
		line := img.Pix[oy*img.Stride:]
		for ox := range ow {
			count := rows * uint32(widths[ox])
			if count == 0 {
				count = 1
			}
			line[ox*4] = uint8(sums[ox*3] / count)
			line[ox*4+1] = uint8(sums[ox*3+1] / count)
			line[ox*4+2] = uint8(sums[ox*3+2] / count)
			line[ox*4+3] = 0xff
		}
	}

	var out bytes.Buffer
	mime := "image/jpeg"
	switch req.Format {
	case "", "jpeg", "jpg":
		quality := req.Quality
		if quality <= 0 {
			quality = 80
		}
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: clampInt(quality, 1, 100)}); err != nil {
			return Snapshot{}, err
		}
	case "png":
		mime = "image/png"
		encoder := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := encoder.Encode(&out, img); err != nil {
			return Snapshot{}, err
		}
	default:
		return Snapshot{}, fmt.Errorf("format is jpeg or png, not %q", req.Format)
	}
	return Snapshot{
		Image:        out.Bytes(),
		MimeType:     mime,
		ScreenWidth:  frame.width,
		ScreenHeight: frame.height,
		X:            x0,
		Y:            y0,
		Width:        cw,
		Height:       ch,
		ImageWidth:   ow,
		ImageHeight:  oh,
	}, nil
}

func clampInt(v, lo, hi int) int {
	return max(lo, min(v, hi))
}

func clamp8(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// Limited-range YCbCr to RGB, coefficients times 1024.
func yuvToRGB709(y, u, v uint8) (uint8, uint8, uint8) {
	c := (int32(y) - 16) * 1192
	d, e := int32(u)-128, int32(v)-128
	return clamp8((c + 1836*e + 512) >> 10), clamp8((c - 218*d - 546*e + 512) >> 10), clamp8((c + 2163*d + 512) >> 10)
}

func yuvToRGB601(y, u, v uint8) (uint8, uint8, uint8) {
	c := (int32(y) - 16) * 1192
	d, e := int32(u)-128, int32(v)-128
	return clamp8((c + 1634*e + 512) >> 10), clamp8((c - 401*d - 833*e + 512) >> 10), clamp8((c + 2066*d + 512) >> 10)
}
