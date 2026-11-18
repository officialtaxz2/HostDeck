package serverebene

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Runner führt ein Programm aus und liefert seine Ausgabe.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Target prüft und übernimmt die Punkte auf dem neuen Server.
type Target struct {
	Root string // Wurzel für Tests; leer: /
	Run  Runner
}

func (t *Target) path(p string) string {
	if t.Root == "" {
		return p
	}
	return filepath.Join(t.Root, p)
}

type account struct{ uid, gid int }

// Zustände einer Datei auf dem neuen Server.
const (
	stNew   = "neu"
	stSame  = "gleich"
	stOther = "anders"
	stAdd   = "dazu" // Zeilen oder Registries fehlen
)

type fileState struct {
	f     File
	dest  string // Pfad auf diesem Server
	state string
	add   int // fehlende Zeilen oder Registries
}

// Result zählt, was die Prüfung gefunden hat.
type Result struct {
	Same, Add, Warn int
}

// Do geht die Punkte durch und schreibt je Punkt eine Zeile. Ohne
// lv.Apply ändert es nichts.
func (t *Target) Do(ctx context.Context, lv Level, w io.Writer) Result {
	var res Result
	users := readNames(t.path("/etc/passwd"))
	groups := readNames(t.path("/etc/group"))
	say := func(mark, format string, args ...any) {
		fmt.Fprintf(w, "  %s %s\n", mark, fmt.Sprintf(format, args...))
		switch mark {
		case "!", "✗":
			res.Warn++
		case "✓":
			res.Same++
		default:
			res.Add++
		}
	}
	cronOK := t.hasCron()
	reloaded := false
	for _, it := range lv.Items {
		if ctx.Err() != nil {
			return res
		}
		if it.Manual != "" {
			say("!", "%s: %s", it.Title, it.Manual)
			continue
		}
		if it.TimeZone != "" {
			t.timeZone(ctx, it.TimeZone, lv.Apply, say)
			continue
		}
		var missing []string
		for _, u := range it.Users {
			if _, ok := users[u]; !ok {
				missing = append(missing, u)
			}
		}
		if len(missing) > 0 {
			say("!", "%s: Benutzer %s gibt es hier nicht; HostDeck übernimmt das nicht. Lege den Benutzer an und starte den Schritt neu, oder richte es von Hand ein.", it.Title, strings.Join(missing, ", "))
			continue
		}
		if it.Cron && !cronOK {
			if !lv.Apply {
				say("·", "%s: cron ist hier nicht installiert; HostDeck installiert es am Umzugstag mit", it.Title)
				continue
			}
			if out, err := t.Run(ctx, "apt-get", "install", "-y", "-q", "cron"); err != nil {
				say("✗", "%s: cron ließ sich nicht installieren: %s", it.Title, firstLine(out, err))
				continue
			}
			_, _ = t.Run(ctx, "systemctl", "enable", "--now", "cron")
			cronOK = true
		}
		var states []fileState
		var other []string
		adds, news := 0, 0
		for _, f := range it.Files {
			st := t.state(f)
			states = append(states, st)
			switch st.state {
			case stOther:
				other = append(other, st.dest)
			case stAdd:
				adds += st.add
			case stNew:
				news++
			}
		}
		needs := t.missingNeeds(it.Needs)
		switch {
		case news == 0 && adds == 0 && len(other) == 0 && lv.Apply && len(it.Units) > 0:
			// Dateien kamen schon mit (etwa mit einem Ordner); eingeschaltet
			// sind sie damit noch nicht.
			args := append([]string{"enable", "--now"}, it.Units...)
			if out, err := t.Run(ctx, "systemctl", args...); err != nil {
				say("✗", "%s: schon da, aber Einschalten ging nicht: %s", it.Title, firstLine(out, err))
			} else {
				say("✓", "%s: schon da, eingeschaltet", it.Title)
			}
		case news == 0 && adds == 0 && len(other) == 0:
			say("✓", "%s: schon so da", it.Title)
		case !lv.Apply:
			say("·", "%s: %s", it.Title, previewText(it, news, adds))
		default:
			changed, err := t.apply(states, users, groups)
			if err != nil {
				say("✗", "%s: %v", it.Title, err)
				continue
			}
			text := "übernommen"
			if changed && len(it.Units) > 0 {
				if !reloaded {
					_, _ = t.Run(ctx, "systemctl", "daemon-reload")
					reloaded = true
				}
			}
			if len(it.Units) > 0 {
				args := append([]string{"enable", "--now"}, it.Units...)
				if out, err := t.Run(ctx, "systemctl", args...); err != nil {
					say("✗", "%s: übernommen, aber Einschalten ging nicht: %s", it.Title, firstLine(out, err))
					continue
				}
				text = "übernommen und eingeschaltet"
			}
			if it.Sysctl && changed {
				for _, st := range states {
					if out, err := t.Run(ctx, "sysctl", "-p", st.dest); err != nil {
						say("!", "%s: übernommen; sie gelten erst nach einem Neustart (%s)", it.Title, firstLine(out, err))
						text = ""
					}
				}
			}
			if text != "" {
				say("✓", "%s: %s", it.Title, text)
			}
		}
		for _, p := range other {
			say("!", "%s: %s liegt hier schon anders; HostDeck lässt die Datei, wie sie ist. Vergleiche sie von Hand.", it.Title, p)
		}
		for _, p := range needs {
			say("!", "%s braucht %s; das gibt es hier nicht", it.Title, p)
		}
	}
	return res
}

func previewText(it Item, news, adds int) string {
	switch {
	case news > 0 && adds > 0:
		return fmt.Sprintf("käme am Umzugstag dazu (%s, dazu %s)", plural(news, "neue Datei", "neue Dateien"), addText(it, adds))
	case adds > 0:
		return "käme am Umzugstag dazu (" + addText(it, adds) + ")"
	case len(it.Units) > 0:
		return "käme am Umzugstag dazu und würde eingeschaltet"
	default:
		return "käme am Umzugstag dazu"
	}
}

func addText(it Item, n int) string {
	for _, f := range it.Files {
		if f.How == HowDocker {
			return plural(n, "Registry", "Registries")
		}
	}
	return plural(n, "Zeile", "Zeilen")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func firstLine(out []byte, err error) string {
	if l, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n"); l != "" {
		return l
	}
	return err.Error()
}

func (t *Target) hasCron() bool {
	for _, p := range []string{"/usr/sbin/cron", "/usr/sbin/crond", "/usr/bin/crontab"} {
		if fi, err := os.Stat(t.path(p)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

func (t *Target) missingNeeds(needs []string) []string {
	var out []string
	for _, p := range needs {
		if _, err := os.Stat(t.path(p)); err != nil {
			out = append(out, p)
		}
	}
	return out
}

func (t *Target) currentZone() string {
	if b, err := os.ReadFile(t.path("/etc/timezone")); err == nil {
		if z := strings.TrimSpace(string(b)); z != "" {
			return z
		}
	}
	if l, err := os.Readlink(t.path("/etc/localtime")); err == nil {
		if _, after, ok := strings.Cut(l, "zoneinfo/"); ok {
			return after
		}
	}
	return "UTC"
}

func sameZone(a, b string) bool {
	norm := func(z string) string {
		switch z {
		case "Etc/UTC", "UTC", "Etc/Universal", "Universal", "Zulu", "Etc/Zulu":
			return "UTC"
		}
		return z
	}
	return norm(a) == norm(b)
}

func (t *Target) timeZone(ctx context.Context, tz string, apply bool, say func(string, string, ...any)) {
	cur := t.currentZone()
	switch {
	case sameZone(cur, tz):
		say("✓", "Zeitzone %s, wie auf dem alten Server", tz)
	case !apply:
		say("·", "Zeitzone: hier %s, am Umzugstag stellt HostDeck %s ein wie auf dem alten Server", cur, tz)
	default:
		if out, err := t.Run(ctx, "timedatectl", "set-timezone", tz); err != nil {
			say("✗", "Zeitzone %s ließ sich nicht einstellen: %s", tz, firstLine(out, err))
			return
		}
		say("✓", "Zeitzone auf %s gestellt (vorher %s)", tz, cur)
	}
}

// dest ist der Pfad einer Datei auf diesem Server.
func (t *Target) dest(f File) string {
	if f.How == HowCrontab {
		user := filepath.Base(f.Path)
		if fi, err := os.Stat(t.path("/var/spool/cron/crontabs")); err == nil && fi.IsDir() {
			return "/var/spool/cron/crontabs/" + user
		}
		if !t.hasCron() {
			return "/var/spool/cron/crontabs/" + user // kommt mit cron
		}
		return "/var/spool/cron/" + user
	}
	return f.Path
}

func (t *Target) state(f File) fileState {
	st := fileState{f: f, dest: t.dest(f)}
	fi, err := os.Lstat(t.path(st.dest))
	if err != nil {
		st.state = stNew
		return st
	}
	if !fi.Mode().IsRegular() {
		st.state = stOther
		return st
	}
	b, err := os.ReadFile(t.path(st.dest))
	if err != nil {
		st.state = stOther
		return st
	}
	switch f.How {
	case HowLines:
		have := map[string]bool{}
		for _, h := range lineHashes(b) {
			have[h] = true
		}
		for _, h := range f.Lines {
			if !have[h] {
				st.add++
				have[h] = true
			}
		}
	case HowDocker:
		auths, ok := dockerAuths(b)
		if !ok {
			st.state = stOther
			return st
		}
		for _, n := range f.Names {
			if _, ok := auths[n]; !ok {
				st.add++
			}
		}
	default:
		if hashOf(b) != f.Hash {
			st.state = stOther
			return st
		}
	}
	if st.add > 0 {
		st.state = stAdd
	} else {
		st.state = stSame
	}
	return st
}

// apply schreibt die Dateien eines Punkts. Es sagt, ob sich etwas
// geändert hat.
func (t *Target) apply(states []fileState, users, groups map[string]account) (bool, error) {
	changed := false
	for _, st := range states {
		f := st.f
		if st.state != stNew && st.state != stAdd {
			continue
		}
		if f.Data == nil {
			return changed, fmt.Errorf("%s: Inhalt fehlt", st.dest)
		}
		if f.How == HowFile || f.How == HowCrontab {
			if hashOf(f.Data) != f.Hash {
				return changed, fmt.Errorf("%s: Inhalt passt nicht zur Prüfsumme", st.dest)
			}
		}
		uid, gid, err := ids(f, users, groups)
		if err != nil {
			return changed, fmt.Errorf("%s: %v", st.dest, err)
		}
		full := t.path(st.dest)
		if err := t.noLinks(st.dest); err != nil {
			return changed, fmt.Errorf("%s: %v", st.dest, err)
		}
		if err := t.ensureDir(filepath.Dir(full), f.DirMode, uid, gid); err != nil {
			return changed, fmt.Errorf("%s: %v", st.dest, err)
		}
		switch st.state {
		case stNew:
			if err := writeFile(full, f.Data, os.FileMode(f.Mode), uid, gid); err != nil {
				return changed, fmt.Errorf("%s: %v", st.dest, err)
			}
		case stAdd:
			if err := t.merge(full, f); err != nil {
				return changed, fmt.Errorf("%s: %v", st.dest, err)
			}
		}
		changed = true
	}
	return changed, nil
}

// merge ergänzt eine vorhandene Datei um fehlende Zeilen oder Registries.
// Besitzer und Rechte bleiben.
func (t *Target) merge(full string, f File) error {
	fi, err := os.Lstat(full)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("keine gewöhnliche Datei")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	var out []byte
	switch f.How {
	case HowLines:
		have := map[string]bool{}
		for _, l := range keyLines(b) {
			have[l] = true
		}
		out = append([]byte{}, b...)
		if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
		for _, l := range keyLines(f.Data) {
			if !have[l] {
				out = append(out, l...)
				out = append(out, '\n')
				have[l] = true
			}
		}
	case HowDocker:
		var cfg map[string]json.RawMessage
		if err := json.Unmarshal(b, &cfg); err != nil {
			return errors.New("config.json ist hier unlesbar")
		}
		auths, _ := dockerAuths(b)
		from, ok := dockerAuths(f.Data)
		if !ok {
			return errors.New("config.json vom alten Server ist unlesbar")
		}
		for k, v := range from {
			if _, ok := auths[k]; !ok {
				auths[k] = v
			}
		}
		a, _ := json.Marshal(auths)
		if cfg == nil {
			cfg = map[string]json.RawMessage{}
		}
		cfg["auths"] = a
		out, _ = json.MarshalIndent(cfg, "", "\t")
		out = append(out, '\n')
	default:
		return errors.New("lässt sich nicht ergänzen")
	}
	uid, gid, _ := fileIDs(fi)
	return writeFile(full, out, fi.Mode().Perm(), int(uid), int(gid))
}

// ids sind Besitzer und Gruppe einer Datei auf diesem Server.
func ids(f File, users, groups map[string]account) (int, int, error) {
	u, ok := users[f.Owner]
	if !ok {
		n, err := strconv.Atoi(f.Owner)
		if err != nil {
			return 0, 0, fmt.Errorf("Benutzer %s gibt es hier nicht", f.Owner)
		}
		u = account{uid: n, gid: n}
	}
	gid := u.gid
	if g, ok := groups[f.Group]; ok {
		gid = g.uid
	}
	return u.uid, gid, nil
}

// noLinks prüft in Home-Ordnern jeden Teil des Pfads: Ein Benutzer könnte
// dort einen Symlink legen, und HostDeck schreibt als root.
func (t *Target) noLinks(dest string) error {
	if !strings.HasPrefix(dest, "/root/") && !strings.HasPrefix(dest, "/home/") {
		return nil
	}
	p := "/"
	for _, part := range strings.Split(strings.Trim(dest, "/"), "/") {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(t.path(p))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s ist ein Symlink; HostDeck schreibt dort nicht", p)
		}
	}
	return nil
}

// ensureDir legt fehlende Ordner an. Ein Symlink unterwegs ist ein Fehler:
// HostDeck schreibt als root auch in Home-Ordner.
func (t *Target) ensureDir(dir string, mode uint32, uid, gid int) error {
	fi, err := os.Lstat(dir)
	switch {
	case err == nil && fi.IsDir():
		return nil
	case err == nil:
		return fmt.Errorf("%s ist kein Ordner", dir)
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	if err := t.ensureDir(filepath.Dir(dir), 0, 0, 0); err != nil {
		return err
	}
	m := os.FileMode(0o755)
	if mode != 0 {
		m = os.FileMode(mode)
	}
	if err := os.Mkdir(dir, m); err != nil {
		return err
	}
	if mode != 0 {
		return chown(dir, uid, gid)
	}
	return nil
}

func writeFile(full string, b []byte, mode os.FileMode, uid, gid int) error {
	tmp := full + ".hostdeck-neu"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := chown(tmp, uid, gid); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, full)
}

// readNames liest /etc/passwd oder /etc/group: Name → IDs.
func readNames(file string) map[string]account {
	out := map[string]account{}
	b, err := os.ReadFile(file)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 3 {
			continue
		}
		id, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		a := account{uid: id, gid: id}
		if len(f) >= 4 {
			if g, err := strconv.Atoi(f[3]); err == nil {
				a.gid = g
			}
		}
		out[f[0]] = a
	}
	return out
}
