package hw

// The daemon writes one JSON object per line on stdout, {"type": <topic>,
// "data": ...}, and reads commands from stdin, one JSON object per line:
//
//	{"cmd": "view", "open": true, "tab": "cpu"}
//	{"cmd": "refresh", "what": "drivers"|"storage"|"peripherals", "force": true}
//	{"cmd": "dir", "path": "~/Downloads"}
//	{"cmd": "selftest", "obj": "/org/freedesktop/UDisks2/drives/X"}
//	{"cmd": "bt", "action": "connect"|"disconnect"|"autoconnect"|"visible", "mac": "...", "on": true}
//
// Topics: live (1 s on a live tab, 5 s otherwise), history (last 120 samples,
// when the panel opens), drivers (startup + 30 min), storage.* (overview every
// 10 min; breakdown, steam, cleanup, firmware with the Storage tab),
// peripherals (3 s on the Devices tab, 60 s otherwise), alerts, result, error.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
)

var liveTabs = map[string]bool{"summary": true, "cpu": true, "gpu": true}

const historySize = 120

// Command from the panel.
type Command struct {
	Cmd    string  `json:"cmd"`
	Open   bool    `json:"open"`
	Tab    string  `json:"tab"`
	What   string  `json:"what"`
	Force  bool    `json:"force"`
	Path   string  `json:"path"`
	Obj    string  `json:"obj"`
	Action string  `json:"action"`
	Mac    *string `json:"mac"`
	On     bool    `json:"on"`
}

// Daemon streams every hardware topic.
type Daemon struct {
	out     io.Writer
	Exit    func(int)
	mu      sync.Mutex // output and shared state
	busy    map[string]bool
	cfg     Config
	sensors *Sensors
	history []M
	opened  bool
	tab     string
	latest  map[string]M
	notif   *Notifier
	alerts  []Alert
	due     map[string]time.Time
	cmds    chan Command
	sem     chan struct{}
}

func NewDaemon(out io.Writer, useNvml bool) *Daemon {
	now := time.Now()
	return &Daemon{out: out, Exit: os.Exit, busy: map[string]bool{}, cfg: LoadConfig(), sensors: NewSensors(useNvml), tab: "summary",
		latest: map[string]M{}, notif: NewNotifier(10 * time.Minute), cmds: make(chan Command, 64), sem: make(chan struct{}, 3),
		due: map[string]time.Time{"drivers": now.Add(20 * time.Second), "storage": now.Add(5 * time.Second), "peripherals": now.Add(8 * time.Second)}}
}

func (d *Daemon) emit(topic string, data any) {
	line, err := json.Marshal(M{"type": topic, "data": data})
	if err != nil {
		line, _ = json.Marshal(M{"type": "error", "data": M{"job": topic, "error": err.Error()}})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.out.Write(append(line, '\n')); err != nil {
		d.Exit(0) // the shell went away
	}
}

func (d *Daemon) publishAlerts() {
	d.mu.Lock()
	active := Evaluate(d.cfg, d.latest["live"], d.latest["drivers"], d.latest["storage"], d.latest["peripherals"])
	d.notif.Update(active, time.Now())
	changed := !reflect.DeepEqual(active, d.alerts)
	if changed {
		d.alerts = active
	}
	d.mu.Unlock()
	if changed {
		d.emit("alerts", active)
	}
}

// job runs fn in the background (3 at a time) unless one with this name is running.
func (d *Daemon) job(name string, fn func() M, then func(M)) {
	d.mu.Lock()
	if d.busy[name] {
		d.mu.Unlock()
		return
	}
	d.busy[name] = true
	d.mu.Unlock()
	go func() {
		d.sem <- struct{}{}
		var res M
		func() {
			defer func() {
				if r := recover(); r != nil { // a failing collector must not kill the stream
					e := fmt.Sprint(r)
					if len(e) > 300 {
						e = e[:300]
					}
					d.emit("error", M{"job": name, "error": e})
					res = nil
				}
			}()
			res = fn()
		}()
		<-d.sem
		d.mu.Lock()
		delete(d.busy, name)
		d.mu.Unlock()
		if res != nil && then != nil {
			then(res)
		}
	}()
}

func (d *Daemon) setLatest(k string, v M) { d.mu.Lock(); d.latest[k] = v; d.mu.Unlock() }

func (d *Daemon) refreshDrivers(force bool) {
	d.job("drivers", func() M { return CollectDrivers(force) }, func(r M) {
		d.setLatest("drivers", r)
		d.emit("drivers", r)
		d.publishAlerts()
	})
}

func (d *Daemon) refreshStorage(full bool) {
	d.job("storage.overview", Overview, func(r M) {
		d.setLatest("storage", r)
		d.emit("storage.overview", r)
		d.publishAlerts()
	})
	if full {
		for name, fn := range map[string]func() M{"breakdown": Breakdown, "cleanup": Cleanup, "steam": Steam, "firmware": Firmware} {
			topic := "storage." + name
			d.job(topic, fn, func(r M) { d.emit(topic, r) })
		}
	}
}

func (d *Daemon) refreshPeripherals(slow bool) {
	d.job("peripherals", func() M { return CollectPeripherals(slow) }, func(r M) {
		d.setLatest("peripherals", r)
		d.emit("peripherals", r)
		d.publishAlerts()
	})
}

func (d *Daemon) handle(c Command) {
	switch c.Cmd {
	case "view":
		wasOpen, oldTab := d.opened, d.tab
		d.opened = c.Open
		if c.Tab != "" {
			d.tab = c.Tab
		}
		if d.opened && !wasOpen {
			d.mu.Lock()
			h := append([]M{}, d.history...)
			d.mu.Unlock()
			d.emit("history", h)
		}
		if d.opened && (!wasOpen || d.tab != oldTab) {
			switch d.tab {
			case "storage":
				d.refreshStorage(true)
			case "devices":
				d.refreshPeripherals(true)
			case "drivers":
				d.mu.Lock()
				none := d.latest["drivers"] == nil
				d.mu.Unlock()
				if none {
					d.refreshDrivers(false)
				}
			}
		}
	case "refresh":
		switch c.What {
		case "drivers":
			d.refreshDrivers(c.Force)
		case "storage":
			d.refreshStorage(true)
		case "peripherals":
			d.refreshPeripherals(true)
		}
		cfg := LoadConfig()
		d.mu.Lock()
		d.cfg = cfg
		d.mu.Unlock()
	case "dir":
		path := c.Path
		if path == "" {
			path = "~"
		}
		d.job("storage.dir", func() M { return Directory(path) }, func(r M) { d.emit("storage.dir", r) })
	case "selftest":
		if strings.HasPrefix(c.Obj, "/org/freedesktop/UDisks2/drives/") {
			obj := c.Obj
			d.job("selftest", func() M { return StartSelftest(obj, "short") }, func(r M) { r["action"] = "selftest"; d.emit("result", r) })
		}
	case "bt":
		mac := ""
		if c.Mac != nil {
			mac = *c.Mac
		}
		action, on, macp := c.Action, c.On, c.Mac
		d.job("bt-"+mac, func() M { return BTAction(action, macp, on) }, func(r M) {
			r["action"] = "bt-" + action
			if macp != nil {
				r["mac"] = *macp
			} else {
				r["mac"] = nil
			}
			d.emit("result", r)
			d.refreshPeripherals(false)
		})
		d.due["peripherals"] = time.Now().Add(3 * time.Second)
	}
}

// ReadCommands feeds stdin lines to the loop; EOF means the shell went away.
func (d *Daemon) ReadCommands(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var c Command
		if json.Unmarshal(sc.Bytes(), &c) == nil {
			d.cmds <- c
		}
	}
	d.Exit(0)
}

func maxCooler(fans []M) int64 {
	var best int64
	for _, f := range fans {
		if f["role"] == "cpu" {
			if r, _ := f["rpm"].(int64); r > best {
				best = r
			}
		}
	}
	return best
}

// Run is the main loop (never returns).
func (d *Daemon) Run(stdin io.Reader) {
	go d.ReadCommands(stdin)
	next := time.Now()
	tick := 0
	for {
	drain:
		for {
			select {
			case c := <-d.cmds:
				d.handle(c)
			default:
				break drain
			}
		}
		now := time.Now()
		if !now.Before(next) {
			tick++
			live := d.opened && liveTabs[d.tab]
			snap := d.sensors.Sample(live || tick%5 == 0, d.opened && d.tab == "gpu" && tick%2 == 0)
			d.mu.Lock()
			if _, ok := snap["gpus"]; !ok && d.latest["live"] != nil {
				if g, ok := d.latest["live"]["gpus"]; ok {
					snap["gpus"] = g
				}
			}
			var gpuUtil any
			if g, ok := snap["gpus"].([]M); ok && len(g) > 0 {
				gpuUtil = g[0]["util"]
			}
			cpu := snap["cpu"].(M)
			d.history = append(d.history, M{"t": iround(snap["t"].(float64)), "tctl": cpu["tctl"], "load": cpu["load"], "cooler": maxCooler(snap["fans"].([]M)), "gpu": gpuUtil})
			if len(d.history) > historySize {
				d.history = d.history[len(d.history)-historySize:]
			}
			d.latest["live"] = snap
			d.mu.Unlock()
			if live || tick%5 == 0 {
				d.emit("live", snap)
				d.publishAlerts()
			}
			next = next.Add(time.Second)
			if next.Before(now) {
				next = now.Add(time.Second)
			}
		}
		every := map[string]time.Duration{"drivers": 30 * time.Minute, "storage": 10 * time.Minute, "peripherals": time.Minute}
		if d.opened && d.tab == "devices" {
			every["peripherals"] = 3 * time.Second
		}
		for _, what := range []string{"drivers", "storage", "peripherals"} {
			if !now.Before(d.due[what]) {
				d.due[what] = now.Add(every[what])
				switch what {
				case "drivers":
					d.refreshDrivers(false)
				case "storage":
					d.refreshStorage(false)
				case "peripherals":
					d.refreshPeripherals(false)
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}
