package vt

import (
	"github.com/charmbracelet/x/ansi"
)

func (e *Emulator) handleMode(params ansi.Params, set, isAnsi bool) {
	for _, p := range params {
		param := p.Param(-1)
		if param == -1 {
			// Missing parameter, ignore
			continue
		}

		var mode ansi.Mode = ansi.DECMode(param)
		if isAnsi {
			mode = ansi.ANSIMode(param)
		}

		setting := e.modes[mode]
		if setting == ansi.ModePermanentlyReset || setting == ansi.ModePermanentlySet {
			// Permanently set modes are ignored.
			continue
		}

		setting = ansi.ModeReset
		if set {
			setting = ansi.ModeSet
		}

		e.setMode(mode, setting)
	}
}

// setAltScreenMode sets the alternate screen mode.
func (e *Emulator) setAltScreenMode(on bool) {
	if (on && e.scr == &e.scrs[1]) || (!on && e.scr == &e.scrs[0]) {
		// Already in alternate screen mode, or normal screen, do nothing.
		return
	}
	if on {
		// The pending wrap belongs to the main screen's cursor, which the
		// switch back restores: keep it for then.
		e.mainPhantom = e.atPhantom
		e.scr = &e.scrs[1]
		e.scrs[1].cur = e.scrs[0].cur
		e.scr.Clear()
		e.scr.ClearTouched()
		e.setCursor(0, 0)
	} else {
		e.scr = &e.scrs[0]
		// The main cursor comes back with its pending wrap, as xterm's
		// ?1049l restores it with the cursor; the alternate screen's does
		// not follow. A resize meanwhile keeps it, as it keeps the active
		// screen's.
		e.atPhantom = e.mainPhantom
		e.mainPhantom = false
	}
	if e.cb.AltScreen != nil {
		e.cb.AltScreen(on)
	}
	if e.cb.CursorVisibility != nil {
		e.cb.CursorVisibility(!e.scr.cur.Hidden)
	}
}

// saveCursor saves the cursor in the active screen's slot, as xterm's
// CursorSave does: its position, pen and link, and with them the character
// sets, GL and GR, origin mode and the pending wrap.
func (e *Emulator) saveCursor() {
	designations, gl, gr := e.Charsets()
	e.scr.SaveCursor()
	e.scr.saved.Saved = SavedState{
		Designations: designations,
		GL:           gl,
		GR:           gr,
		Origin:       e.isModeSet(ansi.ModeOrigin),
		Wrap:         e.atPhantom,
		Saved:        true,
	}
}

// restoreCursor restores what saveCursor saved, in the order of xterm's
// CursorRestoreFlags: the character sets, GL and GR, then origin mode, then
// the move (see [Screen.RestoreCursor]) with the pen and link, then the
// pending wrap, which xterm sets after the move has cleared it. A slot never
// saved restores the defaults. A pending single shift stays, as the slot
// doesn't hold it.
func (e *Emulator) restoreCursor() {
	st := e.scr.SavedCursor().Saved
	for g, d := range st.Designations {
		e.designations[g] = d
		e.charsets[g] = charsetOf(d)
	}
	e.gl, e.gr = st.GL, st.GR
	// Origin mode comes back as a flag, without the homing that setting it
	// does.
	if st.Origin != e.isModeSet(ansi.ModeOrigin) {
		setting, cb := ansi.ModeReset, e.cb.DisableMode
		if st.Origin {
			setting, cb = ansi.ModeSet, e.cb.EnableMode
		}
		e.modes[ansi.ModeOrigin] = setting
		if cb != nil {
			cb(ansi.ModeOrigin)
		}
	}
	e.scr.RestoreCursor()
	e.atPhantom = st.Wrap
}

// setMode sets the mode to the given value.
func (e *Emulator) setMode(mode ansi.Mode, setting ansi.ModeSetting) {
	e.logf("setting mode %T(%v) to %v", mode, mode, setting)
	e.modes[mode] = setting
	switch mode {
	case ansi.ModeInsertReplace:
		e.insertMode = setting.IsSet()
	case ansi.ModeOrigin:
		// Setting or resetting DECOM homes the cursor, to the scroll
		// region's corner or the screen's.
		e.setCursorPosition(0, 0)
	case ansi.ModeTextCursorEnable:
		e.scr.setCursorHidden(!setting.IsSet())
	case ansi.ModeAltScreen:
		e.setAltScreenMode(setting.IsSet())
	case ansi.ModeSaveCursor:
		if setting.IsSet() {
			e.saveCursor()
		} else {
			e.restoreCursor()
		}
	case ansi.ModeAltScreenSaveCursor: // Alternate Screen Save Cursor (1047 & 1048)
		// As in xterm, ?1049h saves the cursor, then switches to the
		// alternate screen, and ?1049l switches back, then restores the
		// cursor, whichever screen was showing.
		// Doesn't support scrollback
		if setting.IsSet() {
			e.saveCursor()
		}
		e.setAltScreenMode(setting.IsSet())
		if !setting.IsSet() {
			e.restoreCursor()
		}
	case ansi.ModeInBandResize:
		if setting.IsSet() {
			e.reply(ansi.InBandResize(e.Height(), e.Width(), 0, 0))
		}
	}
	if setting.IsSet() {
		if e.cb.EnableMode != nil {
			e.cb.EnableMode(mode)
		}
	} else if setting.IsReset() {
		if e.cb.DisableMode != nil {
			e.cb.DisableMode(mode)
		}
	}
}

// isModeSet returns true if the mode is set.
func (e *Emulator) isModeSet(mode ansi.Mode) bool {
	m, ok := e.modes[mode]
	return ok && m.IsSet()
}
