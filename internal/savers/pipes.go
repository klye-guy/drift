package savers

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"archavist.com/drift/internal/saver"
)

// pipes: shaded tubes grow through a 3D grid, turning at random and ending in
// ball joints, until the space fills up; then the screen clears and a new
// scene starts from a different camera angle.
//
// Options:
//
//	pipes  = 4     pipes growing at once
//	rate   = 14    segments per second, per pipe
//	turn   = 0.25  chance of turning at each step
//	grid   = 16    grid cells along the longest axis
//	fill   = 0.35  fraction of the grid filled before the scene resets
func init() {
	saver.Register(saver.Kind{
		Name:        "pipes",
		Description: "Shaded 3D pipes growing and turning through space",
		New:         func() saver.Saver { return &pipes{} },
	})
}

type ivec3 struct{ x, y, z int }

func (a ivec3) add(b ivec3) ivec3 { return ivec3{a.x + b.x, a.y + b.y, a.z + b.z} }

var pipeDirs = []ivec3{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}}

type pipe struct {
	pos, dir ivec3
	hue      float64
	alive    bool
}

type pipes struct {
	env        saver.Env
	w, h       float64
	canvas     *ebiten.Image
	nx, ny, nz int
	used       map[ivec3]bool
	pipes      []*pipe
	rate, turn float64
	fill       float64
	acc        float64
	yaw, pitch float64
	pending    bool // clear canvas on next update
	pauseLeft  float64
}

func (p *pipes) Setup(env saver.Env) error {
	p.env = env
	p.w, p.h = float64(env.W), float64(env.H)
	if p.canvas != nil {
		p.canvas.Deallocate()
	}
	p.canvas = ebiten.NewImage(env.W, env.H)
	p.rate = env.Opts.Float("rate", 14)
	p.turn = env.Opts.Float("turn", 0.25)
	p.fill = env.Opts.Float("fill", 0.35)
	n := clampInt(env.Opts.Int("grid", 16), 4, 64)
	aspect := p.w / p.h
	p.nx = n
	p.ny = max(4, int(float64(n)/aspect))
	p.nz = max(4, n*2/3)
	p.newScene(clampInt(env.Opts.Int("pipes", 4), 1, 16))
	return nil
}

func (p *pipes) newScene(count int) {
	r := p.env.Rand
	p.canvas.Clear()
	p.used = map[ivec3]bool{}
	p.yaw = (r.Float64()*2 - 1) * 0.5
	p.pitch = (r.Float64()*2 - 1) * 0.3
	p.pipes = make([]*pipe, count)
	for i := range p.pipes {
		p.pipes[i] = &pipe{}
		p.spawn(p.pipes[i])
	}
}

func (p *pipes) spawn(pp *pipe) {
	r := p.env.Rand
	for tries := 0; tries < 50; tries++ {
		c := ivec3{r.IntN(p.nx), r.IntN(p.ny), r.IntN(p.nz)}
		if !p.used[c] {
			*pp = pipe{pos: c, dir: pipeDirs[r.IntN(6)], hue: r.Float64() * 360, alive: true}
			p.used[c] = true
			p.ball(c, pp.hue)
			return
		}
	}
	pp.alive = false
}

func (p *pipes) in(c ivec3) bool {
	return c.x >= 0 && c.y >= 0 && c.z >= 0 && c.x < p.nx && c.y < p.ny && c.z < p.nz
}

func (p *pipes) Update(dt float64) {
	if p.pauseLeft > 0 {
		p.pauseLeft -= dt
		if p.pauseLeft <= 0 {
			p.newScene(len(p.pipes))
		}
		return
	}
	p.acc += dt * p.rate
	for p.acc >= 1 {
		p.acc--
		p.step()
	}
	full := float64(len(p.used)) / float64(p.nx*p.ny*p.nz)
	anyAlive := false
	for _, pp := range p.pipes {
		anyAlive = anyAlive || pp.alive
	}
	if full >= p.fill || !anyAlive {
		p.pauseLeft = 2.5 // admire the finished scene, then reset
	}
}

func (p *pipes) step() {
	r := p.env.Rand
	for _, pp := range p.pipes {
		if !pp.alive {
			p.spawn(pp)
			continue
		}
		dir := pp.dir
		next := pp.pos.add(dir)
		turned := false
		if r.Float64() < p.turn || !p.in(next) || p.used[next] {
			// Pick a random open perpendicular-or-straight direction.
			opts := make([]ivec3, 0, 6)
			for _, d := range pipeDirs {
				if d == (ivec3{-dir.x, -dir.y, -dir.z}) {
					continue
				}
				if n := pp.pos.add(d); p.in(n) && !p.used[n] {
					opts = append(opts, d)
				}
			}
			if len(opts) == 0 {
				pp.alive = false // boxed in; a new pipe spawns next step
				continue
			}
			nd := opts[r.IntN(len(opts))]
			turned = nd != dir
			dir, next = nd, pp.pos.add(nd)
		}
		if turned {
			p.ball(pp.pos, pp.hue) // elbow joint
		}
		p.tube(pp.pos, next, pp.hue)
		p.used[next] = true
		pp.pos, pp.dir = next, dir
	}
}

// project maps a grid cell center to screen space; returns x, y and the
// on-screen size of one world unit at that depth.
func (p *pipes) project(c ivec3) (float64, float64, float64) {
	x := float64(c.x) - float64(p.nx-1)/2
	y := float64(c.y) - float64(p.ny-1)/2
	z := float64(c.z) - float64(p.nz-1)/2
	// yaw around Y, then pitch around X
	cy, sy := math.Cos(p.yaw), math.Sin(p.yaw)
	x, z = x*cy-z*sy, x*sy+z*cy
	cp, sp := math.Cos(p.pitch), math.Sin(p.pitch)
	y, z = y*cp-z*sp, y*sp+z*cp
	dist := float64(max(p.nx, p.nz)) * 1.6
	fov := p.w / float64(p.nx) * dist * 0.95
	zc := z + dist
	return p.w/2 + x/zc*fov, p.h/2 + y/zc*fov, fov / zc
}

const pipeRadius = 0.28 // in grid cells

// shade returns the tube color for a lightness band (0 = edge, 1 = highlight).
func shade(hue, t float64) (r, g, b, a float32) {
	c := hsv(hue, 0.75*(1-0.6*t*t), 0.25+0.75*t)
	return float32(c.R) / 255, float32(c.G) / 255, float32(c.B) / 255, 1
}

// tube draws a cylinder between two cells as layered quads: dark edges,
// bright core, and a highlight nudged toward an up-left light.
func (p *pipes) tube(a, b ivec3, hue float64) {
	x0, y0, s0 := p.project(a)
	x1, y1, s1 := p.project(b)
	dx, dy := x1-x0, y1-y0
	l := math.Hypot(dx, dy)
	if l < 0.5 {
		// Pointing at the camera: the joint ball covers it.
		p.ball(b, hue)
		return
	}
	nx, ny := -dy/l, dx/l
	// Which side of the tube faces the light (up-left)?
	side := -(nx*-0.7 + ny*-0.7)
	if side == 0 {
		side = 1
	}
	side = math.Copysign(1, side)

	bands := []struct{ width, offset, light float64 }{
		{1.00, 0, 0.0},
		{0.80, -0.06, 0.35},
		{0.55, -0.14, 0.7},
		{0.20, -0.30, 1.0},
	}
	for _, bd := range bands {
		r0, r1 := pipeRadius*s0*bd.width, pipeRadius*s1*bd.width
		o0, o1 := pipeRadius*s0*bd.offset*side, pipeRadius*s1*bd.offset*side
		var path vector.Path
		path.MoveTo(float32(x0+nx*(o0+r0)), float32(y0+ny*(o0+r0)))
		path.LineTo(float32(x1+nx*(o1+r1)), float32(y1+ny*(o1+r1)))
		path.LineTo(float32(x1+nx*(o1-r1)), float32(y1+ny*(o1-r1)))
		path.LineTo(float32(x0+nx*(o0-r0)), float32(y0+ny*(o0-r0)))
		path.Close()
		op := &vector.DrawPathOptions{AntiAlias: true}
		op.ColorScale.Scale(shade(hue, bd.light))
		vector.FillPath(p.canvas, &path, nil, op)
	}
}

// ball draws a shaded sphere joint at a cell.
func (p *pipes) ball(c ivec3, hue float64) {
	x, y, s := p.project(c)
	r := pipeRadius * s * 1.2
	const steps = 6
	for i := 0; i < steps; i++ {
		t := float64(i) / (steps - 1)
		rr := r * (1 - 0.8*t)
		off := r * 0.35 * t
		var path vector.Path
		path.Arc(float32(x-off), float32(y-off), float32(rr), 0, 2*math.Pi, vector.Clockwise)
		path.Close()
		op := &vector.DrawPathOptions{AntiAlias: true}
		op.ColorScale.Scale(shade(hue, t))
		vector.FillPath(p.canvas, &path, nil, op)
	}
}

func (p *pipes) Draw(dst *ebiten.Image) {
	dst.DrawImage(p.canvas, nil)
}
