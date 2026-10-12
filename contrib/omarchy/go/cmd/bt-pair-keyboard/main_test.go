package main

import (
	"reflect"
	"testing"
)

func TestStripANSIAndPasskeys(t *testing.T) {
	text := stripANSI("\x1b[0;94m[agent]\x1b[0m Passkey: 123456\r\n\x01[agent]\x02 Confirm passkey 654321 (yes/no):")
	if got := passkeys(text); !reflect.DeepEqual(got, []string{"123456", "654321"}) {
		t.Fatalf("%q %v", text, got)
	}
	if len(passkeys("Passkey: 12345")) != 0 {
		t.Fatal("5 digits accepted")
	}
}

func TestDeviceMatching(t *testing.T) {
	re := deviceRe("Wave Keys")
	for _, line := range []string{"[NEW] Device F1:22:33:44:55:66 Wave Keys", "[CHG] Device F1:22:33:44:55:66 Name: Wave Keys", "Device F1:22:33:44:55:66 Alias: Wave Keys"} {
		if m := re.FindStringSubmatch(line); m == nil || m[1] != "F1:22:33:44:55:66" {
			t.Fatalf("%q", line)
		}
	}
	if re.FindStringSubmatch("Device F1:22:33:44:55:66 MX Keys") != nil {
		t.Fatal("other keyboard matched")
	}
	if deviceRe("K380 (x)").FindStringSubmatch("Device AA:BB:CC:DD:EE:FF K380 (x)") == nil {
		t.Fatal("names are quoted literally")
	}
}

func TestOpenPTY(t *testing.T) {
	m, path, err := openPTY()
	if err != nil {
		t.Skip("no /dev/ptmx here:", err)
	}
	defer m.Close()
	if len(path) < 10 || path[:9] != "/dev/pts/" {
		t.Fatal(path)
	}
}
