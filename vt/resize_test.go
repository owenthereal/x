package vt

import (
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// sixRows fills a 6-row screen with "r0" to "r5", leaving the cursor after
// "r5" on the last row.
const sixRows = "r0\r\nr1\r\nr2\r\nr3\r\nr4\r\nr5"

// lineTexts returns the text of each line.
func lineTexts(lines []uv.Line) []string {
	texts := make([]string, len(lines))
	for i, l := range lines {
		texts[i] = l.String()
	}
	return texts
}

// A height shrink with the cursor on the last row moves rows from the top
// into history until the cursor's row is the last, so the cursor keeps its
// row's content, as xterm's ScreenResize does with its default SouthWest
// gravity (screen.c, saveEditBufLines before Reallocate).
func TestHeightShrinkPushesTopRowsIntoHistory(t *testing.T) {
	e := NewEmulator(10, 6)
	e.WriteString(sixRows)
	e.Resize(10, 3)

	if got, want := lineTexts(e.Scrollback().Lines()), []string{"r0", "r1", "r2"}; !slices.Equal(got, want) {
		t.Errorf("history: got = %q, want = %q", got, want)
	}
	if got, want := screenRows(e, 10), []string{"r3", "r4", "r5"}; !slices.Equal(got, want) {
		t.Errorf("screen: got = %q, want = %q", got, want)
	}
	if got, want := e.CursorPosition(), uv.Pos(2, 2); got != want {
		t.Errorf("cursor: got = %v, want = %v, after r5", got, want)
	}
}

// A height shrink drops the rows below the cursor before it takes any from
// the top: here the cursor fits, so nothing goes to history.
func TestHeightShrinkDropsBlankRowsBelowTheCursorFirst(t *testing.T) {
	e := NewEmulator(10, 6)
	e.WriteString("r0\r\nr1")
	e.Resize(10, 3)

	if n := e.ScrollbackLen(); n != 0 {
		t.Errorf("history: got = %q, want none", lineTexts(e.Scrollback().Lines()))
	}
	if got, want := screenRows(e, 10), []string{"r0", "r1", ""}; !slices.Equal(got, want) {
		t.Errorf("screen: got = %q, want = %q", got, want)
	}
	if got, want := e.CursorPosition(), uv.Pos(2, 1); got != want {
		t.Errorf("cursor: got = %v, want = %v, after r1", got, want)
	}
}

// The rows a height shrink moves into history reach the scroll-off function,
// as rows scrolled off the top do.
func TestHeightShrinkFeedsTheScrollOffFunction(t *testing.T) {
	e := NewEmulator(10, 6)
	var off []string
	e.SetScrollOffFunc(func(l uv.Line) { off = append(off, l.String()) })
	e.WriteString(sixRows)
	e.Resize(10, 3)

	if want := []string{"r0", "r1", "r2"}; !slices.Equal(off, want) {
		t.Errorf("scroll-off function: got = %q, want = %q", off, want)
	}
}

// The alternate screen has no history: a height shrink drops its bottom rows
// and clamps its cursor.
func TestAltScreenHeightShrinkDropsBottomRows(t *testing.T) {
	e := NewEmulator(10, 6)
	var off []string
	e.SetScrollOffFunc(func(l uv.Line) { off = append(off, l.String()) })
	e.WriteString("\x1b[?1049h" + sixRows)
	e.Resize(10, 3)

	if got, want := screenRows(e, 10), []string{"r0", "r1", "r2"}; !slices.Equal(got, want) {
		t.Errorf("alternate screen: got = %q, want = %q", got, want)
	}
	if got, want := e.CursorPosition(), uv.Pos(2, 2); got != want {
		t.Errorf("cursor: got = %v, want = %v, clamped", got, want)
	}
	if n := e.ScrollbackLen(); n != 0 || len(off) != 0 {
		t.Errorf("history: got = %q and scroll-off %q, want none", lineTexts(e.Scrollback().Lines()), off)
	}
}

// A height shrink only clamps the DECSC slot; it does not move it with the
// rows that went into history. xterm moves a saved cursor only for the main
// screen while the alternate one shows, and only by the rows a growth pulls
// back from history (AdjustSavedCursor, cursor.c, called from ScreenResize);
// a growth here pulls none back.
func TestHeightShrinkClampsTheSavedCursor(t *testing.T) {
	e := NewEmulator(10, 6)
	e.WriteString("r0\r\nr1\r\nr2\r\nr3\r\nr4\x1b7\r\nr5")
	e.Resize(10, 3)
	e.WriteString("\x1b8")

	if got, want := e.CursorPosition(), uv.Pos(2, 2); got != want {
		t.Errorf("cursor after DECRC: got = %v, want = %v, the slot's row 4 clamped", got, want)
	}
}

// A width change truncates each row (D5) and a growth adds blank cells, not
// zero ones, which would read as the tails of wide characters.
func TestWidthChangeTruncatesAndPadsWithBlanks(t *testing.T) {
	e := NewEmulator(6, 2)
	e.WriteString("abcdef\r\nghijkl")
	e.Resize(3, 2)
	e.Resize(5, 3)

	for y, want := range []string{"abc", "ghi", ""} {
		for x := range 5 {
			c := e.CellAt(x, y)
			switch {
			case x < len(want) && c.Content != want[x:x+1]:
				t.Errorf("cell (%d,%d): got = %q, want = %q", x, y, c.Content, want[x:x+1])
			case x >= len(want) && !c.Equal(&uv.EmptyCell):
				t.Errorf("cell (%d,%d): got = %+v, want a blank", x, y, *c)
			}
		}
	}
}

// A resize leaves a pending wrap pending, on the active screen and on the
// main screen kept behind the alternate one: xterm's ScreenResize does not
// touch do_wrap.
func TestResizeKeepsThePendingWrap(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"main screen", "", ""},
		{"behind ?1049", "\x1b[?1049h", "\x1b[?1049l"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(10, 2)
			e.WriteString(strings.Repeat("x", 10) + tc.before)
			e.Resize(10, 4)
			if !e.MainPendingWrap() {
				t.Error("MainPendingWrap after the resize: got = false, want = true")
			}
			e.WriteString(tc.after)
			if !e.PendingWrap() {
				t.Errorf("PendingWrap: got = false, want = true (cursor %v)", e.CursorPosition())
			}
		})
	}
}

// A widening resize keeps the pending wrap too, so the next character goes
// to the next row, as in xterm, whose wrap does not depend on the column.
// A ?1049 trip around the resize changes nothing.
func TestWideningResizeKeepsThePendingWrap(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"main screen", "", ""},
		{"behind ?1049", "\x1b[?1049h", "\x1b[?1049l"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(10, 2)
			e.WriteString(strings.Repeat("x", 10) + tc.before)
			e.Resize(20, 2)
			e.WriteString(tc.after + "y")

			if got, want := screenRows(e, 20), []string{strings.Repeat("x", 10), "y"}; !slices.Equal(got, want) {
				t.Errorf("screen: got = %q, want = %q", got, want)
			}
			if got, want := e.CursorPosition(), uv.Pos(1, 1); got != want {
				t.Errorf("cursor: got = %v, want = %v", got, want)
			}
		})
	}
}

// A resize resets origin mode, as xterm's ScreenResize clears ORIGIN after it
// resets the margins, without homing the cursor.
func TestResizeResetsOriginMode(t *testing.T) {
	e := NewEmulator(10, 6)
	var disabled []ansi.Mode
	e.SetCallbacks(Callbacks{DisableMode: func(m ansi.Mode) { disabled = append(disabled, m) }})
	e.WriteString("\x1b[2;5r\x1b[?6h\x1b[2;3H")
	pos := e.CursorPosition()
	disabled = nil
	e.Resize(12, 6)

	if got := e.Mode(ansi.ModeOrigin); got != ansi.ModeReset {
		t.Errorf("DECOM: got = %v, want = %v", got, ansi.ModeReset)
	}
	if want := []ansi.Mode{ansi.ModeOrigin}; !slices.Equal(disabled, want) {
		t.Errorf("DisableMode calls: got = %v, want = %v", disabled, want)
	}
	if got := e.CursorPosition(); got != pos {
		t.Errorf("cursor: got = %v, want = %v, where it was", got, pos)
	}
}

// A shrink copies what survives into rows of exactly the new width, so no
// row shares an old row's storage or keeps its capacity, and the row list
// keeps no old rows past the new height: nothing of the old buffer stays
// reachable from either screen.
func TestAShrinkKeepsNoOldCapacity(t *testing.T) {
	e := NewEmulator(200, 50)
	screens := []*Screen{e.MainScreen(), e.AltScreen()}

	// The old rows, with their full capacity, and a copy of their cells.
	old := make([][]uv.Line, len(screens))
	saved := make([][]uv.Line, len(screens))
	for i, s := range screens {
		for _, row := range s.buf.Lines[:cap(s.buf.Lines)] {
			old[i] = append(old[i], row[:cap(row)])
			saved[i] = append(saved[i], slices.Clone(row[:cap(row)]))
		}
	}

	e.Resize(80, 24)

	marker := uv.Cell{Content: "#", Width: 1}
	for i, s := range screens {
		if got := cap(s.buf.Lines); got != 24 {
			t.Errorf("screen %d: row list capacity: got = %d, want = 24", i, got)
		}
		for y, row := range s.buf.Lines {
			if got := cap(row); got != 80 {
				t.Errorf("screen %d, row %d: capacity: got = %d, want = 80", i, y, got)
				break
			}
		}
		for _, row := range s.buf.Lines {
			// Write over the row's whole capacity: an old row that shares
			// its storage changes.
			row = row[:cap(row)]
			for x := range row {
				row[x] = marker
			}
		}
		if !sameLines(old[i], saved[i]) {
			t.Errorf("screen %d: writing to the resized rows changed the old ones", i)
		}
	}
}

// A resize from 2 columns by 65535 rows to 65535 by 2, valid at both ends,
// allocates the final geometry once: two screens of 131,070 cells.
// ultraviolet's Buffer.Resize widened every row before it cut the height,
// which needs about four billion cells. The 2x1024 case runs first and a
// failure there stops the test, so a regression shows at a size that fits
// in memory before the large one can run.
func TestACrossedAspectResizeAllocatesTheFinalGeometryOnce(t *testing.T) {
	cellSize := int(reflect.TypeFor[uv.Cell]().Size())
	for _, n := range []int{1024, 65535} {
		ok := t.Run(fmt.Sprintf("2x%d", n), func(t *testing.T) {
			e := NewEmulator(2, n)

			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			e.Resize(n, 2)
			runtime.ReadMemStats(&after)

			// Two screens of 2 rows of n cells, and a cell to spare per
			// row: 2 x 131,072 cells at n = 65535.
			limit := 2*2*(n+1)*cellSize + 1<<20
			if got := after.TotalAlloc - before.TotalAlloc; got >= uint64(limit) {
				t.Errorf("allocated: got = %d bytes, want under %d", got, limit)
			}
			for i, s := range []*Screen{e.MainScreen(), e.AltScreen()} {
				if got := len(s.buf.Lines); got != 2 {
					t.Errorf("screen %d: rows: got = %d, want = 2", i, got)
				}
				for y, row := range s.buf.Lines {
					if len(row) != n || cap(row) != n {
						t.Errorf("screen %d, row %d: len %d cap %d, want both %d", i, y, len(row), cap(row), n)
						break
					}
				}
			}
		})
		if !ok {
			return
		}
	}
}
