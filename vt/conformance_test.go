package vt

import (
	"testing"
)

// The tests below encode xterm 412 (XTERM_DATE 2026-10-11) where x/vt's
// margins, erases and wraps went another way: each names the xterm function
// it follows.

// charproc.c's CASE_ANSI_SC, which is DECSLRM while DECLRMM is set, reads a
// right margin past the screen as its last column (MaxCols), and sets the
// margins only if right > left. Every operation that works inside the left
// and right margins then stays on the row.
func TestDECSLRMPastTheScreenIsClamped(t *testing.T) {
	for _, op := range []struct{ name, seq string }{
		{"ICH", "\x1b[@"},
		{"IL", "\x1b[L"},
		{"DL", "\x1b[M"},
		{"SU", "\x1b[S"},
		{"SD", "\x1b[T"},
		{"LF at the bottom row", "\x1b[3;1H\n"},
		{"RI at the top", "\x1b[1;1H\x1bM"},
		{"IRM", "\x1b[4hx"},
	} {
		t.Run(op.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 3)
			_, _ = e.WriteString("\x1b[?69h\x1b[1;99s")
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s under a right margin past the screen panicked: %v", op.name, r)
				}
			}()
			_, _ = e.WriteString(op.seq)
			if r := e.scr.ScrollRegion(); r.Min.X != 0 || r.Max.X != 10 {
				t.Errorf("region: got = %v, want columns 0 to 10", r)
			}
		})
	}
}

// charproc.c's set_left_right_margin_mode resets the left and right margins
// (reset_lr_margins) when DECLRMM is reset, so a line feed at the bottom row
// scrolls whole rows again, wherever the cursor is.
func TestResettingDECLRMMResetsTheMargins(t *testing.T) {
	e := newTestTerminal(t, 6, 2)
	_, _ = e.WriteString("abcdef\r\nghijkl\x1b[?69h\x1b[2;3s\x1b[?69l\x1b[2;1H\n")
	for y, want := range []string{"ghijkl", ""} {
		if got := rowText(e, y); got != want {
			t.Errorf("row %d: got = %q, want = %q", y, got, want)
		}
	}
	if r := e.scr.ScrollRegion(); r.Min.X != 0 || r.Max.X != 6 {
		t.Errorf("region: got = %v, want columns 0 to 6", r)
	}
}
