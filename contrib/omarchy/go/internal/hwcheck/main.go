package hwcheck

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const Usage = `omarchy-hwcheck: validador determinista de drivers, actualizaciones y conflictos para Omarchy.

Solo lee el sistema: no necesita root y no cambia nada (salvo guardar la línea base con ` + "`baseline`" + `).
El mismo estado del sistema produce siempre el mismo informe: todo se ordena, no hay marcas de tiempo
y los comandos se ejecutan con LC_ALL=C. La única entrada que depende de la red es ` + "`scan --online`" + `.

Uso:
  omarchy-hwcheck [scan] [--online] [--json] [--only hw,updates,conflicts]
  omarchy-hwcheck baseline [--force]    guarda el estado actual como referencia
  omarchy-hwcheck diff [--json]         compara el estado actual con la referencia
  omarchy-hwcheck state [--json]        muestra el estado normalizado que se compara

Códigos de salida: 0 todo bien (o solo informativo), 1 hay avisos, 2 hay fallos o regresiones,
3 error de uso.
`

// encode writes JSON like Python's json.dumps(indent=2, ensure_ascii=False, sort_keys=True).
func encode(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func StateDir(home string) string {
	b := os.Getenv("XDG_STATE_HOME")
	if b == "" {
		b = filepath.Join(home, ".local/state")
	}
	return filepath.Join(b, "omarchy-hwcheck")
}

// Main runs the command line; s nil = the real system.
func Main(args []string, s System, out, errw io.Writer) int {
	cmd := "scan"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	} else if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "--version") {
		cmd = args[0]
	}
	asJSON, online, force := slices.Contains(args, "--json"), slices.Contains(args, "--online"), slices.Contains(args, "--force")
	var only map[string]bool
	if i := slices.Index(args, "--only"); i >= 0 {
		if i+1 >= len(args) {
			fmt.Fprintln(errw, "--only necesita hw, updates o conflicts")
			return 3
		}
		only = setOf(strings.Split(args[i+1], ","))
	}
	switch cmd {
	case "-h", "--help", "help":
		io.WriteString(out, Usage+"\n")
		return 0
	case "--version":
		fmt.Fprintln(out, Version)
		return 0
	case "scan", "baseline", "diff", "state":
	default:
		fmt.Fprintf(errw, "comando desconocido: %s\n%s\n", cmd, Usage)
		return 3
	}
	if s == nil {
		s = NewRealSystem()
	}
	facts := NewFacts(s)
	if cmd == "scan" {
		fs := RunChecks(facts, online, only)
		if asJSON {
			if fs == nil {
				fs = []*Finding{}
			}
			encode(out, struct {
				Findings []*Finding `json:"findings"`
				Kernel   string     `json:"kernel"`
				Version  string     `json:"version"`
			}{fs, facts.Release, Version})
		} else {
			io.WriteString(out, RenderScan(fs, facts.Release))
		}
		var lv []string
		for _, x := range fs {
			lv = append(lv, x.Level)
		}
		return ExitCode(lv)
	}
	state := CollectState(facts)
	if cmd == "state" {
		encode(out, state)
		return 0
	}
	path := filepath.Join(StateDir(s.Home()), "baseline.json")
	if cmd == "baseline" {
		if _, err := os.Stat(path); err == nil && !force {
			fmt.Fprintf(out, "Ya existe %s. Usa --force para reemplazarla (revisa antes `omarchy-hwcheck diff`).\n", path)
			return 1
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(errw, err)
			return 3
		}
		tmp := path + ".tmp"
		fh, err := os.Create(tmp)
		if err != nil {
			fmt.Fprintln(errw, err)
			return 3
		}
		encode(fh, state)
		fh.Close()
		if err := os.Rename(tmp, path); err != nil {
			fmt.Fprintln(errw, err)
			return 3
		}
		fmt.Fprintf(out, "Línea base guardada en %s\n", path)
		return 0
	}
	var old State
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &old)
	}
	if err != nil {
		fmt.Fprintf(errw, "No hay línea base válida en %s: créala con `omarchy-hwcheck baseline`.\n", path)
		return 3
	}
	changes := DiffStates(old, state)
	if asJSON {
		if changes == nil {
			changes = []Change{}
		}
		encode(out, struct {
			Baseline string   `json:"baseline"`
			Changes  []Change `json:"changes"`
		}{path, changes})
	} else {
		io.WriteString(out, RenderDiff(changes, path))
	}
	var lv []string
	for _, c := range changes {
		lv = append(lv, c.Level)
	}
	return ExitCode(lv)
}
