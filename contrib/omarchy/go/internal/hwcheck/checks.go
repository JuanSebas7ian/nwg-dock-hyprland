package hwcheck

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	OK   = "ok"
	INFO = "info"
	WARN = "warn"
	FAIL = "fail"
)

var levelRank = map[string]int{OK: 0, INFO: 0, WARN: 1, FAIL: 2}

// Classes that normally have no driver: host bridges, ISA bridge, IOMMU.
var pciClassesWithoutDriver = []string{"0x0600", "0x0601", "0x0806"}

// Loaded module -> firmware package it needs (Arch's firmware is split).
var firmwarePackages = []struct {
	re  *regexp.Regexp
	pkg string
}{
	{regexp.MustCompile(`^(?:amdgpu)$`), "linux-firmware-amdgpu"},
	{regexp.MustCompile(`^(?:radeon)$`), "linux-firmware-radeon"},
	{regexp.MustCompile(`^(?:mt76.*|mt79.*|btmtk)$`), "linux-firmware-mediatek"},
	{regexp.MustCompile(`^(?:iwlwifi|iwlmvm|btintel|i915|xe)$`), "linux-firmware-intel"},
	{regexp.MustCompile(`^(?:r8169|rtw8.*|rtw88_.*|rtw89_.*|btrtl)$`), "linux-firmware-realtek"},
	{regexp.MustCompile(`^(?:ath1[012]k.*|ath9k_htc)$`), "linux-firmware-atheros"},
	{regexp.MustCompile(`^(?:brcmfmac|brcmsmac|btbcm)$`), "linux-firmware-broadcom"},
	{regexp.MustCompile(`^(?:nouveau)$`), "linux-firmware-nvidia"},
	{regexp.MustCompile(`^(?:snd_sof.*)$`), "sof-firmware"},
}

var conflictingPackages = [][2]string{{"nvidia", "nvidia-open"}, {"nvidia", "nvidia-open-dkms"}, {"nvidia-dkms", "nvidia-open-dkms"},
	{"nvidia-open", "nvidia-open-dkms"}, {"nvidia-dkms", "nvidia"}, {"pulseaudio", "pipewire-pulse"}, {"jack2", "pipewire-jack"}}

var conflictingServices = [][2]string{{"tlp.service", "power-profiles-daemon.service"}, {"iwd.service", "wpa_supplicant.service"},
	{"NetworkManager.service", "systemd-networkd.service"}}

// DriverPackageRe: packages whose version is part of the driver state.
var DriverPackageRe = regexp.MustCompile(`^(linux(-lts|-zen|-hardened|-omarchy|-t2)?(-headers)?|linux-firmware.*|nvidia.*|lib32-nvidia.*|` +
	`.*-dkms|dkms|mesa|lib32-mesa|vulkan-.*|lib32-vulkan-.*|bluez.*|amd-ucode|intel-ucode|` +
	`sof-firmware|alsa-firmware|pipewire|wireplumber|fwupd)$`)

// Finding is one check result; the id is stable so it can be searched and compared.
// Fields are in alphabetical order: the JSON matches Python's sort_keys.
type Finding struct {
	Category string   `json:"category"`
	Details  []string `json:"details"`
	Hint     string   `json:"hint"`
	ID       string   `json:"id"`
	Level    string   `json:"level"`
	Title    string   `json:"title"`
}

func fnd(id, cat, level, title string, details []string, hint string) *Finding {
	if details == nil {
		details = []string{}
	}
	return &Finding{cat, details, hint, id, level, title}
}

func firstN(l []string, n int) []string {
	if len(l) > n {
		return l[:n]
	}
	return l
}

func checkPCI(f *Facts) *Finding {
	var missing []string
	for _, slot := range sortedKeys(f.PCI) {
		d := f.PCI[slot]
		skip := false
		for _, c := range pciClassesWithoutDriver {
			skip = skip || strings.HasPrefix(d.Class, c)
		}
		if d.Driver == "" && !skip {
			missing = append(missing, strings.TrimRight(fmt.Sprintf("%s [%s] %s", slot, d.ID, d.Name), " "))
		}
	}
	if len(missing) > 0 {
		return fnd("HW01", "hw", WARN, fmt.Sprintf("%d dispositivo(s) PCI sin driver", len(missing)), missing,
			"Busca el id [vendor:device] en https://linux-hardware.org o con `modinfo`/`lspci -k`.")
	}
	return fnd("HW01", "hw", OK, fmt.Sprintf("Los %d dispositivos PCI tienen driver (salvo puentes)", len(f.PCI)), nil, "")
}

func checkUSB(f *Facts) *Finding {
	if !f.KernelOK {
		return fnd("HW02", "hw", INFO, "No se pudo leer el registro del kernel (journalctl -k)", nil, "")
	}
	errs := UsbEnumErrors(f.KernelLog)
	if len(errs) > 0 {
		var det []string
		for _, k := range sortedKeys(errs) {
			det = append(det, "usb "+k) // no count: it grows over time and would break the comparison
		}
		return fnd("HW02", "hw", WARN, fmt.Sprintf("%d dispositivo(s) USB no lograron conectarse en este arranque", len(errs)), det,
			"Un dispositivo que no responde retrasa el arranque y no tiene driver que lo arregle: "+
				"revisa el cable/puerto, o corte total de energía si es interno (Bluetooth 6-11).")
	}
	return fnd("HW02", "hw", OK, "Sin errores de enumeración USB en este arranque", nil, "")
}

func checkFirmwareLoad(f *Facts) *Finding {
	if !f.KernelOK {
		return fnd("HW03", "hw", INFO, "No se pudo leer el registro del kernel: firmware sin verificar", nil, "")
	}
	if failed := FirmwareFailures(f.KernelLog); len(failed) > 0 {
		return fnd("HW03", "hw", FAIL, fmt.Sprintf("%d firmware(s) no se pudieron cargar", len(failed)), failed,
			"Instala el paquete que lo contiene: `pacman -F <archivo>` y `omarchy pkg add <paquete>`.")
	}
	return fnd("HW03", "hw", OK, "Ningún firmware falló al cargar en este arranque", nil, "")
}

func checkFirmwarePackages(f *Facts) *Finding {
	needed := map[string][]string{}
	for _, mod := range f.Loaded {
		for _, fp := range firmwarePackages {
			if fp.re.MatchString(mod) {
				needed[fp.pkg] = append(needed[fp.pkg], mod)
			}
		}
	}
	var missing, have []string
	for _, pkg := range sortedKeys(needed) {
		mods := strings.Join(needed[pkg], ", ")
		have = append(have, pkg+": "+mods)
		if !f.has(pkg) {
			if rc, _ := f.sys.Run(60*time.Second, "pacman", "-T", pkg); rc != 0 {
				missing = append(missing, fmt.Sprintf("%s (lo usa %s)", pkg, mods))
			}
		}
	}
	if len(missing) > 0 {
		return fnd("HW04", "hw", FAIL, "Faltan paquetes de firmware para drivers cargados", missing, "`omarchy pkg add <paquete>` y reinicia.")
	}
	return fnd("HW04", "hw", OK, "Paquetes de firmware presentes para los drivers cargados", have, "")
}

// nvidiaVersions: loaded, nvidia-utils, {installed kernel: module version on disk}.
func nvidiaVersions(f *Facts) (string, string, map[string]string) {
	loaded := strings.TrimSpace(f.read("/sys/module/nvidia/version"))
	utils := ""
	if v, ok := f.Packages["nvidia-utils"]; ok {
		utils = UpstreamVersion(v)
	}
	onDisk := map[string]string{}
	for _, rel := range sortedKeys(f.Kernels) {
		rc, out := f.sys.Run(60*time.Second, "modinfo", "-k", rel, "-F", "version", "nvidia")
		onDisk[rel] = ""
		if rc == 0 {
			onDisk[rel] = strings.TrimSpace(out)
		}
	}
	return loaded, utils, onDisk
}

func checkNvidiaFirmware(f *Facts) *Finding {
	if !f.isLoaded("nvidia") {
		return nil
	}
	loaded, utils, _ := nvidiaVersions(f)
	// nvidia-utils installs the firmware: after an update the loaded version's folder is gone and what
	// matters is the new version's, used after the reboot.
	version := utils
	if version == "" {
		version = loaded
	}
	p := "/usr/lib/firmware/nvidia/" + version
	if version != "" && !f.sys.Exists(p) {
		return fnd("HW05", "hw", FAIL, "Falta el firmware GSP de NVIDIA "+version, []string{p},
			"Lo instala nvidia-utils: reinstálalo con la misma versión del módulo.")
	}
	if utils != "" && loaded != "" && utils != loaded {
		return fnd("HW05", "hw", WARN, fmt.Sprintf("Firmware GSP de NVIDIA %s listo; el módulo cargado es %s", utils, loaded), []string{p},
			"NVIDIA se actualizó: reinicia para usarlo.")
	}
	return fnd("HW05", "hw", OK, fmt.Sprintf("Firmware GSP de NVIDIA %s presente", version), nil, "")
}

func enabled(f *Facts, unit string) bool {
	_, out := f.sys.Run(60*time.Second, "systemctl", "is-enabled", unit)
	return strings.TrimSpace(out) == "enabled"
}

func checkBluetooth(f *Facts) *Finding {
	en := enabled(f, "bluetooth.service")
	if len(f.BTAdapters) > 0 {
		det := []string{"adaptadores: " + strings.Join(f.BTAdapters, ", ")}
		for _, r := range f.Rfkill {
			if r.Type == "bluetooth" && r.Soft {
				return fnd("HW06", "hw", INFO, "Bluetooth presente pero apagado (rfkill soft)", det, "Encenderlo: `omarchy bluetooth power on` o SUPER+CTRL+B.")
			}
		}
		return fnd("HW06", "hw", OK, "Adaptador Bluetooth presente", det, "")
	}
	if f.has("bluez") && en {
		stuck := sortedKeys(UsbEnumErrors(f.KernelLog))
		bt := "no"
		if f.isLoaded("btusb") {
			bt = "sí"
		}
		s := strings.Join(stuck, ", ")
		if s == "" {
			s = "ninguno"
		}
		return fnd("HW06", "hw", WARN, "No hay adaptador Bluetooth (bluetooth.service habilitado)",
			[]string{"btusb cargado: " + bt, "USB que no enumeran (uno puede ser el chip Bluetooth): " + s},
			"Si el chip no enumera no es un driver: corte total de energía. Ver "+btReference)
	}
	return fnd("HW06", "hw", INFO, "Sin Bluetooth (ni adaptador ni servicio habilitado)", nil, "")
}

func checkRfkill(f *Facts) *Finding {
	var hard []string
	for _, r := range f.Rfkill {
		if r.Hard {
			hard = append(hard, r.Type+" "+r.Name)
		}
	}
	if len(hard) > 0 {
		return fnd("HW07", "hw", WARN, "Radios bloqueadas por hardware (rfkill hard)", hard, "Interruptor físico, tecla de modo avión o ajuste de BIOS.")
	}
	return fnd("HW07", "hw", OK, "Ninguna radio bloqueada por hardware", nil, "")
}

func checkRunningKernel(f *Facts) *Finding {
	if !f.sys.Exists("/usr/lib/modules/" + f.Release) {
		inst := strings.Join(sortedKeys(f.Kernels), ", ")
		if inst == "" {
			inst = "?"
		}
		return fnd("UP01", "updates", WARN, fmt.Sprintf("El kernel en uso (%s) ya no está instalado", f.Release), []string{"instalados: " + inst},
			"Reinicia: hasta entonces no se pueden cargar módulos nuevos (USB, Bluetooth...).")
	}
	return fnd("UP01", "updates", OK, fmt.Sprintf("El kernel en uso (%s) está instalado", f.Release), nil, "")
}

func checkHeaders(f *Facts) *Finding {
	if !f.has("dkms") {
		return nil
	}
	var bad []string
	for _, rel := range sortedKeys(f.Kernels) {
		pkg := f.Kernels[rel]
		want, have := f.Packages[pkg], f.Packages[pkg+"-headers"]
		if have != want {
			h := have
			if h == "" {
				h = "NO instalado"
			}
			w := want
			if _, ok := f.Packages[pkg]; !ok {
				w = "None"
			}
			bad = append(bad, fmt.Sprintf("%s %s: %s-headers %s", pkg, w, pkg, h))
		}
	}
	if len(bad) > 0 {
		return fnd("UP02", "updates", FAIL, "Headers del kernel no coinciden: DKMS no podrá compilar", bad, "`omarchy pkg add <kernel>-headers` y actualiza ambos juntos.")
	}
	return fnd("UP02", "updates", OK, "Headers instalados y a la par de cada kernel", nil, "")
}

func checkDkms(f *Facts) *Finding {
	if len(f.Dkms) == 0 {
		return nil
	}
	built := map[[2]string]bool{}
	modset := map[string]bool{}
	staleSet := map[string]bool{}
	var warned []string
	for _, r := range f.Dkms {
		modset[r.Mod] = true
		if r.Kernel != "" && strings.HasPrefix(r.Status, "installed") {
			built[[2]string{r.Mod, r.Kernel}] = true
		}
		if _, ok := f.Kernels[r.Kernel]; r.Kernel != "" && !ok {
			staleSet[fmt.Sprintf("%s/%s para %s", r.Mod, r.Ver, r.Kernel)] = true
		}
		if strings.Contains(r.Status, "WARNING") || strings.Contains(r.Status, "Diff") {
			warned = append(warned, fmt.Sprintf("%s/%s %s: %s", r.Mod, r.Ver, r.Kernel, r.Status))
		}
	}
	modules := sortedKeys(modset)
	var missing []string
	for _, rel := range sortedKeys(f.Kernels) {
		for _, m := range modules {
			if !built[[2]string{m, rel}] {
				missing = append(missing, m+" para "+rel)
			}
		}
	}
	if len(missing) > 0 {
		return fnd("UP03", "updates", FAIL, "Módulos DKMS sin compilar para un kernel instalado", append(missing, warned...),
			"NO reinicies hasta resolverlo (riesgo de pantalla negra con NVIDIA): "+
				"`sudo dkms autoinstall -k <kernel>` y revisa /var/lib/dkms/<mod>/<ver>/build/make.log.")
	}
	if len(warned) > 0 {
		return fnd("UP03", "updates", WARN, "DKMS avisa de módulos distintos a los compilados", warned, "")
	}
	var det []string
	ks := strings.Join(sortedKeys(f.Kernels), ", ")
	for _, m := range modules {
		det = append(det, m+": "+ks)
	}
	for _, s := range sortedKeys(staleSet) {
		det = append(det, "(sobrante) "+s)
	}
	return fnd("UP03", "updates", OK, "Módulos DKMS compilados para todos los kernels", det, "")
}

func checkNvidiaVersion(f *Facts) *Finding {
	if !f.isLoaded("nvidia") || !f.has("nvidia-utils") {
		return nil
	}
	loaded, utils, onDisk := nvidiaVersions(f)
	det := []string{fmt.Sprintf("cargado %s, nvidia-utils %s", loaded, utils)}
	allSame := len(onDisk) > 0
	for _, rel := range sortedKeys(onDisk) {
		v := onDisk[rel]
		allSame = allSame && v == utils
		if v == "" {
			v = "no está"
		}
		det = append(det, fmt.Sprintf("en disco para %s: %s", rel, v))
	}
	if lib32, ok := f.Packages["lib32-nvidia-utils"]; ok && lib32 != "" && UpstreamVersion(lib32) != utils {
		return fnd("UP04", "updates", WARN, "lib32-nvidia-utils no coincide con nvidia-utils", append(det, "lib32-nvidia-utils "+UpstreamVersion(lib32)),
			"Actualiza ambos juntos (juegos de 32 bits/Steam fallarían).")
	}
	if loaded == utils {
		return fnd("UP04", "updates", OK, fmt.Sprintf("NVIDIA %s: módulo y nvidia-utils coinciden", loaded), det, "")
	}
	if allSame {
		return fnd("UP04", "updates", WARN, "NVIDIA actualizado: falta reiniciar", det, "Reinicia para cargar el módulo nuevo.")
	}
	return fnd("UP04", "updates", FAIL, "El módulo NVIDIA en disco no coincide con nvidia-utils", det,
		"Revisa UP03 (DKMS) y reinstala nvidia-open-dkms/nvidia-utils a la misma versión.")
}

func checkCmdlinePending(f *Facts) *Finding {
	conf := f.LimineTokens()
	if len(conf) == 0 {
		return fnd("UP05", "updates", INFO, "No se pudo leer la configuración de Limine (/etc/default/limine)", nil, "")
	}
	running := map[string]bool{}
	for _, t := range f.Cmdline {
		running[t] = true
	}
	var pending []string
	for _, t := range sortedKeys(conf) {
		if !running[t] {
			pending = append(pending, fmt.Sprintf("%s  (%s)", t, base(conf[t])))
		}
	}
	if len(pending) > 0 {
		return fnd("UP05", "updates", WARN, "Parámetros del kernel configurados que aún no están activos", pending,
			"Reinicia. Si siguen faltando después, ejecuta `sudo limine-update`.")
	}
	return fnd("UP05", "updates", OK, "La línea de arranque en uso coincide con la configurada", nil, "")
}

func checkFailedUnits(f *Facts) *Finding {
	var failed []string
	for _, scope := range [][]string{{}, {"--user"}} {
		cmd := append(append([]string{"systemctl"}, scope...), "--failed", "--no-legend", "--plain")
		_, out := f.sys.Run(60*time.Second, cmd...)
		for _, line := range splitLines(out) {
			if w := strings.Fields(line); len(w) > 0 {
				who := "sistema: "
				if len(scope) > 0 {
					who = "usuario: "
				}
				failed = append(failed, who+w[0])
			}
		}
	}
	sort.Strings(failed)
	if len(failed) > 0 {
		return fnd("UP06", "updates", WARN, fmt.Sprintf("%d servicio(s) fallidos", len(failed)), failed,
			"`systemctl [--user] status <unidad>` y `journalctl -b -u <unidad>`.")
	}
	return fnd("UP06", "updates", OK, "Ningún servicio fallido", nil, "")
}

func checkRebootFlags(f *Facts) *Finding {
	reasons := map[string]bool{}
	if f.sys.Exists(f.sys.Home() + "/.local/state/omarchy/reboot-required") {
		reasons["Omarchy marcó que la actualización necesita reiniciar"] = true
	}
	_, out := f.sys.Run(60*time.Second, "pgrep", "-x", "Hyprland")
	for _, pid := range strings.Fields(out) {
		if l, _ := f.sys.Readlink("/proc/" + pid + "/exe"); strings.Contains(l, "(deleted)") {
			reasons["Hyprland se actualizó mientras corría"] = true
		}
	}
	if len(reasons) > 0 {
		return fnd("UP07", "updates", WARN, "Reinicio pendiente", sortedKeys(reasons), "Reinicia.")
	}
	return fnd("UP07", "updates", OK, "Sin reinicio pendiente marcado por Omarchy", nil, "")
}

var (
	ansiRe     = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	logErrorRe = regexp.MustCompile(`^(error:|ERROR|Hook failed|==> ERROR|Something went wrong)`)
)

func runes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func checkUpdateLog(f *Facts) *Finding {
	text, ok := f.sys.Read(UpdateLog)
	if !ok {
		return fnd("UP08", "updates", INFO, "Sin registro de `omarchy update` desde que arrancó el equipo", nil, "")
	}
	var lines []string
	wrong := false
	for _, raw := range splitLines(strings.ReplaceAll(text, "\r", "\n")) {
		// Indentation kept: this validator's own (indented) details land in the log, because the hook runs
		// inside `omarchy update`, and must not count as errors.
		line := strings.TrimRight(ansiRe.ReplaceAllString(raw, ""), " \t\n\r\v\f")
		if logErrorRe.MatchString(line) {
			if strings.HasPrefix(line, "error: failed retrieving file") { // one mirror failed; pacman used another
				continue
			}
			lines = append(lines, runes(line, 160))
			wrong = wrong || strings.HasPrefix(line, "Something went wrong")
		}
	}
	if wrong {
		return fnd("UP08", "updates", FAIL, "La última `omarchy update` terminó con error", firstN(lines, 12),
			fmt.Sprintf("Lee %s y vuelve a ejecutar `omarchy update`.", UpdateLog))
	}
	if len(lines) > 0 {
		return fnd("UP08", "updates", WARN, "La última `omarchy update` registró errores", firstN(lines, 12), fmt.Sprintf("Revisa %s.", UpdateLog))
	}
	return fnd("UP08", "updates", OK, "La última `omarchy update` no registró errores", nil, "")
}

func checkPendingUpdates(f *Facts) *Finding {
	rc, out := f.sys.Run(180*time.Second, "checkupdates")
	if rc == 127 {
		return fnd("UP09", "updates", INFO, "checkupdates no está instalado (paquete pacman-contrib)", nil, "")
	}
	if rc != 0 && rc != 2 {
		return fnd("UP09", "updates", INFO, "No se pudo consultar las actualizaciones (¿sin red?)", nil, "")
	}
	var rows, drivers []string
	for _, l := range splitLines(out) {
		if l = strings.TrimSpace(l); l != "" {
			rows = append(rows, l)
		}
	}
	sort.Strings(rows)
	if len(rows) == 0 {
		return fnd("UP09", "updates", OK, "El sistema está al día", nil, "")
	}
	kernelPkgs := map[string]bool{}
	for _, p := range f.Kernels {
		kernelPkgs[p] = true
	}
	newKernel := false
	for _, r := range rows {
		name := strings.Fields(r)[0]
		if DriverPackageRe.MatchString(name) {
			drivers = append(drivers, r)
			newKernel = newKernel || kernelPkgs[name]
		}
	}
	hint := "Actualiza con `omarchy update`; el hook de omarchy-hwcheck valida DKMS antes de que reinicies."
	if newKernel && len(f.Dkms) > 0 {
		hint = "Viene un kernel nuevo y hay módulos DKMS: tras `omarchy update` revisa UP02/UP03 ANTES de reiniciar."
	}
	return fnd("UP09", "updates", INFO, fmt.Sprintf("%d actualización(es) pendientes, %d de drivers/kernel", len(rows), len(drivers)), drivers, hint)
}

func checkPacmanDB(f *Facts) *Finding {
	rc, out := f.sys.Run(60*time.Second, "pacman", "-Dk")
	if rc == 127 {
		return nil
	}
	if rc != 0 {
		var l []string
		for _, x := range splitLines(out) {
			if strings.TrimSpace(x) != "" {
				l = append(l, x)
			}
		}
		return fnd("CF01", "conflicts", FAIL, "La base de datos de pacman tiene dependencias rotas o conflictos", firstN(l, 15),
			"Suele ser una actualización parcial: ejecuta `omarchy update` completo.")
	}
	return fnd("CF01", "conflicts", OK, "Base de datos de pacman consistente (pacman -Dk)", nil, "")
}

func checkPacnew(f *Facts) *Finding {
	if files := f.sys.WalkFiles("/etc", ".pacnew", ".pacsave"); len(files) > 0 {
		return fnd("CF02", "conflicts", WARN, fmt.Sprintf("%d archivo(s) .pacnew/.pacsave sin resolver", len(files)), files,
			"Compara y combina (`sudo pacdiff`, de pacman-contrib); luego borra el .pacnew.")
	}
	return fnd("CF02", "conflicts", OK, "Sin .pacnew/.pacsave pendientes en /etc", nil, "")
}

func checkReplaced(f *Facts) *Finding {
	if len(f.sys.ListDir(ReplacedDir)) > 0 {
		var rel []string
		for _, p := range f.sys.WalkFiles(ReplacedDir, "") {
			rel = append(rel, p[len(ReplacedDir):])
		}
		return fnd("CF03", "conflicts", WARN, "`omarchy update` apartó archivos para resolver un conflicto", firstN(rel, 20),
			fmt.Sprintf("Son archivos que pacman no tenía registrados. Revisa si necesitas algo de %s y bórralos cuando no.", ReplacedDir))
	}
	return fnd("CF03", "conflicts", OK, "Ningún archivo apartado por conflictos de actualización", nil, "")
}

type valSrc struct{ v, src string }

func checkModuleOptions(f *Facts) *Finding {
	opts, _ := f.ModprobeConfig()
	values := map[[2]string][]valSrc{}
	for _, o := range opts {
		k := [2]string{o.mod, o.key}
		values[k] = append(values[k], valSrc{o.val, o.src})
	}
	cp := cmdlineParams(f.Cmdline)
	for _, k := range cp.keys {
		if strings.Contains(k, ".") {
			mod, param, _ := strings.Cut(k, ".")
			key := [2]string{normMod(mod), param}
			values[key] = append(values[key], valSrc{cp.vals[k], "/proc/cmdline"})
		}
	}
	keys := make([][2]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	var conflicts []string
	for _, k := range keys {
		vals := values[k]
		norm := map[string]bool{}
		var parts []string
		for _, v := range vals {
			norm[boolNorm(v.v)] = true
			parts = append(parts, v.v+" en "+v.src)
		}
		if len(norm) > 1 {
			conflicts = append(conflicts, fmt.Sprintf("%s.%s: %s", k[0], k[1], strings.Join(parts, "; ")))
		}
	}
	if len(conflicts) > 0 {
		return fnd("CF04", "conflicts", WARN, "Opciones de módulo con valores contradictorios", conflicts,
			"Gana la línea de arranque, y luego el último archivo de modprobe.d en orden alfabético.")
	}
	return fnd("CF04", "conflicts", OK, "Sin opciones de módulo contradictorias", nil, "")
}

func checkBuiltinOptions(f *Facts) *Finding {
	opts, _ := f.ModprobeConfig()
	builtin := map[string]bool{}
	for _, b := range f.Builtin {
		builtin[b] = true
	}
	onCmd := map[[2]string]string{}
	cp := cmdlineParams(f.Cmdline)
	for _, k := range cp.keys {
		if strings.Contains(k, ".") {
			mod, param, _ := strings.Cut(k, ".")
			onCmd[[2]string{normMod(mod), param}] = cp.vals[k]
		}
	}
	ignored, covered := map[string]bool{}, map[string]bool{}
	for _, o := range opts {
		if !builtin[o.mod] {
			continue
		}
		if v, ok := onCmd[[2]string{o.mod, o.key}]; ok && v == o.val { // the same value is already on the kernel command line
			covered[fmt.Sprintf("%s.%s=%s en %s (ya está en la línea de arranque)", o.mod, o.key, o.val, o.src)] = true
		} else {
			ignored[fmt.Sprintf("%s.%s=%s en %s", o.mod, o.key, o.val, o.src)] = true
		}
	}
	if len(ignored) == 0 && len(covered) > 0 {
		return fnd("CF05", "conflicts", INFO, "Opciones de modprobe.d para módulos integrados, aplicadas por la línea de arranque", sortedKeys(covered), "")
	}
	if len(ignored) > 0 {
		return fnd("CF05", "conflicts", WARN, "Opciones en modprobe.d para módulos integrados en el kernel: no tienen efecto", sortedKeys(ignored),
			"Un módulo integrado solo lee la línea de arranque: pon `modulo.opcion=valor` como "+
				"parámetro del kernel (kernel-param.sh) si de verdad la necesitas.")
	}
	return fnd("CF05", "conflicts", OK, "Ninguna opción de modprobe.d se pierde en un módulo integrado", nil, "")
}

func checkEffectiveParams(f *Facts) *Finding {
	expected := map[[2]string]valSrc{}
	opts, _ := f.ModprobeConfig()
	for _, o := range opts {
		if f.isLoaded(o.mod) {
			expected[[2]string{o.mod, o.key}] = valSrc{o.val, o.src}
		}
	}
	cp := cmdlineParams(f.Cmdline)
	for _, k := range cp.keys {
		if strings.Contains(k, ".") {
			mod, param, _ := strings.Cut(k, ".")
			expected[[2]string{normMod(mod), param}] = valSrc{cp.vals[k], "/proc/cmdline"}
		}
	}
	keys := make([][2]string, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	var wrong []string
	checked := 0
	for _, k := range keys {
		actual, ok := f.sys.Read(fmt.Sprintf("/sys/module/%s/parameters/%s", k[0], k[1]))
		if !ok {
			continue
		}
		checked++
		e := expected[k]
		if !sameParamValue(e.v, actual) && !sameParamArray(e.v, actual) {
			wrong = append(wrong, fmt.Sprintf("%s.%s: pedido %s (%s), en uso %s", k[0], k[1], e.v, e.src, strings.TrimSpace(actual)))
		}
	}
	if len(wrong) > 0 {
		return fnd("CF06", "conflicts", WARN, "Parámetros de módulo que no tomaron el valor pedido", wrong,
			"Otro archivo o la línea de arranque lo pisa, o el módulo se cargó antes (initramfs).")
	}
	return fnd("CF06", "conflicts", OK, fmt.Sprintf("%d parámetro(s) de módulo con el valor pedido", checked), nil, "")
}

func checkBlacklistedLoaded(f *Facts) *Finding {
	_, black := f.ModprobeConfig()
	wanted := f.IntentionalLoads()
	bad, meant := map[string]bool{}, map[string]bool{}
	for _, b := range black {
		if !f.isLoaded(b[0]) {
			continue
		}
		if rule, ok := wanted[b[0]]; ok {
			meant[fmt.Sprintf("%s (lista negra en %s; lo carga a propósito %s)", b[0], b[1], rule)] = true
		} else {
			bad[fmt.Sprintf("%s (lista negra en %s)", b[0], b[1])] = true
		}
	}
	if len(bad) > 0 {
		return fnd("CF07", "conflicts", FAIL, "Módulos en lista negra que igual están cargados", sortedKeys(bad),
			"Probablemente vienen del initramfs: `sudo limine-update` y reinicia.")
	}
	if len(meant) > 0 {
		return fnd("CF07", "conflicts", INFO, "Módulos en lista negra cargados a propósito", sortedKeys(meant), "")
	}
	return fnd("CF07", "conflicts", OK, "Ningún módulo en lista negra está cargado", nil, "")
}

func checkPackageConflicts(f *Facts) *Finding {
	var bad []string
	for _, p := range conflictingPackages {
		if f.has(p[0]) && f.has(p[1]) {
			bad = append(bad, p[0]+" y "+p[1])
		}
	}
	for _, s := range conflictingServices {
		if enabled(f, s[0]) && enabled(f, s[1]) {
			bad = append(bad, fmt.Sprintf("servicios %s y %s habilitados a la vez", s[0], s[1]))
		}
	}
	if len(bad) > 0 {
		return fnd("CF08", "conflicts", WARN, "Paquetes o servicios que compiten entre sí", bad,
			"Deja uno solo (`omarchy pkg drop <paquete>` o `systemctl disable <servicio>`).")
	}
	return fnd("CF08", "conflicts", OK, "Sin paquetes ni servicios que compitan entre sí", nil, "")
}

func checkVersionSkew(f *Facts) *Finding {
	var fw []string
	for _, p := range sortedKeys(f.Packages) {
		if p == "linux-firmware" || strings.HasPrefix(p, "linux-firmware-") {
			fw = append(fw, p)
		}
	}
	present := func(names ...string) []string {
		var out []string
		for _, n := range names {
			if f.has(n) {
				out = append(out, n)
			}
		}
		return out
	}
	groups := [][2]any{
		{"linux-firmware", fw},
		{"mesa", present("mesa", "lib32-mesa", "vulkan-radeon", "lib32-vulkan-radeon", "vulkan-intel", "lib32-vulkan-intel")},
		{"nvidia", present("nvidia-utils", "lib32-nvidia-utils", "nvidia-open-dkms", "nvidia-open", "nvidia-dkms", "nvidia", "opencl-nvidia")},
	}
	var skew []string
	for _, g := range groups {
		members := g[1].([]string)
		vers := map[string]bool{}
		var parts []string
		for _, p := range members {
			vers[UpstreamVersion(f.Packages[p])] = true
			parts = append(parts, p+" "+f.Packages[p])
		}
		if len(vers) > 1 {
			skew = append(skew, g[0].(string)+": "+strings.Join(parts, ", "))
		}
	}
	if len(skew) > 0 {
		return fnd("CF09", "conflicts", WARN, "Paquetes que deberían ir a la par tienen versiones distintas", skew,
			"Señal de actualización parcial: ejecuta `omarchy update` completo.")
	}
	return fnd("CF09", "conflicts", OK, "Firmware, Mesa y NVIDIA a la par entre sus paquetes", nil, "")
}

var checks = []func(*Facts) *Finding{
	checkPCI, checkUSB, checkFirmwareLoad, checkFirmwarePackages, checkNvidiaFirmware, checkBluetooth, checkRfkill,
	checkRunningKernel, checkHeaders, checkDkms, checkNvidiaVersion, checkCmdlinePending, checkFailedUnits, checkRebootFlags, checkUpdateLog,
	checkPacmanDB, checkPacnew, checkReplaced, checkModuleOptions, checkBuiltinOptions, checkEffectiveParams, checkBlacklistedLoaded,
	checkPackageConflicts, checkVersionSkew,
}

// RunChecks returns the findings sorted by id (only: categories to keep, nil = all).
func RunChecks(f *Facts, online bool, only map[string]bool) []*Finding {
	list := checks
	if online {
		list = append(append([]func(*Facts) *Finding{}, checks...), checkPendingUpdates)
	}
	var out []*Finding
	for _, c := range list {
		if r := c(f); r != nil && (only == nil || only[r.Category]) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func ExitCode(levels []string) int {
	m := 0
	for _, l := range levels {
		if levelRank[l] > m {
			m = levelRank[l]
		}
	}
	return m
}

// sameParamArray handles array parameters (e.g. snd_usb_audio.vid, one slot per card):
// sysfs prints every slot ("7532,-1,-1,…") while modprobe.d gives the first ones ("0x1d6c").
func sameParamArray(want, actual string) bool {
	got := strings.Split(strings.TrimSpace(actual), ",")
	if len(got) < 2 {
		return false
	}
	for i, w := range strings.Split(want, ",") {
		if i >= len(got) || !sameParamValue(strings.TrimSpace(w), got[i]) {
			return false
		}
	}
	return true
}
