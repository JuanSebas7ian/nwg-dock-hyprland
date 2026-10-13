package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestAppScopeWrapper(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, x := range names {
				if x == n {
					return "/usr/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	if w, a := scopeWrapper(has("uwsm-app", "systemd-run")), scopeArgs(has("uwsm-app", "systemd-run")); w != "/usr/bin/uwsm-app" || !reflect.DeepEqual(a, []string{"--"}) {
		t.Fatal(w, a)
	}
	if w, a := scopeWrapper(has("systemd-run")), scopeArgs(has("systemd-run")); w != "/usr/bin/systemd-run" || a[len(a)-1] != "--" || a[1] != "--scope" {
		t.Fatal(w, a)
	}
	if w, a := scopeWrapper(has()), scopeArgs(has()); w != "env" || a != nil {
		t.Fatal(w, a)
	}
}

func TestAppCommandKeepsOfficialBehaviourWithoutDnd(t *testing.T) {
	off := false
	saved := dnd
	dnd = &off
	defer func() { dnd = saved }()
	if c := appCommand("steam", "-silent"); !reflect.DeepEqual(c.Args, []string{"steam", "-silent"}) {
		t.Fatal(c.Args)
	}
	on := true
	dnd = &on
	if c := appCommand("steam"); c.Args[len(c.Args)-1] != "steam" || len(c.Args) < 3 {
		t.Fatal(c.Args)
	}
}
