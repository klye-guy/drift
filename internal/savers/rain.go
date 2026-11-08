package savers

import (
	"bytes"
	"image/color"
	"math"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/gomono"

	"archavist.com/drift/internal/saver"
)

// rain: columns of glyphs streaming down the screen, each stream with a
// bright head and a fading tail, glyphs flickering as they fall. Streams
// start at random columns and speeds, so no pixel stays lit for long.
//
// Options ([rain] in drift.conf or a kind=rain .conf):
//
//	size       = 20     glyph height, px at 1× scale
//	speed      = 1      speed multiplier
//	density    = 0.6    fraction of columns with a stream at any moment (0.1–1)
//	tail       = 22     average tail length in glyphs
//	hue        = 130    color in degrees (130 green, 190 cyan, 30 amber, 300 magenta)
//	saturation = 0.85   0 = white/grey, 1 = fully colored
//	rainbow    = false  give every stream its own hue instead
//	flicker    = 0.04   chance per glyph per frame of changing (at 60 fps)
//	glyphs     =        characters to use (default: digits, letters, symbols)
//	font       =        optional path to a .ttf/.otf (default: Go Mono)
func init() {
	saver.Register(saver.Kind{
		Name:        "rain",
		Description: "Streams of glyphs raining down the screen",
		New:         func() saver.Saver { return &rain{} },
	})
}

const defaultGlyphs = "0123456789ABCDEFGHJKLMNPQRSTUVWXYZ=+-*<>/\\|:;#$%&@{}[]()"

var (
	goMonoOnce sync.Once
	goMono     *text.GoTextFaceSource
	goMonoErr  error
)

func monoFont() (*text.GoTextFaceSource, error) {
	goMonoOnce.Do(func() { goMono, goMonoErr = text.NewGoTextFaceSource(bytes.NewReader(gomono.TTF)) })
	return goMono, goMonoErr
}

type stream struct {
	head   float64 // row of the head (fractional)
	speed  float64 // rows per second
	length int     // tail length in rows
	hue    float64
	active bool
	wait   float64 // seconds until it (re)starts
}

type rain struct {
	env        saver.Env
	cols, rows int
	cellW      float64
	cellH      float64
	atlas      []*ebiten.Image // one white glyph image per rune
	grid       []int           // glyph index per cell (cols*rows)
	streams    []stream
	speedMul   float64
	density    float64
	tail       int
	hue        float64
	sat        float64
	rainbow    bool
	flicker    float64
	clock      float64
}

func (r *rain) Setup(env saver.Env) error {
	r.env = env
	o := env.Opts
	src, err := monoFont()
	if p := o.String("font", ""); p != "" {
		src, err = loadFont(env.Path(p))
	}
	if err != nil {
		return err
	}
	size := math.Max(8, o.Float("size", 20)) * env.Scale
	face := &text.GoTextFace{Source: src, Size: size}

	glyphs := []rune(o.String("glyphs", defaultGlyphs))
	if len(glyphs) == 0 {
		glyphs = []rune(defaultGlyphs)
	}
	// Pre-render every glyph once in white; drawing then is just a tinted
	// image copy, which the GPU batches.
	for _, a := range r.atlas {
		a.Deallocate()
	}
	r.atlas = r.atlas[:0]
	r.cellW, r.cellH = 0, size*1.15
	for _, g := range glyphs {
		w, _ := text.Measure(string(g), face, 0)
		r.cellW = math.Max(r.cellW, w)
	}
	r.cellW = math.Ceil(r.cellW * 1.15)
	for _, g := range glyphs {
		img := ebiten.NewImage(int(r.cellW), int(math.Ceil(r.cellH)))
		w, _ := text.Measure(string(g), face, 0)
		op := &text.DrawOptions{}
		op.GeoM.Translate((r.cellW-w)/2, 0)
		op.ColorScale.ScaleWithColor(color.White)
		text.Draw(img, string(g), face, op)
		r.atlas = append(r.atlas, img)
	}

	r.cols = int(float64(env.W)/r.cellW) + 1
	r.rows = int(float64(env.H)/r.cellH) + 1
	r.grid = make([]int, r.cols*r.rows)
	for i := range r.grid {
		r.grid[i] = env.Rand.IntN(len(r.atlas))
	}

	r.speedMul = math.Max(0.05, o.Float("speed", 1))
	r.density = math.Min(1, math.Max(0.1, o.Float("density", 0.6)))
	r.tail = clampInt(o.Int("tail", 22), 4, 200)
	r.hue = o.Float("hue", 130)
	r.sat = math.Min(1, math.Max(0, o.Float("saturation", 0.85)))
	r.rainbow = o.Bool("rainbow", false)
	r.flicker = math.Max(0, o.Float("flicker", 0.04))

	r.streams = make([]stream, r.cols)
	for i := range r.streams {
		r.spawn(&r.streams[i], true)
	}
	return nil
}

// spawn starts a stream; at setup the streams are scattered mid-fall so the
// screen isn't empty at first.
func (r *rain) spawn(s *stream, initial bool) {
	rnd := r.env.Rand
	s.speed = (6 + rnd.Float64()*14) * r.speedMul
	s.length = max(4, int(float64(r.tail)*(0.5+rnd.Float64())))
	s.hue = r.hue
	if r.rainbow {
		s.hue = rnd.Float64() * 360
	}
	s.head = -rnd.Float64() * 4
	s.active = true
	s.wait = 0
	if initial {
		if rnd.Float64() < r.density {
			s.head = rnd.Float64() * float64(r.rows+s.length)
		} else {
			s.active = false
			s.wait = rnd.Float64() * 3 / r.density
		}
	}
}

func (r *rain) Update(dt float64) {
	r.clock += dt
	rnd := r.env.Rand
	for i := range r.streams {
		s := &r.streams[i]
		if !s.active {
			s.wait -= dt
			if s.wait <= 0 {
				r.spawn(s, false)
			}
			continue
		}
		prev := int(s.head)
		s.head += s.speed * dt
		// A fresh glyph appears under the head each time it enters a row.
		for row := prev + 1; row <= int(s.head); row++ {
			if row >= 0 && row < r.rows {
				r.grid[row*r.cols+i] = rnd.IntN(len(r.atlas))
			}
		}
		if s.head-float64(s.length) > float64(r.rows) {
			s.active = false
			// Idle gap: denser rain means shorter gaps between streams.
			s.wait = rnd.Float64() * 2.5 * (1 - r.density + 0.15) / r.density
		}
	}
	// Random glyph changes across the field.
	n := int(float64(len(r.grid)) * r.flicker * dt * 60)
	for k := 0; k < n; k++ {
		r.grid[rnd.IntN(len(r.grid))] = rnd.IntN(len(r.atlas))
	}
}

func (r *rain) Draw(dst *ebiten.Image) {
	op := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
	for i := range r.streams {
		s := &r.streams[i]
		if !s.active {
			continue
		}
		head := int(s.head)
		x := float64(i) * r.cellW
		base := hsv(s.hue, r.sat, 1)
		for k := 0; k <= s.length; k++ {
			row := head - k
			if row < 0 || row >= r.rows {
				continue
			}
			var c color.RGBA
			if k == 0 {
				c = hsv(s.hue, r.sat*0.25, 1) // head: nearly white
			} else {
				f := math.Pow(1-float64(k)/float64(s.length+1), 1.6)
				c = scaleColor(base, 0.15+0.85*f)
				if k > s.length-2 {
					c = scaleColor(c, 0.5)
				}
			}
			op.GeoM.Reset()
			op.GeoM.Translate(x, float64(row)*r.cellH)
			op.ColorScale.Reset()
			op.ColorScale.ScaleWithColor(c)
			dst.DrawImage(r.atlas[r.grid[row*r.cols+i]], op)
		}
	}
}
