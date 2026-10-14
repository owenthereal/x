package vt

import uv "github.com/charmbracelet/ultraviolet"

// CursorStyle represents a cursor style.
type CursorStyle int

// Cursor styles.
const (
	CursorBlock CursorStyle = iota
	CursorUnderline
	CursorBar
)

// SavedState is what DECSC saves alongside the cursor, as xterm's
// CursorSave2 does.
type SavedState struct {
	Designations [4]byte // the SCS final for G0-G3 ('B' ASCII, '0' DEC graphics, 'A' UK)
	GL, GR       int
	Origin       bool // DECOM
	Wrap         bool // the pending wrap (xterm's wrap_flag)
	Saved        bool // a DECSC has filled this slot
}

// unsavedState is what a slot no DECSC has filled restores, as xterm's slots
// hold from the CursorSave it does for each at start, after resetCharsets.
var unsavedState = SavedState{Designations: [4]byte{'B', 'B', 'B', 'B'}, GR: 2}

// Cursor represents a cursor in a terminal.
type Cursor struct {
	Pen  uv.Style
	Link uv.Link

	uv.Position

	Style  CursorStyle
	Steady bool // Not blinking
	Hidden bool

	// Saved is what DECSC saved with the cursor, filled only in a screen's
	// saved slot (see [Screen.SavedCursor]).
	Saved SavedState
}
