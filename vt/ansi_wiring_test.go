package vt

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// vt builds against the in-repo ansi module (a replace in go.mod), so its tests
// see the parser fixes that module carries. These two tests exist to fail when
// that wiring is lost: each feeds the emulator bytes whose handling the
// published ansi v0.11.7 gets wrong and the in-repo parser gets right.

// The three bytes of U+2733 are E2 9C B3, and 0x9C is the C1 string terminator.
// The parser used to treat that continuation byte as ST and end the title
// after "\xe2", leaving " Claude Code" to print as text. A byte inside a
// UTF-8 sequence is a continuation, not a C1 control.
func TestTitleKeepsAUTF8RuneWhoseByteIsC1(t *testing.T) {
	var title string
	term := newTestTerminal(t, 20, 1)
	term.SetCallbacks(Callbacks{Title: func(s string) { title = s }})
	term.WriteString("\x1b]0;✳ Claude Code\x07ok")

	if want := "✳ Claude Code"; title != want {
		t.Errorf("title callback: got %q, want %q", title, want)
	}
	if want := "✳ Claude Code"; term.title != want {
		t.Errorf("title: got %q, want %q", term.title, want)
	}
	if got, want := rowText(term, 0), "ok"; got != want {
		t.Errorf("row 0: got %q, want %q", got, want)
	}
}

// A rune cut short by an escape sequence is replaced with U+FFFD, and the ESC
// still begins the sequence. The parser used to take the ESC as the rune's next
// byte, so "\x1b[1m" never ran and "1mb" printed instead.
func TestATruncatedRuneDoesNotSwallowTheEscape(t *testing.T) {
	term := newTestTerminal(t, 10, 1)
	term.WriteString("a\xe2\x1b[1mb")

	if got, want := rowText(term, 0), "a�b"; got != want {
		t.Fatalf("row 0: got %q, want %q", got, want)
	}
	for x, bold := range []bool{false, false, true} {
		cell := term.CellAt(x, 0)
		if cell == nil {
			t.Fatalf("cell %d: nil", x)
		}
		if got := cell.Style.Attrs&uv.AttrBold != 0; got != bold {
			t.Errorf("cell %d (%q): bold is %v, want %v", x, cell.Content, got, bold)
		}
	}
}
