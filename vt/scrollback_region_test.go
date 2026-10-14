package vt

import (
	"fmt"
	"slices"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// regionTestRows are the rows of the screen the region tests start from, on a
// 10-column, 6-row emulator.
var regionTestRows = []string{"r1", "r2", "r3", "r4", "r5", "r6"}

// runRegionTest writes regionTestRows to a new emulator, then seq, and
// returns the lines the scrollback and the scroll-off function received, and
// the text of the screen's rows afterwards.
func runRegionTest(seq string) (scrollback, scrolledOff, rows []string) {
	e := NewEmulator(10, len(regionTestRows))
	e.SetScrollOffFunc(func(l uv.Line) { scrolledOff = append(scrolledOff, l.String()) })
	for r, row := range regionTestRows {
		e.WriteString(fmt.Sprintf("\x1b[%d;1H%s", r+1, row))
	}
	e.WriteString(seq)
	for _, l := range e.Scrollback().Lines() {
		scrollback = append(scrollback, l.String())
	}
	return scrollback, scrolledOff, screenRows(e, 10)
}

// A row scrolled out of a region whose top margin is below the first row is
// discarded, as in xterm: xtermScroll (util.c) saves the lines only when
// screen->top_marg == 0, as does DeleteLine (util.c) for DL with the cursor
// in row 0, and scrollInMargins saves none. A progress bar kept in such a
// region would otherwise fill the history with lines a terminal never saved.
func TestARegionNotAtTheTopFeedsNoHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
		want []string
	}{
		{
			// DECSTBM 3;5, then two line feeds at row 5.
			name: "line feeds at the bottom margin",
			seq:  "\x1b[3;5r\x1b[5;1H\n\n",
			want: []string{"r1", "r2", "r5", "", "", "r6"},
		},
		{
			name: "SU",
			seq:  "\x1b[3;5r\x1b[2S",
			want: []string{"r1", "r2", "r5", "", "", "r6"},
		},
		{
			name: "DL at the top margin",
			seq:  "\x1b[3;5r\x1b[3;1H\x1b[2M",
			want: []string{"r1", "r2", "r5", "", "", "r6"},
		},
		{
			name: "a region that reaches the bottom of the screen",
			seq:  "\x1b[2;6r\x1b[6;1H\n",
			want: []string{"r1", "r3", "r4", "r5", "r6", ""},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scrollback, scrolledOff, rows := runRegionTest(tc.seq)

			// The rows did scroll, so the empty history is not a scroll that
			// never happened.
			if !slices.Equal(rows, tc.want) {
				t.Errorf("screen rows: got = %q, want = %q", rows, tc.want)
			}
			if len(scrollback) != 0 {
				t.Errorf("scrollback: got = %q, want none", scrollback)
			}
			if len(scrolledOff) != 0 {
				t.Errorf("scroll-off function: got = %q, want none", scrolledOff)
			}
		})
	}
}

// A row scrolled out of a full-width region whose top margin is the first row
// goes to history, wherever its bottom margin is, as in xterm: xtermScroll
// (util.c) saves the lines when screen->top_marg == 0 without looking at
// screen->bot_marg.
func TestARegionAtTheTopFeedsHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
	}{
		// DECSTBM 1;5, then two line feeds at row 5.
		{name: "line feeds at the bottom margin", seq: "\x1b[1;5r\x1b[5;1H\n\n"},
		{name: "SU", seq: "\x1b[1;5r\x1b[2S"},
		{name: "DL at the top margin", seq: "\x1b[1;5r\x1b[1;1H\x1b[2M"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scrollback, scrolledOff, rows := runRegionTest(tc.seq)

			want := []string{"r1", "r2"}
			if !slices.Equal(scrollback, want) {
				t.Errorf("scrollback: got = %q, want = %q", scrollback, want)
			}
			if !slices.Equal(scrolledOff, want) {
				t.Errorf("scroll-off function: got = %q, want = %q", scrolledOff, want)
			}
			wantRows := []string{"r3", "r4", "r5", "", "", "r6"}
			if !slices.Equal(rows, wantRows) {
				t.Errorf("screen rows: got = %q, want = %q", rows, wantRows)
			}
		})
	}
}

// A region that starts at the first row feeds no history if left and right
// margins make it narrower than the screen, as in xterm: xtermScroll (util.c)
// scrolls within the margins, with scrollInMargins, unless the region spans
// every column.
func TestANarrowRegionFeedsNoHistory(t *testing.T) {
	// DECLRMM set, DECSTBM 1;5 and DECSLRM 1;2, then two line feeds at row 5.
	scrollback, scrolledOff, rows := runRegionTest("\x1b[?69h\x1b[1;5r\x1b[1;2s\x1b[5;1H\n\n")

	want := []string{"r3", "r4", "r5", "", "", "r6"}
	if !slices.Equal(rows, want) {
		t.Errorf("screen rows: got = %q, want = %q", rows, want)
	}
	if len(scrollback) != 0 {
		t.Errorf("scrollback: got = %q, want none", scrollback)
	}
	if len(scrolledOff) != 0 {
		t.Errorf("scroll-off function: got = %q, want none", scrolledOff)
	}
}
