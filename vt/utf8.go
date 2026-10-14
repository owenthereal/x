package vt

import (
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// printableASCII reports whether r is a character that can never join the
// grapheme cluster beside it. No ASCII character is a combining mark, a ZWJ, a
// spacing mark or a regional indicator, and CR and LF are not printable, so a
// cluster boundary between two of these is certain without asking the
// segmenter.
func printableASCII(r rune) bool {
	return r >= ansi.SP && r < ansi.DEL
}

// handlePrint handles printable characters.
//
// A character is held back rather than printed straight away, because the one
// after it may be a combining mark that belongs to the same cluster. Printing
// "e" from "e\u0301" before the mark arrives puts the base in one cell and
// leaves the mark to become a zero-width cell of its own, which the next write
// or erase then destroys.
func (e *Emulator) handlePrint(r rune) {
	if e.carry.valid {
		e.resumeCluster(r)
	}

	// No ASCII character can extend the cluster before it: none of them is
	// Extend, ZWJ, SpacingMark, Prepend, a regional indicator, or part of an
	// Indic conjunct. So an ASCII character arriving is proof that whatever is
	// buffered is finished, and the buffer can go out.
	//
	// This is also what keeps the buffer from being re-segmented as it grows.
	// Asking where the clusters are on every character costs the length of the
	// buffer each time, which is quadratic over a long run of combining marks
	// that never resolves into more than one cluster.
	if printableASCII(r) {
		// Two printable ASCII characters in a row is the common case by a wide
		// margin, and needs no segmenting at all.
		if len(e.grapheme) == 1 && printableASCII(rune(e.grapheme[0])) {
			// Converting a one-byte slice does not allocate, where
			// string(e.grapheme[0]) would for every character.
			e.handleGrapheme(string(e.grapheme[:1]), 1)
			e.grapheme = utf8.AppendRune(e.grapheme[:0], r)
			return
		}
		e.flushGrapheme()
	} else if len(e.grapheme) == 1 {
		// A character the charset maps is drawn as another, which no
		// later rune can join: a cluster of more than one byte isn't
		// mapped, so joining it would undo the mapping. Print it alone,
		// as it is when a Write ends right after it.
		if _, ok := e.mapCharset(e.grapheme[0], e.gsingle); ok {
			e.flushGrapheme()
		}
	}

	e.grapheme = utf8.AppendRune(e.grapheme, r)
}

// mapCharset returns what the character sets draw the one-byte character c
// as, with the single shift single pending, and whether they map it at all.
func (e *Emulator) mapCharset(c byte, single int) (string, bool) {
	var charset CharSet
	switch {
	case single > 1 && single < 4:
		charset = e.charsets[single]
	case c < 128:
		charset = e.charsets[e.gl]
	default:
		charset = e.charsets[e.gr]
	}
	r, ok := charset[c]
	return r, ok
}

// flushGrapheme flushes the current grapheme buffer, if any, and handles the
// grapheme as a single unit.
func (e *Emulator) flushGrapheme() {
	// Anything but printing between two Writes ends the cluster the first
	// one finished with, and every sequence handler comes through here.
	e.carry.valid = false
	// What an earlier flush printed is not what this one ends with, so the
	// Write that ends here must not carry it, across whatever came between.
	e.lastCluster.valid = false
	if len(e.grapheme) == 0 {
		return
	}

	// XXX: We always use [ansi.GraphemeWidth] here to report accurate widths
	// and it's up to the caller to decide how to handle Unicode vs non-Unicode
	// modes.
	method := ansi.GraphemeWidth
	graphemes := e.grapheme
	for len(graphemes) > 0 {
		cluster, width := ansi.FirstGraphemeCluster(graphemes, method)
		if len(cluster) == 0 {
			break
		}
		e.handleGrapheme(string(cluster), width)
		graphemes = graphemes[len(cluster):]
	}
	e.grapheme = e.grapheme[:0] // Reset the grapheme buffer.
}

// clusterMark records where a cluster was printed, so that the next Write can
// extend it.
type clusterMark struct {
	valid   bool
	scr     *Screen
	content string
	x, y    int // the cluster's cell
	width   int
	// The cursor right after the cluster was printed.
	curX, curY int
	phantom    bool
	// The cells a cluster printed at the last column wrote over, ending at
	// its cell. Widened to two columns by the next Write, the cluster no
	// longer fits there, so it wraps or isn't printed, and its undo has to
	// put them back. A cluster printed anywhere else is printed again over
	// the cells its undo blanks, and has none.
	under []uv.Cell
	// The character REP repeats, from before the cluster was printed. A lone
	// "e" set it, but extended by the next Write the cluster is no longer a
	// single byte and sets nothing, so the undo puts this back, leaving what
	// the same bytes in one Write would.
	lastChar rune
}

// maxCarriedCluster bounds the cluster resumeCluster extends. Rejoining costs
// the cluster's length, so a program writing one combining mark per Write
// would otherwise make every Write slower and the cell larger without bound.
// Real clusters (an emoji family, a flag) are a few dozen bytes.
const maxCarriedCluster = 128

// resumeCluster takes back the cluster the previous Write ended with when r
// extends it, so a cluster split across two Writes lands in one cell, as it
// would have arriving in one. It applies only if nothing but printing has
// happened since.
func (e *Emulator) resumeCluster(r rune) {
	c := e.carry
	e.carry.valid = false
	if printableASCII(r) || e.scr != c.scr || e.atPhantom != c.phantom || len(c.content) >= maxCarriedCluster {
		return
	}
	if x, y := e.scr.CursorPosition(); x != c.curX || y != c.curY {
		return
	}
	joined := c.content + string(r)
	if cluster, _ := ansi.FirstGraphemeCluster(joined, ansi.GraphemeWidth); len(cluster) != len(joined) {
		return
	}
	switch {
	case len(c.under) > 0 && e.scr.holds(c.x, c.y, c.content, c.width):
		// Put back what the cluster wrote over at the last column. An
		// insert there only sets the cell, so this undoes one too. If the
		// cell no longer holds the cluster, what it wrote over has gone
		// with it, and the cell is blanked as anywhere else.
		if !e.meter.spend(len(c.under)) {
			return
		}
		e.scr.putCells(c.x+1-len(c.under), c.y, c.under)
	case e.insertMode:
		// Undo the insert that made room for it.
		if !e.scr.deleteCellArea(c.x, c.y, c.width, e.scr.blankCell(), e.scr.scroll) {
			return
		}
	default:
		if !e.meter.spend(c.width) {
			return
		}
		for i := range c.width {
			if !e.scr.chargeScan(c.x+i, c.y) {
				return
			}
			e.scr.SetCell(c.x+i, c.y, nil)
		}
	}
	e.scr.setCursor(c.x, c.y, false)
	e.atPhantom = false
	e.lastChar = c.lastChar
	e.grapheme = append(e.grapheme[:0], c.content...)
}

// handleGrapheme handles UTF-8 graphemes.
func (e *Emulator) handleGrapheme(content string, width int) {
	if width <= 0 {
		// A cluster with no base character, such as a lone ZWJ or a
		// combining mark after a control. Stored, it would be a zero-width
		// cell, which reads as the tail of a wide character; drop it, as a
		// terminal with nothing to attach it to does. The segmenter ended
		// the cluster printed before it here, so nothing after it joins
		// that one either.
		e.lastCluster.valid = false
		return
	}
	awm := e.isModeSet(ansi.ModeAutoWrap)
	cell := uv.Cell{
		Content: content,
		Width:   width,
		Style:   e.scr.cursorPen(),
		Link:    e.scr.cursorLink(),
	}

	x, y := e.scr.CursorPosition()
	if e.atPhantom && awm {
		// moves cursor down similar to [Terminal.linefeed] except it doesn't
		// respects [ansi.LNM] mode.
		// This will reset the phantom state i.e. pending wrap state.
		e.index()
		if e.meter.spent() {
			return
		}
		_, y = e.scr.CursorPosition()
		x = 0
	}

	// A single shift applies to the next character, whatever that character
	// turns out to be. Take it now, so it cannot leak onto the one after this
	// when this is a cluster no charset has a mapping for.
	single := e.gsingle
	e.gsingle = 0

	// Handle character set mappings
	if len(content) == 1 {
		if r, ok := e.mapCharset(content[0], single); ok {
			cell.Content = r
			cell.Width = 1
		}
	}

	before := e.lastChar
	if cell.Width == 1 && len(content) == 1 {
		e.lastChar, _ = utf8.DecodeRuneInString(content)
	}

	// A wide character the rest of the line can't hold isn't cut at the
	// margin, as xterm's dotext writes a character only where it fits: under
	// DECAWM it wraps first, leaving the last column as it was, and without,
	// or when no line is wide enough for it, it isn't printed. Either way it
	// leaves no wrap pending, and nothing the next Write can extend.
	if cell.Width > 1 && x+cell.Width > e.scr.Width() {
		if !awm || cell.Width > e.scr.Width() {
			e.atPhantom = false
			e.lastCluster.valid = false
			return
		}
		e.index()
		if e.meter.spent() {
			return
		}
		_, y = e.scr.CursorPosition()
		x = 0
	}

	// Keep what a print at the last column writes over, for the carried
	// cluster's undo (see clusterMark): the last column, and the first half
	// of a wide character it holds the second half of, which SetCell blanks.
	var under []uv.Cell
	if x == e.scr.Width()-1 {
		from := x
		if c := e.scr.CellAt(x, y); c != nil && c.Width == 0 && x > 0 {
			from--
		}
		under = e.under[:e.scr.cellsAt(from, y, e.under[:])]
	}

	if e.insertMode {
		// IRM: shift the rest of the line right to make room.
		if !e.scr.insertCellArea(x, y, cell.Width, e.scr.blankCell(), e.scr.scroll) {
			return
		}
	}
	// The cells the print writes, charged once the wrap and the insert have
	// charged their own work, with SetCell's scan back from a placeholder it
	// writes over.
	if !e.meter.spend(cell.Width + e.scr.scanCost(x, y)) {
		return
	}
	e.scr.SetCell(x, y, &cell)
	cx, cy := x, y

	// A character that reaches the last column leaves the wrap pending, as
	// dotext does for one that fills the rest of the line, whatever its
	// width; setCursor holds the cursor on the last column.
	x += cell.Width
	e.atPhantom = awm && x >= e.scr.Width()

	// NOTE: We don't reset the phantom state here, we handle it up above.
	e.scr.setCursor(x, y, false)
	e.lastCluster = clusterMark{
		// A cluster a charset mapped is not one the next Write can extend.
		valid: cell.Content == content, scr: e.scr, content: content, x: cx, y: cy, width: cell.Width,
		curX: e.scr.cur.X, curY: e.scr.cur.Y, phantom: e.atPhantom,
		under: under, lastChar: before,
	}
}
