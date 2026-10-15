package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
	uansi "github.com/charmbracelet/x/ansi"
	"github.com/owenthereal/x/ansi"
	"github.com/owenthereal/x/ansi/parser"
)

// handleSgr handles Select Graphic Rendition (SGR) escape sequences.
func (e *Emulator) handleSgr(params ansi.Params) {
	uv.ReadStyle(e.sgr.convert(params), &e.scr.cur.Pen)
}

// sgrParams converts an SGR's parameters from this module's ansi to upstream
// ansi's, which uv.ReadStyle takes: ultraviolet imports upstream's, whose
// Params is a different type from the one this module's parser produces,
// though both hold the same packed integers. This and the mouse button
// conversion in Emulator.SendMouse are the two places vt passes a type from
// one ansi module to a function of the other; uansi is imported here for this
// alone.
type sgrParams struct {
	// buf holds the converted parameters, kept so that an SGR allocates
	// nothing once it has grown to the longest one seen. The parser bounds
	// that length at parser.MaxParamsSize.
	buf uansi.Params
}

// convert returns params as upstream ansi's type, in the emulator's buffer:
// the result is good until the next call. It rebuilds each parameter from its
// value and its has-more flag (the sub-parameter marker, a colon in the
// sequence), so a change to either module's packing doesn't change what
// ultraviolet reads.
func (c *sgrParams) convert(params ansi.Params) uansi.Params {
	buf := c.buf[:0]
	for _, p := range params {
		buf = append(buf, uansi.Param(uansi.Parameter(p.Param(parser.MissingParam), p.HasMore())))
	}
	c.buf = buf
	return buf
}
