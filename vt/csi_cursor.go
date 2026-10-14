package vt

import (
	"github.com/charmbracelet/x/ansi"
)

// nextTab moves the cursor to the next tab stop n times. This respects the
// horizontal scrolling region. This performs the same function as [ansi.CHT].
func (e *Emulator) nextTab(n int) {
	x, y := e.scr.CursorPosition()
	scroll := e.scr.ScrollRegion()
	// Each tab moves the cursor at least a column, until it is in the last,
	// where a tab leaves it. A count past the columns to its right moves it
	// no further, and would only spin there.
	n = min(n, e.Width()-1-x)
	// The tabs scan the columns to the right at most once between them,
	// however few stops there are, and each takes a step of the loop.
	if !e.meter.spend(max(e.Width()-1-x+n, 1)) {
		return
	}
	for range n {
		x = e.nextTabStop(x)
	}

	if x >= scroll.Max.X {
		x = min(scroll.Max.X-1, e.Width()-1)
	}

	// NOTE: We use t.scr.setCursor here because we don't want to reset the
	// phantom state.
	e.scr.setCursor(x, y, false)
}

// nextTabStop returns the first tab stop right of column x on the screen, or
// the last column if none is left. The stops past the screen's right edge,
// which a narrowing keeps, are not on it, so the scan stops at the edge.
func (e *Emulator) nextTabStop(x int) int {
	for last := e.Width() - 1; x < last; {
		x++
		if e.tabstops.IsStop(x) {
			break
		}
	}
	return x
}

// prevTab moves the cursor to the previous tab stop n times. This respects the
// horizontal scrolling region when origin mode is set. If the cursor would
// move past the leftmost valid column, the cursor remains at the leftmost
// valid column and the operation completes.
func (e *Emulator) prevTab(n int) {
	x, _ := e.scr.CursorPosition()
	leftmargin := 0
	scroll := e.scr.ScrollRegion()
	if e.isModeSet(ansi.DECOM) {
		leftmargin = scroll.Min.X
	}

	// Each tab moves the cursor at least a column, until it is in the first,
	// where a tab leaves it: a count past the columns to its left only spins.
	n = min(n, x)
	// As in nextTab: one scan of the columns to the left, and the steps.
	if !e.meter.spend(max(x+n, 1)) {
		return
	}
	for range n {
		ts := e.tabstops.Prev(x)
		if ts > x {
			break
		}
		x = ts
	}

	if x < leftmargin {
		x = leftmargin
	}

	// NOTE: We use t.scr.setCursorX here because we don't want to reset the
	// phantom state. xterm's TabToPrevStop sets only the column, so a wrap
	// pending before the tab stays, off the last column, and the next
	// character still wraps.
	e.scr.setCursorX(x, false)
}

// moveCursor moves the cursor by the given x and y deltas. If the cursor
// is at phantom, the state will reset and the cursor is back in the screen.
func (e *Emulator) moveCursor(dx, dy int) {
	e.scr.moveCursor(dx, dy)
	e.atPhantom = false
}

// setCursor sets the cursor position. This resets the phantom state.
func (e *Emulator) setCursor(x, y int) {
	e.scr.setCursor(x, y, false)
	e.atPhantom = false
}

// setCursorPosition sets the cursor position. This respects [ansi.DECOM],
// Origin Mode. This performs the same function as [ansi.CUP].
func (e *Emulator) setCursorPosition(x, y int) {
	mode, ok := e.modes[ansi.DECOM]
	margins := ok && mode.IsSet()
	e.scr.setCursor(x, y, margins)
	e.atPhantom = false
}

// cursorAddress returns the cursor's position as setCursorPosition reads
// one: relative to the top and left margins under [ansi.DECOM], as xterm's
// CursorRow and CursorCol report it, and the screen's otherwise. HPA, HPR,
// VPA and VPR keep one coordinate, or move from it, so they read it here
// rather than add the margins twice.
func (e *Emulator) cursorAddress() (x, y int) {
	x, y = e.scr.CursorPosition()
	if e.isModeSet(ansi.DECOM) {
		region := e.scr.ScrollRegion()
		x, y = x-region.Min.X, y-region.Min.Y
	}
	return x, y
}

// carriageReturn moves the cursor to the leftmost column. If [ansi.DECOM] is
// set, the cursor is set to the left margin. If not, and the cursor is on or
// to the right of the left margin, whatever its row, the cursor is set to the
// left margin. Otherwise, the cursor is set to the leftmost column of the
// screen. The row stays, as xterm's CarriageReturn sets only the column.
// This performs the same function as [ansi.CR].
func (e *Emulator) carriageReturn() {
	mode, ok := e.modes[ansi.DECOM]
	margins := ok && mode.IsSet()
	x, _ := e.scr.CursorPosition()
	if left := e.scr.ScrollRegion().Min.X; margins || x >= left {
		e.scr.setCursorX(left, false)
	} else {
		e.scr.setCursorX(0, false)
	}
	e.atPhantom = false
}

// repeatPreviousCharacter repeats the previous character n times. This is
// equivalent to typing the same character n times. This performs the same as
// [ansi.REP].
func (e *Emulator) repeatPreviousCharacter(n int) {
	if e.lastChar == 0 {
		return
	}
	// REP is not clamped, so its count is unbounded work: each repetition
	// is charged, and a stop ends the rest.
	for range n {
		if !e.meter.spend(1) {
			return
		}
		e.handlePrint(e.lastChar)
	}
}
