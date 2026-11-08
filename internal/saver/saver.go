// Package saver defines the contract every renderer-drawn screensaver
// implements, and the registry the built-in savers add themselves to.
//
// To add a new graphic, create one file in internal/savers:
//
//	func init() {
//	    saver.Register(saver.Kind{
//	        Name:        "aquarium",
//	        Description: "Fish swimming back and forth",
//	        New:         func() saver.Saver { return &aquarium{} },
//	    })
//	}
//
// and implement Setup/Update/Draw. Nothing else in drift changes: the
// daemon, `drift list`, playlists and per-kind config sections pick it up
// automatically.
package saver

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"

	"archavist.com/drift/internal/catalog"
	"archavist.com/drift/internal/conf"
)

// Saver is one running screensaver graphic.
//
// Lifecycle: Setup once (and again if the screen size changes), then
// Update/Draw every frame until the user wakes the screen. The screen is
// cleared to black before each Draw, so savers that want trails keep their
// own offscreen canvas.
type Saver interface {
	// Setup prepares the saver for a w×h pixel screen.
	Setup(env Env) error
	// Update advances the animation by dt seconds.
	Update(dt float64)
	// Draw renders the current frame.
	Draw(dst *ebiten.Image)
}

// Env is everything a saver needs to know about where it is running.
type Env struct {
	W, H  int          // drawable size in physical pixels
	Scale float64      // device scale factor (1 on most screens, 2 on HiDPI)
	Rand  *rand.Rand   // seeded per run; use it instead of the global source
	Opts  conf.Section // options from the global [kind] section and the .conf file
	Dir   string       // directory of the .conf file, for resolving asset paths
}

// Path resolves an asset path from Opts (relative to the .conf file, ~ ok).
func (e Env) Path(p string) string { return catalog.ResolvePath(e.Dir, p) }

// Kind is a saver implementation to register.
type Kind struct {
	Name        string
	Description string
	New         func() Saver
	// NeedsConfig marks kinds that are useless without a .conf file
	// (sprite needs an image). No default saver is listed for them.
	NeedsConfig bool
}

var kinds = map[string]Kind{}

// Register adds a Kind. Call it from init(). Panics on duplicates so a name
// clash is caught on first run rather than silently shadowed.
func Register(k Kind) {
	k.Name = strings.ToLower(k.Name)
	if _, dup := kinds[k.Name]; dup || k.Name == catalog.Video.Name {
		panic("saver: duplicate kind " + k.Name)
	}
	if k.New == nil {
		panic("saver: kind " + k.Name + " has no constructor")
	}
	kinds[k.Name] = k
}

// Kinds returns every kind drift knows about — the registered savers plus
// the external video kind — sorted by name.
func Kinds() []catalog.Kind {
	out := []catalog.Kind{catalog.Video}
	for _, k := range kinds {
		out = append(out, catalog.Kind{Name: k.Name, Description: k.Description, NeedsConfig: k.NeedsConfig})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// New constructs the saver for def.
func New(def catalog.Def) (Saver, error) {
	k, ok := kinds[def.Kind.Name]
	if !ok {
		return nil, fmt.Errorf("saver %q (kind %s) is not drawn by drift-render", def.Name, def.Kind.Name)
	}
	return k.New(), nil
}
