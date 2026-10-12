// Package hwcheck is omarchy-hwcheck: a deterministic validator of drivers,
// updates and conflicts. It only reads the system (no root): the same state
// always gives the same report (everything sorted, no timestamps, commands
// run with LC_ALL=C). Only `scan --online` depends on the network.
package hwcheck

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// System is everything the checks read; tests give it fixed data.
type System interface {
	Read(path string) (string, bool)
	ListDir(path string) []string
	Exists(path string) bool
	Readlink(path string) (string, bool)
	WalkFiles(top string, suffixes ...string) []string
	Run(timeout time.Duration, cmd ...string) (int, string)
	Home() string
}

// RealSystem reads the machine; command results are cached per invocation.
type RealSystem struct {
	home  string
	cache map[string][2]any
}

func NewRealSystem() *RealSystem {
	h, _ := os.UserHomeDir()
	return &RealSystem{home: h, cache: map[string][2]any{}}
}

func (s *RealSystem) Home() string { return s.home }

func (s *RealSystem) Read(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.ToValidUTF8(string(b), "�"), true
}

func (s *RealSystem) ListDir(path string) []string {
	ents, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func (s *RealSystem) Exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func (s *RealSystem) Readlink(path string) (string, bool) {
	l, err := os.Readlink(path)
	return l, err == nil
}

func hasSuffix(name string, suffixes []string) bool {
	for _, x := range suffixes {
		if strings.HasSuffix(name, x) {
			return true
		}
	}
	return false
}

func (s *RealSystem) WalkFiles(top string, suffixes ...string) []string {
	var found []string
	_ = filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != top {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if hasSuffix(d.Name(), suffixes) {
			found = append(found, p)
		}
		return nil
	})
	sort.Strings(found)
	return found
}

// Run returns (exit code, stdout); 127 if the command does not exist, 124 on timeout.
func (s *RealSystem) Run(timeout time.Duration, cmd ...string) (int, string) {
	key := strings.Join(cmd, "\x00")
	if r, ok := s.cache[key]; ok {
		return r[0].(int), r[1].(string)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Env = append(os.Environ(), "LC_ALL=C", "LANG=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=")
	out, err := c.Output()
	code := 0
	switch {
	case ctx.Err() != nil:
		code, out = 124, nil
	case err != nil:
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code, out = 127, nil
		}
	}
	res := strings.ToValidUTF8(string(out), "�")
	s.cache[key] = [2]any{code, res}
	return code, res
}
