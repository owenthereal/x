package vt

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// rowCells returns each cell of row y of e's screen as lineCells does.
func rowCells(e *Emulator, y int) []string {
	return lineCells(e.scr.buf.Line(y))
}

// lineCells returns each cell of line as its content, followed by its width
// when that isn't 1: "日/2", then its placeholder, "/0".
func lineCells(line uv.Line) []string {
	cells := make([]string, len(line))
	for x, c := range line {
		cells[x] = c.Content
		if c.Width != 1 {
			cells[x] += "/" + strconv.Itoa(c.Width)
		}
	}
	return cells
}

// checkCells reports how row y of e differs from want, cell for cell.
func checkCells(t *testing.T, e *Emulator, y int, want ...string) {
	t.Helper()
	if got := rowCells(e, y); !slices.Equal(got, want) {
		t.Errorf("row %d cells:\n got = %q\nwant = %q", y, got, want)
	}
}

// xterm 412's DeleteChar (util.c:1987) and ScrnDeleteChar (screen.c:1391)
// blank a wide character the deleted cells cut: DamagedCells (util.c:172)
// finds it, at the cursor or where the deleted cells end, and ClearCells
// (screen.c:839) blanks it. Then MemMove moves the rest of the row left as it
// is, so the wide characters it moves stay whole.
func TestDeleteCharKeepsWideCharacters(t *testing.T) {
	e := NewEmulator(10, 1)
	e.WriteString("a日b本c\x1b[1;1H\x1b[P")
	checkCells(t, e, 0, "日/2", "/0", "b", "本/2", "/0", "c", " ", " ", " ", " ")
	if got := rowText(e, 0); got != "日b本c" {
		t.Errorf("row: got = %q, want = %q", got, "日b本c")
	}

	for _, tc := range []struct {
		name, in string
		want     []string
	}{
		// The cursor on the second half of 日: DamagedCells scans back to
		// its first, and ClearCells blanks both.
		{"from inside a wide character", "a日b本c\x1b[1;3H\x1b[P", []string{"a", " ", "b", "本/2", "/0", "c", " ", " ", " ", " "}},
		// The deleted cells end on the first half of 日: the second half,
		// which would move to column 0, is blanked with it.
		{"ending inside a wide character", "a日b本c\x1b[1;1H\x1b[2P", []string{" ", "b", "本/2", "/0", "c", " ", " ", " ", " ", " "}},
		// The first cell the delete opens, column 8, still holds the
		// second half of 日, which moved on, when ScrnDeleteChar's last
		// ClearCells (screen.c:1442) blanks the cells opened. ClearCells
		// takes it for a cut wide character and blanks column 7 too, where
		// the h moved, though xterm's screen, which horizontal_copy_area
		// moved, shows the h. vt keeps it.
		{"next to the cells opened", "abcdefg日h\x1b[1;1H\x1b[2P", []string{"c", "d", "e", "f", "g", "日/2", "/0", "h", " ", " "}},
		// With a right margin through 日, the margin cuts it. That last
		// ClearCells blanks its second half, outside the margins, and
		// leaves its first, moved to column 2, without it; vt blanks both.
		{"across the right margin", "abc日\x1b[?69h\x1b[1;4s\x1b[1;1H\x1b[P", []string{"b", "c", " ", " ", " ", " ", " ", " ", " ", " "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(10, 1)
			e.WriteString(tc.in)
			checkCells(t, e, 0, tc.want...)
		})
	}
}

// InsertChar repairs "the text which will be shifted into the
// right-margin" (util.c:1936-1952), as ScrnInsertChar does at the right
// margin (screen.c:1360-1364): a wide character the cells pushed past the
// margin cut is blanked, so no first half is left in the last column without
// room for its second. Under IRM, WriteText makes room through InsertChar
// (util.c:1550).
func TestInsertBlanksAWideCharacterCutByTheMargin(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		want     []string
	}{
		{"IRM", "\x1b[4hX", []string{"X", "a", "b", "c", "d", " "}},
		{"ICH", "\x1b[@", []string{" ", "a", "b", "c", "d", " "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(6, 1)
			e.WriteString("abcd日\x1b[1;1H" + tc.op)
			checkCells(t, e, 0, tc.want...)
		})
	}

	// With a right margin through 日, the margin itself cuts it: its first
	// half is pushed out, and its second, outside the margins, stays. xterm
	// leaves that second half without its first: ScrnInsertChar looks at the
	// cell pushed out, a first half, and InsertChar at the screen's last
	// columns. vt blanks it, so the row stays whole.
	t.Run("across the right margin", func(t *testing.T) {
		e := NewEmulator(10, 1)
		e.WriteString("abcd日\x1b[?69h\x1b[1;5s\x1b[1;1H\x1b[@")
		checkCells(t, e, 0, " ", "a", "b", "c", "d", " ", " ", " ", " ", " ")
	})
}

// InsertChar's first repair (util.c:1942-1946) and ScrnInsertChar's
// (screen.c:1357-1359): an insert at the second half of a wide character
// blanks the whole of it before the cells move, so neither half is left
// apart from the other.
func TestInsertInsideAWideCharacterBlanksIt(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		want     []string
	}{
		{"IRM", "\x1b[4hX", []string{"a", " ", "X", " ", "b", "c"}},
		{"ICH", "\x1b[@", []string{"a", " ", " ", " ", "b", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(6, 1)
			e.WriteString("a日bc\x1b[1;3H" + tc.op)
			checkCells(t, e, 0, tc.want...)
		})
	}
}

// WriteText (util.c:1564-1572) blanks the rest of a wide character the text
// writes over part of, on either side: DamagedCurCells finds it, and
// ClearInLine blanks it, with the pen's colours, as ClearCells blanks. Here
// 本's second half lands on 日's first, so 日's second half is blanked.
func TestOverwritingAWideHeadBlanksItsTail(t *testing.T) {
	e := NewEmulator(4, 1)
	e.WriteString("\x1b[1;2H日\x1b[1;1H本\x1b[1;4HX")
	checkCells(t, e, 0, "本/2", "/0", " ", "X")
	if got := e.String(); got != "本 X" {
		t.Errorf("String: got = %q, want = %q", got, "本 X")
	}

	// X over 日's second half blanks its first, which takes the background
	// X is printed with, as ClearCells colours the cells it blanks.
	t.Run("the other side", func(t *testing.T) {
		e := NewEmulator(4, 1)
		e.WriteString("\x1b[41m\x1b[1;2H日\x1b[44m\x1b[1;3HX")
		checkCells(t, e, 0, " ", " ", "X", " ")
		if c := e.CellAt(1, 0); c == nil || c.Style.Bg != ansi.BasicColor(4) {
			t.Errorf("cell (1, 0): got = %+v, want a blank on blue", c)
		}
	})
}

// InsertChar and DeleteChar check only that the cursor is inside the left
// and right margins (ScrnIsColInMargins, util.c:1898 and :1998), and
// ScrnInsertChar and ScrnDeleteChar shift the cursor's row whatever it is:
// the top and bottom margins play no part.
func TestShiftsIgnoreTheVerticalMargins(t *testing.T) {
	for _, tc := range []struct {
		name, op, want string
	}{
		{"IRM", "\x1b[4;2H\x1b[4hX", "aXbcde"},
		{"ICH", "\x1b[4;2H\x1b[@", "a bcde"},
		{"DCH", "\x1b[4;2H\x1b[P", "acdef"},
		// With left and right margins at columns 2 to 4, the shift stays
		// inside them, and outside them nothing shifts, or is blanked.
		{"ICH inside the left and right margins", "\x1b[?69h\x1b[2;4s\x1b[4;2H\x1b[@", "a bcef"},
		{"DCH inside the left and right margins", "\x1b[?69h\x1b[2;4s\x1b[4;2H\x1b[P", "acd ef"},
		{"ICH right of the right margin", "\x1b[4;5H日\x1b[?69h\x1b[2;4s\x1b[4;6H\x1b[@", "abcd日"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(6, 4)
			e.WriteString("\x1b[4;1Habcdef\x1b[1;2r" + tc.op)
			if got := rowText(e, 3); got != tc.want {
				t.Errorf("row 4: got = %q, want = %q", got, tc.want)
			}
		})
	}
}

// The carried cluster's undo puts back what its print displaced, so a cluster
// split across two Writes ends as it does in one. Under IRM the undo is a
// delete, which moves back whole the wide characters the insert pushed
// along; at the last column it puts back the cells the print wrote over, the
// first half of a wide character among them.
func TestTheCarriedUndoRestoresWideCharacters(t *testing.T) {
	const family1, family2 = "\U0001F468\u200d", "\U0001F469"
	for _, tc := range []struct {
		name   string
		w      int
		prep   string
		chunks []string
		want   wideState
	}{
		{"wide characters pushed along", 8, "日本xy\x1b[1;1H\x1b[4h", []string{family1, family2}, wideState{[]string{family1 + family2 + "日本xy", ""}, uv.Pos(2, 0), false}},
		{"a wide character cut at the margin", 7, "ab日本\x1b[1;1H\x1b[4h", []string{family1, family2}, wideState{[]string{family1 + family2 + "ab日", ""}, uv.Pos(2, 0), false}},
		// The heart alone is one column wide, and VS16 makes it two.
		{"inserted inside a wide character", 6, "a日b\x1b[1;3H\x1b[4h", []string{"\u2764", "\ufe0f"}, wideState{[]string{"a \u2764\ufe0f b", ""}, uv.Pos(4, 0), false}},
		{"at the last column, over a wide character", 5, "abc日\x1b[1;5H\x1b[4h", []string{"\u2764", "\ufe0f"}, wideState{[]string{"abc日", "\u2764\ufe0f"}, uv.Pos(2, 1), false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			whole, wholeState := writeWide(tc.w, 2, tc.prep, strings.Join(tc.chunks, ""))
			wholeState.check(t, tc.want)
			split, splitState := writeWide(tc.w, 2, append([]string{tc.prep}, tc.chunks...)...)
			splitState.check(t, tc.want)
			if diff := splitDiff(whole, split); diff != "" {
				t.Error(diff)
			}
		})
	}
}

// Found in F5's review: ultraviolet's Line.Set scans left from a placeholder
// target, so on the base a DCH at column 0 of a 131,072x1 row of 日 is
// quadratic (5.4 s), and leaves an orphaned placeholder at every other column
// for the next erase to scan from. With vt's own raw moves that DCH, and an
// EL 2 over the row it leaves, each complete in under 100 ms.
func TestShiftsOverWideRowsAreLinear(t *testing.T) {
	const w = 131072
	e := NewEmulator(w, 1)
	e.WriteString(strings.Repeat("日", w/2))
	for _, op := range []struct {
		name, seq string
		check     func()
	}{
		// The DCH cuts the first 日, whose second half is left blank at
		// column 0; the rest move left whole.
		{"DCH at column 0", "\x1b[1;1H\x1b[P", func() {
			cells := rowCells(e, 0)
			got := []string{cells[0], cells[1], cells[w-2], cells[w-1]}
			if want := []string{" ", "日/2", "/0", " "}; !slices.Equal(got, want) {
				t.Errorf("columns 0, 1, %d and %d: got = %q, want = %q", w-2, w-1, got, want)
			}
		}},
		{"EL 2 over the row DCH left", "\x1b[2K", func() {
			if got := cellsHolding(e, " "); got != w {
				t.Errorf("blank cells: got = %d, want = %d", got, w)
			}
		}},
	} {
		start := time.Now()
		e.WriteString(op.seq)
		elapsed := time.Since(start)
		t.Logf("%s: %v", op.name, elapsed)
		if elapsed > 100*time.Millisecond {
			t.Errorf("%s: took %v, want under 100ms", op.name, elapsed)
		}
		op.check()
	}
}

// shiftModel is what insertCells (insert) and deleteCells do to row, written
// with nothing metered: inside the margins, blank each wide character the
// shift cuts, at the cursor, where the cells that move end or start, and at
// the right margin, then move the cells and fill the ones opened with blank.
func shiftModel(row uv.Line, x, n, left, right int, insert bool, blank uv.Cell) uv.Line {
	out := slices.Clone(row)
	right = min(right, len(row))
	if n <= 0 || x < left || x >= right {
		return out
	}
	n = min(n, right-x)
	cuts := []int{x, x + n, right}
	if insert {
		cuts[1] = right - n
	}
	for _, b := range cuts {
		if b <= 0 || b >= len(out) || out[b].Width != 0 {
			continue
		}
		lo, hi := b-1, b+1
		for lo > 0 && out[lo].Width == 0 {
			lo--
		}
		for hi < len(out) && out[hi].Width == 0 {
			hi++
		}
		for i := lo; i < hi; i++ {
			out[i] = blank
		}
	}
	src := slices.Clone(out)
	for i := x; i < right; i++ {
		switch {
		case insert && i < x+n, !insert && i >= right-n:
			out[i] = blank
		case insert:
			out[i] = src[i-n]
		default:
			out[i] = src[i+n]
		}
	}
	return out
}

// wellFormed reports where row first breaks the shape of wide characters: a
// placeholder that follows no wide character, or a wide character without
// all of its placeholders. It returns -1 for a row that keeps it.
func wellFormed(row uv.Line) int {
	for x := 0; x < len(row); {
		w := row[x].Width
		if w == 0 {
			return x
		}
		for i := 1; i < w; i++ {
			if x+i >= len(row) || row[x+i].Width != 0 {
				return x
			}
		}
		x += max(w, 1)
	}
	return -1
}

// insertCells and deleteCells do what shiftModel does, whatever spans the
// meter cuts them into, on rows of narrow and wide characters, with margins
// and without, with a pen's background and without, and change no other row.
// They leave every row whole, and mark touched every cell they change, as the
// cell-copy shifts before them did, and no other row.
func TestRowShiftsMatchTheirModel(t *testing.T) {
	lowerMeterInterval(t, 3)
	const w, h = 12, 3
	rnd := rand.New(rand.NewPCG(3, 4))
	for i := range 4000 {
		s := NewScreen(w, h)
		for y := range h {
			for x := 0; x < w; {
				c := uv.Cell{Content: string(rune('a' + rnd.IntN(26))), Width: 1}
				c.Style.Fg = ansi.BasicColor(rnd.IntN(8))
				if x+1 < w && rnd.IntN(3) == 0 {
					c.Content, c.Width = "日", 2
				}
				s.buf.SetCell(x, y, &c)
				x += c.Width
			}
		}
		left, right := 0, w
		if i%2 == 1 {
			left = rnd.IntN(w)
			right = left + 1 + rnd.IntN(w-left)
		}
		s.setHorizontalMargins(left, right)
		blank := uv.EmptyCell
		if rnd.IntN(2) == 0 {
			s.cur.Pen.Bg = ansi.BasicColor(4)
			blank.Style.Bg = s.cur.Pen.Bg
		}
		x, y, n := rnd.IntN(w+2)-1, rnd.IntN(h), rnd.IntN(w+2)-1
		insert := i%4 < 2
		before := make([]uv.Line, h)
		for r := range h {
			before[r] = slices.Clone(s.buf.Line(r))
		}
		s.buf.Touched = make([]*uv.LineData, h)
		op := fmt.Sprintf("deleteCells(%d, %d, %d) in columns [%d, %d)", x, y, n, left, right)
		if insert {
			op = "insert" + op[len("delete"):]
			s.insertCells(x, y, n)
		} else {
			s.deleteCells(x, y, n)
		}
		for r := range h {
			expected := before[r]
			if r == y {
				expected = shiftModel(before[r], x, n, left, right, insert, blank)
			}
			got := s.buf.Line(r)
			if !slices.Equal(got, expected) {
				t.Fatalf("%s, row %d:\n  was = %q\n  got = %q\n want = %q", op, r, before[r], got, expected)
			}
			if at := wellFormed(got); at >= 0 {
				t.Fatalf("%s, row %d: broken wide character at column %d: %q", op, r, at, got)
			}
			// A renderer repaints only the rows marked touched: every cell
			// the shift changed is inside its row's mark, and the rows it
			// didn't shift have none.
			var touched *uv.LineData
			if r < len(s.buf.Touched) {
				touched = s.buf.Touched[r]
			}
			if r != y {
				if touched != nil {
					t.Fatalf("%s: row %d, which the shift doesn't change, is touched %d-%d", op, r, touched.FirstCell, touched.LastCell)
				}
				continue
			}
			for c := range got {
				if got[c] != before[r][c] && (touched == nil || c < touched.FirstCell || c >= touched.LastCell) {
					t.Fatalf("%s: row %d's column %d changed, outside its touched mark %v", op, r, c, touchedText(s.buf.Touched))
				}
			}
		}
	}
}
