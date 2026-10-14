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

// aroundTheResize runs a resize on the main screen, and on the main screen
// kept behind the alternate one by a ?1049 trip around it, with the
// alternate screen's cursor moved home, off the row the main screen keeps,
// and by a ?47 trip, which carries the cursor and its wrap back.
var aroundTheResize = []struct{ name, before, after string }{
	{"main screen", "", ""},
	{"behind ?1049", "\x1b[?1049h\x1b[H", "\x1b[?1049l"},
	{"behind ?47", "\x1b[?47h", "\x1b[?47l"},
}

// A height shrink with the cursor on the last row moves rows from the top
// into history until the cursor's row is the last, so the cursor keeps its
// row's content, as xterm's ScreenResize does with its default SouthWest
// gravity (screen.c, saveEditBufLines before Reallocate). The main screen
// does so behind the alternate one too, by its own cursor.
func TestHeightShrinkPushesTopRowsIntoHistory(t *testing.T) {
	for _, tc := range aroundTheResize {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(10, 6)
			e.WriteString(sixRows + tc.before)
			e.Resize(10, 3)
			e.WriteString(tc.after)

			if got, want := lineTexts(e.Scrollback().Lines()), []string{"r0", "r1", "r2"}; !slices.Equal(got, want) {
				t.Errorf("history: got = %q, want = %q", got, want)
			}
			if got, want := screenRows(e, 10), []string{"r3", "r4", "r5"}; !slices.Equal(got, want) {
				t.Errorf("screen: got = %q, want = %q", got, want)
			}
			if got, want := e.CursorPosition(), uv.Pos(2, 2); got != want {
				t.Errorf("cursor: got = %v, want = %v, after r5", got, want)
			}
		})
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
// as rows scrolled off the top do, behind the alternate screen too.
func TestHeightShrinkFeedsTheScrollOffFunction(t *testing.T) {
	for _, tc := range aroundTheResize {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(10, 6)
			var off []string
			e.SetScrollOffFunc(func(l uv.Line) { off = append(off, l.String()) })
			e.WriteString(sixRows + tc.before)
			e.Resize(10, 3)

			if want := []string{"r0", "r1", "r2"}; !slices.Equal(off, want) {
				t.Errorf("scroll-off function: got = %q, want = %q", off, want)
			}
		})
	}
}

// A height shrink drops the rows below the cursor whatever they hold, and
// they never reach history: only the rows above the cursor move, as in
// xterm's ScreenResize and Reallocate (screen.c), which save and move up
// only the rows the cursor's row needs and cut the rest from the bottom, and
// tmux's screen_resize_y, which deletes the lines below the cursor first.
func TestHeightShrinkDropsRowsBelowTheCursorWhateverTheyHold(t *testing.T) {
	e := NewEmulator(10, 6)
	var off []string
	e.SetScrollOffFunc(func(l uv.Line) { off = append(off, l.String()) })
	e.WriteString(sixRows + "\x1b[3;3H")
	e.Resize(10, 3)

	if got, want := screenRows(e, 10), []string{"r0", "r1", "r2"}; !slices.Equal(got, want) {
		t.Errorf("screen: got = %q, want = %q", got, want)
	}
	if got, want := e.CursorPosition(), uv.Pos(2, 2); got != want {
		t.Errorf("cursor: got = %v, want = %v", got, want)
	}
	if e.ScrollbackLen() != 0 || len(off) != 0 {
		t.Errorf("history: got = %q and scroll-off %q, want none", lineTexts(e.Scrollback().Lines()), off)
	}
}

// The alternate screen keeps its cursor's row through a height shrink too,
// as xterm's Reallocate does for whichever buffer shows with its default
// SouthWest gravity, and as tmux's screen_resize_y does for a grid without
// history: the rows below the cursor go first, then rows from the top. Those
// are discarded, since the alternate screen has no history.
func TestAltScreenHeightShrinkKeepsTheCursorLine(t *testing.T) {
	for _, tc := range []struct {
		name, rows string
		screen     []string
		cursor     uv.Position
	}{
		{"cursor on the last row", sixRows, []string{"r3", "r4", "r5"}, uv.Pos(2, 2)},
		{"blank rows below the cursor", "r0\r\nr1", []string{"r0", "r1", ""}, uv.Pos(2, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(10, 6)
			var off []string
			e.SetScrollOffFunc(func(l uv.Line) { off = append(off, l.String()) })
			e.WriteString("\x1b[?1049h" + tc.rows)
			e.Resize(10, 3)

			if got := screenRows(e, 10); !slices.Equal(got, tc.screen) {
				t.Errorf("alternate screen: got = %q, want = %q", got, tc.screen)
			}
			if got := e.CursorPosition(); got != tc.cursor {
				t.Errorf("cursor: got = %v, want = %v", got, tc.cursor)
			}
			if e.ScrollbackLen() != 0 || e.AltScreen().Scrollback().Len() != 0 || len(off) != 0 {
				t.Errorf("history: got = %q, alternate %q, scroll-off %q, want none",
					lineTexts(e.Scrollback().Lines()), lineTexts(e.AltScreen().Scrollback().Lines()), off)
			}
		})
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

// A resize leaves a pending wrap pending, whichever screen shows, and the one
// ?1049h saved in the main screen's slot too: xterm's ScreenResize does not
// touch do_wrap, nor a slot's wrap_flag.
func TestResizeKeepsThePendingWrap(t *testing.T) {
	for _, tc := range aroundTheResize {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(10, 2)
			e.WriteString(strings.Repeat("x", 10) + tc.before)
			e.Resize(10, 4)
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
	for _, tc := range aroundTheResize {
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

// crossedAspectResize resizes a new 2 x n emulator to n x 2. It returns the
// emulator, the bytes the resize allocated, and the most it may: two screens
// of 2 rows of n cells with a cell to spare per row (2 x 131,072 cells at
// n = 65535), and 1 MiB.
func crossedAspectResize(n int) (e *Emulator, allocated, limit uint64) {
	e = NewEmulator(2, n)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	e.Resize(n, 2)
	runtime.ReadMemStats(&after)
	cellSize := uint64(reflect.TypeFor[uv.Cell]().Size())
	return e, after.TotalAlloc - before.TotalAlloc, 2*2*uint64(n+1)*cellSize + 1<<20
}

// A resize from 2 columns by 65535 rows to 65535 by 2, valid at both ends,
// allocates the final geometry once: two screens of 131,070 cells.
// ultraviolet's Buffer.Resize widened every row before it cut the height,
// which needs about four billion cells. So the large subtest first measures
// the 2x1024 resize and stops if that is over its limit: a regression fails
// at a size that fits in memory, whichever subtests -run selects.
func TestACrossedAspectResizeAllocatesTheFinalGeometryOnce(t *testing.T) {
	const small = 1024
	for _, n := range []int{small, 65535} {
		t.Run(fmt.Sprintf("2x%d", n), func(t *testing.T) {
			if n > small {
				if _, got, limit := crossedAspectResize(small); got >= limit {
					t.Fatalf("2x%d: allocated %d bytes, want under %d; not trying 2x%d", small, got, limit, n)
				}
			}
			e, got, limit := crossedAspectResize(n)
			if got >= limit {
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
	}
}

// A resize to or from no rows or no columns has no cursor row to keep: it
// neither fails nor moves anything into history.
func TestResizeToNoRowsOrColumns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		w, h  int
		setup string
		sizes [][2]int
	}{
		{"80x0 to 80x0", 80, 0, "", [][2]int{{80, 0}}},
		{"to 80x0 twice", 80, 6, sixRows, [][2]int{{80, 0}, {80, 0}}},
		{"to 80x0 then 100x0", 80, 6, sixRows, [][2]int{{80, 0}, {100, 0}}},
		{"to 0x3", 80, 6, sixRows, [][2]int{{0, 3}}},
		{"0x6 to 10x3", 0, 6, "\x1b[6;1H", [][2]int{{10, 3}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(tc.w, tc.h)
			var off []string
			e.SetScrollOffFunc(func(l uv.Line) { off = append(off, l.String()) })
			e.WriteString(tc.setup)
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("resize panicked: %v", r)
				}
			}()
			for _, size := range tc.sizes {
				e.Resize(size[0], size[1])
			}
			if e.ScrollbackLen() != 0 || len(off) != 0 {
				t.Errorf("history: got = %q and scroll-off %q, want none", lineTexts(e.Scrollback().Lines()), off)
			}
		})
	}
}
