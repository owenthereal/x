package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

// eraseCharacter erases n characters starting from the cursor position. It
// does not move the cursor. This is equivalent to [ansi.ECH].
func (e *Emulator) eraseCharacter(n int) {
	if n <= 0 {
		n = 1
	}
	x, y := e.scr.CursorPosition()
	// Nothing past the last column is erased, so a count past it does no more.
	// Filling a rectangle visits every cell it covers, on or off the screen.
	n = min(n, max(e.scr.Width()-x, 1))
	rect := uv.Rect(x, y, n, 1)
	e.scr.FillArea(e.scr.blankCell(), rect)
	e.atPhantom = false
	// ECH does not move the cursor.
}

// clearWrapInMargins clears the pending wrap after [ansi.ICH] or [ansi.DCH]
// when the cursor is inside the left and right margins, as xterm's
// InsertChar and DeleteChar do. Outside them the shift does nothing, and
// xterm returns before it resets the wrap.
func (e *Emulator) clearWrapInMargins() {
	x, _ := e.scr.CursorPosition()
	if scroll := e.scr.ScrollRegion(); x >= scroll.Min.X && x < scroll.Max.X {
		e.atPhantom = false
	}
}
