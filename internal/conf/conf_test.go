package conf

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMergeAndGetters(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.conf")
	b := filepath.Join(dir, "b.conf")
	os.WriteFile(a, []byte("# system\nidle_minutes = 5\nsavers = mystify, pipes\n\n[marquee]\ntext = \"hello world\"\n"), 0o644)
	os.WriteFile(b, []byte("idle_minutes = 2.5\nBrightness = 0.8\n[MARQUEE]\nsize=40\n"), 0o644)

	f := File{}
	if err := f.Merge(a); err != nil {
		t.Fatal(err)
	}
	if err := f.Merge(b); err != nil {
		t.Fatal(err)
	}
	g := f.Section("")
	if g.Minutes("idle_minutes", 0) != 150*time.Second {
		t.Errorf("idle_minutes override = %v", g.Minutes("idle_minutes", 0))
	}
	if g.Float("brightness", 1) != 0.8 {
		t.Error("keys should be case-insensitive")
	}
	if l := g.List("savers"); len(l) != 2 || l[1] != "pipes" {
		t.Errorf("list = %v", l)
	}
	m := f.Section("marquee")
	if m.String("text", "") != "hello world" || m.Int("size", 0) != 40 {
		t.Errorf("marquee = %v", m)
	}
	if Section(nil).Int("missing", 7) != 7 {
		t.Error("nil section should return defaults")
	}
}

func TestInlineComments(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.conf")
	os.WriteFile(p, []byte("image = courier        # folder of frames\n"+
		"color = #ff8800\n"+
		"text = \"say # hi\"   # quoted keeps it\n"+
		"fps = 10.5\t; tab comment\n"), 0o644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	g := f.Section("")
	for k, want := range map[string]string{"image": "courier", "color": "#ff8800", "text": "say # hi", "fps": "10.5"} {
		if got := g.String(k, ""); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestBadLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.conf")
	os.WriteFile(p, []byte("this is not a key value line\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Error("expected an error with a line number")
	}
}
