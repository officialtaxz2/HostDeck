package tuning

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Line ist eine Zeile der Ausgabe von tune.sh.
type Line struct {
	// Kind: plan, info, warn, fehler, hinweis, achtung, detail (eingerückt
	// unter „schreiben:“) oder leer.
	Kind string `json:"art,omitempty"`
	Text string `json:"text"`
}

var prefixes = []struct{ p, kind string }{
	{"[PLAN] ", "plan"}, {"[INFO] ", "info"}, {"[WARN] ", "warn"}, {"[FEHLER] ", "fehler"}, {"[HINWEIS] ", "hinweis"},
}

// ParseLine ordnet eine Zeile ein.
func ParseLine(l string) Line {
	l = strings.TrimRight(l, " \r")
	for _, p := range prefixes {
		if strings.HasPrefix(l, p.p) {
			return Line{Kind: p.kind, Text: l[len(p.p):]}
		}
	}
	switch {
	case strings.HasPrefix(l, "         "):
		return Line{Kind: "detail", Text: strings.TrimSpace(l)}
	case strings.HasPrefix(l, "ACHTUNG: "), strings.HasPrefix(l, "Prozesse in dieser VM"):
		return Line{Kind: "achtung", Text: l}
	}
	return Line{Text: l}
}

// ParseLines teilt eine Ausgabe in Zeilen; Leerzeilen am Anfang und Ende
// fallen weg.
func ParseLines(out string) []Line {
	var ls []Line
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(ls) == 0 && strings.TrimSpace(l) == "" {
			continue
		}
		ls = append(ls, ParseLine(l))
	}
	return ls
}

// Risky sind die Bootparameter, für die tune.sh eine Bestätigung verlangt
// (confirm_risk).
var Risky = []string{"mitigations=off", "init_on_alloc=0", "randomize_kstack_offset=off"}

// RiskText ist der Warntext von confirm_risk, wörtlich (Test).
func RiskText(params []string) string {
	return "ACHTUNG: " + strings.Join(params, " ") + " schalten Schutzmechanismen des Gastkernels ab.\n" +
		"Prozesse in dieser VM können sich dadurch leichter gegenseitig oder den Kernel angreifen."
}

var bootLines = []*regexp.Regexp{
	regexp.MustCompile(`^Bootparameter über \S+: (.+) \+ update-grub$`),
	regexp.MustCompile(`^Bootparameter bereits eingetragen: (.+)$`),
	regexp.MustCompile(`Bootparameter bitte manuell setzen: (.+)$`),
}

// bootParams liest aus der Ausgabe von apply, welche Bootparameter das
// Skript setzen will (PARAMS); nil: keine.
func bootParams(ls []Line) []string {
	for _, l := range ls {
		for _, re := range bootLines {
			if m := re.FindStringSubmatch(l.Text); m != nil {
				if strings.HasPrefix(m[1], "(") { // „(keine, Datei entfernen)“
					return nil
				}
				return strings.Fields(m[1])
			}
		}
	}
	return nil
}

// Plan ist die Vorschau eines Aufrufs mit --dry-run.
type Plan struct {
	Args  []string `json:"aufruf"`
	Lines []Line   `json:"zeilen"`
	Code  int      `json:"code"`
	// Changes zählt die [PLAN]-Zeilen: Das würde sich ändern.
	Changes  int `json:"aenderungen"`
	Warnings int `json:"warnungen"`
	// Boot sind die Bootparameter, die danach eingetragen sind.
	Boot []string `json:"bootparameter,omitempty"`
	// BootChange: Die Bootparameter ändern sich, ein Neustart ist danach
	// nötig.
	BootChange bool `json:"neustart,omitempty"`
	// Risky sind die Parameter, die der Bestätigung bedürfen; RiskText
	// ist dann der Warntext des Skripts.
	Risky    []string `json:"riskant,omitempty"`
	RiskText string   `json:"warntext,omitempty"`
	// Problem: Das Skript ist mit Fehler ausgestiegen (etwa „Bitte mit
	// sudo/root ausführen“).
	Problem string `json:"problem,omitempty"`
}

func newPlan(args []string, out string, code int) Plan {
	p := Plan{Args: args, Lines: ParseLines(out), Code: code}
	if p.Lines == nil {
		p.Lines = []Line{}
	}
	var errs []string
	for _, l := range p.Lines {
		switch l.Kind {
		case "plan":
			p.Changes++
			if strings.HasPrefix(l.Text, "Bootparameter über ") {
				p.BootChange = true
			}
		case "warn":
			p.Warnings++
		case "fehler":
			errs = append(errs, l.Text)
		}
	}
	p.Boot = bootParams(p.Lines)
	for _, b := range p.Boot {
		for _, r := range Risky {
			if b == r {
				p.Risky = append(p.Risky, b)
			}
		}
	}
	if len(p.Risky) > 0 {
		p.RiskText = RiskText(p.Risky)
	}
	if code != 0 {
		p.Problem = strings.Join(errs, " ")
		if p.Problem == "" {
			p.Problem = "tune.sh ist mit Code " + strconv.Itoa(code) + " ausgestiegen."
		}
	}
	return p
}

// Row ist eine Zeile des Status.
type Row struct {
	Label string `json:"name"`
	Value string `json:"wert"`
}

// BootParam ist ein Bootparameter von vps-tune.
type BootParam struct {
	Param  string `json:"parameter"`
	Active bool   `json:"aktiv"` // false: erst nach einem Neustart
}

// Status ist die Ausgabe von „status“, gelesen.
type Status struct {
	Version   string      `json:"version,omitempty"`
	Head      []Row       `json:"kopf"`
	Applied   *Applied    `json:"angewendet,omitempty"`
	Boot      []BootParam `json:"bootparameter"`
	BootNotes []string    `json:"boot_hinweise,omitempty"`
	Rows      []Row       `json:"wirksamkeit"`
	Notes     []string    `json:"hinweise,omitempty"`
	// Reboot: Bootparameter warten auf einen Neustart; RebootWhy sagt,
	// welche oder warum.
	Reboot    bool   `json:"neustart_noetig,omitempty"`
	RebootWhy string `json:"neustart_grund,omitempty"`
	// Lines ist die ganze Ausgabe, wie im Terminal.
	Lines []string `json:"ausgabe"`
	Error string   `json:"fehler,omitempty"`
}

var (
	versionRe = regexp.MustCompile(`^vps-tune (\S+) – Status$`)
	headRe    = regexp.MustCompile(`^(\S[^:]*:)\s+(.*)$`)
	rowRe     = regexp.MustCompile(`^  (\S.*?:)\s+(.*)$`)
	bootRe    = regexp.MustCompile(`^  (\S+)\s+(aktiv|NACH NEUSTART)$`)
)

// ParseStatus liest die Ausgabe von „status“.
func ParseStatus(out string) Status {
	s := Status{Head: []Row{}, Boot: []BootParam{}, Rows: []Row{}, Lines: []string{}}
	section := "kopf"
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		l = strings.TrimRight(l, " \r")
		if len(s.Lines) > 0 || l != "" {
			s.Lines = append(s.Lines, l)
		}
		switch {
		case l == "" || strings.HasPrefix(l, "-----"):
			continue
		case versionRe.MatchString(l):
			s.Version = versionRe.FindStringSubmatch(l)[1]
			continue
		case l == "Bootparameter (vps-tune):":
			section = "boot"
			continue
		case l == "Wirksamkeit:":
			section = "rows"
			continue
		case strings.HasPrefix(l, "Hinweis: "):
			s.Notes = append(s.Notes, strings.TrimPrefix(l, "Hinweis: "))
			continue
		}
		switch section {
		case "kopf":
			if m := headRe.FindStringSubmatch(l); m != nil {
				s.Head = append(s.Head, Row{Label: strings.TrimSuffix(m[1], ":"), Value: m[2]})
			}
		case "boot":
			if m := bootRe.FindStringSubmatch(l); m != nil {
				s.Boot = append(s.Boot, BootParam{Param: m[1], Active: m[2] == "aktiv"})
			} else if t := strings.TrimSpace(l); strings.HasPrefix(t, "Hinweis: ") {
				s.BootNotes = append(s.BootNotes, strings.TrimPrefix(t, "Hinweis: "))
			}
		case "rows":
			if m := rowRe.FindStringSubmatch(l); m != nil {
				s.Rows = append(s.Rows, Row{Label: strings.TrimSuffix(m[1], ":"), Value: m[2]})
			}
		}
	}
	var wait []string
	for _, b := range s.Boot {
		if !b.Active {
			wait = append(wait, b.Param)
		}
	}
	if len(wait) > 0 {
		s.Reboot = true
		s.RebootWhy = "Diese Bootparameter wirken erst nach einem Neustart: " + strings.Join(wait, " ") + "."
	}
	return s
}

// Applied ist /var/lib/vps-tune/applied.conf: womit apply zuletzt lief.
type Applied struct {
	Preset string `json:"preset"`
	// Values: alle Schalter wie preempt=full, ohne preset.
	Values map[string]string `json:"werte"`
	Boot   []string          `json:"boot"`
	At     time.Time         `json:"seit"`
}

// AppliedFile ist der Ort von applied.conf unterhalb der Wurzel.
const AppliedFile = "/var/lib/vps-tune/applied.conf"

// ReadApplied liest applied.conf unter root (leer: /); nil: vps-tune ist
// nicht aktiv.
func ReadApplied(root string) *Applied {
	p := filepath.Join(root, AppliedFile)
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	a := &Applied{Values: map[string]string{}, Boot: []string{}}
	if fi, err := f.Stat(); err == nil {
		a.At = fi.ModTime()
	}
	sc := bufio.NewScanner(f)
	for n := 0; sc.Scan(); n++ {
		l := sc.Text()
		if b, ok := strings.CutPrefix(l, "boot="); ok {
			a.Boot = strings.Fields(b)
			continue
		}
		if n > 0 {
			continue
		}
		for _, kv := range strings.Fields(l) {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			if k == "preset" {
				a.Preset = v
			} else {
				a.Values[k] = v
			}
		}
	}
	if a.Preset == "" {
		return nil
	}
	return a
}

// Options macht aus applied.conf einen Aufruf von apply mit denselben
// Werten: nur die Schalter, die vom Preset abweichen.
func (a *Applied) Options() Options {
	o := Options{Preset: a.Preset, Set: map[string]string{}}
	p, ok := PresetByName(a.Preset)
	for _, sw := range Switches {
		v := a.Values[sw.Key]
		if v == "" || (ok && p.Values[sw.Key] == v) {
			continue
		}
		if sw.Key == "iface" && v == "auto" {
			continue
		}
		o.Set[sw.Key] = v
	}
	return o
}
