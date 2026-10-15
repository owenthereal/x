package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/owenthereal/x/ansi"
)

// handleEsc handles an escape sequence.
func (e *Emulator) handleEsc(cmd ansi.Cmd) {
	e.flushGrapheme() // Flush any pending grapheme before handling ESC sequences.
	if !e.handlers.handleEsc(int(cmd)) {
		var str string
		if inter := cmd.Intermediate(); inter != 0 {
			str += string(inter) + " "
		}
		if final := cmd.Final(); final != 0 {
			str += string(final)
		}
		e.logf("unhandled sequence: ESC %q", str)
	}
}

// fullReset performs a full terminal reset as in [ansi.RIS]. As in xterm, it
// clears the history too, which a soft reset keeps.
func (e *Emulator) fullReset() {
	// xterm's RIS leaves the alternate screen with FromAlternate(xw, True),
	// which clears it first only if it shows, and resets the main screen's
	// cursor and DECSC slot alone: an alternate screen that isn't showing
	// keeps its cells, and either way it keeps its slot.
	if e.IsAltScreen() {
		e.clearScreen()
		e.setAltScreenMode(false)
	}
	e.scrs[0].Reset()
	e.scrs[0].clearHistory()
	e.resetTabStops()

	// XXX: Do we reset all modes here? Investigate.
	e.resetModes()

	e.resetCharsets()
	e.atPhantom = false
	e.grapheme = e.grapheme[:0]
	e.carry.valid = false
	e.lastChar = 0
}

// softReset performs a soft terminal reset as in [ansi.DECSTR], following
// xterm's ReallyReset(xw, False, False): the cursor stays where it is, with
// its pending wrap, and the modes, margins, cursor shape, pen and charsets a
// program may have left behind go back to their defaults. The saved cursor is
// then what DECSC saves of that, moved home.
func (e *Emulator) softReset() {
	x, y := e.scr.CursorPosition()
	phantom := e.atPhantom
	e.setMode(ansi.ModeTextCursorEnable, ansi.ModeSet)
	e.setMode(ansi.ModeInsertReplace, ansi.ModeReset)
	e.setMode(ansi.ModeOrigin, ansi.ModeReset)
	e.setMode(ansi.ModeAutoWrap, ansi.ModeSet)
	e.setMode(ansi.ModeCursorKeys, ansi.ModeReset)
	e.setMode(ansi.ModeNumericKeypad, ansi.ModeReset)
	e.setMode(ansi.ModeLeftRightMargin, ansi.ModeReset)
	e.scr.setVerticalMargins(0, e.Height())
	e.scr.setHorizontalMargins(0, e.Width())
	e.scr.setCursorStyle(CursorBlock, true)
	e.scr.cur.Pen = uv.Style{}
	e.resetCharsets()

	// Resetting DECOM homes the cursor; DECSTR leaves it, and its wrap,
	// which the slot saves too.
	e.scr.setCursor(x, y, false)
	e.atPhantom = phantom
	e.saveCursor()
	e.scr.saved.Position = uv.Position{}
}
