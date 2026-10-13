// omarchy-game: one-button game mode for the bar (and GameMode's start/end hooks).
//
//	omarchy-game status          on/off, what is paused, CPU pressure, upload rate (JSON)
//	omarchy-game on [--auto]     freeze background work (--auto: started by GameMode for a game)
//	omarchy-game off [--auto]    resume it (--auto: a mode switched on by hand stays on)
//	omarchy-game toggle
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"omarchy/internal/game"
	"omarchy/internal/jsonout"
)

func uplink() (iface string, kbps int) {
	ents, _ := os.ReadDir("/sys/class/net")
	read := func(i string) int64 {
		b, _ := os.ReadFile(filepath.Join("/sys/class/net", i, "statistics", "tx_bytes"))
		n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		return n
	}
	best, bestRate := "", int64(0)
	before := map[string]int64{}
	for _, e := range ents {
		if n := e.Name(); n != "lo" && !strings.HasPrefix(n, "docker") && !strings.HasPrefix(n, "veth") && !strings.HasPrefix(n, "br-") {
			before[n] = read(n)
		}
	}
	time.Sleep(time.Second)
	for n, b := range before {
		if r := read(n) - b; r > bestRate || best == "" {
			best, bestRate = n, r
		}
	}
	return best, int(bestRate / 1024)
}

func notify(title, body string) {
	_, _ = game.ExecRunner("notify-send", "-a", "Game mode", "-i", "applications-games", title, body)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"status"}
	}
	path := game.StatePath()
	auto := slices.Contains(args, "--auto")
	home, _ := os.UserHomeDir()
	out := func(s game.State, err error) {
		if err != nil {
			jsonout.Err(err.Error())
			os.Exit(1)
		}
		jsonout.OK(map[string]any{"state": s})
	}
	switch args[0] {
	case "status":
		iface, kbps := uplink()
		jsonout.Print(map[string]any{"state": game.Load(path), "cpuPressure": game.Pressure("/proc/pressure/cpu"), "uplink": iface, "uploadKBps": kbps})
	case "on":
		s, err := game.On(game.ExecRunner, path, auto)
		game.ProtonNoXalia(filepath.Join(home, ".local/share/Steam/steamapps/common"))
		if err == nil && s.On {
			notify("Modo juego activado", fmt.Sprintf("En pausa: %d tareas de fondo (subidas, respaldos, indexador)", len(s.Frozen)))
		}
		out(s, err)
	case "off":
		s, err := game.Off(game.ExecRunner, path, auto)
		if err == nil && !s.On {
			notify("Modo juego desactivado", "Las subidas y respaldos siguen donde iban")
		}
		out(s, err)
	case "toggle":
		if game.Load(path).On {
			s, err := game.Off(game.ExecRunner, path, false)
			if err == nil {
				notify("Modo juego desactivado", "Las subidas y respaldos siguen donde iban")
			}
			out(s, err)
		} else {
			s, err := game.On(game.ExecRunner, path, false)
			game.ProtonNoXalia(filepath.Join(home, ".local/share/Steam/steamapps/common"))
			if err == nil {
				notify("Modo juego activado", fmt.Sprintf("En pausa: %d tareas de fondo (subidas, respaldos, indexador)", len(s.Frozen)))
			}
			out(s, err)
		}
	default:
		fmt.Fprintln(os.Stderr, "omarchy-game status | on [--auto] | off [--auto] | toggle")
		os.Exit(3)
	}
}
