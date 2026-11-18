// Package neueversion sieht auf Wunsch einmal am Tag bei GitHub nach, ob es
// eine neuere Version von HostDeck gibt, und merkt sich die Antwort. Es zeigt
// nur einen Hinweis; eingespielt wird nie von selbst, sondern wie immer
// über eine signierte Update-Datei im Leitstand.
//
// GitHub sieht bei der Anfrage nur die Adresse des Servers und die Version
// von HostDeck (im User-Agent), keine Daten über Projekte oder Server.
package neueversion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/outdir"
)

// Repo ist das öffentliche Repository mit den Releases („Besitzer/Name“).
// Es ist die einzige Stelle mit dem Namen; install.sh trägt denselben
// (ein Test vergleicht beide). Beim Bauen änderbar mit
// -ldflags "-X github.com/officialtaxz2/hostdeck/internal/neueversion.Repo=…".
var Repo = "officialtaxz2/HostDeck"

// Interval: so oft fragt der Dienst nach, solange der Hinweis an ist.
const Interval = 24 * time.Hour

// State liegt in versionshinweis.json im Datenordner.
type State struct {
	// On: nil, solange niemand gewählt hat (gilt als aus).
	On      *bool      `json:"an"`
	Checked *time.Time `json:"geprueft,omitempty"`
	Latest  string     `json:"neueste,omitempty"`
	Link    string     `json:"link,omitempty"`
	Error   string     `json:"fehler,omitempty"`
}

// Enabled sagt, ob der Hinweis eingeschaltet ist.
func (s State) Enabled() bool { return s.On != nil && *s.On }

// Service fragt nach und hält den Stand.
type Service struct {
	File    string // versionshinweis.json
	Version string // die laufende Version
	Repo    string // leer: Repo
	API     string // leer: https://api.github.com
	Client  *http.Client
	Now     func() time.Time

	mu sync.Mutex
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) repo() string {
	if s.Repo != "" {
		return s.Repo
	}
	return Repo
}

// Load liest den Stand; ohne Datei ist alles leer (aus).
func (s *Service) Load() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Service) load() State {
	var st State
	b, err := os.ReadFile(s.File)
	if err != nil || json.Unmarshal(b, &st) != nil {
		return State{}
	}
	return st
}

func (s *Service) save(st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.File), 0o700); err != nil {
		return err
	}
	return outdir.WritePrivate(s.File, append(b, '\n'))
}

// SetOn schaltet den Hinweis ein oder aus. Aus vergisst auch die letzte
// Antwort, damit kein alter Hinweis stehen bleibt.
func (s *Service) SetOn(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.load()
	st.On = &on
	if !on {
		st.Checked, st.Latest, st.Link, st.Error = nil, "", "", ""
	}
	return s.save(st)
}

// Due sagt, ob jetzt nachgefragt werden soll.
func (s *Service) Due() bool {
	st := s.Load()
	return st.Enabled() && (st.Checked == nil || s.now().Sub(*st.Checked) >= Interval)
}

type release struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Check fragt GitHub nach dem neuesten Release und speichert die Antwort,
// auch einen Fehler. Ist der Hinweis aus, fragt es nicht.
func (s *Service) Check(ctx context.Context) (State, error) {
	if !s.Load().Enabled() {
		return s.Load(), errors.New("der Hinweis auf neue Versionen ist aus")
	}
	tag, link, err := s.fetch(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.load()
	if !st.Enabled() {
		return st, errors.New("der Hinweis auf neue Versionen ist aus")
	}
	now := s.now().UTC()
	st.Checked = &now
	if err != nil {
		st.Error = err.Error()
	} else {
		st.Latest, st.Link, st.Error = tag, link, ""
	}
	if werr := s.save(st); werr != nil && err == nil {
		err = werr
	}
	return st, err
}

func (s *Service) fetch(ctx context.Context) (version, link string, err error) {
	api := s.API
	if api == "" {
		api = "https://api.github.com"
	}
	repo := s.repo()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "hostdeck/"+s.Version)
	c := s.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("GitHub nicht erreichbar: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", "", errors.New("auf GitHub gibt es noch kein Release")
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub antwortet mit %d", resp.StatusCode)
	}
	var r release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return "", "", fmt.Errorf("Antwort von GitHub nicht lesbar: %v", err)
	}
	v := strings.TrimPrefix(r.Tag, "v")
	if _, ok := parse(v); !ok || r.Draft || r.Prerelease {
		return "", "", fmt.Errorf("unerwartete Versionsangabe %q von GitHub", r.Tag)
	}
	// Den Link zeigt der Leitstand. Er entsteht aus Repo und geprüfter
	// Versionsnummer, nicht aus html_url, damit er nur auf die Releases
	// dieses Repos zeigen kann.
	return v, "https://github.com/" + repo + "/releases/tag/" + r.Tag, nil
}

// Run fragt im Hintergrund nach, sobald es fällig ist, bis ctx endet.
func (s *Service) Run(ctx context.Context, log io.Writer) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	first := time.After(time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-first:
		case <-t.C:
		}
		if s.Due() {
			if _, err := s.Check(ctx); err != nil && log != nil {
				fmt.Fprintf(log, "Nachsehen nach neuen Versionen: %v\n", err)
			}
		}
	}
}

// Newer sagt, ob latest eine neuere Version ist als current. Ist current
// keine Versionsnummer (etwa eine Testversion „e0-258afb6“ oder „dev“),
// sagt es nein; der Leitstand nennt dann nur die neueste Version.
func Newer(current, latest string) bool {
	a, ok1 := parse(strings.TrimPrefix(current, "v"))
	b, ok2 := parse(strings.TrimPrefix(latest, "v"))
	return ok1 && ok2 && compare(a, b) < 0
}

// IsVersion sagt, ob v eine Versionsnummer wie 1.2.3 oder 1.2.3-rc.1 ist.
func IsVersion(v string) bool { _, ok := parse(strings.TrimPrefix(v, "v")); return ok }

type semver struct {
	num [3]int
	pre []string
}

func parse(v string) (semver, bool) {
	var s semver
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '.' || c == '-' || c == '+') {
			return s, false
		}
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	core := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core = v[:i]
		if v[i+1:] == "" {
			return s, false
		}
		s.pre = strings.Split(v[i+1:], ".")
		for _, p := range s.pre {
			if p == "" {
				return s, false
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return s, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return s, false
		}
		s.num[i] = n
	}
	return s, true
}

func compare(a, b semver) int {
	for i := range a.num {
		if a.num[i] != b.num[i] {
			if a.num[i] < b.num[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, xe := strconv.Atoi(x)
		yn, ye := strconv.Atoi(y)
		switch {
		case xe == nil && ye == nil:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xe == nil:
			return -1
		case ye == nil:
			return 1
		case x != y:
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}
