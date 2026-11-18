package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/restic"
	"github.com/officialtaxz2/hostdeck/internal/runlog"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// Repo ist das Repository, in das ein Lauf sichert.
type Repo interface {
	Store
	Exists(ctx context.Context) (bool, error)
	Init(ctx context.Context) error
	Check(ctx context.Context, subset string) error
}

// Job ist ein ganzer Lauf von hostdeck backup auf dem Server: Vorprüfung,
// Passwort, Ausführung, Prüfung und Bericht.
type Job struct {
	Version   string // HostDeck-Version, für Bericht und Lauf-Datei
	ResultDir string // Ergebnisordner von hostdeck discover mit manifest.yaml
	StateDir  string // eigener Ordner von HostDeck, etwa /root/.hostdeck
	// Discover erfasst den Server frisch, ohne Größen.
	Discover func(ctx context.Context) (*inventory.Inventory, error)
	Docker   Docker
	Repo     Repo
	// PasswordFile ist die Datei, aus der Repo das Passwort liest.
	PasswordFile string
	Input        Input
	Out          io.Writer
	// Screen zeigt, was nur auf den Bildschirm darf und nicht ins
	// Protokoll, etwa ein neues Passwort. Standard: Out.
	Screen   io.Writer
	Self     []string
	Platform string

	Final    bool
	PlanOnly bool
	// Hello prüft vor dem Lauf, dass am anderen Ende der PC antwortet.
	Hello func(ctx context.Context) error
	// SendReport schickt den Bericht zusätzlich an den PC.
	SendReport func(ctx context.Context, run string, body []byte) error
	// Where beschreibt das Ziel für die Ausgabe, etwa „D:\hostdeck-backup auf
	// deinem PC“.
	Where string
	// Target kommt in den Verlauf: „server“ für ein Backup auf dem Server.
	Target string
	// Exclude bleibt in jedem gesicherten Ordner draußen.
	Exclude []string
	// PlanHint steht am Ende eines Probelaufs: wo das echte Backup startet.
	PlanHint string
	// Leitstand: HostDeck läuft als Dienst. Hinweise nennen dann den Leitstand
	// statt Befehle im Terminal.
	Leitstand bool
	// CheckSpace prüft vor dem Lauf, ob für etwa total Bytes Platz ist.
	CheckSpace func(total int64) error
	// UserStopped liefert, was du im Leitstand gestoppt hast (Name →
	// Zeitpunkt). Es kommt in lauf.json; nil: nichts.
	UserStopped func() map[string]time.Time

	Now func() time.Time
	// Für Tests.
	StopTimeout, StartWait, Settle, Poll time.Duration
}

// Exit-Codes von Job.Run.
const (
	ExitOK      = 0
	ExitFailed  = 1 // Lauf gescheitert oder ungültig
	ExitBlocked = 2 // Vorprüfung nicht bestanden, nichts verändert
)

func (j *Job) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

func (j *Job) printf(format string, args ...any) { fmt.Fprintf(j.Out, format, args...) }

// StopStatePath ist die Liste der gestoppten Container.
func StopStatePath(stateDir string) string { return filepath.Join(stateDir, "gestoppt.json") }

// Run führt den Lauf aus und gibt den Exit-Code zurück.
func (j *Job) Run(ctx context.Context) int {
	runID := j.now().Format("20060102-150405")
	if j.PlanOnly {
		j.printf("hostdeck backup --plan: Probelauf, es wird nichts gestoppt und nichts gesendet\n")
	} else if j.Final {
		j.printf("hostdeck backup --letzter-lauf: Lauf %s, danach bleiben die Dienste aus\n", runID)
	} else {
		j.printf("hostdeck backup: Lauf %s\n", runID)
	}

	j.printf("\nVorprüfung\n")
	m, inv, plan, ok := j.precheck(ctx)
	if plan != nil && j.PlanOnly {
		j.printf("\n")
		plan.Write(j.Out)
	}
	if !ok {
		j.printf("\nDer Lauf startet nicht; auf dem Server wurde nichts verändert.\n")
		return ExitBlocked
	}
	if j.PlanOnly {
		hint := j.PlanHint
		if hint == "" {
			hint = "Das echte Backup startest du am PC: Menüpunkt 2 oder hostdeck.exe backup."
		}
		j.printf("\nProbelauf beendet. %s\n", hint)
		return ExitOK
	}
	if j.Hello != nil {
		if err := j.Hello(ctx); err != nil {
			j.printf("  ✗ Verbindung zum PC: %v\n", err)
			return ExitBlocked
		}
		j.printf("  ✓ Verbindung zum PC steht\n")
	}
	if j.CheckSpace != nil {
		total, _ := plan.TotalSize()
		if err := j.CheckSpace(total); err != nil {
			j.printf("  ✗ %v\n", err)
			return ExitBlocked
		}
	}
	if err := j.repository(ctx); err != nil {
		j.printf("  ✗ %v\n", err)
		return ExitBlocked
	}
	total, _ := plan.TotalSize()
	j.printf("\nGesichert werden etwa %s in %d Einträgen", units.HumanSize(total), len(plan.Items()))
	if j.Where != "" {
		j.printf(" nach %s", j.Where)
	}
	j.printf(".\n")

	state, err := LoadStopState(StopStatePath(j.StateDir))
	if err != nil {
		j.printf("Fehler: Liste der gestoppten Container: %v\n", err)
		return ExitBlocked
	}
	r := &Runner{
		Plan: plan, RunID: runID, Docker: j.Docker, Store: j.Repo, Input: j.Input, State: state,
		Out: j.Out, Self: j.Self, Platform: j.Platform, Now: j.Now, Exclude: j.Exclude,
		StopTimeout: j.StopTimeout, StartWait: j.StartWait, Settle: j.Settle, Poll: j.Poll,
	}
	res := r.Run(ctx)

	rep := &Report{Plan: plan, Result: res, Version: j.Version, Where: j.Where}
	if res.OK() {
		rep.Missing = Missing(plan, res)
		if len(rep.Missing) == 0 {
			rep.Check = j.check(ctx)
		}
	}
	rep.Valid = res.OK() && len(rep.Missing) == 0 && rep.Check.OK
	if n, _ := rep.Unreadable(); n > 0 && res.Final {
		// Der letzte Lauf ist der Stand für den Umzug; dort darf nichts fehlen.
		rep.Valid = false
	}
	body := rep.Text()

	if res.OK() {
		meta, err := j.writeRun(runID, m, inv, plan, res, rep, body)
		if err != nil {
			j.printf("Fehler beim Ablegen der Lauf-Datei: %v\n", err)
			rep.Valid = false
		} else if _, err := j.Repo.Backup(context.WithoutCancel(ctx), restic.BackupArgs{
			Paths: []string{meta}, Tags: []string{"sbm-lauf", "sbm-run=" + runID},
		}); err != nil {
			j.printf("Fehler beim Sichern der Lauf-Datei: %v\n", err)
			rep.Valid = false
		}
		if !rep.Valid {
			body = rep.Text()
		}
	}
	if err := outdir.WritePrivate(filepath.Join(j.ResultDir, "backup-bericht.txt"), body); err != nil {
		j.printf("Fehler: backup-bericht.txt: %v\n", err)
	}
	if err := runlog.Append(j.ResultDir, runEntry(plan, res, rep, j.Target)); err != nil {
		j.printf("Hinweis: %s nicht aktualisiert: %v\n", runlog.File, err)
	}
	if j.SendReport != nil {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		if err := j.SendReport(sctx, runID, body); err != nil {
			j.printf("Hinweis: Bericht nicht an den PC geschickt: %v\n", err)
		}
		cancel()
	}
	j.printf("\n")
	rep.Summary(j.Out)
	j.printf("Bericht: %s\n", filepath.Join(j.ResultDir, "backup-bericht.txt"))
	if !rep.Valid {
		return ExitFailed
	}
	return ExitOK
}

const (
	fixSelectLeitstand = "Bitte im Leitstand „Jetzt neu erfassen“ drücken, dann unter Auswahl entscheiden und speichern und neu starten."
	fixSelectTerminal  = "Bitte sudo ./hostdeck discover und ./hostdeck serve, dann neu starten."
)

// pick wählt den Hinweis für den Leitstand oder das Terminal.
func (j *Job) pick(leitstand, terminal string) string {
	if j.Leitstand {
		return leitstand
	}
	return terminal
}

// precheck ist die Vorprüfung. Sie ändert nichts.
func (j *Job) precheck(ctx context.Context) (*manifest.Manifest, *inventory.Inventory, *Plan, bool) {
	ok := true
	fail := func(format string, args ...any) {
		ok = false
		j.printf("  ✗ "+format+"\n", args...)
	}
	pass := func(format string, args ...any) { j.printf("  ✓ "+format+"\n", args...) }

	mPath := filepath.Join(j.ResultDir, "manifest.yaml")
	f, err := os.Open(mPath)
	if err != nil {
		if j.Leitstand {
			fail("Deine Auswahl fehlt noch. Im Leitstand unter Auswahl anlegen und speichern.")
		} else {
			fail("Auswahl %s fehlt: zuerst sudo ./hostdeck discover und ./hostdeck serve", mPath)
		}
		return nil, nil, nil, false
	}
	m, err := manifest.Parse(f)
	f.Close()
	if err != nil {
		fail("%v", err)
		return nil, nil, nil, false
	}
	if open := OpenEntries(m); len(open) > 0 {
		fail("In deiner Auswahl ist noch %d offen, etwa %s. %s", len(open), title(open[0]), j.pick("Bitte im Leitstand unter Auswahl entscheiden und speichern.", "Bitte mit ./hostdeck serve entscheiden."))
	} else {
		pass("Auswahl vollständig, nichts offen")
	}

	j.printf("    Server wird frisch erfasst …\n")
	inv, err := j.Discover(ctx)
	if err != nil {
		fail("Erfassen des Servers: %v", err)
		return m, nil, nil, false
	}
	d := manifest.Compare(m, inv)
	gone := manifest.Missing(d.Gone)
	switch {
	case len(d.New) > 0:
		fail("Seit deiner Auswahl ist Neues dazugekommen: %s. %s", list(d.New, 5), j.pick(fixSelectLeitstand, fixSelectTerminal))
	case len(gone) > 0:
		fail("Seit deiner Auswahl ist etwas weggefallen: %s. %s", list(gone, 5), j.pick(fixSelectLeitstand, fixSelectTerminal))
	default:
		pass("Server passt zu deiner Auswahl")
	}

	if st, err := LoadStopState(StopStatePath(j.StateDir)); err != nil {
		fail("Liste der gestoppten Container: %v", err)
	} else if e := st.Entries(); len(e) > 0 {
		var names []string
		for _, x := range e {
			names = append(names, x.Name)
		}
		fail("Ein früherer Lauf hat %s gestoppt und nicht wieder gestartet. %s", list(names, 5), j.pick("Erst am Server sudo hostdeck dienste-starten, dann neu starten.", "Erst hostdeck dienste-starten, dann neu starten."))
	}

	if stored, err := LoadInventory(filepath.Join(j.ResultDir, "inventory.json")); err == nil {
		FillSizes(inv, stored)
		if !stored.CreatedAt.IsZero() {
			j.printf("    Größen vom letzten hostdeck discover (%s)\n", stored.CreatedAt.Local().Format("02.01.2006 15:04"))
		}
	}

	plan := Build(m, inv, Options{Final: j.Final, Excludes: []string{j.StateDir}, Now: j.now()})
	for _, p := range plan.Problems {
		fail("%s", p)
	}
	return m, inv, plan, ok
}

func list(keys []string, max int) string {
	var ts []string
	for i, k := range keys {
		if i == max {
			ts = append(ts, fmt.Sprintf("und %d weitere", len(keys)-max))
			break
		}
		ts = append(ts, title(k))
	}
	return strings.Join(ts, ", ")
}

// OpenEntries listet alle Einträge, die noch offen sind.
func OpenEntries(m *manifest.Manifest) []string {
	var out []string
	for _, name := range sortedKeys(m.Projects) {
		p := m.Projects[name]
		if p == nil {
			continue
		}
		if p.Decision == "open" {
			out = append(out, "project:"+name)
		}
		if p.Decision == "skip" {
			continue
		}
		for _, k := range sortedKeys(p.Data) {
			if d := p.Data[k]; d != nil && d.Strategy == "open" {
				out = append(out, k)
			}
		}
	}
	for _, k := range sortedKeys(m.Unassigned) {
		if d := m.Unassigned[k]; d != nil && d.Strategy == "open" {
			out = append(out, k)
		}
	}
	for _, k := range sortedKeys(m.Containers) {
		if c := m.Containers[k]; c != nil && c.Decision == "open" {
			out = append(out, k)
		}
	}
	if m.Host != nil && m.Host.Caddy != nil && m.Host.Caddy.Decision == "open" {
		out = append(out, "host:caddy")
	}
	return out
}

// repository sorgt für Passwort und Repository. Beim ersten Lauf erzeugt
// HostDeck das Passwort und zeigt es genau einmal.
func (j *Job) repository(ctx context.Context) error {
	RemoveDanglingPassword(j.PasswordFile)
	_, pwErr := os.Stat(j.PasswordFile)
	havePW := pwErr == nil
	if havePW {
		exists, err := j.Repo.Exists(ctx)
		if restic.WrongPassword(err) {
			return fmt.Errorf("das gespeicherte Backup-Passwort passt nicht zu diesem Backup-Ordner (%s)", j.PasswordFile)
		}
		if err != nil {
			return err
		}
		if !exists {
			if err := j.Repo.Init(ctx); err != nil {
				return err
			}
			j.printf("  ✓ Backup-Ordner neu angelegt, mit deinem bisherigen Backup-Passwort\n")
		} else {
			j.printf("  ✓ Backup-Ordner und Passwort passen\n")
		}
		if _, err := os.Stat(UnconfirmedPath(j.PasswordFile)); err == nil {
			// Beim ersten Lauf wurde das Notieren nicht bestätigt.
			b, err := os.ReadFile(j.PasswordFile)
			if err != nil {
				return err
			}
			j.printf("\n  Beim letzten Mal hast du nicht bestätigt, dass du das Backup-Passwort notiert hast.\n")
			return j.showPassword(ctx, strings.TrimSpace(string(b)), "Dein Backup-Passwort")
		}
		return nil
	}

	// Ohne Passwortdatei: Gibt es das Repository schon, muss das Passwort
	// von dir kommen, etwa nach einem neu aufgesetzten Server.
	if err := os.MkdirAll(filepath.Dir(j.PasswordFile), 0o700); err != nil {
		return err
	}
	if err := outdir.WritePrivate(j.PasswordFile, []byte("hostdeck-noch-kein-passwort\n")); err != nil {
		return err
	}
	exists, err := j.Repo.Exists(ctx)
	if err != nil && !restic.WrongPassword(err) {
		os.Remove(j.PasswordFile)
		return err
	}
	if restic.WrongPassword(err) {
		exists = true
	}
	if exists {
		for try := 0; try < 3; try++ {
			pw, err := j.Input.Ask(ctx, Question{Text: "Der Backup-Ordner hat schon ein Backup. Bitte gib dein Backup-Passwort ein:", Secret: true})
			if err != nil {
				os.Remove(j.PasswordFile)
				return err
			}
			if err := outdir.WritePrivate(j.PasswordFile, []byte(strings.TrimSpace(pw)+"\n")); err != nil {
				return err
			}
			_, err = j.Repo.Exists(ctx)
			if err == nil {
				j.printf("  ✓ Passwort passt; es liegt jetzt nur für root lesbar in %s\n", j.PasswordFile)
				return nil
			}
			if !restic.WrongPassword(err) {
				os.Remove(j.PasswordFile)
				return err
			}
			j.printf("    Das Passwort passt nicht.\n")
		}
		os.Remove(j.PasswordFile)
		return errors.New("dreimal ein falsches Backup-Passwort")
	}

	pw, err := NewPassword()
	if err != nil {
		return err
	}
	if err := outdir.WritePrivate(j.PasswordFile, []byte(pw+"\n")); err != nil {
		return err
	}
	if err := outdir.WritePrivate(UnconfirmedPath(j.PasswordFile), nil); err != nil {
		os.Remove(j.PasswordFile)
		return err
	}
	if err := j.Repo.Init(ctx); err != nil {
		os.Remove(j.PasswordFile)
		os.Remove(UnconfirmedPath(j.PasswordFile))
		return err
	}
	return j.showPassword(ctx, pw, "Neues Backup-Passwort")
}

// UnconfirmedPath markiert ein Passwort, dessen Notieren noch nicht
// bestätigt ist. Solange es die Datei gibt, zeigt jeder Lauf es wieder.
func UnconfirmedPath(pwFile string) string { return pwFile + "-unbestaetigt" }

// showPassword zeigt das Passwort nur auf dem Bildschirm, nicht im
// Protokoll, und wartet, bis es notiert ist.
func (j *Job) showPassword(ctx context.Context, pw, title string) error {
	screen := j.Screen
	if screen == nil {
		screen = j.Out
	}
	if screen != j.Out {
		j.printf("\n  (Hier stand das Backup-Passwort; ins Protokoll kommt es nicht.)\n")
	}
	fmt.Fprintf(screen, "\n  %s:\n\n      %s\n\n", title, pw)
	fmt.Fprintf(screen, "  Ohne dieses Passwort lässt sich das Backup nie wieder öffnen, auch von niemand anderem.\n")
	fmt.Fprintf(screen, "  Speichere es in deinem Passwortmanager und drucke es aus. Auf dem Server liegt es\n")
	fmt.Fprintf(screen, "  nur für root lesbar in %s; nach einem Neuaufsetzen ist es dort weg.\n\n", j.PasswordFile)
	for {
		ans, err := j.Input.Ask(ctx, Question{Text: "Hast du das Passwort sicher notiert? Tippe ja:"})
		if err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(ans), "ja") {
			os.Remove(UnconfirmedPath(j.PasswordFile))
			return nil
		}
	}
}

// NewPassword erzeugt ein Passwort aus 6 Gruppen zu 5 Zeichen ohne leicht
// verwechselbare Zeichen (etwa 147 Bit).
func NewPassword() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	var groups []string
	for g := 0; g < 6; g++ {
		var b strings.Builder
		for i := 0; i < 5; i++ {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			if err != nil {
				return "", err
			}
			b.WriteByte(alphabet[n.Int64()])
		}
		groups = append(groups, b.String())
	}
	return strings.Join(groups, "-"), nil
}

// checkParts: Jeder Lauf liest einen anderen Teil der Daten zurück; nach so
// vielen Läufen war alles einmal dran.
const checkParts = 20

func (j *Job) check(ctx context.Context) CheckResult {
	counter := filepath.Join(j.StateDir, "pruefung-teil")
	n := 1
	if b, err := os.ReadFile(counter); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && v >= 1 && v <= checkParts {
			n = v%checkParts + 1
		}
	}
	subset := fmt.Sprintf("%d/%d", n, checkParts)
	j.printf("\nPrüfung des Backups (Struktur und Teil %s der Daten)\n", subset)
	begin := j.now()
	err := j.Repo.Check(ctx, subset)
	res := CheckResult{Subset: subset, Duration: j.now().Sub(begin), OK: err == nil}
	if err != nil {
		res.Err = err.Error()
		j.printf("  ✗ %v\n", err)
		return res
	}
	_ = outdir.WritePrivate(counter, []byte(strconv.Itoa(n)+"\n"))
	j.printf("  ✓ bestanden\n")
	return res
}

// Missing listet die Schritte des Plans ohne fertigen Snapshot.
func Missing(p *Plan, res *Result) []string {
	done := map[string]bool{}
	for _, s := range res.Steps {
		if s.Phase == PhaseFinal && s.Snapshot != "" && s.Err == "" {
			done[s.Item.Key+"|"+s.Item.Strategy] = true
		}
	}
	var out []string
	for _, it := range p.Items() {
		if !done[it.Key+"|"+it.Strategy] {
			out = append(out, it.Key)
		}
	}
	sort.Strings(out)
	return out
}

// RunFile ist lauf.json: alles, was hostdeck restore über einen Lauf wissen muss.
type RunFile struct {
	Run           string         `json:"run"`
	Valid         bool           `json:"valid"`
	Final         bool           `json:"final"`
	Hostname      string         `json:"hostname"`
	Created       time.Time      `json:"created"`
	Version       string         `json:"sbm"` // Kennung im Datenformat, bleibt für ältere Dateien
	Restic        string         `json:"restic"`
	Snapshots     []RunSnapshot  `json:"snapshots"`
	Groups        []Group        `json:"groups"`
	Rebuild       []RebuildImage `json:"rebuild,omitempty"`
	Skipped       []Skip         `json:"skipped,omitempty"`
	GroupsResults []GroupResult  `json:"group_results,omitempty"`
	// UserStopped sind die Container, die du im Leitstand gestoppt hast,
	// mit Zeitpunkt. Das Zurückspielen nimmt sie mit, damit der neue
	// Server sie ebenfalls als „von dir gestoppt“ kennt und nicht warnt.
	UserStopped map[string]time.Time `json:"von_dir_gestoppt,omitempty"`
}

// RunSnapshot ordnet einem Eintrag seinen Snapshot zu.
type RunSnapshot struct {
	Key      string   `json:"key"`
	Group    string   `json:"group,omitempty"`
	Strategy string   `json:"strategy"`
	Snapshot string   `json:"snapshot"`
	Paths    []string `json:"paths,omitempty"`
	Filename string   `json:"filename,omitempty"`
	Engine   string   `json:"engine,omitempty"`
	Refs     []string `json:"refs,omitempty"`
	ImageID  string   `json:"image_id,omitempty"`
	Archive  bool     `json:"archive,omitempty"`
}

// writeRun legt lauf.json, Manifest, Inventar und Bericht in einen eigenen
// Ordner, der als letzter Snapshot des Laufs gesichert wird.
func (j *Job) writeRun(runID string, m *manifest.Manifest, inv *inventory.Inventory, p *Plan, res *Result, rep *Report, body []byte) (string, error) {
	dir := filepath.Join(j.StateDir, "laeufe", runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	rf := RunFile{Run: runID, Valid: rep.Valid, Final: p.Final, Hostname: p.Hostname, Created: res.Started.UTC(),
		Version: j.Version, Restic: restic.Version, Groups: p.Groups, Rebuild: p.Rebuild, Skipped: p.Skipped,
		GroupsResults: res.Groups}
	if j.UserStopped != nil {
		rf.UserStopped = j.UserStopped()
	}
	for _, s := range res.Steps {
		if s.Phase != PhaseFinal {
			continue
		}
		rf.Snapshots = append(rf.Snapshots, RunSnapshot{Key: s.Item.Key, Group: s.Item.Group, Strategy: s.Item.Strategy,
			Snapshot: s.Snapshot, Paths: s.Item.Paths, Filename: s.Item.Filename, Engine: s.Item.Engine,
			Refs: s.Item.Refs, ImageID: s.Item.ImageID, Archive: s.Item.Archive})
	}
	rb, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return "", err
	}
	mb, err := m.Marshal()
	if err != nil {
		return "", err
	}
	ib, err := inv.Marshal()
	if err != nil {
		return "", err
	}
	for name, b := range map[string][]byte{
		"lauf.json": append(rb, '\n'), "manifest.yaml": mb, "inventory.json": ib, "backup-bericht.txt": body,
	} {
		if err := outdir.WritePrivate(filepath.Join(dir, name), b); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// RemoveDanglingPassword löscht eine Platzhalter-Passwortdatei, die ein
// abgebrochener erster Lauf hinterlassen hat.
func RemoveDanglingPassword(path string) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return
	}
	if bytes.Equal(bytes.TrimSpace(b), []byte("hostdeck-noch-kein-passwort")) {
		os.Remove(path)
	}
}

// runEntry fasst einen Lauf für die Übersicht von hostdeck serve zusammen.
func runEntry(p *Plan, res *Result, rep *Report, target string) runlog.Entry {
	total, _ := p.TotalSize()
	added, _ := rep.added()
	e := runlog.Entry{
		Run: res.RunID, Started: res.Started, Finished: res.Finished,
		Valid: rep.Valid, Final: res.Final, Aborted: res.Aborted, Verdict: rep.verdict(),
		Size: total, Added: added, Items: len(p.Items()), Target: target,
	}
	e.Projects = len(p.Groups) // Projekte und Container ohne Projekt
	for _, g := range res.Groups {
		if s := int64(g.Downtime / time.Second); s > e.Downtime {
			e.Downtime = s
		}
	}
	return e
}
