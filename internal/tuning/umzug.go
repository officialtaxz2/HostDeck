package tuning

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Umzug: Dateien von vps-tune ziehen nicht einzeln mit. Der alte
// Server hält fest, womit tune.sh lief (applied.conf, CPU-Vorrang), und der
// neue Leitstand bietet dasselbe Tuning dort mit Vorschau an.

// Marker steht in jeder Datei, die tune.sh schreibt (MARK im Skript).
const Marker = `# Verwaltet von vps-tune – wird bei "vps-tune restore" entfernt.`

// Managed sagt, ob ein Dateiinhalt von vps-tune stammt; tune.sh prüft das
// genauso (grep -F).
func Managed(b []byte) bool { return bytes.Contains(b, []byte(Marker)) }

// PrioSet ist ein Dienst, dem vps-tune prio einen CPU-Vorrang gegeben hat.
type PrioSet struct {
	Unit  string `json:"dienst"`
	Level string `json:"stufe"` // high oder low
}

// PrioDropIn ist die Datei, die systemctl set-property für prio anlegt.
func PrioDropIn(unit string) string {
	return "/etc/systemd/system.control/" + unit + ".d/50-CPUWeight.conf"
}

// ReadPrio liest unter root (leer: /), welchen Diensten vps-tune einen
// Vorrang gegeben hat: die Dienste aus prio.list mit dem CPUWeight aus
// ihrem Drop-in. Andere Werte als hoch und niedrig zählen nicht.
func ReadPrio(root string) []PrioSet {
	b, err := os.ReadFile(filepath.Join(root, PrioList))
	if err != nil {
		return nil
	}
	var out []PrioSet
	for _, u := range strings.Split(string(b), "\n") {
		if u = strings.TrimSpace(u); u == "" || !ValidUnit(u) {
			continue
		}
		d, err := os.ReadFile(filepath.Join(root, PrioDropIn(u)))
		if err != nil {
			continue
		}
		w := 0
		for _, l := range strings.Split(string(d), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "CPUWeight="); ok {
				w, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		for lv, n := range Levels {
			if n != 0 && n == w {
				out = append(out, PrioSet{Unit: u, Level: lv})
			}
		}
	}
	return out
}

// Offer ist, was der neue Server vom Tuning des alten übernehmen kann: das
// Preset und die abweichenden Schalter. Die Netzwerkkarte (--iface) bleibt
// draußen; sie heißt auf dem neuen Server meist anders, und das Skript
// findet sie selbst.
func (a *Applied) Offer() Options {
	o := a.Options()
	delete(o.Set, "iface")
	return o
}

// Same sagt, ob hier dasselbe Tuning angewendet ist wie in a (ohne die
// Netzwerkkarte).
func (a *Applied) Same(here *Applied) bool {
	if a == nil || here == nil || a.Preset != here.Preset {
		return false
	}
	x, y := a.Offer(), here.Offer()
	if len(x.Set) != len(y.Set) {
		return false
	}
	for k, v := range x.Set {
		if y.Set[k] != v {
			return false
		}
	}
	return true
}
