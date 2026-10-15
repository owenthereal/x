package ansi

import (
	"strconv"
	"strings"
	"testing"

	"github.com/owenthereal/x/ansi/parser"
)

func TestCsiSequence(t *testing.T) {
	cases := []testCase{
		{
			name:  "no_params",
			input: "\x1b[m",
			expected: []any{
				csiSequence{
					Params: Params{},
					Cmd:    'm',
				},
			},
		},
		{
			name:  "one_param",
			input: "\x1b[7m",
			expected: []any{
				csiSequence{
					Params: Params{7},
					Cmd:    'm',
				},
			},
		},
		{
			name:  "param_reset",
			input: "\x1b[0mabc\x1b[1;2m",
			expected: []any{
				csiSequence{
					Params: Params{0},
					Cmd:    'm',
				},
				rune('a'),
				rune('b'),
				rune('c'),
				csiSequence{
					Params: Params{1, 2},
					Cmd:    'm',
				},
			},
		},
		{
			name:  "max_params",
			input: "\x1b[" + strings.Repeat("1;", 31) + "p",
			expected: []any{
				csiSequence{
					Params: Params{
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
					},
					Cmd: 'p',
				},
			},
		},
		{
			name:  "ignore_long",
			input: "\x1b[" + strings.Repeat("1;", 18) + "p",
			expected: []any{
				csiSequence{
					Params: Params{
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
						1,
					},
					Cmd: 'p',
				},
			},
		},
		{
			name:  "trailing_semicolon",
			input: "\x1b[4;m",
			expected: []any{
				csiSequence{
					Params: Params{4, parser.MissingParam},
					Cmd:    'm',
				},
			},
		},
		{
			name:  "leading_semicolon",
			input: "\x1b[;4m",
			expected: []any{
				csiSequence{
					Params: Params{parser.MissingParam, 4},
					Cmd:    'm',
				},
			},
		},
		{
			name:  "long_param",
			input: "\x1b[" + strconv.Itoa(parser.MaxParam) + "m",
			expected: []any{
				csiSequence{
					Params: Params{parser.MaxParam},
					Cmd:    'm',
				},
			},
		},
		{
			// A parameter past the largest stays at it, as xterm's
			// CASE_ESC_DIGIT keeps it at MAX_I_PARAM, digit by digit, so a
			// count too long for an int doesn't wrap around to a small one.
			name:  "params_past_the_largest",
			input: "\x1b[100000;4294967297;7777777777777777777777777770;65536m",
			expected: []any{
				csiSequence{
					Params: Params{parser.MaxParam, parser.MaxParam, parser.MaxParam, parser.MaxParam},
					Cmd:    'm',
				},
			},
		},
		{
			// A control inside a CSI is executed at once, and the sequence
			// goes on with its parameters and command, as in xterm, whose
			// CSI tables run a C0 and keep their state: a CUP cut by a
			// newline is the newline, then the CUP.
			name:  "control_inside",
			input: "\x1b[2;\n5H\x1b[?\r25h\x1b[1 \x07q",
			expected: []any{
				byte('\n'),
				csiSequence{
					Params: Params{2, 5},
					Cmd:    'H',
				},
				byte('\r'),
				csiSequence{
					Params: Params{25},
					Cmd:    'h' | '?'<<parser.PrefixShift,
				},
				byte('\a'),
				csiSequence{
					Params: Params{1},
					Cmd:    'q' | ' '<<parser.IntermedShift,
				},
			},
		},
		{
			name:  "reset",
			input: "\x1b[3;1\x1b[?1049h",
			expected: []any{
				csiSequence{
					Params: Params{1049},
					Cmd:    'h' | '?'<<parser.PrefixShift,
				},
			},
		},
		{
			name:  "subparams",
			input: "\x1b[38:2:255:0:255;1m",
			expected: []any{
				csiSequence{
					Params: Params{
						38 | parser.HasMoreFlag, 2 | parser.HasMoreFlag, 255 |
							parser.HasMoreFlag, 0 | parser.HasMoreFlag, 255, 1,
					},
					Cmd: 'm',
				},
			},
		},
		{
			name:  "params_buffer_filled_with_subparams",
			input: "\x1b[::::::::::::::::::::::::::::::::x\x1b",
			expected: []any{
				csiSequence{
					Params: Params{
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
						parser.MissingParam | parser.HasMoreFlag,
					},
					Cmd: 'x',
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dispatcher := &testDispatcher{}
			parser := testParser(dispatcher)
			parser.Parse([]byte(c.input))
			assertEqual(t, len(c.expected), len(dispatcher.dispatched))
			assertEqual(t, c.expected, dispatcher.dispatched)
		})
	}
}
