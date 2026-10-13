package vt

import (
	"fmt"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// hugeCount is a count past anything a screen can reach. It is not 2^31-1,
// which is x/ansi's missing-parameter sentinel (ParamMask): that reads as the
// default count.
const hugeCount = 1_000_000_000

// clampOps are the operations whose count is clamped to what they can reach,
// by name and final byte.
var clampOps = []struct{ name, final string }{
	{"ECH", "X"}, {"ICH", "@"}, {"DCH", "P"}, {"IL", "L"}, {"DL", "M"},
	{"SU", "S"}, {"SD", "T"},
	{"CUU", "A"}, {"CUD", "B"}, {"CUF", "C"}, {"CUB", "D"}, {"CNL", "E"}, {"CPL", "F"},
	{"CHT", "I"}, {"CBT", "Z"},
}

// clampScenarios are the states an 80x24 screen is in when the operations
// run, each with the reach of every operation there: the smallest count that
// does all that a huge one does. A reach of 0 is an operation that does
// nothing, or nothing more than a count of 1 does.
var clampScenarios = []struct {
	name  string
	prep  string
	reach map[string]int
}{
	{
		// Row 10, column 30, with no margins.
		name: "interior",
		prep: "\x1b[11;31H",
		reach: map[string]int{
			"ECH": 50, "ICH": 50, "DCH": 50, // columns 30 to 79
			"IL": 14, "DL": 14, // rows 10 to 23
			"SU": 24, "SD": 24,
			"CUU": 10, "CUD": 13, "CUF": 49, "CUB": 30, "CNL": 13, "CPL": 10,
			"CHT": 7, // the stops at 32, 40, 48, 56, 64 and 72, then the last column
			"CBT": 4, // the stops at 24, 16, 8 and 0
		},
	},
	{
		// Rows 4 to 19 and columns 10 to 59 are the region, and the cursor
		// is at row 9, column 29, inside it.
		name: "margins",
		prep: "\x1b[5;20r\x1b[?69h\x1b[11;60s\x1b[10;30H",
		reach: map[string]int{
			// ECH ignores the margins: columns 29 to 79.
			"ECH": 51,
			"ICH": 31, "DCH": 31, // columns 29 to 59
			"IL": 11, "DL": 11, // rows 9 to 19
			"SU": 16, "SD": 16,
			"CUU": 5, "CUD": 10, "CUF": 30, "CUB": 19, "CNL": 10, "CPL": 5,
			"CHT": 5, // 32, 40, 48, 56, and 64, which is past the right margin
			"CBT": 4, // 24, 16, 8 and 0: the left margin only holds in origin mode
		},
	},
	{
		// The same cursor, in origin mode, where CBT stops at the left margin.
		name: "origin mode",
		prep: "\x1b[5;20r\x1b[?69h\x1b[11;60s\x1b[?6h\x1b[6;20H",
		reach: map[string]int{
			"ECH": 51,
			"ICH": 31, "DCH": 31,
			"IL": 11, "DL": 11,
			"SU": 16, "SD": 16,
			"CUU": 5, "CUD": 10, "CUF": 30, "CUB": 19, "CPL": 5,
			// CNL moves to row 9 + n, and the carriage return then reads that
			// row as relative to the top margin, so from n = 6 it ends in the
			// region's bottom row.
			"CNL": 6,
			"CHT": 5,
			"CBT": 3, // 24, 16, and 8, which is past the left margin
		},
	},
	{
		// Row 1, column 5, above and to the left of the region: ICH, DCH, IL
		// and DL do nothing there, and the cursor moves are bounded by the
		// screen.
		name: "outside the margins",
		prep: "\x1b[5;20r\x1b[?69h\x1b[11;60s\x1b[2;6H",
		reach: map[string]int{
			"ECH": 75,
			"ICH": 0, "DCH": 0, "IL": 0, "DL": 0,
			"SU": 16, "SD": 16,
			"CUU": 1, "CUD": 22, "CUF": 74, "CUB": 5, "CNL": 22, "CPL": 1,
			"CHT": 8, // 8, 16, 24, 32, 40, 48, 56, and 64, which is past the right margin
			"CBT": 1,
		},
	},
	{
		// Row 10, with a wrap pending after column 79.
		name: "pending wrap",
		prep: "\x1b[11;80Hx",
		reach: map[string]int{
			"ECH": 1, "ICH": 1, "DCH": 1,
			"IL": 14, "DL": 14,
			"SU": 24, "SD": 24,
			"CUU": 10, "CUD": 13, "CUF": 0, "CUB": 79, "CNL": 13, "CPL": 10,
			"CHT": 0,
			"CBT": 10, // the stops at 72, 64, 56, 48, 40, 32, 24, 16, 8 and 0
		},
	},
}

// clampState is what an operation leaves behind that a clamp must not change.
type clampState struct {
	screen, history []uv.Line
	cursor          uv.Position
	pendingWrap     bool
}

// runClamped feeds count of the CSI operation final to an 80x24 screen that
// holds a different rune in every cell, after prep, with a blue pen so that
// blank cells show which operation made them. It returns what the operation
// left, and how long the Write took.
func runClamped(t *testing.T, prep, final string, count int) (clampState, time.Duration) {
	t.Helper()
	const w, h = 80, 24
	e := newTestTerminal(t, w, h)
	for y := range h {
		for x := range w {
			e.scr.SetCell(x, y, &uv.Cell{Content: string(rune(0x100 + y*w + x)), Width: 1})
		}
	}
	_, _ = e.WriteString("\x1b[44m" + prep)
	e.scr.ClearTouched()

	start := time.Now()
	_, _ = e.WriteString(fmt.Sprintf("\x1b[%d%s", count, final))
	elapsed := time.Since(start)

	screen, history := snapshotRows(e)
	return clampState{
		screen:      screen,
		history:     history,
		cursor:      e.CursorPosition(),
		pendingWrap: e.PendingWrap(),
	}, elapsed
}

// diff describes how s differs from other, or returns "" if it doesn't.
func (s clampState) diff(other clampState) string {
	switch {
	case s.cursor != other.cursor:
		return fmt.Sprintf("cursor: got = %v, want = %v", s.cursor, other.cursor)
	case s.pendingWrap != other.pendingWrap:
		return fmt.Sprintf("pending wrap: got = %v, want = %v", s.pendingWrap, other.pendingWrap)
	case !sameLines(s.screen, other.screen):
		for y := range s.screen {
			if !sameLines(s.screen[y:y+1], other.screen[y:y+1]) {
				return fmt.Sprintf("row %d: got = %q, want = %q", y, s.screen[y].String(), other.screen[y].String())
			}
		}
		return "screen differs"
	case !sameLines(s.history, other.history):
		return fmt.Sprintf("history: got = %d lines, want = %d lines, or the same count with other cells", len(s.history), len(other.history))
	}
	return ""
}

func TestClampsAreExact(t *testing.T) {
	for _, sc := range clampScenarios {
		for _, op := range clampOps {
			reach, ok := sc.reach[op.name]
			if !ok {
				t.Fatalf("%s: no reach for %s", sc.name, op.name)
			}
			t.Run(sc.name+"/"+op.name, func(t *testing.T) {
				huge, elapsed := runClamped(t, sc.prep, op.final, hugeCount)
				if elapsed >= 10*time.Millisecond {
					t.Errorf("count %d took %v, want under 10ms", hugeCount, elapsed)
				}

				// One past the reach is the smallest count that is clamped
				// where the operation can't be told from it.
				past, _ := runClamped(t, sc.prep, op.final, reach+1)
				if d := past.diff(huge); d != "" {
					t.Errorf("count %d against count %d: %s", reach+1, hugeCount, d)
				}

				// One short of the reach does not do all a huge count does,
				// so a clamp below the reach shows: it makes the two the same.
				// A reach below 2 has no such count to try: 0 is not a count
				// short of 1, but a request that the operations treat apart.
				if reach < 2 {
					return
				}
				short, _ := runClamped(t, sc.prep, op.final, reach-1)
				if short.diff(huge) == "" {
					t.Errorf("count %d does all that count %d does: the reach of %d is too high, or the clamp is too low", reach-1, hugeCount, reach)
				}
			})
		}
	}
}

func TestREPKeepsItsMeaning(t *testing.T) {
	e := newTestTerminal(t, 80, 24)
	_, _ = e.WriteString("x\x1b[160b")

	for y, want := range []string{strings.Repeat("x", 80), strings.Repeat("x", 80), "x", ""} {
		if got := rowText(e, y); got != want {
			t.Errorf("row %d: got = %q, want = %q", y, got, want)
		}
	}
	if got, want := e.CursorPosition(), uv.Pos(1, 2); got != want {
		t.Errorf("cursor: got = %v, want = %v", got, want)
	}
	if e.PendingWrap() {
		t.Error("wrap pending after REP, which ended in column 1")
	}
}
