package ansi

import (
	"testing"

	"github.com/charmbracelet/x/ansi/parser"
)

func TestEscSequence(t *testing.T) {
	cases := []testCase{
		{
			name:  "reset",
			input: "\x1b[3;1\x1b(A",
			expected: []any{
				Cmd('A' | '('<<parser.IntermedShift),
			},
		},
		{
			// A control inside an escape sequence is executed at once, and
			// the sequence goes on, as in xterm, whose escape tables run a
			// C0 and keep their state.
			name:  "control_inside",
			input: "\x1b\nc",
			expected: []any{
				byte('\n'),
				Cmd('c'),
			},
		},
		{
			name:  "control_after_an_intermediate",
			input: "\x1b(\r0",
			expected: []any{
				byte('\r'),
				Cmd('0' | '('<<parser.IntermedShift),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dispatcher := &testDispatcher{}
			parser := testParser(dispatcher)
			parser.Parse([]byte(c.input))
			assertEqual(t, len(c.expected), len(dispatcher.dispatched))
			for i := range c.expected {
				assertEqual(t, c.expected[i], dispatcher.dispatched[i])
			}
		})
	}
}
