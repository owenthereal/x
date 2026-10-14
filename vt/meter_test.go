package vt

import (
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// lowerMeterInterval sets meterInterval to n until the test ends.
func lowerMeterInterval(t testing.TB, n int) {
	t.Helper()
	old := meterInterval
	meterInterval = n
	t.Cleanup(func() { meterInterval = old })
}

// charges returns the cell operations e is charged while do runs, and how
// many times it calls the stop function, which charges sets to one that never
// stops. Each call reports the charges since the one before; the charges
// since the last call make up the rest.
func charges(e *Emulator, do func()) (charged, calls int) {
	charged = -e.meter.work
	e.SetStopFunc(func() bool {
		charged += e.meter.last
		calls++
		return false
	})
	do()
	return charged + e.meter.work, calls
}

// writePattern writes a letter to every cell of e's screen, 'a'+(x+y)%26 at
// column x of row y, so that a cell blanked or moved by a row or a column
// differs from what it held.
func writePattern(e *Emulator) {
	var b strings.Builder
	for y := range e.Height() {
		fmt.Fprintf(&b, "\x1b[%d;1H", y+1)
		for x := range e.Width() {
			b.WriteByte(byte('a' + (x+y)%26))
		}
	}
	e.WriteString(b.String())
}

// cellWrites holds the rows of some screens, to count the cells written to
// them since. It keeps each row by its storage, not its place, so that a row
// moved whole, as a full-width scroll moves it, is not counted as written.
type cellWrites map[*uv.Cell]uv.Line

// takeCellWrites copies the rows of scrs.
func takeCellWrites(scrs ...*Screen) cellWrites {
	w := cellWrites{}
	for _, s := range scrs {
		for y := range s.Height() {
			if line := s.Line(y); len(line) > 0 {
				w[&line[0]] = slices.Clone(line)
			}
		}
	}
	return w
}

// since counts the cells of scrs that differ from what w copied, and every
// cell of a row w didn't copy.
func (w cellWrites) since(scrs ...*Screen) int {
	n := 0
	for _, s := range scrs {
		for y := range s.Height() {
			line := s.Line(y)
			if len(line) == 0 {
				continue
			}
			old, ok := w[&line[0]]
			if !ok || len(old) != len(line) {
				n += len(line)
				continue
			}
			for x := range line {
				if line[x] != old[x] {
					n++
				}
			}
		}
	}
	return n
}

// cellsHolding counts the cells of e's screen whose content is s.
func cellsHolding(e *Emulator, s string) int {
	n := 0
	for y := range e.Height() {
		for _, c := range e.scr.Line(y) {
			if c.Content == s {
				n++
			}
		}
	}
	return n
}

// 512x256 filled with "x", then one Write of a single ED 2. The stop function
// returns false until it sees erase progress (it reads the screen: at least
// one cell blank), then true. Write returns ErrStopped, and the screen holds
// both blank cells and "x" cells: the stop was observed inside the erase's
// cell work. Unmetered, the erase finishes before any call can see progress,
// Write returns nil, and the test fails.
func TestStopFuncStopsInsideAnErase(t *testing.T) {
	const w, h = 512, 256
	e := NewEmulator(w, h)
	e.WriteString(strings.Repeat("x", w*h))
	e.SetStopFunc(func() bool { return cellsHolding(e, " ") > 0 })

	_, err := e.WriteString("\x1b[2J")
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("Write: got error %v, want ErrStopped: no call to the stop function came inside the erase", err)
	}
	blank, x := cellsHolding(e, " "), cellsHolding(e, "x")
	if blank == 0 || x == 0 {
		t.Errorf("after the stop: %d blank cells and %d \"x\" cells, want some of each", blank, x)
	}
}

// With meterInterval lowered to 1,024 (each of these is smaller than the
// default interval, so the API alone doesn't require a stop inside it): the
// same arming, for EL across a 4,096-column row, ECH 1,000,000,000 (a full
// row after P3's clamp), DECALN on 64x64, and DL in a region one column
// narrower than a 64x64 screen: each stops with its operation part done, and
// the delegated spans are at most 1,024 cells.
//
// vt has no DECALN (it ignores ESC # 8), so RIS stands in for it: the other
// sequence that fills a whole screen. The other cases cover the rest of the
// cell loops: the line and cell shifts, the full-width scrolls, the IRM print
// and the carried cluster's undo.
func TestStopFuncStopsInsideEveryCellLoop(t *testing.T) {
	lowerMeterInterval(t, 1024)
	// One column short of the 64-column screen: the region's line shifts
	// copy cells instead of rotating rows.
	const narrow = "\x1b[?69h\x1b[1;63s\x1b[1;1H"
	for _, tc := range []struct {
		name     string
		w, h     int
		prep, op string
	}{
		{"EL across a 4,096-column row", 4096, 1, "\x1b[1;1H", "\x1b[K"},
		{"ECH 1,000,000,000", 4096, 1, "\x1b[1;1H", "\x1b[1000000000X"},
		{"RIS on 64x64, for DECALN", 64, 64, "", "\x1bc"},
		{"DL in a region one column narrower than the screen", 64, 64, narrow, "\x1b[M"},
		{"IL in a region one column narrower than the screen", 64, 64, narrow, "\x1b[L"},
		{"SU of every row of 64x64", 64, 64, "", "\x1b[64S"},
		{"SD of every row of 64x64", 64, 64, "", "\x1b[64T"},
		{"ICH across a 4,096-column row", 4096, 1, "\x1b[1;1H", "\x1b[@"},
		{"DCH across a 4,096-column row", 4096, 1, "\x1b[1;1H", "\x1b[P"},
		{"an IRM print across a 4,096-column row", 4096, 1, "\x1b[4h\x1b[1;1H", "X"},
		// The mark joins the "e" the previous Write ended with, so the insert
		// that made room for the "e" is undone first.
		{"the carried cluster's undo across a 4,096-column row", 4096, 1, "\x1b[4h\x1b[1;1He", "\u0301"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// run writes op after the pattern and prep, and returns the cells
			// the stop function first saw written, if it was armed, and the
			// screen it ends with.
			run := func(armed bool) (seen int, screen []string, err error) {
				e := NewEmulator(tc.w, tc.h)
				writePattern(e)
				e.WriteString(tc.prep)
				writes := takeCellWrites(e.MainScreen(), e.AltScreen())
				if armed {
					e.SetStopFunc(func() bool {
						seen = writes.since(e.MainScreen(), e.AltScreen())
						return seen > 0
					})
				}
				_, err = e.WriteString(tc.op)
				return seen, screenRows(e, tc.w), err
			}

			_, done, err := run(false)
			if err != nil {
				t.Fatalf("unarmed: got error %v", err)
			}
			seen, stopped, err := run(true)
			if !errors.Is(err, ErrStopped) {
				t.Fatalf("Write: got error %v, want ErrStopped: no call to the stop function came inside the operation", err)
			}
			if seen > meterInterval {
				t.Errorf("the stop function first saw %d cells written, more than the interval of %d: a span went unmetered", seen, meterInterval)
			}
			if slices.Equal(stopped, done) {
				t.Errorf("the screen after the stop is the screen the whole operation leaves: the stop didn't cut it short")
			}
		})
	}
}

// meteredCalls reads the list in meter.go's header comment: each ultraviolet
// function vt calls, with the vt functions the list allows to call it.
func meteredCalls(t *testing.T) map[string][]string {
	t.Helper()
	src, err := os.ReadFile("meter.go")
	if err != nil {
		t.Fatal(err)
	}
	list := map[string][]string{}
	for line := range strings.SplitSeq(string(src), "\n") {
		entry, ok := strings.CutPrefix(line, "//\t")
		if !ok {
			continue
		}
		name, rest, _ := strings.Cut(entry, " ")
		callers, _, ok := strings.Cut(strings.TrimSpace(rest), ":")
		if !ok {
			t.Fatalf("meter.go's list: %q has no \"callers: bound\"", entry)
		}
		list[name] = append(list[name], strings.Split(callers, ", ")...)
	}
	if len(list) == 0 {
		t.Fatal("meter.go has no list of ultraviolet calls")
	}
	return list
}

// ultravioletFunc returns the name meter.go's list gives obj, when obj is an
// ultraviolet function or method: (*uv.Buffer).FillArea, uv.NewLine,
// screen.FillArea.
func ultravioletFunc(obj types.Object) string {
	const uvPath = "github.com/charmbracelet/ultraviolet"
	f, ok := obj.(*types.Func)
	if !ok || f.Pkg() == nil {
		return ""
	}
	path := f.Pkg().Path()
	switch {
	case path == uvPath:
		return strings.ReplaceAll(f.FullName(), uvPath+".", "uv.")
	case strings.HasPrefix(path, uvPath+"/"):
		return strings.ReplaceAll(f.FullName(), path+".", f.Pkg().Name()+".")
	}
	return ""
}

// declName names a top-level declaration as meter.go's list does:
// Screen.fillArea, NewScreen.
func declName(decl ast.Decl) string {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok {
		return "package scope"
	}
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if id, ok := recv.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// A source scan: every call from vt into an ultraviolet function on meter.go's
// list goes through the metered wrapper (grep the package's non-test files
// for the direct calls).
//
// The scan type-checks the package, so it finds a call however it is
// reached. A call to a function the list doesn't have fails it too, and so
// does an entry for a call vt no longer makes: the list stays the whole of
// what vt calls.
func TestNoUnmeteredUltravioletCellCall(t *testing.T) {
	list := meteredCalls(t)

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	if _, err := conf.Check("github.com/charmbracelet/x/vt", fset, files, info); err != nil {
		t.Fatal(err)
	}

	made := map[string]map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			caller := declName(decl)
			ast.Inspect(decl, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				callee := ultravioletFunc(info.Uses[id])
				if callee == "" {
					return true
				}
				callers, listed := list[callee]
				switch {
				case !listed:
					t.Errorf("%s: %s calls %s, which meter.go's list doesn't have: add it, with what bounds it", fset.Position(id.Pos()), caller, callee)
				case !slices.Contains(callers, "anywhere") && !slices.Contains(callers, caller):
					t.Errorf("%s: %s calls %s directly; meter.go's list allows only %s", fset.Position(id.Pos()), caller, callee, strings.Join(callers, ", "))
				}
				if made[callee] == nil {
					made[callee] = map[string]bool{}
				}
				made[callee][caller] = true
				return true
			})
		}
	}

	for callee, callers := range list {
		for _, caller := range callers {
			switch {
			case caller == "none":
			case caller == "anywhere" && len(made[callee]) == 0:
				t.Errorf("meter.go's list has %s, which nothing calls", callee)
			case caller != "anywhere" && !made[callee][caller]:
				t.Errorf("meter.go's list has %s in %s, which doesn't call it", callee, caller)
			}
		}
	}
}

// ESC[65535b, the largest count: stopped within one interval of the stop.
func TestStopFuncStopsREP(t *testing.T) {
	lowerMeterInterval(t, 1024)
	const w = 80
	e := NewEmulator(w, 24)
	scrolled := 0
	e.SetScrollOffFunc(func(uv.Line) { scrolled++ })
	e.WriteString("x")
	calls := 0
	e.SetStopFunc(func() bool {
		calls++
		return true
	})

	_, err := e.WriteString("\x1b[65535b")
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("Write: got error %v, want ErrStopped", err)
	}
	if calls != 1 {
		t.Errorf("stop function calls: got = %d, want = 1", calls)
	}
	// Each repetition prints an "x", and the full rows scroll off: what
	// was printed is what scrolled off and what is still on the screen.
	if printed := scrolled*w + cellsHolding(e, "x"); printed > meterInterval+1 {
		t.Errorf("%d characters printed, more than the interval of %d past the first", printed, meterInterval)
	}
	// The repetitions left must not go on after the stop, printing nothing.
	// Each would ask the meter for its charge again, and be turned down.
	if e.meter.refused > 1 {
		t.Errorf("%d charges turned down after the stop: the repetitions went on", e.meter.refused)
	}
}

// After ErrStopped, Write returns ErrStopped and consumes nothing.
func TestAStoppedEmulatorIsSpent(t *testing.T) {
	e := NewEmulator(80, 24)
	e.SetStopFunc(func() bool { return true })
	in := strings.Repeat("x", 2*meterInterval)
	n, err := e.WriteString(in)
	if !errors.Is(err, ErrStopped) || n <= 0 || n >= len(in) {
		t.Fatalf("Write: got (%d, %v), want part of the %d bytes and ErrStopped", n, err, len(in))
	}
	before := e.Render()

	for _, p := range []string{"abc", "\x1b[2J", "\x1bc", ""} {
		if n, err := e.WriteString(p); n != 0 || !errors.Is(err, ErrStopped) {
			t.Errorf("Write(%q): got (%d, %v), want (0, ErrStopped)", p, n, err)
		}
	}
	if n, err := e.Write(nil); n != 0 || !errors.Is(err, ErrStopped) {
		t.Errorf("Write(nil): got (%d, %v), want (0, ErrStopped)", n, err)
	}
	if after := e.Render(); after != before {
		t.Errorf("a Write after the stop changed the screen:\n got = %q\nwant = %q", after, before)
	}
}

// Bytes that touch no cell still count: a run of CUF, twice the interval long,
// reaches the stop function, which stops it part way.
func TestStopFuncStopsBetweenParsedBytes(t *testing.T) {
	e := NewEmulator(80, 24)
	e.SetStopFunc(func() bool { return true })
	in := strings.Repeat("\x1b[C", 2*meterInterval/3)
	n, err := e.WriteString(in)
	if !errors.Is(err, ErrStopped) || n >= len(in) {
		t.Errorf("Write: got (%d, %v), want part of the %d bytes and ErrStopped", n, err, len(in))
	}
}

// A scroll at the bottom of a full-width region moves every row of it, and
// marks each touched, so it is charged at least twice the region's height:
// the stop function's first call comes before a tall screen has scrolled an
// interval's worth of rows. A line handed to history is charged its width,
// and the row blanked as much again, so on a wide screen the first call comes
// before an interval's worth of cells has gone either way.
func TestStopFuncCountsTheRowsAScrollMoves(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
		// The least a line feed at the bottom is charged.
		least int
	}{
		{"tall and narrow: the rows moved and touched", 1, 4096, 2 * 4096},
		{"wide and short: the line to history and the row blanked", 4096, 2, 2 * 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(tc.w, tc.h)
			scrolled := 0
			e.SetScrollOffFunc(func(uv.Line) { scrolled++ })
			e.WriteString(fmt.Sprintf("\x1b[%d;1H", tc.h))
			e.SetStopFunc(func() bool { return true })
			_, err := e.WriteString(strings.Repeat("\n", 2*meterInterval))
			if !errors.Is(err, ErrStopped) {
				t.Fatalf("Write: got error %v, want ErrStopped", err)
			}
			// The last line scrolled off may be the one whose scroll the
			// call came in; the ones before it were charged in full.
			if done := scrolled - 1; done*tc.least > meterInterval {
				t.Errorf("%d lines scrolled off before the first call, each charged at least %d: more than the interval of %d", done, tc.least, meterInterval)
			}
		})
	}
}

// ultraviolet's SetCell, writing over a wide character's placeholder, scans
// back toward column 0 for the character it belongs to, and finds none once
// that character is gone. So a fill over such orphans is quadratic in the
// width, as a DCH at column 0 of a row of wide characters was while it moved
// cells through SetCell, leaving an orphan at every other column, and as
// prints and an ICH over orphans were. Each SetCell vt still makes is charged
// its scan, and the shifts and the print no longer scan, so the stop function
// still comes throughout.
//
// The count shows it, where a clock would depend on the machine's load: what
// the operation is charged is at least the cells those scans read, and at
// most a few rows' width more. Charged work reaches the stop function at
// least once an interval, so it comes at least once per interval of scanning.
// A scan left uncharged makes the charge fall short of the scans; work that
// grows with the scans charged where nothing scans, such as a shift moving
// cells through SetCell again, takes it past the rows' width.
func TestStopFuncComesThroughAPlaceholderScan(t *testing.T) {
	const w = 32768
	wide := strings.Repeat("日", w/2)
	// Every other cell a zero-width placeholder with no wide character
	// before it.
	orphans := func(e *Emulator) {
		for x := 1; x < w; x += 2 {
			e.SetCell(x, 0, &uv.Cell{})
		}
	}
	// Writing over each orphan, from the left, SetCell reads every cell
	// before it, all of them blank by then.
	orphanScans := 0
	for x := 1; x < w; x += 2 {
		orphanScans += x
	}
	for _, tc := range []struct {
		name string
		prep func(*Emulator)
		op   string
		// The cells ultraviolet's SetCell reads in its scans from
		// placeholders.
		scans int
	}{
		// The shift moves the cells as they are, so nothing scans.
		{"DCH at column 0 of a row of wide characters", func(e *Emulator) { e.WriteString(wide) }, "\x1b[1;1H\x1b[P", 0},
		{"EL over orphaned placeholders", orphans, "\x1b[1;1H\x1b[2K", orphanScans},
		// A count of the whole row moves nothing and blanks every cell, as
		// they are.
		{"ICH over orphaned placeholders", orphans, "\x1b[1;1H\x1b[32768@", 0},
		// Each print blanks the orphan it lands on before SetCell writes
		// there.
		{"prints over orphaned placeholders", orphans, "\x1b[1;1Hx\x1b[32766b", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(w, 1)
			tc.prep(e)
			charged, calls := charges(e, func() {
				if _, err := e.WriteString(tc.op); err != nil {
					t.Fatalf("Write: got error %v", err)
				}
			})
			t.Logf("%d cell operations charged, %d calls to the stop function", charged, calls)
			if charged < tc.scans {
				t.Errorf("%d cell operations charged, fewer than the %d cells SetCell's scans read: the scans went uncharged", charged, tc.scans)
			}
			if most := tc.scans + 4*w; charged > most {
				t.Errorf("%d cell operations charged, more than the %d cells SetCell's scans read and four rows of %d", charged, tc.scans, w)
			}
			if least := tc.scans / meterInterval; calls < least {
				t.Errorf("stop function calls: got = %d, want at least %d, one per interval of the scans", calls, least)
			}
		})
	}
}

// A resize counts each row it copies: growing 64x64 to 128x128 builds 128 rows
// of 128 cells, so with an interval of 1,024 the stop function is called at
// least 16 times inside it, with no more than the interval between two calls.
func TestAResizeIsMetered(t *testing.T) {
	lowerMeterInterval(t, 1024)
	e := NewEmulator(64, 64)
	writePattern(e)
	calls := 0
	e.SetStopFunc(func() bool {
		calls++
		if e.meter.last > meterInterval {
			t.Errorf("%d cell operations between two calls, more than the interval of %d", e.meter.last, meterInterval)
		}
		return false
	})
	e.Resize(128, 128)
	if want := 128 * 128 / meterInterval; calls < want {
		t.Errorf("stop function calls inside the resize: got = %d, want at least %d", calls, want)
	}
	if e.Width() != 128 || e.Height() != 128 {
		t.Errorf("size: got = %dx%d, want = 128x128", e.Width(), e.Height())
	}
}

// A stop function that fires inside a Resize spends the emulator: Resize has
// no error to return, so the next Write(nil) returns ErrStopped.
func TestAStopInsideAResizeSpendsTheEmulator(t *testing.T) {
	lowerMeterInterval(t, 1024)
	e := NewEmulator(64, 64)
	calls := 0
	e.SetStopFunc(func() bool {
		calls++
		return true
	})
	e.Resize(128, 128)
	if calls != 1 {
		t.Errorf("stop function calls inside the resize: got = %d, want = 1", calls)
	}
	if n, err := e.Write(nil); n != 0 || !errors.Is(err, ErrStopped) {
		t.Errorf("Write(nil): got (%d, %v), want (0, ErrStopped)", n, err)
	}
	// What the screen holds is undefined, but reading it must not fail.
	_ = e.Render()
}

// shiftRowsDown, shiftRowsUp, fillArea and clearCells do what ultraviolet's
// InsertLineArea, DeleteLineArea, FillArea and Clear do, cell for cell and
// touch for touch, whatever spans the meter cuts them into: rows of wide and
// narrow characters, with and without a blank cell. (The cell shifts don't
// follow ultraviolet's: TestRowShiftsMatchTheirModel.)
func TestMeteredShiftsMatchUltraviolet(t *testing.T) {
	lowerMeterInterval(t, 3)
	const w, h = 12, 6
	rnd := rand.New(rand.NewPCG(1, 2))
	newScreens := func() (want, got *Screen) {
		want, got = NewScreen(w, h), NewScreen(w, h)
		for y := range h {
			for x := 0; x < w; {
				c := uv.Cell{Content: string(rune('a' + rnd.IntN(26))), Width: 1}
				c.Style.Fg = ansi.BasicColor(rnd.IntN(8))
				if rnd.IntN(3) == 0 {
					c.Content, c.Width = "日", 2
				}
				want.buf.SetCell(x, y, &c)
				got.buf.SetCell(x, y, &c)
				x += c.Width
			}
		}
		want.buf.Touched = make([]*uv.LineData, h)
		got.buf.Touched = make([]*uv.LineData, h)
		return want, got
	}
	blank := uv.EmptyCell
	blank.Style.Bg = ansi.BasicColor(4)
	wide := uv.Cell{Content: "本", Width: 2}

	for i := range 4000 {
		want, got := newScreens()
		var c *uv.Cell
		switch rnd.IntN(3) {
		case 0:
			c = &blank
		case 1:
			// Only a fill is ever given a wide cell.
			if i%4 == 2 {
				c = &wide
			}
		}
		x0, x1 := rnd.IntN(w), rnd.IntN(w+1)
		y0, y1 := rnd.IntN(h), rnd.IntN(h+1)
		area := uv.Rect(min(x0, x1), min(y0, y1), max(x0, x1)-min(x0, x1), max(y0, y1)-min(y0, y1))
		var op string
		switch i % 4 {
		case 0, 1:
			// The line shifts' callers clamp n and keep y inside the area,
			// as InsertLine and DeleteLine do.
			if area.Empty() {
				continue
			}
			y := area.Min.Y + rnd.IntN(area.Dy())
			n := 1 + rnd.IntN(area.Max.Y-y)
			*got.scroll = area
			if i%4 == 0 {
				op = fmt.Sprintf("InsertLineArea(%d, %d, %v)", y, n, area)
				want.buf.InsertLineArea(y, n, c, area)
				got.shiftRowsDown(y, n, c)
			} else {
				op = fmt.Sprintf("DeleteLineArea(%d, %d, %v)", y, n, area)
				want.buf.DeleteLineArea(y, n, c, area)
				got.shiftRowsUp(y, n, c)
			}
		case 2:
			// Screen.FillArea before the meter: ultraviolet's fill, then a
			// touch of every row of the area.
			area = area.Add(uv.Pos(rnd.IntN(5)-2, rnd.IntN(5)-2))
			op = fmt.Sprintf("FillArea(%v)", area)
			want.buf.FillArea(c, area)
			for y := area.Min.Y; y < area.Max.Y; y++ {
				want.buf.TouchLine(area.Min.X, y, area.Dx())
			}
			got.fillArea(c, area)
		case 3:
			op = "Clear()"
			want.buf.Clear()
			got.clearCells()
		}
		for y := range h {
			if !slices.Equal(got.buf.Line(y), want.buf.Line(y)) {
				t.Fatalf("%s, row %d:\n got = %q\nwant = %q", op, y, got.buf.Line(y), want.buf.Line(y))
			}
		}
		if !slices.EqualFunc(got.buf.Touched, want.buf.Touched, func(a, b *uv.LineData) bool {
			return (a == nil) == (b == nil) && (a == nil || *a == *b)
		}) {
			t.Fatalf("%s: touched rows differ:\n got = %v\nwant = %v", op, touchedText(got.buf.Touched), touchedText(want.buf.Touched))
		}
	}
}

// touchedText formats touched rows as "first-last", "" for a row not touched.
func touchedText(touched []*uv.LineData) []string {
	rows := make([]string, len(touched))
	for y, d := range touched {
		if d != nil {
			rows[y] = fmt.Sprintf("%d-%d", d.FirstCell, d.LastCell)
		}
	}
	return rows
}

// Random bytes, random stop points: the number of cell operations between two
// calls never exceeds MeterInterval.
//
// The meter's own count says so, and the screens back it: the cells written
// between two calls are at most three times the count (a cell write can blank
// the halves of a wide character on either side), so work the meter never
// counted shows. The screen and the scrollback are smaller than the lowered
// interval, so no piece of work handed over whole (a row to history, a row
// of tab stops) is larger than it.
func FuzzMeterInterval(f *testing.F) {
	for _, seed := range []string{
		"\x1b[2J",
		"x\x1b[2000b",
		"\x1b[?69h\x1b[2;50s\x1b[5;20r\x1b[3;10H\x1b[M\x1b[L\x1b[9S\x1b[9T",
		"\x1b[4hxyz\x1b[1;1H\x1b[@\x1b[P日本",
		"\x1b[?1049h\x1b[2J\x1b[?1049l",
		"\x1bc\x1b[?5W",
		"\x1b[3g\x1b[1000I\x1b[1000Z",
		"e\u0301\x1b[100S\x1b[100T",
		"日本\x1b[1;2H\x1b[K\x1b[1J\x1b[3J",
		strings.Repeat("abcdefghij\r\n", 50),
		// A full screen first, so that the whole-screen work after it
		// writes cells that show.
		"x\x1b[2999b\x1b[30S",
		"x\x1b[2999b\x1b[30T",
		"x\x1b[2999b\x1b[2J",
		"x\x1b[2999b\x1bc",
		"x\x1b[2999b\x1b[?69h\x1b[2;99s\x1b[1;1H\x1b[30M\x1b[30L",
	} {
		f.Add(uint8(3), []byte(seed))
		f.Add(uint8(0), []byte(seed))
	}
	f.Fuzz(func(t *testing.T, stopAt uint8, p []byte) {
		lowerMeterInterval(t, 256)
		e := NewEmulator(100, 30)
		e.SetScrollbackSize(16)
		e.SetScrollOffFunc(func(uv.Line) {})
		// A query's reply would otherwise block the Write on the pipe.
		e.SetReplyFunc(func([]byte) {})
		scrs := []*Screen{e.MainScreen(), e.AltScreen()}
		writes := takeCellWrites(scrs...)
		check := func(counted int) {
			if counted > meterInterval {
				t.Fatalf("the meter counted %d cell operations between two calls, more than the interval of %d", counted, meterInterval)
			}
			if n := writes.since(scrs...); n > 3*meterInterval {
				t.Fatalf("%d cells written between two calls, more than three times the interval of %d: work the meter didn't count", n, meterInterval)
			}
			writes = takeCellWrites(scrs...)
		}
		calls := 0
		e.SetStopFunc(func() bool {
			check(e.meter.last)
			calls++
			return calls == int(stopAt)
		})

		n, err := e.Write(p)
		if stopAt > 0 && calls == int(stopAt) {
			if !errors.Is(err, ErrStopped) || n > len(p) {
				t.Fatalf("Write after the stop function returned true: got (%d, %v), want at most %d bytes and ErrStopped", n, err, len(p))
			}
			if n, err := e.Write(p); n != 0 || !errors.Is(err, ErrStopped) {
				t.Fatalf("Write on a spent emulator: got (%d, %v), want (0, ErrStopped)", n, err)
			}
		} else {
			if err != nil || n != len(p) {
				t.Fatalf("Write: got (%d, %v), want (%d, nil)", n, err, len(p))
			}
			check(e.meter.work)
		}
		// What a stopped emulator holds is undefined, but reading it must
		// not fail.
		_ = e.Render()
	})
}
