package savers

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"archavist.com/drift/internal/saver"
)

// ribbons: smooth cubic Bézier curves whose control points wander the screen,
// painted onto a fading canvas so each curve sweeps out a glowing ribbon.
//
// Options:
//
//	curves   = 3     how many curves
//	speed    = 180   control-point speed, px/s at 1× scale
//	width    = 1.5   line width, px at 1× scale
//	trail    = 0.25  brightness kept per second (higher = longer ribbons)
//	hue_rate = 25    degrees of hue shift per second
func init() {
	saver.Register(saver.Kind{
		Name:        "ribbons",
		Description: "Wandering Bézier curves that sweep out fading ribbons",
		New:         func() saver.Saver { return &ribbons{} },
	})
}

type ribbons struct {
	w, h    float64
	canvas  *fader
	curves  [][4]bouncer
	hues    []float64
	width   float32
	hueRate float64
}

func (r *ribbons) Setup(env saver.Env) error {
	o := env.Opts
	r.w, r.h = float64(env.W), float64(env.H)
	if r.canvas != nil {
		r.canvas.dispose()
	}
	r.canvas = newFader(env.W, env.H, o.Float("trail", 0.25))
	r.width = float32(o.Float("width", 1.5) * env.Scale)
	r.hueRate = o.Float("hue_rate", 25)
	speed := o.Float("speed", 180) * env.Scale

	n := clampInt(o.Int("curves", 3), 1, 12)
	r.curves = make([][4]bouncer, n)
	r.hues = make([]float64, n)
	for i := range r.curves {
		for j := range r.curves[i] {
			r.curves[i][j] = newBouncer(env.Rand, r.w, r.h, speed)
		}
		r.hues[i] = env.Rand.Float64() * 360
	}
	return nil
}

func (r *ribbons) Update(dt float64) {
	r.canvas.fade(dt)
	for i := range r.curves {
		for j := range r.curves[i] {
			r.curves[i][j].step(dt, r.w, r.h)
		}
		r.hues[i] += r.hueRate * dt

		c := r.curves[i]
		var p vector.Path
		p.MoveTo(float32(c[0].x), float32(c[0].y))
		p.CubicTo(float32(c[1].x), float32(c[1].y), float32(c[2].x), float32(c[2].y), float32(c[3].x), float32(c[3].y))
		op := &vector.DrawPathOptions{AntiAlias: true}
		op.ColorScale.ScaleWithColor(hsv(r.hues[i], 0.8, 1))
		vector.StrokePath(r.canvas.img, &p, &vector.StrokeOptions{Width: r.width}, op)
	}
}

func (r *ribbons) Draw(dst *ebiten.Image) {
	dst.DrawImage(r.canvas.img, nil)
}
