// pad-keepalive: keep a wired Xbox 360 pad alive across USB drops (see internal/padkeep).
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"omarchy/internal/padkeep"
)

func main() {
	log := func(s string) { fmt.Println(s) }
	ui, err := padkeep.NewUInput(padkeep.Name, padkeep.Vendor, padkeep.Product, padkeep.Version, padkeep.Phys,
		padkeep.Keys, padkeep.Axes, padkeep.FF, 16)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot create the virtual pad:", err)
		os.Exit(1)
	}
	log("virtual pad ready: " + ui.Path)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sig; ui.Close(); os.Exit(0) }()
	padkeep.NewProxy(ui, log).Run(func() padkeep.Physical {
		path := padkeep.FindPhysicalPath()
		if path == "" {
			return nil
		}
		pad, err := padkeep.OpenPad(path)
		if err != nil {
			return nil
		}
		return pad
	})
	os.Exit(1)
}
