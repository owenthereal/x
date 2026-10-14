package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/exp/ordered"
)

// Screen represents a virtual terminal screen.
type Screen struct {
	// cb is the callbacks struct to use.
	cb *Callbacks
	// The buffer of the screen.
	buf *uv.RenderBuffer
	// The cur of the screen.
	cur, saved Cursor
	// scroll is the scroll region.
	scroll uv.Rectangle
	// scrollback is the scrollback buffer for lines scrolled off the top.
	scrollback *Scrollback
	// scrollOff, when set, receives each line that leaves the screen into
	// history, before it is overwritten.
	scrollOff func(uv.Line)
	// scrollbackClear, when set, is called when the history is cleared.
	scrollbackClear func()
	// meter counts the screen's work: the emulator's meter, for an
	// emulator's screens.
	meter *meter
}

// NewScreen creates a new screen.
func NewScreen(w, h int) *Screen {
	s := Screen{
		buf:        uv.NewRenderBuffer(w, h),
		scrollback: NewScrollback(DefaultScrollbackSize),
		meter:      new(meter),
	}
	s.scroll = s.buf.Bounds()
	return &s
}

// Reset resets the screen.
// It clears the screen, sets the cursor to the top left corner, reset the
// cursor styles, and resets the scroll region.
func (s *Screen) Reset() {
	s.clearCells()
	s.cur = Cursor{}
	s.saved = Cursor{}
	s.scroll = s.buf.Bounds()
	s.buf.Touched = nil
}

// Bounds returns the bounds of the screen.
func (s *Screen) Bounds() uv.Rectangle {
	return s.buf.Bounds()
}

// Touched returns touched lines in the screen buffer.
func (s *Screen) Touched() []*uv.LineData {
	return s.buf.Touched
}

// ClearTouched clears the touched state.
func (s *Screen) ClearTouched() {
	s.buf.Touched = nil
}

// CellAt returns the cell at the given x, y position.
func (s *Screen) CellAt(x int, y int) *uv.Cell {
	return s.buf.CellAt(x, y)
}

// SetCell sets the cell at the given x, y position.
func (s *Screen) SetCell(x, y int, c *uv.Cell) {
	s.buf.SetCell(x, y, c)
}

// Height returns the height of the screen.
func (s *Screen) Height() int {
	return s.buf.Height()
}

// Resize resizes the screen. A height shrink keeps the cursor's row, as xterm
// and tmux do: it drops the rows below the cursor first, then moves rows from
// the top into history, through the scrollback and the scroll-off function,
// until the cursor's row is the last. A width change truncates each row or
// pads it with blank cells.
func (s *Screen) Resize(width int, height int) {
	s.resize(width, height, true)
}

// resize resizes the screen. Unless history is set, the rows a height shrink
// takes from the top are discarded, not moved into history, as on the
// alternate screen, which has none. It reports false if the meter stopped it,
// which leaves the screen at its old size.
func (s *Screen) resize(width, height int, history bool) bool {
	if s.meter == nil {
		// A Screen that NewScreen didn't make has no meter yet.
		s.meter = new(meter)
	}
	if s.buf == nil {
		s.buf = uv.NewRenderBuffer(width, height)
	} else {
		if width != s.buf.Width() || height != s.buf.Height() {
			if !s.reallocate(width, height, history) {
				return false
			}
		}
		s.buf.Touched = nil
	}
	s.scroll = s.buf.Bounds()
	// The cursor and the DECSC slot stay inside the new size, whether or not
	// this is the active screen: a cursor off the screen drops what is
	// printed at it. The slot is only clamped, not moved with its row: xterm
	// moves it only by the rows a growth pulls back from history, and a
	// growth here pulls none back.
	s.cur.Position = clampToScreen(s.cur.Position, width, height)
	s.saved.Position = clampToScreen(s.saved.Position, width, height)
	return true
}

// reallocate copies the rows that survive a resize into a buffer of exactly
// the new size, and drops the old one. ultraviolet's Buffer.Resize widens
// every row before it cuts the height, so going from 2x65535 to 65535x2 would
// briefly need over four billion cells, and it shrinks by reslicing, which
// keeps the old storage.
//
// The rows above the cursor that no longer fit go first, into history if
// history is set, so the cursor's row becomes the last one, where the clamp
// in resize puts the cursor. A screen with no rows or no columns, before or
// after, has no cursor row to keep.
//
// Each row is built a span of columns at a time, metered. A stop keeps the
// old rows, so the screen it leaves is whole, at its old size; it reports
// false then.
func (s *Screen) reallocate(width, height int, history bool) bool {
	top := 0
	if width > 0 && height > 0 && s.buf.Width() > 0 {
		top = max(s.cur.Y-(height-1), 0)
	}
	old := s.buf.Lines
	if history {
		for _, line := range old[:top] {
			if !s.pushScrollback(line) {
				return false
			}
		}
	}
	lines := make([]uv.Line, height)
	for y := range lines {
		line := make(uv.Line, width)
		var src uv.Line
		if top+y < len(old) {
			src = old[top+y]
		}
		// What uv.NewLine and a copy of the old row do: the old cells that
		// fit, then blank ones.
		if !s.meter.spans(0, width, func(lo, hi int) {
			span := line[lo:hi]
			n := copy(span, src[min(lo, len(src)):min(hi, len(src))])
			rest := span[n:]
			for x := range rest {
				rest[x] = uv.EmptyCell
			}
		}) {
			return false
		}
		lines[y] = line
	}
	s.buf.Lines = lines
	return true
}

// clampToScreen returns p moved inside a width by height screen.
func clampToScreen(p uv.Position, width, height int) uv.Position {
	return uv.Pos(ordered.Clamp(p.X, 0, max(width-1, 0)), ordered.Clamp(p.Y, 0, max(height-1, 0)))
}

// Width returns the width of the screen.
func (s *Screen) Width() int {
	return s.buf.Width()
}

// Clear clears the screen with blank cells.
func (s *Screen) Clear() {
	s.ClearArea(s.Bounds())
}

// ClearWithScrollback saves all non-empty lines to scrollback before clearing.
// This is used for operations like ED 2 (erase screen) where content should
// be preserved in history.
func (s *Screen) ClearWithScrollback() {
	if s.scrollback != nil || s.scrollOff != nil {
		// Save all lines that have content before clearing
		for y := 0; y < s.buf.Height(); y++ {
			line := s.buf.Line(y)
			empty, ok := s.isLineEmpty(line)
			if !ok {
				return
			}
			if line != nil && !empty && !s.pushScrollback(line) {
				return
			}
		}
	}
	s.Clear()
}

// pushScrollback hands a line leaving the screen to the scrollback buffer and
// the scroll-off function. It reports false, pushing nothing, if the meter
// stopped it.
//
// The line goes to each whole: the scrollback copies it, and moves every line
// it holds to drop the oldest once it is full, and the scroll-off function
// does what it does. So the push is charged as one piece, which is larger
// than the interval only for a line or a scrollback longer than it.
func (s *Screen) pushScrollback(line uv.Line) bool {
	work := max(len(line), 1)
	if sb := s.scrollback; sb != nil && sb.maxLines > 0 && len(sb.lines) >= sb.maxLines {
		work += len(sb.lines)
	}
	if !s.meter.spend(work) {
		return false
	}
	if s.scrollback != nil {
		s.scrollback.Push(line)
	}
	if s.scrollOff != nil {
		s.scrollOff(line)
	}
	return true
}

// SetScrollOffFunc sets fn to receive each line that leaves the screen into
// history: the lines that would be pushed to the scrollback buffer, whether
// or not the screen has one. fn must not retain the line, which the screen
// reuses. A nil fn removes it.
func (s *Screen) SetScrollOffFunc(fn func(uv.Line)) {
	s.scrollOff = fn
}

// SetScrollbackClearFunc sets fn to be called when the history is cleared by
// what the program writing to the terminal sends: ED 3, and RIS. It is called
// whether or not the screen has a scrollback buffer, as the history it speaks
// of is the one [Screen.SetScrollOffFunc] feeds, so a caller keeping its own
// can clear it. Clearing the scrollback buffer yourself, with
// [Screen.Scrollback] or [Emulator.ClearScrollback], does not call it. A nil
// fn removes it.
func (s *Screen) SetScrollbackClearFunc(fn func()) {
	s.scrollbackClear = fn
}

// clearHistory empties the scrollback buffer, if there is one, and then calls
// the scrollback clear function. The buffer is emptied by reslicing, which is
// a constant amount of work whatever it holds, so the meter does not charge
// it.
func (s *Screen) clearHistory() {
	s.scrollback.Clear()
	if s.scrollbackClear != nil {
		s.scrollbackClear()
	}
}

// isLineEmpty reports whether the line contains only empty/space cells,
// reading it a metered span at a time. ok is false if the meter stopped it.
func (s *Screen) isLineEmpty(line uv.Line) (empty, ok bool) {
	for lo := 0; lo < len(line); lo += meterInterval {
		hi := min(lo+meterInterval, len(line))
		if !s.meter.spend(hi - lo) {
			return false, false
		}
		for _, cell := range line[lo:hi] {
			if cell.Content != "" && cell.Content != " " {
				return false, true
			}
		}
	}
	return true, true
}

// ClearArea clears the given area.
func (s *Screen) ClearArea(area uv.Rectangle) {
	s.fillArea(nil, area)
}

// Fill fills the screen or part of it.
func (s *Screen) Fill(c *uv.Cell) {
	s.FillArea(c, s.Bounds())
}

// FillArea fills the given area with the given cell.
func (s *Screen) FillArea(c *uv.Cell, area uv.Rectangle) {
	s.fillArea(c, area)
}

// fillArea fills area with c, or clears it if c is nil, and marks it touched,
// as [uv.Buffer.FillArea] and a touch of each of its rows do: SetCell on every
// c's width of cells from the area's left edge. It fills a metered span of a
// row at a time, charging each SetCell's scan from a placeholder too, until
// the meter stops it.
//
// SetCell ignores a cell off the screen, so only the rows on it, and the
// columns left of its right edge, need a visit. The spans start where the
// area does and are a whole number of c's width, so a wide c lands where it
// would in one pass.
func (s *Screen) fillArea(c *uv.Cell, area uv.Rectangle) {
	step := 1
	if c != nil && c.Width > 1 {
		step = c.Width
	}
	span := max(meterInterval-meterInterval%step, step)
	right := min(area.Max.X, s.buf.Width())
	for y := max(area.Min.Y, 0); y < min(area.Max.Y, s.buf.Height()); y++ {
		if area.Min.X >= right && !s.meter.spend(1) {
			return
		}
		for lo := area.Min.X; lo < right; lo += span {
			hi := min(lo+span, right)
			if !s.meter.spend(hi - lo) {
				return
			}
			for x := lo; x < hi; x += step {
				// chargeScan, spelled out so that it inlines in a loop
				// every erase runs.
				if n := s.scanCost(x, y); n > 0 && !s.meter.spend(n) {
					return
				}
				s.buf.Buffer.SetCell(x, y, c)
			}
		}
		s.buf.TouchLine(area.Min.X, y, area.Dx())
	}
}

// clearCells sets every cell of the screen to [uv.EmptyCell], as
// [uv.Buffer.Clear] does, a metered span of a row at a time, until the meter
// stops it.
func (s *Screen) clearCells() {
	for _, line := range s.buf.Lines {
		if !s.meter.spans(0, len(line), func(lo, hi int) {
			span := line[lo:hi]
			for x := range span {
				span[x] = uv.EmptyCell
			}
		}) {
			return
		}
	}
}

// setHorizontalMargins sets the horizontal margins.
func (s *Screen) setHorizontalMargins(left, right int) {
	s.scroll.Min.X = left
	s.scroll.Max.X = right
}

// setVerticalMargins sets the vertical margins.
func (s *Screen) setVerticalMargins(top, bottom int) {
	s.scroll.Min.Y = top
	s.scroll.Max.Y = bottom
}

// setCursorX sets the cursor X position. If margins is true, the cursor is
// only set if it is within the scroll margins.
func (s *Screen) setCursorX(x int, margins bool) {
	s.setCursor(x, s.cur.Y, margins)
}

// setCursor sets the cursor position. If margins is true, the cursor is only
// set if it is within the scroll margins. This follows how [ansi.CUP] works.
func (s *Screen) setCursor(x, y int, margins bool) {
	old := s.cur.Position
	if !margins {
		y = ordered.Clamp(y, 0, s.buf.Height()-1)
		x = ordered.Clamp(x, 0, s.buf.Width()-1)
	} else {
		y = ordered.Clamp(s.scroll.Min.Y+y, s.scroll.Min.Y, s.scroll.Max.Y-1)
		x = ordered.Clamp(s.scroll.Min.X+x, s.scroll.Min.X, s.scroll.Max.X-1)
	}
	s.cur.X, s.cur.Y = x, y

	if s.cb.CursorPosition != nil && (old.X != x || old.Y != y) {
		s.cb.CursorPosition(old, uv.Pos(x, y))
	}
}

// moveCursor moves the cursor by the given x and y deltas. If the cursor
// position is inside the scroll region, it is bounded by the scroll region.
// Otherwise, it is bounded by the screen bounds.
// This follows how [ansi.CUU], [ansi.CUD], [ansi.CUF], [ansi.CUB], [ansi.CNL],
// [ansi.CPL].
func (s *Screen) moveCursor(dx, dy int) {
	scroll := s.scroll
	old := s.cur.Position
	if old.X < scroll.Min.X {
		scroll.Min.X = 0
	}
	if old.X >= scroll.Max.X {
		scroll.Max.X = s.buf.Width()
	}

	area := s.buf.Bounds()
	if old.In(scroll) {
		area = scroll
	}

	// The cursor stops at an edge of the area, so a count past the distance to
	// it moves the cursor no further. Clamping the count to the distance first
	// also keeps a huge one from overflowing the sum.
	dx = ordered.Clamp(dx, area.Min.X-old.X, area.Max.X-1-old.X)
	dy = ordered.Clamp(dy, area.Min.Y-old.Y, area.Max.Y-1-old.Y)
	x, y := old.X+dx, old.Y+dy

	s.cur.X, s.cur.Y = x, y

	if s.cb.CursorPosition != nil && (old.X != x || old.Y != y) {
		s.cb.CursorPosition(old, uv.Pos(x, y))
	}
}

// Cursor returns the cursor.
func (s *Screen) Cursor() Cursor {
	return s.cur
}

// CursorPosition returns the cursor position.
func (s *Screen) CursorPosition() (x, y int) {
	return s.cur.X, s.cur.Y
}

// ScrollRegion returns the scroll region.
func (s *Screen) ScrollRegion() uv.Rectangle {
	return s.scroll
}

// SaveCursor saves the cursor.
func (s *Screen) SaveCursor() {
	s.saved = s.cur
}

// RestoreCursor restores the cursor.
func (s *Screen) RestoreCursor() {
	old := s.cur.Position
	// DECRC restores the position, pen and link, but not the cursor's
	// visibility or shape, which DECSC does not save in xterm.
	hidden, style, steady := s.cur.Hidden, s.cur.Style, s.cur.Steady
	s.cur = s.saved
	s.cur.Hidden, s.cur.Style, s.cur.Steady = hidden, style, steady
	s.cur.Position = clampToScreen(s.cur.Position, s.buf.Width(), s.buf.Height())

	if s.cb.CursorPosition != nil && (old.X != s.cur.X || old.Y != s.cur.Y) {
		s.cb.CursorPosition(old, s.cur.Position)
	}
}

// setCursorHidden sets the cursor hidden.
func (s *Screen) setCursorHidden(hidden bool) {
	changed := s.cur.Hidden != hidden
	s.cur.Hidden = hidden
	if changed && s.cb.CursorVisibility != nil {
		s.cb.CursorVisibility(!hidden)
	}
}

// setCursorStyle sets the cursor style.
func (s *Screen) setCursorStyle(style CursorStyle, blink bool) {
	changed := s.cur.Style != style || s.cur.Steady != !blink
	s.cur.Style = style
	s.cur.Steady = !blink
	if changed && s.cb.CursorStyle != nil {
		s.cb.CursorStyle(style, !blink)
	}
}

// cursorPen returns the cursor pen.
func (s *Screen) cursorPen() uv.Style {
	return s.cur.Pen
}

// cursorLink returns the cursor link.
func (s *Screen) cursorLink() uv.Link {
	return s.cur.Link
}

// ShowCursor shows the cursor.
func (s *Screen) ShowCursor() {
	s.setCursorHidden(false)
}

// HideCursor hides the cursor.
func (s *Screen) HideCursor() {
	s.setCursorHidden(true)
}

// InsertCell inserts n blank characters at the cursor position pushing out
// cells to the right and out of the screen.
func (s *Screen) InsertCell(n int) {
	if n <= 0 {
		return
	}

	x, y := s.cur.X, s.cur.Y
	// Cells pushed past the right margin are lost, so a count past the
	// distance to it inserts no more. Outside the margins nothing is inserted,
	// whatever the count.
	n = min(n, max(s.scroll.Max.X-x, 1))
	s.insertCellArea(x, y, n, s.blankCell(), s.scroll)
}

// DeleteCell deletes n cells at the cursor position moving cells to the left.
// This has no effect if the cursor is outside the scroll region.
func (s *Screen) DeleteCell(n int) {
	if n <= 0 {
		return
	}

	x, y := s.cur.X, s.cur.Y
	// Cells are deleted up to the right margin, so a count past the distance
	// to it deletes no more. Outside the margins nothing is deleted, whatever
	// the count.
	n = min(n, max(s.scroll.Max.X-x, 1))
	s.deleteCellArea(x, y, n, s.blankCell(), s.scroll)
}

// insertCellArea inserts n cells set to c at x on row y, within area, as
// [uv.RenderBuffer.InsertCellArea] does: the cells from x move right, and
// those pushed past the area's right edge are lost. It reports false if the
// meter stopped it.
//
// It moves a metered span at a time, from the right end, so each span's
// source is left of every cell moved yet; and it sets the cells opened as
// ultraviolet does, through SetCell. ultraviolet's own call can't be split by
// columns and come out the same, since its SetCell mends a wide character
// across the split.
func (s *Screen) insertCellArea(x, y, n int, c *uv.Cell, area uv.Rectangle) bool {
	if s.inCellArea(x, y, n, area) {
		line := s.buf.Lines[y]
		m := min(n, area.Max.X-x)
		for hi := area.Max.X; hi > x+m; {
			lo := max(hi-meterInterval, x+m)
			if !s.meter.spend(hi - lo) {
				return false
			}
			copy(line[lo:hi], line[lo-m:hi-m])
			hi = lo
		}
		if !s.setCells(x, x+m, y, c) {
			return false
		}
	}
	if x+n > area.Max.X {
		n = area.Max.X - x
	}
	s.buf.TouchLine(x, y, n)
	return true
}

// deleteCellArea deletes n cells at x on row y, within area, and sets the
// cells opened at the area's right edge to c, as
// [uv.RenderBuffer.DeleteCellArea] does: each cell moves through SetCell, a
// metered span at a time. It reports false if the meter stopped it.
//
// That order of moves leaves an orphaned placeholder at every other column of
// a row of wide characters, which the next move's SetCell scans back from:
// the charges for those scans keep the stop function coming.
func (s *Screen) deleteCellArea(x, y, n int, c *uv.Cell, area uv.Rectangle) bool {
	if s.inCellArea(x, y, n, area) {
		m := min(n, area.Max.X-x)
		if !s.meter.spans(x, area.Max.X-m, func(lo, hi int) {
			for i := lo; i < hi && s.chargeScan(i, y); i++ {
				s.buf.Buffer.SetCell(i, y, s.buf.CellAt(i+m, y))
			}
		}) {
			return false
		}
		if !s.setCells(area.Max.X-m, area.Max.X, y, c) {
			return false
		}
	}
	if remaining := area.Max.X - x; n > remaining {
		n = remaining
	}
	s.buf.TouchLine(x, y, n)
	return true
}

// inCellArea reports whether a shift of n cells at x on row y, within area,
// moves anything: the test [uv.Buffer.InsertCellArea] and
// [uv.Buffer.DeleteCellArea] make first.
func (s *Screen) inCellArea(x, y, n int, area uv.Rectangle) bool {
	return n > 0 && y >= area.Min.Y && y < area.Max.Y && y < s.buf.Height() &&
		x >= area.Min.X && x < area.Max.X && x < s.buf.Width()
}

// setCells sets cells [from, to) of row y to c through ultraviolet's SetCell,
// which mends a wide character a cell cuts, a metered span at a time. It
// reports false if the meter stopped it.
func (s *Screen) setCells(from, to, y int, c *uv.Cell) bool {
	return s.meter.spans(from, to, func(lo, hi int) {
		for x := lo; x < hi && s.chargeScan(x, y); x++ {
			s.buf.Buffer.SetCell(x, y, c)
		}
	})
}

// cellsAt copies the cells of row y from column x into dst, as many as the
// row has, and returns how many it copied.
func (s *Screen) cellsAt(x, y int, dst []uv.Cell) int {
	if y < 0 || y >= len(s.buf.Lines) || x < 0 || x > len(s.buf.Lines[y]) {
		return 0
	}
	return copy(dst, s.buf.Lines[y][x:])
}

// holds reports whether the cell at x on row y holds content, width columns
// wide.
func (s *Screen) holds(x, y int, content string, width int) bool {
	c := s.buf.CellAt(x, y)
	return c != nil && c.Width == width && c.Content == content
}

// putCells writes cells that cellsAt copied back over row y from column x as
// they were, a wide character's placeholder included, which SetCell would
// mend. Cells that no longer fit the row are not written.
func (s *Screen) putCells(x, y int, cells []uv.Cell) {
	if y < 0 || y >= len(s.buf.Lines) || x < 0 || x+len(cells) > len(s.buf.Lines[y]) {
		return
	}
	copy(s.buf.Lines[y][x:], cells)
	s.buf.TouchLine(x, y, len(cells))
}

// chargeScan charges, before ultraviolet's SetCell writes over the cell at x
// on row y, the scan it makes when that cell is a wide character's
// placeholder (see scanCost). A span charges the cell itself. It reports
// false if the meter stopped it.
func (s *Screen) chargeScan(x, y int) bool {
	n := s.scanCost(x, y)
	return n == 0 || s.meter.spend(n)
}

// scanCost returns the most ultraviolet's SetCell reads before it writes over
// the cell at x on row y: when that cell is a wide character's placeholder,
// it scans back toward column 0 for the character the placeholder belongs to,
// as far as column 0 when the character is gone, x+1 cells; otherwise none.
func (s *Screen) scanCost(x, y int) int {
	if cell := s.buf.CellAt(x, y); cell != nil && cell.Width == 0 {
		return x + 1
	}
	return 0
}

// ScrollUp scrolls the content up n lines within the given region. Lines
// scrolled past the top margin are lost. This is equivalent to [ansi.SU] which
// moves the cursor to the top margin and performs a [ansi.DL] operation.
func (s *Screen) ScrollUp(n int) {
	x, y := s.CursorPosition()
	s.setCursor(s.cur.X, 0, true)
	// From the top margin, DeleteLine clamps n to the region's height.
	s.DeleteLine(n)
	s.setCursor(x, y, false)
}

// ScrollDown scrolls the content down n lines within the given region. Lines
// scrolled past the bottom margin are lost. This is equivalent to [ansi.SD]
// which moves the cursor to top margin and performs a [ansi.IL] operation.
func (s *Screen) ScrollDown(n int) {
	x, y := s.CursorPosition()
	s.setCursor(s.cur.X, 0, true)
	// From the top margin, InsertLine clamps n to the region's height.
	s.InsertLine(n)
	s.setCursor(x, y, false)
}

// InsertLine inserts n blank lines at the cursor position Y coordinate.
// Only operates if cursor is within scroll region. Lines below cursor Y
// are moved down, with those past bottom margin being discarded.
// It returns true if the operation was successful.
func (s *Screen) InsertLine(n int) bool {
	if n <= 0 {
		return false
	}

	x, y := s.cur.X, s.cur.Y

	// Only operate if cursor Y is within scroll region
	if y < s.scroll.Min.Y || y >= s.scroll.Max.Y ||
		x < s.scroll.Min.X || x >= s.scroll.Max.X {
		return false
	}

	// Rows pushed past the bottom margin are lost, so a count past the
	// distance to it inserts no more.
	n = min(n, s.scroll.Max.Y-y)

	if s.scrollsFullWidth() {
		s.insertRows(y, n)
	} else {
		s.shiftRowsDown(y, n, s.blankCell())
	}

	return true
}

// DeleteLine deletes n lines at the cursor position Y coordinate.
// Only operates if cursor is within scroll region. Lines below cursor Y
// are moved up, with blank lines inserted at the bottom of scroll region.
// If scrollback is enabled and cursor is at top of a scroll region that starts
// at the first row, lines are saved to the scrollback buffer before deletion.
// It returns true if the operation was successful.
func (s *Screen) DeleteLine(n int) bool {
	if n <= 0 {
		return false
	}

	scroll := s.scroll
	x, y := s.cur.X, s.cur.Y

	// Only operate if cursor Y is within scroll region
	if y < scroll.Min.Y || y >= scroll.Max.Y ||
		x < scroll.Min.X || x >= scroll.Max.X {
		return false
	}

	// Rows are deleted up to the bottom margin, so a count past the distance
	// to it deletes no more.
	n = min(n, scroll.Max.Y-y)

	fullWidth := s.scrollsFullWidth()

	// Save lines to scrollback if we're at the top of the scroll region,
	// the region starts at the first row of the screen, and it uses the full
	// width (typical terminal scroll). This captures lines that would be lost
	// during scroll up operations. A region that starts lower, such as one
	// that keeps a status line above it, discards them, as xterm does.
	// This happens before the lines are deleted, because a full-width delete
	// reuses their storage as the blank lines at the bottom of the region.
	if (s.scrollback != nil || s.scrollOff != nil) && y == scroll.Min.Y && scroll.Min.Y == 0 && fullWidth {
		// Save lines that will be deleted
		for i := range min(n, scroll.Max.Y-y, s.buf.Height()-y) {
			if line := s.buf.Line(y + i); line != nil && !s.pushScrollback(line) {
				return true
			}
		}
	}

	if fullWidth {
		s.deleteRows(y, n)
	} else {
		s.shiftRowsUp(y, n, s.blankCell())
	}

	return true
}

// scrollsFullWidth reports whether the scroll region spans every column.
func (s *Screen) scrollsFullWidth() bool {
	return s.scroll.Min.X == 0 && s.scroll.Max.X == s.buf.Width()
}

// insertRows inserts n blank rows at row y of a scroll region that spans every
// column. It rotates the region's row slices, rather than copying the cells of
// each row as [uv.RenderBuffer.InsertLineArea] does, and blanks the rows pushed
// out of the region to reuse as the new ones. The meter can stop it between
// spans of each part.
func (s *Screen) insertRows(y, n int) {
	region := s.buf.Lines[y:s.scroll.Max.Y]
	count := min(n, len(region))
	if s.rotateRows(region, len(region)-count) && s.blankRows(region[:count]) {
		s.touchRows(-n)
	}
}

// deleteRows deletes n rows at row y of a scroll region that spans every
// column, and adds n blank ones at its bottom. Like insertRows it rotates the
// region's row slices, and reuses the deleted rows as the blank ones: callers
// must be done with them.
func (s *Screen) deleteRows(y, n int) {
	region := s.buf.Lines[y:s.scroll.Max.Y]
	count := min(n, len(region))
	if s.rotateRows(region, count) && s.blankRows(region[len(region)-count:]) {
		s.touchRows(n)
	}
}

// shiftRowsDown moves the cells of the scroll region's rows from row y down by
// n, and sets the n rows opened to c, as [uv.RenderBuffer.InsertLineArea]
// does, for a region narrower than the screen, whose rows can't simply
// rotate. It copies a metered span of a row at a time, until the meter stops
// it. The caller keeps y inside the region and n within it.
func (s *Screen) shiftRowsDown(y, n int, c *uv.Cell) {
	area := s.scroll
	for i := area.Max.Y - 1; i >= y+n; i-- {
		if !s.copyRow(i, i-n) {
			return
		}
	}
	for i := y; i < y+n; i++ {
		if !s.setCells(area.Min.X, area.Max.X, i, c) {
			return
		}
	}
	s.touchRows(-n)
}

// shiftRowsUp moves the cells of the scroll region's rows below row y up by n,
// and sets the n rows opened at the region's bottom to c, as
// [uv.RenderBuffer.DeleteLineArea] does. Like shiftRowsDown it is metered,
// for a region narrower than the screen.
func (s *Screen) shiftRowsUp(y, n int, c *uv.Cell) {
	area := s.scroll
	for i := y; i < area.Max.Y-n; i++ {
		if !s.copyRow(i, i+n) {
			return
		}
	}
	for i := area.Max.Y - n; i < area.Max.Y; i++ {
		if !s.setCells(area.Min.X, area.Max.X, i, c) {
			return
		}
	}
	s.touchRows(n)
}

// copyRow copies the scroll region's columns of row src to row dst, a metered
// span at a time. It reports false if the meter stopped it.
func (s *Screen) copyRow(dst, src int) bool {
	to, from := s.buf.Lines[dst], s.buf.Lines[src]
	return s.meter.spans(s.scroll.Min.X, s.scroll.Max.X, func(lo, hi int) {
		copy(to[lo:hi], from[lo:hi])
	})
}

// rotateRows rotates rows left by k: the first k rows move to the end, and
// each part keeps its order. It reverses the two parts and then the whole,
// which needs no scratch space. It reports false if the meter stopped it.
func (s *Screen) rotateRows(rows []uv.Line, k int) bool {
	return s.reverseRows(rows[:k]) && s.reverseRows(rows[k:]) && s.reverseRows(rows)
}

// reverseRows reverses rows, as [slices.Reverse] does, a metered span of swaps
// at a time. It reports false if the meter stopped it.
func (s *Screen) reverseRows(rows []uv.Line) bool {
	last := len(rows) - 1
	return s.meter.spans(0, len(rows)/2, func(lo, hi int) {
		for i := lo; i < hi; i++ {
			rows[i], rows[last-i] = rows[last-i], rows[i]
		}
	})
}

// blankRows sets every cell of rows to the cursor's blank cell, a metered span
// of a row at a time. It reports false if the meter stopped it.
func (s *Screen) blankRows(rows []uv.Line) bool {
	blank := uv.EmptyCell
	if c := s.blankCell(); c != nil {
		blank = *c
	}
	for _, row := range rows {
		if !s.meter.spans(0, len(row), func(lo, hi int) {
			// Ranging over the span, not indexing up to hi, keeps the
			// bounds checks out of a loop every scroll runs.
			span := row[lo:hi]
			for x := range span {
				span[x] = blank
			}
		}) {
			return false
		}
	}
	return true
}

// touchRows marks the lines touched that [uv.RenderBuffer.InsertLineArea] and
// [uv.RenderBuffer.DeleteLineArea] mark, so that a full-width scroll and a
// cell copy report the same damage: every row of the region, and the row
// shift away from it. It marks a metered span of rows at a time, until the
// meter stops it.
func (s *Screen) touchRows(shift int) {
	x, w := s.scroll.Min.X, s.scroll.Dx()
	s.meter.spans(s.scroll.Min.Y, s.scroll.Max.Y, func(lo, hi int) {
		for y := lo; y < hi; y++ {
			s.buf.TouchLine(x, y, w)
			s.buf.TouchLine(x, y+shift, w)
		}
	})
}

// blankCell returns the cursor blank cell with the background color set to the
// current pen background color. If the pen background color is nil, the return
// value is nil.
func (s *Screen) blankCell() *uv.Cell {
	if s.cur.Pen.Bg == nil {
		return nil
	}

	c := uv.EmptyCell
	c.Style.Bg = s.cur.Pen.Bg
	return &c
}

// Scrollback returns the screen's scrollback buffer.
func (s *Screen) Scrollback() *Scrollback {
	return s.scrollback
}

// SetScrollback sets the screen's scrollback buffer.
// Pass nil to disable scrollback.
func (s *Screen) SetScrollback(sb *Scrollback) {
	s.scrollback = sb
}

// SetScrollbackSize sets the maximum number of lines in the scrollback buffer.
func (s *Screen) SetScrollbackSize(maxLines int) {
	if s.scrollback == nil {
		s.scrollback = NewScrollback(maxLines)
	} else {
		s.scrollback.SetMaxLines(maxLines)
	}
}
