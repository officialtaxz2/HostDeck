package leistung

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// state merkt sich, was HostDeck geändert hat (Dir/zustand.json).
type state struct {
	Logs *logState  `json:"logs,omitempty"`
	Swap *swapState `json:"swap,omitempty"`
}

type logState struct {
	Path    string      `json:"datei"`
	Existed bool        `json:"gab_es"`
	Mode    os.FileMode `json:"modus"`
	Written string      `json:"pruefsumme"` // so hat HostDeck die Datei geschrieben
	At      time.Time   `json:"zeit"`
}

type swapState struct {
	File string    `json:"datei"`
	Size int64     `json:"groesse"`
	At   time.Time `json:"zeit"`
}

func (s *Service) statePath() string { return filepath.Join(s.Dir, "zustand.json") }

func (s *Service) state() state {
	var st state
	if s.Dir == "" {
		return st
	}
	b, err := os.ReadFile(s.statePath())
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (s *Service) saveState(st state) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	return writeAtomic(s.statePath(), append(b, '\n'), 0o600)
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".hostdeck-neu"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Plan ist die Vorschau eines Vorschlags.
type Plan struct {
	Key     string   `json:"schluessel"`
	Title   string   `json:"titel"`
	Steps   []string `json:"schritte"`
	Effect  string   `json:"auswirkung"`
	Back    string   `json:"rueckweg"`
	Notes   []string `json:"hinweise,omitempty"`
	File    string   `json:"datei,omitempty"`
	Before  string   `json:"vorher,omitempty"` // nur die betroffenen Zeilen
	After   string   `json:"nachher,omitempty"`
	Problem string   `json:"problem,omitempty"`
	Stand   string   `json:"stand"`
}

// fingerprint fasst zusammen, worauf eine Vorschau beruht.
func (s *Service) fingerprint(key string) string {
	var parts []string
	daemon, _ := s.daemonPath()
	for _, p := range []string{daemon, s.fstab(), filepath.Join(s.proc(), "swaps"), s.statePath()} {
		b, _ := os.ReadFile(p)
		parts = append(parts, hashOf(append([]byte{1}, b...)))
	}
	if _, err := os.Stat(s.swapFile()); err == nil {
		parts = append(parts, "datei")
	}
	return hashOf([]byte(key + "|" + strings.Join(parts, "|")))[:16]
}

// Plan zeigt, was ein Vorschlag ändern würde.
func (s *Service) Plan(ctx context.Context, key string) (Plan, error) {
	return s.plan(ctx, key)
}

func (s *Service) plan(ctx context.Context, key string) (Plan, error) {
	st := s.state()
	p := Plan{Key: key, Stand: s.fingerprint(key)}
	switch key {
	case KeyLogs:
		s.planLogs(ctx, st, &p)
	case KeyLogsBack:
		s.planLogsBack(st, &p)
	case KeySwap:
		s.planSwap(st, &p)
	case KeySwapOff:
		s.planSwapOff(st, &p)
	default:
		return Plan{}, ErrNotFound
	}
	return p, nil
}

// daemonPath ist daemon.json, wie der laufende dockerd sie liest.
func (s *Service) daemonPath() (path string, flags []string) {
	path = s.daemonJSON()
	args := s.dockerdArgs()
	for i, a := range args {
		switch {
		case a == "--config-file" && i+1 < len(args):
			path = args[i+1]
		case strings.HasPrefix(a, "--config-file="):
			path = strings.TrimPrefix(a, "--config-file=")
		}
		if strings.HasPrefix(a, "--log-opt") || strings.HasPrefix(a, "--log-driver") {
			flags = append(flags, a)
		}
	}
	return path, flags
}

// dockerdArgs liest den Startbefehl des laufenden dockerd. Gibt es mehrere
// (Docker in Docker, rootless), zählt der, den systemd gestartet hat
// (Elternprozess 1).
func (s *Service) dockerdArgs() []string {
	dirs, _ := os.ReadDir(s.proc())
	var first []string
	for _, d := range dirs {
		if _, err := strconv.Atoi(d.Name()); err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(s.proc(), d.Name(), "comm"))
		if err != nil || strings.TrimSpace(string(comm)) != "dockerd" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.proc(), d.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
		if parentPID(filepath.Join(s.proc(), d.Name(), "stat")) == 1 {
			return args
		}
		if first == nil {
			first = args
		}
	}
	return first
}

// parentPID liest den Elternprozess aus /proc/<pid>/stat; 0: unbekannt.
func parentPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}

func logLines(d daemonConfig) string {
	var out []string
	if v, ok := d.cfg["log-driver"]; ok {
		out = append(out, `"log-driver": `+string(v))
	}
	if v, ok := d.cfg["log-opts"]; ok {
		var m map[string]string
		if json.Unmarshal(v, &m) == nil {
			b, _ := json.Marshal(m)
			out = append(out, `"log-opts": `+string(b))
		}
	}
	if len(out) == 0 {
		return "(keine Log-Einstellung, also ohne Grenze)"
	}
	return strings.Join(out, "\n")
}

// limitedConfig ist daemon.json mit der Grenze; alles andere bleibt.
func limitedConfig(d daemonConfig) (daemonConfig, []byte) {
	cfg := map[string]json.RawMessage{}
	for k, v := range d.cfg {
		cfg[k] = v
	}
	o := d.opts()
	o["max-size"] = LogMaxSize
	if o["max-file"] == "" {
		o["max-file"] = LogMaxFile
	}
	ob, _ := json.Marshal(o)
	cfg["log-opts"] = ob
	b, _ := json.MarshalIndent(cfg, "", "  ")
	b = append(b, '\n')
	return daemonConfig{raw: b, exists: true, cfg: cfg}, b
}

func (s *Service) daemonFile() (daemonConfig, string, []string) {
	path, flags := s.daemonPath()
	return readDaemonAt(path), path, flags
}

func (s *Service) planLogs(ctx context.Context, st state, p *Plan) {
	p.Title = "Container-Logs begrenzen"
	d, path, flags := s.daemonFile()
	p.File = path
	switch {
	case d.err != nil:
		p.Problem = d.err.Error() + "; HostDeck ändert sie nicht."
		return
	case len(flags) > 0:
		p.Problem = "Docker bekommt Log-Einstellungen über seinen Startbefehl (" + strings.Join(flags, " ") + "). Dann darf daemon.json sie nicht auch enthalten; HostDeck ändert nichts."
		return
	case limitedDriver(d.driver(), d.opts()):
		p.Problem = "Neue Container haben schon eine Log-Grenze."
		return
	}
	nd, _ := limitedConfig(d)
	p.Before, p.After = logLines(d), logLines(nd)
	p.Steps = []string{
		"Die neue daemon.json von Docker prüfen lassen (dockerd --validate).",
		"Die bisherige daemon.json als Vorher-Stand ablegen.",
		"Die Grenze in " + path + " eintragen. Alles andere darin bleibt, wie es ist.",
	}
	p.Effect = "Container schreiben höchstens " + LogMaxFile + " Log-Dateien zu je 10 MB. Das gilt ab dem nächsten Neustart von Docker (etwa wenn der Server neu startet) für Container, die danach neu angelegt werden, zum Beispiel beim Aktualisieren unter Projekte. HostDeck startet Docker dafür nicht neu und leert keine Logs."
	p.Back = "Unter Leistung „Log-Grenze zurücknehmen“. daemon.json ist dann wieder wie vorher."
	_, list := s.logs(ctx, st)
	var big []string
	var n int
	for _, c := range list {
		if c.Limited {
			continue
		}
		n++
		if len(big) < 3 && c.Size >= 50_000_000 {
			big = append(big, c.Name+" ("+Bytes(c.Size)+")")
		}
	}
	if n > 0 {
		note := fmt.Sprintf("%d Container behalten ihre Logs ohne Grenze, bis sie neu angelegt werden.", n)
		if n == 1 {
			note = "1 Container behält sein Log ohne Grenze, bis er neu angelegt wird."
		}
		if len(big) > 0 {
			note += " Die größten: " + strings.Join(big, ", ") + "."
		}
		p.Notes = append(p.Notes, note)
	}
}

func (s *Service) planLogsBack(st state, p *Plan) {
	p.Title = "Log-Grenze zurücknehmen"
	if st.Logs == nil {
		p.Problem = "HostDeck hat keine Log-Grenze eingetragen."
		return
	}
	p.File = st.Logs.Path
	d := readDaemonAt(st.Logs.Path)
	if hashOf(d.raw) != st.Logs.Written {
		p.Problem = ErrEdited.Error() + "."
		return
	}
	p.Before = logLines(d)
	if st.Logs.Existed {
		old := readDaemonAt(s.logBackup())
		if old.err != nil || !old.exists {
			p.Problem = "Der Vorher-Stand von daemon.json fehlt."
			return
		}
		p.After = logLines(old)
		p.Steps = []string{"daemon.json wieder so hinlegen, wie sie vor HostDeck war."}
	} else {
		p.After = "(keine daemon.json, wie vorher)"
		p.Steps = []string{"daemon.json entfernen; vor HostDeck gab es keine."}
	}
	p.Effect = "Ab dem nächsten Neustart von Docker haben neu angelegte Container wieder keine Log-Grenze. HostDeck startet Docker nicht neu."
	p.Back = "Die Grenze lässt sich danach unter Leistung wieder eintragen."
}

func (s *Service) logBackup() string   { return filepath.Join(s.Dir, "daemon.json.vorher") }
func (s *Service) fstabBackup() string { return filepath.Join(s.Dir, "fstab.vorher") }

// mountType ist das Dateisystem, auf dem path liegt.
func (s *Service) mountType(path string) string {
	b, err := os.ReadFile(filepath.Join(s.proc(), "mounts"))
	if err != nil {
		return ""
	}
	best, typ := -1, ""
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		mp := strings.ReplaceAll(f[1], `\040`, " ")
		if path == mp || strings.HasPrefix(path, strings.TrimSuffix(mp, "/")+"/") {
			if len(mp) > best {
				best, typ = len(mp), f[2]
			}
		}
	}
	return typ
}

// swapProblem sagt, warum HostDeck gerade keine Swap-Datei anlegen kann.
func (s *Service) swapProblem(size int64) string {
	f := s.swapFile()
	if _, err := os.Lstat(f); err == nil {
		return "Die Datei " + f + " gibt es schon."
	}
	dir := filepath.Dir(f)
	switch t := s.mountType(dir); t {
	case "btrfs", "zfs", "tmpfs", "overlay", "nfs", "nfs4", "cifs", "fuse.sshfs":
		return "Auf diesem Dateisystem (" + t + ") legt HostDeck keine Swap-Datei an."
	}
	if s.Disk != nil {
		_, free, err := s.Disk(dir)
		if err != nil {
			return "Freier Platz lässt sich nicht messen: " + err.Error()
		}
		if need := size + 5_000_000_000; free < need {
			return "Zu wenig Platz: frei sind " + Bytes(free) + ", HostDeck will danach noch 5 GB frei haben."
		}
	}
	return ""
}

func (s *Service) planSwap(st state, p *Plan) {
	p.Title = "Kleine Swap-Datei als Puffer"
	m := s.memory()
	f := s.swapFile()
	p.File = f
	switch {
	case st.Swap != nil:
		p.Problem = "HostDeck hat schon einen Swap-Puffer angelegt."
		return
	case m.Total == 0:
		p.Problem = "Der Arbeitsspeicher lässt sich nicht lesen."
		return
	case m.SwapTotal > 0:
		p.Problem = "Der Server hat schon Swap (" + gib(m.SwapTotal) + ")."
		return
	}
	size := swapSize(m.Total)
	if why := s.swapProblem(size); why != "" {
		p.Problem = why
		return
	}
	p.Steps = []string{
		"Die Datei " + f + " mit " + gib(size) + " anlegen; lesen darf sie nur root.",
		"Sie als Swap einrichten und einschalten.",
		"Sie in /etc/fstab eintragen, damit sie nach einem Neustart wieder da ist. Die bisherige fstab legt HostDeck als Vorher-Stand ab.",
	}
	p.After = f + " none swap sw 0 0"
	p.Effect = "Wird der Arbeitsspeicher knapp, lagert Linux selten Genutztes aus, statt sofort einen Prozess zu beenden. Der Puffer belegt " + gib(size) + " auf der Platte."
	p.Back = "Unter Leistung „Swap-Puffer entfernen“: HostDeck schaltet ihn ab, nimmt die Zeile aus /etc/fstab und löscht die Datei."
	switch {
	case m.Swappiness == 0:
		p.Notes = append(p.Notes, "vm.swappiness steht auf 0: Linux nutzt den Swap erst, wenn es eng wird. Genau so ist er als Puffer gedacht; HostDeck ändert das nicht.")
	case m.Swappiness > 0:
		p.Notes = append(p.Notes, "vm.swappiness steht auf "+strconv.Itoa(m.Swappiness)+"; HostDeck ändert das nicht.")
	}
}

// swapUse liest /proc/swaps: ist die Datei eingeschaltet, wie viel ist belegt.
func (s *Service) swapUse(file string) (active bool, used int64) {
	b, err := os.ReadFile(filepath.Join(s.proc(), "swaps"))
	if err != nil {
		return false, 0
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == file {
			n, _ := strconv.ParseInt(f[3], 10, 64)
			return true, n * 1024
		}
	}
	return false, 0
}

// swapRoom: Für das Abschalten braucht HostDeck so viel freien Speicher mehr,
// als im Swap liegt.
const swapRoom = 256 << 20

func (s *Service) planSwapOff(st state, p *Plan) {
	p.Title = "Swap-Puffer entfernen"
	if st.Swap == nil {
		p.Problem = "HostDeck hat keinen Swap-Puffer angelegt."
		return
	}
	f := st.Swap.File
	p.File = f
	active, used := s.swapUse(f)
	if active {
		if avail := s.memInfo()["MemAvailable"]; used+swapRoom > avail {
			p.Problem = ErrNoRoom.Error() + "."
			return
		}
		p.Steps = append(p.Steps, "Den Swap abschalten; was darin liegt ("+Bytes(used)+"), holt Linux zurück in den Arbeitsspeicher.")
	}
	p.Steps = append(p.Steps, "Die Zeile für "+f+" aus /etc/fstab nehmen.", "Die Datei "+f+" löschen.")
	p.Before = f + " none swap sw 0 0"
	p.Effect = "Der Server hat danach wieder keinen Swap, " + gib(st.Swap.Size) + " auf der Platte sind frei."
	p.Back = "Der Puffer lässt sich unter Leistung wieder anlegen."
}

// Apply macht einen Vorschlag wahr. stand kommt aus der Vorschau.
func (s *Service) Apply(ctx context.Context, key, stand string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.plan(ctx, key)
	if err != nil {
		return "", err
	}
	if p.Stand != stand {
		return "", ErrChanged
	}
	if p.Problem != "" {
		return "", errors.New(strings.TrimSuffix(p.Problem, "."))
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return "", err
	}
	switch key {
	case KeyLogs:
		return s.applyLogs(ctx)
	case KeyLogsBack:
		return s.applyLogsBack()
	case KeySwap:
		return s.applySwap(ctx)
	case KeySwapOff:
		return s.applySwapOff(ctx)
	}
	return "", ErrNotFound
}

func (s *Service) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if s.Run == nil {
		return nil, errors.New("HostDeck kann hier keine Programme starten")
	}
	return s.Run(ctx, name, args...)
}

func firstLine(b []byte) string {
	t := strings.TrimSpace(string(b))
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[:i]
	}
	if len(t) > 200 {
		t = t[:200]
	}
	return t
}

func (s *Service) applyLogs(ctx context.Context) (string, error) {
	d, path, _ := s.daemonFile()
	_, b := limitedConfig(d)
	tmp := filepath.Join(s.Dir, "daemon.json.neu")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	if out, err := s.run(ctx, "dockerd", "--validate", "--config-file", tmp); err != nil {
		msg := string(out) + err.Error()
		if !errors.Is(err, exec.ErrNotFound) && !strings.Contains(msg, "unknown flag") && !strings.Contains(msg, "kann hier keine Programme") {
			return "", errors.New("Docker lehnt die neue daemon.json ab: " + firstLine(out))
		}
	}
	mode := os.FileMode(0o644)
	if d.exists {
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := writeAtomic(s.logBackup(), d.raw, 0o600); err != nil {
			return "", err
		}
	} else {
		os.Remove(s.logBackup())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := writeAtomic(path, b, mode); err != nil {
		return "", err
	}
	st := s.state()
	st.Logs = &logState{Path: path, Existed: d.exists, Mode: mode, Written: hashOf(b), At: s.now()}
	if err := s.saveState(st); err != nil {
		// Ohne Zustand kein sicherer Rückweg: Datei zurücklegen.
		if d.exists {
			_ = writeAtomic(path, d.raw, mode)
		} else {
			os.Remove(path)
		}
		return "", err
	}
	return "Container-Logs begrenzt: höchstens " + LogMaxFile + " Dateien zu je 10 MB je Container, eingetragen in " + path + ". Gilt für Container, die nach dem nächsten Neustart von Docker neu angelegt werden.", nil
}

func (s *Service) applyLogsBack() (string, error) {
	st := s.state()
	ls := st.Logs
	if ls.Existed {
		old, err := os.ReadFile(s.logBackup())
		if err != nil {
			return "", errors.New("der Vorher-Stand von daemon.json fehlt")
		}
		if err := writeAtomic(ls.Path, old, ls.Mode); err != nil {
			return "", err
		}
	} else if err := os.Remove(ls.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	st.Logs = nil
	if err := s.saveState(st); err != nil {
		return "", err
	}
	os.Remove(s.logBackup())
	return "Log-Grenze zurückgenommen: " + ls.Path + " ist wieder wie vorher. Gilt nach dem nächsten Neustart von Docker.", nil
}

const fstabNote = "# HostDeck: Swap-Puffer (im Leitstand unter Leistung entfernbar)"

// legacyFstabNote schrieb die frühere Fassung sbm; Entfernen nimmt auch sie heraus.
const legacyFstabNote = "# sbm: Swap-Puffer (im Leitstand unter Leistung entfernbar)"

func (s *Service) applySwap(ctx context.Context) (res string, err error) {
	f := s.swapFile()
	size := swapSize(s.memory().Total)
	var undo []func()
	defer func() {
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
	}()
	undo = append(undo, func() { os.Remove(f) })
	build := func(dd bool) error {
		os.Remove(f)
		if !dd {
			if out, err := s.run(ctx, "fallocate", "-l", strconv.FormatInt(size, 10), f); err != nil {
				return fmt.Errorf("fallocate: %s", firstLine(append(out, []byte(err.Error())...)))
			}
		} else if out, err := s.run(ctx, "dd", "if=/dev/zero", "of="+f, "bs=1M", "count="+strconv.FormatInt(size>>20, 10), "status=none"); err != nil {
			return fmt.Errorf("dd: %s", firstLine(append(out, []byte(err.Error())...)))
		}
		if err := os.Chmod(f, 0o600); err != nil {
			return err
		}
		if out, err := s.run(ctx, "mkswap", f); err != nil {
			return fmt.Errorf("mkswap: %s", firstLine(append(out, []byte(err.Error())...)))
		}
		if out, err := s.run(ctx, "swapon", f); err != nil {
			return fmt.Errorf("swapon: %s", firstLine(append(out, []byte(err.Error())...)))
		}
		return nil
	}
	// Manche Dateisysteme nehmen keine vorab belegte Datei als Swap; dann
	// schreibt HostDeck sie mit Nullen voll.
	if err1 := build(false); err1 != nil {
		if err2 := build(true); err2 != nil {
			return "", errors.New("Swap-Datei ließ sich nicht einrichten: " + err2.Error())
		}
	}
	undo = append(undo, func() { _, _ = s.run(context.Background(), "swapoff", f) })
	tab, terr := os.ReadFile(s.fstab())
	mode := os.FileMode(0o644)
	if terr == nil {
		if fi, err := os.Stat(s.fstab()); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := writeAtomic(s.fstabBackup(), tab, 0o600); err != nil {
			return "", err
		}
	} else if !errors.Is(terr, os.ErrNotExist) {
		return "", terr
	}
	nt := string(tab)
	if nt != "" && !strings.HasSuffix(nt, "\n") {
		nt += "\n"
	}
	nt += fstabNote + "\n" + f + " none swap sw 0 0\n"
	if err := writeAtomic(s.fstab(), []byte(nt), mode); err != nil {
		return "", err
	}
	undo = append(undo, func() {
		if terr == nil {
			_ = writeAtomic(s.fstab(), tab, mode)
		} else {
			os.Remove(s.fstab())
		}
	})
	st := s.state()
	st.Swap = &swapState{File: f, Size: size, At: s.now()}
	if err := s.saveState(st); err != nil {
		return "", err
	}
	return "Swap-Puffer angelegt: " + f + " mit " + gib(size) + ", eingeschaltet und in /etc/fstab eingetragen.", nil
}

// dropSwapLines nimmt die Zeilen von HostDeck für file aus der fstab.
func dropSwapLines(tab, file string) string {
	lines := strings.SplitAfter(tab, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == fstabNote || t == legacyFstabNote {
			continue
		}
		if f := strings.Fields(t); len(f) >= 3 && f[0] == file && f[2] == "swap" {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "")
}

func (s *Service) applySwapOff(ctx context.Context) (string, error) {
	st := s.state()
	sw := st.Swap
	if active, _ := s.swapUse(sw.File); active {
		if out, err := s.run(ctx, "swapoff", sw.File); err != nil {
			return "", errors.New("swapoff: " + firstLine(append(out, []byte(err.Error())...)))
		}
	}
	if tab, err := os.ReadFile(s.fstab()); err == nil {
		if nt := dropSwapLines(string(tab), sw.File); nt != string(tab) {
			mode := os.FileMode(0o644)
			if fi, err := os.Stat(s.fstab()); err == nil {
				mode = fi.Mode().Perm()
			}
			if err := writeAtomic(s.fstab(), []byte(nt), mode); err != nil {
				return "", err
			}
		}
	}
	if err := os.Remove(sw.File); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	st.Swap = nil
	if err := s.saveState(st); err != nil {
		return "", err
	}
	os.Remove(s.fstabBackup())
	return "Swap-Puffer entfernt: " + sw.File + " ist abgeschaltet, aus /etc/fstab genommen und gelöscht.", nil
}
