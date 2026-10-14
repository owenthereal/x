package vt

import (
	"fmt"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The tests below encode xterm 412 (XTERM_DATE 2026-10-11): cursor.c's
// CursorSave2, which fills a DECSC slot, and CursorRestoreFlags, which DECRC
// restores it with; charproc.c's resetCharsets, the charsets' defaults; and
// charproc.c's ReallyReset, which DECSTR runs as ReallyReset(xw, False,
// False).

// saveRestore is each pair of sequences that fills the active screen's DECSC
// slot and restores it. xterm's CASE_DECSC and srm_SAVE_CURSOR (?1048h) call
// CursorSave, CASE_DECRC and ?1048l CursorRestore, and so does CASE_ANSI_SC
// (CSI s while DECLRMM is reset) for SCOSC; x/vt has no SCORC, so DECRC
// restores what SCOSC saved.
var saveRestore = []struct{ name, save, restore string }{
	{"DECSC", "\x1b7", "\x1b8"},
	{"?1048", "\x1b[?1048h", "\x1b[?1048l"},
	{"SCOSC", "\x1b[s", "\x1b8"},
}

// defaultCharsets is what resetCharsets designates (charproc.c:1271-1274,
// with UTF-8, where G2 and G3 get ASCII too).
var defaultCharsets = [4]byte{'B', 'B', 'B', 'B'}

// slot formats a cursor for a failure message, which %v alone can't: Cursor's
// String is its embedded Position's.
func slot(c Cursor) string {
	return fmt.Sprintf("%v pen %+v link %+v hidden %v style %v steady %v %+v", c.Position, c.Pen, c.Link, c.Hidden, c.Style, c.Steady, c.Saved)
}

// CursorRestoreFlags restores the charsets G0-G3 a save holds
// (restoreCharsets, a copy of all four), whatever was designated since.
func TestDECRCRestoresTheCharsets(t *testing.T) {
	for _, p := range saveRestore {
		t.Run(p.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 2)
			_, _ = e.WriteString("\x1b(0\x1b)A\x1b*0\x1b+A" + p.save + "\x1b(B\x1b)B\x1b*B\x1b+B" + p.restore)
			if g, _, _ := e.Charsets(); g != [4]byte{'0', 'A', '0', 'A'} {
				t.Errorf("Charsets: got = %q, want = %q", g, "0A0A")
			}
			_, _ = e.WriteString("q")
			if c := e.CellAt(0, 0); c == nil || c.Content != "─" {
				t.Errorf("cell (0,0): got = %+v, want the line-drawing ─", c)
			}
		})
	}
}

// CursorRestoreFlags restores curgl and curgr with the charsets: SO put G1 in
// GL and LS3R G3 in GR before the save, so after SI, LS2R and the restore "q"
// maps through G1 again.
func TestDECRCRestoresTheShift(t *testing.T) {
	for _, p := range saveRestore {
		t.Run(p.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 2)
			_, _ = e.WriteString("\x1b)0\x0e\x1b|" + p.save + "\x0f\x1b}" + p.restore)
			if _, gl, gr := e.Charsets(); gl != 1 || gr != 3 {
				t.Errorf("Charsets: got = GL %d GR %d, want = GL 1 GR 3", gl, gr)
			}
			_, _ = e.WriteString("q")
			if c := e.CellAt(0, 0); c == nil || c.Content != "─" {
				t.Errorf("cell (0,0): got = %+v, want the line-drawing ─", c)
			}
		})
	}

	// curss is not in the slot, and the saved branch of CursorRestoreFlags
	// leaves it: SS2 sent before the restore still shifts the next character,
	// through the G2 the restore brought back.
	t.Run("a pending single shift", func(t *testing.T) {
		e := newTestTerminal(t, 10, 2)
		_, _ = e.WriteString("\x1b*0\x1b7\x1b*B\x1bN\x1b8")
		if got := e.SingleShift(); got != 2 {
			t.Errorf("SingleShift after DECRC: got = %d, want = 2", got)
		}
		_, _ = e.WriteString("qq")
		if got := rowText(e, 0); got != "─q" {
			t.Errorf("row 0: got = %q, want = %q", got, "─q")
		}
	})
}

// CursorRestoreFlags puts ORIGIN back from the slot (DECSC_FLAGS) as a flag,
// without the homing DECSET 6 does, and then moves with CursorSet under it.
func TestDECRCRestoresOriginMode(t *testing.T) {
	for _, p := range saveRestore {
		t.Run(p.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 10)
			_, _ = e.WriteString("\x1b[?6h\x1b[3;4H" + p.save + "\x1b[?6l\x1b[8;8H" + p.restore)
			if got := e.Mode(ansi.ModeOrigin); got != ansi.ModeSet {
				t.Errorf("DECOM: got = %v, want = %v", got, ansi.ModeSet)
			}
			if pos := e.CursorPosition(); pos != uv.Pos(3, 2) {
				t.Errorf("cursor: got = %v, want the slot's (3,2)", pos)
			}
		})
	}

	// Under ORIGIN, CursorRestoreFlags passes CursorSet the slot's position
	// relative to the margins, and CursorSet adds the margins back and clamps
	// it to [0, bot_marg] and [0, rgt_marg] (cursor.c CursorSet): a slot below
	// or right of the margins comes back at their edge, one above them stays
	// above. Without ORIGIN only the screen bounds it.
	for _, tc := range []struct {
		name, in string
		want     uv.Position
	}{
		{"below and right of the margins", "\x1b[?6h\x1b[9;9H\x1b7\x1b[?69h\x1b[3;6s\x1b[3;6r\x1b8", uv.Pos(5, 5)},
		{"above the margins", "\x1b[?6h\x1b7\x1b[3;6r\x1b8", uv.Pos(0, 0)},
		{"without origin mode", "\x1b[9;9H\x1b7\x1b[?69h\x1b[3;6s\x1b[3;6r\x1b8", uv.Pos(8, 8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 10)
			_, _ = e.WriteString(tc.in)
			if pos := e.CursorPosition(); pos != tc.want {
				t.Errorf("cursor: got = %v, want = %v", pos, tc.want)
			}
		})
	}
}

// CursorSave2 saves do_wrap as wrap_flag, and CursorRestoreFlags sets it back
// after CursorSet, whose ResetWrap clears it ("after CursorSet/ResetWrap",
// cursor.c): the "y" after the restore wraps to the next row first.
func TestDECRCRestoresThePendingWrap(t *testing.T) {
	for _, p := range saveRestore {
		t.Run(p.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 3)
			_, _ = e.WriteString("0123456789" + p.save + "\x1b[1;1H" + p.restore)
			if !e.PendingWrap() {
				t.Error("PendingWrap after the restore: got = false, want = true")
			}
			if pos := e.CursorPosition(); pos != uv.Pos(9, 0) {
				t.Errorf("cursor after the restore: got = %v, want = (9,0)", pos)
			}
			_, _ = e.WriteString("y")
			if got := rowText(e, 0); got != "0123456789" {
				t.Errorf("row 0: got = %q, want = %q", got, "0123456789")
			}
			if got := rowText(e, 1); got != "y" {
				t.Errorf("row 1: got = %q, want = %q", got, "y")
			}
		})
	}
}

// xterm's ?1049h is CursorSave, then the switch and a clear, and ?1049l is
// FromAlternate, then CursorRestore (srm_OPT_ALTBUF_CURSOR, charproc.c), so
// the main screen's slot brings back the charsets, the shift, origin mode and
// the wrap the alternate screen changed.
func Test1049SavesAndRestoresTheCursorState(t *testing.T) {
	e := newTestTerminal(t, 10, 3)
	_, _ = e.WriteString("\x1b)0\x0e0123456789\x1b[?1049h")
	// The alternate screen's own DECSC fills its own slot, which ?1049l, back
	// on the main screen, doesn't read.
	_, _ = e.WriteString("\x1b)B\x0f\x1b[?6h\x1b(A\x1b7\x1b[?1049l")
	if e.IsAltScreen() {
		t.Fatal("still on the alternate screen after ?1049l")
	}
	if g, gl, gr := e.Charsets(); g != [4]byte{'B', '0', 'B', 'B'} || gl != 1 || gr != 2 {
		t.Errorf("Charsets: got = %q GL %d GR %d, want = %q GL 1 GR 2", g, gl, gr, "B0BB")
	}
	if got := e.Mode(ansi.ModeOrigin); got != ansi.ModeReset {
		t.Errorf("DECOM: got = %v, want = %v", got, ansi.ModeReset)
	}
	if !e.PendingWrap() || e.CursorPosition() != uv.Pos(9, 0) {
		t.Errorf("cursor: got = %v PendingWrap %v, want = (9,0) with the wrap pending", e.CursorPosition(), e.PendingWrap())
	}
	_, _ = e.WriteString("q")
	if got := rowText(e, 1); got != "─" {
		t.Errorf("row 1: got = %q, want = %q", got, "─")
	}

	// FromAlternate does nothing on the main screen, and CursorRestore still
	// runs.
	t.Run("on the main screen", func(t *testing.T) {
		e := newTestTerminal(t, 10, 3)
		_, _ = e.WriteString("\x1b(0\x1b[2;3H\x1b7\x1b(B\x1b[1;1H\x1b[?1049l")
		if g, _, _ := e.Charsets(); g != [4]byte{'0', 'B', 'B', 'B'} {
			t.Errorf("Charsets: got = %q, want = %q", g, "0BBB")
		}
		if pos := e.CursorPosition(); pos != uv.Pos(2, 1) {
			t.Errorf("cursor: got = %v, want = (2,1)", pos)
		}
	})
}

// A slot no DECSC has filled holds what VTRealize's CursorSave of both
// buffers put there at start (charproc.c:13066-13069), after resetCharsets:
// home, no attributes, ASCII in G0-G3, G0 in GL and G2 in GR, origin mode
// off and no wrap. The link is x/vt's (xterm has none), and a slot never
// saved has none either. The resetCharsets branch of CursorRestoreFlags,
// for a slot with saved unset, is never taken for the VT100 window.
func TestDECRCWithNoSaveRestoresTheDefaults(t *testing.T) {
	const changed = "\x1b(0\x1b)A\x1b*0\x1b+A\x0e\x1b|\x1b[1;31m\x1b]8;;https://example.com\x07\x1b[?6h0123456789"
	for _, restore := range []string{"\x1b8", "\x1b[?1048l"} {
		t.Run(restore, func(t *testing.T) {
			e := newTestTerminal(t, 10, 3)
			_, _ = e.WriteString(changed + restore)
			c := e.scr.Cursor()
			if c.Position != uv.Pos(0, 0) {
				t.Errorf("cursor: got = %v, want home", c.Position)
			}
			if e.PendingWrap() {
				t.Error("PendingWrap: got = true, want = false")
			}
			if !c.Pen.IsZero() || !c.Link.IsZero() {
				t.Errorf("pen and link: got = %+v %+v, want neither", c.Pen, c.Link)
			}
			if got := e.Mode(ansi.ModeOrigin); got != ansi.ModeReset {
				t.Errorf("DECOM: got = %v, want = %v", got, ansi.ModeReset)
			}
			if g, gl, gr := e.Charsets(); g != defaultCharsets || gl != 0 || gr != 2 {
				t.Errorf("Charsets: got = %q GL %d GR %d, want = %q GL 0 GR 2", g, gl, gr, defaultCharsets)
			}
			_, _ = e.WriteString("q")
			if c := e.CellAt(0, 0); c == nil || c.Content != "q" {
				t.Errorf("cell (0,0): got = %+v, want q", c)
			}
		})
	}

	// RIS ends with CursorSave after resetCharsets (ReallyReset, full), so
	// what was saved before it is gone.
	t.Run("after RIS", func(t *testing.T) {
		e := newTestTerminal(t, 10, 3)
		_, _ = e.WriteString("\x1b(0\x1b[2;3H\x1b7\x1bc\x1b(0\x1b8")
		if g, _, _ := e.Charsets(); g != defaultCharsets {
			t.Errorf("Charsets: got = %q, want = %q", g, defaultCharsets)
		}
		if pos := e.CursorPosition(); pos != uv.Pos(0, 0) {
			t.Errorf("cursor: got = %v, want home", pos)
		}
	})

	// That slot was saved too, so restoring it leaves curss.
	t.Run("a pending single shift", func(t *testing.T) {
		e := newTestTerminal(t, 10, 3)
		_, _ = e.WriteString("\x1bN\x1b8")
		if got := e.SingleShift(); got != 2 {
			t.Errorf("SingleShift: got = %d, want = 2", got)
		}
	})
}

// SavedCursor reports what CursorSave2 put in the slot, on each screen: the
// position, the pen and link, and the charsets, GL and GR, origin mode and
// the wrap. The active cursor carries none of it.
func TestSavedCursorReportsTheSavedState(t *testing.T) {
	e := newTestTerminal(t, 10, 3)
	unsaved := Cursor{Saved: SavedState{Designations: defaultCharsets, GR: 2}}
	if got := e.MainScreen().SavedCursor(); got != unsaved {
		t.Errorf("a slot never saved: got = %s, want = %s", slot(got), slot(unsaved))
	}

	_, _ = e.WriteString("\x1b(A\x1b)0\x0e\x1b|\x1b[1m\x1b]8;;https://example.com\x07\x1b[?6h\x1b[2;3H")
	want := e.scr.Cursor()
	want.Saved = SavedState{Designations: [4]byte{'A', '0', 'B', 'B'}, GL: 1, GR: 3, Origin: true, Saved: true}
	_, _ = e.WriteString("\x1b7")
	if got := e.MainScreen().SavedCursor(); got != want {
		t.Errorf("after DECSC: got = %s, want = %s", slot(got), slot(want))
	}
	if got := e.scr.Cursor().Saved; got != (SavedState{}) {
		t.Errorf("the active cursor's Saved after DECSC: got = %+v, want none", got)
	}

	_, _ = e.WriteString("\x1b[?6l\x1b[1;1H0123456789\x1b7")
	if s := e.MainScreen().SavedCursor().Saved; !s.Wrap || s.Origin {
		t.Errorf("after DECSC with a wrap pending: got = %+v, want the wrap and no origin mode", s)
	}

	_, _ = e.WriteString("\x1b[?1049h\x1b[2;2H\x1b7")
	if s := e.AltScreen().SavedCursor(); s.Position != uv.Pos(1, 1) || !s.Saved.Saved {
		t.Errorf("the alternate screen's slot: got = %s, want (1,1), saved", slot(s))
	}
	if s := e.MainScreen().SavedCursor().Saved; !s.Wrap || !s.Saved {
		t.Errorf("the main screen's slot after ?1049h: got = %+v, want the wrap, saved", s)
	}

	_, _ = e.WriteString("\x1b8")
	if got := e.scr.Cursor().Saved; got != (SavedState{}) {
		t.Errorf("the active cursor's Saved after DECRC: got = %+v, want none", got)
	}
}

// resetCharsets puts G2 in GR (curgr = 2, charproc.c:1278). xterm runs it at
// start (VTRealize), and ReallyReset runs it for RIS and DECSTR.
func TestCharsetsReportsGRAsG2(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"fresh", ""},
		{"after RIS", "\x1b|\x1bc"},
		{"after DECSTR", "\x1b|\x1b[!p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 2)
			_, _ = e.WriteString(tc.in)
			if _, _, gr := e.Charsets(); gr != 2 {
				t.Errorf("GR: got = %d, want = 2", gr)
			}
		})
	}
}

// ESC N and ESC O are SS2 and SS3 in 7 bits, CASE_SS2 and CASE_SS3 as 0x8E
// and 0x8F are (charproc.c), setting curss; WriteNow prints the next
// character from gsets[curss] and clears it, so the one after maps through
// GL again.
func TestESCNAndESCOShiftTheNextCharacter(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     int
	}{
		{"SS2", "\x1b*0\x1bN", 2},
		{"SS3", "\x1b+0\x1bO", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 2)
			if got := e.SingleShift(); got != 0 {
				t.Errorf("SingleShift fresh: got = %d, want = 0", got)
			}
			_, _ = e.WriteString(tc.in)
			if got := e.SingleShift(); got != tc.want {
				t.Errorf("SingleShift before the character: got = %d, want = %d", got, tc.want)
			}
			_, _ = e.WriteString("qq")
			if got := e.SingleShift(); got != 0 {
				t.Errorf("SingleShift after it: got = %d, want = 0", got)
			}
			if got := rowText(e, 0); got != "─q" {
				t.Errorf("row 0: got = %q, want = %q", got, "─q")
			}
		})
	}
}

// DECSTR is ReallyReset(xw, False, False) (CASE_DECSTR, charproc.c), and
// each expected field below is what that function leaves.
func TestDECSTRIsXtermsSoftReset(t *testing.T) {
	// resetMarginMode clears LEFT_RIGHT (?69) and resets the margins, and
	// InitCursorShape and SetCursorBlink(cursor_blink_i) put the cursor's
	// shape and blink back to what they started as: a blinking block in
	// x/vt.
	t.Run("margin mode and the cursor's shape", func(t *testing.T) {
		e := newTestTerminal(t, 10, 5)
		_, _ = e.WriteString("\x1b[?69h\x1b[3;6s\x1b[2;4r\x1b[4 q\x1b[!p")
		if got := e.Mode(ansi.ModeLeftRightMargin); got != ansi.ModeReset {
			t.Errorf("DECLRMM: got = %v, want = %v", got, ansi.ModeReset)
		}
		if r := e.scr.ScrollRegion(); r != uv.Rect(0, 0, 10, 5) {
			t.Errorf("scroll region: got = %v, want the whole screen", r)
		}
		if style, blink := e.CursorStyle(); style != CursorBlock || !blink {
			t.Errorf("cursor style: got = %v blink %v, want a blinking block", style, blink)
		}
	})

	// The soft branch ends with CursorSave, then sets the slot's row and col
	// to 0. ORIGIN, the pen and the charsets were reset before it, and
	// do_wrap wasn't, so the slot holds the reset pen and charsets, origin
	// mode off and the wrap as it was, at home; DECRC then leaves a wrap
	// pending at column 0.
	t.Run("the saved cursor", func(t *testing.T) {
		e := newTestTerminal(t, 10, 5)
		_, _ = e.WriteString("\x1b(0\x1b)0\x0e\x1b|\x1b[1m\x1b[?6h\x1b[2;2H\x1b7\x1b[3;1H0123456789\x1b[!p")
		want := Cursor{Saved: SavedState{Designations: defaultCharsets, GR: 2, Wrap: true, Saved: true}}
		if got := e.MainScreen().SavedCursor(); got != want {
			t.Errorf("SavedCursor: got = %s, want = %s", slot(got), slot(want))
		}
		if !e.PendingWrap() || e.CursorPosition() != uv.Pos(9, 2) {
			t.Errorf("cursor: got = %v PendingWrap %v, want (9,2) with the wrap pending", e.CursorPosition(), e.PendingWrap())
		}
		_, _ = e.WriteString("\x1b8y")
		if c := e.CellAt(0, 1); c == nil || c.Content != "y" {
			t.Errorf("cell (0,1) after DECRC: got = %+v, want y", c)
		}
	})
}
