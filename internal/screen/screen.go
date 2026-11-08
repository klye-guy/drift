// Package screen hosts a Saver in an Ebitengine window: fullscreen on one
// monitor for real use, or windowed for previewing while you build a saver.
package screen

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"math/rand/v2"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"archavist.com/drift/internal/catalog"
	"archavist.com/drift/internal/saver"
)

// Options controls how the saver window behaves.
type Options struct {
	Monitor    int     // index into the monitor list (0 = primary)
	Preview    bool    // windowed, quits on Esc only
	Brightness float64 // 0..1 multiplier on everything drawn (OLED wear)
	MaxFPS     int     // frame cap; 0 = follow vsync

	// SnapshotPath, if set, writes the frame shown after SnapshotAfter
	// seconds to a PNG and exits. Used by `drift snapshot`.
	SnapshotPath  string
	SnapshotAfter float64
}

// Monitors returns the number of monitors the display server reports.
func Monitors() int {
	return len(ebiten.AppendMonitors(nil))
}

// MonitorInfo describes each monitor, one per line, for diagnostics.
func MonitorInfo() string {
	var out string
	for i, m := range ebiten.AppendMonitors(nil) {
		w, h := m.Size()
		out += fmt.Sprintf("%d\t%s\t%dx%d (logical)\tscale %.2f\n", i, m.Name(), w, h, m.DeviceScaleFactor())
	}
	return out
}

// Run shows def until the user provides input (or presses Esc in preview).
func Run(def catalog.Def, opt Options) error {
	s, err := saver.New(def)
	if err != nil {
		return err
	}
	if opt.Brightness <= 0 || opt.Brightness > 1 {
		opt.Brightness = 1
	}

	ebiten.SetWindowTitle("drift — " + def.Name)
	ebiten.SetRunnableOnUnfocused(true)
	if opt.MaxFPS > 0 {
		ebiten.SetTPS(opt.MaxFPS)
	}

	var target *ebiten.MonitorType
	if opt.Preview || opt.SnapshotPath != "" {
		ebiten.SetWindowSize(1280, 720)
		ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	} else {
		mons := ebiten.AppendMonitors(nil)
		if opt.Monitor < 0 || opt.Monitor >= len(mons) {
			return fmt.Errorf("monitor %d out of range (have %d)", opt.Monitor, len(mons))
		}
		target = mons[opt.Monitor]
		ebiten.SetMonitor(target)
		// Start as a borderless window the size of the target monitor. Once
		// it's up, pinToOutput takes it away from the window manager and
		// puts it exactly over that monitor. If that can't be done (no X11
		// or RandR), fall back to toolkit fullscreen.
		ebiten.SetWindowDecorated(false)
		ebiten.SetWindowSize(target.Size())
		ebiten.SetCursorMode(ebiten.CursorModeHidden)
		// Keep the saver above other windows even if the compositor refuses
		// to give a background-launched window focus.
		ebiten.SetWindowFloating(true)
	}

	g := &host{def: def, s: s, opt: opt, started: time.Now(), target: target,
		rng: rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(opt.Monitor)+1))}
	err = ebiten.RunGameWithOptions(g, &ebiten.RunGameOptions{InitUnfocused: false})
	if errors.Is(err, errWake) {
		return nil
	}
	return err
}

var errWake = errors.New("wake")

// wakeGrace ignores the burst of synthetic pointer events some compositors
// send when a fullscreen window maps.
const wakeGrace = 750 * time.Millisecond

// wakeDistance is how far (logical px) the pointer must travel to wake.
const wakeDistance = 12

type host struct {
	def     catalog.Def
	s       saver.Saver
	opt     Options
	rng     *rand.Rand
	started time.Time

	w, h    int
	ready   bool
	err     error
	last    time.Time
	canvas  *ebiten.Image // only used when dimming
	anchorX int
	anchorY int
	anchor  bool
	elapsed float64
	snapped bool

	// Monitor placement. Window managers (KWin especially) may ignore where
	// a new window asks to open and put it on the active screen, which
	// stacks every monitor's saver on one display.
	target    *ebiten.MonitorType
	pinning   bool
	pinned    chan error // result of pinToOutput
	fallback  time.Time  // when we fell back to toolkit fullscreen
	nextCheck time.Time
	moves     int
}

// placement pins the window to its monitor once it exists; if pinning is
// impossible it falls back to fullscreen plus fixPlacement.
func (g *host) placement() {
	if g.target == nil {
		return
	}
	if !g.pinning && g.ready && time.Since(g.started) > 200*time.Millisecond {
		g.pinning = true
		g.pinned = make(chan error, 1)
		name := g.target.Name()
		go func() { g.pinned <- pinToOutput(name) }()
	}
	if g.pinned != nil {
		select {
		case err := <-g.pinned:
			g.pinned = nil
			if err != nil {
				fmt.Fprintf(os.Stderr, "drift-render: can't pin window to %s (%v); using fullscreen\n", g.target.Name(), err)
				ebiten.SetFullscreen(true)
				g.fallback = time.Now()
			}
			// Remapping generates pointer events; don't treat them as a wake.
			g.started = time.Now().Add(-wakeGrace / 2)
			g.anchor = false
		default:
		}
	}
	if !g.fallback.IsZero() {
		g.fixPlacement()
	}
}

// placementWindow is how long after falling back we keep correcting placement.
const placementWindow = 6 * time.Second

func (g *host) fixPlacement() {
	if time.Since(g.fallback) > placementWindow || time.Now().Before(g.nextCheck) {
		return
	}
	g.nextCheck = time.Now().Add(150 * time.Millisecond)
	cur := ebiten.Monitor()
	if cur == nil || cur == g.target || g.moves >= 3 {
		return
	}
	g.moves++
	fmt.Fprintf(os.Stderr, "drift-render: window opened on %q, moving it to monitor %d (%q)\n",
		cur.Name(), g.opt.Monitor, g.target.Name())
	ebiten.SetMonitor(g.target)
	// Ebitengine caches the window's monitor for about a second; recheck
	// after that so one move isn't mistaken for a failure.
	g.nextCheck = time.Now().Add(1300 * time.Millisecond)
	g.started = time.Now().Add(-wakeGrace / 2)
	g.anchor = false
}

func (g *host) Update() error {
	if g.err != nil {
		return g.err
	}
	if g.snapped {
		return errWake
	}
	g.placement()
	if g.opt.SnapshotPath == "" && g.woke() {
		return errWake
	}
	if !g.ready {
		return nil // Layout hasn't told us the size yet
	}
	now := time.Now()
	dt := now.Sub(g.last).Seconds()
	g.last = now
	if dt > 0.1 { // after a stall, don't teleport
		dt = 0.1
	}
	g.s.Update(dt)
	g.elapsed += dt
	return nil
}

func (g *host) woke() bool {
	if g.opt.Preview {
		return inpututil.IsKeyJustPressed(ebiten.KeyEscape) || inpututil.IsKeyJustPressed(ebiten.KeyQ)
	}
	if time.Since(g.started) < wakeGrace || g.pinned != nil {
		return false // just started, or the window is being moved
	}
	if len(inpututil.AppendJustPressedKeys(nil)) > 0 {
		return true
	}
	for b := ebiten.MouseButton0; b <= ebiten.MouseButtonMax; b++ {
		if inpututil.IsMouseButtonJustPressed(b) {
			return true
		}
	}
	if wx, wy := ebiten.Wheel(); wx != 0 || wy != 0 {
		return true
	}
	if len(inpututil.AppendJustPressedTouchIDs(nil)) > 0 {
		return true
	}
	x, y := ebiten.CursorPosition()
	if !g.anchor {
		g.anchorX, g.anchorY, g.anchor = x, y, true
		return false
	}
	dx, dy := x-g.anchorX, y-g.anchorY
	return dx*dx+dy*dy > wakeDistance*wakeDistance
}

func (g *host) Draw(screen *ebiten.Image) {
	if !g.ready {
		return
	}
	if g.opt.Brightness >= 1 {
		g.s.Draw(screen)
	} else {
		g.canvas.Clear()
		g.s.Draw(g.canvas)
		op := &ebiten.DrawImageOptions{}
		op.ColorScale.Scale(float32(g.opt.Brightness), float32(g.opt.Brightness), float32(g.opt.Brightness), 1)
		screen.DrawImage(g.canvas, op)
	}
	if g.opt.SnapshotPath != "" && !g.snapped && g.elapsed >= g.opt.SnapshotAfter {
		g.snapped = true
		if err := writePNG(screen, g.opt.SnapshotPath); err != nil {
			g.err = err
		}
	}
}

func writePNG(img *ebiten.Image, path string) error {
	b := img.Bounds()
	rgba := image.NewRGBA(b)
	img.ReadPixels(rgba.Pix)
	for i := 3; i < len(rgba.Pix); i += 4 {
		rgba.Pix[i] = 255 // the window is opaque; save what is actually seen
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, rgba)
}

func (g *host) Layout(outW, outH int) (int, int) {
	scale := 1.0
	if m := ebiten.Monitor(); m != nil {
		scale = m.DeviceScaleFactor()
	}
	w, h := int(float64(outW)*scale), int(float64(outH)*scale)
	if w < 1 || h < 1 {
		return 1, 1
	}
	if w != g.w || h != g.h {
		g.w, g.h = w, h
		if g.canvas != nil {
			g.canvas.Deallocate()
		}
		g.canvas = ebiten.NewImage(w, h)
		env := saver.Env{W: w, H: h, Scale: scale, Rand: g.rng, Opts: g.def.Opts, Dir: g.def.Dir}
		if err := g.s.Setup(env); err != nil {
			g.err = fmt.Errorf("%s: %w", g.def.Name, err)
		}
		g.ready = g.err == nil
		g.last = time.Now()
	}
	return w, h
}
