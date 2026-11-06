package app

import (
	_ "embed"
	"math"
	"time"

	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// The welcome page's backdrop is a lattice of dots adrift on a slow
// current, fainter behind the page's content: the pointer lights and
// parts them along its path, and a click sends a ring through them.
// fieldmotion.kage works out each dot's place and light into a canvas of
// a pixel a dot, and fielddots.kage draws the dots from it.
const (
	fieldSpacing = 22  // DIPs between dots
	fieldDot     = 1.3 // a dot's radius at rest, in DIPs
	fieldRate    = 30  // frames a second while the dots move
	// Behind the content the dots show fieldFloor of their light, coming
	// back whole fieldMask DIPs beyond its edge.
	fieldFloor, fieldMask = 0.6, 72
	// The current drifts for fieldIdle after the last pointer input, then
	// comes to rest over fieldSettle seconds.
	fieldIdle   = 45 * time.Second
	fieldSettle = 4.0
	fieldFadeIn = 700 * time.Millisecond
	// The pointer's path keeps a mark every fieldStep DIPs, fieldMarks at
	// most, each warm for fieldMarkLife seconds; the pointer itself stays
	// warm for fieldHoverLife seconds after it stops.
	fieldStep, fieldMarks, fieldMarkLife, fieldHoverLife = 12, 24, 1.4, 1.2
	// A click's ring runs for fieldRingLife seconds, fieldRings at most.
	fieldRings, fieldRingLife = 4, 1.8
)

var (
	//go:embed fieldmotion.kage
	fieldMotionSource string
	//go:embed fielddots.kage
	fieldDotsSource string

	fieldMotion = shader.MustCompile(fieldMotionSource)
	fieldDots   = shader.MustCompile(fieldDotsSource)
)

// fieldMark is where the pointer passed or clicked, in DIPs of the pane,
// when, in seconds of the field's clock, and how strongly.
type fieldMark struct{ x, y, at, force float32 }

// welcomeField is the state of the welcome page's dots.
type welcomeField struct {
	// origin is the clock's zero, last the current's last step, shown
	// when the dots first painted and input the last pointer input.
	origin, last, shown, input time.Time
	drift                      float64 // the current's time, in seconds
	calm                       bool    // at rest until the next input
	hover                      *fieldMark
	trail, rings               []fieldMark
	// pane, content and controls are where the page, what it shows and
	// its buttons were in the last frame, in window DIPs: the dots are
	// fainter behind the content, and a press on a button sends no ring.
	pane, content ui.Rect
	controls      []ui.Rect
	canvas        *ui.Canvas
}

// leave forgets the dots as the welcome page closes, to fade in anew.
func (f *welcomeField) leave() { *f = welcomeField{} }

// hold notes el, last frame's, as part of the page's content, and as a
// control that takes its own presses.
func (f *welcomeField) hold(el ui.Element, control bool) {
	r := el.Bounds()
	if r.W <= 0 || r.H <= 0 {
		return
	}
	if control {
		f.controls = append(f.controls, r)
	}
	if f.content.W <= 0 {
		f.content = r
		return
	}
	x0, y0 := min(f.content.X, r.X), min(f.content.Y, r.Y)
	x1, y1 := max(f.content.X+f.content.W, r.X+r.W), max(f.content.Y+f.content.H, r.Y+r.H)
	f.content = ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// onControl reports whether (x, y) in the pane lies on a control.
func (f *welcomeField) onControl(x, y float32) bool {
	for _, r := range f.controls {
		if r.Contains(f.pane.X+x, f.pane.Y+y) {
			return true
		}
	}
	return false
}

func (f *welcomeField) clock(now time.Time) float32 {
	if f.origin.IsZero() {
		f.origin = now
	}
	return float32(now.Sub(f.origin).Seconds())
}

func (f *welcomeField) wake(now time.Time) {
	f.input = now
	if f.calm {
		f.calm, f.last = false, time.Time{}
	}
}

// handle notes the pointer's moves and primary presses off the buttons,
// unless still; it reports whether a frame should follow.
func (f *welcomeField) handle(ev ui.InputEvent, still bool) bool {
	if still {
		return false
	}
	switch ev.Kind {
	case ui.InputPointerMove:
		calm := f.calm
		f.move(ev.X, ev.Y, ev.Time)
		return calm
	case ui.InputPointerDown:
		if ev.Button == 0 && !f.onControl(ev.X, ev.Y) {
			f.tap(ev.X, ev.Y, ev.Time)
			return true
		}
	}
	return false
}

// move notes the pointer at (x, y): a mark each fieldStep DIPs of its
// path, stronger the faster it goes.
func (f *welcomeField) move(x, y float32, now time.Time) {
	t := f.clock(now)
	f.wake(now)
	f.hover = &fieldMark{x: x, y: y, at: t, force: 1}
	force := float32(0.3)
	if n := len(f.trail); n > 0 {
		p := f.trail[n-1]
		d := float32(math.Hypot(float64(x-p.x), float64(y-p.y)))
		if d < fieldStep {
			return
		}
		if dt := t - p.at; dt < 0.3 {
			force += 0.7 * smoothstep(80, 1000, d/max(dt, 1.0/240))
		}
	}
	f.trail = append(lastMarks(f.trail, fieldMarks-1), fieldMark{x, y, t, force})
}

// tap sends a ring out from (x, y).
func (f *welcomeField) tap(x, y float32, now time.Time) {
	t := f.clock(now)
	f.wake(now)
	f.rings = append(lastMarks(f.rings, fieldRings-1), fieldMark{x, y, t, 1})
}

// step moves the current on to now, and lets it settle once the pointer
// has been still for fieldIdle and the last marks and rings faded.
func (f *welcomeField) step(now time.Time) {
	if !f.last.IsZero() {
		f.drift += min(max(now.Sub(f.last).Seconds(), 0), 0.1) * f.energy(now)
	}
	f.last = now
	t := f.clock(now)
	f.trail = marksSince(f.trail, t-fieldMarkLife)
	f.rings = marksSince(f.rings, t-fieldRingLife)
	if f.hover != nil && t-f.hover.at >= fieldHoverLife {
		f.hover = nil
	}
	if f.energy(now) == 0 && f.hover == nil && len(f.trail) == 0 && len(f.rings) == 0 {
		f.calm = true
	}
}

// energy is how fast the current drifts at now, from 1 down to rest.
func (f *welcomeField) energy(now time.Time) float64 {
	return 1 - float64(smoothstep(0, 1, float32((now.Sub(f.input)-fieldIdle).Seconds()/fieldSettle)))
}

// marks returns the shaders' trail and rings at t: each mark's place,
// warmth and reach, and each ring's place, age and strength.
func (f *welcomeField) marks(t float32) (trail, rings []float32) {
	if h := f.hover; h != nil && t-h.at < fieldHoverLife {
		fade := 1 - (t-h.at)/fieldHoverLife
		trail = append(trail, h.x, h.y, 0.4*fade*fade, 64)
	}
	for _, m := range f.trail {
		if age := t - m.at; age < fieldMarkLife {
			fade := 1 - age/fieldMarkLife
			trail = append(trail, m.x, m.y, m.force*fade*fade, 36+44*age)
		}
	}
	for _, r := range f.rings {
		if age := t - r.at; age < fieldRingLife {
			rings = append(rings, r.x, r.y, age, r.force)
		}
	}
	return trail, rings
}

// draw paints the dots over r, the pane, and asks for frames while they
// move. Where the window draws on the CPU it paints nothing: the shaders
// would take far too long there.
func (f *welcomeField) draw(p *ui.Painter, r ui.Rect, tok theme.Tokens, still bool) {
	if p.Accelerated() {
		f.paint(p, r, tok, still)
	}
}

func (f *welcomeField) paint(p *ui.Painter, r ui.Rect, tok theme.Tokens, still bool) {
	if r.W < fieldSpacing || r.H < fieldSpacing {
		return
	}
	now := p.Now()
	if f.shown.IsZero() {
		f.shown = now
		f.wake(now)
	}
	if still {
		f.hover, f.trail, f.rings = nil, nil, nil
	} else if !f.calm && !now.Equal(f.last) {
		f.step(now)
	}

	cols, rows := int(r.W/fieldSpacing)+1, int(r.H/fieldSpacing)+1
	if f.canvas == nil || !canvasSized(f.canvas, cols, rows) {
		f.canvas = ui.NewCanvas(cols, rows)
	}
	origin := []float32{(r.W - float32(cols)*fieldSpacing) / 2, (r.H - float32(rows)*fieldSpacing) / 2}
	reach := float32(fieldSpacing/2 - 1.9*fieldDot - 1)
	trail, rings := f.marks(f.clock(now))
	f.canvas.Draw(p, func(cp *ui.Painter) {
		fieldMotion.Draw(cp, ui.Rect{W: float32(cols), H: float32(rows)}, shader.Options{Uniforms: map[string]any{
			"Time": float32(f.drift), "Spacing": float32(fieldSpacing), "Origin": origin, "Reach": reach,
			"TrailCount": len(trail) / 4, "Trail": trail, "RingCount": len(rings) / 4, "Rings": rings,
		}})
	})

	opacity, rest := float32(1), float32(0.16)
	if tok.Dark {
		rest = 0.2
	}
	hole := []float32{f.content.X - f.pane.X, f.content.Y - f.pane.Y, f.content.W, f.content.H}
	since := now.Sub(f.shown)
	if !still && since < fieldFadeIn {
		e := float32(since) / float32(fieldFadeIn)
		opacity = e * (2 - e)
	}
	fieldDots.Draw(p, r, shader.Options{Images: [4]*ui.Bitmap{f.canvas.Bitmap()}, Uniforms: map[string]any{
		"Time": float32(f.drift), "Spacing": float32(fieldSpacing), "Origin": origin, "Reach": reach,
		"Dot": float32(fieldDot), "Rest": rest, "Hole": hole, "Floor": float32(fieldFloor), "Mask": float32(fieldMask), "Ink": tok.TextMuted, "Glow": tok.Accent, "Opacity": opacity,
	}})
	if !still && (!f.calm || since < fieldFadeIn) {
		p.AnimationFrameRate(fieldRate)
	}
}

func canvasSized(c *ui.Canvas, w, h int) bool {
	cw, ch := c.Size()
	return cw == w && ch == h
}

func lastMarks(m []fieldMark, n int) []fieldMark {
	if len(m) > n {
		m = append(m[:0], m[len(m)-n:]...)
	}
	return m
}

func marksSince(m []fieldMark, t float32) []fieldMark {
	out := m[:0]
	for _, x := range m {
		if x.at > t {
			out = append(out, x)
		}
	}
	return out
}

func smoothstep(lo, hi, x float32) float32 {
	v := min(max((x-lo)/(hi-lo), 0), 1)
	return v * v * (3 - 2*v)
}
