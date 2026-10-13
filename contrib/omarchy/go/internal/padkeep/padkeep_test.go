package padkeep

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func addNode(t *testing.T, root, name, vendor, product, phys string) {
	d := filepath.Join(root, name, "device")
	_ = os.MkdirAll(filepath.Join(d, "id"), 0o755)
	for rel, v := range map[string]string{"id/vendor": vendor, "id/product": product, "phys": phys} {
		_ = os.WriteFile(filepath.Join(d, rel), []byte(v+"\n"), 0o644)
	}
}

func TestFindPhysical(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PAD_KEEPALIVE_SYSFS", root)
	t.Setenv("PAD_KEEPALIVE_DEV", "/dev/input")
	addNode(t, root, "event3", "046d", "c548", "usb-0000:09:00.0-9/input0")
	if FindPhysicalPath() != "" {
		t.Fatal("found a non-pad")
	}
	addNode(t, root, "event23", "045e", "028e", "pad-keepalive/input0") // its own virtual pad
	addNode(t, root, "event5", "045e", "0b12", "usb-0000:09:00.0-3/input0")
	_ = os.MkdirAll(filepath.Join(root, "event7", "device"), 0o755)
	if FindPhysicalPath() != "" {
		t.Fatal("virtual pad / other Microsoft device / empty node taken for the pad")
	}
	addNode(t, root, "event22", "045e", "028e", "usb-0000:0b:00.4-2.2/input0")
	if got := FindPhysicalPath(); got != "/dev/input/event22" {
		t.Fatal(got)
	}
	t.Setenv("PAD_KEEPALIVE_SYSFS", filepath.Join(root, "nope"))
	if FindPhysicalPath() != "" {
		t.Fatal("missing sysfs")
	}
}

type write struct {
	t, c uint16
	v    int32
	syn  bool
}

type fakeUI struct{ w []write }

func (u *fakeUI) Read() ([]Event, error) { select {} }
func (u *fakeUI) Write(t, c uint16, v int32) error {
	u.w = append(u.w, write{t, c, v, false})
	return nil
}
func (u *fakeUI) Syn() error { u.w = append(u.w, write{syn: true}); return nil }
func (u *fakeUI) BeginUpload(int32) (Effect, error) {
	var e Effect
	e.SetID(0)
	return e, nil
}
func (u *fakeUI) EndUpload(int32, Effect, int32) error { return nil }
func (u *fakeUI) BeginErase(int32) (int16, error)      { return 0, nil }
func (u *fakeUI) EndErase(int32, int16, int32) error   { return nil }

type fakePad struct {
	grabErr  error
	keys     map[uint16]bool
	axes     map[uint16]int32
	closed   bool
	nextID   int16
	uploaded []int16
	wrote    []write
}

func (p *fakePad) Path() string { return "/dev/input/event22" }
func (p *fakePad) Grab() error  { return p.grabErr }
func (p *fakePad) Close() error { p.closed = true; return nil }
func (p *fakePad) Read() ([]Event, error) {
	select {}
}
func (p *fakePad) Write(t, c uint16, v int32) error {
	p.wrote = append(p.wrote, write{t, c, v, false})
	return nil
}
func (p *fakePad) ActiveKeys() map[uint16]bool { return p.keys }
func (p *fakePad) AbsValue(c uint16) int32     { return p.axes[c] }
func (p *fakePad) UploadEffect(e Effect) (int16, error) {
	p.uploaded = append(p.uploaded, e.ID())
	return p.nextID, nil
}
func (p *fakePad) EraseEffect(int16) error { return nil }

func has(ws []write, w write) bool {
	for _, x := range ws {
		if x == w {
			return true
		}
	}
	return false
}

func newProxy() (*Proxy, *fakeUI, *[]string) {
	ui := &fakeUI{}
	var logs []string
	return NewProxy(ui, func(s string) { logs = append(logs, s) }), ui, &logs
}

func TestGrabFailureLoggedOnceAndBacksOff(t *testing.T) {
	p, _, logs := newProxy()
	busy := errors.New("device or resource busy")
	if p.Attach(&fakePad{grabErr: busy}) || p.Attach(&fakePad{grabErr: busy}) || p.phys != nil {
		t.Fatal("attached a busy pad")
	}
	n := 0
	for _, l := range *logs {
		if strings.Contains(l, "cannot grab") {
			n++
		}
	}
	if n != 1 {
		t.Fatal(*logs)
	}
}

func TestAttachSyncsHeldButtonsAndSticks(t *testing.T) {
	p, ui, _ := newProxy()
	if !p.Attach(&fakePad{keys: map[uint16]bool{BtnA: true}, axes: map[uint16]int32{AbsX: 12000}}) {
		t.Fatal("attach")
	}
	if !has(ui.w, write{EvKey, BtnA, 1, false}) || !has(ui.w, write{EvKey, BtnB, 0, false}) || !has(ui.w, write{EvAbs, AbsX, 12000, false}) || !ui.w[len(ui.w)-1].syn {
		t.Fatal(ui.w)
	}
}

func TestDetachReleasesEverythingAndKeepsVirtual(t *testing.T) {
	p, ui, _ := newProxy()
	pad := &fakePad{keys: map[uint16]bool{BtnA: true}}
	p.Attach(pad)
	ui.w = nil
	p.Detach("no such device")
	if !pad.closed || p.phys != nil || !has(ui.w, write{EvKey, BtnA, 0, false}) || !has(ui.w, write{EvAbs, AbsX, 0, false}) || !ui.w[len(ui.w)-1].syn {
		t.Fatal(ui.w)
	}
	g := p.gen
	p.Detach("again") // no-op
	if p.gen != g {
		t.Fatal("double detach")
	}
}

func TestRumbleReuploadedToANewPad(t *testing.T) {
	p, _, _ := newProxy()
	p.effects[0] = Effect{}
	pad := &fakePad{nextID: 3}
	p.Attach(pad)
	if id, ok := p.physEffect(0); !ok || id != 3 {
		t.Fatal(id, ok)
	}
	p.Detach("gone")
	if len(p.mapped) != 0 {
		t.Fatal(p.mapped)
	}
	pad2 := &fakePad{nextID: 5}
	p.Attach(pad2)
	if id, ok := p.physEffect(0); !ok || id != 5 || pad2.uploaded[0] != -1 { // a new pad gets a new effect, id -1
		t.Fatal(id, ok, pad2.uploaded)
	}
}

func TestPlayAndGainGoToThePhysicalPad(t *testing.T) {
	p, _, _ := newProxy()
	pad := &fakePad{nextID: 7}
	p.Attach(pad)
	p.HandleUI([]Event{{EvUinput, UIFFUpload, 1}, {EvFF, 0, 1}, {EvFF, FFGain, 0xffff}, {EvFF, 9, 1}})
	if !has(pad.wrote, write{EvFF, 7, 1, false}) || !has(pad.wrote, write{EvFF, FFGain, 0xffff, false}) || len(pad.wrote) != 2 {
		t.Fatal(pad.wrote) // effect 9 was never uploaded: dropped
	}
}

func TestForwardOnlyInputEvents(t *testing.T) {
	p, ui, _ := newProxy()
	p.forward([]Event{{EvKey, BtnA, 1}, {EvAbs, AbsX, 5}, {EvSyn, 0, 0}, {0x04, 4, 9}})
	if len(ui.w) != 3 {
		t.Fatal(ui.w)
	}
}

func TestEffectID(t *testing.T) {
	var e Effect
	e.SetID(-1)
	if e.ID() != -1 || e[2] != 0xff || e[3] != 0xff {
		t.Fatal(e[:4])
	}
	if uiBeginFFUpload != 0xc06855c8 || eviocSFF != 0x40304580 || uiDevSetup != 0x405c5503 || eviocGrab != 0x40044590 {
		t.Fatalf("%x %x %x %x", uiBeginFFUpload, eviocSFF, uiDevSetup, eviocGrab)
	}
}

func TestStaleRumbleIsStopped(t *testing.T) {
	p, _, logs := newProxy()
	clock := time.Date(2026, 10, 12, 21, 40, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }
	pad := &fakePad{nextID: 3}
	p.Attach(pad)
	var e Effect
	e.SetID(0)
	p.effects[0] = e
	p.HandleUI([]Event{{Type: EvFF, Code: 0, Value: 1}}) // the game starts a rumble
	clock = clock.Add(5 * time.Second)
	p.StopStaleRumble()
	if has(pad.wrote, write{EvFF, 3, 0, false}) {
		t.Fatal("stopped too early")
	}
	clock = clock.Add(4 * time.Second) // 9 s without news from the game
	p.StopStaleRumble()
	if !has(pad.wrote, write{EvFF, 3, 0, false}) || len(p.playing) != 0 {
		t.Fatal("a stale rumble was not stopped", pad.wrote)
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "rumble stopped") {
		t.Fatal(*logs)
	}
}

func TestStoppedRumbleIsNotTouched(t *testing.T) {
	p, _, _ := newProxy()
	clock := time.Date(2026, 10, 12, 21, 40, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }
	pad := &fakePad{nextID: 3}
	p.Attach(pad)
	var e Effect
	e.SetID(0)
	p.effects[0] = e
	p.HandleUI([]Event{{Type: EvFF, Code: 0, Value: 1}, {Type: EvFF, Code: 0, Value: 0}})
	clock = clock.Add(time.Minute)
	n := len(pad.wrote)
	p.StopStaleRumble()
	if len(pad.wrote) != n {
		t.Fatal("wrote to a pad with nothing playing")
	}
}

func TestANewPadStartsSilent(t *testing.T) {
	p, _, _ := newProxy()
	p.Attach(&fakePad{})
	p.playing[5] = true
	p.Detach("unplugged")
	p.Attach(&fakePad{})
	if len(p.playing) != 0 {
		t.Fatal(p.playing)
	}
}
