package vt

import (
	"slices"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// wideState is what a test of wide characters at the margin reads back: the
// text of each row, the cursor and the wrap flag.
type wideState struct {
	rows    []string
	pos     uv.Position
	pending bool
}

// writeWide writes each chunk to a new w×h emulator in its own Write and
// returns the state it ends in.
func writeWide(w, h int, chunks ...string) (*Emulator, wideState) {
	e := NewEmulator(w, h)
	for _, c := range chunks {
		e.WriteString(c)
	}
	rows := make([]string, h)
	for y := range rows {
		rows[y] = rowText(e, y)
	}
	return e, wideState{rows, e.CursorPosition(), e.PendingWrap()}
}

// check reports each way s differs from want.
func (s wideState) check(t *testing.T, want wideState) {
	t.Helper()
	if !slices.Equal(s.rows, want.rows) {
		t.Errorf("rows: got = %q, want = %q", s.rows, want.rows)
	}
	if s.pos != want.pos {
		t.Errorf("cursor: got = %v, want = %v", s.pos, want.pos)
	}
	if s.pending != want.pending {
		t.Errorf("PendingWrap: got = %t, want = %t", s.pending, want.pending)
	}
}

// xterm 412's dotext (charproc.c) writes a character only where it fits in
// what is left of the line. With the cursor on the last column a wide one
// doesn't (:7142), so nothing is written on that line, and under DECAWM
// force_wrap (:7165-7170) wraps it (:7065-7075) and writes the character at
// column 0 of the next. The last column keeps what it had.
func TestAWideCharacterAtTheLastColumnWraps(t *testing.T) {
	e, got := writeWide(5, 2, "abcd", "日")
	got.check(t, wideState{[]string{"abcd", "日"}, uv.Pos(2, 1), false})
	if c := e.CellAt(4, 0); c == nil || !c.Equal(&uv.EmptyCell) {
		t.Errorf("cell (4, 0): got = %+v, want a blank", c)
	}
	if c := e.CellAt(0, 1); c == nil || c.Content != "日" || c.Width != 2 {
		t.Errorf("cell (0, 1): got = %+v, want 日 of width 2", c)
	}

	// The cluster the Write ends with is the one it wrapped, so a mark in
	// the next Write joins it there.
	t.Run("a mark in the next Write", func(t *testing.T) {
		e, got := writeWide(5, 2, "abcd日", "\u0301")
		got.check(t, wideState{[]string{"abcd", "日\u0301"}, uv.Pos(2, 1), false})
		if c := e.CellAt(0, 1); c == nil || c.Width != 2 {
			t.Errorf("cell (0, 1): got = %+v, want width 2", c)
		}
	})
}

// Under IRM, WriteText makes room for the cells it writes (util.c:1549-1551),
// and dotext calls it only on the line the character goes on: the wrap
// comes first, and the line it leaves isn't shifted.
func TestAnInsertedWideCharacterAtTheLastColumnWrapsFirst(t *testing.T) {
	_, got := writeWide(5, 2, "\x1b[2;1Hvwxyz\x1b[1;1Habcde\x1b[1;5H\x1b[4h", "日")
	got.check(t, wideState{[]string{"abcde", "日vwx"}, uv.Pos(2, 1), false})

	t.Run("without autowrap", func(t *testing.T) {
		_, got := writeWide(5, 2, "\x1b[2;1Hvwxyz\x1b[1;1Habcde\x1b[1;5H\x1b[4h\x1b[?7l", "日")
		got.check(t, wideState{[]string{"abcde", "vwxyz"}, uv.Pos(4, 0), false})
	})
}

// Without DECAWM there is no force_wrap (:7167), so the character that
// doesn't fit isn't written at all, and dotext leaves no wrap pending: it
// clears one left from before (:7066) and sets none (:7173).
func TestAWideCharacterWithoutAutowrapIsNotPrinted(t *testing.T) {
	e, got := writeWide(5, 2, "\x1b[?7l", "abcd", "日")
	got.check(t, wideState{[]string{"abcd", ""}, uv.Pos(4, 0), false})
	if c := e.CellAt(4, 0); c == nil || !c.Equal(&uv.EmptyCell) {
		t.Errorf("cell (4, 0): got = %+v, want a blank", c)
	}

	for _, tc := range []struct {
		name   string
		chunks []string
		want   wideState
	}{
		// The "e" left a wrap pending under DECAWM, which ?7l doesn't clear.
		{"over a character, with a wrap pending", []string{"abcde\x1b[?7l", "日"}, wideState{[]string{"abcde", ""}, uv.Pos(4, 0), false}},
		// The mark joins the 日, which wasn't printed, so it isn't printed
		// either; the "e" before the 日 is a cluster of its own.
		{"a mark in the next Write", []string{"\x1b[?7labcd", "e日", "\u0301"}, wideState{[]string{"abcde", ""}, uv.Pos(4, 0), false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, got := writeWide(5, 2, tc.chunks...)
			got.check(t, tc.want)
		})
	}
}

// A character wider than the line can never fit: dotext gives up on it
// (:7143-7144) after the pending wrap the "a" left (:7065-7075), and leaves
// none.
func TestAWideCharacterWiderThanTheLineIsNotPrinted(t *testing.T) {
	_, got := writeWide(1, 3, "a", "日", "b")
	got.check(t, wideState{[]string{"a", "b", ""}, uv.Pos(0, 1), true})
}

// dotext sets the wrap when a character fills the rest of the line exactly,
// whatever its width (:7157-7158, :7173), and WriteText's CursorForward
// (util.c:1621, cursor.c:231-232) holds the cursor on the last column. So a
// wide character ending on the last column leaves the cursor there with the
// wrap pending, and the next character goes to the next line.
func TestAWideCharacterEndingOnTheLastColumnLeavesTheWrapPending(t *testing.T) {
	_, got := writeWide(10, 2, "日日日日日")
	got.check(t, wideState{[]string{"日日日日日", ""}, uv.Pos(9, 0), true})
	_, got = writeWide(10, 2, "日日日日日X")
	got.check(t, wideState{[]string{"日日日日日", "X"}, uv.Pos(1, 1), false})

	t.Run("after narrow characters", func(t *testing.T) {
		e, got := writeWide(10, 2, "abcdefgh日", "X")
		got.check(t, wideState{[]string{"abcdefgh日", "X"}, uv.Pos(1, 1), false})
		if c := e.CellAt(0, 1); c == nil || c.Content != "X" {
			t.Errorf("cell (0, 1): got = %+v, want X", c)
		}
	})

	// The carried cluster records the cursor on the last column with the
	// wrap pending, so a mark in the next Write joins it, and it ends as it
	// would have arriving in one Write.
	t.Run("a mark in the next Write", func(t *testing.T) {
		e, got := writeWide(10, 2, "日日日日日", "\u0301", "X")
		got.check(t, wideState{[]string{"日日日日日\u0301", "X"}, uv.Pos(1, 1), false})
		if c := e.CellAt(8, 0); c == nil || c.Content != "日\u0301" || c.Width != 2 {
			t.Errorf("cell (8, 0): got = %+v, want 日\u0301 of width 2", c)
		}
	})
}

// A cluster split across two Writes ends as it does in one (see
// TestClusterAcrossWrites). U+2764 (a heart) is one column wide, and VS16
// makes it two: at the last column the heart alone fits, and with VS16 it
// doesn't, so it wraps or isn't printed. Either way the last column keeps what
// it had, which the carried cluster's undo puts back: an X, or the second half
// of a wide character the heart alone cut.
func TestAClusterWideningAtTheLastColumnEndsAsInOneWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		prep string // leaves the cursor on the last column
		want wideState
	}{
		{"without autowrap", "\x1b[?7labcdX", wideState{[]string{"abcdX", ""}, uv.Pos(4, 0), false}},
		{"without autowrap, inserting", "\x1b[?7l\x1b[4habcdX", wideState{[]string{"abcdX", ""}, uv.Pos(4, 0), false}},
		{"without autowrap, over a wide character", "\x1b[?7labc日", wideState{[]string{"abc日", ""}, uv.Pos(4, 0), false}},
		{"with autowrap", "abcdX\x1b[1;5H", wideState{[]string{"abcdX", "\u2764\ufe0f"}, uv.Pos(2, 1), false}},
		{"with autowrap, inserting", "abcdX\x1b[1;5H\x1b[4h", wideState{[]string{"abcdX", "\u2764\ufe0f"}, uv.Pos(2, 1), false}},
		{"with autowrap, over a wide character", "abc日\x1b[1;5H", wideState{[]string{"abc日", "\u2764\ufe0f"}, uv.Pos(2, 1), false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, whole := writeWide(5, 2, tc.prep, "\u2764\ufe0f")
			whole.check(t, tc.want)
			_, split := writeWide(5, 2, tc.prep, "\u2764", "\ufe0f")
			split.check(t, tc.want)
		})
	}

	// A renderer that drew the screen between the two Writes has to draw
	// the cells the undo put back again.
	t.Run("the undo marks its cells touched", func(t *testing.T) {
		e := NewEmulator(5, 2)
		e.WriteString("\x1b[?7labcdX\u2764")
		e.MainScreen().ClearTouched()
		e.WriteString("\ufe0f")
		if got := touchedRows(e, 2); got[0] == "" {
			t.Errorf("touched rows: got = %q, want row 0 touched", got)
		}
	})
}

// A Write that ends in a mark with no base still carries the cluster printed
// before the mark, across whatever came between. Where an erase came between,
// the cell no longer holds the cluster, and what the cluster wrote over at the
// last column is gone with it: the undo mustn't put it back. Either way the
// screen ends as it does in one Write, where the mark and VS16 have no base
// and are dropped.
func TestTheCarriedUndoKeepsAnEraseAtTheLastColumn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first string
		want  wideState
	}{
		{"ECH", "\x1b[?7labcdX\x1b[1;5H\u2764\x1b[1;5H\x1b[X\u0301", wideState{[]string{"abcd", ""}, uv.Pos(4, 0), false}},
		{"EL 1", "\x1b[?7labcdX\x1b[1;5H\u2764\x1b[1;5H\x1b[1K\u0301", wideState{[]string{"", ""}, uv.Pos(4, 0), false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, whole := writeWide(5, 2, tc.first+"\ufe0f")
			whole.check(t, tc.want)
			_, split := writeWide(5, 2, tc.first, "\ufe0f")
			split.check(t, tc.want)
		})
	}
}
