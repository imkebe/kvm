package usbgadget

import "testing"

func TestAbsJiggleTargets(t *testing.T) {
	cases := []struct {
		name                 string
		x, y                 int
		known                bool
		dx, dy               int
		awayX, awayY, bx, by int
	}{
		{"middle", 1000, 2000, true, 40, -20, 1040, 1980, 1000, 2000},
		{"unknown uses the middle", 0, 0, false, 40, 40, 16423, 16423, 16383, 16383},
		{"right and bottom edge step back", 32767, 32767, true, 40, 40, 32727, 32727, 32767, 32767},
		{"left and top edge", 0, 0, true, -40, -40, 40, 40, 0, 0},
	}
	for _, c := range cases {
		ax, ay, bx, by := AbsJiggleTargets(c.x, c.y, c.known, c.dx, c.dy)
		if ax != c.awayX || ay != c.awayY || bx != c.bx || by != c.by {
			t.Errorf("%s: got away (%d,%d) back (%d,%d)", c.name, ax, ay, bx, by)
		}
	}
}
