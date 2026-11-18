package restore

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"
)

// CaddyKey ist der Eintrag im Backup mit Konfiguration und Zertifikaten
// von Caddy.
const CaddyKey = "host:caddy"

// CaddyPlan sagt, was von Caddy zurückkommt.
type CaddyPlan struct {
	Snapshot string   `json:"snapshot"`
	Paths    []string `json:"pfade"`
	// Config ist das Caddyfile auf dem alten Server.
	Config string `json:"config,omitempty"`
	// UID und GID des Benutzers caddy auf dem alten Server (0: unbekannt).
	UID int `json:"uid,omitempty"`
	GID int `json:"gid,omitempty"`
}

// CaddyRecord hält fest, was HostDeck an Caddy gemacht hat.
type CaddyRecord struct {
	// Installed: HostDeck hat Caddy hier installiert (beim Entfernen der
	// Probe-Kopie kommt es wieder weg).
	Installed bool     `json:"installiert,omitempty"`
	Paths     []string `json:"pfade,omitempty"`
	Running   bool     `json:"laeuft,omitempty"`
	Error     string   `json:"fehler,omitempty"`
}

// CaddyHost ist Caddy auf diesem Server, so weit HostDeck es braucht.
type CaddyHost interface {
	// Present: Caddy ist als systemd-Dienst installiert.
	Present(ctx context.Context) bool
	// Install installiert Caddy aus der offiziellen Quelle.
	Install(ctx context.Context) error
	Stop(ctx context.Context) error
	// Validate prüft das Caddyfile mit caddy validate.
	Validate(ctx context.Context, config string) error
	// Start schaltet Caddy ein (auch nach einem Neustart des Servers) und
	// sagt, ob es danach läuft; sonst steht in why die letzte Meldung.
	Start(ctx context.Context) (running bool, why string)
	// Disable hält Caddy an und lässt es nach einem Neustart aus.
	Disable(ctx context.Context) error
	// Remove nimmt das von HostDeck installierte Caddy wieder weg (Paket und
	// Quelle).
	Remove(ctx context.Context) error
	// Owner gibt den Dateien unter paths, die dem Benutzer caddy des alten
	// Servers gehörten (Nummern uid, gid), den Benutzer caddy von hier.
	Owner(paths []string, uid, gid int) error
}

// caddy spielt Caddy zurück: installieren, wenn es fehlt, anhalten,
// Dateien zurück, Besitzer richten, prüfen, starten. Ein Fehler hält die
// Projekte nicht auf; sie laufen schon.
func (j *Job) caddy(ctx context.Context, p *CaddyPlan) *CaddyRecord {
	rec := &CaddyRecord{Paths: p.Paths}
	if j.prev != nil && j.prev.Caddy != nil && j.prev.Caddy.Installed {
		rec.Installed = true // von der letzten Probe-Kopie
	}
	j.printf("\nCaddy\n")
	fail := func(format string, args ...any) *CaddyRecord {
		rec.Error = fmt.Sprintf(format, args...)
		j.printf("  ✗ %s\n", rec.Error)
		return rec
	}
	if !j.Caddy.Present(ctx) {
		j.printf("  Caddy installieren (offizielle Quelle) …\n")
		if err := j.Caddy.Install(ctx); err != nil {
			return fail("Caddy installieren: %v", err)
		}
		rec.Installed = true
		j.printf("  ✓ Caddy installiert\n")
	}
	if err := j.Caddy.Stop(ctx); err != nil {
		return fail("Caddy anhalten: %v", err)
	}
	j.printf("  Konfiguration und Zertifikate\n")
	if err := j.Repo.Restore(ctx, p.Snapshot, "", "/"); err != nil {
		return fail("Dateien von Caddy zurückspielen: %v", err)
	}
	if p.UID > 0 || p.GID > 0 {
		if err := j.Caddy.Owner(p.Paths, p.UID, p.GID); err != nil {
			return fail("Besitzer der Dateien von Caddy richten: %v", err)
		}
	}
	config := p.Config
	if config == "" {
		config = "/etc/caddy/Caddyfile"
	}
	if err := j.Caddy.Validate(ctx, config); err != nil {
		return fail("Caddy lehnt die Konfiguration ab (%v); Caddy bleibt aus", err)
	}
	j.printf("  ✓ Caddy prüft die Dateien: in Ordnung\n")
	ok, why := j.Caddy.Start(ctx)
	if !ok {
		return fail("Caddy startet nicht: %s", why)
	}
	rec.Running = true
	j.printf("  %s\n", CaddyRunningLine)
	return rec
}

// caddyConflict: Hier läuft schon ein Caddy, das nicht von HostDeck kommt.
func (j *Job) caddyConflict(ctx context.Context, p *Plan) []string {
	if p.Caddy == nil || j.Caddy == nil || !j.Caddy.Present(ctx) {
		return nil
	}
	if prev, err := LoadRecord(j.StateDir); err == nil && prev.Caddy != nil {
		return nil // von der letzten Probe-Kopie
	}
	return []string{"Caddy ist hier schon installiert; HostDeck ersetzt seine Konfiguration (" + strings.Join(p.Caddy.Paths, ", ") + ")"}
}

// caddyNote ist die Zeile zu Caddy in der Vorprüfung.
func (j *Job) caddyNote(ctx context.Context, p *Plan) (Note, bool) {
	if p.Caddy == nil {
		return Note{}, false
	}
	if j.Caddy == nil {
		return Note{Level: NoteWarn, Text: "Caddy-Konfiguration bleibt im Backup (hier nicht unterstützt)"}, true
	}
	if j.Caddy.Present(ctx) {
		return Note{Level: NoteOK, Text: "Caddy ist installiert"}, true
	}
	when := "beim Zurückspielen"
	if j.PlanOnly {
		when = "beim Probe-Umzug"
	}
	return Note{Level: NoteOK, Text: "Caddy fehlt noch; HostDeck installiert es " + when + " aus der offiziellen Quelle"}, true
}

// stopCaddy hält Caddy für die Probe-Kopie an.
func (p *Probe) stopCaddy(ctx context.Context, rec *Record) bool {
	if rec.Caddy == nil || p.Caddy == nil {
		return true
	}
	if err := p.Caddy.Disable(ctx); err != nil {
		p.printf("  ✗ Caddy anhalten: %v\n", err)
		return false
	}
	p.printf("  ✓ Caddy angehalten (bleibt auch nach einem Neustart aus)\n")
	return true
}

// removeCaddy entfernt Caddy der Probe-Kopie: Hat HostDeck es installiert,
// kommen Paket, Konfiguration und Zertifikate weg; sonst wird es nur
// angehalten.
func (p *Probe) removeCaddy(ctx context.Context, rec *Record, rm func(string) error) bool {
	if rec.Caddy == nil || p.Caddy == nil {
		return true
	}
	if !p.stopCaddy(ctx, rec) {
		return false
	}
	if !rec.Caddy.Installed {
		p.printf("  Caddy war schon vor HostDeck hier: bleibt installiert, nur angehalten.\n")
		return true
	}
	if err := p.Caddy.Remove(ctx); err != nil {
		p.printf("  ✗ Caddy deinstallieren: %v\n", err)
		return false
	}
	ok := true
	for _, d := range rec.Caddy.Paths {
		if !caddyOwnPath(d) {
			continue
		}
		if err := rm(d); err != nil && !os.IsNotExist(err) {
			p.printf("  ✗ %s: %v\n", d, err)
			ok = false
		}
	}
	if ok {
		p.printf("  ✓ Caddy deinstalliert, Konfiguration und Zertifikate entfernt\n")
	}
	return ok
}

// caddyOwnPath: Ordner, die nur Caddy gehören und beim Entfernen weg
// dürfen (Konfiguration unter /etc/caddy, Daten unter .local/share/caddy).
func caddyOwnPath(d string) bool {
	d = path.Clean(d)
	return d == "/etc/caddy" || strings.HasPrefix(d, "/etc/caddy/") ||
		(strings.HasSuffix(d, "/.local/share/caddy") && strings.Count(d, "/") >= 4)
}

// CaddyRunningLine steht in der Ausgabe, wenn Caddy nach dem Zurückspielen
// läuft; daran erkennt der alte Server, dass er die Seiten prüfen kann.
const CaddyRunningLine = "✓ Caddy läuft"

// caddyWait: so lange wartet Start, bis Caddy läuft.
const caddyWait = 15 * time.Second
