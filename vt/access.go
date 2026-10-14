package vt

import (
	"maps"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// The accessors below expose terminal state that a caller needs to reproduce
// the screen elsewhere, for example to snapshot it and replay it into another
// terminal. They are not synchronized; see [SafeEmulator].

// MainScreen returns the main screen, whichever screen is active.
func (e *Emulator) MainScreen() *Screen {
	return &e.scrs[0]
}

// AltScreen returns the alternate screen, whichever screen is active.
func (e *Emulator) AltScreen() *Screen {
	return &e.scrs[1]
}

// ActiveScreen returns the screen output is drawn on.
func (e *Emulator) ActiveScreen() *Screen {
	return e.scr
}

// SavedCursor returns the cursor saved by DECSC on this screen, with what
// DECSC saved alongside it in Saved. A slot no DECSC has filled reports what
// DECRC restores from it, with Saved.Saved false: home, the default pen and
// no link, ASCII in G0-G3, G0 in GL and G2 in GR, no origin mode and no wrap.
func (s *Screen) SavedCursor() Cursor {
	c := s.saved
	if !c.Saved.Saved {
		c.Saved = unsavedState
	}
	return c
}

// Line returns row y of the screen, or nil outside it. The line is the
// screen's own storage and changes with the next Write.
func (s *Screen) Line(y int) uv.Line {
	return s.buf.Line(y)
}

// PendingWrap reports whether a wrap is pending, so the next printed
// character wraps to the next line first. A character that reaches the last
// column leaves one, and, as in xterm, CBT keeps it as it moves the cursor
// left. Like the cursor, the wrap is the terminal's, whichever screen shows.
func (e *Emulator) PendingWrap() bool {
	return e.atPhantom
}

// AtBoundary reports whether the bytes written so far end between sequences:
// the parser holds no partial escape sequence, control string or UTF-8 rune.
func (e *Emulator) AtBoundary() bool {
	return e.parser.State() == parser.GroundState
}

// Partial returns how many of the last bytes written belong to the sequence
// the parser holds unfinished, counted across Writes; zero at a boundary.
func (e *Emulator) Partial() int {
	return e.partial
}

// GroundPrefix returns the length of the longest prefix of b after which the
// parser, continuing from where the bytes written so far left it, would be
// at a boundary (see [Emulator.AtBoundary]). It writes nothing and changes
// nothing.
func (e *Emulator) GroundPrefix(b []byte) int {
	return e.parser.GroundPrefix(b)
}

// HandoffPrefix returns the length of the longest prefix of b that ends at a
// boundary right after an action that isn't a print: a control executed, or
// a sequence or string dispatched. Each of those ends the cluster being
// printed, so after that prefix the emulator carries none that a later byte
// could extend (see [Emulator.Carrying]). A string CAN or SUB aborts, and a
// CSI the parser ignores, end nothing and don't count; neither does REP,
// which prints. It is 0 if no prefix does, and like GroundPrefix it writes
// nothing and changes nothing.
func (e *Emulator) HandoffPrefix(b []byte) int {
	return e.parser.HandoffPrefix(b)
}

// Carrying reports whether the emulator holds a cluster that the next Write
// could still extend: the last Write ended in printing, and nothing but
// printing has happened since. A mark at the start of the next Write then
// joins that cluster, as it would have arriving in the same Write. A cluster
// dropped for having no base counts: what joins it is dropped with it, or
// printed with it when it gives the cluster a width. So does one too wide
// for the line, which wasn't printed: what joins it isn't printed either,
// where a spacing mark on its own would be.
func (e *Emulator) Carrying() bool {
	// Between Writes the cursor and the screen are where the cluster left
	// them, so of resumeCluster's conditions only the bound on its length
	// and the rune that comes next can refuse it.
	return e.carry.valid && len(e.carry.content) < maxCarriedCluster
}

// Mode returns the setting of a mode, or [ansi.ModeNotRecognized] for a mode
// the program never set and the emulator has no default for.
func (e *Emulator) Mode(m ansi.Mode) ansi.ModeSetting {
	return e.modes[m]
}

// Modes returns a copy of every mode setting the emulator holds.
func (e *Emulator) Modes() ansi.Modes {
	return maps.Clone(e.modes)
}

// Charsets reports the final byte of the SCS sequence designating each of
// G0-G3 ('B' for ASCII, '0' for DEC special graphics, 'A' for UK), and which
// of them GL and GR invoke.
func (e *Emulator) Charsets() (designations [4]byte, gl, gr int) {
	for i, d := range e.designations {
		if d == 0 {
			d = 'B'
		}
		designations[i] = d
	}
	return designations, e.gl, e.gr
}

// SingleShift reports the character set a single shift (SS2 or SS3) selects
// for the next character printed: 2 or 3, or 0 when none is pending.
func (e *Emulator) SingleShift() int {
	return e.gsingle
}

// TabStops returns the columns that have a tab stop, ascending. The slice is
// the caller's.
func (e *Emulator) TabStops() []int {
	// The scan is as wide as the screen, and not charged to the work meter:
	// it runs outside Write and Resize, at the caller's pace, over a width
	// only the caller sets.
	var stops []int
	for x := range e.tabstops.Width() {
		if e.tabstops.IsStop(x) {
			stops = append(stops, x)
		}
	}
	return stops
}

// LastChar returns the character [ansi.REP] repeats: the last one printed
// whose input was a single byte of width 1, taken before the character set
// maps it, so it is always ASCII. It is 0 if none has been printed since the
// emulator started or was last reset with RIS; DECSTR leaves it.
func (e *Emulator) LastChar() rune {
	return e.lastChar
}
