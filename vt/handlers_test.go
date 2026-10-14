package vt

import (
	"testing"
)

func TestShiftOutShiftIn(t *testing.T) {
	e := newTestTerminal(t, 10, 1)
	// G1 is DEC special graphics, where "q" is a horizontal line.
	_, _ = e.WriteString("\x1b)0\x0eq\x0fq")
	if got := rowText(e, 0); got != "─q" {
		t.Errorf("row 0: got = %q, want = %q", got, "─q")
	}
}

func TestScrollRegionClamp(t *testing.T) {
	e := newTestTerminal(t, 10, 5)
	_, _ = e.WriteString("\x1b[2;99r")
	if r := e.scr.ScrollRegion(); r.Min.Y != 1 || r.Max.Y != 5 {
		t.Errorf("region: got = %v, want rows 1-5", r)
	}
	_, _ = e.WriteString("\x1b[5;9r")
	if r := e.scr.ScrollRegion(); r.Min.Y != 1 || r.Max.Y != 5 {
		t.Errorf("region after an empty one: got = %v, want it unchanged", r)
	}

	// Lines inserted and deleted in a region given past the screen stay
	// within the screen.
	e = newTestTerminal(t, 10, 5)
	_, _ = e.WriteString("a\r\nb\r\nc\r\nd\r\ne\x1b[2;99r\x1b[3;1H\x1b[M\x1b[2;1H\x1b[L")
	for y, want := range []string{"a", "", "b", "d", "e"} {
		if got := rowText(e, y); got != want {
			t.Errorf("row %d: got = %q, want = %q", y, got, want)
		}
	}
}
