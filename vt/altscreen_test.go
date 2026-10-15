package vt

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/owenthereal/x/ansi"
)

// The tests below encode xterm 412 (XTERM_DATE 2026-10-11). charproc.c's
// dpmodes switches buffers for ?47 (srm_ALTBUF), ?1047 (srm_OPT_ALTBUF) and
// ?1049 (srm_OPT_ALTBUF_CURSOR) with ToAlternate and FromAlternate, which
// do nothing when the buffer asked for already shows, and otherwise call
// SwitchBufs. That changes the buffer shown and nothing else: the cursor
// (cur_row and cur_col, the attributes, do_wrap), the margins (top_marg and
// bot_marg) and the DECSC slots (sc[whichBuf]) are all fields of TScreen,
// beside whichBuf. Only ?1047l and ?1049h clear, with util.c's ClearScreen,
// which resets do_wrap and leaves the cursor where it is.

// carrySwitches are the ways to the alternate screen and back that carry the
// cursor both ways: ?47, and ?1047, whose way back clears the alternate
// screen first. ?1049l restores the main screen's DECSC slot instead.
var carrySwitches = []struct{ name, on, off string }{
	{"?47", "\x1b[?47h", "\x1b[?47l"},
	{"?1047", "\x1b[?1047h", "\x1b[?1047l"},
}

// screenText returns the text of each row of s.
func screenText(s *Screen) []string {
	rows := make([]string, s.Height())
	for y := range rows {
		rows[y] = s.Line(y).String()
	}
	return rows
}

// labelRows writes the label prefix+n at the start of each row n, from 1 to
// rows, with CUP, which no margin bounds.
func labelRows(prefix string, rows int) string {
	var b strings.Builder
	for n := 1; n <= rows; n++ {
		fmt.Fprintf(&b, "\x1b[%d;1H%s%d", n, prefix, n)
	}
	return b.String()
}

// mustWrite writes s to e and checks which screen shows after it.
func mustWrite(t *testing.T, e *Emulator, s string, alt bool) {
	t.Helper()
	if _, err := e.WriteString(s); err != nil {
		t.Fatal(err)
	}
	if e.IsAltScreen() != alt {
		t.Fatalf("after %q: IsAltScreen = %v, want = %v", s, e.IsAltScreen(), alt)
	}
}

// SwitchBufs moves no cursor: cur_row and cur_col are the terminal's, so the
// row the alternate screen's CUP chose is where the main screen prints after
// FromAlternate, and the cursor the main screen had is where the alternate
// screen starts after ToAlternate.
func TestAlt47CarriesTheCursorBack(t *testing.T) {
	for _, sw := range carrySwitches {
		t.Run(sw.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 8)
			mustWrite(t, e, "\x1b[1;1H"+sw.on, true)
			mustWrite(t, e, "\x1b[5;1H"+sw.off+"X", false)
			if c := e.MainScreen().CellAt(0, 4); c == nil || c.Content != "X" {
				t.Errorf("main screen's cell (0,4): got = %+v, want X, on row 5 where the alternate screen left the cursor", c)
			}
			if got, want := e.CursorPosition(), uv.Pos(1, 4); got != want {
				t.Errorf("cursor: got = %v, want = %v", got, want)
			}
		})
		t.Run(sw.name+" in", func(t *testing.T) {
			e := newTestTerminal(t, 10, 8)
			mustWrite(t, e, "\x1b[3;4H"+sw.on+"X", true)
			if c := e.AltScreen().CellAt(3, 2); c == nil || c.Content != "X" {
				t.Errorf("alternate screen's cell (3,2): got = %+v, want X, where the main screen left the cursor", c)
			}
		})
	}
}

// The attributes and colors are the terminal's too (xw->flags and the
// current colors in TScreen), so the pen the alternate screen reset is the
// main screen's after FromAlternate. x/vt's OSC 8 link goes with the pen.
func TestAlt47CarriesThePen(t *testing.T) {
	for _, sw := range carrySwitches {
		t.Run(sw.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 4)
			mustWrite(t, e, "\x1b[31m\x1b]8;;https://example.com\x07"+sw.on, true)
			mustWrite(t, e, "\x1b[m\x1b]8;;\x07"+sw.off+"X", false)
			c := e.MainScreen().CellAt(0, 0)
			if c == nil || c.Content != "X" {
				t.Fatalf("main screen's cell (0,0): got = %+v, want X", c)
			}
			if !c.Style.IsZero() || c.Link != (uv.Link{}) {
				t.Errorf("X's pen: got = %+v link %+v, want the default pen and no link, as the alternate screen left them", c.Style, c.Link)
			}
		})
	}
}

// DECTCEM's cursor_set and DECSCUSR's cursor_shape are TScreen's too, and
// SwitchBufs leaves them, as CursorRestore does after ?1049l: a cursor the
// alternate screen hid and reshaped is still hidden and reshaped on the main
// screen, with ?25 still reset.
func TestAltSwitchCarriesCursorVisibilityAndShape(t *testing.T) {
	for _, sw := range []struct{ name, on, off string }{
		{"?47", "\x1b[?47h", "\x1b[?47l"},
		{"?1047", "\x1b[?1047h", "\x1b[?1047l"},
		{"?1049", "\x1b[?1049h", "\x1b[?1049l"},
	} {
		t.Run(sw.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 3)
			mustWrite(t, e, sw.on+"\x1b[?25l\x1b[4 q", true)
			mustWrite(t, e, sw.off, false)
			if !e.CursorHidden() {
				t.Error("CursorHidden: got = false, want = true, as the alternate screen left it")
			}
			if got := e.Mode(ansi.ModeTextCursorEnable); got != ansi.ModeReset {
				t.Errorf("DECTCEM: got = %v, want = %v", got, ansi.ModeReset)
			}
			if style, blink := e.CursorStyle(); style != CursorUnderline || blink {
				t.Errorf("cursor style: got = %v blink %v, want a steady underline", style, blink)
			}
		})
	}
}

// do_dec_rqm (misc.c) answers ?47 from whichBuf, as it does ?1047 and ?1049
// (MdBool), so a fresh terminal reports it reset, not unrecognized.
func TestDECRQMReportsTheAltBufferMode(t *testing.T) {
	e := newTestTerminal(t, 10, 3)
	var replies []string
	e.SetReplyFunc(func(p []byte) { replies = append(replies, string(p)) })
	mustWrite(t, e, "\x1b[?47$p\x1b[?47h\x1b[?47$p", true)
	if want := []string{"\x1b[?47;2$y", "\x1b[?47;1$y"}; !slices.Equal(replies, want) {
		t.Errorf("replies: got = %q, want = %q", replies, want)
	}
}

// SwitchBufs only changes which buffer shows, and ToAlternate(xw, False) and
// FromAlternate(xw, False) clear nothing, so ?47l leaves the alternate
// buffer as it was, and both ?47h and ?1047h show it again.
func TestAlt47KeepsItsContents(t *testing.T) {
	for _, on := range []string{"\x1b[?47h", "\x1b[?1047h"} {
		t.Run(on, func(t *testing.T) {
			e := newTestTerminal(t, 10, 3)
			mustWrite(t, e, "\x1b[?47hTEXT", true)
			mustWrite(t, e, "\x1b[?47l", false)
			if got := screenText(e.MainScreen()); slices.Contains(got, "TEXT") {
				t.Errorf("main screen: got = %q, want no TEXT", got)
			}
			mustWrite(t, e, on, true)
			if got, want := screenText(e.AltScreen()), []string{"TEXT", "", ""}; !slices.Equal(got, want) {
				t.Errorf("alternate screen: got = %q, want = %q", got, want)
			}
		})
	}
}

// srm_OPT_ALTBUF's reset branch runs ClearScreen before FromAlternate, but
// only while the alternate buffer shows (if (screen->whichBuf)): ?1047l
// clears the alternate screen on the way back, and leaves a main screen it
// finds showing alone.
func TestAlt1047ClearsOnLeaving(t *testing.T) {
	e := newTestTerminal(t, 10, 3)
	mustWrite(t, e, "\x1b[?1047hTEXT", true)
	mustWrite(t, e, "\x1b[?1047l", false)
	mustWrite(t, e, "\x1b[?47h", true)
	if got, want := screenText(e.AltScreen()), []string{"", "", ""}; !slices.Equal(got, want) {
		t.Errorf("alternate screen: got = %q, want = %q", got, want)
	}

	t.Run("on the main screen", func(t *testing.T) {
		e := newTestTerminal(t, 10, 3)
		mustWrite(t, e, "TEXT\x1b[?1047l", false)
		if got, want := screenText(e.MainScreen()), []string{"TEXT", "", ""}; !slices.Equal(got, want) {
			t.Errorf("main screen: got = %q, want = %q", got, want)
		}
	})
}

// srm_OPT_ALTBUF_CURSOR's set branch is CursorSave, ToAlternate(xw, True)
// and ClearScreen, and its reset branch FromAlternate(xw, False) and
// CursorRestore. The save fills the main screen's slot, sc[0], and the
// restore reads it back after the switch, so the alternate screen's CUP is
// forgotten. ClearScreen moves no cursor, so the alternate screen starts
// where the main screen left it, blank whatever ?47 left there.
func TestAlt1049SavesAndClears(t *testing.T) {
	e := newTestTerminal(t, 10, 10)
	mustWrite(t, e, "\x1b[3;7H\x1b[?1049h", true)
	if got, want := e.CursorPosition(), uv.Pos(6, 2); got != want {
		t.Errorf("cursor after ?1049h: got = %v, want = %v, where the main screen left it", got, want)
	}
	mustWrite(t, e, "\x1b[9;1H\x1b[?1049lX", false)
	if c := e.MainScreen().CellAt(6, 2); c == nil || c.Content != "X" {
		t.Errorf("main screen's cell (6,2): got = %+v, want X, at the 3;7 the main slot saved", c)
	}

	t.Run("cleared on entering", func(t *testing.T) {
		e := newTestTerminal(t, 10, 3)
		mustWrite(t, e, "\x1b[?47hTEXT", true)
		mustWrite(t, e, "\x1b[?47l", false)
		mustWrite(t, e, "\x1b[?1049h", true)
		if got, want := screenText(e.AltScreen()), []string{"", "", ""}; !slices.Equal(got, want) {
			t.Errorf("alternate screen: got = %q, want = %q", got, want)
		}
	})

	// With the alternate buffer showing, ToAlternate does nothing, but
	// CursorSave and ClearScreen still run: the save goes to the alternate
	// screen's slot, sc[1], and the alternate screen is cleared.
	t.Run("on the alternate screen", func(t *testing.T) {
		e := newTestTerminal(t, 10, 3)
		mustWrite(t, e, "\x1b[?47h\x1b[2;3HTEXT", true)
		mustWrite(t, e, "\x1b[?1049h", true)
		if got, want := screenText(e.AltScreen()), []string{"", "", ""}; !slices.Equal(got, want) {
			t.Errorf("alternate screen: got = %q, want = %q", got, want)
		}
		if s := e.AltScreen().SavedCursor(); s.Position != uv.Pos(6, 1) || !s.Saved.Saved {
			t.Errorf("alternate screen's slot: got = %s, want (6,1), saved", slot(s))
		}
		if s := e.MainScreen().SavedCursor(); s.Saved.Saved {
			t.Errorf("main screen's slot: got = %s, want never saved", slot(s))
		}
	})
}

// do_wrap is the terminal's, and SwitchBufs leaves it: a wrap pending when
// ?47h or ?1047h switches, either way, is still pending, and the next
// character wraps on the screen switched to. ClearScreen's ResetWrap clears
// it, so it is gone after ?1047l, which clears the alternate screen before
// FromAlternate, and after ?1049h, which clears once it has switched (the
// main screen's slot keeps it for ?1049l, as TestDECRCRestoresThePendingWrap
// and Test1049SavesAndRestoresTheCursorState show).
func TestAltSwitchCarriesThePendingWrap(t *testing.T) {
	full := strings.Repeat("x", 10)
	for _, tc := range []struct {
		name, in string
		alt      bool
		pending  bool
		y        uv.Position // where the next character lands
		main     string      // the main screen's first row after it
	}{
		{"?47h", full + "\x1b[?47h", true, true, uv.Pos(0, 1), full},
		{"?1047h", full + "\x1b[?1047h", true, true, uv.Pos(0, 1), full},
		{"?47l", "\x1b[?47h" + full + "\x1b[?47l", false, true, uv.Pos(0, 1), ""},
		{"?1047l clears it", "\x1b[?1047h" + full + "\x1b[?1047l", false, false, uv.Pos(9, 0), "         y"},
		{"?1047l clears it back to a wrap on main", full + "\x1b[?1047h\x1b[?1047l", false, false, uv.Pos(9, 0), "xxxxxxxxxy"},
		{"?1049h clears it", full + "\x1b[?1049h", true, false, uv.Pos(9, 0), full},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 3)
			mustWrite(t, e, tc.in, tc.alt)
			if got := e.PendingWrap(); got != tc.pending {
				t.Errorf("PendingWrap: got = %v, want = %v (cursor %v)", got, tc.pending, e.CursorPosition())
			}
			mustWrite(t, e, "y", tc.alt)
			if c := e.CellAt(tc.y.X, tc.y.Y); c == nil || c.Content != "y" {
				t.Errorf("cell %v: got = %+v, want y", tc.y, c)
			}
			if got := e.MainScreen().Line(0).String(); got != tc.main {
				t.Errorf("main screen's row 0: got = %q, want = %q", got, tc.main)
			}
		})
	}
}

// CASE_DECSTBM takes the top margin as 1 when it is absent or 0
// (one_if_default), and the bottom as the screen's height when it is absent,
// default, 0 or past the height. Only bottom > top sets the margins
// (set_tb_margins) and homes the cursor (CursorSet); anything else changes
// neither.
func TestDECSTBMFollowsXterm(t *testing.T) {
	const w, h = 10, 8
	// Each sequence follows margins 2;5 and a CUP to 7;3, so an ignored
	// one leaves both.
	kept, from := uv.Rect(0, 1, w, 4), uv.Pos(2, 6)
	for _, tc := range []struct {
		seq    string
		region uv.Rectangle
		cursor uv.Position
	}{
		{"\x1b[2;5r", uv.Rect(0, 1, w, 4), uv.Pos(0, 0)},
		{"\x1b[10;11r", kept, from},
		{"\x1b[10r", kept, from},
		{"\x1b[3r", uv.Rect(0, 2, w, 6), uv.Pos(0, 0)},
		{"\x1b[2;99r", uv.Rect(0, 1, w, 7), uv.Pos(0, 0)},
		{"\x1b[0;0r", uv.Rect(0, 0, w, h), uv.Pos(0, 0)},
		{"\x1b[r", uv.Rect(0, 0, w, h), uv.Pos(0, 0)},
		{"\x1b[5;5r", kept, from},
		{"\x1b[6;3r", kept, from},
	} {
		t.Run(fmt.Sprintf("%q", tc.seq), func(t *testing.T) {
			e := newTestTerminal(t, w, h)
			mustWrite(t, e, "\x1b[2;5r\x1b[7;3H"+tc.seq, false)
			if got := e.MainScreen().ScrollRegion(); got != tc.region {
				t.Errorf("region: got = %v, want = %v", got, tc.region)
			}
			if got := e.CursorPosition(); got != tc.cursor {
				t.Errorf("cursor: got = %v, want = %v", got, tc.cursor)
			}
		})
	}
}

// The margins are TScreen's top_marg and bot_marg, which SwitchBufs leaves,
// so a region set on either screen is the one line feeds scroll on the other
// after a switch, in both directions. Rows scrolled out of a region whose top
// is below the first row reach no history, as xtermScroll saves lines only
// when top_marg is 0.
func TestMarginsSurviveBufferSwitches(t *testing.T) {
	const w, h = 10, 8
	for _, sw := range []struct{ name, on, off string }{
		{"?1049", "\x1b[?1049h", "\x1b[?1049l"},
		{"?47", "\x1b[?47h", "\x1b[?47l"},
		{"?1047", "\x1b[?1047h", "\x1b[?1047l"},
	} {
		t.Run(sw.name+" to the alternate screen", func(t *testing.T) {
			e := newTestTerminal(t, w, h)
			mustWrite(t, e, labelRows("m", h)+"\x1b[2;5r"+sw.on, true)
			mustWrite(t, e, labelRows("a", h)+"\x1b[5;1H\n", true)
			if got, want := screenText(e.AltScreen()), []string{"a1", "a3", "a4", "a5", "", "a6", "a7", "a8"}; !slices.Equal(got, want) {
				t.Errorf("alternate screen: got = %q, want = %q, rows 2-5 scrolled", got, want)
			}
			if got, want := screenText(e.MainScreen()), []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}; !slices.Equal(got, want) {
				t.Errorf("main screen: got = %q, want = %q", got, want)
			}
			for i, s := range []*Screen{e.MainScreen(), e.AltScreen()} {
				if got, want := s.ScrollRegion(), uv.Rect(0, 1, w, 4); got != want {
					t.Errorf("screen %d's ScrollRegion: got = %v, want = %v", i, got, want)
				}
			}
		})
		t.Run(sw.name+" back to the main screen", func(t *testing.T) {
			e := newTestTerminal(t, w, h)
			mustWrite(t, e, labelRows("m", h)+sw.on+"\x1b[3;6r", true)
			mustWrite(t, e, sw.off+"\x1b[6;1H\n", false)
			if got, want := screenText(e.MainScreen()), []string{"m1", "m2", "m4", "m5", "m6", "", "m7", "m8"}; !slices.Equal(got, want) {
				t.Errorf("main screen: got = %q, want = %q, rows 3-6 scrolled", got, want)
			}
			if n := e.ScrollbackLen(); n != 0 {
				t.Errorf("history: got = %q, want none", lineTexts(e.Scrollback().Lines()))
			}
		})
	}
}

// Both resets of the margins reach the one region, whichever screen shows
// when they come: DECSTR's ReallyReset(xw, False, False) runs
// resetMarginMode, and ScreenResize runs resetMargins.
func TestDECSTRAndAResizeResetTheOneRegion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reset func(*Emulator)
		w     int
	}{
		{"DECSTR", func(e *Emulator) { _, _ = e.WriteString("\x1b[!p") }, 10},
		{"a resize", func(e *Emulator) { e.Resize(12, 8) }, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 8)
			mustWrite(t, e, "\x1b[2;5r\x1b[?1049h", true)
			tc.reset(e)
			mustWrite(t, e, "\x1b[?1049l", false)
			for i, s := range []*Screen{e.MainScreen(), e.AltScreen()} {
				if got, want := s.ScrollRegion(), uv.Rect(0, 0, tc.w, 8); got != want {
					t.Errorf("screen %d's ScrollRegion: got = %v, want = %v", i, got, want)
				}
			}
		})
	}
}

// ReallyReset(xw, True, …), RIS, leaves the alternate screen with
// FromAlternate(xw, True), which clears it first only if it shows, and then
// clears the main screen and saves the cursor there, home (CursorSave on
// sc[0]). The alternate screen's slot, sc[1], is never touched, and an
// alternate screen that wasn't showing keeps its cells.
func TestRISLeavesTheAlternateScreen(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		rows     []string // the alternate screen's, after RIS
	}{
		{"from the alternate screen", "\x1b[?47h\x1b[2;3H\x1b7TEXT\x1bc", []string{"", "", ""}},
		{"from the main screen", "\x1b[?47h\x1b[2;3H\x1b7TEXT\x1b[?47l\x1bc", []string{"", "  TEXT", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 3)
			mustWrite(t, e, tc.in, false)
			if got := e.CursorPosition(); got != uv.Pos(0, 0) || e.PendingWrap() {
				t.Errorf("cursor: got = %v PendingWrap %v, want home with no wrap", got, e.PendingWrap())
			}
			if s := e.AltScreen().SavedCursor(); s.Position != uv.Pos(2, 1) || !s.Saved.Saved {
				t.Errorf("alternate screen's slot: got = %s, want (2,1), saved", slot(s))
			}
			if got := screenText(e.AltScreen()); !slices.Equal(got, tc.rows) {
				t.Errorf("alternate screen: got = %q, want = %q", got, tc.rows)
			}
		})
	}
}
