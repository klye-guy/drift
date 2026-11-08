package savers

import (
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"archavist.com/drift/internal/saver"
)

// starfield: flying forward through space. The vanishing point drifts in a
// slow loop instead of sitting at screen center, so no pixel sees the same
// brightness for long.
//
// Options:
//
//	stars   = 600   number of stars
//	speed   = 0.45  forward speed (depth units per second)
//	streaks = true  draw motion streaks instead of dots
//	tint    = 0     0 = white stars; otherwise hue degrees for a faint tint
func init() {
	saver.Register(saver.Kind{
		Name:        "starfield",
		Description: "Fly through a starfield with a drifting vanishing point",
		New:         func() saver.Saver { return &starfield{} },
	})
}

type star struct{ x, y, z float64 }

type starfield struct {
	env     saver.Env
	w, h    float64
	stars   []star
	speed   float64
	streaks bool
	tint    float64
	clock   float64
}

func (s *starfield) Setup(env saver.Env) error {
	s.env = env
	s.w, s.h = float64(env.W), float64(env.H)
	s.speed = env.Opts.Float("speed", 0.45)
	s.streaks = env.Opts.Bool("streaks", true)
	s.tint = env.Opts.Float("tint", 0)
	n := clampInt(env.Opts.Int("stars", 600), 10, 20000)
	s.stars = make([]star, n)
	for i := range s.stars {
		s.respawn(&s.stars[i], env.Rand.Float64())
	}
	return nil
}

// respawn places a star at depth z (0..1 of the far range) somewhere that
// projects inside the screen, so few stars are wasted off-screen.
func (s *starfield) respawn(st *star, z float64) {
	half := 0.5 / s.fov() * 1.15
	st.x = (s.env.Rand.Float64()*2 - 1) * s.w * half
	st.y = (s.env.Rand.Float64()*2 - 1) * s.h * half
	st.z = 0.05 + z*0.95
}

func (s *starfield) fov() float64 { return math.Max(s.w, s.h) * 0.5 }

// streakTime is how many seconds of travel a motion streak shows.
const streakTime = 0.07

// center returns the drifting vanishing point.
func (s *starfield) center() (float64, float64) {
	return s.w/2 + s.w*0.18*math.Sin(s.clock*0.071),
		s.h/2 + s.h*0.18*math.Sin(s.clock*0.053+1)
}

func (s *starfield) Update(dt float64) {
	s.clock += dt
	for i := range s.stars {
		st := &s.stars[i]
		st.z -= s.speed * dt
		if st.z <= 0.02 {
			s.respawn(st, 1)
		}
	}
}

func (s *starfield) Draw(dst *ebiten.Image) {
	cx, cy := s.center()
	fov := s.fov()
	for i := range s.stars {
		st := &s.stars[i]
		x, y := cx+st.x/st.z*fov, cy+st.y/st.z*fov
		if x < -50 || y < -50 || x > s.w+50 || y > s.h+50 {
			s.respawn(st, 1)
			continue
		}
		b := math.Min(1, (1-st.z)*1.3)
		c := color.RGBA{uint8(255 * b), uint8(255 * b), uint8(255 * b), 255}
		if s.tint != 0 {
			c = scaleColor(hsv(s.tint, 0.35, 1), b)
		}
		r := float32(max((1-st.z)*2.2, 0.6) * s.env.Scale)
		if s.streaks {
			// Tail = where the star was streakTime ago (farther away).
			tz := st.z + s.speed*streakTime
			tx, ty := cx+st.x/tz*fov, cy+st.y/tz*fov
			vector.StrokeLine(dst, float32(tx), float32(ty), float32(x), float32(y), r, scaleColor(c, 0.6), true)
		}
		vector.FillCircle(dst, float32(x), float32(y), r, c, true)
	}
}
