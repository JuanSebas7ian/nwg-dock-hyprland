// Package padkeep keeps a wired Xbox 360 pad alive across USB drops: one
// persistent virtual pad (uinput, same ids as the real one) gets the buttons,
// sticks and rumble of the physical pad, which is grabbed so games only see
// the virtual one. When the physical pad drops off the bus ("disabled by hub
// (EMI?)"), the virtual pad stays, inputs are released and the pad is
// re-attached as soon as it re-enumerates (~1 s): the game never sees a
// disconnect.
package padkeep

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	Vendor, Product, Version = 0x045e, 0x028e, 0x0110
	Name                     = "Microsoft X-Box 360 pad"
	Phys                     = "pad-keepalive/input0"
	ScanEvery                = 250 * time.Millisecond // between looks for the physical pad while it is gone
	GrabRetry                = 5 * time.Second        // before retrying a pad another process holds
	// RumbleTimeout: an effect still marked playing with no rumble traffic from the game for this long is
	// stopped (a stop lost around a USB drop or a game hang left the pad vibrating non-stop)
	RumbleTimeout = 8 * time.Second
)

var Keys = []uint16{BtnA, BtnB, BtnX, BtnY, BtnTL, BtnTR, BtnSelect, BtnStart, BtnMode, BtnThumbL, BtnThumbR}

var Axes = []absSpec{
	{AbsX, -32768, 32767, 16, 128}, {AbsY, -32768, 32767, 16, 128}, {AbsZ, 0, 255, 0, 0},
	{AbsRX, -32768, 32767, 16, 128}, {AbsRY, -32768, 32767, 16, 128}, {AbsRZ, 0, 255, 0, 0},
	{AbsHat0X, -1, 1, 0, 0}, {AbsHat0Y, -1, 1, 0, 0},
}

var FF = []uint16{FFRumble, FFPeriodic, FFSquare, FFTriangle, FFSine, FFGain}

// Virtual is the uinput pad.
type Virtual interface {
	Read() ([]Event, error)
	Write(t, c uint16, v int32) error
	Syn() error
	BeginUpload(req int32) (Effect, error)
	EndUpload(req int32, e Effect, retval int32) error
	BeginErase(req int32) (int16, error)
	EndErase(req int32, id int16, retval int32) error
}

// Physical is the real pad.
type Physical interface {
	Path() string
	Grab() error
	Close() error
	Read() ([]Event, error)
	Write(t, c uint16, v int32) error
	ActiveKeys() map[uint16]bool
	AbsValue(code uint16) int32
	UploadEffect(e Effect) (int16, error)
	EraseEffect(id int16) error
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func readAttr(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// FindPhysicalPath finds the physical pad's event node through sysfs: nothing
// is opened while it is unplugged, and the virtual pad (phys pad-keepalive/…) is skipped.
func FindPhysicalPath() string {
	sysfs, devdir := env("PAD_KEEPALIVE_SYSFS", "/sys/class/input"), env("PAD_KEEPALIVE_DEV", "/dev/input")
	ents, err := os.ReadDir(sysfs)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "event") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		d := filepath.Join(sysfs, n, "device")
		if readAttr(filepath.Join(d, "id/vendor")) == fmt.Sprintf("%04x", Vendor) &&
			readAttr(filepath.Join(d, "id/product")) == fmt.Sprintf("%04x", Product) &&
			strings.HasPrefix(readAttr(filepath.Join(d, "phys")), "usb-") {
			return filepath.Join(devdir, n)
		}
	}
	return ""
}

type msg struct {
	fromUI bool
	gen    int
	evs    []Event
	err    error
}

// Proxy forwards the physical pad to the virtual one.
type Proxy struct {
	UI        Virtual
	phys      Physical
	gen       int
	grabError string
	effects   map[int16]Effect // virtual effect id -> effect
	mapped    map[int16]int16  // virtual effect id -> physical effect id (current pad)
	Log       func(string)
	msgs      chan msg
	playing   map[int16]bool // physical effect ids the game started and did not stop
	lastFF    time.Time      // last rumble traffic from the game (play, stop, upload)
	now       func() time.Time
}

func NewProxy(ui Virtual, log func(string)) *Proxy {
	return &Proxy{UI: ui, effects: map[int16]Effect{}, mapped: map[int16]int16{}, Log: log, msgs: make(chan msg, 64),
		playing: map[int16]bool{}, now: time.Now}
}

// Attach grabs the physical pad; false if it cannot be grabbed (the caller backs off).
func (p *Proxy) Attach(d Physical) bool {
	if err := d.Grab(); err != nil {
		if p.grabError != err.Error() {
			p.Log(fmt.Sprintf("cannot grab %s: %v; retrying every %.0f s", d.Path(), err, GrabRetry.Seconds()))
			p.grabError = err.Error()
		}
		d.Close()
		return false
	}
	p.grabError = ""
	p.phys, p.mapped, p.playing = d, map[int16]int16{}, map[int16]bool{} // a new pad starts silent
	p.gen++
	gen := p.gen
	go func() {
		for {
			evs, err := d.Read()
			if errors.Is(err, syscall.EAGAIN) {
				continue
			}
			p.msgs <- msg{gen: gen, evs: evs, err: err}
			if err != nil {
				return
			}
		}
	}()
	p.syncState()
	p.Log("attached " + d.Path())
	return true
}

// syncState copies what is held right now so nothing stays stale after a re-attach.
func (p *Proxy) syncState() {
	held := p.phys.ActiveKeys()
	for _, k := range Keys {
		v := int32(0)
		if held[k] {
			v = 1
		}
		p.UI.Write(EvKey, k, v)
	}
	for _, a := range Axes {
		p.UI.Write(EvAbs, a.code, p.phys.AbsValue(a.code))
	}
	p.UI.Syn()
}

func (p *Proxy) Detach(why string) {
	if p.phys == nil {
		return
	}
	p.phys.Close()
	p.phys, p.mapped = nil, map[int16]int16{}
	p.gen++ // events still in flight from the old pad are dropped
	p.releaseAll()
	p.Log("physical pad lost (" + why + "); virtual pad kept, waiting for it to come back")
}

func (p *Proxy) releaseAll() {
	for _, k := range Keys {
		p.UI.Write(EvKey, k, 0)
	}
	for _, a := range Axes {
		p.UI.Write(EvAbs, a.code, 0)
	}
	p.UI.Syn()
}

func (p *Proxy) forward(evs []Event) {
	for _, e := range evs {
		if e.Type == EvKey || e.Type == EvAbs || e.Type == EvSyn {
			p.UI.Write(e.Type, e.Code, e.Value)
		}
	}
}

// physEffect uploads a virtual effect to the current physical pad; ok=false if it cannot.
func (p *Proxy) physEffect(vid int16) (int16, bool) {
	eff, ok := p.effects[vid]
	if p.phys == nil || !ok {
		return 0, false
	}
	id, have := p.mapped[vid]
	if !have {
		id = -1
	}
	eff.SetID(id)
	nid, err := p.phys.UploadEffect(eff)
	if err != nil {
		return 0, false
	}
	p.mapped[vid] = nid
	return nid, true
}

// HandleUI answers rumble uploads/erases from games and plays effects on the physical pad.
func (p *Proxy) HandleUI(evs []Event) {
	for _, e := range evs {
		switch {
		case e.Type == EvUinput && e.Code == UIFFUpload:
			eff, err := p.UI.BeginUpload(e.Value)
			if err != nil {
				continue
			}
			p.effects[eff.ID()] = eff
			p.lastFF = p.now()
			p.physEffect(eff.ID())
			p.UI.EndUpload(e.Value, eff, 0)
		case e.Type == EvUinput && e.Code == UIFFErase:
			id, err := p.UI.BeginErase(e.Value)
			if err != nil {
				continue
			}
			delete(p.effects, id)
			if pid, ok := p.mapped[id]; ok {
				delete(p.mapped, id)
				if p.phys != nil {
					p.phys.EraseEffect(pid)
				}
			}
			p.UI.EndErase(e.Value, id, 0)
		case e.Type == EvFF && p.phys != nil:
			code := e.Code
			if code != FFGain {
				pid, ok := p.mapped[int16(code)]
				if !ok {
					pid, ok = p.physEffect(int16(code))
				}
				if !ok {
					continue
				}
				code = uint16(pid)
				p.lastFF = p.now()
				if e.Value > 0 {
					p.playing[pid] = true
				} else {
					delete(p.playing, pid)
				}
			}
			p.phys.Write(EvFF, code, e.Value)
		}
	}
}

// StopStaleRumble stops effects the game left playing without any rumble traffic for RumbleTimeout.
func (p *Proxy) StopStaleRumble() {
	if p.phys == nil || len(p.playing) == 0 || p.now().Sub(p.lastFF) < RumbleTimeout {
		return
	}
	for pid := range p.playing {
		p.phys.Write(EvFF, uint16(pid), 0)
	}
	p.Log(fmt.Sprintf("rumble stopped: %d effect(s) still playing with no update from the game for %.0f s", len(p.playing), RumbleTimeout.Seconds()))
	p.playing = map[int16]bool{}
}

// Run is the main loop; find opens the physical pad (nil while it is gone).
func (p *Proxy) Run(find func() Physical) {
	go func() {
		for {
			evs, err := p.UI.Read()
			if errors.Is(err, syscall.EAGAIN) {
				continue
			}
			p.msgs <- msg{fromUI: true, evs: evs, err: err}
			if err != nil {
				return
			}
		}
	}()
	next := time.Time{}
	tick := time.NewTicker(ScanEvery)
	defer tick.Stop()
	for {
		if p.phys == nil && !time.Now().Before(next) {
			wait := ScanEvery
			if d := find(); d != nil && !p.Attach(d) {
				wait = GrabRetry
			}
			next = time.Now().Add(wait)
		}
		select {
		case m := <-p.msgs:
			switch {
			case m.fromUI && m.err != nil:
				p.Log("virtual pad closed: " + m.err.Error())
				return
			case m.fromUI:
				p.HandleUI(m.evs)
			case m.gen != p.gen || p.phys == nil:
			case m.err != nil:
				p.Detach(m.err.Error())
			default:
				p.forward(m.evs)
			}
		case <-tick.C:
			p.StopStaleRumble()
		}
	}
}
