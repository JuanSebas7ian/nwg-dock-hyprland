// omarchy-vpn: Surfshark and Proton VPN (Free) through NetworkManager, for the VPN bar widget.
//
//	omarchy-vpn status [--refresh]       connections, public IP, leaks (JSON)
//	omarchy-vpn locations [--refresh]    Surfshark locations + imported Proton servers
//	omarchy-vpn connect surfshark <host> | proton <uuid>
//	omarchy-vpn down | toggle
//	omarchy-vpn import [files...] [--provider surfshark|proton]   (default: WireGuard .conf in ~/Downloads)
//	omarchy-vpn delete <uuid> | forget-surfshark
//	omarchy-vpn surfshark-keygen [--force]  create the key pair here (prints only the public key)
//	omarchy-vpn leaktest                 DNS/IP leak test (bash.ws)
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"omarchy/internal/vpn"
)

const usage = `omarchy-vpn status|locations [--refresh] | connect surfshark <host>|proton <uuid> | down | toggle |
            import [files] [--provider p] | delete <uuid> | forget-surfshark | surfshark-keygen [--force] | leaktest`

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(usage)
		return
	}
	p := vpn.DefaultPaths()
	cmd, rest := args[0], args[1:]
	has := func(f string) bool { return slices.Contains(rest, f) }
	var res vpn.M
	var err error
	need := func(n int) bool {
		if len(rest) < n {
			err = errors.New("missing argument")
			return false
		}
		return true
	}
	switch cmd {
	case "status":
		res = p.Status(has("--refresh"))
	case "locations":
		res = p.Locations(has("--refresh"))
	case "connect":
		if need(2) {
			res, err = p.Connect(rest[0], rest[1])
		}
	case "down":
		vpn.Down()
		res = vpn.M{"ok": true}
	case "toggle":
		res, err = p.Toggle()
	case "import":
		prov := ""
		if i := slices.Index(rest, "--provider"); i >= 0 && i+1 < len(rest) {
			prov = rest[i+1]
			rest = append(rest[:i:i], rest[i+2:]...)
		}
		res = p.Import(rest, prov)
	case "delete":
		if need(1) {
			res, err = vpn.Delete(rest[0])
		}
	case "forget-surfshark":
		res = p.ForgetSurfshark()
	case "surfshark-keygen":
		res, err = p.SurfsharkKeygen(has("--force"))
	case "leaktest":
		res, err = p.Leaktest()
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(3)
	}
	if err != nil {
		res = vpn.M{"ok": false, "error": err.Error()} // the panel shows the message
	}
	b, _ := json.Marshal(res)
	fmt.Println(string(b))
	if ok, set := res["ok"].(bool); set && !ok {
		os.Exit(1)
	}
}
