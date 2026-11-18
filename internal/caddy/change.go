package caddy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// File ist eine Datei der Konfiguration nach der Änderung. Data nil heißt:
// Die Datei soll es danach nicht mehr geben.
type File struct {
	Path string
	Data []byte
	// Was: So muss die Datei jetzt aussehen (nil: egal). Hat sie sich seit
	// dem Plan geändert, etwa weil jemand das Caddyfile bearbeitet hat,
	// bricht Apply ab, statt diese Änderung still zu überschreiben.
	Was []byte
}

// Change ist eine Änderung an Caddy.
type Change struct {
	Files []File
	// Check läuft nach dem Neuladen, etwa: Die neue Seite antwortet. Ein
	// Fehler nimmt die Änderung zurück.
	Check func(ctx context.Context) error
	// Skip sind Hostnamen, die nicht mit vorher verglichen werden (die
	// Seite, um die es geht).
	Skip []string
}

// Answer ist die Antwort einer Seite.
type Answer struct {
	Status int    // HTTP-Status, 0 bei Fehler
	Err    string // Verbindung oder TLS ging nicht
}

func (a Answer) String() string {
	if a.Err != "" {
		return "Fehler: " + a.Err
	}
	return fmt.Sprintf("Status %d", a.Status)
}

// usable: Die Seite antwortet, und Caddy kommt an das Ziel dahinter.
func (a Answer) usable() bool { return a.Err == "" && a.Status > 0 && a.Status < 500 }

// Worse sagt, ob after schlechter ist als before: Eine Seite, die vorher
// brauchbar antwortete, tut es nicht mehr, oder eine, die vorher überhaupt
// antwortete, ist jetzt gar nicht erreichbar.
func Worse(before, after Answer) bool { return worse(before, after) }

// worse sagt, ob after schlechter ist als before.
func worse(before, after Answer) bool {
	if before.usable() {
		return !after.usable()
	}
	// Vorher schon 5xx: schlechter nur, wenn jetzt gar keine Verbindung geht.
	return before.Err == "" && after.Err != ""
}

// Prober fragt eine Seite über Caddy ab.
type Prober func(ctx context.Context, host string) Answer

// Event ist ein Schritt der Änderung, für Fortschritt und Protokoll.
type Event struct {
	Step   string // etwa „Caddy prüft die Dateien“
	State  string // StateRunning, StateOK, StateFailed
	Detail string
}

// Zustände eines Schritts.
const (
	StateRunning = "laeuft"
	StateOK      = "ok"
	StateFailed  = "fehler"
	StateUndone  = "zurueck"
)

// Schritte einer Änderung.
const (
	StepBefore   = "Deine Seiten vorher abfragen"
	StepWrite    = "Caddy-Dateien sichern und ändern"
	StepValidate = "Caddy prüft die Dateien"
	StepReload   = "Caddy neu laden"
	StepCheck    = "Neue Seite prüfen"
	StepCompare  = "Deine Seiten mit vorher vergleichen"
	StepUndo     = "Alles zurücklegen"
	StepRehearse = "Prüfung mit kaputter Datei proben"
)

// Applier führt Änderungen aus.
type Applier struct {
	Caddy *Caddy
	Probe Prober
	Event func(Event)
	// Retry: So oft und so lange fragt Apply eine Seite noch einmal, bevor
	// sie als abweichend gilt.
	Retries    int
	RetryDelay time.Duration
	Parallel   int
	// Journal: Datei, in die Apply vor dem ersten Schreiben die alten
	// Fassungen legt (leer: nur im Speicher). Apply löscht sie nie; das
	// macht der Aufrufer mit ClearJournal, wenn auch sein Teil fertig ist.
	Journal string
	Note    string // steht im Journal, etwa „freischalten“
}

func (a *Applier) event(step, state, detail string) {
	if a.Event != nil {
		a.Event(Event{Step: step, State: state, Detail: detail})
	}
}

// ErrUndone heißt: Die Änderung ging nicht, alles ist wie vorher.
type ErrUndone struct {
	Step string // Schritt, der schiefging
	Err  error
}

func (e *ErrUndone) Error() string {
	return fmt.Sprintf("%s: %v. Alles ist zurückgelegt, Caddy läuft wie vorher.", e.Step, e.Err)
}

func (e *ErrUndone) Unwrap() error { return e.Err }

// saved ist eine Datei vor der Änderung.
type saved struct {
	path    string
	existed bool
	data    []byte
	mode    fs.FileMode
	uid     int
	gid     int
}

// resolve folgt einem Symlink, damit die Änderung die echte Datei trifft.
func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// snapshot liest eine Datei vor der Änderung. Nur beim Caddyfile (follow)
// folgt es einem Symlink; bei hostdeck.caddy hieße das, als root dorthin zu
// schreiben, wohin ein anderer den Symlink gelegt hat.
func snapshot(p string, follow bool) (saved, error) {
	s := saved{path: p, mode: 0o644, uid: -1, gid: -1}
	if follow {
		s.path = resolve(p)
	}
	fi, err := os.Lstat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return s, fmt.Errorf("%s ist ein Symlink; HostDeck ändert ihn nicht", s.path)
	}
	if !fi.Mode().IsRegular() {
		return s, fmt.Errorf("%s ist keine normale Datei", s.path)
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return s, err
	}
	s.existed, s.data, s.mode = true, b, fi.Mode().Perm()
	s.uid, s.gid = owner(fi)
	return s, nil
}

// writeFile schreibt eine Datei auf einmal (neue Datei, dann umbenennen),
// mit Rechten und Besitzer wie angegeben.
func writeFile(p string, data []byte, mode fs.FileMode, uid, gid int) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".hostdeck-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if uid >= 0 && os.Geteuid() == 0 {
		if err := tmp.Chown(uid, gid); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// restore legt alle Dateien zurück und prüft, dass sie byte-gleich sind.
func restore(files []saved) error {
	var errs []string
	for _, s := range files {
		if s.existed {
			if err := writeFile(s.path, s.data, s.mode, s.uid, s.gid); err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if b, err := os.ReadFile(s.path); err != nil || !bytes.Equal(b, s.data) {
				errs = append(errs, s.path+" ist nach dem Zurücklegen nicht wie vorher")
			}
			continue
		}
		if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Apply führt die Änderung aus. Klappt etwas nicht, legt es alle Dateien
// zurück, lädt Caddy neu und liefert *ErrUndone. Andere Fehler heißen:
// Auch das Zurücklegen ging nicht, ein Mensch muss nachsehen.
func (a *Applier) Apply(ctx context.Context, ch Change) error {
	c := a.Caddy
	a.event(StepBefore, StateRunning, "")
	hosts, err := c.Hosts(ctx)
	if err != nil {
		a.event(StepBefore, StateFailed, err.Error())
		return &ErrUndone{Step: StepBefore, Err: err}
	}
	hosts = without(hosts, ch.Skip)
	before := a.probeAll(ctx, hosts)
	a.event(StepBefore, StateOK, countPages(len(hosts))+" abgefragt")

	a.event(StepWrite, StateRunning, "")
	var files []saved
	for _, f := range ch.Files {
		s, err := snapshot(f.Path, f.Path == c.Config)
		if err == nil && f.Was != nil && !bytes.Equal(s.data, f.Was) {
			err = fmt.Errorf("%s hat sich gerade geändert; bitte noch einmal prüfen und freischalten", s.path)
		}
		if err != nil {
			a.event(StepWrite, StateFailed, err.Error())
			return &ErrUndone{Step: StepWrite, Err: err}
		}
		files = append(files, s)
	}
	if a.Journal != "" {
		// Erst die alten Fassungen auf die Platte: Stürzt HostDeck mitten in der
		// Änderung ab, legt Recover sie beim nächsten Start zurück.
		if err := writeJournal(a.Journal, a.Note, files); err != nil {
			a.event(StepWrite, StateFailed, err.Error())
			return &ErrUndone{Step: StepWrite, Err: fmt.Errorf("Sicherung auf der Platte: %v", err)}
		}
	}
	undo := func(step string, cause error, reload bool) error {
		a.event(StepUndo, StateRunning, "")
		if err := restore(files); err != nil {
			a.event(StepUndo, StateFailed, err.Error())
			return fmt.Errorf("%s: %v. Zurücklegen ging nicht: %v. Bitte die Dateien prüfen: %s", step, cause, err, paths(files))
		}
		if reload {
			// Auch wenn der Auftrag abgebrochen wurde (Strg+C, Dienst hält
			// an): Caddy muss die zurückgelegten Dateien laden.
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			err := c.Reload(rctx)
			cancel()
			if err != nil {
				a.event(StepUndo, StateFailed, "Dateien zurückgelegt, aber "+err.Error())
				return fmt.Errorf("%s: %v. Die Dateien sind zurückgelegt, aber Caddy ließ sich danach nicht neu laden: %v", step, cause, err)
			}
		}
		done := "Dateien byte-gleich zurückgelegt"
		if reload {
			done += ", Caddy neu geladen"
		}
		a.event(StepUndo, StateUndone, done)
		return &ErrUndone{Step: step, Err: cause}
	}
	for i, f := range ch.Files {
		if f.Data == nil {
			if err := os.Remove(files[i].path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				a.event(StepWrite, StateFailed, err.Error())
				return undo(StepWrite, err, false)
			}
			continue
		}
		mode, uid, gid := files[i].mode, files[i].uid, files[i].gid
		if !files[i].existed {
			// Neue Datei: Rechte wie das Caddyfile.
			if ref, err := snapshot(c.Config, true); err == nil && ref.existed {
				mode, uid, gid = ref.mode, ref.uid, ref.gid
			}
		}
		if err := writeFile(files[i].path, f.Data, mode, uid, gid); err != nil {
			a.event(StepWrite, StateFailed, err.Error())
			return undo(StepWrite, err, false)
		}
	}
	a.event(StepWrite, StateOK, written(files, ch.Files)+"; die alten Fassungen hält HostDeck bereit")

	a.event(StepValidate, StateRunning, "")
	if err := c.Validate(ctx); err != nil {
		a.event(StepValidate, StateFailed, err.Error())
		return undo(StepValidate, err, false)
	}
	a.event(StepValidate, StateOK, "")

	a.event(StepReload, StateRunning, "")
	if err := c.Reload(ctx); err != nil {
		a.event(StepReload, StateFailed, err.Error())
		return undo(StepReload, err, true)
	}
	a.event(StepReload, StateOK, "")

	if ch.Check != nil {
		a.event(StepCheck, StateRunning, "")
		if err := ch.Check(ctx); err != nil {
			a.event(StepCheck, StateFailed, err.Error())
			return undo(StepCheck, err, true)
		}
		a.event(StepCheck, StateOK, "")
	}

	a.event(StepCompare, StateRunning, "")
	if bad := a.compare(ctx, hosts, before); len(bad) > 0 {
		err := errors.New("Abweichung bei " + strings.Join(bad, ", "))
		a.event(StepCompare, StateFailed, err.Error())
		return undo(StepCompare, err, true)
	}
	a.event(StepCompare, StateOK, countPages(len(hosts))+" wie vorher")
	return nil
}

// ApplyOffline ändert die Dateien, wenn Caddy nicht läuft: ohne validate
// und ohne Neuladen. Nur für Änderungen, die nichts kaputt machen können,
// etwa eine import-Zeile herausnehmen. Geht etwas schief, legt es alles
// zurück.
func ApplyOffline(config string, ch Change) error {
	var files []saved
	for _, f := range ch.Files {
		s, err := snapshot(f.Path, f.Path == config)
		if err != nil {
			return err
		}
		files = append(files, s)
	}
	for i, f := range ch.Files {
		var err error
		if f.Data == nil {
			if err = os.Remove(files[i].path); errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
		} else {
			err = writeFile(files[i].path, f.Data, files[i].mode, files[i].uid, files[i].gid)
		}
		if err != nil {
			if rerr := restore(files); rerr != nil {
				return fmt.Errorf("%v; Zurücklegen ging auch nicht: %v. Bitte prüfen: %s", err, rerr, paths(files))
			}
			return err
		}
	}
	return nil
}

// Rehearse probt vor jeder Änderung, ob caddy validate hier einen Fehler
// erkennt: Erst muss deine jetzige Konfiguration gültig sein, dann muss
// eine Kopie des Caddyfiles mit einem absichtlich kaputten Block (broken)
// abgelehnt werden. Die Kopie liegt neben dem Caddyfile, damit relative
// imports gleich bleiben, hat dieselben Rechte und wird gleich wieder
// gelöscht. Deine Dateien rührt die Probe nicht an.
func (a *Applier) Rehearse(ctx context.Context, broken []byte) error {
	c := a.Caddy
	a.event(StepRehearse, StateRunning, "")
	fail := func(err error) error {
		a.event(StepRehearse, StateFailed, err.Error())
		return err
	}
	cf, err := snapshot(c.Config, true)
	if err != nil {
		return fail(err)
	}
	if !cf.existed {
		return fail(fmt.Errorf("das Caddyfile %s fehlt", c.Config))
	}
	if err := c.Validate(ctx); err != nil {
		return fail(fmt.Errorf("caddy validate lehnt schon deine jetzige Konfiguration ab (%v); so ließe sich nicht erkennen, ob die Änderung passt", err))
	}
	probe := ProbePath(c.Config)
	_ = os.Remove(probe) // Rest einer abgebrochenen Probe
	data := append(append(append([]byte(nil), cf.data...), '\n'), broken...)
	if err := writeFile(probe, data, cf.mode, cf.uid, cf.gid); err != nil {
		return fail(err)
	}
	verr := c.ValidateFile(ctx, probe)
	if err := os.Remove(probe); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fail(fmt.Errorf("Probe-Datei %s löschen: %v", probe, err))
	}
	if verr == nil {
		return fail(errors.New("caddy validate hat die absichtlich kaputte Datei angenommen; so ließe sich ein Fehler nicht erkennen"))
	}
	a.event(StepRehearse, StateOK, "kaputte Probe erkannt; an deinen Dateien hat sich nichts geändert")
	return nil
}

// ProbePath ist die Kopie des Caddyfiles für die Probe.
func ProbePath(config string) string {
	p := resolve(config)
	return filepath.Join(filepath.Dir(p), ".hostdeck-probe-"+filepath.Base(p))
}

func countPages(n int) string {
	if n == 1 {
		return "1 Seite"
	}
	return fmt.Sprintf("%d Seiten", n)
}

func paths(files []saved) string {
	var p []string
	for _, f := range files {
		p = append(p, f.path)
	}
	return strings.Join(p, ", ")
}

// written sagt kurz, was mit jeder Datei geschah, etwa
// "hostdeck.caddy angelegt, Caddyfile geändert".
func written(files []saved, ch []File) string {
	var p []string
	for i, f := range files {
		what := "geändert"
		switch {
		case ch[i].Data == nil:
			what = "gelöscht"
		case !f.existed:
			what = "angelegt"
		}
		p = append(p, filepath.Base(f.path)+" "+what)
	}
	return strings.Join(p, ", ")
}

func without(hosts, skip []string) []string {
	var out []string
	for _, h := range hosts {
		drop := false
		for _, s := range skip {
			if strings.EqualFold(h, s) {
				drop = true
			}
		}
		if !drop {
			out = append(out, h)
		}
	}
	return out
}

func (a *Applier) probeAll(ctx context.Context, hosts []string) map[string]Answer {
	out := map[string]Answer{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	n := a.Parallel
	if n <= 0 {
		n = 4
	}
	sem := make(chan struct{}, n)
	for _, h := range hosts {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			ans := a.Probe(ctx, h)
			mu.Lock()
			out[h] = ans
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// compare fragt alle Seiten noch einmal ab. Eine Seite, die schlechter
// antwortet, bekommt ein paar weitere Versuche, damit ein kurzer Aussetzer
// nicht alles zurücknimmt.
func (a *Applier) compare(ctx context.Context, hosts []string, before map[string]Answer) []string {
	after := a.probeAll(ctx, hosts)
	var bad []string
	for _, h := range hosts {
		now := after[h]
		for i := 0; worse(before[h], now) && i < a.Retries; i++ {
			select {
			case <-ctx.Done():
				return append(bad, h+" (abgebrochen)")
			case <-time.After(a.RetryDelay):
			}
			now = a.Probe(ctx, h)
		}
		if worse(before[h], now) {
			bad = append(bad, fmt.Sprintf("%s (vorher %s, jetzt %s)", h, before[h], now))
		}
	}
	sort.Strings(bad)
	return bad
}

// LocalProber fragt Seiten über Caddy auf diesem Server ab (HTTPS an
// 127.0.0.1:443 mit dem Hostnamen), ohne Weiterleitungen zu folgen.
func LocalProber(port int, roots *tls.Config) Prober {
	return func(ctx context.Context, host string) Answer {
		resp, err := LocalGet(ctx, host, "/", port, roots)
		if err != nil {
			return Answer{Err: shortErr(err)}
		}
		resp.Body.Close()
		return Answer{Status: resp.StatusCode}
	}
}

// AddrProber fragt Seiten über Caddy unter der Adresse ip ab, etwa auf
// einem neuen Server, bevor DNS dorthin zeigt. Das Zertifikat wird wie
// im Browser geprüft.
func AddrProber(ip string, port int) Prober {
	return func(ctx context.Context, host string) Answer {
		resp, err := Get(ctx, ip, host, "/", port, nil)
		if err != nil {
			return Answer{Err: shortErr(err)}
		}
		resp.Body.Close()
		return Answer{Status: resp.StatusCode}
	}
}

// LocalGet holt https://host/path über Caddy auf diesem Server.
func LocalGet(ctx context.Context, host, path string, port int, base *tls.Config) (*http.Response, error) {
	return Get(ctx, "127.0.0.1", host, path, port, base)
}

// Get holt https://host/path über die Adresse ip, ohne DNS zu fragen.
func Get(ctx context.Context, ip, host, path string, port int, base *tls.Config) (*http.Response, error) {
	if port == 0 {
		port = 443
	}
	tc := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if base != nil {
		tc = base.Clone()
		tc.ServerName = host
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp", net.JoinHostPort(ip, fmt.Sprint(port)))
		},
		TLSClientConfig:     tc,
		TLSHandshakeTimeout: 5 * time.Second,
		DisableKeepAlives:   true,
	}
	cl := &http.Client{
		Transport:     tr,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "HostDeck (Prüfung nach Caddy-Änderung)")
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	// Höchstens 1 MiB lesen lassen.
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, 1<<20), resp.Body}
	return resp, nil
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i > 8 {
		s = s[i+2:]
	}
	if len(s) > 160 {
		s = s[:160] + " …"
	}
	return s
}
