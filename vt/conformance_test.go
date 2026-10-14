package vt

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
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

// xterm resets the pending wrap (ResetWrap) in util.c's ClearInLine2, which
// EL 1, EL 2 and ED 1 reach through ClearInLine, ClearRight (EL 0 and ED 0)
// and ClearScreen (ED 2), and in InsertChar and DeleteChar once the cursor
// is inside the left and right margins. cursor.c's RevIndex ends in
// CursorUp, which resets it too. So the "y" after each lands on the row the
// x's filled, not the next.
func TestErasesAndShiftsClearThePendingWrap(t *testing.T) {
	for _, op := range []struct{ name, seq string }{
		{"EL 1", "\x1b[1K"},
		{"EL 2", "\x1b[2K"},
		{"ED 0", "\x1b[J"},
		{"ED 1", "\x1b[1J"},
		{"ED 2", "\x1b[2J"},
		{"ICH", "\x1b[@"},
		{"DCH", "\x1b[P"},
	} {
		t.Run(op.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 3)
			_, _ = e.WriteString(strings.Repeat("x", 10) + op.seq)
			if e.PendingWrap() {
				t.Errorf("wrap still pending after %s", op.name)
			}
			_, _ = e.WriteString("y")
			if c := e.CellAt(9, 0); c == nil || c.Content != "y" {
				t.Errorf("cell (9,0): got = %+v, want the y", c)
			}
			if got := rowText(e, 1); got != "" {
				t.Errorf("row 1: got = %q, want it blank", got)
			}
		})
	}

	t.Run("RI", func(t *testing.T) {
		e := newTestTerminal(t, 10, 3)
		_, _ = e.WriteString("\x1b[2;1H" + strings.Repeat("x", 10) + "\x1bMy")
		for y, want := range []string{"         y", strings.Repeat("x", 10), ""} {
			if got := rowText(e, y); got != want {
				t.Errorf("row %d: got = %q, want = %q", y, got, want)
			}
		}
	})

	// InsertChar and DeleteChar return before ResetWrap when the cursor is
	// outside the left and right margins, so there the wrap stays, and the
	// "y" goes to the next row.
	for _, op := range []struct{ name, seq string }{
		{"ICH outside the margins", "\x1b[@"},
		{"DCH outside the margins", "\x1b[P"},
	} {
		t.Run(op.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 3)
			_, _ = e.WriteString("\x1b[?69h\x1b[1;5s\x1b[1;10Hx" + op.seq)
			if !e.PendingWrap() {
				t.Errorf("no wrap pending after %s", op.name)
			}
			_, _ = e.WriteString("y")
			for y, want := range []string{"         x", "y", ""} {
				if got := rowText(e, y); got != want {
					t.Errorf("row %d: got = %q, want = %q", y, got, want)
				}
			}
		})
	}
}

// charproc.c's CASE_CBT moves with tabs.c's TabToPrevStop, which sets only
// the column (set_cur_col) and leaves do_wrap, so the "y" after it wraps.
func TestCBTKeepsThePendingWrap(t *testing.T) {
	e := newTestTerminal(t, 20, 2)
	_, _ = e.WriteString(strings.Repeat("x", 20) + "\x1b[Z")
	if !e.PendingWrap() {
		t.Error("no wrap pending after CBT")
	}
	if got, want := e.CursorPosition(), uv.Pos(16, 0); got != want {
		t.Errorf("cursor: got = %v, want = %v", got, want)
	}
	_, _ = e.WriteString("y")
	for y, want := range []string{strings.Repeat("x", 20), "y"} {
		if got := rowText(e, y); got != want {
			t.Errorf("row %d: got = %q, want = %q", y, got, want)
		}
	}
}

// util.c's ClearAbove, ED 1, clears the rows above the cursor and then, with
// ClearLeft, the cursor's row from column 0 up to and including the
// cursor's cell.
func TestED1ErasesToTheCursor(t *testing.T) {
	e := newTestTerminal(t, 10, 3)
	_, _ = e.WriteString(strings.Repeat("x", 30) + "\x1b[2;5H\x1b[1J")
	for y, want := range []string{"", "     xxxxx", strings.Repeat("x", 10)} {
		if got := rowText(e, y); got != want {
			t.Errorf("row %d: got = %q, want = %q", y, got, want)
		}
	}
}

// cursor.c's CarriageReturn sets only the column (set_cur_col): under ORIGIN
// the left margin, the screen's first column here. CursorNextLine and
// CursorPrevLine move down or up and then call it, so they too end on the
// row they moved to. The row is the screen's, not one relative to the top
// margin.
func TestCarriageReturnUnderOriginModeKeepsTheRow(t *testing.T) {
	// The region is rows 4 to 19, and CUP 3;4 under origin mode puts the
	// cursor at column 3 of row 6.
	const prep = "\x1b[5;20r\x1b[?6h\x1b[3;4H"
	for _, tc := range []struct {
		name, seq string
		want      uv.Position
	}{
		{"CR", "\r", uv.Pos(0, 6)},
		{"CNL", "\r\x1b[E", uv.Pos(0, 7)},
		{"CPL", "\r\x1b[F", uv.Pos(0, 5)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 24)
			_, _ = e.WriteString(prep + tc.seq)
			if got := e.CursorPosition(); got != tc.want {
				t.Errorf("cursor: got = %v, want = %v", got, tc.want)
			}
		})
	}
}

// charproc.c's dotext sets need_wrap when the text fills the rest of the line
// (:7157-7158), whatever its width, and keeps it in do_wrap (:7173) whatever
// WRAPAROUND is. The next print takes the flag (:7065-7066) and wraps only if
// WRAPAROUND is set by then (:7067-7068): without it, the "y" overwrites the
// last column, and fills it, so the wrap is pending again; with ?7h sent
// between, the "y" goes to the next row.
func TestAFullRowLeavesAWrapPendingUnderAutowrapOff(t *testing.T) {
	for _, tc := range []struct {
		name, row string
		// The row once the "y" overwrites column 9, which blanks the rest
		// of a wide character it lands on.
		overwritten string
	}{
		{"narrow characters", strings.Repeat("x", 10), strings.Repeat("x", 9) + "y"},
		{"a wide character ending on the last column", strings.Repeat("x", 8) + "日", strings.Repeat("x", 8) + " y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 2)
			_, _ = e.WriteString("\x1b[?7l" + tc.row)
			if !e.PendingWrap() {
				t.Error("no wrap pending after the row filled under ?7l")
			}
			_, _ = e.WriteString("y")
			for y, want := range []string{tc.overwritten, ""} {
				if got := rowText(e, y); got != want {
					t.Errorf("row %d: got = %q, want = %q", y, got, want)
				}
			}
			if !e.PendingWrap() {
				t.Error("no wrap pending after the y filled the last column")
			}
		})

		t.Run(tc.name+", then ?7h", func(t *testing.T) {
			e := newTestTerminal(t, 10, 2)
			_, _ = e.WriteString("\x1b[?7l" + tc.row + "\x1b[?7hy")
			for y, want := range []string{tc.row, "y"} {
				if got := rowText(e, y); got != want {
					t.Errorf("row %d: got = %q, want = %q", y, got, want)
				}
			}
			if got, want := e.CursorPosition(), uv.Pos(1, 1); got != want {
				t.Errorf("cursor: got = %v, want = %v", got, want)
			}
		})
	}
}
