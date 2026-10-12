// omarchy-hwcheck: deterministic validator of drivers, updates and conflicts (see internal/hwcheck).
package main

import (
	"os"

	"omarchy/internal/hwcheck"
)

func main() {
	os.Exit(hwcheck.Main(os.Args[1:], nil, os.Stdout, os.Stderr))
}
