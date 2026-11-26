package uvc

import "testing"

func TestH264AccessUnits(t *testing.T) {
	key := []byte{0, 0, 0, 1, 0x67, 42, 0, 0, 1, 0x68, 12, 0, 0, 0, 1, 0x65, 99}
	delta := []byte{0, 0, 1, 0x41, 88}
	for _, tc := range []struct {
		name    string
		data    []byte
		key, ok bool
	}{
		{"independent IDR", key, true, true}, {"predictive frame", delta, false, true},
		{"missing parameter sets", []byte{0, 0, 1, 0x65, 99}, true, false},
		{"AVCC rejected", []byte{0, 0, 0, 2, 0x41, 99}, false, false},
		{"key flag on delta", delta, true, false}, {"unmarked IDR", key, false, false},
		{"truncated start code", []byte{0, 0, 1}, false, false},
		{"forbidden bit", []byte{0, 0, 1, 0xc1, 88}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidateH264(tc.data, tc.key) == nil; got != tc.ok {
				t.Fatalf("valid=%v want=%v", got, tc.ok)
			}
		})
	}
}
