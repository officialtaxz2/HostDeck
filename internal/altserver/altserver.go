// Package altserver hält fest, was HostDeck auf dem alten Server selbst
// eingestellt hatte: geschlossene Ports, die Log-Grenze von
// Docker und den Swap-Puffer; dazu das Tuning von vps-tune. Beim Umzug zieht das als alter-server.json mit;
// der neue Leitstand schlägt dasselbe dort neu vor, berechnet für dessen
// Ports, und ändert nichts von selbst.
package altserver

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
)

// Port ist ein Port oder Bereich, den HostDeck auf dem alten Server geschlossen
// hatte.
type Port struct {
	Proto string `json:"proto"`
	From  int    `json:"von"`
	To    int    `json:"bis"`
	Owner string `json:"wer,omitempty"`
}

// Info ist der Stand des alten Servers.
type Info struct {
	From   string `json:"von,omitempty"` // Rechnername des alten Servers
	Closed []Port `json:"geschlossen,omitempty"`
	Logs   bool   `json:"log_grenze,omitempty"`
	Swap   int64  `json:"swap,omitempty"` // Größe des Swap-Puffers von HostDeck
	// Tuning: womit tune.sh zuletzt lief (applied.conf); Prio: CPU-Vorrang
	// je Dienst von vps-tune prio.
	Tuning *tuning.Applied  `json:"tuning,omitempty"`
	Prio   []tuning.PrioSet `json:"vorrang,omitempty"`
}

// Empty sagt, ob es nichts vorzuschlagen gibt.
func (i *Info) Empty() bool {
	return i == nil || (len(i.Closed) == 0 && !i.Logs && i.Swap == 0 && i.Tuning == nil && len(i.Prio) == 0)
}

// Build liest aus dem Datenordner dieses Servers, was HostDeck hier eingestellt
// hat, und von der Platte das Tuning. Fehlende oder kaputte Dateien zählen
// als nichts eingestellt.
func Build(dataDir, from string) Info { return BuildIn("", dataDir, from) }

// BuildIn ist Build mit einer anderen Wurzel für vps-tune (Tests).
func BuildIn(root, dataDir, from string) Info {
	info := Info{From: from, Tuning: tuning.ReadApplied(root), Prio: tuning.ReadPrio(root)}
	var shield struct {
		Closed []Port `json:"geschlossen"`
	}
	if b, err := os.ReadFile(filepath.Join(dataDir, ort.ShieldFile)); err == nil && json.Unmarshal(b, &shield) == nil {
		for _, p := range shield.Closed {
			if p.Proto != "" && p.From > 0 && p.To >= p.From {
				info.Closed = append(info.Closed, p)
			}
		}
	}
	var perf struct {
		Logs *struct{} `json:"logs"`
		Swap *struct {
			Size int64 `json:"groesse"`
		} `json:"swap"`
	}
	if b, err := os.ReadFile(filepath.Join(dataDir, ort.PerfDir, "zustand.json")); err == nil && json.Unmarshal(b, &perf) == nil {
		info.Logs = perf.Logs != nil
		if perf.Swap != nil && perf.Swap.Size > 0 {
			info.Swap = perf.Swap.Size
		}
	}
	return info
}

// Load liest alter-server.json aus dem Datenordner; nil: kein Umzug hierher
// oder nichts vorzuschlagen.
func Load(dataDir string) *Info {
	b, err := os.ReadFile(filepath.Join(dataDir, ort.OldServerFile))
	if err != nil {
		return nil
	}
	var info Info
	if json.Unmarshal(b, &info) != nil || info.Empty() {
		return nil
	}
	return &info
}

// DropTuning streicht das Tuning aus alter-server.json, wenn du es hier
// nicht übernehmen willst; bleibt sonst nichts, fällt die Datei weg.
func DropTuning(dataDir string) error {
	p := filepath.Join(dataDir, ort.OldServerFile)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		return err
	}
	info.Tuning, info.Prio = nil, nil
	if info.Empty() {
		return os.Remove(p)
	}
	b, _ = json.Marshal(info)
	return outdir.WritePrivate(p, b)
}

// Has sagt, ob der alte Server diesen Port oder Bereich geschlossen hatte,
// auch als Teil eines größeren Bereichs.
func (i *Info) Has(proto string, from, to int) bool {
	if i == nil {
		return false
	}
	for _, p := range i.Closed {
		if p.Proto == proto && p.From <= from && p.To >= to {
			return true
		}
	}
	return false
}
