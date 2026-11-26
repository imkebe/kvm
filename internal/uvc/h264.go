package uvc

import "fmt"

// ValidateH264 accepts one Annex-B access unit. Every recovery keyframe must
// include SPS/PPS so a host opening the camera mid-stream can decode it.
func ValidateH264(data []byte, keyframe bool) error {
	var sps, pps, idr, picture bool
	for offset := 0; offset < len(data); {
		start := annexBStart(data, offset)
		if start == 0 || offset+start >= len(data) {
			return fmt.Errorf("invalid H.264 Annex-B access unit")
		}
		nal := data[offset+start]
		if nal&0x80 != 0 {
			return fmt.Errorf("invalid H.264 NAL header")
		}
		switch nal & 31 {
		case 1:
			picture = true
		case 5:
			picture = true
			idr = true
		case 7:
			sps = true
		case 8:
			pps = true
		}
		offset += start + 1
		for offset < len(data) && annexBStart(data, offset) == 0 {
			offset++
		}
	}
	if !picture || idr != keyframe || (keyframe && (!sps || !pps)) {
		return fmt.Errorf("H.264 keyframes require IDR, SPS and PPS; delta frames require a picture")
	}
	return nil
}

func annexBStart(data []byte, offset int) int {
	if len(data)-offset >= 3 && data[offset] == 0 && data[offset+1] == 0 {
		if data[offset+2] == 1 {
			return 3
		}
		if len(data)-offset >= 4 && data[offset+2] == 0 && data[offset+3] == 1 {
			return 4
		}
	}
	return 0
}
