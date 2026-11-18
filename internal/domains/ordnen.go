package domains

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
)

// Options sind die Vorschläge, die beim Umordnen mitlaufen.
type Options struct {
	HTTP3Off bool     `json:"http3_aus"`
	Off      []string `json:"abschalten"` // tote Domains, die nach sites/aus/ gehen
}

// FilePlan ist eine Datei, die sich ändert.
type FilePlan struct {
	Path  string   `json:"pfad"`
	What  string   `json:"was"` // neu, geaendert, aus, weg
	Hosts []string `json:"domains,omitempty"`
	Text  string   `json:"text,omitempty"`
}

// Choice ist ein Vorschlag, den man an- und abwählen kann.
type Choice struct {
	Key    string `json:"schluessel"` // http3 oder aus:<domain>
	Title  string `json:"titel"`
	Detail string `json:"detail"`
	Chosen bool   `json:"gewaehlt"`
}

// ReorgPlan zeigt, was das Umordnen ändert.
type ReorgPlan struct {
	Config  string     `json:"caddyfile"`
	Files   []FilePlan `json:"dateien"`
	Keep    []string   `json:"bleibt"`
	Choices []Choice   `json:"vorschlaege"`
	Options Options    `json:"auswahl"`
	Same    bool       `json:"gleichwertig"`
	Backup  string     `json:"sicherung"`
	Steps   []string   `json:"schritte"`
	Back    string     `json:"rueckweg"`
	Notes   []string   `json:"hinweise"`
	Problem string     `json:"problem,omitempty"`
	Stand   string     `json:"stand"`

	files []caddy.File
	skip  []string
	dirs  []string
	open  []string // vorhandene Ordner, die Caddy nicht lesen kann
	sums  map[string]string
	main  []byte
	cfg   *caddy.Caddy
	vary  []string // Zeitstempel; zählen nicht zum Stand
}

const probeDir = ".hostdeck-probe-" + SitesDir

// ReorgPlan rechnet das Umordnen durch, ohne etwa zu ändern. opts nil:
// alle Vorschläge gewählt.
func (s *Service) ReorgPlan(ctx context.Context, opts *Options) (ReorgPlan, error) {
	l, err := s.read(ctx)
	if err != nil {
		return ReorgPlan{}, err
	}
	p := ReorgPlan{Config: l.c.Config, Files: []FilePlan{}, Keep: []string{}, Choices: []Choice{}, Steps: []string{}, Notes: []string{}, cfg: l.c, main: l.main, sums: map[string]string{}}
	if l.other != "" {
		p.Problem = "HostDeck ordnet dieses Caddyfile nicht um: " + l.other + "."
		return p.finish(), nil
	}
	var inline []Site
	for _, st := range l.sites {
		switch st.Kind {
		case KindInline:
			inline = append(inline, st)
		case KindImported:
			what := st.File + " (" + strings.Join(st.Hosts, ", ")
			if st.Project != "" {
				what += "; gehört zu Projekt " + st.Project
			}
			if !contains(p.Keep, what+")") {
				p.Keep = append(p.Keep, what+")")
			}
		case KindLeitstand:
			p.Keep = append(p.Keep, st.File+" (Leitstand)")
		}
	}
	sitesDir := filepath.Join(l.dir, SitesDir)
	importing := false
	for _, sg := range l.segs {
		if sg.Kind == caddy.SegImport && sg.Args[1] == SitesGlob {
			importing = true
		}
	}
	existing, _ := filepath.Glob(filepath.Join(sitesDir, "*.caddy"))
	if !importing && len(existing) > 0 {
		p.Problem = "In " + sitesDir + " liegen schon Dateien, die Caddy heute nicht lädt (" + strings.Join(baseNames(existing), ", ") + "). Bitte erst prüfen und wegräumen; sonst würde Caddy sie beim Umordnen plötzlich laden."
		return p.finish(), nil
	}
	if len(inline) == 0 && (!l.http3 || !l.h3edit) {
		p.Problem = "Hier gibt es nichts umzuordnen: Jede Domain hat schon ihre eigene Datei."
		if l.http3 && !l.h3edit {
			p.Problem += " HTTP/3 lässt sich nicht von HostDeck abschalten, weil der globale Block eigene servers-Einträge hat."
		}
		return p.finish(), nil
	}

	// Tote Domains: Am Ziel lauscht nichts.
	dead := map[string]bool{}
	for _, st := range inline {
		if len(st.Hosts) == 0 || len(st.Upstreams) == 0 || s.Listening == nil {
			continue
		}
		alive := false
		for _, u := range st.Upstreams {
			if s.Listening(ctx, u) {
				alive = true
				break
			}
		}
		if !alive {
			dead[st.Hosts[0]] = true
		}
	}
	if opts == nil {
		// HTTP/3 bleibt ohne ausdrücklichen Wunsch an: Es hilft vor allem
		// unterwegs und in wackeligen Netzen; nur wo ein Anbieter UDP
		// drosselt, bremst es große Downloads. Der Leitstand selbst bietet
		// HTTP/3 ohnehin nicht an (Alt-Svc clear in hostdeck.caddy).
		opts = &Options{}
		for _, st := range inline {
			if len(st.Hosts) > 0 && dead[st.Hosts[0]] {
				opts.Off = append(opts.Off, st.Hosts[0])
			}
		}
	}
	for _, h := range opts.Off {
		if !dead[h] {
			return p, fmt.Errorf("%w: %s ist keine tote Domain im Caddyfile", ErrBadChoice, h)
		}
	}
	if opts.HTTP3Off && !(l.http3 && l.h3edit) {
		return p, fmt.Errorf("%w: HTTP/3 lässt sich hier nicht abschalten", ErrBadChoice)
	}
	p.Options = *opts
	if l.http3 && l.h3edit {
		p.Choices = append(p.Choices, Choice{Key: "http3", Title: "HTTP/3 für alle Seiten aus (nur bei Problemen)",
			Detail: "Lass es an, solange alles gut läuft: HTTP/3 lädt Seiten vor allem unterwegs und in wackeligen Netzen schneller. Nur wenn Besucher über langsame Downloads klagen, weil ihr Anbieter UDP drosselt, schalte es ab; Caddy spricht dann HTTP/1.1 und HTTP/2, genauso sicher. Der Leitstand bietet HTTP/3 ohnehin nicht an.",
			Chosen: opts.HTTP3Off})
	}
	if len(inline) == 0 && !opts.HTTP3Off {
		p.Problem = "Jede Domain hat schon ihre eigene Datei. Hak „HTTP/3 aus“ an, wenn du es abschalten willst."
		return p.finish(), nil
	}
	for _, st := range inline {
		if len(st.Hosts) > 0 && dead[st.Hosts[0]] {
			p.Choices = append(p.Choices, Choice{Key: "aus:" + st.Hosts[0], Title: st.Hosts[0] + " abschalten",
				Detail: "Am Ziel " + strings.Join(st.Upstreams, ", ") + " lauscht nichts; die Seite zeigt nur einen Fehler. Die Datei kommt nach sites/aus/ und lässt sich mit einem Klick wieder anschalten.",
				Chosen: contains(opts.Off, st.Hosts[0])})
		}
	}

	// Dateinamen je Domain.
	taken := map[string]bool{}
	for _, f := range existing {
		taken[filepath.Base(f)] = true
	}
	offExisting, _ := filepath.Glob(filepath.Join(sitesDir, OffDir, "*.caddy"))
	for _, f := range offExisting {
		taken[filepath.Base(f)] = true
	}
	type moved struct {
		st   Site
		name string
		off  bool
		data []byte
	}
	var mv []moved
	for i, st := range inline {
		name := fileName(st, i, taken)
		off := len(st.Hosts) > 0 && contains(opts.Off, st.Hosts[0])
		mv = append(mv, moved{st, name, off, siteFile(st, "aus dem Caddyfile übernommen")})
	}

	now := s.now()
	stamp, day := now.Format("20060102-150405"), now.Format("02.01.2006")
	p.vary = []string{stamp, day}
	p.Backup = filepath.Join(l.dir, BackupDir, stamp)
	note := []string{
		"# Von HostDeck geordnet am " + day + ": Jede Domain hat ihre eigene Datei in " + SitesDir + "/.",
		"# Abgeschaltete Domains liegen in " + SitesDir + "/" + OffDir + "/. Die alte Fassung dieser Datei: " + BackupDir + "/" + stamp + "/.",
	}
	build := func(sitesRel string, withOpts bool) ([]byte, error) {
		return newMain(l.segs, sitesRel, note, withOpts && opts.HTTP3Off)
	}
	final, err := build(SitesDir, true)
	if err != nil {
		p.Problem = err.Error()
		return p.finish(), nil
	}

	// Probe 1: Die reine Umordnung muss für Caddy dasselbe sein wie heute.
	if len(mv) > 0 {
		pure, err := build(probeDir, false)
		if err != nil {
			p.Problem = err.Error()
			return p.finish(), nil
		}
		var files [][2]string
		for _, f := range existing {
			b, _ := os.ReadFile(f)
			files = append(files, [2]string{filepath.Base(f), string(b)})
		}
		for _, m := range mv {
			files = append(files, [2]string{m.name, string(m.data)})
		}
		same, where, err := s.stage(ctx, l, pure, files, true)
		if err != nil {
			p.Problem = "Probe der neuen Ordnung ging nicht: " + err.Error()
			return p.finish(), nil
		}
		if !same {
			p.Problem = "Die neue Ordnung wäre für Caddy nicht dasselbe wie heute (Abweichung bei " + where + "). HostDeck ändert darum nichts."
			return p.finish(), nil
		}
	}
	p.Same = true
	// Probe 2: Mit den gewählten Vorschlägen muss Caddy die Dateien annehmen.
	probeMain, err := build(probeDir, true)
	if err != nil {
		p.Problem = err.Error()
		return p.finish(), nil
	}
	var files [][2]string
	for _, f := range existing {
		b, _ := os.ReadFile(f)
		files = append(files, [2]string{filepath.Base(f), string(b)})
	}
	for _, m := range mv {
		if !m.off {
			files = append(files, [2]string{m.name, string(m.data)})
		}
	}
	if _, _, err := s.stage(ctx, l, probeMain, files, false); err != nil {
		p.Problem = "Caddy würde die neue Ordnung nicht annehmen: " + err.Error()
		return p.finish(), nil
	}

	// Plan festhalten.
	p.Files = append(p.Files, FilePlan{Path: l.c.Config, What: "geaendert", Text: string(final)})
	p.files = append(p.files, caddy.File{Path: l.c.Config, Data: final, Was: l.main})
	if _, err := os.Stat(sitesDir); err != nil {
		p.dirs = append(p.dirs, sitesDir)
	}
	anyOff := false
	for _, m := range mv {
		path := filepath.Join(sitesDir, m.name)
		what := "neu"
		if m.off {
			path, what, anyOff = filepath.Join(sitesDir, OffDir, m.name), "aus", true
			p.skip = append(p.skip, m.st.Hosts...)
		}
		p.Files = append(p.Files, FilePlan{Path: path, What: what, Hosts: m.st.Hosts, Text: string(m.data)})
		p.files = append(p.files, caddy.File{Path: path, Data: m.data, Was: []byte{}})
		p.sums[path] = sum(m.data)
	}
	if _, err := os.Stat(filepath.Join(sitesDir, OffDir)); err != nil && anyOff {
		p.dirs = append(p.dirs, filepath.Join(sitesDir, OffDir))
	}
	p.Steps = append(p.Steps, "Sichern: Das heutige Caddyfile kommt als Kopie nach "+p.Backup+"/ und bleibt dort (auch im nächsten Backup).")
	if p.open = closedDirs(sitesDir); len(p.open) > 0 {
		p.Steps = append(p.Steps, openStep(p.open))
	}
	if len(mv) > 0 {
		p.Steps = append(p.Steps, fmt.Sprintf("Umordnen: %d %s aus dem Caddyfile in eigene Dateien in %s/; im Caddyfile bleiben die allgemeinen Einstellungen und die Import-Zeilen. HostDeck hat vorher geprüft: Für Caddy ist das genau dasselbe wie heute.", len(mv), plural(len(mv), "Domain", "Domains"), sitesDir))
	}
	if opts.HTTP3Off {
		p.Steps = append(p.Steps, "HTTP/3 aus: im globalen Block „servers { protocols h1 h2 }“.")
	}
	if len(opts.Off) > 0 {
		p.Steps = append(p.Steps, "Abschalten: "+strings.Join(opts.Off, ", ")+" kommen nach "+filepath.Join(sitesDir, OffDir)+"/.")
	}
	p.Steps = append(p.Steps, "Caddy prüft die Dateien und lädt neu. Danach ruft HostDeck jede Domain auf; antwortet eine schlechter als vorher, legt HostDeck alles zurück.")
	p.Back = "Geht etwas schief, legt HostDeck alle Dateien von selbst zurück. Später: Die alte Fassung liegt in " + p.Backup + "/; abgeschaltete Domains schaltest du auf der Seite Domains wieder an."
	if len(p.Keep) > 0 {
		p.Notes = append(p.Notes, "Eingebundene Dateien bleiben, wo sie sind, und HostDeck schreibt sie nicht um.")
	}
	return p.finish(), nil
}

func (p ReorgPlan) finish() ReorgPlan {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%v\x00%s\x00", p.Problem, p.Options, p.main)
	for _, f := range p.Files {
		text := f.Text
		for _, v := range p.vary {
			text = strings.ReplaceAll(text, v, "")
		}
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00", f.Path, f.What, text)
	}
	p.Stand = hex.EncodeToString(h.Sum(nil))[:16]
	return p
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func baseNames(ps []string) []string {
	var out []string
	for _, p := range ps {
		out = append(out, filepath.Base(p))
	}
	return out
}

var unsafeName = regexp.MustCompile(`[^a-z0-9._-]+`)

// fileName wählt den Dateinamen für eine Seite: die erste Domain.
func fileName(st Site, i int, taken map[string]bool) string {
	base := ""
	if len(st.Hosts) > 0 {
		base = strings.Replace(st.Hosts[0], "*", "wildcard", 1)
		base = strings.Trim(unsafeName.ReplaceAllString(base, "_"), "._-")
	}
	if base == "" {
		base = fmt.Sprintf("seite-%d", i+1)
	}
	name := base + ".caddy"
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s-%d.caddy", base, n)
	}
	taken[name] = true
	return name
}

// siteFile ist der Inhalt der Datei einer Seite: eine Zeile von HostDeck, dann
// der Block, wie er war.
func siteFile(st Site, why string) []byte {
	body := strings.TrimLeft(st.text, "\n")
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return []byte("# " + strings.Join(st.Hosts, ", ") + ": " + why + ".\n" + body)
}

// newMain baut das Caddyfile ohne eigene Seiten, mit einer Import-Zeile
// für sitesRel/*.caddy. Die Zeile steht nach allen Snippets und dort, wo
// die erste Seite stand.
func newMain(segs []caddy.Segment, sitesRel string, note []string, h3off bool) ([]byte, error) {
	firstSite, lastSnippet, existing := -1, -1, -1
	for i, sg := range segs {
		switch {
		case sg.Kind == caddy.SegSite && firstSite < 0:
			firstSite = i
		case sg.Kind == caddy.SegSnippet:
			lastSnippet = i
		case sg.Kind == caddy.SegImport && sg.Args[1] == SitesGlob:
			existing = i
		}
	}
	pos := firstSite
	if lastSnippet+1 > pos {
		pos = lastSnippet + 1
	}
	if firstSite < 0 {
		pos = -1
	}
	imp := "\n" + strings.Join(note, "\n") + "\nimport " + sitesRel + "/*.caddy\n"
	var b strings.Builder
	if h3off && (len(segs) == 0 || segs[0].Kind != caddy.SegGlobal) {
		b.WriteString("{\n\tservers {\n\t\tprotocols h1 h2\n\t}\n}\n\n")
	}
	for i, sg := range segs {
		if i == pos && existing < 0 {
			b.WriteString(imp)
		}
		switch sg.Kind {
		case caddy.SegSite:
			continue
		case caddy.SegGlobal:
			if h3off {
				t, _, ok := http3Off(sg.Text)
				if !ok {
					return nil, errors.New("HTTP/3 lässt sich im globalen Block nicht abschalten")
				}
				b.WriteString(t)
				continue
			}
		case caddy.SegImport:
			if i == existing {
				b.WriteString(strings.Replace(sg.Text, SitesGlob, sitesRel+"/*.caddy", 1))
				continue
			}
		}
		b.WriteString(sg.Text)
	}
	if existing < 0 && (pos < 0 || pos >= len(segs)) {
		b.WriteString(imp)
	}
	return []byte(b.String()), nil
}

// http3Off schaltet HTTP/3 im globalen Block ab. on: HTTP/3 ist heute an;
// ok: HostDeck kann es hier abschalten.
func http3Off(global string) (text string, on, ok bool) {
	lines := strings.Split(global, "\n")
	ds := caddy.Directives(global)
	srv := -1
	for i, d := range ds {
		if d.Depth == 1 && d.Words[0] == "servers" {
			if len(d.Words) != 1 || !d.Block {
				return global, true, false
			}
			srv = i
		}
	}
	if srv >= 0 {
		for _, d := range ds[srv+1:] {
			if d.Depth <= 1 {
				break
			}
			if d.Depth == 2 && d.Words[0] == "protocols" {
				var keep []string
				for _, w := range d.Words[1:] {
					if w != "h3" {
						keep = append(keep, w)
					}
				}
				if len(keep) == len(d.Words)-1 {
					return global, false, true // schon ohne h3
				}
				if len(keep) == 0 {
					keep = []string{"h1", "h2"}
				}
				lines[d.Line] = indentOf(lines[d.Line]) + "protocols " + strings.Join(keep, " ")
				return strings.Join(lines, "\n"), true, true
			}
		}
		at := ds[srv].Line
		ins := indentOf(lines[at]) + "\tprotocols h1 h2"
		lines = append(lines[:at+1], append([]string{ins}, lines[at+1:]...)...)
		return strings.Join(lines, "\n"), true, true
	}
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if t != "}" {
			return global, true, false
		}
		ins := []string{"\tservers {", "\t\tprotocols h1 h2", "\t}"}
		lines = append(lines[:i], append(ins, lines[i:]...)...)
		return strings.Join(lines, "\n"), true, true
	}
	return global, true, false
}

func indentOf(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

// stage legt eine Probe neben das Caddyfile (Probe-Caddyfile und
// Probe-Ordner für sites/), lässt Caddy sie prüfen und räumt sie wieder
// weg. compare: zusätzlich mit der heutigen Konfiguration vergleichen.
func (s *Service) stage(ctx context.Context, l *layout, main []byte, files [][2]string, compare bool) (same bool, where string, err error) {
	dir := filepath.Join(l.dir, probeDir)
	cfg := caddy.ProbePath(l.c.Config)
	cleanup := func() {
		os.RemoveAll(dir)
		os.Remove(cfg)
	}
	cleanup()
	defer cleanup()
	if err := os.Mkdir(dir, 0o700); err != nil {
		return false, "", err
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f[0]), []byte(f[1]), 0o600); err != nil {
			return false, "", err
		}
	}
	if err := os.WriteFile(cfg, main, 0o600); err != nil {
		return false, "", err
	}
	if !compare {
		return true, "", l.c.ValidateFile(ctx, cfg)
	}
	now, err := l.c.Adapt(ctx, l.c.Config)
	if err != nil {
		return false, "", fmt.Errorf("heutige Konfiguration: %v", err)
	}
	next, err := l.c.Adapt(ctx, cfg)
	if err != nil {
		return false, "", err
	}
	same, where, err = caddy.SameConfig(now, next)
	return same, where, err
}

// Reorg ordnet um, wenn der Plan noch derselbe ist wie in der Vorschau
// (stand). msg sagt, was geschah.
func (s *Service) Reorg(ctx context.Context, opts *Options, stand string, event func(caddy.Event)) (string, error) {
	p, err := s.ReorgPlan(ctx, opts)
	if err != nil {
		return "", err
	}
	if p.Problem != "" {
		return "", errors.New(p.Problem)
	}
	if p.Stand != stand {
		return "", ErrChanged
	}
	if err := backupCopy(p.Config, p.Backup); err != nil {
		return "", fmt.Errorf("Sicherung ging nicht: %v. Nichts geändert", err)
	}
	ok := false
	reopen := func() {}
	defer func() {
		if !ok {
			reopen()
			for i := len(p.dirs) - 1; i >= 0; i-- {
				os.Remove(p.dirs[i]) // nur leere Ordner
			}
		}
	}()
	for _, d := range p.dirs {
		if err := mkdirCaddy(d); err != nil {
			return "", fmt.Errorf("Ordner %s anlegen: %v. Nichts geändert", d, err)
		}
	}
	if reopen, err = openForCaddy(p.open); err != nil {
		reopen = func() {}
		return "", fmt.Errorf("Rechte für Caddy setzen: %v. Nichts geändert", err)
	}
	undo, err := s.noteSums(p.sums)
	if err != nil {
		return "", fmt.Errorf("Stand ließ sich nicht merken: %v. Nichts geändert", err)
	}
	if err := s.apply(ctx, p.cfg, caddy.Change{Files: p.files, Skip: p.skip}, "domains-ordnen", event); err != nil {
		undo()
		return "", err
	}
	ok = true
	s.changed()
	n := 0
	for _, f := range p.Files {
		if f.What == "neu" {
			n++
		}
	}
	msg := fmt.Sprintf("Caddy ist geordnet: %d %s mit eigener Datei in %s.", n, plural(n, "Domain", "Domains"), filepath.Join(filepath.Dir(p.Config), SitesDir))
	if p.Options.HTTP3Off {
		msg += " HTTP/3 ist aus."
	}
	if len(p.Options.Off) > 0 {
		msg += " Abgeschaltet: " + strings.Join(p.Options.Off, ", ") + "."
	}
	return msg + " Die alte Fassung liegt in " + p.Backup + ".", nil
}

// apply führt eine Änderung mit Journal aus. Nach einem sauberen
// Zurücklegen ist das Journal weg; bleibt etwas halb, bleibt es liegen,
// damit der nächste Start aufräumt.
func (s *Service) apply(ctx context.Context, c *caddy.Caddy, ch caddy.Change, note string, event func(caddy.Event)) error {
	a := &caddy.Applier{Caddy: c, Probe: s.Probe, Retries: s.Retries, RetryDelay: s.RetryDelay, Journal: s.Journal, Note: note,
		Event: func(e caddy.Event) {
			if event != nil {
				event(e)
			}
			if s.Event != nil {
				s.Event(e)
			}
		}}
	err := a.Apply(ctx, ch)
	var undone *caddy.ErrUndone
	if err == nil || errors.As(err, &undone) {
		if jerr := caddy.ClearJournal(s.Journal); jerr != nil && err == nil {
			err = jerr
		}
	}
	return err
}

// backupCopy legt das Caddyfile vor dem Umordnen zur Seite.
func backupCopy(config, dir string) error {
	b, err := os.ReadFile(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, filepath.Base(config)), b, 0o600)
}
