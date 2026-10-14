package vt

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// newLookaheadEmulator returns a 10x3 emulator that discards its replies, so
// that a query among the actions below doesn't block its Write.
func newLookaheadEmulator(t testing.TB) *Emulator {
	e := newTestTerminal(t, 10, 3)
	e.SetReplyFunc(func([]byte) {})
	return e
}

// closingActions are the actions that end the cluster being printed: each
// control x/vt executes, and each sequence or string it dispatches. Each
// reaches ground state.
func closingActions() []struct{ name, action string } {
	var actions []struct{ name, action string }
	add := func(name, action string) {
		actions = append(actions, struct{ name, action string }{name, action})
	}
	// Every C0 control but ESC, which begins a sequence: CAN and SUB in
	// ground state among them. Then DEL.
	for c := range byte(0x20) {
		if c != 0x1b {
			add(fmt.Sprintf("C0 %#02x", c), string([]byte{c}))
		}
	}
	add("DEL", "\x7f")
	// Every C1 control executed in ground state; the others begin a
	// sequence or a string.
	for c := byte(0x80); c < 0xa0; c++ {
		switch c {
		case 0x90, 0x98, 0x9b, 0x9d, 0x9e, 0x9f:
		default:
			add(fmt.Sprintf("C1 %#02x", c), string([]byte{c}))
		}
	}
	// CAN and SUB, executed where they cut a sequence off.
	add("CAN in a CSI", "\x1b[1\x18")
	add("SUB in an ESC", "\x1b\x1a")
	add("CAN in an ESC intermediate", "\x1b(\x18")
	add("CAN in DCS parameters", "\x1bP1\x18")
	add("SGR", "\x1b[m")
	add("CUP", "\x1b[1;1H")
	add("a private CSI", "\x1b[?25h")
	add("a CSI with no handler", "\x1b[y")
	add("CSI b with a prefix", "\x1b[?b")
	add("a CSI by C1", "\x9bm")
	add("DECSC", "\x1b7")
	add("SCS", "\x1b(B")
	add("RIS", "\x1bc")
	add("an ESC with no handler", "\x1bV")
	add("OSC ended by BEL", "\x1b]0;t\x07")
	for _, s := range []struct{ name, start string }{
		{"OSC", "\x1b]2;t"}, {"DCS", "\x1bPz"}, {"APC", "\x1b_x"}, {"PM", "\x1b^x"}, {"SOS", "\x1bXx"},
	} {
		add(s.name+" ended by ESC \\", s.start+"\x1b\\")
		// ST as one byte leaves the string's own dispatch the only
		// action, where ESC \ is a dispatch of its own after it.
		add(s.name+" ended by C1 ST", s.start+"\x9c")
	}
	add("OSC holding a rune", "\x1b]0;✳\x07")
	add("OSC with no handler", "\x1b]999;x\x07")
	add("DECRQSS", "\x1bP$qm\x9c")
	add("OSC by C1", "\x9d0;t\x07")
	add("DCS by C1", "\x90z\x9c")
	add("APC by C1", "\x9fx\x9c")
	add("PM by C1", "\x9ex\x9c")
	add("SOS by C1", "\x98x\x9c")
	return actions
}

// keepingActions reach ground state with nothing dispatched, so x/vt (like
// xterm) keeps the cluster being printed, and they are no handoff point: a
// string CAN or SUB aborts, a CSI the parser ignores, and a byte it ignores
// in ground state.
func keepingActions() []struct{ name, action string } {
	return []struct{ name, action string }{
		{"OSC aborted by CAN", "\x1b]0;x\x18"},
		{"OSC aborted by SUB", "\x1b]0;x\x1a"},
		{"DCS aborted by CAN", "\x1bPz\x18"},
		{"APC aborted by SUB", "\x1b_x\x1a"},
		{"PM aborted by CAN", "\x1b^x\x18"},
		{"SOS aborted by SUB", "\x1bXx\x1a"},
		{"an ignored CSI", "\x1b[$1"},
		{"a byte the parser ignores", "\xff"},
	}
}

// rowsOf returns the text of each of e's rows.
func rowsOf(e *Emulator) []string {
	rows := make([]string, e.Height())
	for y := range rows {
		rows[y] = rowText(e, y)
	}
	return rows
}

func TestEveryNonPrintActionClosesTheCluster(t *testing.T) {
	// The carry the actions below have to end: with nothing between, a
	// mark in the next Write joins the "e".
	t.Run("nothing between", func(t *testing.T) {
		e := newLookaheadEmulator(t)
		e.WriteString("e")
		if !e.Carrying() {
			t.Error("Carrying after \"e\": got = false, want = true")
		}
		e.WriteString("\u0301")
		if c := e.CellAt(0, 0); c == nil || c.Content != "e\u0301" {
			t.Errorf("cell 0: got = %+v, want e\u0301", c)
		}
	})

	for _, tc := range closingActions() {
		t.Run(tc.name, func(t *testing.T) {
			e := newLookaheadEmulator(t)
			e.WriteString("e")
			if got := e.GroundPrefix([]byte(tc.action)); got != len(tc.action) {
				t.Errorf("GroundPrefix(%q): got = %d, want = %d", tc.action, got, len(tc.action))
			}
			if got := e.HandoffPrefix([]byte(tc.action)); got != len(tc.action) {
				t.Errorf("HandoffPrefix(%q): got = %d, want = %d", tc.action, got, len(tc.action))
			}
			e.WriteString(tc.action)
			if e.Carrying() || !e.AtBoundary() {
				t.Errorf("after %q: got Carrying %t, AtBoundary %t, want false and true", tc.action, e.Carrying(), e.AtBoundary())
			}
			e.WriteString("\u0301")
			rows := rowsOf(e)
			if slices.ContainsFunc(rows, func(r string) bool { return strings.Contains(r, "\u0301") }) {
				t.Errorf("rows: got = %q, want the mark dropped", rows)
			}
			whole := newLookaheadEmulator(t)
			whole.WriteString("e" + tc.action + "\u0301")
			if w := rowsOf(whole); !slices.Equal(rows, w) {
				t.Errorf("rows: got = %q, in one Write = %q", rows, w)
			}
		})
	}

	for _, tc := range keepingActions() {
		t.Run(tc.name, func(t *testing.T) {
			e := newLookaheadEmulator(t)
			e.WriteString("e")
			if got := e.GroundPrefix([]byte(tc.action)); got != len(tc.action) {
				t.Errorf("GroundPrefix(%q): got = %d, want = %d", tc.action, got, len(tc.action))
			}
			if got := e.HandoffPrefix([]byte(tc.action)); got != 0 {
				t.Errorf("HandoffPrefix(%q): got = %d, want = 0", tc.action, got)
			}
			e.WriteString(tc.action)
			if !e.Carrying() {
				t.Errorf("Carrying after %q: got = false, want = true", tc.action)
			}
			e.WriteString("\u0301")
			whole := newLookaheadEmulator(t)
			whole.WriteString("e" + tc.action + "\u0301")
			for _, e := range []*Emulator{e, whole} {
				if c := e.CellAt(0, 0); c == nil || c.Content != "e\u0301" {
					t.Errorf("cell 0: got = %+v, want e\u0301", c)
				}
			}
		})
	}

	// REP prints the last character again, which is the cluster carried
	// then. With a count of 0 it prints nothing and x/vt carries none, but
	// the lookahead can't tell, so REP is never a handoff point.
	for _, tc := range []struct {
		name, action string
		carrying     bool
		want         []string
	}{
		{"REP", "\x1b[b", true, []string{"ee\u0301"}},
		{"REP with a count of 0", "\x1b[0b", false, []string{"e"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newLookaheadEmulator(t)
			e.WriteString("e")
			if got := e.HandoffPrefix([]byte(tc.action)); got != 0 {
				t.Errorf("HandoffPrefix(%q): got = %d, want = 0", tc.action, got)
			}
			e.WriteString(tc.action)
			if e.Carrying() != tc.carrying {
				t.Errorf("Carrying after %q: got = %t, want = %t", tc.action, e.Carrying(), tc.carrying)
			}
			e.WriteString("\u0301")
			whole := newLookaheadEmulator(t)
			whole.WriteString("e" + tc.action + "\u0301")
			for _, e := range []*Emulator{e, whole} {
				if got := rowsOf(e)[:1]; !slices.Equal(got, tc.want) {
					t.Errorf("row 0: got = %q, want = %q", got, tc.want)
				}
			}
		})
	}
}

// The next Write extends a carried cluster only while it is shorter than
// maxCarriedCluster bytes, and Carrying says so at the bound.
func TestCarryingStopsAtTheLongestClusterCarried(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cluster  string
		carrying bool
	}{
		{"a byte short of the bound", "e" + strings.Repeat("\u0301", 63), true},
		{"at the bound", "e" + strings.Repeat("\u0301", 62) + "\u20d0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if n := len(tc.cluster); n != maxCarriedCluster-1 && n != maxCarriedCluster {
				t.Fatalf("cluster of %d bytes, want %d or %d", n, maxCarriedCluster-1, maxCarriedCluster)
			}
			e := newLookaheadEmulator(t)
			e.WriteString(tc.cluster)
			if e.Carrying() != tc.carrying {
				t.Errorf("Carrying after %d bytes: got = %t, want = %t", len(tc.cluster), e.Carrying(), tc.carrying)
			}
			e.WriteString("\u0301")
			if joined := e.CellAt(0, 0).Content == tc.cluster+"\u0301"; joined != tc.carrying {
				t.Errorf("the next Write's mark joined: got = %t, want = %t", joined, tc.carrying)
			}
		})
	}
}

// A resize between a base character and its combining mark ends the
// cluster.
func TestAResizeClosesTheCluster(t *testing.T) {
	for _, size := range [][2]int{{8, 3}, {10, 3}} {
		t.Run(fmt.Sprintf("to %dx%d", size[0], size[1]), func(t *testing.T) {
			e := newLookaheadEmulator(t)
			e.WriteString("e")
			e.Resize(size[0], size[1])
			if e.Carrying() {
				t.Error("Carrying after the resize: got = true, want = false")
			}
			e.WriteString("\u0301")
			if c := e.CellAt(0, 0); c == nil || c.Content != "e" {
				t.Errorf("cell 0: got = %+v, want e", c)
			}
		})
	}
}

// lookaheadState is what the lookaheads must leave alone: the screen, the
// cursor, and the state the next Write starts from.
type lookaheadState struct {
	rows              string
	x, y              int
	pending, carrying bool
	boundary          bool
	partial, single   int
	last              rune
}

func stateOf(e *Emulator) lookaheadState {
	x, y := e.scr.CursorPosition()
	return lookaheadState{
		strings.Join(rowsOf(e), "\n"), x, y, e.PendingWrap(), e.Carrying(),
		e.AtBoundary(), e.Partial(), e.SingleShift(), e.LastChar(),
	}
}

// Calling the lookaheads changes nothing: an emulator asked at every byte of
// its input, mid-sequence, mid-string and mid-rune, and about bytes it is
// never given, ends as one that is never asked.
func TestLookaheadHasNoEffect(t *testing.T) {
	input := []byte("ab\x1b[1;31mc\x1b]0;✳ t\x07d\u0301\xe2\x9c\xb3\x1bP1$qm\x1b\\" +
		"\x1b_\xc3\xa9\x1b\\e\x1b[2Df\x1b]8;;x\x1b\\g\x1b(0q\x1b(B\x1b[$1h\x1b]0;x\x18i")
	asked, quiet := newLookaheadEmulator(t), newLookaheadEmulator(t)
	for i := range input {
		for _, b := range [][]byte{input[i:], []byte("\x1b[1;"), []byte("\xe2\x9c"), []byte("\x1b]0;\x9c\x07")} {
			asked.GroundPrefix(b)
			asked.HandoffPrefix(b)
		}
		asked.Write(input[i : i+1])
		quiet.Write(input[i : i+1])
		if a, q := stateOf(asked), stateOf(quiet); a != q {
			t.Fatalf("after %q: asked = %+v, never asked = %+v", input[:i+1], a, q)
		}
	}
}

// Random bytes written in random pieces, on a screen of up to 20x5: after
// HandoffPrefix's prefix the emulator carries no cluster and is between
// sequences, and after GroundPrefix's it is between sequences.
func FuzzHandoffPrefixLeavesNothingCarried(f *testing.F) {
	for _, s := range []struct {
		w, h   uint8
		pre, b string
	}{
		{19, 4, "e", "\x1b[m\u0301"},
		{19, 4, "e", "\x1b]0;x\x18\u0301"},
		{19, 4, "e", "\x1b[$1\u0301\r"},
		{19, 4, "x", "\x1b[b"},
		{19, 4, "x", "\x1b[\x02`\u0301"},
		{19, 4, "x\x1b[", "\x02`"},
		{19, 4, "\x1b]0;\xe2", "\x9c\xb3\x07e"},
		{19, 4, "ab", "\r\ncd\u0301\x1b[3b"},
		{19, 4, "\x1b(0q", "\u0302\x1b[b"},
		{19, 4, "日\u200b", "\u0301\x07"},
		{19, 4, "\x1bP1", "\x18e\u0301"},
		// A cluster too wide for the line is carried unprinted: on 1x1, where
		// a control ends it, and at the last column without DECAWM, where a
		// mark joins it first.
		{0, 0, "日", "\x07\u0301"},
		{19, 4, "\x1b[?7l\x1b[1;20H日", "\u0301\x1b[m\u0301"},
	} {
		f.Add(s.w, s.h, []byte(s.pre), []byte(s.b))
	}
	f.Fuzz(func(t *testing.T, w, h uint8, pre, b []byte) {
		e := NewEmulator(1+int(w)%20, 1+int(h)%5)
		e.SetReplyFunc(func([]byte) {})
		// REP does the work its count asks for, up to 65,535 prints each,
		// so an input past about a million cell operations is skipped
		// rather than left to run past the fuzzer's time limit.
		calls := 0
		e.SetStopFunc(func() bool {
			calls++
			return calls > 16
		})
		write := func(p []byte) {
			if _, err := e.Write(p); errors.Is(err, ErrStopped) {
				t.Skip("more work than the fuzz allows an input")
			}
		}
		write(pre)
		n, g := e.HandoffPrefix(b), e.GroundPrefix(b)
		if n > g {
			t.Fatalf("after %q: HandoffPrefix(%q) = %d past GroundPrefix = %d", pre, b, n, g)
		}
		write(b[:n])
		if n > 0 && (e.Carrying() || !e.AtBoundary()) {
			t.Fatalf("after %q then %q: got Carrying %t, AtBoundary %t, want false and true", pre, b[:n], e.Carrying(), e.AtBoundary())
		}
		write(b[n:g])
		if !e.AtBoundary() && g > 0 {
			t.Fatalf("after %q then %q: AtBoundary got = false, want = true", pre, b[:g])
		}
	})
}
