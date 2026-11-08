package savers

import (
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"archavist.com/drift/internal/saver"
)

// sprite: your own artwork moving around the screen. Configured entirely from
// a .conf file — no code, no rebuild:
//
//	# ~/.config/drift/savers/mychar.conf
//	kind   = sprite
//	image  = mychar.png      # PNG/JPEG, animated GIF, or a folder of PNG frames
//	motion = float           # bounce | drift | float | wander
//	height = 0.45            # sprite height as a fraction of the screen
//
// Options:
//
//	image   (required) path, relative to the .conf file or ~/...
//	motion  = bounce
//	speed   = 160     px/s at 1× scale (bounce, wander); drift/float scale it too
//	height  = 0.35    fraction of screen height; set 0 to use scale instead
//	scale   = 1       multiplier on the image's own size when height = 0
//	count   = 1       copies on screen
//	flip    = true    mirror the image to face its direction of travel
//	spin    = 0       degrees per second of continuous rotation
//	sway    = 0       degrees of gentle back-and-forth tilt (float motion)
//	fps     = 12      frame rate when image is a folder of frames
//	gif_speed = 1     playback speed multiplier for animated GIFs
//	trail   = 0       0 = no trail; else brightness kept per second (e.g. 0.3)
func init() {
	saver.Register(saver.Kind{
		Name:        "sprite",
		Description: "Your own image or animation moving around the screen",
		New:         func() saver.Saver { return &sprite{} },
		NeedsConfig: true,
	})
}

type spriteFrames struct {
	imgs   []*ebiten.Image
	delays []float64 // seconds per frame
	w, h   int
}

type actor struct {
	x, y, vx, vy float64 // top-left position, velocity
	t, phase     float64
	frame        int
	frameT       float64
	facing       float64 // +1 right, -1 left
	heading      float64 // wander
}

type sprite struct {
	env     saver.Env
	w, h    float64
	frames  *spriteFrames
	actors  []*actor
	dw, dh  float64 // drawn size
	motion  string
	speed   float64
	flip    bool
	spin    float64
	sway    float64
	gifRate float64
	trail   *fader
}

func (s *sprite) Setup(env saver.Env) error {
	s.env = env
	s.w, s.h = float64(env.W), float64(env.H)
	o := env.Opts

	src := o.String("image", "")
	if src == "" {
		return fmt.Errorf("sprite needs image = <file or folder>")
	}
	if s.frames == nil {
		f, err := loadFrames(env.Path(src), o.Float("fps", 12))
		if err != nil {
			return err
		}
		s.frames = f
	}

	if hf := o.Float("height", 0.35); hf > 0 {
		s.dh = s.h * math.Min(hf, 0.95)
		s.dw = s.dh * float64(s.frames.w) / float64(s.frames.h)
	} else {
		k := o.Float("scale", 1) * env.Scale
		s.dw, s.dh = float64(s.frames.w)*k, float64(s.frames.h)*k
	}
	if s.dw > s.w*0.95 { // very wide art: fit width instead
		k := s.w * 0.95 / s.dw
		s.dw, s.dh = s.dw*k, s.dh*k
	}

	s.motion = strings.ToLower(o.String("motion", "bounce"))
	switch s.motion {
	case "bounce", "drift", "float", "wander":
	default:
		return fmt.Errorf("sprite: unknown motion %q (bounce, drift, float, wander)", s.motion)
	}
	s.speed = o.Float("speed", 160) * env.Scale
	s.flip = o.Bool("flip", true)
	s.spin = o.Float("spin", 0)
	s.sway = o.Float("sway", 0)
	s.gifRate = o.Float("gif_speed", 1)
	if t := o.Float("trail", 0); t > 0 {
		if s.trail != nil {
			s.trail.dispose()
		}
		s.trail = newFader(env.W, env.H, t)
	}

	n := clampInt(o.Int("count", 1), 1, 50)
	s.actors = make([]*actor, n)
	for i := range s.actors {
		r := env.Rand
		a := r.Float64() * 2 * math.Pi
		s.actors[i] = &actor{
			x: r.Float64() * (s.w - s.dw), y: r.Float64() * (s.h - s.dh),
			vx: math.Cos(a) * s.speed, vy: math.Sin(a) * s.speed,
			phase: r.Float64() * 100, heading: a, facing: 1,
			frame: r.IntN(len(s.frames.imgs)),
		}
	}
	return nil
}

func (s *sprite) Update(dt float64) {
	maxX, maxY := s.w-s.dw, s.h-s.dh
	for _, a := range s.actors {
		a.t += dt
		px := a.x

		switch s.motion {
		case "bounce":
			a.x += a.vx * dt
			a.y += a.vy * dt
		case "wander":
			// Steer slowly toward a heading that itself drifts.
			a.heading += (s.env.Rand.Float64()*2 - 1) * 1.2 * dt
			a.vx += (math.Cos(a.heading)*s.speed - a.vx) * dt * 0.8
			a.vy += (math.Sin(a.heading)*s.speed - a.vy) * dt * 0.8
			a.x += a.vx * dt
			a.y += a.vy * dt
		case "drift", "float":
			// Lissajous path with incommensurate frequencies: covers the whole
			// screen over time and never exactly repeats.
			k := s.speed / 160
			t := (a.t + a.phase) * k
			a.x = maxX * (0.5 + 0.5*math.Sin(t*0.131))
			a.y = maxY * (0.5 + 0.5*math.Sin(t*0.097+1.3))
			if s.motion == "float" {
				a.y += math.Sin(t*1.1) * s.dh * 0.04
			}
		}

		if s.motion == "bounce" || s.motion == "wander" {
			if a.x < 0 {
				a.x, a.vx = -a.x, math.Abs(a.vx)
				a.heading = math.Pi - a.heading
			} else if a.x > maxX {
				a.x, a.vx = 2*maxX-a.x, -math.Abs(a.vx)
				a.heading = math.Pi - a.heading
			}
			if a.y < 0 {
				a.y, a.vy = -a.y, math.Abs(a.vy)
				a.heading = -a.heading
			} else if a.y > maxY {
				a.y, a.vy = 2*maxY-a.y, -math.Abs(a.vy)
				a.heading = -a.heading
			}
		}
		if dx := a.x - px; math.Abs(dx) > 0.01*dt*s.speed {
			a.facing = math.Copysign(1, dx)
		}

		if n := len(s.frames.imgs); n > 1 {
			a.frameT += dt * s.gifRate
			for a.frameT >= s.frames.delays[a.frame] {
				a.frameT -= s.frames.delays[a.frame]
				a.frame = (a.frame + 1) % n
			}
		}
	}
	if s.trail != nil {
		s.trail.fade(dt)
		for _, a := range s.actors {
			s.drawActor(s.trail.img, a)
		}
	}
}

func (s *sprite) drawActor(dst *ebiten.Image, a *actor) {
	img := s.frames.imgs[a.frame]
	op := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
	fw, fh := float64(s.frames.w), float64(s.frames.h)
	op.GeoM.Translate(-fw/2, -fh/2) // transform around the center
	sx := s.dw / fw
	if s.flip && a.facing < 0 {
		sx = -sx
	}
	op.GeoM.Scale(sx, s.dh/fh)
	angle := s.spin * a.t
	if s.sway != 0 {
		angle += s.sway * math.Sin((a.t+a.phase)*0.9)
	}
	if angle != 0 {
		op.GeoM.Rotate(angle * math.Pi / 180)
	}
	op.GeoM.Translate(a.x+s.dw/2, a.y+s.dh/2)
	dst.DrawImage(img, op)
}

func (s *sprite) Draw(dst *ebiten.Image) {
	if s.trail != nil {
		dst.DrawImage(s.trail.img, nil)
		return
	}
	for _, a := range s.actors {
		s.drawActor(dst, a)
	}
}

// loadFrames reads a still image, an animated GIF, or a folder of PNG frames.
func loadFrames(path string, fps float64) (*spriteFrames, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return loadFrameDir(path, fps)
	}
	if strings.EqualFold(filepath.Ext(path), ".gif") {
		return loadGIF(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	b := img.Bounds()
	return &spriteFrames{imgs: []*ebiten.Image{ebiten.NewImageFromImage(img)}, delays: []float64{1}, w: b.Dx(), h: b.Dy()}, nil
}

func loadFrameDir(dir string, fps float64) (*spriteFrames, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	if len(files) == 0 {
		return nil, fmt.Errorf("%s: no .png frames found", dir)
	}
	sort.Strings(files)
	if fps <= 0 {
		fps = 12
	}
	out := &spriteFrames{}
	for _, p := range files {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if out.w == 0 {
			out.w, out.h = img.Bounds().Dx(), img.Bounds().Dy()
		}
		out.imgs = append(out.imgs, ebiten.NewImageFromImage(img))
		out.delays = append(out.delays, 1/fps)
	}
	return out, nil
}

// loadGIF composites every GIF frame (honoring disposal modes) into full
// frames, so partial-frame GIFs animate correctly.
func loadGIF(path string) (*spriteFrames, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	g, err := gif.DecodeAll(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	bounds := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	if bounds.Empty() {
		for _, fr := range g.Image {
			bounds = bounds.Union(fr.Bounds())
		}
	}
	canvas := image.NewRGBA(bounds)
	out := &spriteFrames{w: bounds.Dx(), h: bounds.Dy()}
	for i, fr := range g.Image {
		var saved *image.RGBA
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		if disposal == gif.DisposalPrevious {
			saved = cloneRGBA(canvas)
		}
		draw.Draw(canvas, fr.Bounds(), fr, fr.Bounds().Min, draw.Over)
		out.imgs = append(out.imgs, ebiten.NewImageFromImage(cloneRGBA(canvas)))

		delay := 10 * time.Millisecond * 10 // GIF default when delay is 0
		if i < len(g.Delay) && g.Delay[i] > 1 {
			delay = time.Duration(g.Delay[i]) * 10 * time.Millisecond
		}
		out.delays = append(out.delays, delay.Seconds())

		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, fr.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = saved
		}
	}
	return out, nil
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}
