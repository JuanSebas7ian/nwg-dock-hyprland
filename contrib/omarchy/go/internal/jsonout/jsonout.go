// Package jsonout prints one compact JSON document per line on stdout, the
// format every bar widget backend speaks to its QML panel.
package jsonout

import (
	"encoding/json"
	"io"
	"os"
	"sync"
)

var (
	mu sync.Mutex
	w  io.Writer = os.Stdout
)

// SetWriter redirects output (tests).
func SetWriter(nw io.Writer) { mu.Lock(); w = nw; mu.Unlock() }

// Print writes v as one JSON line. Errors writing (closed pipe: the panel went
// away) are ignored on purpose: a backend must never crash because nobody listens.
func Print(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"error": err.Error()})
	}
	mu.Lock()
	defer mu.Unlock()
	_, _ = w.Write(append(b, '\n'))
}

// Err prints {"ok":false,"error":msg}.
func Err(msg string) { Print(map[string]any{"ok": false, "error": msg}) }

// OK prints {"ok":true} merged with extra fields.
func OK(extra map[string]any) {
	m := map[string]any{"ok": true}
	for k, v := range extra {
		m[k] = v
	}
	Print(m)
}
