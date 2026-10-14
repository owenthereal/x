package ansi

import (
	"reflect"
	"slices"
	"testing"

	"github.com/charmbracelet/x/ansi/parser"
)

// copyParser returns a copy of p that shares no memory with it and calls h,
// so that feeding the copy leaves p as it was.
func copyParser(p *Parser, h Handler) *Parser {
	return &Parser{
		handler:   h,
		params:    slices.Clone(p.params),
		data:      slices.Clone(p.data),
		dataLen:   p.dataLen,
		paramsLen: p.paramsLen,
		cmd:       p.cmd,
		state:     p.state,
		strRune:   p.strRune,
	}
}

// sameParser reports whether a and b hold the same state, their handlers
// aside.
func sameParser(a, b *Parser) bool {
	return slices.Equal(a.params, b.params) && slices.Equal(a.data, b.data) &&
		a.dataLen == b.dataLen && a.paramsLen == b.paramsLen && a.cmd == b.cmd &&
		a.state == b.state && a.strRune == b.strRune
}

// copyParser and sameParser name every field of the parser, so the
// references below see all of its state. A field the parser gains has to be
// added to both.
func TestCopyParserCopiesEveryField(t *testing.T) {
	if n := reflect.TypeFor[Parser]().NumField(); n != 8 {
		t.Fatalf("Parser has %d fields; copyParser and sameParser copy and compare 8", n)
	}
}

// referenceLookahead feeds b to a copy of p, and returns the longest prefix
// of b after which the copy is in ground state, and the longest after which
// it is in ground state right after handing the handler something other than
// a printed character. Advancing one copy byte by byte visits the state after
// every prefix, as feeding a fresh copy each prefix would.
func referenceLookahead(p *Parser, b []byte) (ground, handoff int) {
	// Whether the last handler call for the byte being fed was a control
	// function rather than a print. A malformed rune makes two calls for one
	// byte: the replacement character, then what the byte itself does.
	var handed bool
	control := func() { handed = true }
	q := copyParser(p, Handler{
		Print:   func(rune) { handed = false },
		Execute: func(byte) { control() },
		// REP repeats the last graphic character: a print.
		HandleCsi: func(cmd Cmd, _ Params) { handed = cmd != 'b' },
		HandleEsc: func(Cmd) { control() },
		HandleDcs: func(Cmd, Params, []byte) { control() },
		HandleOsc: func(int, []byte) { control() },
		HandleApc: func([]byte) { control() },
		HandlePm:  func([]byte) { control() },
		HandleSos: func([]byte) { control() },
	})
	for i, c := range b {
		handed = false
		q.Advance(c)
		if q.state == parser.GroundState {
			ground = i + 1
			if handed {
				handoff = i + 1
			}
		}
	}
	return ground, handoff
}

// lookaheadSeeds start a parser in each kind of state, and end in each kind
// of sequence, string and rune, finished or not.
var lookaheadSeeds = []struct{ pre, b string }{
	{"", "ab\x1b[1;"},
	{"", "ab\r\nc"},
	{"", "\xe2\x9c"},
	{"", "\x1b]0;\xe2\x9c\xb3 x\x07ok"},
	{"", "\x1b]0;\xe2\x9c\xb3"},
	{"", "\x1b_\xc3\xa9"},
	{"", "\x1b]0;\xe0\x9cx\x07"},
	{"", "\x1b]0;a\x9cok"},
	{"", "\x1b_\xe2\x9c\xb3\x1b\\\x9f\xf0\x9f\x98\x80\x9c"},
	{"", "\x1bP1$qm\x1b\\\x1bPz\x18\x1bP1\x18"},
	{"", "\x1bXa\x1a\x1b^b\x18\x1b_c\x85"},
	{"", "e\x1b[$1\x1b[1\x18\x1b\x1b\x1bc"},
	{"", "x\x1b[b\x1b[?b\x1b[\x02`\x1b[3 b"},
	{"", "\xe2\r\xe2x\xe2\x1b[m\x80\xa0\xff\xc0"},
	{"\x1b[1", ";2H"},
	{"\x1b[?", "b"},
	{"\x1b[\x02", "`"},
	{"\xe2\x9c", "\xb3"},
	{"\xf0\x9f", "\x98x"},
	{"\x1b]0;\xe2", "\x9c\xb3\x07"},
	{"\x1b]0;\xe2", "\x9c\xb3"},
	{"", "\x1b]0;\xe2x\x9c"},
	{"\x1b]0;", "\x9c"},
	{"\x1bP", "1;2|x\x9c"},
	{"\x1b", "\x1b[m"},
	{"\x1b(", "0q"},
}

// Random starting states and random bytes: GroundPrefix is the longest prefix
// after which a copy of the parser fed it is in ground state, and asking
// leaves the parser as it was.
func FuzzGroundPrefixMatchesACopiedParser(f *testing.F) {
	for _, s := range lookaheadSeeds {
		f.Add([]byte(s.pre), []byte(s.b))
	}
	f.Fuzz(func(t *testing.T, pre, b []byte) {
		p := NewParser()
		p.SetDataSize(64)
		p.Parse(pre)
		before := copyParser(p, Handler{})
		got := p.GroundPrefix(b)
		if !sameParser(p, before) {
			t.Fatalf("GroundPrefix(%q) after %q changed the parser", b, pre)
		}
		if want, _ := referenceLookahead(p, b); got != want {
			t.Fatalf("GroundPrefix(%q) after %q: got = %d, want = %d", b, pre, got, want)
		}
	})
}

// The same for HandoffPrefix: the longest prefix ending in ground state right
// after the parser handed its handler a control function, not a print.
func FuzzHandoffPrefixMatchesACopiedParser(f *testing.F) {
	for _, s := range lookaheadSeeds {
		f.Add([]byte(s.pre), []byte(s.b))
	}
	f.Fuzz(func(t *testing.T, pre, b []byte) {
		p := NewParser()
		p.SetDataSize(64)
		p.Parse(pre)
		before := copyParser(p, Handler{})
		got := p.HandoffPrefix(b)
		if !sameParser(p, before) {
			t.Fatalf("HandoffPrefix(%q) after %q changed the parser", b, pre)
		}
		if _, want := referenceLookahead(p, b); got != want {
			t.Fatalf("HandoffPrefix(%q) after %q: got = %d, want = %d", b, pre, got, want)
		}
	})
}

func TestLookaheadCases(t *testing.T) {
	for _, tc := range []struct {
		name            string
		pre             string // fed to the parser first
		b               string
		ground, handoff int
	}{
		{"an unfinished CSI", "", "ab\x1b[1;", 2, 0},
		{"printing after a newline", "", "ab\r\nc", 5, 4},
		{"an unfinished rune", "", "\xe2\x9c", 0, 0},
		// The rune's 0x9C is not ST, and the OSC runs to the BEL.
		{"an OSC holding a rune", "", "\x1b]0;\xe2\x9c\xb3 x\x07ok", 12, 10},
		{"an unfinished OSC holding a rune", "", "\x1b]0;\xe2\x9c\xb3", 0, 0},
		{"an unfinished APC holding a rune", "", "\x1b_\xc3\xa9", 0, 0},
		// Inside a string any lead and any continuation byte make a rune,
		// as the parser reads them, so 0x9C after 0xE0 is not ST either.
		{"E0 9C inside an OSC", "", "\x1b]0;\xe0\x9c", 0, 0},
		{"E0 9C inside an OSC, then BEL", "", "\x1b]0;\xe0\x9cx\x07", 8, 8},
		{"a lone C1 ST ends an OSC", "", "\x1b]0;a\x9cok", 8, 6},
		// A byte that isn't a continuation ends the rune, and an ST after
		// it is ST again.
		{"ST after a rune cut short inside an OSC", "", "\x1b]0;\xe2x\x9c", 7, 7},
		{"ESC \\ ends an APC", "", "\x1b_x\x1b\\", 5, 5},
		{"a C1 control inside an APC is executed", "", "\x1b_x\x85", 4, 4},
		{"a C1 byte inside an OSC is data", "", "\x1b]0;\x85\x07", 6, 6},
		// Aborted strings and ignored CSIs reach ground state with nothing
		// dispatched.
		{"CAN aborting an OSC", "", "\x1b]0;x\x18", 6, 0},
		{"SUB aborting an APC", "", "\x1b_x\x1a", 4, 0},
		{"CAN aborting a DCS string", "", "\x1bPz\x18", 4, 0},
		{"an ignored CSI", "", "\x1b[$1", 4, 0},
		// CAN and SUB anywhere else are executed.
		{"CAN", "", "\x18", 1, 1},
		{"SUB inside a CSI", "", "\x1b[1\x1a", 4, 4},
		{"CAN inside DCS parameters", "", "\x1bP1\x18", 4, 4},
		{"two ESCs", "", "\x1b\x1b", 0, 0},
		{"two ESCs, then a final", "", "\x1b\x1bc", 3, 3},
		// A malformed rune prints a replacement character, then the byte
		// that ended it is read afresh.
		{"a rune cut by CR", "", "\xe2\r", 2, 2},
		{"a rune cut by a letter", "", "\xe2x", 2, 0},
		{"a rune cut by a CSI", "", "\xe2\x1b[m", 4, 4},
		{"a lone C1 control", "", "\x80", 1, 1},
		{"a byte the parser ignores", "", "\r\xff", 2, 1},
		// REP repeats the last graphic character, so it prints.
		{"REP", "", "x\x1b[b", 4, 0},
		{"REP with a count", "", "x\x1b[2b", 5, 0},
		{"CSI b with a prefix is not REP", "", "x\x1b[?b", 5, 5},
		{"CSI b with an intermediate is not REP", "", "x\x1b[ b", 5, 5},
		{"ESC b is not REP", "", "x\x1bb", 3, 3},
		{"OSC 98 is not REP", "", "x\x1b]98\x07", 6, 6},
		// A C0 executed inside a CSI replaces the command collected so
		// far with the C0 byte, and the final byte is ORed into it: STX
		// then '`' dispatches 0x02|0x60, which is 'b'.
		{"a C0 inside a CSI that makes it REP", "", "x\x1b[\x02`", 5, 0},
		// Continuing from where the parser is.
		{"the rest of a CSI", "\x1b[1", ";2H", 3, 3},
		{"the rest of a CSI with a prefix", "\x1b[?", "b", 1, 1},
		{"the rest of a CSI the C0 made REP", "\x1b[\x02", "`", 1, 0},
		{"the rest of a rune", "\xe2\x9c", "\xb3", 1, 0},
		{"a rune cut short", "\xf0\x9f", "\x98x", 2, 0},
		{"the rest of a rune inside an OSC", "\x1b]0;\xe2", "\x9c\xb3\x07", 3, 3},
		{"the rest of a rune inside an OSC, unfinished", "\x1b]0;\xe2", "\x9c\xb3", 0, 0},
		{"ST after an OSC", "\x1b]0;", "\x9c", 1, 1},
		{"the rest of an ESC", "\x1b", "\x1b[m", 3, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser()
			p.Parse([]byte(tc.pre))
			if got := p.GroundPrefix([]byte(tc.b)); got != tc.ground {
				t.Errorf("GroundPrefix(%q) after %q: got = %d, want = %d", tc.b, tc.pre, got, tc.ground)
			}
			if got := p.HandoffPrefix([]byte(tc.b)); got != tc.handoff {
				t.Errorf("HandoffPrefix(%q) after %q: got = %d, want = %d", tc.b, tc.pre, got, tc.handoff)
			}
		})
	}
}

func TestLookaheadAllocatesNothing(t *testing.T) {
	p := NewParser()
	p.Parse([]byte("\x1b]0;"))
	b := []byte("\xe2\x9c\xb3\x07ab\x1b[1;2H\xe2\x1b[\x02`\x1bP1\x18\xf0\x9f")
	if n := testing.AllocsPerRun(100, func() {
		p.GroundPrefix(b)
		p.HandoffPrefix(b)
	}); n != 0 {
		t.Errorf("allocations per lookahead: got = %v, want = 0", n)
	}
}
