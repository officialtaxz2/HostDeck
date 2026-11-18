package ueberwachung

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Grenzen ändern: Im Leitstand ohne Passwort, aber jede
// Änderung steht im Protokoll. Die Wartezeiten stehen im JSON in Minuten.

type limitsAlias Limits

type limitsJSON struct {
	limitsAlias
	MemMin       *int `json:"speicher_minuten"`
	CPUMin       *int `json:"prozessor_minuten"`
	StealMin     *int `json:"steal_minuten"`
	UnhealthyMin *int `json:"unhealthy_minuten"`
}

func mins(d time.Duration) *int {
	m := int(d / time.Minute)
	return &m
}

// MarshalJSON schreibt die Wartezeiten in Minuten.
func (l Limits) MarshalJSON() ([]byte, error) {
	return json.Marshal(limitsJSON{limitsAlias(l), mins(l.MemHold), mins(l.CPUHold), mins(l.StealHold), mins(l.UnhealthyFor)})
}

// UnmarshalJSON liest die Wartezeiten in Minuten; was fehlt, kommt aus
// den Voreinstellungen.
func (l *Limits) UnmarshalJSON(b []byte) error {
	x := limitsJSON{limitsAlias: limitsAlias(DefaultLimits)}
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	*l = Limits(x.limitsAlias)
	set := func(d *time.Duration, m *int) {
		if m != nil {
			*d = time.Duration(*m) * time.Minute
		}
	}
	set(&l.MemHold, x.MemMin)
	set(&l.CPUHold, x.CPUMin)
	set(&l.StealHold, x.StealMin)
	set(&l.UnhealthyFor, x.UnhealthyMin)
	// Nicht änderbar.
	l.RestartsIn, l.ClearAfter, l.RemindAfter = DefaultLimits.RestartsIn, DefaultLimits.ClearAfter, DefaultLimits.RemindAfter
	return nil
}

// Validate prüft, ob die Grenzen Sinn ergeben.
func (l Limits) Validate() error {
	var errs []string
	in := func(ok bool, msg string) {
		if !ok {
			errs = append(errs, msg)
		}
	}
	whole := func(x float64) bool { return math.Abs(x*100-math.Round(x*100)) < 1e-6 }
	in(l.DiskWarn >= 0.5 && l.DiskWarn <= 0.99 && whole(l.DiskWarn), "Platte gelb: zwischen 50 und 99 %.")
	in(l.DiskErr >= l.DiskWarn && l.DiskErr <= 0.99 && whole(l.DiskErr), "Platte rot: zwischen gelb und 99 %.")
	in(l.DiskMinFree >= 0 && l.DiskMinFree <= 1000e9, "Platte, fester Rest: 0 bis 1000 GB.")
	in(l.MemWarn >= 0.02 && l.MemWarn <= 0.5 && whole(l.MemWarn), "Arbeitsspeicher gelb: unter 2 bis 50 % frei.")
	in(l.MemErr >= 0.01 && l.MemErr <= l.MemWarn && whole(l.MemErr), "Arbeitsspeicher rot: zwischen 1 % und gelb.")
	hold := func(d time.Duration, what string) {
		in(d >= time.Minute && d <= 240*time.Minute && d%time.Minute == 0, what+": 1 bis 240 Minuten.")
	}
	hold(l.MemHold, "Arbeitsspeicher, Dauer")
	in(l.CPUWarn >= 50 && l.CPUWarn <= 100, "Prozessor: 50 bis 100 %.")
	hold(l.CPUHold, "Prozessor, Dauer")
	in(l.StealWarn >= 5 && l.StealWarn <= 100, "Steal: 5 bis 100 %.")
	hold(l.StealHold, "Steal, Dauer")
	in(l.SiteFails >= 1 && l.SiteFails <= 12, "Seite: 1 bis 12 Prüfungen.")
	in(l.Restarts >= 2 && l.Restarts <= 50, "Neustarts: 2 bis 50 in einer Stunde.")
	hold(l.UnhealthyFor, "unhealthy, Dauer")
	in(l.CertWarnDays >= 2 && l.CertWarnDays <= 60, "Zertifikat gelb: 2 bis 60 Tage.")
	in(l.CertErrDays >= 1 && l.CertErrDays <= l.CertWarnDays, "Zertifikat rot: 1 Tag bis gelb.")
	in(l.BackupWarn >= 1 && l.BackupWarn <= 365, "Backup gelb: 1 bis 365 Tage.")
	in(l.BackupErr >= l.BackupWarn && l.BackupErr <= 365, "Backup rot: zwischen gelb und 365 Tagen.")
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, " "))
	}
	return nil
}

// limitLabels beschreiben jede änderbare Grenze fürs Protokoll.
var limitLabels = []struct {
	name string
	val  func(Limits) string
}{
	{"Platte gelb", func(l Limits) string { return pct(l.DiskWarn*100) + " belegt" }},
	{"Platte rot", func(l Limits) string { return pct(l.DiskErr*100) + " belegt" }},
	{"Platte fester Rest", func(l Limits) string { return gbText(l.DiskMinFree) }},
	{"Arbeitsspeicher gelb", func(l Limits) string { return "unter " + pct(l.MemWarn*100) + " frei" }},
	{"Arbeitsspeicher rot", func(l Limits) string { return "unter " + pct(l.MemErr*100) + " frei" }},
	{"Arbeitsspeicher Dauer", func(l Limits) string { return minutes(l.MemHold) }},
	{"Prozessor", func(l Limits) string { return "über " + pct(l.CPUWarn) }},
	{"Prozessor Dauer", func(l Limits) string { return minutes(l.CPUHold) }},
	{"Steal", func(l Limits) string { return "über " + pct(l.StealWarn) }},
	{"Steal Dauer", func(l Limits) string { return minutes(l.StealHold) }},
	{"Seite", func(l Limits) string { return strconv.Itoa(l.SiteFails) + " Prüfungen" }},
	{"Neustarts", func(l Limits) string { return strconv.Itoa(l.Restarts) + " in einer Stunde" }},
	{"unhealthy Dauer", func(l Limits) string { return minutes(l.UnhealthyFor) }},
	{"Zertifikat gelb", func(l Limits) string { return strconv.Itoa(l.CertWarnDays) + " Tage" }},
	{"Zertifikat rot", func(l Limits) string { return strconv.Itoa(l.CertErrDays) + " Tage" }},
	{"Backup gelb", func(l Limits) string { return strconv.Itoa(l.BackupWarn) + " Tage" }},
	{"Backup rot", func(l Limits) string { return strconv.Itoa(l.BackupErr) + " Tage" }},
}

func (r *Rules) limitsFile() string {
	if r.File == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(r.File), "grenzen.json")
}

// loadLimits liest eigene Grenzen. Aufrufer hält r.mu.
func (r *Rules) loadLimits() {
	f := r.limitsFile()
	if f == "" {
		return
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return
	}
	var l Limits
	if json.Unmarshal(b, &l) == nil && l.Validate() == nil {
		r.Limits = l
	}
}

// SetLimits übernimmt neue Grenzen und sagt, was sich geändert hat.
func (r *Rules) SetLimits(l Limits) ([]string, error) {
	l.RestartsIn, l.ClearAfter, l.RemindAfter = DefaultLimits.RestartsIn, DefaultLimits.ClearAfter, DefaultLimits.RemindAfter
	if err := l.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
	old := r.limits()
	var changes []string
	for _, x := range limitLabels {
		if a, b := x.val(old), x.val(l); a != b {
			changes = append(changes, fmt.Sprintf("%s: %s → %s", x.name, a, b))
		}
	}
	if len(changes) == 0 {
		return nil, nil
	}
	if f := r.limitsFile(); f != "" {
		if l == DefaultLimits {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else {
			b, err := json.MarshalIndent(l, "", " ")
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(f+".neu", b, 0o600); err != nil {
				return nil, err
			}
			if err := os.Rename(f+".neu", f); err != nil {
				return nil, err
			}
		}
	}
	r.Limits = l
	return changes, nil
}
