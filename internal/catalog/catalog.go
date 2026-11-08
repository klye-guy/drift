// Package catalog knows which savers exist and how they are configured. It
// has no graphics dependencies, so the lightweight `drift` daemon/CLI can use
// it without loading a GPU stack.
//
// Two levels:
//
//   - A Kind is code: a saver implementation compiled into drift-render
//     (see internal/savers), or the special "video" kind handled by mpv.
//
//   - A Def is a named, configured instance of a Kind. Every built-in kind
//     that needs no parameters gets a Def with its own name automatically.
//     Users add more by dropping a .conf file in a savers directory:
//
//     ~/.config/drift/savers/mychar.conf
//     kind   = sprite
//     image  = mychar.png
//
//     So a moving picture, a looped video, or a variant of any built-in
//     (kind = mystify, polygons = 4) needs no recompile at all.
package catalog

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"archavist.com/drift/internal/conf"
)

// Kind describes a saver implementation.
type Kind struct {
	Name        string
	Description string
	NeedsConfig bool // useless without a .conf (sprite needs an image)
	External    bool // run by an outside program rather than drift-render
}

// Video is the one kind drift handles outside the renderer: mpv plays it.
//
//	kind     = video
//	file     = ~/Videos/loop.mp4   # a file, or a folder to shuffle through
//	audio    = false
//	shuffle  = true                # folders only
//	mpv_args =                     # extra mpv flags, space separated
var Video = Kind{
	Name:        "video",
	Description: "Loop your own video file or folder of videos (via mpv)",
	NeedsConfig: true,
	External:    true,
}

// Def is a named, configured saver the user can select.
type Def struct {
	Name   string
	Kind   Kind
	Opts   conf.Section
	Dir    string // directory of the defining .conf file ("" for built-ins)
	Source string // defining file, or "built-in"
}

// Description is the def's own description= option, or its kind's.
func (d Def) Description() string { return d.Opts.String("description", d.Kind.Description) }

// Path resolves an asset path from the def's options: ~ is expanded and
// relative paths are taken relative to the .conf file's directory.
func (d Def) Path(p string) string { return ResolvePath(d.Dir, p) }

// ResolvePath expands ~ and makes p relative to dir when it isn't absolute.
func ResolvePath(dir, p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) && dir != "" {
		p = filepath.Join(dir, p)
	}
	return p
}

// Catalog is every selectable Def, keyed by lowercase name.
type Catalog map[string]Def

// Names returns the def names in sorted order.
func (c Catalog) Names() []string {
	out := make([]string, 0, len(c))
	for n := range c {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Load builds the catalog from the available kinds: one Def per
// parameterless kind, then every *.conf in dirs (later dirs override earlier
// ones by file name). global supplies [kind] sections from drift.conf as
// per-kind defaults. Bad files come back as warnings, not failures.
func Load(global conf.File, kinds []Kind, dirs ...string) (Catalog, []error) {
	byName := map[string]Kind{}
	for _, k := range kinds {
		byName[k.Name] = k
	}
	cat := Catalog{}
	var warns []error

	for _, k := range kinds {
		if k.NeedsConfig || k.External {
			continue
		}
		cat[k.Name] = Def{Name: k.Name, Kind: k, Opts: global.Section(k.Name).With(nil), Source: "built-in"}
	}

	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.conf"))
		sort.Strings(files)
		for _, f := range files {
			sec, err := conf.Load(f)
			if err != nil {
				warns = append(warns, err)
				continue
			}
			opts := sec.Section("")
			name := strings.ToLower(strings.TrimSuffix(filepath.Base(f), ".conf"))
			kn := strings.ToLower(opts.String("kind", ""))
			k, ok := byName[kn]
			if !ok {
				warns = append(warns, fmt.Errorf("%s: unknown kind %q (have: %s)", f, kn, names(kinds)))
				continue
			}
			cat[name] = Def{
				Name:   name,
				Kind:   k,
				Opts:   global.Section(k.Name).With(opts),
				Dir:    filepath.Dir(f),
				Source: f,
			}
		}
	}
	return cat, warns
}

func names(kinds []Kind) string {
	var n []string
	for _, k := range kinds {
		n = append(n, k.Name)
	}
	sort.Strings(n)
	return strings.Join(n, ", ")
}

// FormatKinds writes kinds as tab-separated lines: name, flags, description.
// drift-render prints this so the graphics-free drift binary can learn which
// kinds were compiled in.
func FormatKinds(kinds []Kind) string {
	var b strings.Builder
	for _, k := range kinds {
		var flags []string
		if k.NeedsConfig {
			flags = append(flags, "needs-config")
		}
		if k.External {
			flags = append(flags, "external")
		}
		if len(flags) == 0 {
			flags = append(flags, "-")
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\n", k.Name, strings.Join(flags, ","), k.Description)
	}
	return b.String()
}

// QueryRenderer runs `<renderer> kinds` and parses its output.
func QueryRenderer(renderer string) ([]Kind, error) {
	var stderr bytes.Buffer
	cmd := exec.Command(renderer, "kinds")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		return nil, fmt.Errorf("%s kinds: %v %s", renderer, err, msg)
	}
	return ParseKinds(out)
}

// ParseKinds reads the output of FormatKinds.
func ParseKinds(b []byte) ([]Kind, error) {
	var kinds []Kind
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		k := Kind{Name: parts[0], Description: parts[2]}
		k.NeedsConfig = strings.Contains(parts[1], "needs-config")
		k.External = strings.Contains(parts[1], "external")
		kinds = append(kinds, k)
	}
	return kinds, sc.Err()
}
