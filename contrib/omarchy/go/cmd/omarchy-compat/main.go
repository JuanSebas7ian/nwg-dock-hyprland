// omarchy-compat: compatibility validator for driver updates (contrib/omarchy/drivers/PLAN.md).
//
//	omarchy-compat preflight [--from-file F] [--json]   will the pending updates break drivers or apps?
//	omarchy-compat post [--json]                        does everything work right now?
//	omarchy-compat hook                                 pacman PreTransaction hook: targets on stdin, never fails
//
// Output: "LEVEL ID text" per rule. Exit 0 all OK, 1 warnings, 2 something
// would break, 3 could not evaluate (checkupdates failed, bad usage).
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"omarchy/internal/compat"
	"omarchy/internal/cuda"
)

const usage = `uso: omarchy-compat preflight [--from-file FICHERO] [--json] | post [--json] | hook`

func println(s string) { fmt.Println(s) }

func preflight(args []string) int {
	var text *string
	if i := slices.Index(args, "--from-file"); i >= 0 {
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			fmt.Fprintln(os.Stderr, "uso: omarchy-compat preflight [--from-file FICHERO] [--json]")
			return 3
		}
		b, err := os.ReadFile(args[i+1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
		t := string(b)
		text = &t
	}
	return compat.Preflight(text, slices.Contains(args, "--json"), println, func(s string) { fmt.Fprintln(os.Stderr, s) })
}

func post(args []string) int {
	return compat.Post(slices.Contains(args, "--json"), cuda.Init, println)
}

// hook never fails and never takes more than 4 s: a slow or broken pacman just prints nothing.
func hook() int {
	done := make(chan []string, 1)
	go func() {
		defer func() { recover() }()
		var targets []string
		sc := bufio.NewScanner(io.LimitReader(os.Stdin, 1<<20))
		for sc.Scan() {
			targets = append(targets, sc.Text())
		}
		done <- compat.Hook(targets, compat.Run)
	}()
	select {
	case lines := <-done:
		for _, l := range lines {
			fmt.Println(l)
		}
	case <-time.After(4 * time.Second):
	}
	return 0
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(3)
	}
	switch os.Args[1] {
	case "preflight":
		os.Exit(preflight(os.Args[2:]))
	case "post":
		os.Exit(post(os.Args[2:]))
	case "hook":
		os.Exit(hook())
	}
	fmt.Fprintln(os.Stderr, usage)
	os.Exit(3)
}
