package savers

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"archavist.com/drift/internal/saver"
)

// mystify: a few closed polygons whose corners bounce around the screen,
// each leaving a ribbon of echoes behind it while its color slowly cycles.
//
// Options ([mystify] in drift.conf or a kind=mystify .conf):
//
//	polygons = 2     how many shapes
//	vertices = 4     corners per shape
//	echoes   = 6     outlines drawn behind each shape
//	spacing  = 0.06  seconds between echoes
//	speed    = 260   corner speed, px/s at 1× scale
//	width    = 2     line width, px at 1× scale
//	hue_rate = 18    degrees of hue shift per second
func init() {
	saver.Register(saver.Kind{
		Name:        "mystify",
		Description: "Bouncing polygons with echo trails and cycling colors",
		New:         func() saver.Saver { return &mystify{} },
	})
}

type mystify struct {
	w, h     float64
	shapes   []*polyShape
	echoes   int
	spacing  float64
	width    float32
	hueRate  float64
	clock    float64
	lastSnap float64
}

type polyShape struct {
	pts     []bouncer
	history [][]float64 // newest first: x0,y0,x1,y1,...
	hue     float64
}

func (m *mystify) Setup(env saver.Env) error {
	o := env.Opts
	m.w, m.h = float64(env.W), float64(env.H)
	m.echoes = clampInt(o.Int("echoes", 6), 1, 40)
	m.spacing = o.Float("spacing", 0.06)
	m.width = float32(o.Float("width", 2) * env.Scale)
	m.hueRate = o.Float("hue_rate", 18)
	speed := o.Float("speed", 260) * env.Scale
	n := clampInt(o.Int("polygons", 2), 1, 12)
	v := clampInt(o.Int("vertices", 4), 2, 16)

	m.shapes = nil
	for i := 0; i < n; i++ {
		s := &polyShape{hue: env.Rand.Float64() * 360}
		for j := 0; j < v; j++ {
			s.pts = append(s.pts, newBouncer(env.Rand, m.w, m.h, speed))
		}
		m.shapes = append(m.shapes, s)
	}
	return nil
}

func (m *mystify) Update(dt float64) {
	m.clock += dt
	snap := m.clock-m.lastSnap >= m.spacing
	if snap {
		m.lastSnap = m.clock
	}
	for _, s := range m.shapes {
		for i := range s.pts {
			s.pts[i].step(dt, m.w, m.h)
		}
		s.hue += m.hueRate * dt
		if snap || len(s.history) == 0 {
			frame := make([]float64, 0, 2*len(s.pts))
			for _, p := range s.pts {
				frame = append(frame, p.x, p.y)
			}
			s.history = append([][]float64{frame}, s.history...)
			if len(s.history) > m.echoes {
				s.history = s.history[:m.echoes]
			}
		}
	}
}

func (m *mystify) Draw(dst *ebiten.Image) {
	for _, s := range m.shapes {
		// Draw oldest first so the bright leading edge sits on top.
		for i := len(s.history) - 1; i >= 0; i-- {
			f := s.history[i]
			fade := 1 - float64(i)/float64(len(s.history)+1)
			c := scaleColor(hsv(s.hue-float64(i)*4, 0.85, 1), fade)
			var p vector.Path
			p.MoveTo(float32(f[0]), float32(f[1]))
			for k := 2; k < len(f); k += 2 {
				p.LineTo(float32(f[k]), float32(f[k+1]))
			}
			p.Close()
			op := &vector.DrawPathOptions{AntiAlias: true}
			op.ColorScale.ScaleWithColor(c)
			vector.StrokePath(dst, &p, &vector.StrokeOptions{Width: m.width, LineJoin: vector.LineJoinRound}, op)
		}
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
