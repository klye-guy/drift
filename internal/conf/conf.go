// Package conf reads drift's flat INI-style configuration files.
//
// Format:
//
//	# comment
//	key = value
//	[section]
//	key = value
//
// Keys before the first [section] belong to the global section "".
// Later files loaded into the same File override earlier keys.
package conf

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Section is one [section] of key/value pairs. A nil Section is valid and
// returns defaults for every lookup.
type Section map[string]string

// File is a parsed config: section name -> keys.
type File map[string]Section

// Load parses path into a new File.
func Load(path string) (File, error) {
	f := File{}
	return f, f.Merge(path)
}

// Merge parses path and overlays its keys onto f.
func (f File) Merge(path string) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()

	section := ""
	sc := bufio.NewScanner(fh)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			if !strings.HasSuffix(line, "]") {
				return fmt.Errorf("%s:%d: unterminated section header", path, n)
			}
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected key = value", path, n)
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = unquote(stripComment(strings.TrimSpace(v)))
		if f[section] == nil {
			f[section] = Section{}
		}
		f[section][k] = v
	}
	return sc.Err()
}

// stripComment removes a trailing " # comment" or " ; comment" from an
// unquoted value. A # or ; with no whitespace before it is kept, so values
// like "#ff8800" or "a;b" survive; quote a value to keep a " #" inside it.
func stripComment(v string) string {
	if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[:end+2]
		}
		return v
	}
	for i := 1; i < len(v); i++ {
		if (v[i] == '#' || v[i] == ';') && (v[i-1] == ' ' || v[i-1] == '\t') {
			return strings.TrimSpace(v[:i])
		}
	}
	return v
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// Section returns the named section (possibly nil).
func (f File) Section(name string) Section { return f[strings.ToLower(name)] }

// Has reports whether key is set.
func (s Section) Has(key string) bool { _, ok := s[key]; return ok }

// String returns key or def.
func (s Section) String(key, def string) string {
	if v, ok := s[key]; ok && v != "" {
		return v
	}
	return def
}

// Float returns key parsed as float64, or def if unset or invalid.
func (s Section) Float(key string, def float64) float64 {
	if v, ok := s[key]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// Int returns key parsed as int, or def if unset or invalid.
func (s Section) Int(key string, def int) int {
	if v, ok := s[key]; ok {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

// Bool returns key parsed as a boolean (true/yes/on/1), or def.
func (s Section) Bool(key string, def bool) bool {
	switch strings.ToLower(s[key]) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// Minutes returns key, given in (fractional) minutes, as a Duration.
func (s Section) Minutes(key string, def float64) time.Duration {
	return time.Duration(s.Float(key, def) * float64(time.Minute))
}

// List returns key split on commas with blanks removed.
func (s Section) List(key string) []string {
	var out []string
	for _, p := range strings.Split(s[key], ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// With returns a copy of s with o's keys layered on top.
func (s Section) With(o Section) Section {
	out := Section{}
	for k, v := range s {
		out[k] = v
	}
	for k, v := range o {
		out[k] = v
	}
	return out
}
