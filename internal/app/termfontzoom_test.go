package app

import (
	"math"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

// TestTerminalWheelFont: the wheel with fontMods held steps the font a
// point a notch (up larger), a touchpad by the distance it scrolls but
// not its glide, a pinch by how far it opens; without the modifiers the
// wheel scrolls and leaves the font alone, even for a program reading the
// mouse.
func TestTerminalWheelFont(t *testing.T) {
	term := vt.New(20, 4)
	v := newTermView(paintTarget{term}, 13)
	v.cellH = 16
	var steps []int
	v.zoomFont = func(n int) { steps = append(steps, n) }
	scroll := func(dy float32, mods ui.Modifiers, precise, momentum bool) {
		t.Helper()
		if !v.input(ui.InputEvent{Kind: ui.InputScroll, DY: dy, Mods: mods, Precise: precise, Momentum: momentum}, nil) {
			t.Fatalf("scroll %v with %v not taken", dy, mods)
		}
	}

	scroll(-1, ui.Ctrl, false, false)
	scroll(-1, ui.Ctrl, false, false)
	scroll(3, ui.Ctrl, false, false)
	scroll(-1, linkMods(), false, false)
	if want := []int{1, 1, -1, 1}; !slices.Equal(steps, want) {
		t.Fatalf("wheel notches stepped %v, want %v", steps, want)
	}

	steps = nil
	scroll(-fontScrollStep/2, ui.Ctrl, true, false)
	if len(steps) != 0 {
		t.Fatalf("half a step of a touchpad stepped %v", steps)
	}
	scroll(-fontScrollStep/2, ui.Ctrl, true, false)
	scroll(-5*fontScrollStep, ui.Ctrl, true, true)
	scroll(2*fontScrollStep, ui.Ctrl, true, false)
	if want := []int{1, -2}; !slices.Equal(steps, want) {
		t.Fatalf("touchpad stepped %v, want %v", steps, want)
	}

	steps = nil
	pinch := func(scale float32) {
		t.Helper()
		if !v.input(ui.InputEvent{Kind: ui.InputZoom, Scale: scale}, nil) {
			t.Fatalf("pinch %v not taken", scale)
		}
	}
	pinch(float32(math.Exp(fontPinchStep / 2)))
	pinch(float32(math.Exp(fontPinchStep/2 + 0.001)))
	pinch(float32(math.Exp(-3 * fontPinchStep)))
	if want := []int{1, -2}; !slices.Equal(steps, want) {
		t.Fatalf("pinches stepped %v, want %v", steps, want)
	}

	// A program reading the mouse gets the plain wheel, the font stays.
	var out []byte
	v.target = recordTarget{term, &out}
	term.WriteString("\x1b[?1000h\x1b[?1006h")
	steps = nil
	scroll(-1, 0, false, false)
	if len(steps) != 0 || len(out) == 0 {
		t.Fatalf("plain wheel: stepped %v, reported %q", steps, out)
	}
	out = out[:0]
	scroll(-1, ui.Ctrl, false, false)
	if !slices.Equal(steps, []int{1}) || len(out) != 0 {
		t.Fatalf("Ctrl+wheel: stepped %v, reported %q", steps, out)
	}
}

type recordTarget struct {
	term *vt.Terminal
	out  *[]byte
}

func (r recordTarget) Terminal() *vt.Terminal { return r.term }
func (r recordTarget) Resize(cols, rows int)  { r.term.Resize(cols, rows) }
func (r recordTarget) Write(b []byte) (int, error) {
	*r.out = append(*r.out, b...)
	return len(b), nil
}

// TestStepTermFont: a step changes the font of the terminals open and
// persists it, within the settings' bounds.
func TestStepTermFont(t *testing.T) {
	a, _ := newTestApp(t)
	tv := newTermView(paintTarget{vt.New(20, 4)}, float32(a.settings.TerminalFontSize))
	a.conns[-1] = &conn{tv: tv}

	a.stepTermFont(2)
	if a.settings.TerminalFontSize != 15 || tv.fontSize != 15 || a.fontChoice != 15 {
		t.Fatalf("after +2: settings %d, terminal %v, field %v", a.settings.TerminalFontSize, tv.fontSize, a.fontChoice)
	}
	saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
	if err != nil || saved.TerminalFontSize != 15 {
		t.Fatalf("saved %d, %v", saved.TerminalFontSize, err)
	}

	a.stepTermFont(100)
	if a.settings.TerminalFontSize != settings.MaxFontSize {
		t.Errorf("past the top: %d", a.settings.TerminalFontSize)
	}
	a.stepTermFont(-100)
	if a.settings.TerminalFontSize != settings.MinFontSize || tv.fontSize != settings.MinFontSize {
		t.Errorf("past the bottom: %d, terminal %v", a.settings.TerminalFontSize, tv.fontSize)
	}
}
