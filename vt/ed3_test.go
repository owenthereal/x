package vt

import (
	"fmt"
	"slices"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// threeRowsOverTwo is five lines, which a 10-column, 3-row emulator shows as
// "c", "d" and "e" with the cursor after the "e", having sent "a" and "b" to
// history: to the scrollback and, if set, the scroll-off function.
const threeRowsOverTwo = "a\r\nb\r\nc\r\nd\r\ne"

// xterm's ED 3 (do_erase_display in util.c) only sets screen->savedlines to
// zero: it erases no cell and moves no cursor.
func TestED3ClearsOnlyHistory(t *testing.T) {
	e := NewEmulator(10, 3)
	cleared, lenWhenCalled := 0, -1
	e.SetScrollbackClearFunc(func() {
		cleared++
		lenWhenCalled = e.ScrollbackLen()
	})
	e.WriteString(threeRowsOverTwo)
	if n := e.ScrollbackLen(); n != 2 {
		t.Fatalf("ScrollbackLen before ED 3: got = %d, want = 2", n)
	}
	rows, render, pos := screenRows(e, 10), e.Render(), e.CursorPosition()

	e.WriteString("\x1b[3J")

	if got := screenRows(e, 10); !slices.Equal(got, rows) {
		t.Errorf("screen after ED 3: got = %q, want = %q", got, rows)
	}
	if got := e.Render(); got != render {
		t.Errorf("render after ED 3: got = %q, want = %q", got, render)
	}
	if got := e.CursorPosition(); got != pos {
		t.Errorf("cursor after ED 3: got = %v, want = %v", got, pos)
	}
	if n := e.ScrollbackLen(); n != 0 {
		t.Errorf("ScrollbackLen after ED 3: got = %d, want = 0", n)
	}
	if cleared != 1 {
		t.Errorf("clear function calls: got = %d, want = 1", cleared)
	}
	if lenWhenCalled != 0 {
		t.Errorf("ScrollbackLen when the clear function was called: got = %d, want = 0", lenWhenCalled)
	}
}

// Claude Code repaints on resize with ESC[2J ESC[3J: ED 2 pushes the screen
// into history, then ED 3 clears the history, so the transcript that follows
// is the only one there. A consumer keeping history must see the pushes before
// the clear, and no push after it. It does so whatever the scrollback's size:
// a consumer can set it to 0 and keep history through these two functions
// alone.
func TestED2ThenED3OrderTheirCallbacks(t *testing.T) {
	for _, size := range []int{DefaultScrollbackSize, 0} {
		t.Run(fmt.Sprintf("size %d", size), func(t *testing.T) {
			e := NewEmulator(10, 3)
			e.SetScrollbackSize(size)
			var events []string
			e.SetScrollOffFunc(func(l uv.Line) { events = append(events, "off "+l.String()) })
			e.SetScrollbackClearFunc(func() { events = append(events, "clear") })
			// The "a" scrolls off with the line feed before the "d".
			e.WriteString("a\r\nb\r\nc\r\nd")

			e.WriteString("\x1b[2J\x1b[3J")

			want := []string{"off a", "off b", "off c", "off d", "clear"}
			if !slices.Equal(events, want) {
				t.Errorf("callbacks: got = %q, want = %q", events, want)
			}
			if n := e.ScrollbackLen(); n != 0 {
				t.Errorf("ScrollbackLen: got = %d, want = 0", n)
			}
		})
	}
}

// ED 3 touches no cell and no cursor, so a wrap pending at the right margin is
// still pending after it, on either screen, and the next character wraps as it
// would have without the ED 3. CursorPosition reports the last column both
// ways, so only PendingWrap and the next print show it.
func TestED3KeepsAPendingWrap(t *testing.T) {
	// A step is bytes written to both emulators, and where the cursor is after.
	type step struct {
		seq    string
		cursor uv.Position
	}
	for _, tc := range []struct {
		name, setup string
		steps       []step
	}{
		{
			// The two line feeds at the bottom scroll "a" and "b" off.
			name:  "main screen",
			setup: "a\r\nb\r\nc\r\nd\r\n0123456789",
			steps: []step{{"x", uv.Pos(1, 2)}},
		},
		{
			// The main screen's wrap, which ?1049h saved, comes back with
			// ?1049l.
			name:  "alternate screen",
			setup: "a\r\n0123456789\x1b[?1049h\x1b[H0123456789",
			steps: []step{{"x", uv.Pos(1, 1)}, {"\x1b[?1049ly", uv.Pos(1, 2)}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			with, without := NewEmulator(10, 3), NewEmulator(10, 3)
			for _, e := range []*Emulator{with, without} {
				e.WriteString(tc.setup)
				if !e.PendingWrap() {
					t.Fatal("no wrap pending after the setup")
				}
			}
			with.WriteString("\x1b[3J")

			if !with.PendingWrap() {
				t.Error("wrap no longer pending after ED 3")
			}

			for _, s := range tc.steps {
				with.WriteString(s.seq)
				without.WriteString(s.seq)

				if got, want := screenRows(with, 10), screenRows(without, 10); !slices.Equal(got, want) {
					t.Errorf("screen after %q: got = %q, want = %q (without the ED 3)", s.seq, got, want)
				}
				if got := with.CursorPosition(); got != s.cursor {
					t.Errorf("cursor after %q: got = %v, want = %v", s.seq, got, s.cursor)
				}
				if got, want := with.PendingWrap(), without.PendingWrap(); got != want {
					t.Errorf("pending wrap after %q: got = %v, want = %v", s.seq, got, want)
				}
			}
		})
	}
}

// With the alternate screen showing, xterm's ED 3 still clears the history:
// do_erase_display zeroes screen->savedlines without looking at
// screen->whichBuf, and only the main buffer adds to it (xtermScroll saves a
// line only while !screen->whichBuf). It leaves the alternate screen and its
// cursor as they were, and the main screen is as it was when it returns.
func TestED3WhileTheAlternateScreenShows(t *testing.T) {
	e := NewEmulator(10, 3)
	cleared := 0
	e.SetScrollbackClearFunc(func() { cleared++ })
	e.WriteString(threeRowsOverTwo)
	e.WriteString("\x1b[?1049hx\r\ny")
	if !e.IsAltScreen() {
		t.Fatal("not on the alternate screen")
	}
	rows, render, pos := screenRows(e, 10), e.Render(), e.CursorPosition()

	e.WriteString("\x1b[3J")

	if !e.IsAltScreen() {
		t.Error("ED 3 left the alternate screen")
	}
	if got := screenRows(e, 10); !slices.Equal(got, rows) {
		t.Errorf("alternate screen after ED 3: got = %q, want = %q", got, rows)
	}
	if got := e.Render(); got != render {
		t.Errorf("render after ED 3: got = %q, want = %q", got, render)
	}
	if got := e.CursorPosition(); got != pos {
		t.Errorf("cursor after ED 3: got = %v, want = %v", got, pos)
	}
	if n := e.ScrollbackLen(); n != 0 {
		t.Errorf("main screen's ScrollbackLen after ED 3: got = %d, want = 0", n)
	}
	if cleared != 1 {
		t.Errorf("clear function calls: got = %d, want = 1", cleared)
	}

	e.WriteString("\x1b[?1049l")
	if want := []string{"c", "d", "e"}; !slices.Equal(screenRows(e, 10), want) {
		t.Errorf("main screen after ED 3 and ?1049l: got = %q, want = %q", screenRows(e, 10), want)
	}
	if got, want := e.CursorPosition(), uv.Pos(1, 2); got != want {
		t.Errorf("main cursor after ?1049l: got = %v, want = %v", got, want)
	}
}

// The clear function is for a caller keeping history through the scroll-off
// function, whose history is whole whether or not the emulator keeps a
// scrollback of its own: so ED 3 calls it for a scrollback of size 0 and for
// none, and every time, as xterm zeroes savedlines on every ED 3.
func TestED3CallsTheClearFuncWithoutAScrollback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Emulator)
	}{
		{"size 0", func(e *Emulator) { e.SetScrollbackSize(0) }},
		{"no buffer", func(e *Emulator) { e.MainScreen().SetScrollback(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(10, 3)
			tc.setup(e)
			cleared := 0
			e.SetScrollbackClearFunc(func() { cleared++ })
			e.WriteString(threeRowsOverTwo)
			rows := screenRows(e, 10)

			e.WriteString("\x1b[3J\x1b[3J")

			if cleared != 2 {
				t.Errorf("clear function calls after two ED 3: got = %d, want = 2", cleared)
			}
			if got := screenRows(e, 10); !slices.Equal(got, rows) {
				t.Errorf("screen after ED 3: got = %q, want = %q", got, rows)
			}
		})
	}
}

// xterm's RIS clears the saved lines (VTReset(xw, True, True) in charproc.c
// sets screen->savedlines to zero) and its soft reset keeps them
// (VTReset(xw, False, False)), so the clear function is told of the first
// and not the second.
func TestRISClearsHistoryAndDECSTRKeepsIt(t *testing.T) {
	for _, tc := range []struct {
		name        string
		seq         string
		wantHistory int
		wantCleared int
	}{
		{"RIS", "\x1bc", 0, 1},
		{"DECSTR", "\x1b[!p", 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(10, 3)
			cleared := 0
			e.SetScrollbackClearFunc(func() { cleared++ })
			e.WriteString(threeRowsOverTwo)

			e.WriteString(tc.seq)

			if n := e.ScrollbackLen(); n != tc.wantHistory {
				t.Errorf("ScrollbackLen: got = %d, want = %d", n, tc.wantHistory)
			}
			if cleared != tc.wantCleared {
				t.Errorf("clear function calls: got = %d, want = %d", cleared, tc.wantCleared)
			}
		})
	}
}

// What the host does to the scrollback itself, it knows: the clear function
// reports what the program running in the terminal did.
func TestTheHostsOwnClearsDoNotCallTheClearFunc(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func(*Emulator)
	}{
		{"ClearScrollback", (*Emulator).ClearScrollback},
		{"SetScrollbackSize", func(e *Emulator) { e.SetScrollbackSize(0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(10, 3)
			cleared := 0
			e.SetScrollbackClearFunc(func() { cleared++ })
			e.WriteString(threeRowsOverTwo)

			tc.do(e)

			if n := e.ScrollbackLen(); n != 0 {
				t.Errorf("ScrollbackLen: got = %d, want = 0", n)
			}
			if cleared != 0 {
				t.Errorf("clear function calls: got = %d, want = 0", cleared)
			}
		})
	}
}
