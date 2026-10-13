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
