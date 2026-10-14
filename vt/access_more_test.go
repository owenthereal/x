package vt

import (
	"slices"
	"testing"
)

// 80 columns: every 8th. After ESC[3g, then HTS at 5 and 20: [5 20].
func TestTabStopsReportsTheStops(t *testing.T) {
	e := newTestTerminal(t, 80, 24)
	want := []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 72}
	if got := e.TabStops(); !slices.Equal(got, want) {
		t.Errorf("default stops: got = %v, want = %v", got, want)
	}

	// HTS sets a stop at the cursor, and TBC 3 clears every stop. The stops
	// are neither the default set's nor in the order they were set.
	_, _ = e.WriteString("\x1b[3g\x1b[1;21H\x1bH\x1b[1;6H\x1bH")
	if got, want := e.TabStops(), []int{5, 20}; !slices.Equal(got, want) {
		t.Errorf("after TBC 3 and HTS at 5 and 20: got = %v, want = %v", got, want)
	}

	_, _ = e.WriteString("\x1b[1;80H\x1bH")
	if got, want := e.TabStops(), []int{5, 20, 79}; !slices.Equal(got, want) {
		t.Errorf("after HTS at the last column: got = %v, want = %v", got, want)
	}

	// TBC 0 clears the stop at the cursor.
	_, _ = e.WriteString("\x1b[1;21H\x1b[g")
	if got, want := e.TabStops(), []int{5, 79}; !slices.Equal(got, want) {
		t.Errorf("after TBC 0 at 20: got = %v, want = %v", got, want)
	}

	// Each slice is the caller's: changing one changes no other.
	first, second := e.TabStops(), e.TabStops()
	first[0] = 40
	if want := []int{5, 79}; !slices.Equal(second, want) {
		t.Errorf("after changing another returned slice: got = %v, want = %v", second, want)
	}

	_, _ = e.WriteString("\x1b[3g")
	if got := e.TabStops(); len(got) != 0 {
		t.Errorf("after TBC 3: got = %v, want none", got)
	}

	// A resize and RIS both go back to the default set, for the new width.
	_, _ = e.WriteString("\x1b[1;6H\x1bH")
	e.Resize(20, 24)
	if got, want := e.TabStops(), []int{0, 8, 16}; !slices.Equal(got, want) {
		t.Errorf("after a resize to 20 columns: got = %v, want = %v", got, want)
	}
	_, _ = e.WriteString("\x1b[3g\x1bc")
	if got, want := e.TabStops(), []int{0, 8, 16}; !slices.Equal(got, want) {
		t.Errorf("after RIS: got = %v, want = %v", got, want)
	}
}

// "A", CUP elsewhere, "é" as e + U+0301: still 'A' (multi-byte input never
// sets it). ESC ( 0 then "q": 'q', though the cell shows a line-drawing
// character. After ESC c: 0. A CSI or a control between changes nothing.
func TestLastCharReportsWhatREPRepeats(t *testing.T) {
	e := newTestTerminal(t, 10, 5)
	if got := e.LastChar(); got != 0 {
		t.Errorf("before anything is printed: got = %q, want 0", got)
	}

	steps := []struct {
		name  string
		input string
		want  rune
	}{
		{"an ASCII character", "A", 'A'},
		{"CUP", "\x1b[3;3H", 'A'},
		{"SGR", "\x1b[1;31m", 'A'},
		{"controls", "\r\n\t\b", 'A'},
		{"a cluster of two runes", "e\u0301", 'A'},
		{"one rune of two bytes", "\u00e9", 'A'},
		{"a wide character", "世", 'A'},
		{"an emoji", "\U0001F600", 'A'},
		{"a character the charset maps", "\x1b(0q", 'q'},
		{"a second one", "x", 'x'},
		{"SI and SO", "\x0e\x0f", 'x'},
		{"a DEC special graphics character in G1", "\x1b)0\x0eq\x0f", 'q'},
		{"back to ASCII", "\x1b(Bz", 'z'},
		{"RIS", "\x1bc", 0},
		{"REP after RIS", "\x1b[b", 0},
		{"after RIS", "w", 'w'},
		{"DECSTR", "\x1b[!p", 'w'},
	}
	for _, step := range steps {
		_, _ = e.WriteString(step.input)
		if got := e.LastChar(); got != step.want {
			t.Errorf("after %s (%q): got = %q, want = %q", step.name, step.input, got, step.want)
		}
	}

	// The cell holds the line-drawing character REP draws, while LastChar
	// reports the byte the program sent.
	e = newTestTerminal(t, 10, 5)
	_, _ = e.WriteString("\x1b(0q")
	if c := e.CellAt(0, 0); c == nil || c.Content != "─" {
		t.Errorf("cell after ESC ( 0 q: got = %+v, want a line-drawing character", c)
	}
	if got := e.LastChar(); got != 'q' {
		t.Errorf("LastChar after ESC ( 0 q: got = %q, want 'q'", got)
	}
	_, _ = e.WriteString("\x1b[2b")
	if got, want := rowText(e, 0), "───"; got != want {
		t.Errorf("row after REP: got = %q, want = %q", got, want)
	}

	// REP repeats what LastChar reports.
	e = newTestTerminal(t, 10, 5)
	_, _ = e.WriteString("A\x1b[3;3He\u0301\x1b[1;2H\x1b[2b")
	if got, want := rowText(e, 0), "AAA"; got != want {
		t.Errorf("row after REP: got = %q, want = %q", got, want)
	}
}

// A cluster split across Writes is one cluster, so the character REP repeats
// is the one it would be had the bytes arrived in one Write: not the cluster's
// base, which its first part had printed on its own.
func TestLastCharSurvivesASplitCluster(t *testing.T) {
	for _, tc := range []struct {
		name   string
		width  int
		chunks []string
		want   rune
		row    string // the first row after the last chunk, REP included
	}{
		{"mark in the second write", 10, []string{"ae", "\u0301", "\x1b[b"}, 'a', "ae\u0301a"},
		{"mark and REP in one", 10, []string{"ae", "\u0301\x1b[b"}, 'a', "ae\u0301a"},
		{"nothing printed before the cluster", 10, []string{"e", "\u0301", "\x1b[b"}, 0, "e\u0301"},
		{"two marks, one write each", 10, []string{"ae", "\u0301", "\u0302", "\x1b[b"}, 'a', "ae\u0301\u0302a"},
		{"cluster after a longer run", 10, []string{"abcd", "e", "\u0301", "\x1b[2b"}, 'd', "abcde\u0301dd"},
		{"cluster at the last column", 5, []string{"abcde", "\u0301", "\x1b[b"}, 'd', "abcde\u0301"},
		{"insert mode", 10, []string{"xyz\x1b[1;1H\x1b[4hae", "\u0301", "\x1b[b"}, 'a', "ae\u0301axyz"},
		{"an ASCII character instead of a mark", 10, []string{"ae", "b", "\x1b[b"}, 'b', "aebb"},
		{"a rune that is not a mark", 10, []string{"ae", "\u00e9", "\x1b[b"}, 'e', "ae\u00e9e"},
		{"a wide character instead of a mark", 10, []string{"ae", "世", "\x1b[b"}, 'e', "ae世e"},
		{"a mark after a sequence is not joined", 10, []string{"ae", "\x1b[m", "\u0301", "\x1b[b"}, 'e', "aee"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			split := newTestTerminal(t, tc.width, 2)
			var all string
			for _, c := range tc.chunks {
				_, _ = split.WriteString(c)
				all += c
			}
			whole := newTestTerminal(t, tc.width, 2)
			_, _ = whole.WriteString(all)

			for name, e := range map[string]*Emulator{"split": split, "one write": whole} {
				if got := e.LastChar(); got != tc.want {
					t.Errorf("%s: LastChar: got = %q, want = %q", name, got, tc.want)
				}
				if got := rowText(e, 0); got != tc.row {
					t.Errorf("%s: row: got = %q, want = %q", name, got, tc.row)
				}
			}
		})
	}
}
