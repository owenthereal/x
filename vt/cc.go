package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/owenthereal/x/ansi"
)

// handleControl handles a control character.
func (e *Emulator) handleControl(r byte) {
	e.flushGrapheme() // Flush any pending grapheme before handling control codes.
	if !e.handleCc(r) {
		e.logf("unhandled sequence: ControlCode %q", r)
	}
}

// linefeed is the same as [index], except that it respects [ansi.LNM] mode.
func (e *Emulator) linefeed() {
	e.index()
	if e.isModeSet(ansi.ModeLineFeedNewLine) {
		e.carriageReturn()
	}
}

// index moves the cursor down one line, scrolling up if necessary. This
// always resets the phantom state i.e. pending wrap state.
func (e *Emulator) index() {
	x, y := e.scr.CursorPosition()
	scroll := e.scr.ScrollRegion()
	// At the bottom margin, the scroll sends the top line to history when
	// the region starts at the first row and spans every column (see
	// [Screen.ScrollUp]).
	if y == scroll.Max.Y-1 && x >= scroll.Min.X && x < scroll.Max.X {
		e.scr.ScrollUp(1)
	} else if y < scroll.Max.Y-1 || !uv.Pos(x, y).In(scroll) {
		e.scr.moveCursor(0, 1)
	}
	e.atPhantom = false
}

// horizontalTabSet sets a horizontal tab stop at the current cursor position.
// A screen with no columns has no column to set it at.
func (e *Emulator) horizontalTabSet() {
	if x, _ := e.scr.CursorPosition(); e.Width() > 0 {
		e.tabstops.Set(x)
	}
}

// reverseIndex moves the cursor up one line, or scrolling down. This always
// resets the phantom state i.e. pending wrap state, as xterm's RevIndex ends
// in CursorUp, which resets it.
func (e *Emulator) reverseIndex() {
	x, y := e.scr.CursorPosition()
	scroll := e.scr.ScrollRegion()
	if y == scroll.Min.Y && x >= scroll.Min.X && x < scroll.Max.X {
		e.scr.ScrollDown(1)
	} else {
		e.scr.moveCursor(0, -1)
	}
	e.atPhantom = false
}

// backspace moves the cursor back one cell, if possible.
func (e *Emulator) backspace() {
	// This acts like [ansi.CUB]
	e.moveCursor(-1, 0)
}
