// bt-pair-keyboard pairs a Bluetooth LE keyboard that asks for a passkey
// (Logitech Wave Keys, MX Keys, K380...).
//
//	bt-pair-keyboard ["Wave Keys"]
//
// These keyboards only finish pairing after you TYPE a 6-digit code on them
// and press Enter. If nothing shows that code the link stays unencrypted,
// BlueZ can't read the HID service and the keyboard never works. This runs
// bluetoothctl with a KeyboardDisplay agent, shows the code big, then trusts
// and connects.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x01|\x02`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

var passkeyRe = regexp.MustCompile(`(?:Passkey:?\s*|Confirm passkey )(\d{6})`)

// passkeys returns the 6-digit codes bluetoothctl shows in text.
func passkeys(text string) []string {
	var out []string
	for _, m := range passkeyRe.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

func deviceRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`Device ([0-9A-F:]{17}) (?:Name: |Alias: )?` + regexp.QuoteMeta(name))
}

var anyDevice = regexp.MustCompile(`Device ([0-9A-F:]{17}) ([^\r\n]*)`)

// openPTY returns the master side and the path of the slave of a new pseudo-terminal.
func openPTY() (*os.File, string, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", err
	}
	unlock := 0
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		return nil, "", e
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		m.Close()
		return nil, "", e
	}
	return m, fmt.Sprintf("/dev/pts/%d", n), nil
}

type ctl struct {
	m    *os.File
	out  chan string
	buf  string
	proc *exec.Cmd
}

func start() (*ctl, error) {
	m, slavePath, err := openPTY()
	if err != nil {
		return nil, err
	}
	s, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("bluetoothctl", "--agent", "KeyboardDisplay")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s, s, s
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s.Close()
	c := &ctl{m: m, out: make(chan string, 64), proc: cmd}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := m.Read(b)
			if n > 0 {
				c.out <- stripANSI(string(b[:n]))
			}
			if err != nil {
				close(c.out)
				return
			}
		}
	}()
	return c, nil
}

func (c *ctl) send(line string) { _, _ = c.m.WriteString(line + "\n") }

func (c *ctl) read(d time.Duration) string {
	end := time.After(d)
	var sb strings.Builder
	for {
		select {
		case s, ok := <-c.out:
			if !ok {
				return sb.String()
			}
			sb.WriteString(s)
		case <-end:
			return sb.String()
		}
	}
}

func (c *ctl) waitFor(re *regexp.Regexp, d time.Duration, onText func(string)) string {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		t := c.read(500 * time.Millisecond)
		if t == "" {
			continue
		}
		c.buf += t
		if onText != nil {
			onText(t)
		}
		if m := re.FindString(c.buf); m != "" {
			return m
		}
	}
	return ""
}

func big(code string) {
	fmt.Println("\n" + strings.Repeat("=", 46))
	fmt.Printf("   ESCRIBE EN EL TECLADO:   %s   y luego Enter\n", code)
	fmt.Println(strings.Repeat("=", 46) + "\n")
}

func run(name string) int {
	fmt.Printf("Emparejar «%s»\n\n", name)
	fmt.Println("1. Pon el teclado en modo emparejamiento: mantén pulsada una tecla Easy-Switch")
	fmt.Println("   (1, 2 o 3) unos 3 segundos, hasta que su luz parpadee RÁPIDO.")
	fmt.Println("   Usa un canal libre, o el que antes usabas en Windows (este lo reemplaza).")
	fmt.Print("\n   Cuando parpadee rápido, pulsa Enter aquí... ")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')

	c, err := start()
	if err != nil {
		fmt.Println("   No pude abrir bluetoothctl:", err)
		return 1
	}
	defer func() { c.send("quit"); _ = c.proc.Wait() }()
	c.read(1500 * time.Millisecond)
	c.send("power on")
	c.send("pairable on")
	c.send("default-agent")
	c.read(time.Second)

	// Forget stale, half-paired entries with this name (the address can change per channel).
	c.send("devices")
	listing := c.read(1500 * time.Millisecond)
	for _, m := range regexp.MustCompile(`Device ([0-9A-F:]{17}) `+regexp.QuoteMeta(name)).FindAllStringSubmatch(listing, -1) {
		fmt.Printf("   Quitando el registro anterior %s\n", m[1])
		c.send("remove " + m[1])
		c.read(1500 * time.Millisecond)
	}

	fmt.Println("\n2. Buscando el teclado (hasta 90 s)...")
	c.buf = ""
	c.send("scan le")
	// LE devices often appear by address first and get their name a moment later.
	re := deviceRe(name)
	mac := ""
	end := time.Now().Add(90 * time.Second)
	for time.Now().Before(end) && mac == "" {
		c.buf += c.read(3 * time.Second)
		m := re.FindStringSubmatch(c.buf)
		if m == nil {
			c.send("devices")
			c.buf += c.read(time.Second)
			m = re.FindStringSubmatch(c.buf)
		}
		if m != nil {
			mac = m[1]
		}
	}
	if mac == "" {
		c.send("scan off")
		fmt.Println("   No apareció. ¿La luz parpadeaba rápido? Vuelve a intentarlo.")
		seen := map[string]bool{}
		for _, m := range anyDevice.FindAllStringSubmatch(c.buf, -1) {
			seen[m[1]+" "+strings.TrimSpace(m[2])] = true
		}
		list := make([]string, 0, len(seen))
		for s := range seen {
			list = append(list, s)
		}
		sort.Strings(list)
		if len(list) > 8 {
			list = list[:8]
		}
		if len(list) == 0 {
			list = []string{"ninguno"}
		}
		fmt.Println("   Dispositivos vistos:", strings.Join(list, ", "))
		fmt.Println("   Si tienes el receptor Logi Bolt, prueba otro canal Easy-Switch (el 1 suele estar ligado al receptor)")
		fmt.Println("   o vincúlalo al receptor con: solaar pair")
		return 1
	}
	fmt.Println("   Encontrado:", mac)
	c.send("scan off")
	c.read(time.Second)

	fmt.Println("\n3. Emparejando...")
	c.buf = ""
	c.send("pair " + mac)
	shown := map[string]bool{}
	res := c.waitFor(regexp.MustCompile(`Pairing successful|Failed to pair[^\n]*|AuthenticationFailed|AuthenticationCanceled`), 90*time.Second, func(t string) {
		for _, code := range passkeys(t) {
			if !shown[code] {
				shown[code] = true
				big(code)
			}
		}
		if strings.Contains(t, "Confirm passkey") {
			c.send("yes")
		}
	})
	if !strings.Contains(res, "successful") {
		if res == "" {
			res = "se agotó el tiempo"
		}
		fmt.Println("\n   No se emparejó:", res)
		fmt.Println("   Repite: luz parpadeando rápido, y escribe el código en el teclado + Enter a tiempo.")
		return 1
	}
	fmt.Println("   Emparejado.")
	c.send("trust " + mac)
	c.read(time.Second)
	c.send("connect " + mac)
	c.waitFor(regexp.MustCompile(`Connection successful|Failed to connect`), 20*time.Second, nil)
	c.buf = ""
	c.send("info " + mac)
	info := c.read(2 * time.Second)
	paired := strings.Contains(info, "Paired: yes") && strings.Contains(info, "Bonded: yes")
	connected := strings.Contains(info, "Connected: yes")
	yn := func(b bool) string {
		if b {
			return "sí"
		}
		return "NO"
	}
	fmt.Printf("\n   Paired/Bonded: %s · Conectado: %s\n", yn(paired), yn(connected))
	if paired && connected {
		fmt.Println("   Escribe algo con el teclado para probarlo.")
	} else {
		fmt.Println("   Revisa: journalctl -u bluetooth -b")
	}
	if paired {
		return 0
	}
	return 1
}

func main() {
	name := "Wave Keys"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	os.Exit(run(name))
}
