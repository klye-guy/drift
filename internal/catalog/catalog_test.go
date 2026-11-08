package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"archavist.com/drift/internal/conf"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("mychar.conf", "kind = sprite\nimage = art/mychar.png\nmotion = float\n")
	write("busy.conf", "kind = mystify\npolygons = 4\n")
	write("broken.conf", "kind = nope\n")
	write("clip.conf", "kind = video\nfile = ~/Videos/x.mp4\n")

	kinds := []Kind{{Name: "mystify"}, {Name: "pipes"}, {Name: "sprite", NeedsConfig: true}, Video}
	global := conf.File{"mystify": conf.Section{"polygons": "2", "echoes": "6"}}

	cat, warns := Load(global, kinds, dir)
	if len(warns) != 1 {
		t.Errorf("warnings = %v, want 1 (broken.conf)", warns)
	}
	for _, want := range []string{"mystify", "pipes", "mychar", "busy", "clip"} {
		if _, ok := cat[want]; !ok {
			t.Errorf("missing %q; have %v", want, cat.Names())
		}
	}
	if _, ok := cat["sprite"]; ok {
		t.Error("sprite needs a .conf and should not get a default def")
	}
	if _, ok := cat["video"]; ok {
		t.Error("video should not get a default def")
	}
	busy := cat["busy"]
	if busy.Opts.Int("polygons", 0) != 4 || busy.Opts.Int("echoes", 0) != 6 {
		t.Errorf("file options should layer over [mystify] defaults: %v", busy.Opts)
	}
	if got := cat["mychar"].Path("art/mychar.png"); got != filepath.Join(dir, "art/mychar.png") {
		t.Errorf("relative asset path = %s", got)
	}
	if !cat["clip"].Kind.External {
		t.Error("video defs must be external")
	}
}

func TestKindsRoundTrip(t *testing.T) {
	in := []Kind{{Name: "a", Description: "first one"}, {Name: "b", Description: "x\ty", NeedsConfig: true}, Video}
	text := FormatKinds(in)
	out, err := ParseKinds([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || !out[1].NeedsConfig || !out[2].External || out[1].Description != "x\ty" {
		t.Errorf("round trip = %+v", out)
	}
}
