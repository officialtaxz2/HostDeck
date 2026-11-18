// Package tuning führt tune.sh (vps-tune 2.3.0-lean) als Teil von
// HostDeck aus: Ist-Zustand, Vorschau, Anwenden, CPU-Vorrang
// je Dienst, Zurücknehmen.
//
// Das Skript steckt unverändert im Programm (tune.sh, eingebettet; ein Test
// prüft die Prüfsumme gegen das Original). HostDeck schreibt es für jeden Aufruf
// mit Rechten 0700 in den Ordner der Laufsperre (/run/hostdeck), ruft es mit
// sauberer Umgebung auf und liest die Ausgabe: [PLAN], [INFO], [WARN],
// [FEHLER] und die Zeilen von „status“. Die Sicherheitsabfrage des Skripts
// (confirm_risk) ersetzt im Leitstand eine eigene Bestätigung mit Passwort;
// danach ruft HostDeck apply mit --yes auf.
package tuning

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
)

// Script ist tune.sh, wie HostDeck es ausführt.
//
//go:embed tune.sh
var Script []byte

// ScriptSHA256 ist der Fingerabdruck von tune.sh, gleich dem Original
// (vps-tune 2.3.0-lean). Ändert sich das Skript, muss er bewusst
// mitgeändert werden (Test).
const ScriptSHA256 = "2f8cb62dd0897857a8a4f539f1554b239d35d418e024182564759cd837f3dcdc"

// Version ist die Version von vps-tune im Skript.
const Version = "2.3.0-lean"

// Path ist der Suchpfad für das Skript.
const Path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// RebootFile liegt im Ordner der Laufsperre (tmpfs), solange ein Lauf von
// apply oder restore einen Neustart verlangt hat. Ein Neustart leert /run
// und damit auch diese Merkhilfe.
const RebootFile = "tuning-neustart"

// Service ruft tune.sh auf.
type Service struct {
	// RunDir: Hier legt HostDeck das Skript für den Aufruf ab und merkt sich
	// einen nötigen Neustart; leer: lauf.Dir().
	RunDir string
	// Prefix ist die Testwurzel des Skripts (VT_PREFIX); leer im Betrieb.
	Prefix string
	// Env: weitere Variablen für Tests (VT_KREL, VT_VIRT).
	Env []string
	// Path ersetzt den Suchpfad (Tests mit nachgebauten Programmen).
	Path string
	// Bash ist die Shell; leer: /bin/bash.
	Bash string
	// Script ersetzt das eingebettete Skript (Tests).
	Script []byte
	// RebootCmd startet den Server neu; nil: systemctl reboot.
	RebootCmd func(ctx context.Context) error
	// Refuse: Dann ruft HostDeck nichts auf (unvollständiger Testaufbau).
	Refuse error
}

func (s *Service) runDir() string {
	if s.RunDir != "" {
		return s.RunDir
	}
	return lauf.Dir()
}

func (s *Service) script() ([]byte, error) {
	if s.Script != nil {
		return s.Script, nil
	}
	if sum := sha256.Sum256(Script); hex.EncodeToString(sum[:]) != ScriptSHA256 {
		return nil, errors.New("tune.sh im Programm ist beschädigt")
	}
	return Script, nil
}

func (s *Service) env() []string {
	p := s.Path
	if p == "" {
		p = Path
	}
	e := []string{"PATH=" + p, "LANG=C.UTF-8"}
	if s.Prefix != "" {
		e = append(e, "VT_PREFIX="+s.Prefix)
	}
	return append(e, s.Env...)
}

// Run ruft tune.sh mit args auf. stdin darf nil sein. Endet ctx, bekommt
// das Skript SIGTERM; im Leitstand nur nach langer Frist, denn ein halb
// angewendetes Tuning ist schlechter als ein fertiges.
func (s *Service) Run(ctx context.Context, args []string, stdin io.Reader, out io.Writer) (int, error) {
	if s.Refuse != nil {
		return 0, s.Refuse
	}
	b, err := s.script()
	if err != nil {
		return 0, err
	}
	dir := s.runDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	f, err := os.CreateTemp(dir, ".tune-*.sh")
	if err != nil {
		return 0, err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Chmod(0o700); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	bash := s.Bash
	if bash == "" {
		bash = "/bin/bash"
	}
	cmd := exec.CommandContext(ctx, bash, append([]string{path}, args...)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 30 * time.Second
	cmd.Dir = dir
	cmd.Env = s.env()
	cmd.Stdin = stdin
	cmd.Stdout, cmd.Stderr = out, out
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return ee.ExitCode(), nil
	case err != nil:
		return 0, err
	}
	return 0, nil
}

// capture ruft tune.sh auf und gibt die Ausgabe zurück.
func (s *Service) capture(ctx context.Context, args ...string) (string, int, error) {
	var buf bytes.Buffer
	code, err := s.Run(ctx, args, nil, &buf)
	return buf.String(), code, err
}

// Status liest den Ist-Zustand (status), dazu applied.conf und ob ein
// Neustart aussteht.
func (s *Service) Status(ctx context.Context) (Status, error) {
	out, code, err := s.capture(ctx, "status")
	if err != nil {
		return Status{}, err
	}
	st := ParseStatus(out)
	if code != 0 {
		st.Error = fmt.Sprintf("tune.sh status ist mit Code %d ausgestiegen.", code)
	}
	st.Applied = ReadApplied(s.Prefix)
	if why := s.RebootNote(); why != "" && !st.Reboot {
		st.Reboot = true
		st.RebootWhy = why
	}
	return st, nil
}

// RebootNote ist der Grund für einen Neustart, den sich HostDeck nach apply
// oder restore gemerkt hat; leer: keiner.
func (s *Service) RebootNote() string {
	b, err := os.ReadFile(filepath.Join(s.runDir(), RebootFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Preview zeigt, was apply täte (--dry-run).
func (s *Service) Preview(ctx context.Context, opt Options) (Plan, error) {
	if err := opt.Check(); err != nil {
		return Plan{}, err
	}
	args := append(opt.Args(), "--dry-run")
	out, code, err := s.capture(ctx, args...)
	if err != nil {
		return Plan{}, err
	}
	return newPlan(args, out, code), nil
}

// RestorePreview zeigt, was restore täte (--dry-run).
func (s *Service) RestorePreview(ctx context.Context) (Plan, error) {
	args := []string{"restore", "--dry-run"}
	out, code, err := s.capture(ctx, args...)
	if err != nil {
		return Plan{}, err
	}
	return newPlan(args, out, code), nil
}

// Outcome ist das Ergebnis eines Aufrufs, der etwas ändert.
type Outcome struct {
	Code     int    `json:"code"`
	Warnings int    `json:"warnungen"`
	Reboot   bool   `json:"neustart,omitempty"`
	Last     string `json:"zuletzt,omitempty"` // letzte [INFO]- oder [FEHLER]-Zeile
	// Lines ist die ganze Ausgabe (nur bei CPU-Vorrang zurückgegeben).
	Lines []Line `json:"zeilen,omitempty"`
}

// watch schreibt durch und merkt sich, was am Ende zählt.
type watch struct {
	out  io.Writer
	part []byte
	o    *Outcome
	why  string
	all  []Line
}

func (w *watch) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	w.part = append(w.part, p[:n]...)
	for {
		i := bytes.IndexByte(w.part, '\n')
		if i < 0 {
			break
		}
		w.line(string(w.part[:i]))
		w.part = w.part[i+1:]
	}
	return n, err
}

func (w *watch) line(raw string) {
	l := ParseLine(raw)
	w.all = append(w.all, l)
	switch l.Kind {
	case "warn":
		w.o.Warnings++
	case "info", "fehler":
		w.o.Last = l.Text
	}
	if strings.Contains(l.Text, "NEUSTART") {
		w.o.Reboot = true
		w.why = l.Text
	}
}

func (w *watch) flush() {
	if len(w.part) > 0 {
		w.line(string(w.part))
		w.part = nil
	}
}

// Exec ruft tune.sh mit args auf und merkt sich einen nötigen
// Neustart.
func (s *Service) Exec(ctx context.Context, args []string, stdin io.Reader, out io.Writer) (Outcome, error) {
	var o Outcome
	w := &watch{out: out, o: &o}
	code, err := s.Run(ctx, args, stdin, w)
	w.flush()
	o.Code = code
	o.Lines = w.all
	if err != nil {
		return o, err
	}
	if o.Reboot {
		if err := os.WriteFile(filepath.Join(s.runDir(), RebootFile), []byte(w.why+"\n"), 0o644); err != nil {
			return o, err
		}
	}
	return o, nil
}

// Apply wendet ein Preset an. yes: die Sicherheitsabfrage ist schon
// bestätigt (--yes); sonst fragt das Skript über stdin.
func (s *Service) Apply(ctx context.Context, opt Options, yes bool, stdin io.Reader, out io.Writer) (Outcome, error) {
	if err := opt.Check(); err != nil {
		return Outcome{}, err
	}
	args := opt.Args()
	if yes {
		args = append(args, "--yes")
	}
	return s.Exec(ctx, args, stdin, out)
}

// Restore nimmt alles zurück.
func (s *Service) Restore(ctx context.Context, out io.Writer) (Outcome, error) {
	return s.Exec(ctx, []string{"restore"}, nil, out)
}

// Prio setzt den CPU-Vorrang eines Dienstes (high, normal, low).
func (s *Service) Prio(ctx context.Context, unit, level string, out io.Writer) (Outcome, error) {
	if !ValidUnit(unit) {
		return Outcome{}, fmt.Errorf("%w: Ungültiger Name eines Dienstes %q", ErrOptions, unit)
	}
	if _, ok := Levels[level]; !ok {
		return Outcome{}, fmt.Errorf("%w: Stufe %q (high, normal oder low)", ErrOptions, level)
	}
	return s.Exec(ctx, []string{"prio", unit, level}, nil, out)
}

// Reboot startet den Server neu.
func (s *Service) Reboot(ctx context.Context) error {
	if s.RebootCmd != nil {
		return s.RebootCmd(ctx)
	}
	_, err := s.systemctl(ctx, "reboot")
	return err
}

// TestPrograms sind die Programme, mit denen tune.sh etwas außerhalb seiner
// Dateien ändert. Für einen Testlauf mit VT_PREFIX müssen sie in VT_BIN
// als Ersatz liegen (tuningtest baut sie).
var TestPrograms = []string{"update-grub", "systemctl", "tc", "modprobe", "dpkg-reconfigure"}

// FromEnv baut den Dienst für den Betrieb. Zum Durchspielen auf einem
// nachgebauten Server braucht es VT_PREFIX (Testwurzel des Skripts) und
// VT_BIN (Ordner mit Ersatz für TestPrograms). VT_PREFIX verlegt im Skript
// nur Dateipfade; Programme wie update-grub, systemctl und tc kämen sonst
// echt aus dem PATH und änderten den Server. Fehlt ein Ersatz, ruft HostDeck gar
// nichts auf. Gesucht wird dann in VT_BIN und den Systemordnern.
func FromEnv(runDir string) *Service {
	s := &Service{RunDir: runDir}
	p := os.Getenv("VT_PREFIX")
	if p == "" {
		return s
	}
	s.Prefix = p
	bin := os.Getenv("VT_BIN")
	for _, prog := range TestPrograms {
		if bin == "" || !isExec(filepath.Join(bin, prog)) {
			s.Refuse = fmt.Errorf("VT_PREFIX ist gesetzt, aber in VT_BIN (%q) fehlt der Ersatz für %s; sonst änderte der Testlauf den echten Server", bin, prog)
			return s
		}
	}
	s.Path = bin + ":" + Path
	for _, k := range []string{"VT_KREL", "VT_VIRT"} {
		if v := os.Getenv(k); v != "" {
			s.Env = append(s.Env, k+"="+v)
		}
	}
	return s
}

// Unit ist ein systemd-Dienst für den CPU-Vorrang.
type Unit struct {
	Name        string `json:"name"`
	Description string `json:"beschreibung,omitempty"`
	Running     bool   `json:"laeuft"`
	// Weight ist CPUWeight; 0: Standard (nicht gesetzt).
	Weight int `json:"gewicht,omitempty"`
	// Level: high, low oder normal, wie prio es setzt; other: ein anderer
	// Wert, den nicht vps-tune gesetzt hat.
	Level string `json:"stufe"`
	// Managed: steht in prio.list, vps-tune hat ihn verändert.
	Managed bool `json:"von_vps_tune,omitempty"`
}

// PrioList ist die Liste der Dienste, deren Vorrang vps-tune geändert hat.
const PrioList = "/var/lib/vps-tune/prio.list"

func (s *Service) systemctl(ctx context.Context, args ...string) (string, error) {
	if s.Refuse != nil {
		return "", s.Refuse
	}
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	cmd.Env = s.env()
	if s.Path != "" {
		// exec sucht mit dem PATH von HostDeck; die Testprogramme liegen im
		// eigenen Pfad.
		for _, d := range filepath.SplitList(s.Path) {
			if p := filepath.Join(d, "systemctl"); isExec(p) {
				cmd.Path = p
				break
			}
		}
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("systemctl %s: %v %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

// Services listet die laufenden Dienste und die, deren Vorrang vps-tune
// geändert hat, mit ihrem CPUWeight.
func (s *Service) Services(ctx context.Context) ([]Unit, error) {
	out, err := s.systemctl(ctx, "list-units", "--type=service", "--state=running", "--no-legend", "--plain", "--no-pager")
	if err != nil {
		return nil, err
	}
	running := map[string]bool{}
	var names []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 || !strings.HasSuffix(f[0], ".service") || !ValidUnit(f[0]) {
			continue
		}
		running[f[0]] = true
		names = append(names, f[0])
	}
	managed := map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(s.Prefix, PrioList)); err == nil {
		for _, u := range strings.Split(string(b), "\n") {
			if u = strings.TrimSpace(u); u != "" && ValidUnit(u) {
				managed[u] = true
				if !running[u] {
					names = append(names, u)
				}
			}
		}
	}
	if len(names) == 0 {
		return []Unit{}, nil
	}
	show, err := s.systemctl(ctx, append([]string{"show", "--no-pager", "-p", "Id", "-p", "Description", "-p", "CPUWeight", "--"}, names...)...)
	if err != nil {
		return nil, err
	}
	var list []Unit
	add := func(u *Unit) {
		if u == nil || u.Name == "" {
			return
		}
		u.Running = running[u.Name]
		u.Managed = managed[u.Name]
		switch u.Weight {
		case 1000:
			u.Level = "high"
		case 20:
			u.Level = "low"
		case 0, 100:
			u.Level = "normal"
		default:
			u.Level = "other"
		}
		list = append(list, *u)
	}
	var cur *Unit
	sc := bufio.NewScanner(strings.NewReader(show))
	for sc.Scan() {
		l := sc.Text()
		if l == "" {
			add(cur)
			cur = nil
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		if cur == nil {
			cur = &Unit{}
		}
		switch k {
		case "Id":
			cur.Name = v
		case "Description":
			cur.Description = v
		case "CPUWeight":
			cur.Weight, _ = strconv.Atoi(v) // „[not set]“ oder leer: 0
		}
	}
	add(cur)
	sort.Slice(list, func(i, j int) bool {
		if list[i].Managed != list[j].Managed {
			return list[i].Managed
		}
		return list[i].Name < list[j].Name
	})
	if list == nil {
		list = []Unit{}
	}
	return list, nil
}
