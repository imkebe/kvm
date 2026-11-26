package native

import (
	"bytes"
	"image/jpeg"
	"image/png"
	"testing"
)

// frameOf builds a YUYV frame whose left half is white and right half black
// (limited range), with a padded stride.
func frameOf(width, height int) yuyvFrame {
	stride := width*2 + 16
	data := make([]byte, stride*height)
	for y := range height {
		for x := 0; x < width; x += 2 {
			luma := byte(235)
			if x >= width/2 {
				luma = 16
			}
			o := y*stride + x*2
			data[o], data[o+1], data[o+2], data[o+3] = luma, 128, luma, 128
		}
	}
	return yuyvFrame{data: data, width: width, height: height, stride: stride}
}

func TestEncodeSnapshotScalesAndCrops(t *testing.T) {
	frame := frameOf(1920, 1080)
	shot, err := encodeSnapshot(frame, SnapshotRequest{MaxWidth: 1280, Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if shot.ImageWidth != 1280 || shot.ImageHeight != 720 || shot.Width != 1920 || shot.Height != 1080 {
		t.Fatalf("geometry %+v", shot)
	}
	img, err := png.Decode(bytes.NewReader(shot.Image))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := img.At(10, 10).RGBA(); r>>8 != 255 {
		t.Fatalf("left should be white, got %d", r>>8)
	}
	if r, _, _, _ := img.At(1270, 700).RGBA(); r>>8 != 0 {
		t.Fatalf("right should be black, got %d", r>>8)
	}

	// A region on the right half, odd x, no scaling.
	shot, err = encodeSnapshot(frame, SnapshotRequest{X: 1001, Y: 5, Width: 101, Height: 50})
	if err != nil {
		t.Fatal(err)
	}
	if shot.ImageWidth != 101 || shot.ImageHeight != 50 || shot.X != 1001 || shot.MimeType != "image/jpeg" {
		t.Fatalf("region %+v", shot)
	}
	if _, err := jpeg.Decode(bytes.NewReader(shot.Image)); err != nil {
		t.Fatal(err)
	}

	// A region past the edge is cut at the edge.
	shot, err = encodeSnapshot(frame, SnapshotRequest{X: 1900, Y: 1000, Width: 500, Height: 500, Format: "png"})
	if err != nil || shot.Width != 20 || shot.Height != 80 {
		t.Fatalf("edge %+v %v", shot, err)
	}
	if _, err := encodeSnapshot(frame, SnapshotRequest{Format: "gif"}); err == nil {
		t.Fatal("gif should be refused")
	}
}

func TestYUVToRGB(t *testing.T) {
	for _, f := range []func(y, u, v uint8) (uint8, uint8, uint8){yuvToRGB601, yuvToRGB709} {
		if r, g, b := f(16, 128, 128); r != 0 || g != 0 || b != 0 {
			t.Fatalf("black %d %d %d", r, g, b)
		}
		if r, g, b := f(235, 128, 128); r != 255 || g != 255 || b != 255 {
			t.Fatalf("white %d %d %d", r, g, b)
		}
	}
	// BT.709 red: Y 63, Cb 102, Cr 240.
	if r, g, b := yuvToRGB709(63, 102, 240); r < 250 || g > 5 || b > 5 {
		t.Fatalf("red %d %d %d", r, g, b)
	}
}
