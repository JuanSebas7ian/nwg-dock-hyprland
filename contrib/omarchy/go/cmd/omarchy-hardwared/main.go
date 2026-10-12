// omarchy-hardwared: backend of the juansebas7ian.hardware bar widget, one
// long-running process for every hardware tab (protocol in internal/hw/daemon.go).
//
//	omarchy-hardwared                  stream topics on stdout, commands on stdin
//	omarchy-hardwared --once [topic]   print one topic and exit (live, drivers, storage, peripherals)
//	omarchy-hardwared cuda-probe       CUDA check in its own short-lived process
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"omarchy/internal/hw"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "cuda-probe" {
		b, _ := json.Marshal(hw.CudaProbe())
		fmt.Println(string(b))
		return
	}
	if len(args) > 0 && args[0] == "--once" {
		topic := "live"
		if len(args) > 1 {
			topic = args[1]
		}
		var data any
		switch topic {
		case "live":
			s := hw.NewSensors(true)
			time.Sleep(500 * time.Millisecond)
			data = s.Sample(true, true)
		case "drivers":
			data = hw.CollectDrivers(false)
		case "storage":
			data = hw.Overview()
		case "peripherals":
			data = hw.CollectPeripherals(true)
		default:
			fmt.Fprintln(os.Stderr, "usage: omarchy-hardwared [--once live|drivers|storage|peripherals | cuda-probe]")
			os.Exit(2)
		}
		b, _ := json.MarshalIndent(data, "", " ")
		fmt.Println(string(b))
		return
	}
	signal.Ignore(syscall.SIGPIPE) // a closed stdout shows up as a write error: exit cleanly then
	hw.NewDaemon(os.Stdout, true).Run(os.Stdin)
}
