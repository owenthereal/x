package vt

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
)

// A combining mark belongs to the character before it, however that character
// arrived. The printable-ASCII path used to write its cell immediately, which
// left a following mark with nowhere to go: it became a zero-width cell of its
// own, and the next write or erase destroyed it.
func TestCombiningMarkJoinsPrecedingASCII(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  []string
	}{
		{"bare", "bcé", []string{"b", "c", "é"}},
		{"then erase", "bcé\x1b[K", []string{"b", "c", "é"}},
		{"then another character", "bcéx", []string{"b", "c", "é", "x"}},
		{"several in a row", "áb́c", []string{"á", "b́", "c"}},
		{"mark on a wide character", "世́x", []string{"世́", "", "x"}},
		{"emoji with a modifier", "\U0001f44b\U0001f3ffx", []string{"\U0001f44b\U0001f3ff", "", "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := newTestTerminal(t, 10, 1)
			term.WriteString(tc.input)
			for x, want := range tc.want {
				cell := term.CellAt(x, 0)
				if cell == nil {
					t.Fatalf("cell %d: nil", x)
				}
				if cell.Content != want {
					t.Errorf("cell %d: got %q, want %q", x, cell.Content, want)
				}
			}
		})
	}
}

// A character is held back until the next one arrives, so it has to be flushed
// when the write ends or a caller reading the screen would not see it.
func TestLastCharacterIsFlushedWhenTheWriteEnds(t *testing.T) {
	term := newTestTerminal(t, 10, 1)
	term.WriteString("hi")
	for x, want := range []string{"h", "i"} {
		if cell := term.CellAt(x, 0); cell == nil || cell.Content != want {
			t.Errorf("cell %d: got %v, want %q", x, cell, want)
		}
	}
}

// A single shift applies to one character, and the character it applies to may
// be a cluster no charset has a mapping for. Taking the shift only when the
// mapping happens left it set, and it landed on the character after.
func TestSingleShiftDoesNotLeakPastACluster(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  []string
	}{
		// SS2, then a cluster, then a plain character. The cluster has no
		// mapping; the "b" must not be drawn from the shifted set.
		{"cluster then plain", "\x1b*0\x8eáb", []string{"á", "b"}},
		// The same shift with a mappable character, to show it still applies.
		{"plain then plain", "\x1b*0\x8eab", []string{"▒", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := newTestTerminal(t, 10, 1)
			term.WriteString(tc.input)
			for x, want := range tc.want {
				if cell := term.CellAt(x, 0); cell == nil || cell.Content != want {
					t.Errorf("cell %d: got %v, want %q", x, cell, want)
				}
			}
		})
	}
}

// A run of combining marks is one cluster that keeps growing. Asking where its
// boundaries are on every character costs the length of the buffer each time,
// which is quadratic: 64KB of marks took six seconds before this was a single
// segmentation at the end of the run.
//
// The budget below is thousands of times the linear cost and a fraction of the
// quadratic one, so it says which of the two is in place without being a
// stopwatch on the machine it runs on.
func TestLongClusterRunStaysLinear(t *testing.T) {
	input := "a" + strings.Repeat("́", 32*1024)

	done := make(chan time.Duration, 1)
	go func() {
		term := NewEmulator(80, 24)
		start := time.Now()
		term.WriteString(input)
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		t.Logf("%d bytes in %v", len(input), elapsed)
	case <-time.After(10 * time.Second):
		t.Fatalf("writing %d bytes of one cluster did not finish in 10s", len(input))
	}
}

func TestClusterWithoutBaseIsDropped(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"combining mark after a control", "ab\r\u0301"},
		{"lone ZWJ after a control", "ab\r\u200d"},
		{"variation selector after a control", "ab\r\ufe0f"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := newTestTerminal(t, 10, 1)
			term.WriteString(tc.input)
			if got := rowText(term, 0); got != "ab" {
				t.Errorf("row 0: got = %q, want = %q", got, "ab")
			}
			if c := term.CellAt(0, 0); c == nil || c.Width != 1 || c.Content != "a" {
				t.Errorf("cell 0: got = %+v, want a", c)
			}
			if x, _ := term.scr.CursorPosition(); x != 0 {
				t.Errorf("cursor x: got = %d, want = 0", x)
			}
		})
	}
}

// A PTY read can end anywhere, including inside a grapheme cluster. A
// cluster split across two Writes lands in one cell, as it does when it
// arrives in one.
func TestClusterAcrossWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   []string // cell contents from column 0
	}{
		{"ZWJ sequence cut after a ZWJ", []string{"\U0001F468\u200d", "\U0001F469\u200d\U0001F467x"}, []string{"\U0001F468\u200d\U0001F469\u200d\U0001F467", "", "x"}},
		{"combining mark in the next write", []string{"ae", "\u0301b"}, []string{"a", "e\u0301", "b"}},
		{"rune split across writes", []string{"e\xcc", "\x81b"}, []string{"e\u0301", "b"}},
		{"flag split between its regional indicators", []string{"\U0001F1FA", "\U0001F1F8x"}, []string{"\U0001F1FA\U0001F1F8", "", "x"}},
		{"wide base at the end of the line", []string{"abcdefgh世", "\u0301"}, []string{"a", "b", "c", "d", "e", "f", "g", "h", "世\u0301", ""}},
		{"insert mode shifts once", []string{"xyz\x1b[1;1H\x1b[4he", "\u0301"}, []string{"e\u0301", "x", "y", "z"}},
		{"not joined after a control", []string{"e", "\r", "\u0301"}, []string{"e"}},
		{"not joined after a sequence that does not move the cursor", []string{"ae", "\x1b[1m", "\u0301"}, []string{"a", "e", " "}},
		{"not joined to an ASCII character", []string{"e", "f"}, []string{"e", "f"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := newTestTerminal(t, 10, 1)
			for _, c := range tc.chunks {
				term.WriteString(c)
			}
			for x, want := range tc.want {
				if c := term.CellAt(x, 0); c == nil || c.Content != want {
					t.Errorf("cell %d: got %+v, want %q", x, c, want)
				}
			}
		})
	}

	t.Run("carried cluster is bounded", func(t *testing.T) {
		term := newTestTerminal(t, 10, 1)
		term.WriteString("e")
		for range 1000 {
			term.WriteString("\u0301")
		}
		if n := len(term.CellAt(0, 0).Content); n > maxCarriedCluster+utf8.UTFMax {
			t.Errorf("cell content: got %d bytes, want at most %d", n, maxCarriedCluster+utf8.UTFMax)
		}
	})
}

// A Write that ends in a cluster with no base, dropped, carries nothing: not
// the cluster printed before it, across whatever came between. Each input
// split where it is ends as the same bytes in one Write, where the marks have
// nothing to attach to and are dropped.
func TestNoStaleClusterIsCarried(t *testing.T) {
	for _, tc := range []struct {
		name   string
		w, h   int
		chunks []string
		want   wideState
	}{
		{"across a BEL", 10, 1, []string{"ab\x07\u0301", "\u0301x"}, wideState{[]string{"abx"}, uv.Pos(3, 0), false}},
		{"across an SGR", 10, 1, []string{"e\x1b[1m\u0301", "\u0301"}, wideState{[]string{"e"}, uv.Pos(1, 0), false}},
		{"onto a cell erased since", 8, 2, []string{"e\x1b[1;1H\x1b[X\x1b[1;2H\u0301", "\u0301"}, wideState{[]string{"", ""}, uv.Pos(1, 0), false}},
		{"onto another row's text scrolled up", 8, 2, []string{"\x1b[2;1Hq\x1b[1;1He\x1b[S\u0301", "\u0301"}, wideState{[]string{"q", ""}, uv.Pos(1, 0), false}},
		// The undo at the last column would put back the 日 the heart
		// wrote over, which the erase took.
		{"onto a cell erased since at the last column", 5, 2, []string{"\x1b[?7labc日\x1b[1;5H\u2764\x1b[1;4H\x1b[41m\x1b[X\x1b[1;5H\u0301", "\ufe0f"}, wideState{[]string{"abc \u2764", ""}, uv.Pos(4, 0), false}},
		// Joined to the "a", the ZWJs would make REP find no character
		// to repeat.
		{"a lone ZWJ, then REP", 8, 1, []string{"a\x1b[m\u200d", "\u200d\x1b[3b"}, wideState{[]string{"aaaa"}, uv.Pos(4, 0), false}},
		// A zero-width space is a cluster of its own after the 日, and is
		// dropped in the same flush that prints the 日.
		{"a zero-width space after a printed cluster", 6, 1, []string{"日\u200b", "\u0301"}, wideState{[]string{"日"}, uv.Pos(2, 0), false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, whole := writeWide(tc.w, tc.h, strings.Join(tc.chunks, ""))
			whole.check(t, tc.want)
			_, split := writeWide(tc.w, tc.h, tc.chunks...)
			split.check(t, tc.want)
		})
	}

	// Resize carries nothing over itself, and leaves nothing a later Write
	// could carry again.
	t.Run("across a resize", func(t *testing.T) {
		want := wideState{[]string{"e", ""}, uv.Pos(1, 0), false}
		for _, chunks := range [][]string{{"\x1b[1m\u0301\u0301"}, {"\x1b[1m\u0301", "\u0301"}} {
			e := NewEmulator(10, 2)
			e.WriteString("e")
			e.Resize(8, 2)
			for _, c := range chunks {
				e.WriteString(c)
			}
			wideState{[]string{rowText(e, 0), rowText(e, 1)}, e.CursorPosition(), e.PendingWrap()}.check(t, want)
		}
	})
}

// A cluster with no base is dropped, but the next Write can still extend it,
// as the same bytes in one Write would: what joins it is dropped with it, or,
// where the joined cluster has a width (a mark and VS16), printed with it.
// Joining nothing, the next Write prints as it would anyway.
func TestABaseLessClusterIsCarried(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   wideState
	}{
		{"a ZWJ, then a spacing mark", []string{"\u200d", "\u093f"}, wideState{[]string{""}, uv.Pos(0, 0), false}},
		{"a mark, then a spacing mark", []string{"\u0301", "\u093f"}, wideState{[]string{""}, uv.Pos(0, 0), false}},
		{"a mark after text, then a spacing mark", []string{"a\x1b[m\u0301", "\u093f"}, wideState{[]string{"a"}, uv.Pos(1, 0), false}},
		{"a ZWJ, then an ignored CSI and a spacing mark", []string{"\u200d", "\x1b[$1\u093f"}, wideState{[]string{""}, uv.Pos(0, 0), false}},
		{"a mark, then VS16", []string{"\u0301", "\ufe0f"}, wideState{[]string{"\u0301\ufe0f"}, uv.Pos(2, 0), false}},
		{"a mark, then VS16 and text", []string{"\u0301", "\ufe0fx"}, wideState{[]string{"\u0301\ufe0fx"}, uv.Pos(3, 0), false}},
		{"a mark, then VS16 and a spacing mark", []string{"\u0301", "\ufe0f\u093f"}, wideState{[]string{"\u0301\ufe0f\u093f"}, uv.Pos(2, 0), false}},
		// A zero-width space ends its cluster, so nothing joins it.
		{"a zero-width space, then a spacing mark", []string{"\u200b", "\u093f"}, wideState{[]string{"\u093f"}, uv.Pos(1, 0), false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, whole := writeWide(8, 1, strings.Join(tc.chunks, ""))
			whole.check(t, tc.want)
			_, split := writeWide(8, 1, tc.chunks...)
			split.check(t, tc.want)
			if e, _ := writeWide(8, 1, tc.chunks[0]); !e.Carrying() {
				t.Errorf("Carrying after %q: got = false, want = true", tc.chunks[0])
			}
		})
	}
}

// A cluster too wide for the line isn't printed, but the next Write can still
// extend it, as the same bytes in one Write would: a mark it begins with joins
// the cluster, unprinted, rather than being dropped and carried on its own.
// Joining never narrows a cluster, so nothing the next Write adds can make it
// fit, and Carrying reports nothing.
func TestAClusterTooWideToPrintIsCarried(t *testing.T) {
	for _, tc := range []struct {
		name   string
		w      int
		chunks []string
		row    string
	}{
		{"wider than the screen", 1, []string{"世", "\u0301"}, ""},
		{"wider than the screen, the mark's bytes cut", 1, []string{"世\xcc", "\x81"}, ""},
		{"wider than the screen, a mark each Write", 1, []string{"世", "\u0301", "\u0302"}, ""},
		{"wider than the screen, after a single shift", 1, []string{"\x1bN世", "\u0301"}, ""},
		{"past the last column without autowrap", 5, []string{"\x1b[?7labcd世", "\u0301"}, "abcd"},
		{"an emoji and VS15", 5, []string{"\x1b[?7labcd\U0001F600", "\ufe0e"}, "abcd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			whole := NewEmulator(tc.w, 1)
			whole.WriteString(strings.Join(tc.chunks, ""))
			split := NewEmulator(tc.w, 1)
			for _, c := range tc.chunks {
				split.WriteString(c)
			}
			if diff := splitDiff(whole, split); diff != "" {
				t.Error(diff)
			}
			for name, e := range map[string]*Emulator{"one Write": whole, "split": split} {
				if got := rowText(e, 0); got != tc.row {
					t.Errorf("%s: row: got = %q, want = %q", name, got, tc.row)
				}
				if e.Carrying() {
					t.Errorf("%s: Carrying: got = true, want = false", name)
				}
			}
		})
	}
}

// A character a charset maps is a cluster of its own, whether a mark after it
// comes in the same Write or the next: the charset draws the byte as another
// character, and a cluster of more than one byte isn't mapped, so joined to
// the mark it would lose the mapping. xterm draws the mapped character and
// puts the mark on it; x/vt keeps the character and, as for any mark with
// nothing to attach to, drops the mark.
func TestACharsetMappedCharacterTakesNoMark(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   wideState
	}{
		{"DEC special graphics", []string{"\x1b(0q", "\u0302"}, wideState{[]string{"─"}, uv.Pos(1, 0), false}},
		// REP repeats the byte, which the charset maps again.
		{"DEC special graphics, then REP", []string{"\x1b(0q", "\u0302\x1b[b"}, wideState{[]string{"──"}, uv.Pos(2, 0), false}},
		{"a single shift", []string{"\x1b*0\x8ea", "\u0301b"}, wideState{[]string{"▒b"}, uv.Pos(2, 0), false}},
		{"a byte the charset doesn't map", []string{"\x1b(0A", "\u0301"}, wideState{[]string{"A\u0301"}, uv.Pos(1, 0), false}},
		// The shifted charset maps nothing, though GL would: the byte
		// joins its mark, and the shift is the joined cluster's.
		{"a byte the shifted charset doesn't map", []string{"\x1b(0\x1b*B\x1bNq", "\u0302"}, wideState{[]string{"q\u0302"}, uv.Pos(1, 0), false}},
		{"a byte the C1-shifted charset doesn't map", []string{"\x1b(0\x8ee", "\u0301"}, wideState{[]string{"e\u0301"}, uv.Pos(1, 0), false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, whole := writeWide(6, 1, strings.Join(tc.chunks, ""))
			whole.check(t, tc.want)
			_, split := writeWide(6, 1, tc.chunks...)
			split.check(t, tc.want)
		})
	}
}

// splitDiff describes the first way split ends unlike whole: a cell of either
// screen, which screen shows, the cursor, the wrap, LastChar, the single shift
// or Carrying. It is empty when they end alike.
func splitDiff(whole, split *Emulator) string {
	for i, scr := range []func(*Emulator) *Screen{(*Emulator).MainScreen, (*Emulator).AltScreen} {
		w, s := scr(whole), scr(split)
		for y := range w.Height() {
			for x := range w.Width() {
				if a, b := w.CellAt(x, y), s.CellAt(x, y); a == nil || !a.Equal(b) {
					return fmt.Sprintf("screen %d cell (%d, %d): one Write %+v, split %+v", i, x, y, a, b)
				}
			}
		}
	}
	type state struct {
		alt               bool
		pos               uv.Position
		pending, carrying bool
		last              rune
		single            int
	}
	stateOf := func(e *Emulator) state {
		return state{e.IsAltScreen(), e.CursorPosition(), e.PendingWrap(), e.Carrying(), e.LastChar(), e.SingleShift()}
	}
	if w, s := stateOf(whole), stateOf(split); w != s {
		return fmt.Sprintf("one Write %+v, split %+v", w, s)
	}
	return ""
}

// Random bytes cut into two to four Writes at random offsets end as the same
// bytes in one Write. The one exception is the bound on the cluster a Write
// carries: past maxCarriedCluster bytes the next Write doesn't extend it.
func FuzzWriteSplitMatchesWhole(f *testing.F) {
	for _, s := range []struct {
		w, h   int
		chunks []string
	}{
		// TestClusterAcrossWrites.
		{10, 1, []string{"\U0001F468\u200d", "\U0001F469\u200d\U0001F467x"}},
		{10, 1, []string{"ae", "\u0301b"}},
		{10, 1, []string{"e\xcc", "\x81b"}},
		{10, 1, []string{"\U0001F1FA", "\U0001F1F8x"}},
		{10, 1, []string{"abcdefgh世", "\u0301"}},
		{10, 1, []string{"xyz\x1b[1;1H\x1b[4he", "\u0301"}},
		{10, 1, []string{"e", "\r", "\u0301"}},
		{10, 1, []string{"ae", "\x1b[1m", "\u0301"}},
		// TestNoStaleClusterIsCarried.
		{10, 1, []string{"ab\x07\u0301", "\u0301x"}},
		{10, 1, []string{"e\x1b[1m\u0301", "\u0301"}},
		{8, 2, []string{"e\x1b[1;1H\x1b[X\x1b[1;2H\u0301", "\u0301"}},
		{8, 2, []string{"\x1b[2;1Hq\x1b[1;1He\x1b[S\u0301", "\u0301"}},
		{5, 2, []string{"\x1b[?7labc日\x1b[1;5H\u2764\x1b[1;4H\x1b[41m\x1b[X\x1b[1;5H\u0301", "\ufe0f"}},
		{8, 1, []string{"a\x1b[m\u200d", "\u200d\x1b[3b"}},
		{6, 1, []string{"日\u200b", "\u0301"}},
		// TestABaseLessClusterIsCarried.
		{8, 1, []string{"\u200d", "\u093f"}},
		{8, 1, []string{"a\x1b[m\u0301", "\u093f"}},
		{8, 1, []string{"\u200d", "\x1b[$1\u093f"}},
		{8, 1, []string{"\u0301", "\ufe0f\u093f"}},
		{8, 1, []string{"\u0301", "\ufe0fx"}},
		// TestACharsetMappedCharacterTakesNoMark.
		{6, 1, []string{"\x1b(0q", "\u0302\x1b[b"}},
		{6, 1, []string{"\x1b*0\x8ea", "\u0301b"}},
		{6, 1, []string{"\x1b(0\x1b*B\x1bNq", "\u0302"}},
		{6, 1, []string{"\x1b(0\x8ee", "\u0301"}},
		// The undo at the last column, from wide_test.go.
		{5, 2, []string{"\x1b[?7labcdX", "\u2764", "\ufe0f"}},
		{5, 2, []string{"\x1b[?7l\x1b[4habcdX", "\u2764", "\ufe0f"}},
		{5, 2, []string{"abc日\x1b[1;5H", "\u2764", "\ufe0f"}},
		{5, 2, []string{"\x1b[?7labcdX\x1b[1;5H\u2764\x1b[1;5H\x1b[1K\u0301", "\ufe0f"}},
		// TestLastCharSurvivesASplitCluster.
		{10, 2, []string{"ae", "\u0301", "\u0302", "\x1b[b"}},
		{10, 2, []string{"abcd", "e", "\u0301", "\x1b[2b"}},
		{5, 2, []string{"abcde", "\u0301", "\x1b[b"}},
		{10, 2, []string{"xyz\x1b[1;1H\x1b[4hae", "\u0301", "\x1b[b"}},
		// The lookahead's.
		{10, 3, []string{"x", "\x1b[b", "\u0301"}},
		{10, 3, []string{"e", "\x1b]0;x\x18", "\u0301"}},
		// TestAClusterTooWideToPrintIsCarried.
		{1, 1, []string{"世", "\u0301"}},
		{1, 1, []string{"世\xcc", "\x81"}},
		{1, 1, []string{"\x1bN世", "\u0301"}},
		{5, 1, []string{"\x1b[?7labcd\U0001F600", "\ufe0e"}},
		// Past the bound: one Write joins the mark, split doesn't.
		{10, 1, []string{"e" + strings.Repeat("\u0301", 64), "\u0301"}},
	} {
		var cuts [3]uint16
		end := 0
		for i := range cuts {
			if i < len(s.chunks)-1 {
				end += len(s.chunks[i])
			}
			cuts[i] = uint16(end)
		}
		f.Add(uint8(s.w-1), uint8(s.h-1), cuts[0], cuts[1], cuts[2], []byte(strings.Join(s.chunks, "")))
	}
	f.Fuzz(func(t *testing.T, w, h uint8, a, b, c uint16, in []byte) {
		width, height := 1+int(w%20), 1+int(h%6)
		cuts := []int{int(a) % (len(in) + 1), int(b) % (len(in) + 1), int(c) % (len(in) + 1)}
		slices.Sort(cuts)
		newEmulator := func() *Emulator {
			e := NewEmulator(width, height)
			e.SetReplyFunc(func([]byte) {})
			return e
		}
		whole, split := newEmulator(), newEmulator()
		whole.Write(in)
		bounded, from := false, 0
		for _, to := range append(cuts, len(in)) {
			bounded = bounded || split.carry.valid && len(split.carry.content) >= maxCarriedCluster
			split.Write(in[from:to])
			from = to
		}
		if diff := splitDiff(whole, split); diff != "" && !bounded {
			t.Fatalf("%dx%d, %q cut at %v: %s", width, height, in, cuts, diff)
		}
	})
}

func TestPrintingASCIIDoesNotAllocate(t *testing.T) {
	term := newTestTerminal(t, 80, 24)
	line := []byte(strings.Repeat("abcdefgh", 10)[:79] + "\r")
	allocs := testing.AllocsPerRun(100, func() {
		term.Write(line)
	})
	if allocs > 0 {
		t.Errorf("allocations per %d-byte line: got = %v, want = 0", len(line), allocs)
	}
}

func BenchmarkWriteASCII(b *testing.B) {
	term := NewEmulator(80, 24)
	line := []byte(strings.Repeat("abcdefgh", 10)[:79] + "\r\n")
	b.SetBytes(int64(len(line)))
	b.ReportAllocs()
	for b.Loop() {
		term.Write(line)
	}
}
