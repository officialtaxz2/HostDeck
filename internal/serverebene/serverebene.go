// Package serverebene bringt beim Umzug mit, was zum Server selbst gehört
// und nicht zu einem Projekt: Zeitzone und Kernel-Einstellungen
// automatisch, zeitgesteuerte Aufgaben und Zugänge (SSH-Schlüssel,
// Docker-Anmeldungen) nur, wenn du sie ankreuzt.
//
// Der alte Server stellt die Punkte zusammen (Source), der neue prüft und
// übernimmt sie (Target). Beim Probe-Umzug ist es nur eine Vorschau: Dann
// gehen keine Inhalte über die Leitung, nur Prüfsummen, und auf dem neuen
// Server ändert sich nichts. Aufgaben liefen dort sonst doppelt, neben denen
// auf dem alten Server. Erst am Umzugstag übernimmt HostDeck sie.
//
// Bestehendes überschreibt HostDeck nie: Liegt eine Datei auf dem neuen Server
// schon anders, bleibt sie und steht in der Liste. Bei authorized_keys und
// known_hosts kommen nur fehlende Zeilen dazu, bei der Docker-Anmeldung nur
// fehlende Registries.
package serverebene

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Arten von Punkten.
const (
	KindAuto   = "automatisch" // Zeitzone, Kernel: ohne Haken
	KindTask   = "aufgabe"     // Cronjob, Timer, eigener Dienst
	KindAccess = "zugang"      // SSH-Schlüssel, Login-Schlüssel, Docker-Anmeldung
)

// Arten, wie eine Datei auf den neuen Server kommt.
const (
	HowFile    = "datei"   // anlegen, wenn sie fehlt
	HowLines   = "zeilen"  // fehlende Zeilen anhängen (authorized_keys, known_hosts)
	HowDocker  = "docker"  // fehlende Registries in config.json eintragen
	HowCrontab = "crontab" // Crontab eines Benutzers; der Ort hängt vom System ab
)

// File ist eine Datei eines Punkts.
type File struct {
	Path  string `json:"pfad"`
	Owner string `json:"besitzer"`
	Group string `json:"gruppe"`
	Mode  uint32 `json:"modus"`
	// DirMode gilt für Ordner, die HostDeck dafür anlegt (0700 bei .ssh); 0:
	// 0755 und root.
	DirMode uint32 `json:"ordner_modus,omitempty"`
	How     string `json:"art"`
	Hash    string `json:"pruefsumme"`
	// Lines: Prüfsummen der Zeilen (HowLines), Names: Registries
	// (HowDocker). Damit sagt die Vorschau, was dazukäme, ohne Inhalte.
	Lines []string `json:"zeilen,omitempty"`
	Names []string `json:"namen,omitempty"`
	// Data ist der Inhalt, nur am Umzugstag.
	Data []byte `json:"inhalt,omitempty"`
}

// Item ist ein Punkt der Server-Ebene.
type Item struct {
	Key    string `json:"schluessel"`
	Kind   string `json:"art"`
	Title  string `json:"titel"`
	Detail string `json:"detail,omitempty"`
	// Manual: Warum HostDeck das nicht übernimmt; dann steht es nur in der
	// Liste.
	Manual string `json:"von_hand,omitempty"`
	// Users muss es auf dem neuen Server geben, Needs sind Programme und
	// Dateien, die die Aufgabe dort braucht.
	Users []string `json:"benutzer,omitempty"`
	Needs []string `json:"braucht,omitempty"`
	Files []File   `json:"dateien,omitempty"`
	// Units schaltet HostDeck ein und startet sie.
	Units    []string `json:"einschalten,omitempty"`
	Cron     bool     `json:"cron,omitempty"`
	Sysctl   bool     `json:"sysctl,omitempty"`
	TimeZone string   `json:"zeitzone,omitempty"`
}

// Level geht an den neuen Server.
type Level struct {
	Apply bool   `json:"uebernehmen"`
	Items []Item `json:"punkte"`
}

// Selectable sagt, ob es für den Punkt einen Haken gibt.
func (it Item) Selectable() bool { return it.Kind != KindAuto && it.Manual == "" }

// Choose nimmt die automatischen Punkte, die angekreuzten und die zum
// Nachsehen (von Hand). Ohne apply bleiben die Inhalte daheim.
func Choose(items []Item, chosen map[string]bool, apply bool) Level {
	lv := Level{Apply: apply}
	for _, it := range items {
		if it.Selectable() && !chosen[it.Key] {
			continue
		}
		if !apply {
			it = it.withoutData()
		}
		lv.Items = append(lv.Items, it)
	}
	return lv
}

func (it Item) withoutData() Item {
	files := make([]File, len(it.Files))
	for i, f := range it.Files {
		f.Data = nil
		files[i] = f
	}
	it.Files = files
	return it
}

// Strip nimmt allen Punkten die Inhalte, etwa für den Leitstand.
func Strip(items []Item) []Item {
	out := make([]Item, len(items))
	for i, it := range items {
		out[i] = it.withoutData()
	}
	return out
}

func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// keyLines sind die Zeilen, die zählen: ohne Leerzeilen und Kommentare.
func keyLines(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func lineHashes(b []byte) []string {
	var out []string
	for _, l := range keyLines(b) {
		out = append(out, hashOf([]byte(l)))
	}
	return out
}

// dockerAuths liest die Registries aus config.json, nie die Zugangsdaten.
func dockerAuths(b []byte) (map[string]json.RawMessage, bool) {
	var cfg struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil, false
	}
	if cfg.Auths == nil {
		cfg.Auths = map[string]json.RawMessage{}
	}
	return cfg.Auths, true
}

// dockerHelper sagt, ob config.json die Zugangsdaten nicht selbst enthält:
// Schlüsselspeicher (credsStore, credHelpers) oder Einträge ohne auth.
func dockerHelper(b []byte) bool {
	var cfg struct {
		CredsStore  string            `json:"credsStore"`
		CredHelpers map[string]string `json:"credHelpers"`
		Auths       map[string]struct {
			Auth          string `json:"auth"`
			IdentityToken string `json:"identitytoken"`
		} `json:"auths"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	if cfg.CredsStore != "" || len(cfg.CredHelpers) > 0 {
		return true
	}
	for _, a := range cfg.Auths {
		if a.Auth == "" && a.IdentityToken == "" {
			return true
		}
	}
	return false
}
