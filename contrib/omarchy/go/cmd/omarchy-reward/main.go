// omarchy-reward: score the smoke test JSON (maintenance/smoke-test.sh --json) from 0 to 100.
//
//	omarchy-reward [smoke.json]   (stdin if no file)
//
// stdout: JSON; stderr: human summary. Exit 0 if stable, 1 if not, 3 on bad input.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"omarchy/internal/reward"
)

func main() {
	var raw []byte
	var err error
	if len(os.Args) > 1 {
		raw, err = os.ReadFile(os.Args[1])
	} else {
		raw, err = io.ReadAll(os.Stdin)
	}
	var in struct {
		Checks *[]reward.Check `json:"checks"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &in)
	}
	if err == nil && in.Checks == nil {
		err = errors.New(`missing "checks"`)
	}
	if err != nil {
		b, _ := json.Marshal(map[string]any{"error": err.Error(), "stable": false, "score": 0})
		fmt.Println(string(b))
		fmt.Fprintf(os.Stderr, "reward: entrada invalida: %v\n", err)
		os.Exit(3)
	}
	r := reward.Score(*in.Checks)
	b, _ := json.MarshalIndent(r, "", " ")
	fmt.Println(string(b))
	verdict, blockers := "NO estable", strings.Join(r.Blockers, ", ")
	if r.Stable {
		verdict = "ESTABLE"
	}
	if blockers == "" {
		blockers = "ninguno"
	}
	fmt.Fprintf(os.Stderr, "reward: %v/100 (umbral %v); bloqueantes: %s; %s\n", r.Score, reward.Threshold, blockers, verdict)
	if !r.Stable {
		os.Exit(1)
	}
}
