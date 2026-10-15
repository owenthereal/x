package vt

import (
	"image/color"
	"slices"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	uansi "github.com/charmbracelet/x/ansi"
	"github.com/owenthereal/x/ansi"
	"github.com/owenthereal/x/ansi/parser"
)

// The two ansi modules meet where vt hands an SGR's parameters to
// uv.ReadStyle, which takes upstream ansi's Params: handleSgr converts them
// into a buffer kept on the emulator. The first tests below check that
// conversion against upstream's own parse of the same bytes. The last checks
// the other crossing, a mouse button that ultraviolet types with upstream
// ansi's MouseButton.

// sgrBoundaryCases are SGR sequences whose style depends on the has-more flag
// (a colon between parameters) or on a missing parameter, with the style each
// gives. Each is one sequence, so that both parsers see the same final CSI.
var sgrBoundaryCases = []struct {
	name string
	seq  string
	want uv.Style
}{
	{
		"semicolons: bold, 24-bit foreground, indexed background",
		"\x1b[1;38;2;10;20;30;48;5;200m",
		uv.Style{Attrs: uv.AttrBold, Fg: color.RGBA{10, 20, 30, 255}, Bg: uansi.IndexedColor(200)},
	},
	{
		"colon: curly underline",
		"\x1b[4:3m",
		uv.Style{Underline: uv.UnderlineCurly},
	},
	{
		"semicolon: underline, then italic",
		"\x1b[4;3m",
		uv.Style{Underline: uv.UnderlineSingle, Attrs: uv.AttrItalic},
	},
	{
		"colon: 24-bit foreground, empty color space",
		"\x1b[38:2::10:20:30m",
		uv.Style{Fg: color.RGBA{10, 20, 30, 255}},
	},
	{
		"colon: 24-bit background, color space 0",
		"\x1b[48:2:0:10:20:30m",
		uv.Style{Bg: color.RGBA{10, 20, 30, 255}},
	},
	{
		"colon: indexed foreground",
		"\x1b[38:5:200m",
		uv.Style{Fg: uansi.IndexedColor(200)},
	},
	{
		"colon and semicolon: dashed underline, underline color",
		"\x1b[1;4:5;58:2::10:20:30m",
		uv.Style{Attrs: uv.AttrBold, Underline: uv.UnderlineDashed, UnderlineColor: color.RGBA{10, 20, 30, 255}},
	},
	{
		"missing first parameter is a reset, then bold",
		"\x1b[;1m",
		uv.Style{Attrs: uv.AttrBold},
	},
}

// forkSGRParams returns the parameters this module's parser gives the SGR in
// seq, copied out of the parser's buffer.
func forkSGRParams(t *testing.T, seq string) ansi.Params {
	t.Helper()
	var params ansi.Params
	found := false
	p := ansi.NewParser()
	p.SetParamsSize(parser.MaxParamsSize)
	p.SetHandler(ansi.Handler{HandleCsi: func(cmd ansi.Cmd, ps ansi.Params) {
		if cmd.Final() == 'm' {
			params, found = slices.Clone(ps), true
		}
	}})
	p.Parse([]byte(seq))
	if !found {
		t.Fatalf("%q: this module's parser dispatched no SGR", seq)
	}
	return params
}

// upstreamSGRParams returns the parameters upstream ansi's own parser gives
// the SGR in seq.
func upstreamSGRParams(t *testing.T, seq string) uansi.Params {
	t.Helper()
	var params uansi.Params
	found := false
	p := uansi.NewParser()
	p.SetParamsSize(parser.MaxParamsSize)
	p.SetHandler(uansi.Handler{HandleCsi: func(cmd uansi.Cmd, ps uansi.Params) {
		if cmd.Final() == 'm' {
			params, found = slices.Clone(ps), true
		}
	}})
	p.Parse([]byte(seq))
	if !found {
		t.Fatalf("%q: upstream's parser dispatched no SGR", seq)
	}
	return params
}

// The conversion gives, element by element and flag by flag, what upstream's
// parser gives for the same bytes.
func TestSGRConversionMatchesUpstreamParse(t *testing.T) {
	var conv sgrParams
	for _, tc := range sgrBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			got := conv.convert(forkSGRParams(t, tc.seq))
			want := upstreamSGRParams(t, tc.seq)
			if !slices.Equal(got, want) {
				t.Errorf("%q: converted %v, upstream's parse is %v", tc.seq, got, want)
			}
			for i := range want {
				if i < len(got) && got[i].HasMore() != want[i].HasMore() {
					t.Errorf("%q, parameter %d: has-more is %v, upstream's is %v", tc.seq, i, got[i].HasMore(), want[i].HasMore())
				}
			}
		})
	}
}

// An SGR written to the emulator sets the pen uv.ReadStyle sets from upstream's
// own parse of the same bytes, and the style the sequence means: a colon's
// sub-parameters survive the conversion.
func TestSGRStyleMatchesReadStyleOfUpstreamParse(t *testing.T) {
	for _, tc := range sgrBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			var ref uv.Style
			uv.ReadStyle(upstreamSGRParams(t, tc.seq), &ref)
			if !ref.Equal(&tc.want) {
				t.Fatalf("%q: uv.ReadStyle of upstream's parse is %+v, the case says %+v", tc.seq, ref, tc.want)
			}

			term := newTestTerminal(t, 10, 1)
			term.WriteString(tc.seq)
			if got := term.scr.cur.Pen; !got.Equal(&ref) {
				t.Errorf("%q: pen is %+v, uv.ReadStyle of upstream's parse gives %+v", tc.seq, got, ref)
			}
		})
	}
}

// A steady-state SGR allocates nothing: the conversion reuses the emulator's
// buffer. The first Write grows the buffer, so it is outside the count. The
// sequence has no 24-bit color, because upstream ansi's ReadStyleColor, which
// uv.ReadStyle calls, boxes a color.RGBA in a color.Color and allocates
// whatever vt does; the conversion alone is counted for that case below.
func TestSGRDoesNotAllocate(t *testing.T) {
	term := newTestTerminal(t, 80, 24)
	sgr := []byte("\x1b[1;4:3;38;5;100;48;5;200;58:5:9m")
	term.Write(sgr)
	allocs := testing.AllocsPerRun(100, func() {
		term.Write(sgr)
	})
	if allocs > 0 {
		t.Errorf("allocations per %q: got = %v, want = 0", sgr, allocs)
	}
}

// The conversion of a 24-bit color SGR allocates nothing either.
func TestSGRConversionDoesNotAllocate(t *testing.T) {
	params := forkSGRParams(t, "\x1b[1;38;2;10;20;30;48;5;200m")
	var conv sgrParams
	conv.convert(params)
	allocs := testing.AllocsPerRun(100, func() {
		conv.convert(params)
	})
	if allocs > 0 {
		t.Errorf("allocations per conversion: got = %v, want = 0", allocs)
	}
}

// SendMouse encodes the button ultraviolet's event carries with this module's
// ansi, whose MouseButton is another type from upstream's.
func TestSendMouseEncodesTheButton(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event Mouse
		want  string
	}{
		{"left press", MouseClick{Button: MouseLeft, X: 1, Y: 2}, "\x1b[<0;2;3M"},
		{"right press", MouseClick{Button: MouseRight, X: 1, Y: 2}, "\x1b[<2;2;3M"},
		{"wheel up", MouseWheel{Button: MouseWheelUp, X: 1, Y: 2}, "\x1b[<64;2;3M"},
		{"left release", MouseRelease{Button: MouseLeft, X: 1, Y: 2}, "\x1b[<0;2;3m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestTerminal(t, 10, 5)
			_, _ = e.WriteString("\x1b[?1000h\x1b[?1006h") // button events, SGR encoding

			go e.SendMouse(tc.event)
			type read struct {
				got string
				err error
			}
			done := make(chan read, 1)
			go func() {
				buf := make([]byte, 32)
				n, err := e.Read(buf)
				done <- read{string(buf[:n]), err}
			}()
			select {
			case r := <-done:
				if r.err != nil {
					t.Fatalf("Read: %v", r.err)
				}
				if r.got != tc.want {
					t.Errorf("SendMouse(%T): got %q, want %q", tc.event, r.got, tc.want)
				}
			case <-time.After(10 * time.Second):
				// Closing ends the pending Read and SendMouse.
				_ = e.Close()
				t.Fatalf("SendMouse(%T): nothing to read after 10s, want %q", tc.event, tc.want)
			}
		})
	}
}
