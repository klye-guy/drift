// Package paths says where drift reads configuration and saver definitions,
// and finds the drift-render binary.
package paths

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"archavist.com/drift/internal/conf"
)

// UserDir is ~/.config/drift (honoring XDG_CONFIG_HOME).
func UserDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "drift")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "drift")
}

// ConfFiles lists drift.conf locations; later files override earlier keys.
func ConfFiles() []string {
	return []string{"/etc/drift/drift.conf", filepath.Join(UserDir(), "drift.conf")}
}

// SaverDirs lists saver-definition directories; later wins by file name.
func SaverDirs() []string {
	return []string{"/usr/share/drift/savers", "/etc/drift/savers", filepath.Join(UserDir(), "savers")}
}

// LoadConf merges every drift.conf that exists.
func LoadConf() (conf.File, error) {
	f := conf.File{}
	for _, p := range ConfFiles() {
		if err := f.Merge(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return f, nil
}

// Renderer returns the drift-render binary: next to this executable first
// (so a build directory works without installing), then $PATH.
func Renderer() string {
	if self, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(self), "drift-render")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("drift-render"); err == nil {
		return p
	}
	return "drift-render"
}
