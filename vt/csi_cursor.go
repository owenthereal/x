package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
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
		ts := e.tabstops.Next(x)
		if ts < x {
			break
		}
		x = ts
	}

	if x >= scroll.Max.X {
		x = min(scroll.Max.X-1, e.Width()-1)
	}

	// NOTE: We use t.scr.setCursor here because we don't want to reset the
	// phantom state.
	e.scr.setCursor(x, y, false)
}

// prevTab moves the cursor to the previous tab stop n times. This respects the
// horizontal scrolling region when origin mode is set. If the cursor would
// move past the leftmost valid column, the cursor remains at the leftmost
// valid column and the operation completes.
func (e *Emulator) prevTab(n int) {
	x, _ := e.scr.CursorPosition()
	from := x
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
	// phantom state.
	e.scr.setCursorX(x, false)
	if x != from {
		// A cursor that left the last column has no wrap pending.
		e.atPhantom = false
	}
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

// carriageReturn moves the cursor to the leftmost column. If [ansi.DECOM] is
// set, the cursor is set to the left margin. If not, and the cursor is on or
// to the right of the left margin, the cursor is set to the left margin.
// Otherwise, the cursor is set to the leftmost column of the screen.
// This performs the same function as [ansi.CR].
func (e *Emulator) carriageReturn() {
	mode, ok := e.modes[ansi.DECOM]
	margins := ok && mode.IsSet()
	x, y := e.scr.CursorPosition()
	if margins {
		e.scr.setCursor(0, y, true)
	} else if region := e.scr.ScrollRegion(); uv.Pos(x, y).In(region) {
		e.scr.setCursor(region.Min.X, y, false)
	} else {
		e.scr.setCursor(0, y, false)
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
