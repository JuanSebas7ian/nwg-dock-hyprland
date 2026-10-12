package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, input string, args ...string) (map[string]any, int) {
	bin := filepath.Join(t.TempDir(), "omarchy-reward")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(input)
	out, _ := cmd.Output()
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m, cmd.ProcessState.ExitCode()
}

func TestCLI(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(p, []byte("{not json"), 0o644)
	if m, code := run(t, "", p); code != 3 || m["stable"] != false {
		t.Fatal(code, m)
	}
	if _, code := run(t, `{"x": 1}`); code != 3 {
		t.Fatal(code)
	}
	if m, code := run(t, `{"checks": [{"id": "TRIM01", "status": "PASS"}]}`); code != 1 || m["stable"] != false {
		t.Fatal(code, m)
	}
}
