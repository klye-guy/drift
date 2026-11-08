// Package savers holds drift's built-in screensaver graphics.
//
// Each file is one saver and registers itself from init(). To add a new one,
// copy a small file such as starfield.go, rename the type and the Register
// call, and rebuild — no other file needs to change. The helpers below cover
// what most savers want: colors, bouncing points, and a fading trail canvas.
package savers

import (
	"image/color"
	"math"
	"math/rand/v2"

	"github.com/hajimehoshi/ebiten/v2"
)

// hsv converts hue (degrees, any range), saturation and value (0..1) to RGBA.
func hsv(h, s, v float64) color.RGBA {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return color.RGBA{uint8((r + m) * 255), uint8((g + m) * 255), uint8((b + m) * 255), 255}
}

// scaleColor multiplies a color's channels by f (0..1).
func scaleColor(c color.RGBA, f float64) color.RGBA {
	return color.RGBA{uint8(float64(c.R) * f), uint8(float64(c.G) * f), uint8(float64(c.B) * f), c.A}
}

// bouncer is a point moving at constant speed that reflects off the screen
// edges — the building block of Mystify-style savers.
type bouncer struct{ x, y, vx, vy float64 }

func newBouncer(r *rand.Rand, w, h, speed float64) bouncer {
	a := r.Float64() * 2 * math.Pi
	s := speed * (0.6 + 0.8*r.Float64())
	return bouncer{x: r.Float64() * w, y: r.Float64() * h, vx: math.Cos(a) * s, vy: math.Sin(a) * s}
}

func (b *bouncer) step(dt, w, h float64) {
	b.x += b.vx * dt
	b.y += b.vy * dt
	if b.x < 0 {
		b.x, b.vx = -b.x, math.Abs(b.vx)
	} else if b.x > w {
		b.x, b.vx = 2*w-b.x, -math.Abs(b.vx)
	}
	if b.y < 0 {
		b.y, b.vy = -b.y, math.Abs(b.vy)
	} else if b.y > h {
		b.y, b.vy = 2*h-b.y, -math.Abs(b.vy)
	}
}

// fader is a persistent canvas that darkens a little every frame, giving
// anything drawn on it a fading trail. Rate is the fraction of brightness
// kept per second (0.2 = trails fade to 20% after one second).
type fader struct {
	img  *ebiten.Image
	rate float64
	acc  float64
}

func newFader(w, h int, keepPerSecond float64) *fader {
	return &fader{img: ebiten.NewImage(w, h), rate: keepPerSecond}
}

// pixel is a 1×1 white image stretched to cover the canvas when fading.
var pixel = func() *ebiten.Image {
	p := ebiten.NewImage(1, 1)
	p.Fill(color.White)
	return p
}()

// subtract computes dst - src on RGB and leaves alpha alone.
var subtract = ebiten.Blend{
	BlendFactorSourceRGB:        ebiten.BlendFactorOne,
	BlendFactorSourceAlpha:      ebiten.BlendFactorZero,
	BlendFactorDestinationRGB:   ebiten.BlendFactorOne,
	BlendFactorDestinationAlpha: ebiten.BlendFactorOne,
	BlendOperationRGB:           ebiten.BlendOperationReverseSubtract,
	BlendOperationAlpha:         ebiten.BlendOperationAdd,
}

// fade darkens the canvas for dt seconds of elapsed time.
//
// Two passes: a proportional darken (source-over translucent black), then a
// subtraction of one 8-bit step. The second pass matters on OLED: with only
// the first, 8-bit rounding leaves faint pixels that never reach true black,
// which is exactly the static residue a burn-in saver must not leave.
func (f *fader) fade(dt float64) {
	keep := math.Pow(f.rate, dt)
	w, h := f.img.Bounds().Dx(), f.img.Bounds().Dy()

	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(float64(w), float64(h))
	op.ColorScale.Scale(0, 0, 0, float32(1-keep))
	f.img.DrawImage(pixel, op)

	// One step every 1/8 s is invisible on bright trails but clears the
	// rounding residue (values that are stuck below ~8) within a second or two.
	f.acc += dt
	if f.acc >= 0.125 {
		f.acc = 0
		op = &ebiten.DrawImageOptions{Blend: subtract}
		op.GeoM.Scale(float64(w), float64(h))
		op.ColorScale.Scale(1.0/255, 1.0/255, 1.0/255, 1)
		f.img.DrawImage(pixel, op)
	}
}

func (f *fader) dispose() { f.img.Deallocate() }
