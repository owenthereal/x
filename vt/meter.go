package vt

// The work meter.
//
// Every operation counts its work as it goes, in cell operations: a cell
// written, copied, blanked or read, a row moved, a tab stop scanned, and each
// byte parsed. It charges a piece of work before doing it, and the stop
// function is called before a piece that would take the count since the last
// call past meterInterval. Pieces are cut to at most meterInterval, so that
// is the most work done between two calls, except for the pieces handed over
// whole (a line to history, a row of tab stops, a SetCell's scan from a
// placeholder), which are larger only on a screen more than MeterInterval
// columns wide, or with a scrollback longer than that.
//
// A check around a call into ultraviolet bounds nothing inside the call, so
// each call vt makes is bounded here: by the cells it is given, which the
// caller charges first, by a constant, or by being outside Write and Resize.
// TestNoUnmeteredUltravioletCellCall checks this list against the source: a
// call from a function its entry doesn't name, or to a function it doesn't
// have, fails it.
//
// SetCell is one cell of work, except over a wide character's placeholder:
// there it scans back toward column 0 for the character the placeholder
// belongs to, and on to column 0 when that character is gone, as DCH's moves
// leave it at every other column of a row of wide characters. So before each
// SetCell vt makes in Write or Resize, Screen.chargeScan charges a placeholder
// target its column plus one, the most that scan reads.
//
//	(*uv.Buffer).SetCell               Screen.setCells, Screen.deleteCellArea, Screen.fillArea: one cell in a metered span, and a placeholder's scan
//	(*uv.Buffer).SetCell               Buffer.InsertLineRect, Buffer.DeleteLineRect, Buffer.InsertCellRect, Buffer.DeleteCellRect: outside the meter, in vt's Buffer, which no emulator uses
//	(*uv.RenderBuffer).SetCell         Screen.SetCell: one cell, which a print or the carried cluster's undo charges with its width, and a placeholder's scan
//	(*uv.RenderBuffer).TouchLine       anywhere: a constant, once per row a charged operation touches
//	(*uv.Buffer).Bounds                anywhere: a constant
//	(*uv.Buffer).CellAt                anywhere: a constant
//	(*uv.Buffer).Height                anywhere: a constant
//	(*uv.Buffer).Line                  anywhere: a constant
//	(*uv.Buffer).Width                 anywhere: a constant
//	(*uv.Cell).Clone                   anywhere: a constant
//	(*uv.Cell).Equal                   anywhere: a constant
//	(*uv.Cell).IsZero                  anywhere: a constant
//	uv.Pos                             anywhere: a constant
//	uv.Rect                            anywhere: a constant
//	(uv.KeyMod).Contains               anywhere: a constant
//	(uv.MouseEvent).Mouse              anywhere: a constant
//	(*uv.TabStops).Set                 anywhere: a constant
//	(*uv.TabStops).Reset               anywhere: a constant
//	uv.ReadStyle                       Emulator.handleSgr: the SGR's parameters, which the parser bounds
//	(uv.TabStops).Next                 Emulator.nextTab: whole, charged with the columns right of the cursor
//	(uv.TabStops).Prev                 Emulator.prevTab: whole, charged with the columns left of the cursor
//	(*uv.TabStops).Clear               Emulator.registerDefaultCsiHandlers: whole, charged with the width (TBC 3)
//	uv.DefaultTabStops                 NewEmulator, Emulator.Resize, Emulator.resetTabStops: whole, charged with the width, outside Write in NewEmulator
//	uv.NewRenderBuffer                 NewScreen, Screen.resize: outside the meter, for a screen built from nothing, which no emulator's is once made
//	(*uv.Buffer).Render                Emulator.Render: outside Write and Resize
//	(*uv.Buffer).String                Emulator.String: outside Write and Resize
//	uv.TrimSpace                       Emulator.String: outside Write and Resize
//	screen.FillArea                    Emulator.Draw: the caller's screen, outside Write and Resize
//	(uv.Screen).SetCell                Emulator.Draw: the caller's screen, outside Write and Resize
//
// And the ultraviolet calls vt no longer makes, for doing a whole area or row
// in one call: vt does their work itself, with the same result, a span at a
// time. Screen.clearCells clears as Buffer.Clear does; Screen.fillArea fills
// and clears as Buffer.FillArea and ClearArea do, its own SetCell calls
// charged one by one; Screen.insertCellArea and Screen.deleteCellArea
// shift a row as RenderBuffer.InsertCellArea and DeleteCellArea do (a shift
// split by columns through them would come out different, since their
// SetCell mends a wide character across the split); Screen.shiftRowsDown and
// Screen.shiftRowsUp shift a narrower region's rows as
// RenderBuffer.InsertLineArea and DeleteLineArea do; Screen.reallocate
// builds its rows instead of uv.NewLine, and does the work of Buffer.Resize.
//
//	(*uv.Buffer).Clear                 none: Screen.clearCells
//	(*uv.Buffer).FillArea              none: Screen.fillArea
//	(*uv.Buffer).ClearArea             none: Screen.fillArea
//	(*uv.RenderBuffer).InsertCellArea  none: Screen.insertCellArea
//	(*uv.RenderBuffer).DeleteCellArea  none: Screen.deleteCellArea
//	(*uv.RenderBuffer).InsertLineArea  none: Screen.shiftRowsDown
//	(*uv.RenderBuffer).DeleteLineArea  none: Screen.shiftRowsUp
//	(*uv.Buffer).Resize                none: Screen.reallocate
//	uv.NewLine                         none: Screen.reallocate

import (
	"errors"
	"math"
)

// ErrStopped is returned by Write once the stop function has returned true.
// The emulator is then spent: every later Write returns ErrStopped too.
var ErrStopped = errors.New("vt: stopped")

// MeterInterval bounds the work between two calls to the stop function.
const MeterInterval = 1 << 16

// meterInterval is the interval the emulator uses: MeterInterval, which the
// package's own tests lower.
var meterInterval = MeterInterval

// byteRun is how many bytes Write charges at a time, before parsing them.
const byteRun = 256

// SetStopFunc sets fn, which the emulator calls at least once every
// MeterInterval cell operations inside any single operation, and between
// parsed bytes. Nil (the default) never stops.
//
// Once fn returns true, the operation stops where it is, Write returns
// [ErrStopped], and the emulator is spent; a Resize it stops spends it too.
// fn runs inside Write and Resize, and must not change the emulator.
func (e *Emulator) SetStopFunc(fn func() bool) {
	e.meter.stop = fn
}

// spentWork is the count of a spent meter: past any interval, so that every
// charge reaches check, and far enough below the largest int that charges
// added to it can't wrap it around.
const spentWork = math.MaxInt / 2

// meter counts the work of the emulator's operations, and calls the stop
// function between pieces of it.
type meter struct {
	stop func() bool
	// work counts the cell operations charged since stop was last called,
	// or is spentWork once it has returned true.
	work int
	// last is the work done between the last two calls to stop, which the
	// tests read.
	last int
}

// spend charges n cell operations the caller is about to do, and reports
// whether to do them: false once the stop function has returned true.
func (m *meter) spend(n int) bool {
	m.work += n
	return m.work <= meterInterval || m.check(n)
}

// check calls the stop function before the n cell operations whose charge
// took the count past the interval, and starts the count again with them.
func (m *meter) check(n int) bool {
	if m.work >= spentWork {
		m.work = spentWork
		return false
	}
	m.last = m.work - n
	m.work = n
	if m.stop != nil && m.stop() {
		m.work = spentWork
		return false
	}
	return true
}

// spent reports whether the stop function has returned true.
func (m *meter) spent() bool {
	return m.work >= spentWork
}

// spans calls do on [from, to) in spans of at most meterInterval, charging
// each span first. An empty range still costs one, for the visit. do may
// charge more as it goes, and stop part way when the meter says to. spans
// reports false, with the rest left undone, once the meter stops.
func (m *meter) spans(from, to int, do func(lo, hi int)) bool {
	if from >= to {
		return m.spend(1)
	}
	for lo := from; lo < to; {
		hi := lo + min(to-lo, meterInterval)
		if !m.spend(hi - lo) {
			return false
		}
		do(lo, hi)
		lo = hi
	}
	return !m.spent()
}
