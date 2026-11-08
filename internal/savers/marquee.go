package savers

import (
	"bytes"
	"math"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/gobold"

	"archavist.com/drift/internal/saver"
)

// marquee: a line of text scrolling across the screen. Each pass enters at a
// new height with a new color, so the text never retraces the same pixels.
//
// Options:
//
//	text  = drift   the message
//	size  = 96      text height, px at 1× scale
//	speed = 220     scroll speed, px/s at 1× scale
//	font  =         optional path to a .ttf/.otf file (default: Go Bold)
func init() {
	saver.Register(saver.Kind{
		Name:        "marquee",
		Description: "Scrolling text that changes height and color each pass",
		New:         func() saver.Saver { return &marquee{} },
	})
}

var (
	goBoldOnce sync.Once
	goBold     *text.GoTextFaceSource
	goBoldErr  error
)

func defaultFont() (*text.GoTextFaceSource, error) {
	goBoldOnce.Do(func() { goBold, goBoldErr = text.NewGoTextFaceSource(bytes.NewReader(gobold.TTF)) })
	return goBold, goBoldErr
}

type marquee struct {
	env    saver.Env
	w, h   float64
	msg    string
	face   *text.GoTextFace
	tw, th float64
	x, y   float64
	speed  float64
	hue    float64
	bobT   float64
}

func (m *marquee) Setup(env saver.Env) error {
	m.env = env
	m.w, m.h = float64(env.W), float64(env.H)
	m.msg = env.Opts.String("text", "drift")
	m.speed = env.Opts.Float("speed", 220) * env.Scale

	src, err := defaultFont()
	if p := env.Opts.String("font", ""); p != "" {
		src, err = loadFont(env.Path(p))
	}
	if err != nil {
		return err
	}
	m.face = &text.GoTextFace{Source: src, Size: env.Opts.Float("size", 96) * env.Scale}
	m.tw, m.th = text.Measure(m.msg, m.face, 0)
	m.newPass()
	m.x = m.w * env.Rand.Float64() // first pass starts mid-screen, not off-edge
	return nil
}

func (m *marquee) newPass() {
	m.x = m.w
	m.y = m.env.Rand.Float64() * math.Max(1, m.h-m.th)
	m.hue = m.env.Rand.Float64() * 360
}

func (m *marquee) Update(dt float64) {
	m.x -= m.speed * dt
	m.bobT += dt
	if m.x < -m.tw {
		m.newPass()
	}
}

func (m *marquee) Draw(dst *ebiten.Image) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(m.x, m.y+math.Sin(m.bobT*0.7)*m.th*0.15)
	op.ColorScale.ScaleWithColor(hsv(m.hue+m.bobT*6, 0.7, 1))
	text.Draw(dst, m.msg, m.face, op)
}
