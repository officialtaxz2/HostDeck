package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/update"
)

// Updates im Leitstand: Der Dienst nimmt die hochgeladene Datei an, prüft
// die Signatur und legt das Programm nach /var/lib/hostdeck/update. Eingespielt
// wird es von einem eigenen, kurzlebigen systemd-Dienst
// („hostdeck einrichten --nach-update“ aus der neuen Datei), denn der Dienst kann
// sich nicht selbst neu starten und dabei weiterarbeiten. Klappt der Start
// der neuen Version nicht, kommt die alte zurück.

// updateReceiver baut die Funktion, mit der der Dienst Updates annimmt.
// start stößt das Einspielen an.
type updateReceiver struct {
	Dir     string // Ablage, etwa /var/lib/hostdeck/update
	RunDir  string // Laufsperre
	Key     ed25519.PublicKey
	Version string // laufende Version
	Start   func(ctx context.Context, prog string) error
}

func (u *updateReceiver) receive(ctx context.Context, body io.Reader) (update.Header, error) {
	l, err := lauf.TakeIn(u.RunDir, "Update")
	if err != nil {
		return update.Header{}, err
	}
	defer l.Release()
	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		return update.Header{}, err
	}
	prog := filepath.Join(u.Dir, "hostdeck")
	tmp := prog + ".teil"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return update.Header{}, err
	}
	defer os.Remove(tmp)
	h, err := update.Verify(body, u.Key, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return h, err
	}
	if h.OS != runtime.GOOS || h.Arch != runtime.GOARCH {
		return h, fmt.Errorf("Die Datei ist für %s/%s, dieser Server braucht %s/%s. Nimm die Update-Datei mit „%s“ im Namen.", h.OS, h.Arch, runtime.GOOS, runtime.GOARCH, runtime.GOARCH)
	}
	if h.Version == u.Version {
		return h, fmt.Errorf("Version %s läuft schon. Nichts eingespielt.", h.Version)
	}
	// Ältere signierte Dateien nicht wieder einspielen: Sonst käme eine
	// Version mit einem längst behobenen Fehler zurück.
	if last, ok := readInstalled(u.Dir); ok && !h.Made.After(last.Made) {
		return h, fmt.Errorf("Die Datei (Version %s, gebaut %s) ist nicht neuer als das zuletzt eingespielte Update (Version %s, gebaut %s). Ältere Versionen spielt HostDeck nicht wieder ein; nimm die neueste Update-Datei.",
			h.Version, h.Made.Local().Format("02.01.2006 15:04"), last.Version, last.Made.Local().Format("02.01.2006 15:04"))
	}
	head, err := json.Marshal(h)
	if err != nil {
		return h, err
	}
	if err := os.WriteFile(prog+headSuffix, head, 0o600); err != nil {
		return h, err
	}
	if err := os.Rename(tmp, prog); err != nil {
		return h, err
	}
	if err := u.Start(ctx, prog); err != nil {
		return h, fmt.Errorf("Einspielen ließ sich nicht starten: %w", err)
	}
	return h, nil
}

// headSuffix: neben dem bereitgelegten Programm liegt dessen Kopf (Version,
// Bauzeit). Klappt das Einspielen, wird er zu installedFile.
const (
	headSuffix    = ".kopf"
	installedFile = "eingespielt.json"
)

// readInstalled liest den Kopf des zuletzt eingespielten Updates.
func readInstalled(dir string) (update.Header, bool) {
	var h update.Header
	b, err := os.ReadFile(filepath.Join(dir, installedFile))
	if err != nil || json.Unmarshal(b, &h) != nil || h.Made.IsZero() {
		return update.Header{}, false
	}
	return h, true
}

// markInstalled merkt sich nach geglücktem Einspielen, welches Update jetzt
// läuft (prog ist das bereitgelegte Programm). Ohne Kopf, etwa beim
// Einrichten von Hand, bleibt der alte Stand.
func markInstalled(prog string) error {
	err := os.Rename(prog+headSuffix, filepath.Join(filepath.Dir(prog), installedFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// startUpdate startet „prog einrichten --nach-update“ als eigenen
// systemd-Dienst außerhalb von hostdeck-dienst.
func startUpdate(ctx context.Context, prog string) error {
	unit := "hostdeck-update-" + strconv.FormatInt(time.Now().Unix(), 10)
	out, err := exec.CommandContext(ctx, "systemd-run", "--unit="+unit, "--collect", "--no-block", "--quiet",
		"--description=HostDeck: neue Version einspielen", prog, "einrichten", "--nach-update").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applyUpdate spielt die laufende Datei (die neue Version) ein: alte
// Version beiseitelegen, einrichten, warten, bis der Dienst mit der neuen
// Version antwortet; sonst die alte zurück.
func applyUpdate(ctx context.Context, in *installer, journal *protokoll.Log, running func(ctx context.Context) (string, error), wait time.Duration) error {
	prog := in.prog()
	backup := prog + ".alt"
	before, _ := running(ctx)
	if before == "" {
		before = "unbekannt"
	}
	if err := copyExecutable(prog, backup); err != nil {
		return fmt.Errorf("alte Version nicht gesichert: %w", err)
	}
	// Die neue Version schreibt auch die Dienst-Dateien neu; der Rückweg
	// braucht die alten.
	units := saveUnits(in.UnitDir)
	fail := func(why error) error {
		in.printf("Update fehlgeschlagen: %v\nHole die alte Version zurück …\n", why)
		_ = os.Remove(in.Exe + headSuffix)
		err := copyExecutable(backup, prog)
		if _, serr := os.Stat(filepath.Join(in.UnitDir, ort.UnitSocket)); err == nil && serr == nil && !units.has(ort.UnitSocket) {
			// Erst stoppen, solange systemd die Unit noch kennt.
			_ = in.Run(ctx, "systemctl", "disable", "--now", ort.UnitSocket)
		}
		if err == nil {
			err = units.restore()
		}
		if err == nil {
			err = in.Run(ctx, "systemctl", "daemon-reload")
		}
		if err == nil {
			steps := [][]string{{"restart", ort.UnitDienst}, {"restart", ort.UnitWeb}}
			if units.has(ort.UnitSocket) {
				steps = [][]string{{"restart", ort.UnitDienst}, {"stop", ort.UnitWeb}, {"restart", ort.UnitSocket}, {"start", ort.UnitWeb}}
			}
			for _, args := range steps {
				if rerr := in.Run(ctx, "systemctl", args...); rerr != nil && err == nil {
					err = rerr
				}
			}
		}
		text := fmt.Sprintf("Update auf %s fehlgeschlagen (%v); Version %s läuft wieder", version, why, before)
		if err != nil {
			text = fmt.Sprintf("Update auf %s fehlgeschlagen (%v); alte Version nicht zurückgeholt: %v", version, why, err)
		}
		_ = journal.Add(protokoll.Entry{Kind: protokoll.Update, Text: text, Addr: "Server (Update)"})
		return errors.New(text)
	}
	if err := in.install(ctx); err != nil {
		return fail(err)
	}
	if err := waitForVersion(ctx, running, wait); err != nil {
		return fail(err)
	}
	if err := markInstalled(in.Exe); err != nil {
		in.printf("  Hinweis: Stand des Updates nicht gemerkt: %v\n", err)
	}
	_ = journal.Add(protokoll.Entry{Kind: protokoll.Update, Text: fmt.Sprintf("Update eingespielt: Version %s (vorher %s)", version, before), Addr: "Server (Update)"})
	in.printf("Update eingespielt: Version %s (vorher %s).\n", version, before)
	return nil
}

// savedUnits hält die Dienst-Dateien von HostDeck vor dem Update; ein Eintrag
// nil: Die Datei gab es nicht.
type savedUnits struct {
	dir   string
	files map[string][]byte
}

func saveUnits(dir string) savedUnits {
	s := savedUnits{dir: dir, files: map[string][]byte{}}
	for _, name := range []string{ort.UnitDienst, ort.UnitWeb, ort.UnitSocket} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			b = nil
		}
		s.files[name] = b
	}
	return s
}

func (s savedUnits) has(name string) bool { return s.files[name] != nil }

// restore schreibt die alten Dienst-Dateien zurück und entfernt neue.
func (s savedUnits) restore() error {
	for name, b := range s.files {
		p := filepath.Join(s.dir, name)
		if b == nil {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if old, err := os.ReadFile(p); err == nil && string(old) == string(b) {
			continue
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func dienstVersion(c *dienst.Client) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		st, err := c.Status(ctx)
		return st.Version, err
	}
}
