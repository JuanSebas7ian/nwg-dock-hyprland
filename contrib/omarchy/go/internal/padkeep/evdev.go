package padkeep

// Linux evdev and uinput through raw ioctls (x86_64 layouts from
// <linux/input.h> and <linux/uinput.h>).

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	EvSyn    = 0x00
	EvKey    = 0x01
	EvAbs    = 0x03
	EvFF     = 0x15
	EvUinput = 0x0101

	UIFFUpload = 1
	UIFFErase  = 2

	BtnA, BtnB, BtnX, BtnY                = 0x130, 0x131, 0x133, 0x134
	BtnTL, BtnTR                          = 0x136, 0x137
	BtnSelect, BtnStart, BtnMode          = 0x13a, 0x13b, 0x13c
	BtnThumbL, BtnThumbR                  = 0x13d, 0x13e
	AbsX, AbsY, AbsZ, AbsRX, AbsRY, AbsRZ = 0x00, 0x01, 0x02, 0x03, 0x04, 0x05
	AbsHat0X, AbsHat0Y                    = 0x10, 0x11
	FFRumble, FFPeriodic                  = 0x50, 0x51
	FFSquare, FFTriangle, FFSine, FFGain  = 0x58, 0x59, 0x5a, 0x60
	busUSB                                = 0x03
	effectSize                            = 48 // sizeof(struct ff_effect)
	eventSize                             = 24 // sizeof(struct input_event)
	keyMax                                = 0x2ff
	iocWrite, iocRead                     = 1, 2
)

func ioc(dir, typ, nr, size uintptr) uintptr { return dir<<30 | size<<16 | typ<<8 | nr }

var (
	uiDevCreate     = ioc(0, 'U', 1, 0)
	uiDevDestroy    = ioc(0, 'U', 2, 0)
	uiDevSetup      = ioc(iocWrite, 'U', 3, 92)
	uiAbsSetup      = ioc(iocWrite, 'U', 4, 28)
	uiSetEvBit      = ioc(iocWrite, 'U', 100, 4)
	uiSetKeyBit     = ioc(iocWrite, 'U', 101, 4)
	uiSetAbsBit     = ioc(iocWrite, 'U', 103, 4)
	uiSetFFBit      = ioc(iocWrite, 'U', 107, 4)
	uiSetPhys       = ioc(iocWrite, 'U', 108, 8)
	uiBeginFFUpload = ioc(iocRead|iocWrite, 'U', 200, 104)
	uiEndFFUpload   = ioc(iocWrite, 'U', 201, 104)
	uiBeginFFErase  = ioc(iocRead|iocWrite, 'U', 202, 12)
	uiEndFFErase    = ioc(iocWrite, 'U', 203, 12)
	uiGetSysname    = ioc(iocRead, 'U', 44, 64)
	eviocGrab       = ioc(iocWrite, 'E', 0x90, 4)
	eviocGKey       = ioc(iocRead, 'E', 0x18, (keyMax+7)/8+1)
	eviocSFF        = ioc(iocWrite, 'E', 0x80, effectSize)
	eviocRmFF       = ioc(iocWrite, 'E', 0x81, 4)
)

func eviocGAbs(abs int) uintptr { return ioc(iocRead, 'E', uintptr(0x40+abs), 24) }

// Event is one input event.
type Event struct {
	Type, Code uint16
	Value      int32
}

// Effect is a raw struct ff_effect; the id is the s16 at offset 2.
type Effect [effectSize]byte

func (e *Effect) ID() int16      { return int16(binary.LittleEndian.Uint16(e[2:])) }
func (e *Effect) SetID(id int16) { binary.LittleEndian.PutUint16(e[2:], uint16(id)) }

// dev wraps a device file; ioctls go through RawConn so the fd stays in Go's poller.
type dev struct{ f *os.File }

func (d dev) ioctl(req uintptr, arg uintptr) error {
	rc, err := d.f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if cerr := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg)
	}); cerr != nil {
		return cerr
	}
	if errno != 0 {
		return errno
	}
	return nil
}

func (d dev) ioctlPtr(req uintptr, p unsafe.Pointer) error { return d.ioctl(req, uintptr(p)) }

func (d dev) readEvents(buf []byte) ([]Event, error) {
	n, err := d.f.Read(buf)
	if err != nil {
		return nil, err
	}
	var out []Event
	for i := 0; i+eventSize <= n; i += eventSize {
		b := buf[i+16:]
		out = append(out, Event{binary.LittleEndian.Uint16(b), binary.LittleEndian.Uint16(b[2:]), int32(binary.LittleEndian.Uint32(b[4:]))})
	}
	return out, nil
}

func (d dev) write(t, c uint16, v int32) error {
	var b [eventSize]byte
	binary.LittleEndian.PutUint16(b[16:], t)
	binary.LittleEndian.PutUint16(b[18:], c)
	binary.LittleEndian.PutUint32(b[20:], uint32(v))
	_, err := d.f.Write(b[:])
	return err
}

// --------------------------------------------------------------------------- physical pad

// EvdevPad is the grabbed physical pad.
type EvdevPad struct {
	dev
	path string
	buf  []byte
}

func OpenPad(path string) (*EvdevPad, error) {
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return &EvdevPad{dev{f}, path, make([]byte, eventSize*64)}, nil
}

func (p *EvdevPad) Path() string                     { return p.path }
func (p *EvdevPad) Grab() error                      { return p.ioctl(eviocGrab, 1) }
func (p *EvdevPad) Close() error                     { return p.f.Close() }
func (p *EvdevPad) Read() ([]Event, error)           { return p.readEvents(p.buf) }
func (p *EvdevPad) Write(t, c uint16, v int32) error { return p.write(t, c, v) }

func (p *EvdevPad) ActiveKeys() map[uint16]bool {
	var bits [(keyMax+7)/8 + 1]byte
	keys := map[uint16]bool{}
	if p.ioctlPtr(eviocGKey, unsafe.Pointer(&bits[0])) != nil {
		return keys
	}
	for i := 0; i <= keyMax; i++ {
		if bits[i/8]&(1<<(i%8)) != 0 {
			keys[uint16(i)] = true
		}
	}
	return keys
}

func (p *EvdevPad) AbsValue(code uint16) int32 {
	var info [6]int32
	if p.ioctlPtr(eviocGAbs(int(code)), unsafe.Pointer(&info[0])) != nil {
		return 0
	}
	return info[0]
}

// UploadEffect returns the id the pad gave the effect.
func (p *EvdevPad) UploadEffect(e Effect) (int16, error) {
	if err := p.ioctlPtr(eviocSFF, unsafe.Pointer(&e[0])); err != nil {
		return 0, err
	}
	return e.ID(), nil
}

func (p *EvdevPad) EraseEffect(id int16) error { return p.ioctl(eviocRmFF, uintptr(id)) }

// --------------------------------------------------------------------------- virtual pad

// UInput is the virtual pad.
type UInput struct {
	dev
	buf  []byte
	Path string
}

type absSpec struct {
	code                 uint16
	min, max, fuzz, flat int32
}

// NewUInput creates the virtual pad with the same ids, buttons, axes and rumble as the real one.
func NewUInput(name string, vendor, product, version uint16, phys string, keys []uint16, axes []absSpec, ff []uint16, maxEffects uint32) (*UInput, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	u := &UInput{dev: dev{f}, buf: make([]byte, eventSize*16)}
	fail := func(what string, err error) (*UInput, error) {
		f.Close()
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	for _, t := range []uintptr{EvKey, EvAbs, EvFF} {
		if err := u.ioctl(uiSetEvBit, t); err != nil {
			return fail("UI_SET_EVBIT", err)
		}
	}
	for _, k := range keys {
		if err := u.ioctl(uiSetKeyBit, uintptr(k)); err != nil {
			return fail("UI_SET_KEYBIT", err)
		}
	}
	for _, x := range ff {
		if err := u.ioctl(uiSetFFBit, uintptr(x)); err != nil {
			return fail("UI_SET_FFBIT", err)
		}
	}
	physB := append([]byte(phys), 0)
	if err := u.ioctlPtr(uiSetPhys, unsafe.Pointer(&physB[0])); err != nil {
		return fail("UI_SET_PHYS", err)
	}
	for _, a := range axes {
		if err := u.ioctl(uiSetAbsBit, uintptr(a.code)); err != nil {
			return fail("UI_SET_ABSBIT", err)
		}
		var s [28]byte
		binary.LittleEndian.PutUint16(s[0:], a.code)
		for i, v := range []int32{0, a.min, a.max, a.fuzz, a.flat, 0} {
			binary.LittleEndian.PutUint32(s[4+4*i:], uint32(v))
		}
		if err := u.ioctlPtr(uiAbsSetup, unsafe.Pointer(&s[0])); err != nil {
			return fail("UI_ABS_SETUP", err)
		}
	}
	var setup [92]byte
	binary.LittleEndian.PutUint16(setup[0:], busUSB)
	binary.LittleEndian.PutUint16(setup[2:], vendor)
	binary.LittleEndian.PutUint16(setup[4:], product)
	binary.LittleEndian.PutUint16(setup[6:], version)
	copy(setup[8:87], name)
	binary.LittleEndian.PutUint32(setup[88:], maxEffects)
	if err := u.ioctlPtr(uiDevSetup, unsafe.Pointer(&setup[0])); err != nil {
		return fail("UI_DEV_SETUP", err)
	}
	if err := u.ioctl(uiDevCreate, 0); err != nil {
		return fail("UI_DEV_CREATE", err)
	}
	var sys [64]byte
	if u.ioctlPtr(uiGetSysname, unsafe.Pointer(&sys[0])) == nil {
		n := 0
		for n < len(sys) && sys[n] != 0 {
			n++
		}
		u.Path = "/sys/devices/virtual/input/" + string(sys[:n])
	}
	return u, nil
}

func (u *UInput) Read() ([]Event, error)           { return u.readEvents(u.buf) }
func (u *UInput) Write(t, c uint16, v int32) error { return u.write(t, c, v) }
func (u *UInput) Syn() error                       { return u.write(EvSyn, 0, 0) }
func (u *UInput) Close() error                     { _ = u.ioctl(uiDevDestroy, 0); return u.f.Close() }

// BeginUpload fetches the effect a game uploaded to the virtual pad.
func (u *UInput) BeginUpload(req int32) (Effect, error) {
	var b [104]byte
	binary.LittleEndian.PutUint32(b[0:], uint32(req))
	if err := u.ioctlPtr(uiBeginFFUpload, unsafe.Pointer(&b[0])); err != nil {
		return Effect{}, err
	}
	var e Effect
	copy(e[:], b[8:8+effectSize])
	return e, nil
}

func (u *UInput) EndUpload(req int32, e Effect, retval int32) error {
	var b [104]byte
	binary.LittleEndian.PutUint32(b[0:], uint32(req))
	binary.LittleEndian.PutUint32(b[4:], uint32(retval))
	copy(b[8:], e[:])
	return u.ioctlPtr(uiEndFFUpload, unsafe.Pointer(&b[0]))
}

// BeginErase returns the id of the effect a game erased.
func (u *UInput) BeginErase(req int32) (int16, error) {
	var b [12]byte
	binary.LittleEndian.PutUint32(b[0:], uint32(req))
	if err := u.ioctlPtr(uiBeginFFErase, unsafe.Pointer(&b[0])); err != nil {
		return 0, err
	}
	return int16(binary.LittleEndian.Uint32(b[8:])), nil
}

func (u *UInput) EndErase(req int32, id int16, retval int32) error {
	var b [12]byte
	binary.LittleEndian.PutUint32(b[0:], uint32(req))
	binary.LittleEndian.PutUint32(b[4:], uint32(retval))
	binary.LittleEndian.PutUint32(b[8:], uint32(id))
	return u.ioctlPtr(uiEndFFErase, unsafe.Pointer(&b[0]))
}
