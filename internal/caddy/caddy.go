// Package caddy ändert die Konfiguration von Caddy so, dass nichts kaputt
// bleibt: Dateien sichern, ändern, mit caddy validate prüfen, neu laden und
// danach alle Seiten mit vorher vergleichen. Geht ein Schritt schief oder
// weicht etwas ab, legt es alle Dateien byte-gleich zurück und lädt neu.
//
// HostDeck nutzt das für den Eintrag des Leitstands und für die Routen des
// Servers.
package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Unit ist die systemd-Unit von Caddy, wie sie das Paket anlegt.
const Unit = "caddy.service"

// DefaultConfig ist das Caddyfile, wenn die Unit nichts anderes sagt.
const DefaultConfig = "/etc/caddy/Caddyfile"

// ErrNotFound: Auf dem Server läuft kein Caddy, den HostDeck ändern kann.
var ErrNotFound = errors.New("auf diesem Server läuft kein Caddy als systemd-Dienst")

// Runner führt einen Befehl aus und liefert seine Ausgabe (stdout und
// stderr zusammen).
type Runner func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)

// Exec ist der echte Runner.
func Exec(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// Caddy ist ein Caddy auf diesem Server.
type Caddy struct {
	Bin     string // Programm, etwa /usr/bin/caddy
	Config  string // das Caddyfile
	Service string // systemd-Unit; leer: neu laden über caddy reload
	User    string // Benutzer, unter dem Caddy läuft
	Group   string // dessen Gruppe; bekommt Zugriff auf den Socket von HostDeck
	Run     Runner
	TempDir string // für validate; leer: os.TempDir
}

// Find sucht Caddy: zuerst als systemd-Dienst, sonst nichts. Ein Caddy in
// einem Container lässt sich von hier aus nicht sicher ändern.
func Find(ctx context.Context, run Runner) (*Caddy, error) {
	if run == nil {
		run = Exec
	}
	out, err := run(ctx, nil, "systemctl", "show", Unit, "--property=LoadState,ActiveState,User,Group,ExecStart")
	if err != nil {
		return nil, ErrNotFound
	}
	p := parseShow(string(out))
	if p["LoadState"] != "loaded" || p["ActiveState"] != "active" {
		return nil, ErrNotFound
	}
	c := &Caddy{Service: Unit, User: p["User"], Group: p["Group"], Run: run}
	c.Bin, c.Config = parseExecStart(p["ExecStart"])
	if c.Bin == "" {
		if b, err := exec.LookPath("caddy"); err == nil {
			c.Bin = b
		} else {
			return nil, fmt.Errorf("Caddy läuft, aber das Programm caddy ist nicht zu finden")
		}
	}
	if c.Config == "" {
		c.Config = DefaultConfig
	}
	if strings.HasSuffix(c.Config, ".json") {
		return nil, fmt.Errorf("Caddy nutzt hier eine JSON-Konfiguration (%s); HostDeck ändert nur ein Caddyfile", c.Config)
	}
	if _, err := os.Stat(c.Config); err != nil {
		return nil, fmt.Errorf("das Caddyfile %s fehlt: %v", c.Config, err)
	}
	if c.User == "" {
		c.User = "root"
	}
	if c.Group == "" {
		c.Group = c.User
		if u, err := user.Lookup(c.User); err == nil {
			if g, err := user.LookupGroupId(u.Gid); err == nil {
				c.Group = g.Name
			}
		}
	}
	return c, nil
}

// parseShow liest die Ausgabe von systemctl show (Name=Wert je Zeile).
func parseShow(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			m[k] = v
		}
	}
	return m
}

// parseExecStart holt Programm und --config aus ExecStart, etwa
// „{ path=/usr/bin/caddy ; argv[]=/usr/bin/caddy run --environ --config
// /etc/caddy/Caddyfile ; … }“.
func parseExecStart(s string) (bin, config string) {
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "{"))
		if v, ok := strings.CutPrefix(part, "path="); ok && bin == "" {
			bin = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(part, "argv[]="); ok {
			f := strings.Fields(v)
			for i := 0; i < len(f); i++ {
				switch {
				case f[i] == "--config" && i+1 < len(f):
					config = f[i+1]
				case strings.HasPrefix(f[i], "--config="):
					config = strings.TrimPrefix(f[i], "--config=")
				}
			}
		}
	}
	return bin, config
}

// sandbox legt einen leeren Ordner an, damit caddy validate und caddy adapt
// nichts in den Ordnern von root oder Caddy anlegen.
func (c *Caddy) sandbox() ([]string, func(), error) {
	dir, err := os.MkdirTemp(c.TempDir, "hostdeck-caddy-")
	if err != nil {
		return nil, func() {}, err
	}
	env := []string{"HOME=" + dir, "XDG_DATA_HOME=" + filepath.Join(dir, "data"), "XDG_CONFIG_HOME=" + filepath.Join(dir, "config")}
	return env, func() { os.RemoveAll(dir) }, nil
}

// short kürzt die Ausgabe von caddy auf die letzte aussagekräftige Zeile.
func short(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		// caddy schreibt JSON-Logzeilen; die Meldung steht in "msg" oder "error".
		var j map[string]any
		if json.Unmarshal([]byte(l), &j) == nil {
			for _, k := range []string{"error", "msg"} {
				if s, ok := j[k].(string); ok && s != "" {
					l = s
					break
				}
			}
		}
		if len(l) > 400 {
			l = l[:400] + " …"
		}
		return l
	}
	return ""
}

// Validate prüft das Caddyfile mit caddy validate, ohne etwas zu laden.
func (c *Caddy) Validate(ctx context.Context) error {
	return c.ValidateFile(ctx, c.Config)
}

// ValidateFile prüft ein anderes Caddyfile, etwa die Kopie für die Probe.
func (c *Caddy) ValidateFile(ctx context.Context, config string) error {
	env, done, err := c.sandbox()
	if err != nil {
		return err
	}
	defer done()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := c.Run(ctx, env, c.Bin, "validate", "--config", config, "--adapter", "caddyfile")
	if err != nil {
		return fmt.Errorf("caddy validate: %s", orErr(short(out), err))
	}
	return nil
}

// Reload lädt Caddy neu. Schlägt das fehl, läuft Caddy mit der alten
// Konfiguration weiter.
func (c *Caddy) Reload(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var out []byte
	var err error
	if c.Service != "" {
		out, err = c.Run(ctx, nil, "systemctl", "reload", c.Service)
	} else {
		out, err = c.Run(ctx, nil, c.Bin, "reload", "--config", c.Config, "--adapter", "caddyfile")
	}
	if err != nil {
		return fmt.Errorf("Caddy neu laden: %s", orErr(short(out), err))
	}
	return nil
}

func orErr(s string, err error) string {
	if s != "" {
		return s
	}
	return err.Error()
}

// Hosts liefert alle Hostnamen, die das Caddyfile bedient (über caddy
// adapt, also mit allen Imports). Platzhalter wie *.example.de fehlen.
func (c *Caddy) Hosts(ctx context.Context) ([]string, error) {
	env, done, err := c.sandbox()
	if err != nil {
		return nil, err
	}
	defer done()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := c.Run(ctx, env, c.Bin, "adapt", "--config", c.Config, "--adapter", "caddyfile")
	if err != nil {
		return nil, fmt.Errorf("caddy adapt: %s", orErr(short(out), err))
	}
	return HostsFromJSON(out)
}

// HostsFromJSON liest die Hostnamen aus der Ausgabe von caddy adapt. Davor
// können Log-Zeilen stehen; zählt nur das JSON mit „apps“.
func HostsFromJSON(b []byte) ([]string, error) {
	type config struct {
		Apps *struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []route `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	var cfg config
	dec := json.NewDecoder(bytes.NewReader(b))
	for {
		var c config
		if err := dec.Decode(&c); err != nil {
			if cfg.Apps == nil {
				return nil, fmt.Errorf("keine Konfiguration in der Ausgabe von caddy adapt (%v)", err)
			}
			break
		}
		if c.Apps != nil {
			cfg = c
			break
		}
	}
	seen := map[string]bool{}
	var walk func([]route)
	walk = func(rs []route) {
		for _, r := range rs {
			for _, m := range r.Match {
				for _, h := range m.Host {
					h = strings.ToLower(h)
					if !strings.ContainsAny(h, "*{") {
						seen[h] = true
					}
				}
			}
			for _, h := range r.Handle {
				walk(h.Routes)
			}
		}
	}
	for _, s := range cfg.Apps.HTTP.Servers {
		walk(s.Routes)
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

type route struct {
	Match []struct {
		Host []string `json:"host"`
	} `json:"match"`
	Handle []struct {
		Routes []route `json:"routes"`
	} `json:"handle"`
}

// Adapt liefert die JSON-Konfiguration, die Caddy aus dem Caddyfile config
// macht (mit allen Imports). Davor stehende Log-Zeilen fallen weg.
func (c *Caddy) Adapt(ctx context.Context, config string) ([]byte, error) {
	env, done, err := c.sandbox()
	if err != nil {
		return nil, err
	}
	defer done()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := c.Run(ctx, env, c.Bin, "adapt", "--config", config, "--adapter", "caddyfile")
	if err != nil {
		return nil, fmt.Errorf("caddy adapt: %s", orErr(short(out), err))
	}
	return configJSON(out)
}

// configJSON sucht in der Ausgabe von caddy adapt das JSON-Objekt der
// Konfiguration.
func configJSON(b []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("keine Konfiguration in der Ausgabe von caddy adapt (%v)", err)
		}
		var probe struct {
			Apps json.RawMessage `json:"apps"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Apps != nil {
			return raw, nil
		}
	}
}

// SameConfig vergleicht zwei Ausgaben von caddy adapt. Gleich heißt: Caddy
// macht damit dasselbe. Die Reihenfolge der Routen eines Servers und der
// TLS-Regeln zählt nur dann nicht, wenn jede für eigene, feste Hosts gilt
// (keine Platzhalter, keine Überschneidung, keine ohne Host); sonst gewinnt
// die erste passende, und die Reihenfolge muss gleich bleiben. Server
// heißen nach ihren Adressen statt srv0, srv1. Bei
// Ungleichheit sagt where, welcher Teil abweicht.
func SameConfig(a, b []byte) (same bool, where string, err error) {
	na, err := normConfig(a)
	if err != nil {
		return false, "", err
	}
	nb, err := normConfig(b)
	if err != nil {
		return false, "", err
	}
	if bytes.Equal(canon(na), canon(nb)) {
		return true, "", nil
	}
	return false, firstDiff(na, nb, ""), nil
}

func canon(v any) []byte {
	b, _ := json.Marshal(v) // Schlüssel von Maps sortiert
	return b
}

func normConfig(raw []byte) (map[string]any, error) {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	v, err := renameLoggers(v)
	if err != nil {
		return nil, err
	}
	apps, _ := v["apps"].(map[string]any)
	if httpApp, ok := apps["http"].(map[string]any); ok {
		if servers, ok := httpApp["servers"].(map[string]any); ok {
			named := map[string]any{}
			for name, s := range servers {
				srv, _ := s.(map[string]any)
				key := name
				if l, ok := srv["listen"].([]any); ok {
					var ls []string
					for _, x := range l {
						ls = append(ls, fmt.Sprint(x))
					}
					sort.Strings(ls)
					key = strings.Join(ls, ",")
				}
				if rs, ok := srv["routes"].([]any); ok && orderFree(rs, routeHosts) {
					sortCanon(rs)
				}
				named[key] = srv
			}
			httpApp["servers"] = named
		}
	}
	if tlsApp, ok := apps["tls"].(map[string]any); ok {
		if auto, ok := tlsApp["automation"].(map[string]any); ok {
			if ps, ok := auto["policies"].([]any); ok {
				for _, p := range ps {
					if pm, ok := p.(map[string]any); ok {
						if subj, ok := pm["subjects"].([]any); ok {
							sortCanon(subj)
						}
					}
				}
				if orderFree(ps, policySubjects) {
					sortCanon(ps)
				}
			}
		}
	}
	return v, nil
}

// orderFree sagt, ob die Reihenfolge von xs keine Rolle spielt: Jeder
// Eintrag hat eigene, feste Hosts, und keine zwei teilen einen.
func orderFree(xs []any, hosts func(any) []string) bool {
	seen := map[string]bool{}
	for _, x := range xs {
		hs := hosts(x)
		if len(hs) == 0 {
			return false
		}
		for _, h := range hs {
			h = strings.ToLower(h)
			if strings.ContainsAny(h, "*{") || seen[h] {
				return false
			}
			seen[h] = true
		}
	}
	return true
}

// routeHosts sind die Hosts einer Route; nil, wenn sie auch ohne Host
// oder nach anderem (Pfad …) wählt.
func routeHosts(x any) []string {
	r, _ := x.(map[string]any)
	ms, _ := r["match"].([]any)
	if len(ms) == 0 {
		return nil
	}
	var out []string
	for _, m := range ms {
		mm, _ := m.(map[string]any)
		hs, _ := mm["host"].([]any)
		if len(hs) == 0 || len(mm) != 1 {
			return nil
		}
		for _, h := range hs {
			out = append(out, fmt.Sprint(h))
		}
	}
	return out
}

func policySubjects(x any) []string {
	p, _ := x.(map[string]any)
	ss, _ := p["subjects"].([]any)
	var out []string
	for _, s := range ss {
		out = append(out, fmt.Sprint(s))
	}
	return out
}

func sortCanon(xs []any) {
	sort.SliceStable(xs, func(i, j int) bool { return bytes.Compare(canon(xs[i]), canon(xs[j])) < 0 })
}

// firstDiff nennt den ersten Pfad, an dem sich a und b unterscheiden.
func firstDiff(a, b any, at string) string {
	ma, oka := a.(map[string]any)
	mb, okb := b.(map[string]any)
	if oka && okb {
		keys := map[string]bool{}
		for k := range ma {
			keys[k] = true
		}
		for k := range mb {
			keys[k] = true
		}
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			if !bytes.Equal(canon(ma[k]), canon(mb[k])) {
				return firstDiff(ma[k], mb[k], at+"/"+k)
			}
		}
		return at
	}
	sa, oka := a.([]any)
	sb, okb := b.([]any)
	if oka && okb && len(sa) == len(sb) {
		for i := range sa {
			if !bytes.Equal(canon(sa[i]), canon(sb[i])) {
				return firstDiff(sa[i], sb[i], fmt.Sprintf("%s/%d", at, i))
			}
		}
	}
	if at == "" {
		return "/"
	}
	return at
}

var (
	logName = regexp.MustCompile(`^log\d+$`)
	logRef  = regexp.MustCompile(`([".])(log\d+)"`)
)

// renameLoggers gibt den Loggern, die Caddy der Reihe nach log0, log1, …
// nennt, Namen nach ihrem Inhalt und ihren Hosts. Sonst hieße eine Seite
// nach dem Umordnen anders, obwohl sie dasselbe tut.
func renameLoggers(v map[string]any) (map[string]any, error) {
	logging, _ := v["logging"].(map[string]any)
	logs, _ := logging["logs"].(map[string]any)
	if len(logs) == 0 {
		return v, nil
	}
	hosts := map[string][]string{}
	apps, _ := v["apps"].(map[string]any)
	httpApp, _ := apps["http"].(map[string]any)
	servers, _ := httpApp["servers"].(map[string]any)
	for _, s := range servers {
		srv, _ := s.(map[string]any)
		sl, _ := srv["logs"].(map[string]any)
		ln, _ := sl["logger_names"].(map[string]any)
		for host, val := range ln {
			switch x := val.(type) {
			case string:
				hosts[x] = append(hosts[x], host)
			case []any:
				for _, n := range x {
					hosts[fmt.Sprint(n)] = append(hosts[fmt.Sprint(n)], host)
				}
				sortCanon(x)
			}
		}
	}
	type named struct{ name, key string }
	var ns []named
	for name, def := range logs {
		if !logName.MatchString(name) {
			continue
		}
		hs := hosts[name]
		sort.Strings(hs)
		d := strings.ReplaceAll(string(canon(def)), name+`"`, `X"`)
		ns = append(ns, named{name, d + "|" + strings.Join(hs, ",")})
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i].key < ns[j].key })
	m := map[string]string{}
	for i, n := range ns {
		m[n.name] = fmt.Sprintf("hostdeck_log_%d", i)
	}
	b := logRef.ReplaceAllStringFunc(string(canon(v)), func(s string) string {
		sub := logRef.FindStringSubmatch(s)
		if to, ok := m[sub[2]]; ok {
			return sub[1] + to + `"`
		}
		return s
	})
	var out map[string]any
	if err := json.Unmarshal([]byte(b), &out); err != nil {
		return nil, err
	}
	if lg, ok := out["logging"].(map[string]any); ok {
		if ls, ok := lg["logs"].(map[string]any); ok {
			for _, l := range ls {
				if lm, ok := l.(map[string]any); ok {
					for _, k := range []string{"include", "exclude"} {
						if xs, ok := lm[k].([]any); ok {
							sortCanon(xs)
						}
					}
				}
			}
		}
	}
	return out, nil
}
