package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/altserver"
	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/leistungstest"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/remote"
	"github.com/officialtaxz2/hostdeck/internal/restore"
)

// HostDeck zieht mit um. Nach dem Zurückspielen richtet der alte
// Server HostDeck auf dem neuen als Dienst ein und spielt ein kleines
// Datenpaket ein: Leitstand-Passwort, Protokoll, Zugang von außen, Auswahl,
// Domains, Überwachung und die Ergebnisse des Leistungstests. Nicht dabei sind Anmeldungen und Sperren (gehören
// zu diesem Server), Umzug, Backups und Papierkorb. Ist der neue Leitstand
// eine Probe-Kopie, liegt dort probe-kopie.json: Dann schweigen die
// Mitteilungen, Backups und Umzüge gehen dort nicht, und Anhalten und
// Entfernen nehmen den Leitstand mit.

// takeoverFiles ziehen mit, relativ zum Datenordner.
// Die Domains von Hand (Spielserver, Mail) gehören dazu: Der neue Leitstand
// prüft sie nach der Übernahme mit und kennt sie beim nächsten Umzug (Labor S4).
var takeoverFiles = []string{ort.PasswordFile, ort.JournalFile, ort.AccessFile, ort.DomainsFile, "ergebnis/manifest.yaml", ort.MoveDir + "/" + dnsExtraFile}

// takeoverMonitorSkip bleibt in ueberwachung/: Welche Warnung gerade gilt,
// misst jeder Server selbst.
var takeoverMonitorSkip = map[string]bool{"warnungen.json": true}

// maxTakeover begrenzt das Datenpaket (ungepackt).
const maxTakeover = 128 << 20

// takeoverAllowed sagt, ob ein Name im Paket stehen darf.
func takeoverAllowed(name string) bool {
	if name != path.Clean(name) || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return false
	}
	for _, f := range takeoverFiles {
		if name == f {
			return true
		}
	}
	if name == ort.OldServerFile {
		return true
	}
	dir, base, ok := strings.Cut(name, "/")
	if ok && dir == ort.BenchDir {
		// Ergebnisse des Leistungstests, damit der Vergleich alter und
		// neuer Server möglich bleibt; nicht der Arbeitsordner (Geekbench).
		ext := path.Ext(base)
		return (ext == ".json" || ext == ".zip" || ext == ".claim") && leistungstest.ValidID(strings.TrimSuffix(base, ext))
	}
	return ok && dir == ort.MonitorDir && base != "" && !strings.Contains(base, "/") &&
		!strings.HasPrefix(base, ".") && !takeoverMonitorSkip[base]
}

// packTakeover packt die Daten des Leitstands für den neuen Server.
func packTakeover(dataDir string) ([]byte, []string, error) {
	names := append([]string{}, takeoverFiles...)
	for _, dir := range []string{ort.MonitorDir, ort.BenchDir} {
		es, err := os.ReadDir(filepath.Join(dataDir, dir))
		if err != nil {
			continue
		}
		for _, e := range es {
			n := dir + "/" + e.Name()
			if e.Type().IsRegular() && takeoverAllowed(n) {
				names = append(names, n)
			}
		}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	var packed []string
	total := 0
	for _, n := range names {
		b, err := readRegular(filepath.Join(dataDir, filepath.FromSlash(n)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %v", n, err)
		}
		if total += len(b); total > maxTakeover {
			return nil, nil, fmt.Errorf("die Daten des Leitstands sind größer als %d MB", maxTakeover>>20)
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return nil, nil, err
		}
		if _, err := tw.Write(b); err != nil {
			return nil, nil, err
		}
		packed = append(packed, n)
	}
	// Was HostDeck hier eingestellt hat, schlägt der neue Leitstand dort neu vor.
	host, _ := os.Hostname()
	if old := altserver.BuildIn(tuneRoot, dataDir, host); !old.Empty() {
		b, _ := json.Marshal(old)
		if err := tw.WriteHeader(&tar.Header{Name: ort.OldServerFile, Mode: 0o600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return nil, nil, err
		}
		if _, err := tw.Write(b); err != nil {
			return nil, nil, err
		}
		packed = append(packed, ort.OldServerFile)
	}
	if err := tw.Close(); err != nil {
		return nil, nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), packed, nil
}

// unpackTakeover spielt das Paket in dataDir ein. Es nimmt nur Namen aus
// der festen Liste und nur gewöhnliche Dateien.
func unpackTakeover(pkg []byte, dataDir string) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(pkg))
	if err != nil {
		return nil, fmt.Errorf("Datenpaket unlesbar: %v", err)
	}
	tr := tar.NewReader(gz)
	type file struct {
		name string
		data []byte
	}
	var files []file
	total := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Datenpaket unlesbar: %v", err)
		}
		if h.Typeflag != tar.TypeReg || !takeoverAllowed(h.Name) {
			return nil, fmt.Errorf("Datenpaket enthält %q; das nimmt HostDeck nicht an", h.Name)
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxTakeover-int64(total)+1))
		if err != nil {
			return nil, err
		}
		if total += len(b); total > maxTakeover {
			return nil, errors.New("Datenpaket zu groß")
		}
		files = append(files, file{h.Name, b})
	}
	// Erst alles lesen, dann schreiben: Ein kaputtes Paket ändert nichts.
	var names []string
	for _, f := range files {
		p := filepath.Join(dataDir, filepath.FromSlash(f.name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return names, err
		}
		if err := outdir.WritePrivate(p, f.data); err != nil {
			return names, err
		}
		names = append(names, f.name)
	}
	return names, nil
}

// copyMarker ist probe-kopie.json.
type copyMarker struct {
	From       string    `json:"von"`
	Since      time.Time `json:"seit"`
	TestDomain string    `json:"testadresse,omitempty"`
	Version    string    `json:"sbm"` // Kennung im Datenformat, bleibt für ältere Dateien
	// Move: am Umzugstag eingerichtet. Zeigen die Domains hierher, wird
	// aus der Kopie der echte Leitstand.
	Move bool `json:"umzug,omitempty"`
}

// loadCopy liest probe-kopie.json; nil: Dieser Leitstand ist keine
// Probe-Kopie.
func loadCopy(dataDir string) *copyMarker {
	b, err := os.ReadFile(filepath.Join(dataDir, ort.CopyFile))
	if err != nil {
		return nil
	}
	var m copyMarker
	if json.Unmarshal(b, &m) != nil {
		// Unlesbar heißt trotzdem Kopie: lieber zu still als doppelt.
		return &copyMarker{}
	}
	return &m
}

func copyInfo(dataDir string) *dienst.CopyInfo {
	m := loadCopy(dataDir)
	if m == nil {
		return nil
	}
	return &dienst.CopyInfo{From: m.From, Since: m.Since, TestDomain: m.TestDomain, Move: m.Move}
}

// takeover übernimmt auf dem neuen Server den Leitstand. Felder für Tests.
type takeover struct {
	DataDir, UnitDir string
	Out              io.Writer
	Now              func() time.Time
	// Install richtet HostDeck als Dienst ein (wie hostdeck einrichten).
	Install func(ctx context.Context) error
	// CaddyGroup ist die Gruppe von Caddy hier.
	CaddyGroup func(ctx context.Context) (string, error)
	// Refresh schreibt die Caddy-Datei des Leitstands neu (mit der
	// Testadresse) und prüft wie beim Freischalten.
	Refresh func(ctx context.Context) (string, error)
	// Site wartet, bis eine Adresse die Anmeldeseite zeigt.
	Site func(ctx context.Context, domain string) error
	// Discover macht die erste Bestandsaufnahme; nil: keine.
	Discover func(ctx context.Context) error
}

func (t *takeover) printf(format string, args ...any) { fmt.Fprintf(t.Out, format, args...) }

// existing sagt, ob hier schon ein eigener Leitstand läuft, den HostDeck nicht
// als Probe-Kopie angelegt hat.
func (t *takeover) existing() bool {
	if loadCopy(t.DataDir) != nil {
		return false
	}
	for _, p := range []string{filepath.Join(t.DataDir, ort.PasswordFile), filepath.Join(t.UnitDir, ort.UnitDienst)} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func (t *takeover) Run(ctx context.Context, cfg remote.Config) int {
	t.printf("Leitstand\n")
	if !cfg.Probe {
		t.printf("  ✗ Den Leitstand übernimmt HostDeck in dieser Version nur als Probe-Kopie.\n")
		return restore.ExitBlocked
	}
	if t.existing() {
		t.printf("  ✗ Hier läuft schon ein eigener Leitstand von HostDeck. Die Probe-Kopie überschreibt ihn nicht; der Leitstand bleibt, wie er ist.\n")
		return restore.ExitBlocked
	}
	if err := os.MkdirAll(t.DataDir, 0o700); err != nil {
		t.printf("  ✗ %s: %v\n", t.DataDir, err)
		return restore.ExitBlocked
	}
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	// Erst die Markierung: Bricht danach etwas ab, nimmt Entfernen den
	// halben Leitstand trotzdem mit.
	mk, _ := json.MarshalIndent(copyMarker{From: cfg.From, Since: now().UTC(), TestDomain: cfg.TestDomain, Version: version, Move: cfg.Move}, "", "  ")
	if err := outdir.WritePrivate(filepath.Join(t.DataDir, ort.CopyFile), append(mk, '\n')); err != nil {
		t.printf("  ✗ %s: %v\n", ort.CopyFile, err)
		return restore.ExitFailed
	}
	names, err := unpackTakeover(cfg.Package, t.DataDir)
	if err != nil {
		t.printf("  ✗ Daten des Leitstands: %v\n", err)
		return restore.ExitFailed
	}
	sort.Strings(names)
	t.printf("  ✓ Daten des Leitstands übernommen (%d Dateien: Passwort, Protokoll, Auswahl, Domains, Überwachung)\n", len(names))
	if cfg.Move {
		t.printf("  Der Leitstand hier bleibt still (keine Backups, keine Mitteilungen), bis die Domains hierher zeigen; dann übernimmt er von selbst.\n")
	}
	if i := sort.SearchStrings(names, ort.OldServerFile); i == len(names) || names[i] != ort.OldServerFile {
		_ = os.Remove(filepath.Join(t.DataDir, ort.OldServerFile)) // vom letzten Auffrischen
	} else if old := altserver.Load(t.DataDir); old != nil {
		var pages []string
		if len(old.Closed) > 0 {
			pages = append(pages, "Sicherheit")
		}
		if old.Logs || old.Swap > 0 {
			pages = append(pages, "Leistung")
		}
		if old.Tuning != nil || len(old.Prio) > 0 {
			pages = append(pages, "Tuning")
		}
		list := strings.Join(pages, " und ")
		if n := len(pages); n > 2 {
			list = strings.Join(pages[:n-1], ", ") + " und " + pages[n-1]
		}
		t.printf("  ✓ Einstellungen vom alten Server: %s; der Leitstand hier schlägt sie unter %s neu vor\n", oldText(old), list)
	}

	st, err := loadAccess(t.DataDir)
	if err != nil {
		t.printf("  ! Zugang von außen nicht lesbar (%v); der Leitstand geht hier nur über den Notausgang.\n", err)
		_ = removeAccess(t.DataDir)
		st = nil
	}
	if st != nil {
		why := ""
		if _, err := os.Stat(st.Site); err != nil {
			why = st.Site + " fehlt hier (kam Caddy nicht mit?)"
		} else if g, err := t.CaddyGroup(ctx); err != nil {
			why = "Caddy ist hier nicht erreichbar (" + err.Error() + ")"
		} else {
			st.Group = g
		}
		if why != "" {
			t.printf("  ! %s; der Leitstand geht hier nur über den Notausgang.\n", why)
			_ = removeAccess(t.DataDir)
			st = nil
		}
	}
	if st != nil {
		st.Extra = nil
		if cfg.TestDomain != "" && cfg.TestDomain != st.Domain {
			st.Extra = []string{cfg.TestDomain}
		}
		if err := saveAccess(t.DataDir, st); err != nil {
			t.printf("  ✗ %s: %v\n", ort.AccessFile, err)
			return restore.ExitFailed
		}
	}

	t.printf("  HostDeck als Dienst einrichten …\n")
	if err := t.Install(ctx); err != nil {
		t.printf("  ✗ Einrichten: %v\n", err)
		return restore.ExitFailed
	}
	t.printf("  ✓ HostDeck %s läuft hier als Dienst (hostdeck-dienst, hostdeck-web)\n", version)
	code := restore.ExitOK
	if t.Discover != nil {
		if _, err := os.Stat(filepath.Join(t.DataDir, "ergebnis", "inventory.json")); err != nil {
			t.printf("  Erste Bestandsaufnahme (ändert nichts) …\n")
			if err := t.Discover(ctx); err != nil {
				t.printf("  ! Bestandsaufnahme: %v; der Leitstand zeigt, was da ist.\n", err)
			} else {
				t.printf("  ✓ Bestandsaufnahme fertig\n")
			}
		}
	}
	if st == nil {
		t.printf("  Die Probe-Kopie öffnest du dort über den Notausgang: sudo hostdeck notausgang\n")
		return code
	}
	if msg, err := t.Refresh(ctx); err != nil {
		t.printf("  ! Caddy-Datei des Leitstands: %v\n", err)
		code = restore.ExitFailed
	} else if msg != "" {
		t.printf("  ✓ Caddy-Datei des Leitstands hier angepasst (%s)\n", strings.Join(append([]string{st.Domain}, st.Extra...), ", "))
	}
	if len(st.Extra) == 0 {
		t.printf("  Die Probe-Kopie antwortet unter https://%s, sobald die Domain hierher zeigt; bis dahin über den Notausgang: sudo hostdeck notausgang\n", st.Domain)
		return code
	}
	test := st.Extra[0]
	t.printf("  Warte auf https://%s (Caddy holt beim ersten Mal ein Zertifikat) …\n", test)
	if err := t.Site(ctx, test); err != nil {
		t.printf("  ! %v\n", err)
		t.printf("  Bis das geht, erreichst du die Probe-Kopie über den Notausgang: sudo hostdeck notausgang\n")
		return code
	}
	what := "Probe-Kopie des Leitstands"
	if cfg.Move {
		what = "Neuer Leitstand, bis zur Übernahme unter der Testadresse"
	}
	t.printf("  ✓ %s: https://%s (Anmeldung mit deinem Leitstand-Passwort)\n", what, test)
	return code
}

// takeoverCmd ist die Aktion hostdeck-uebernahme des Agenten.
func takeoverCmd(ctx context.Context, cfg remote.Config, w io.Writer) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(w, "  ✗ Dafür braucht HostDeck root.")
		return restore.ExitBlocked
	}
	if fi, err := os.Stat("/run/systemd/system"); err != nil || !fi.IsDir() {
		fmt.Fprintln(w, "Leitstand\n  ✗ Auf diesem Server läuft kein systemd; HostDeck als Dienst braucht systemd.")
		return restore.ExitBlocked
	}
	lock, ok := takeLauf("Leitstand übernehmen", w)
	if !ok {
		return restore.ExitBlocked
	}
	defer lock.Release()
	in, err := systemInstaller(io.Discard)
	if err != nil {
		fmt.Fprintf(w, "  ✗ %v\n", err)
		return restore.ExitBlocked
	}
	pub := systemPublisher(in)
	t := &takeover{
		DataDir: ort.DataDir, UnitDir: ort.UnitDir, Out: w,
		Install: in.install,
		CaddyGroup: func(ctx context.Context) (string, error) {
			c, err := caddy.Find(ctx, caddy.Exec)
			if err != nil {
				return "", err
			}
			return c.Group, nil
		},
		Refresh: pub.Refresh,
		Site: func(ctx context.Context, d string) error {
			return pub.waitSite(ctx, d, func(string, string, string) {})
		},
		Discover: func(ctx context.Context) error {
			deps := discover.Deps{Docker: dockerapi.New(dockerapi.DefaultSocket), FS: hostfs.New("/"), Git: discover.ExecGit{}, Caddy: discover.ExecCaddy{}}
			opts := discover.Options{ToolVersion: "HostDeck " + version, CaddyConfig: "/etc/caddy/Caddyfile", SizeTimeout: 3 * time.Minute, Euid: 0, Log: io.Discard}
			var errs bytes.Buffer
			if code := runDiscover(ctx, deps, opts, ort.ResultDir, io.Discard, &errs); code != 0 {
				return fmt.Errorf("Code %d: %s", code, strings.TrimSpace(errs.String()))
			}
			return nil
		},
	}
	return t.Run(ctx, cfg)
}

// copyLeitstand ist der Leitstand einer Probe-Kopie für Anhalten und
// Entfernen (restore.LeitstandHost).
type copyLeitstand struct {
	in *installer
	// RemoveAll und DelUser für Tests; nil: echt.
	RemoveAll func(string) error
	DelUser   func(ctx context.Context, name string) error
}

func (c *copyLeitstand) Copy() bool { return loadCopy(c.in.DataDir) != nil }

func (c *copyLeitstand) units() []string {
	units := []string{ort.UnitWeb, ort.UnitDienst}
	if _, err := os.Stat(filepath.Join(c.in.UnitDir, ort.UnitSocket)); err == nil {
		units = append([]string{ort.UnitSocket}, units...)
	}
	return units
}

func (c *copyLeitstand) Stop(ctx context.Context) error {
	return c.in.Run(ctx, "systemctl", append([]string{"disable", "--now"}, c.units()...)...)
}

func (c *copyLeitstand) Remove(ctx context.Context) error {
	if !c.Copy() {
		return errors.New("der Leitstand hier ist keine Probe-Kopie")
	}
	if err := c.in.remove(ctx); err != nil {
		return err
	}
	rm := c.RemoveAll
	if rm == nil {
		rm = os.RemoveAll
	}
	for _, d := range []string{c.in.ProgDir, c.in.DataDir} {
		if err := rm(d); err != nil {
			return err
		}
	}
	del := c.DelUser
	if del == nil {
		del = func(ctx context.Context, name string) error { return c.in.Run(ctx, "userdel", name) }
	}
	if c.in.UserExists(c.in.WebUser) {
		if err := del(ctx, c.in.WebUser); err != nil {
			return fmt.Errorf("Benutzer %s: %v", c.in.WebUser, err)
		}
	}
	return nil
}

// tuneRoot ist die Wurzel, unter der packTakeover das Tuning von vps-tune
// liest; leer: /. Tests setzen sie.
var tuneRoot = ""

// oldText fasst zusammen, was HostDeck auf dem alten Server eingestellt hatte.
func oldText(o *altserver.Info) string {
	var parts []string
	if n := len(o.Closed); n == 1 {
		parts = append(parts, "1 geschlossener Port")
	} else if n > 1 {
		parts = append(parts, fmt.Sprintf("%d geschlossene Ports", n))
	}
	if o.Logs {
		parts = append(parts, "Log-Grenze")
	}
	if o.Swap > 0 {
		parts = append(parts, "Swap-Puffer "+sizeText(o.Swap))
	}
	if o.Tuning != nil {
		parts = append(parts, "Tuning "+o.Tuning.Preset)
	}
	if n := len(o.Prio); n == 1 {
		parts = append(parts, "CPU-Vorrang für 1 Dienst")
	} else if n > 1 {
		parts = append(parts, fmt.Sprintf("CPU-Vorrang für %d Dienste", n))
	}
	return strings.Join(parts, ", ")
}

// testDomain ist die Testadresse des neuen Leitstands: hostdeck.example.de wird
// hostdeck-neu.example.de.
func testDomain(own string) string {
	first, rest, ok := strings.Cut(own, ".")
	if !ok || first == "" || rest == "" {
		return ""
	}
	d := first + "-neu." + rest
	if validDomain(d) != nil {
		return ""
	}
	return d
}

// sizeText schreibt eine Größe lesbar.
func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return strings.Replace(fmt.Sprintf("%.1f GB", float64(n)/(1<<30)), ".", ",", 1)
	case n >= 1<<20:
		return strings.Replace(fmt.Sprintf("%.1f MB", float64(n)/(1<<20)), ".", ",", 1)
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d Bytes", n)
	}
}
