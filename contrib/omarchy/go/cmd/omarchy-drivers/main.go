// omarchy-drivers: driver status for the Hardware widget, and the update
// actions it offers.
//
//	omarchy-drivers status [--force]  JSON for the Drivers tab
//	omarchy-drivers update            (in a terminal) compatibility check, then omarchy update
//	omarchy-drivers firmware          (in a terminal) fwupdmgr update
package main

import (
	"bufio"
	"fmt"
	"omarchy/internal/compat"
	"os"
	"os/exec"
	"strings"

	"omarchy/internal/drivers"
	"omarchy/internal/jsonout"
)

func run(name string, args ...string) int {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 127
	}
	return 0
}

func ask(q string) bool {
	fmt.Print(q + " [s/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "s" || line == "si" || line == "sí" || line == "y" || line == "yes"
}

// update: Arch does not support updating only some packages (a partial
// upgrade can leave the NVIDIA module built for the wrong kernel), so
// drivers are updated with the whole system, after the compatibility check.
func update() int {
	fmt.Println("== 1/3 Compatibility check (omarchy-compat preflight): what this update would change for the drivers")
	{
		code := compat.Preflight(nil, false, func(s string) { fmt.Println(s) }, func(s string) { fmt.Println(s) })
		switch code {
		case 0:
			fmt.Println("   OK: nothing in this update breaks the drivers.")
		case 2:
			fmt.Println("   WARNING: the check says this update would break something (see above).")
			if !ask("Update anyway?") {
				return 2
			}
		default:
			fmt.Println("   Warnings above (exit", code, ").")
		}
	}
	if !ask("== 2/3 Run `omarchy update` now (whole system, asks for your password)?") {
		return 1
	}
	if code := run("omarchy-update"); code != 0 {
		fmt.Println("omarchy update ended with", code)
		return code
	}
	fmt.Println("== 3/3 Checking drivers after the update (omarchy-hwcheck)")
	run("omarchy-hwcheck")
	_ = exec.Command("omarchy-drivers", "status", "--force").Run()
	fmt.Println("\nDone. If the kernel or NVIDIA changed, reboot to load them.")
	return 0
}

func main() {
	cmd := "status"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "status":
		force := len(os.Args) > 2 && os.Args[2] == "--force"
		jsonout.Print(drivers.Build(drivers.ReadEnv(force)))
	case "update":
		code := update()
		fmt.Print("\nEnter to close.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		os.Exit(code)
	case "firmware":
		code := run("fwupdmgr", "update")
		fmt.Print("\nEnter to close.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		os.Exit(code)
	default:
		fmt.Fprintln(os.Stderr, "usage: omarchy-drivers status [--force] | update | firmware")
		os.Exit(2)
	}
}
