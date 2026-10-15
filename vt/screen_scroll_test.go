package vt

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/owenthereal/x/ansi"
)

// scrollTestRows is the content the scroll tests start from. The third and
// fifth rows hold wide characters, and fillScrollTestScreen gives every row
// its own foreground, so a row that lands in the wrong place, loses its style
// or splits a wide cell shows.
var scrollTestRows = []string{"abcd", "efgh", "日本", "ijkl", "m日n", "opqr"}

// fillScrollTestScreen writes scrollTestRows to e, row r in foreground 31+r. A
// screen wider than four columns gets the row's number in its fifth column.
func fillScrollTestScreen(e *Emulator) {
	for r, row := range scrollTestRows {
		e.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[%dm%s", r+1, 31+r, row))
		if e.Width() > 4 {
			e.WriteString(fmt.Sprint(r))
		}
	}
	e.WriteString("\x1b[0m")
}

// touchedRows returns, for each of the first h rows, the columns the screen
// marks touched there as "first-last", or "" for a row it doesn't.
func touchedRows(e *Emulator, h int) []string {
	touched := e.Touched()
	rows := make([]string, h)
	for y := range h {
		if y < len(touched) && touched[y] != nil {
			rows[y] = fmt.Sprintf("%d-%d", touched[y].FirstCell, touched[y].LastCell)
		}
	}
	return rows
}

// screenRows returns the text of each row of e's screen, to read a failure by.
func screenRows(e *Emulator, w int) []string {
	rows := make([]string, e.Height())
	for y := range rows {
		rows[y] = e.scr.buf.Line(y)[:w].String()
	}
	return rows
}

// Every (region, y, n, insert/delete) on a 6-row, 4-column screen agrees cell
// for cell with the same operation on a screen one column wider, whose region
// stops a column short and so takes the cell-copy path.
func TestFullWidthLineOpsMatchTheCellCopy(t *testing.T) {
	const w, h = 4, 6

	for _, op := range []struct {
		name  string
		final byte
	}{{"IL", 'L'}, {"DL", 'M'}} {
		for _, pen := range []string{"", "\x1b[44m"} {
			// DECSTBM takes 1-based rows, and its top must lie above its
			// bottom: a region is at least two rows.
			for top := 1; top < h; top++ {
				for bottom := top + 1; bottom <= h; bottom++ {
					// Rows outside the region too: the operation does nothing
					// there.
					for y := 1; y <= h; y++ {
						for n := 1; n <= h+1; n++ {
							name := fmt.Sprintf("%s pen %q region %d-%d row %d n %d", op.name, pen, top, bottom, y, n)

							// The wider screen's region stops a column short,
							// so its line operations copy cells.
							full, short := NewEmulator(w, h), NewEmulator(w+1, h)
							fillScrollTestScreen(full)
							fillScrollTestScreen(short)
							short.WriteString("\x1b[?69h\x1b[1;4s")

							seq := fmt.Sprintf("%s\x1b[%d;%dr\x1b[%d;1H", pen, top, bottom, y)
							full.WriteString(seq)
							short.WriteString(seq)
							full.scr.ClearTouched()
							short.scr.ClearTouched()

							seq = fmt.Sprintf("\x1b[%d%c", n, op.final)
							full.WriteString(seq)
							short.WriteString(seq)

							for r := range h {
								for x := range w {
									if got, want := full.CellAt(x, r), short.CellAt(x, r); !got.Equal(want) {
										t.Fatalf("%s: cell (%d,%d): got = %+v, want = %+v\nfull:  %q\nshort: %q",
											name, x, r, got, want, screenRows(full, w), screenRows(short, w))
									}
								}
								// The column outside the short region never
								// moves, which shows that the comparison took
								// the cell-copy path.
								if got, want := short.CellAt(w, r).Content, fmt.Sprint(r); got != want {
									t.Fatalf("%s: column %d of row %d: got = %q, want = %q", name, w, r, got, want)
								}
							}
							if got, want := full.CursorPosition(), short.CursorPosition(); got != want {
								t.Fatalf("%s: cursor: got = %v, want = %v", name, got, want)
							}
							if got, want := touchedRows(full, h), touchedRows(short, h); !slices.Equal(got, want) {
								t.Fatalf("%s: touched rows: got = %q, want = %q", name, got, want)
							}
						}
					}
				}
			}
		}
	}
}

// snapshotRows copies the screen's rows and the scrollback's lines, which
// share no storage with the originals.
func snapshotRows(e *Emulator) (screen, history []uv.Line) {
	for y := range e.Height() {
		screen = append(screen, slices.Clone(e.scr.buf.Line(y)))
	}
	for _, l := range e.Scrollback().Lines() {
		history = append(history, slices.Clone(l))
	}
	return screen, history
}

// sameLines reports whether a and b hold equal cells.
func sameLines(a, b []uv.Line) bool {
	return slices.EqualFunc(a, b, func(x, y uv.Line) bool {
		return slices.EqualFunc(x, y, func(p, q uv.Cell) bool { return p.Equal(&q) })
	})
}

// After a rotation, writing a row on screen changes no row in history, and no
// two screen rows share storage.
func TestRotatedRowsShareNoStorage(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
		// history is what the scrollback holds afterwards, unless skipHistory.
		history     []string
		skipHistory bool
	}{
		{name: "line feed at the bottom", seq: "\n", history: []string{"row0"}},
		{name: "DL at the top", seq: "\x1b[1;1H\x1b[2M", history: []string{"row0", "row1"}},
		{name: "SU", seq: "\x1b[3S", history: []string{"row0", "row1", "row2"}},
		{name: "DL past the bottom", seq: "\x1b[1;1H\x1b[9M", history: []string{"row0", "row1", "row2", "row3"}},
		{name: "IL at the top", seq: "\x1b[1;1H\x1b[2L"},
		{name: "reverse index at the top", seq: "\x1b[1;1H\x1bM"},
		{name: "SD", seq: "\x1b[3T"},
		{name: "IL in the middle", seq: "\x1b[2;1H\x1b[L"},
		{name: "DL in a region", seq: "\x1b[2;4r\x1b[2;1H\x1b[M", skipHistory: true},
		{name: "IL in a region", seq: "\x1b[2;4r\x1b[2;1H\x1b[L", skipHistory: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(6, 4)
			e.WriteString("row0\r\nrow1\r\nrow2\r\nrow3")
			e.WriteString(tc.seq)

			if !tc.skipHistory {
				var got []string
				for _, l := range e.Scrollback().Lines() {
					got = append(got, l.String())
				}
				if !slices.Equal(got, tc.history) {
					t.Errorf("history: got = %q, want = %q", got, tc.history)
				}
			}

			// Writing in a row changes that row and no other, on screen or in
			// history.
			for r := range e.Height() {
				screen, history := snapshotRows(e)
				e.WriteString(fmt.Sprintf("\x1b[%d;1H#", r+1))
				gotScreen, gotHistory := snapshotRows(e)

				if !sameLines(gotHistory, history) {
					t.Errorf("writing to row %d changed the history", r)
				}
				if got := gotScreen[r][0].Content; got != "#" {
					t.Errorf("row %d: first cell: got = %q, want = %q", r, got, "#")
				}
				for other := range e.Height() {
					if other != r && !sameLines(gotScreen[other:other+1], screen[other:other+1]) {
						t.Errorf("writing to row %d changed row %d", r, other)
					}
				}
			}
		})
	}
}

// Blank rows rotated in carry the pen's background.
func TestRotatedBlankRowsTakeThePenBackground(t *testing.T) {
	blue := uv.EmptyCell
	blue.Style.Bg = ansi.Blue

	for _, tc := range []struct {
		name  string
		seq   string
		blank []int
	}{
		{"line feed at the bottom", "\n", []int{3}},
		{"DL at the top", "\x1b[1;1H\x1b[2M", []int{2, 3}},
		{"SU", "\x1b[3S", []int{1, 2, 3}},
		{"IL at the top", "\x1b[1;1H\x1b[2L", []int{0, 1}},
		{"reverse index at the top", "\x1b[1;1H\x1bM", []int{0}},
		{"IL in the middle", "\x1b[2;1H\x1b[L", []int{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(6, 4)
			e.WriteString("row0\r\nrow1\r\nrow2\r\nrow3")
			e.WriteString("\x1b[44m")
			e.WriteString(tc.seq)

			for r := range e.Height() {
				blank := slices.Contains(tc.blank, r)
				for x := range e.Width() {
					c := e.CellAt(x, r)
					switch {
					case blank && !c.Equal(&blue):
						t.Fatalf("blank row %d, cell %d: got = %+v, want = %+v", r, x, c, blue)
					case !blank && c.Style.Bg != nil:
						t.Fatalf("row %d, cell %d has a background: %+v", r, x, c)
					}
				}
			}
		})
	}
}

// rowStorage returns the address of each row's first cell, which says which
// row a slice is.
func rowStorage(e *Emulator) []*uv.Cell {
	rows := make([]*uv.Cell, e.Height())
	for y := range rows {
		rows[y] = &e.scr.buf.Line(y)[0]
	}
	return rows
}

// A full-width scroll moves the row slices themselves, which is what makes it
// cost the rows and not their cells; a region narrower than the screen copies
// cells and leaves them where they are.
func TestFullWidthScrollsRotateTheRows(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
		// moved[i] is the row whose storage row i holds afterwards.
		moved []int
	}{
		{"line feed at the bottom", "\x1b[5;1H\n", []int{1, 2, 3, 4, 0}},
		{"DL 2 at the top", "\x1b[1;1H\x1b[2M", []int{2, 3, 4, 0, 1}},
		{"DL past the bottom", "\x1b[1;1H\x1b[9M", []int{0, 1, 2, 3, 4}},
		{"reverse index at the top", "\x1b[1;1H\x1bM", []int{4, 0, 1, 2, 3}},
		{"IL 2 in the middle", "\x1b[2;1H\x1b[2L", []int{0, 3, 4, 1, 2}},
		{"DL in a region", "\x1b[2;4r\x1b[2;1H\x1b[M", []int{0, 2, 3, 1, 4}},
		{"IL in a region", "\x1b[2;4r\x1b[2;1H\x1b[L", []int{0, 3, 1, 2, 4}},
		{"line feed in a narrow region", "\x1b[?69h\x1b[1;3s\x1b[5;1H\n", []int{0, 1, 2, 3, 4}},
		{"IL in a narrow region", "\x1b[?69h\x1b[1;3s\x1b[1;1H\x1b[2L", []int{0, 1, 2, 3, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(4, 5)
			before := rowStorage(e)
			e.WriteString(tc.seq)
			after := rowStorage(e)

			for i, from := range tc.moved {
				if after[i] != before[from] {
					t.Errorf("row %d holds the storage of row %d, want row %d", i, slices.Index(before, after[i]), from)
				}
			}
		})
	}
}

// BenchmarkLineFeedAtTheBottom scrolls a 200x60 screen by a line of text and
// a line feed at the bottom margin, 10,000 times per iteration: with the
// default scrollback, and with none but a scroll-off function, which leaves
// the screen's own work.
func BenchmarkLineFeedAtTheBottom(b *testing.B) {
	feeds := bytes.Repeat([]byte(strings.Repeat("abcdefgh", 12)+"\r\n"), 10000)
	for _, bc := range []struct {
		name  string
		setup func(*Emulator)
	}{
		{"scrollback", func(*Emulator) {}},
		{"scroll-off", func(e *Emulator) {
			e.SetScrollbackSize(0)
			e.SetScrollOffFunc(func(uv.Line) {})
		}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			term := NewEmulator(200, 60)
			bc.setup(term)
			term.WriteString("\x1b[60;1H")
			b.SetBytes(int64(len(feeds)))
			b.ReportAllocs()
			for b.Loop() {
				term.Write(feeds)
			}
		})
	}
}
