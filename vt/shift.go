package vt

import uv "github.com/charmbracelet/ultraviolet"

// The row shifts: ICH and DCH, the room an IRM print makes, and the carried
// cluster's undo of it. They follow xterm's InsertChar and DeleteChar
// (util.c) and ScrnInsertChar and ScrnDeleteChar (screen.c), where
// ultraviolet's InsertCellArea and DeleteCellArea don't:
//
//   - Only the left and right margins bound a shift. The top and bottom ones
//     play no part, so a shift acts on the cursor's row wherever it is.
//   - A wide character the shift cuts is blanked before anything moves: one
//     at the cursor, one where the cells that move part from those an insert
//     pushes past the right margin, or a delete drops, and one across the
//     right margin, whose cells inside it move. Moved apart, or with one
//     half dropped, its halves would be left without each other: a
//     placeholder with no wide character before it, which a renderer skips,
//     shifting the rest of the row left, or a wide character in the last
//     column, with no room for its second half. xterm repairs the first two
//     (DamagedCells and ClearCells), and the one across the margin only as
//     far as ClearCells reaches past the cells it blanks: after a delete,
//     and after an insert that moves nothing.
//   - The cells move as they are, in overlapping copies. DeleteCellArea moves
//     each through SetCell, which blanks the wide character a placeholder
//     lands on, so a DCH destroyed every wide character it moved, and scans
//     back from each orphaned placeholder for the character it belonged to,
//     which made it quadratic in the row's width.

// insertCells inserts n blank cells at column x of row y, as ICH does: the
// cells from x move right, and those pushed past the right margin are lost.
// The cells opened are the cursor's blank cell. Outside the left and right
// margins nothing is inserted. It reports false if the meter stopped it.
func (s *Screen) insertCells(x, y, n int) bool {
	left, right := s.scroll.Min.X, min(s.scroll.Max.X, s.buf.Width())
	if n <= 0 || y < 0 || y >= s.buf.Height() || x < left || x >= right {
		return true
	}
	// Cells pushed past the right margin are lost, so a count past the
	// distance to it inserts no more.
	n = min(n, right-x)
	if !s.blankCut(x, y) || !s.blankCut(right-n, y) || !s.blankCut(right, y) {
		return false
	}
	line := s.buf.Lines[y]
	// A metered span at a time from the right end, so each span's source is
	// left of every cell moved yet.
	for hi := right; hi > x+n; {
		lo := max(hi-meterInterval, x+n)
		if !s.meter.spend(hi - lo) {
			return false
		}
		copy(line[lo:hi], line[lo-n:hi-n])
		hi = lo
	}
	if !s.blankCells(line, x, x+n) {
		return false
	}
	s.buf.TouchLine(x, y, right-x)
	return true
}

// deleteCells deletes n cells at column x of row y, as DCH does: the cells
// after them, up to the right margin, move left, and the cells opened at the
// margin are the cursor's blank cell. Outside the left and right margins
// nothing is deleted. It reports false if the meter stopped it.
func (s *Screen) deleteCells(x, y, n int) bool {
	left, right := s.scroll.Min.X, min(s.scroll.Max.X, s.buf.Width())
	if n <= 0 || y < 0 || y >= s.buf.Height() || x < left || x >= right {
		return true
	}
	// Cells are deleted up to the right margin, so a count past the distance
	// to it deletes no more.
	n = min(n, right-x)
	if !s.blankCut(x, y) || !s.blankCut(x+n, y) || !s.blankCut(right, y) {
		return false
	}
	line := s.buf.Lines[y]
	// Spans from the left end, so each span's source is right of every cell
	// moved yet.
	if !s.meter.spans(x, right-n, func(lo, hi int) {
		copy(line[lo:hi], line[lo+n:hi+n])
	}) {
		return false
	}
	if !s.blankCells(line, right-n, right) {
		return false
	}
	s.buf.TouchLine(x, y, right-x)
	return true
}

// blankCells sets cells [from, to) of line to the cursor's blank cell, a
// metered span at a time. It reports false if the meter stopped it.
func (s *Screen) blankCells(line uv.Line, from, to int) bool {
	blank := uv.EmptyCell
	if c := s.blankCell(); c != nil {
		blank = *c
	}
	return s.meter.spans(from, to, func(lo, hi int) {
		span := line[lo:hi]
		for x := range span {
			span[x] = blank
		}
	})
}

// blankCut blanks the wide character cut at the left edge of column x of row
// y: the one whose placeholder is at x, with a cell on each side of that
// edge. A shift or a print that moves or writes the cells on one side only
// would leave its halves apart. xterm finds such a character with
// DamagedCells (util.c) and blanks it with ClearCells (screen.c), in the
// pen's colours, as the cursor's blank cell is. It reports false if the meter
// stopped it.
func (s *Screen) blankCut(x, y int) bool {
	line := s.buf.Line(y)
	if !cuts(line, x) {
		return true
	}
	return s.blankWide(line, x, y)
}

// cuts reports whether the left edge of column x of line cuts a wide
// character: whether x holds a placeholder, with a cell of the line on its
// left.
func cuts(line uv.Line, x int) bool {
	return x > 0 && x < len(line) && line[x].Width == 0
}

// blankWide blanks the wide character whose placeholder is at column x of
// line, row y: its first cell, before the placeholders that run back from x,
// and the placeholders that run on from x. The cells it blanks are the cells
// it reads to find them, and it charges them before blanking them. It reports
// false if the meter stopped it.
func (s *Screen) blankWide(line uv.Line, x, y int) bool {
	lo, hi := x, x+1
	for lo > 0 && line[lo-1].Width == 0 {
		lo--
	}
	if lo > 0 && line[lo-1].Width > 1 {
		lo--
	}
	for hi < len(line) && line[hi].Width == 0 {
		hi++
	}
	if !s.blankCells(line, lo, hi) {
		return false
	}
	s.buf.TouchLine(lo, y, hi-lo)
	return true
}
