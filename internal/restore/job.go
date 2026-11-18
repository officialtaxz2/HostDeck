package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/restic"
)

// Store ist das Backup, aus dem restore liest. Es ändert daran nichts.
type Store interface {
	CheckPassword(ctx context.Context) error
	Snapshots(ctx context.Context, tags ...string) ([]restic.Snapshot, error)
	Dump(ctx context.Context, snapshot, file string, w io.Writer) error
	Restore(ctx context.Context, snapshot, sub, target string) error
}

// Exit-Codes wie bei backup.
const (
	ExitOK      = 0
	ExitFailed  = 1
	ExitBlocked = 2
)

// Job ist ein Lauf von hostdeck restore.
type Job struct {
	Version      string
	StateDir     string
	Repo         Store
	PasswordFile string
	Docker       Docker
	Input        backup.Input
	Out          io.Writer
	PlanOnly     bool
	Hostname     string // dieser Server
	Now          func() time.Time
	ReadyWait    time.Duration // so lange auf eine Datenbank warten, Standard 3 min
	Settle       time.Duration // vor der Kontrolle nach dem Start, Standard 15 s
	// Password ist das Backup-Passwort, wenn es mitgebracht wurde (Probe-
	// Umzug aus dem Leitstand). Dann fragt der Lauf nicht danach.
	Password string
	// InstallDocker installiert Docker, wenn es fehlt und der Benutzer
	// zustimmt. nil: nur den Befehl nennen.
	InstallDocker func(ctx context.Context) error
	// UserStoppedFile ist die Liste der im Leitstand gestoppten Container
	// auf diesem Server (/var/lib/hostdeck/gestoppt.json). Was du auf dem alten
	// Server gestoppt hattest, kommt dazu. Leer: nichts schreiben.
	UserStoppedFile string
	// Host misst diesen Server vor dem Zurückspielen (Platz, Ports, Uhr).
	// nil: keine Prüfung.
	Host HostProbe
	// Caddy richtet Caddy mit Konfiguration und Zertifikaten aus dem
	// Backup ein. nil: Die Caddy-Konfiguration bleibt im Backup.
	Caddy CaddyHost
	// RunID ist der Lauf, der zurückkommt (Probe-Umzug: der neueste
	// gültige, den der Leitstand nennt). Leer: Der Benutzer wählt.
	RunID string

	prev *Record // wiederherstellung.json vom letzten Lauf, falls da
	from string  // Herkunft der Dumps dieses Laufs: Quelle/Lauf
}

func (j *Job) printf(format string, args ...any) { fmt.Fprintf(j.Out, format, args...) }

func (j *Job) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

// RecordPath ist die Datei, in der restore festhält, was es zurückgespielt
// hat; hostdeck verify vergleicht damit.
func RecordPath(stateDir string) string { return filepath.Join(stateDir, "wiederherstellung.json") }

// Record ist wiederherstellung.json.
type Record struct {
	Run      string        `json:"run"`
	Source   string        `json:"source"`
	Restored time.Time     `json:"restored"`
	Version  string        `json:"sbm"` // Kennung im Datenformat, bleibt für ältere Dateien
	Groups   []GroupRecord `json:"groups"`
	// Was ohne Projekt zurückkam: Ordner, Volumes, Images. Damit lässt
	// sich eine Probe-Kopie wieder genau entfernen.
	Loose   []string `json:"loose,omitempty"`
	Volumes []string `json:"volumes,omitempty"`
	Images  []string `json:"images,omitempty"`
	// ImageIDs: ID im Backup → IDs, unter denen das Image hier liegt. Damit
	// lädt ein späterer Lauf ein gleiches Image nicht noch einmal.
	ImageIDs map[string][]string `json:"image_ids,omitempty"`
	// Caddy: was HostDeck an Caddy gemacht hat; nil wenn Caddy nicht mitkam.
	Caddy *CaddyRecord `json:"caddy,omitempty"`
	// Halted: Die Probe-Kopie wurde aus dem Leitstand angehalten.
	Halted *time.Time `json:"angehalten,omitempty"`
	// Adopted: Der Umzug ist hier abgeschlossen. Ab dann ist
	// das der echte Server, keine Probe-Kopie: Anhalten, Entfernen und
	// Auffrischen verweigern.
	Adopted *time.Time `json:"uebernommen,omitempty"`
}

// AdoptedText ist der Grund, warum hier nichts mehr als Probe-Kopie gilt.
func (r *Record) AdoptedText() string {
	return "Der Umzug ist auf diesem Server am " + r.Adopted.Local().Format("02.01.2006 15:04") + " abgeschlossen: Das ist jetzt der echte Server, keine Probe-Kopie"
}

// MarkAdopted vermerkt in wiederherstellung.json, dass der Umzug hier
// abgeschlossen ist. Ohne die Datei gibt es nichts zu vermerken.
func MarkAdopted(stateDir string, now time.Time) error {
	rec, err := LoadRecord(stateDir)
	if err != nil {
		if _, serr := os.Stat(RecordPath(stateDir)); errors.Is(serr, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	t := now.UTC()
	rec.Adopted = &t
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return outdir.WritePrivate(RecordPath(stateDir), append(b, '\n'))
}

// GroupRecord hält fest, was zu einem Projekt gehört.
type GroupRecord struct {
	Name     string     `json:"name"`
	Compose  bool       `json:"compose"`
	Services []string   `json:"services,omitempty"`
	Ports    []int      `json:"ports,omitempty"`
	DBs      []DBRecord `json:"dbs,omitempty"`
	// Errors bleiben bestehen, bis neu zurückgespielt wird; NotRunning
	// prüft hostdeck verify jedes Mal neu.
	Errors     []string `json:"errors,omitempty"`
	NotRunning []string `json:"not_running,omitempty"`
	// Dir, Paths und Volumes: was HostDeck für dieses Projekt angelegt hat.
	// KeptOff: von dir gestoppt, kam in gestoppt.json.
	Dir     string   `json:"dir,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	Volumes []string `json:"volumes,omitempty"`
	KeptOff []string `json:"von_dir_gestoppt,omitempty"`
}

// DBRecord sind die Zeilenzahlen einer Datenbank laut Dump.
type DBRecord struct {
	Title    string   `json:"title"`
	Service  string   `json:"service"`
	Engine   string   `json:"engine"`
	Counts   Counts   `json:"counts,omitempty"`
	Diff     []string `json:"diff,omitempty"`
	Replayed bool     `json:"replayed"`
	Raw      bool     `json:"raw,omitempty"` // Dateien statt Dump
	// From sagt, aus welchem Lauf (Quelle/Lauf) die Daten stammen, die HostDeck
	// in diese Datenbank eingespielt hat.
	From string `json:"stand,omitempty"`
	// Failed heißt: Die Daten dieser Datenbank kamen nicht sicher zurück.
	Failed bool     `json:"failed,omitempty"`
	Note   string   `json:"note,omitempty"`
	Errors []string `json:"errors,omitempty"` // Fehler beim Einspielen
}

// Run spielt zurück und gibt einen Exit-Code zurück.
func (j *Job) Run(ctx context.Context) int {
	j.printf("hostdeck restore: zurückspielen auf %s\n\nVorprüfung\n", j.Hostname)
	engine, compose, err := j.Docker.Version(ctx)
	if err != nil && j.InstallDocker != nil && !j.PlanOnly {
		j.printf("  ! Docker fehlt hier noch (%v)\n\n", err)
		ans, aerr := j.Input.Ask(ctx, backup.Question{
			Text: "Docker fehlt hier. Jetzt mit dem offiziellen Skript von get.docker.com installieren? Tippe ja:",
			// Die Frage erscheint auch im Leitstand des alten Servers: dort hieße
			// „diesem Server“ der falsche (Labor S4).
			Note:    "Auf " + j.Hostname + ", dem Server, auf den HostDeck zurückspielt, fehlt Docker. HostDeck kann es jetzt mit dem offiziellen Skript von docker.com installieren; das dauert ein bis drei Minuten.",
			Choices: []backup.Choice{{Label: "Docker installieren", Answer: "ja"}, {Label: "Nicht installieren", Answer: "nein"}},
		})
		if aerr != nil || !strings.EqualFold(strings.TrimSpace(ans), "ja") {
			j.printf("Nicht gestartet: ohne Docker lässt sich nichts zurückspielen.\n")
			return ExitBlocked
		}
		j.printf("Docker installieren …\n")
		if ierr := j.InstallDocker(ctx); ierr != nil {
			j.printf("  ✗ Docker installieren: %v\n", ierr)
			return ExitBlocked
		}
		if ctx.Err() != nil {
			j.printf("Abgebrochen nach der Docker-Installation; sonst wurde nichts verändert.\n")
			return ExitBlocked
		}
		engine, compose, err = j.Docker.Version(ctx)
	}
	switch {
	case err != nil && j.InstallDocker != nil && j.PlanOnly:
		// Ein frischer Server hat meist noch kein Docker. Der Probelauf
		// prüft trotzdem alles andere; Docker kommt beim echten Lauf.
		j.printf("  ! Docker fehlt hier noch (%v). Beim Probe-Umzug bietet HostDeck an, es zu installieren.\n", err)
	case err != nil:
		j.printf("  ✗ Docker: %v\n", err)
		if j.InstallDocker != nil {
			j.printf("    Beim Probe-Umzug (nicht beim Probelauf) bietet HostDeck an, Docker zu installieren.\n")
		} else {
			j.printf("    Docker installieren: curl -fsSL https://get.docker.com | sh\n")
		}
		return ExitBlocked
	default:
		j.printf("  ✓ Docker %s mit Compose %s\n", engine, compose)
	}
	if err := j.password(ctx); err != nil {
		j.printf("  ✗ %v\n", err)
		return ExitBlocked
	}
	j.printf("  ✓ Passwort passt zum Backup\n")

	snap, err := j.chooseRun(ctx)
	if err != nil {
		j.printf("  ✗ %v\n", err)
		return ExitBlocked
	}
	rf, inv, err := j.loadRun(ctx, snap)
	if err != nil {
		j.printf("  ✗ Lauf lesen: %v\n", err)
		return ExitBlocked
	}
	p := Build(rf, inv)
	j.printf("\n")
	p.Write(j.Out)
	if len(p.Problems) > 0 {
		return ExitBlocked
	}
	j.prev, _ = LoadRecord(j.StateDir)
	if j.prev != nil && j.prev.Adopted != nil {
		j.printf("\n  ✗ %s. HostDeck spielt hier nichts darüber. Soll trotzdem zurückgespielt werden, lösche vorher %s.\n", j.prev.AdoptedText(), RecordPath(j.StateDir))
		return ExitBlocked
	}
	var notes []Note
	if j.Host != nil {
		notes = CheckHost(ctx, p, j.Host, j.prev)
	}
	if n, ok := j.caddyNote(ctx, p); ok {
		notes = append(notes, n)
	}
	if len(notes) > 0 {
		j.printf("\nDieser Server\n")
		bad := false
		for _, n := range notes {
			mark := map[string]string{NoteOK: "✓", NoteWarn: "!", NoteErr: "✗"}[n.Level]
			j.printf("  %s %s\n", mark, n.Text)
			bad = bad || n.Level == NoteErr
		}
		if bad {
			j.printf("\nNicht gestartet: Dieser Server passt so nicht (siehe ✗). Verändert wurde nichts.\n")
			return ExitBlocked
		}
	}

	if j.prev != nil {
		j.printf("\nAuffrischen\n  Hier liegt schon, was HostDeck am %s zurückgespielt hat (%s, Lauf %s). HostDeck spielt darüber und überträgt nur, was sich geändert hat: Gleiche Dateien und Images bleiben, Datenbanken, die HostDeck hier angelegt hat, ersetzt es aus dem frischen Dump.\n",
			j.prev.Restored.Local().Format("02.01.2006 15:04"), j.prev.Source, j.prev.Run)
	}
	conflicts := j.conflicts(ctx, p)
	if len(conflicts) > 0 {
		j.printf("\nAuf diesem Server liegt schon etwas:\n")
		for i, c := range conflicts {
			if i == 12 {
				j.printf("  und %d weitere\n", len(conflicts)-i)
				break
			}
			j.printf("  %s\n", c)
		}
		if rf.Hostname == j.Hostname {
			j.printf("\nAchtung: Das ist derselbe Rechnername wie beim Backup (%s). Ist das wirklich der neue Server?\n", rf.Hostname)
		}
	}
	if j.PlanOnly {
		j.printf("\nProbelauf beendet, nichts wurde verändert.\n")
		return ExitOK
	}
	want := "ja"
	q := backup.Question{
		Text:    "Jetzt zurückspielen? Tippe ja:",
		Note:    "Jetzt auf den neuen Server (Rechnername " + j.Hostname + ") zurückspielen? Der Server, von dem das Backup kommt, bleibt unverändert.",
		Choices: []backup.Choice{{Label: "Jetzt zurückspielen", Answer: "ja"}, {Label: "Nicht starten", Answer: "nein"}},
	}
	if len(conflicts) > 0 {
		want = "überschreiben"
		q = backup.Question{
			Text:    "Dateien mit gleichem Namen werden überschrieben, gelöscht wird nichts; was dort läuft, stoppt HostDeck vorher. Trotzdem weiter? Tippe überschreiben:",
			Note:    "Auf dem neuen Server (Rechnername " + j.Hostname + ") liegt schon etwas (siehe oben). Dateien mit gleichem Namen werden überschrieben, gelöscht wird nichts; was dort läuft, stoppt HostDeck vorher. Trotzdem weiter?",
			Choices: []backup.Choice{{Label: "Überschreiben", Answer: "überschreiben"}, {Label: "Nicht starten", Answer: "nein"}},
		}
	}
	// Fragen laufen als eine Zeile zum PC; der Abstand kommt deshalb vorher.
	j.printf("\n")
	ans, err := j.Input.Ask(ctx, q)
	if err != nil || !strings.EqualFold(strings.TrimSpace(ans), want) {
		j.printf("Nicht gestartet.\n")
		return ExitBlocked
	}

	rec := j.execute(ctx, p)
	if err := j.saveRecord(rec); err != nil {
		j.printf("Hinweis: %s nicht gespeichert: %v\n", RecordPath(j.StateDir), err)
	}
	if err := j.keepStopped(p); err != nil {
		j.printf("Hinweis: Liste der von dir gestoppten Container nicht gespeichert: %v\n", err)
	}
	return j.summary(rec)
}

// password sorgt für eine Passwortdatei, die zum Backup passt.
func (j *Job) password(ctx context.Context) error {
	if _, err := os.Stat(j.PasswordFile); err == nil {
		err := j.Repo.CheckPassword(ctx)
		if err == nil {
			return nil
		}
		if !restic.WrongPassword(err) {
			return err
		}
		j.printf("  Das gespeicherte Passwort passt nicht zu diesem Backup.\n")
	}
	if err := os.MkdirAll(filepath.Dir(j.PasswordFile), 0o700); err != nil {
		return err
	}
	if j.Password != "" {
		return j.givenPassword(ctx)
	}
	for try := 0; try < 3; try++ {
		pw, err := j.Input.Ask(ctx, backup.Question{Text: "  Backup-Passwort (vom Zettel oder aus dem Passwortmanager):", Secret: true})
		if err != nil {
			return err
		}
		pw = strings.TrimSpace(pw)
		if pw == "" {
			continue
		}
		if err := outdir.WritePrivate(j.PasswordFile, []byte(pw+"\n")); err != nil {
			return err
		}
		err = j.Repo.CheckPassword(ctx)
		if err == nil {
			return nil
		}
		os.Remove(j.PasswordFile)
		if !restic.WrongPassword(err) {
			return err
		}
		j.printf("  Das Passwort passt nicht.\n")
	}
	return errors.New("ohne das richtige Passwort lässt sich das Backup nicht öffnen")
}

// givenPassword legt das mitgebrachte Passwort in die Passwortdatei. Eine
// andere Datei, die schon dort lag, bleibt als .vorher daneben; passt das
// mitgebrachte Passwort nicht, kommt sie an ihren Platz zurück.
func (j *Job) givenPassword(ctx context.Context) error {
	keep := ""
	if _, err := os.Stat(j.PasswordFile); err == nil {
		keep = j.PasswordFile + ".vorher-" + j.now().Format("20060102-150405")
		if err := os.Rename(j.PasswordFile, keep); err != nil {
			return err
		}
	}
	undo := func() {
		os.Remove(j.PasswordFile)
		if keep != "" {
			if err := os.Rename(keep, j.PasswordFile); err != nil {
				j.printf("  Die bisherige Passwortdatei liegt noch in %s.\n", keep)
			}
		}
	}
	if err := outdir.WritePrivate(j.PasswordFile, []byte(strings.TrimSpace(j.Password)+"\n")); err != nil {
		undo()
		return err
	}
	err := j.Repo.CheckPassword(ctx)
	if err == nil {
		if keep != "" {
			j.printf("  Die bisherige Passwortdatei liegt jetzt in %s.\n", keep)
		}
		return nil
	}
	undo()
	if restic.WrongPassword(err) {
		return errors.New("das mitgebrachte Backup-Passwort passt nicht zum Backup")
	}
	return err
}

// chooseRun zeigt die letzten Läufe und lässt einen wählen. Ungültige
// Läufe sind gekennzeichnet; Enter nimmt den neuesten gültigen. Steht der
// Lauf schon fest (RunID), nimmt sie genau diesen.
func (j *Job) chooseRun(ctx context.Context) (restic.Snapshot, error) {
	snaps, err := j.Repo.Snapshots(ctx, "sbm-lauf")
	if err != nil {
		return restic.Snapshot{}, err
	}
	if len(snaps) == 0 {
		return restic.Snapshot{}, errors.New("in diesem Backup gibt es noch keinen fertigen Lauf")
	}
	sort.Slice(snaps, func(a, b int) bool { return snaps[a].Time.After(snaps[b].Time) })
	if j.RunID != "" {
		for _, s := range snaps {
			if runOf(s) == j.RunID {
				j.printf("\nLauf %s vom %s (der neueste gültige)\n", j.RunID, s.Time.Local().Format("02.01.2006 15:04"))
				return s, nil
			}
		}
		return restic.Snapshot{}, fmt.Errorf("der Lauf %s fehlt im Backup", j.RunID)
	}
	if len(snaps) > 5 {
		snaps = snaps[:5]
	}
	labels := make([]string, len(snaps))
	def := -1
	for i, s := range snaps {
		labels[i] = s.Time.Local().Format("02.01.2006 15:04")
		if len(snaps) == 1 {
			break
		}
		var rb bytes.Buffer
		var rf backup.RunFile
		switch {
		case len(s.Paths) != 1 || j.Repo.Dump(ctx, s.ID, path.Join(s.Paths[0], "lauf.json"), &rb) != nil || json.Unmarshal(rb.Bytes(), &rf) != nil:
			labels[i] += " (nicht lesbar)"
		case !rf.Valid:
			labels[i] += " (ungültig)"
		case def < 0:
			def = i
		}
	}
	if def < 0 {
		def = 0
	}
	j.printf("\nLäufe im Backup, der neueste zuerst\n")
	for i, s := range snaps {
		j.printf("  %d  %s  von %s\n", i+1, labels[i], s.Host)
	}
	if len(snaps) == 1 {
		return snaps[0], nil
	}
	q := backup.Question{
		Text: fmt.Sprintf("Welchen Lauf zurückspielen? Enter für %d:", def+1),
		Note: "Welchen Lauf zurückspielen? Der oberste ist der neueste; ungültige Läufe sind gekennzeichnet.",
	}
	for i := range snaps {
		q.Choices = append(q.Choices, backup.Choice{Label: labels[i], Answer: fmt.Sprint(i + 1)})
	}
	for {
		ans, err := j.Input.Ask(ctx, q)
		if err != nil {
			return restic.Snapshot{}, err
		}
		ans = strings.TrimSpace(ans)
		if ans == "" {
			return snaps[def], nil
		}
		var n int
		if _, err := fmt.Sscan(ans, &n); err == nil && n >= 1 && n <= len(snaps) {
			return snaps[n-1], nil
		}
		j.printf("Bitte eine Zahl von 1 bis %d.\n", len(snaps))
	}
}

// runOf ist die Kennung des Laufs zu einem Lauf-Snapshot.
func runOf(s restic.Snapshot) string {
	for _, t := range s.Tags {
		if id, ok := strings.CutPrefix(t, "sbm-run="); ok {
			return id
		}
	}
	if len(s.Paths) == 1 {
		return path.Base(s.Paths[0])
	}
	return ""
}

func (j *Job) loadRun(ctx context.Context, s restic.Snapshot) (*backup.RunFile, *inventory.Inventory, error) {
	if len(s.Paths) != 1 {
		return nil, nil, fmt.Errorf("unerwarteter Lauf-Snapshot %s", s.Short)
	}
	var rb, ib bytes.Buffer
	if err := j.Repo.Dump(ctx, s.ID, path.Join(s.Paths[0], "lauf.json"), &rb); err != nil {
		return nil, nil, err
	}
	if err := j.Repo.Dump(ctx, s.ID, path.Join(s.Paths[0], "inventory.json"), &ib); err != nil {
		return nil, nil, err
	}
	var rf backup.RunFile
	if err := json.Unmarshal(rb.Bytes(), &rf); err != nil {
		return nil, nil, fmt.Errorf("lauf.json: %w", err)
	}
	var inv inventory.Inventory
	if err := json.Unmarshal(ib.Bytes(), &inv); err != nil {
		return nil, nil, fmt.Errorf("inventory.json: %w", err)
	}
	return &rf, &inv, nil
}

// conflicts sucht, was auf diesem Server schon da ist.
func (j *Job) conflicts(ctx context.Context, p *Plan) []string {
	var out []string
	o := j.owned()
	for _, t := range p.Targets() {
		if o.path(t) {
			continue
		}
		fi, err := os.Lstat(t)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			if empty, _ := dirEmpty(t); empty {
				continue
			}
			out = append(out, t+" (Ordner mit Inhalt)")
			continue
		}
		out = append(out, t)
	}
	out = append(out, j.caddyConflict(ctx, p)...)
	// In eine Datenbank, die schon Daten hat, spielt restore nichts ein.
	dbs := map[string]bool{}
	for _, v := range p.DumpVolumes() {
		dbs[v] = true
		if o.volume(v) {
			continue
		}
		dir, ok, err := j.Docker.VolumeMountpoint(ctx, v)
		if err != nil || !ok {
			continue
		}
		if empty, _ := dirEmpty(dir); !empty {
			out = append(out, "Volume "+v+" hat schon eine Datenbank; HostDeck spielt dort nichts ein")
		}
	}
	for _, v := range p.VolumeNames() {
		if dbs[v] || o.volume(v) {
			continue
		}
		dir, ok, err := j.Docker.VolumeMountpoint(ctx, v)
		if err != nil || !ok {
			continue
		}
		if empty, _ := dirEmpty(dir); !empty {
			out = append(out, "Volume "+v+" mit Inhalt")
		}
	}
	for _, g := range p.Groups {
		if !g.Compose || o != nil && o.projects[g.Name] {
			continue
		}
		if cs, err := j.Docker.ProjectContainers(ctx, g.Name); err == nil && len(cs) > 0 {
			running := 0
			for _, c := range cs {
				if c.Running {
					running++
				}
			}
			msg := fmt.Sprintf("Projekt %s hat hier schon %d Container", g.Name, len(cs))
			if running > 0 {
				msg += fmt.Sprintf(", %d laufen; HostDeck stoppt sie vor dem Zurückspielen", running)
			}
			out = append(out, msg)
		}
	}
	return out
}

func dirEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// execute spielt den Plan zurück. Fehler in einem Projekt halten die
// anderen nicht auf.
func (j *Job) execute(ctx context.Context, p *Plan) *Record {
	rec := &Record{Run: p.Run, Source: p.Source, Restored: j.now().UTC(), Version: j.Version, ImageIDs: map[string][]string{}}
	j.from = p.Source + "/" + p.Run
	for _, f := range p.Loose {
		rec.Loose = append(rec.Loose, f.Paths...)
	}
	for _, v := range p.Volumes {
		if v.Name != "" {
			rec.Volumes = append(rec.Volumes, v.Name)
		}
	}
	for _, im := range p.Images {
		names := append(append([]string{}, im.Refs...), im.Tags...)
		if len(names) == 0 {
			names = []string{im.ID}
		}
		rec.Images = append(rec.Images, names...)
	}
	var general []string
	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		general = append(general, msg)
		j.printf("    ✗ %s\n", msg)
	}

	if len(p.Networks)+len(p.Images) > 0 {
		j.printf("\nNetze und Images\n")
	}
	for _, n := range p.Networks {
		if j.Docker.NetworkExists(ctx, n.Name) {
			continue
		}
		j.printf("  Netz %s\n", n.Name)
		if err := j.Docker.NetworkCreate(ctx, n); err != nil {
			fail("Netz %s: %v", n.Name, err)
		}
	}
	for _, im := range p.Images {
		name := strings.Join(im.Refs, ", ")
		if name == "" {
			name = shortID(im.ID)
		}
		if id := j.sameImage(ctx, im); id != "" {
			j.printf("  Image %s: schon hier, gleich wie im Backup\n", name)
			if err := j.tagImage(ctx, []string{id}, append(append([]string{}, im.Refs...), im.Tags...)); err != nil {
				fail("Image %s: %v", name, err)
			}
			rec.ImageIDs[im.ID] = []string{id}
			continue
		}
		j.printf("  Image %s\n", name)
		ids, err := j.loadImage(ctx, im)
		if err != nil {
			fail("Image %s: %v", name, err)
			continue
		}
		rec.ImageIDs[im.ID] = ids
	}

	for _, g := range p.Groups {
		if err := ctx.Err(); err != nil {
			break
		}
		rec.Groups = append(rec.Groups, j.group(ctx, g))
	}

	if len(p.Loose)+len(p.Volumes) > 0 {
		j.printf("\nOhne Projekt\n")
	}
	for _, f := range p.Loose {
		j.printf("  %s\n", f.Title)
		if err := j.Repo.Restore(ctx, f.Snapshot, "", "/"); err != nil {
			fail("%s: %v", f.Title, err)
		}
	}
	for _, v := range p.Volumes {
		j.printf("  %s\n", v.Title)
		if err := j.restoreVolume(ctx, Group{}, v, false); err != nil {
			fail("%s: %v", v.Title, err)
		}
	}
	if p.Caddy != nil && j.Caddy != nil && ctx.Err() == nil {
		rec.Caddy = j.caddy(ctx, p.Caddy)
		if rec.Caddy.Error != "" {
			general = append(general, "Caddy: "+rec.Caddy.Error)
		}
	}
	if len(general) > 0 {
		rec.Groups = append(rec.Groups, GroupRecord{Name: "(allgemein)", Errors: general})
	}

	j.printf("\nKontrolle nach dem Start\n")
	sleepCtx(ctx, orDefault(j.Settle, 15*time.Second))
	for i := range rec.Groups {
		gr := &rec.Groups[i]
		if !gr.Compose {
			continue
		}
		cs, err := j.Docker.ProjectContainers(ctx, gr.Name)
		if err != nil {
			gr.Errors = append(gr.Errors, err.Error())
			continue
		}
		var bad []string
		for _, svc := range gr.Services {
			ok := false
			for _, c := range cs {
				if c.Service == svc && c.Running {
					ok = true
				}
			}
			if !ok {
				bad = append(bad, svc)
			}
		}
		if len(bad) > 0 {
			gr.NotRunning = bad
			j.printf("  ✗ %s: läuft nicht: %s (docker compose logs zeigt warum)\n", gr.Name, strings.Join(bad, ", "))
		} else {
			j.printf("  ✓ %s: alle Dienste laufen\n", gr.Name)
		}
	}
	j.carryOver(rec)
	return rec
}

func (j *Job) group(ctx context.Context, g Group) GroupRecord {
	gr := GroupRecord{Name: g.Name, Compose: g.Compose, Services: g.Services, Ports: g.Ports, Dir: g.Dir, KeptOff: g.KeptOff}
	for _, f := range g.Paths {
		gr.Paths = append(gr.Paths, f.Paths...)
	}
	for _, v := range g.Volumes {
		if v.Name != "" {
			gr.Volumes = append(gr.Volumes, v.Name)
		}
	}
	// Datenbanken, die Compose beim Start neu anlegt: ihr Ordner oder Volume
	// gehört ebenso zur Kopie.
	for _, d := range g.Dumps {
		switch {
		case strings.HasPrefix(d.Key, "path:"):
			gr.Paths = append(gr.Paths, strings.TrimPrefix(d.Key, "path:"))
		case strings.HasPrefix(d.Key, "volume:"):
			gr.Volumes = append(gr.Volumes, strings.TrimPrefix(d.Key, "volume:"))
		}
	}
	j.printf("\n%s\n", g.Name)
	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		gr.Errors = append(gr.Errors, msg)
		j.printf("    ✗ %s\n", msg)
	}
	// Läuft das Projekt hier schon (zweiter Versuch, „überschreiben“),
	// darf nichts mehr in seine Dateien schreiben, während sie zurückkommen.
	if g.Compose {
		cs, err := j.Docker.ProjectContainers(ctx, g.Name)
		if err != nil {
			fail("Container des Projekts: %v; nichts zurückgespielt", err)
			return gr
		}
		if err := j.stopRunning(ctx, cs); err != nil {
			fail("laufende Container stoppen: %v; nichts zurückgespielt", err)
			return gr
		}
	}
	// Rohdateien einer Datenbank kommen nur in ein leeres Volume.
	raw := map[string]bool{}
	for _, d := range g.Dumps {
		if d.Raw {
			raw[d.Key] = true
		}
	}
	// Rohdateien einer Datenbank in einem Ordner, den ein früherer Lauf von
	// HostDeck hier angelegt hat (Auffrischen mit einem letzten Lauf): Erst leeren,
	// sonst liegen Dateien der Probe-Kopie (WAL, pg_xact …) neben denen vom
	// alten Server. Die Container sind schon aus.
	for _, d := range g.Dumps {
		dir, ok := strings.CutPrefix(d.Key, "path:")
		if !d.Raw || !ok || !j.owned().path(dir) {
			continue
		}
		if empty, err := dirEmpty(dir); err != nil || empty {
			continue
		}
		j.printf("  Datenbank-Dateien vom letzten Lauf in %s werden ersetzt\n", dir)
		if err := emptyDir(dir); err != nil {
			fail("%s leeren: %v; nichts zurückgespielt", dir, err)
			return gr
		}
	}
	skipped := map[string]bool{}
	for _, f := range g.Paths {
		j.printf("  Dateien: %s\n", f.Title)
		if err := j.Repo.Restore(ctx, f.Snapshot, "", "/"); err != nil {
			fail("%s: %v", f.Title, err)
		}
	}
	if !g.Compose {
		for _, v := range g.Volumes {
			j.printf("  %s\n", v.Title)
			if err := j.restoreVolume(ctx, g, v, false); err != nil {
				fail("%s: %v", v.Title, err)
			}
		}
		return gr
	}
	for _, f := range g.Files {
		if _, err := os.Stat(f); err != nil {
			fail("Compose-Datei %s fehlt nach dem Zurückspielen; Projekt wird nicht gestartet", f)
			return gr
		}
	}
	for _, v := range g.Volumes {
		if v.Name != "" && v.External {
			if _, ok, _ := j.Docker.VolumeMountpoint(ctx, v.Name); !ok {
				if err := j.Docker.VolumeCreate(ctx, v.Name); err != nil {
					fail("Volume %s anlegen: %v", v.Name, err)
				}
			}
		}
	}
	for _, f := range g.EnvFiles {
		if _, err := os.Stat(f); err != nil {
			j.printf("  Hinweis: Env-Datei %s fehlt hier; Compose legt die Container ohne sie an\n", f)
		}
	}
	j.printf("  Container anlegen (docker compose create)\n")
	err := j.Docker.ComposeCreate(ctx, g)
	// Fehlt Compose ein externes Volume, das im Backup ist, legt HostDeck es an
	// und versucht es noch einmal; die Daten kommen gleich danach hinein.
	if missing := missingExternal(err, g); len(missing) > 0 {
		for _, name := range missing {
			j.printf("  Volume %s anlegen (in der Compose-Datei als external)\n", name)
			if cerr := j.Docker.VolumeCreate(ctx, name); cerr != nil {
				fail("Volume %s anlegen: %v", name, cerr)
			}
		}
		err = j.Docker.ComposeCreate(ctx, g)
	}
	if err != nil {
		fail("%v", err)
		return gr
	}
	for _, msg := range j.lostEnv(ctx, g) {
		fail("%s", msg)
	}
	for _, v := range g.Volumes {
		j.printf("  %s\n", v.Title)
		if err := j.restoreVolume(ctx, g, v, raw[v.Key]); err != nil {
			fail("%s: %v", v.Title, err)
			skipped[v.Key] = true
		}
	}
	for _, d := range g.Dumps {
		if d.Raw && skipped[d.Key] {
			gr.DBs = append(gr.DBs, DBRecord{Title: d.Title, Service: d.Service, Engine: d.Engine, Raw: true, Failed: true,
				Note: "Datenbank-Dateien nicht zurückgespielt"})
			continue
		}
		db := j.database(ctx, g, d)
		if db.Failed {
			gr.Errors = append(gr.Errors, d.Title+": "+db.Note)
		}
		if len(db.Errors) > 0 {
			gr.Errors = append(gr.Errors, fmt.Sprintf("%s: %d Fehler beim Einspielen, etwa: %s", d.Title, len(db.Errors), db.Errors[0]))
		}
		if len(db.Diff) > 0 {
			gr.Errors = append(gr.Errors, fmt.Sprintf("%s: %d Tabellen weichen vom Backup ab", d.Title, len(db.Diff)))
		}
		gr.DBs = append(gr.DBs, db)
	}
	if len(g.KeptOff) > 0 {
		j.printf("  bleibt aus, wie von dir im Leitstand gestoppt: %s\n", strings.Join(g.KeptOff, ", "))
	}
	if len(g.Services) == 0 {
		j.printf("  lief beim Backup nicht und bleibt aus\n")
		return gr
	}
	j.printf("  starten (docker compose up)\n")
	if err := j.Docker.ComposeUp(ctx, g, g.Services, false); err != nil {
		fail("%v", err)
	}
	return gr
}

// lostEnv vergleicht nach dem Anlegen: Welche Variable hatte auf dem alten
// Server einen Wert und ist hier leer oder fehlt? Das passiert, wenn ein
// Skript den Wert beim Start aus der Shell an Compose gegeben hat, statt
// ihn in die .env zu schreiben. Der Container läuft dann, tut aber nicht,
// was er soll. HostDeck vergleicht nur Namen, nie Werte.
func (j *Job) lostEnv(ctx context.Context, g Group) []string {
	var out []string
	for _, ec := range g.Env {
		id, err := j.Docker.ServiceContainer(ctx, g.Name, ec.Service)
		if err != nil {
			continue
		}
		now, err := j.Docker.EnvSet(ctx, id)
		if err != nil {
			continue
		}
		have := map[string]bool{}
		for _, n := range now {
			have[n] = true
		}
		var lost []string
		for _, n := range ec.Set {
			if !have[n] {
				lost = append(lost, n)
			}
		}
		if len(lost) == 0 {
			continue
		}
		vars, verb, is := strings.Join(lost, ", "), "hatte", "ist"
		if len(lost) > 1 {
			verb, is = "hatten", "sind"
		}
		where := "der .env des Projekts"
		if g.Dir != "" {
			where = filepath.Join(g.Dir, ".env")
		}
		out = append(out, fmt.Sprintf("%s: %s %s auf dem alten Server einen Wert und %s hier leer. %s", ec.Service, vars, verb, is, envAdvice(g, ec.Service, lost, where)))
	}
	return out
}

// envAdvice sagt, was in die .env gehört. Der Name im Container ist nicht
// immer der, den man eintragen muss: Steht in der Compose-Datei
// „MODUS: ${START_MODUS:-}“, kommt der Wert aus START_MODUS. Den
// laufenden Dienst auf dem alten Server neu anzulegen, rät HostDeck nicht: Dort
// gilt der Wert aus der Shell noch, und ein falscher Eintrag würde ihn leeren.
func envAdvice(g Group, service string, lost []string, where string) string {
	var entries, notes []string
	unknown := false
	for _, k := range lost {
		expr, names, found := envSource(g.Files, service, k)
		switch {
		case !found:
			unknown = true
			notes = append(notes, k+" steht in keiner Compose-Datei")
		case len(names) == 0:
			unknown = true
			notes = append(notes, fmt.Sprintf("In der Compose-Datei steht „%s: %s“", k, expr))
		case len(names) == 1 && names[0] == k:
			entries = append(entries, k+"=…")
		default:
			for _, n := range names {
				entries = append(entries, n+"=…")
			}
			notes = append(notes, fmt.Sprintf("In der Compose-Datei steht „%s: %s“, den Wert liefert also %s", k, expr, strings.Join(names, " bzw. ")))
		}
	}
	var b strings.Builder
	b.WriteString("Der Wert kam beim Start wohl aus der Shell oder einem Skript, nicht aus einer Datei. ")
	for _, n := range notes {
		b.WriteString(n + ". ")
	}
	switch {
	case len(entries) > 0 && !unknown:
		fmt.Fprintf(&b, "Trag %s in %s ein. ", strings.Join(entries, " und "), where)
	case len(entries) > 0:
		fmt.Fprintf(&b, "Trag %s in %s ein, dazu die Variable, aus der der übrige Wert kommt. ", strings.Join(entries, " und "), where)
	default:
		fmt.Fprintf(&b, "Trag die Variable, aus der der Wert kommt, in %s ein. ", where)
	}
	b.WriteString("Am besten auf dem alten Server: Dann ist der Eintrag im nächsten Backup und kommt beim nächsten Zurückspielen mit. Den laufenden Dienst dort nicht neu anlegen, das braucht es nicht. ")
	cmd := "docker compose up -d " + service
	if g.Dir != "" {
		cmd = "im Ordner " + g.Dir + ": " + cmd
	}
	fmt.Fprintf(&b, "Willst du es hier gleich richten, trag es auch hier ein und leg den Dienst neu an (%s).", cmd)
	return b.String()
}

// stopRunning hält laufende Container an.
func (j *Job) stopRunning(ctx context.Context, cs []ContainerState) error {
	var ids, names []string
	for _, c := range cs {
		if c.Running {
			ids = append(ids, c.ID)
			names = append(names, c.Name)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	j.printf("  läuft hier schon, stoppe: %s\n", strings.Join(names, ", "))
	return j.Docker.Stop(ctx, ids)
}

// restoreVolume spielt ein Volume in seinen Ordner auf diesem Server.
// Container, die es gerade nutzen, hält es vorher an. Mit mustBeEmpty
// (Rohdateien einer Datenbank) fasst es ein Volume mit Inhalt nicht an.
func (j *Job) restoreVolume(ctx context.Context, g Group, v Volume, mustBeEmpty bool) error {
	var dir string
	switch {
	case v.Name != "":
		d, ok, err := j.Docker.VolumeMountpoint(ctx, v.Name)
		if err != nil {
			return err
		}
		if !ok {
			if err := j.Docker.VolumeCreate(ctx, v.Name); err != nil {
				return err
			}
			if d, ok, err = j.Docker.VolumeMountpoint(ctx, v.Name); err != nil || !ok {
				return fmt.Errorf("Volume %s fehlt nach dem Anlegen", v.Name)
			}
		}
		dir = d
	case v.Service != "" && g.Compose:
		id, err := j.Docker.ServiceContainer(ctx, g.Name, v.Service)
		if err != nil {
			return err
		}
		if dir, err = j.Docker.MountSource(ctx, id, v.Dest); err != nil {
			return err
		}
	default:
		return errors.New("Ziel unbekannt")
	}
	if v.OldPath == "" {
		return errors.New("alter Ordner unbekannt")
	}
	if mustBeEmpty && v.Name != "" && j.owned().volume(v.Name) {
		if empty, err := dirEmpty(dir); err == nil && !empty {
			// Datenbank-Dateien, die HostDeck selbst hier angelegt hat: ersetzen.
			users, err := j.Docker.VolumeUsers(ctx, v.Name)
			if err != nil {
				return err
			}
			if len(users) > 0 {
				if err := j.Docker.Stop(ctx, users); err != nil {
					return fmt.Errorf("Container stoppen: %v", err)
				}
			}
			j.printf("    Datenbank-Dateien vom letzten Lauf werden ersetzt\n")
			if err := emptyDir(dir); err != nil {
				return err
			}
		}
	}
	if mustBeEmpty {
		if empty, err := dirEmpty(dir); err == nil && !empty {
			return errors.New("das Volume hat schon Datenbank-Dateien; HostDeck spielt dort nichts ein. Liegt darin nichts Wichtiges: Projekt stoppen, Volume mit docker volume rm löschen und neu zurückspielen")
		}
	}
	if v.Name != "" {
		users, err := j.Docker.VolumeUsers(ctx, v.Name)
		if err != nil {
			return err
		}
		if len(users) > 0 {
			j.printf("    wird gerade benutzt, stoppe %d Container\n", len(users))
			if err := j.Docker.Stop(ctx, users); err != nil {
				return fmt.Errorf("Container stoppen: %v", err)
			}
		}
	}
	return j.Repo.Restore(ctx, v.Snapshot, v.OldPath, dir)
}

// database startet den Datenbank-Dienst allein, spielt den Dump ein und
// vergleicht die Zeilenzahlen mit dem Dump.
func (j *Job) database(ctx context.Context, g Group, d Dump) DBRecord {
	db := DBRecord{Title: d.Title, Service: d.Service, Engine: d.Engine, Raw: d.Raw}
	j.printf("  Datenbank %s (%s im Dienst %s)\n", d.Title, d.Engine, d.Service)
	note := func(format string, args ...any) DBRecord {
		db.Note = fmt.Sprintf(format, args...)
		db.Failed = true
		j.printf("    ✗ %s\n", db.Note)
		return db
	}
	sc, err := ScriptsFor(d.Engine)
	if err != nil {
		return note("%v", err)
	}
	if err := j.Docker.ComposeUp(ctx, g, []string{d.Service}, true); err != nil {
		return note("Start: %v", err)
	}
	id, err := j.Docker.ServiceContainer(ctx, g.Name, d.Service)
	if err != nil {
		return note("%v", err)
	}
	if err := j.waitReady(ctx, id, sc.Ready); err != nil {
		return note("%v", err)
	}
	counter := NewDumpCounter(d.Engine)
	if d.Raw {
		// Die Dateien kamen schon zurück; der Dump dient nur zum Vergleich.
		db.From = j.from
		if counter != nil {
			if err := j.Repo.Dump(ctx, d.Snapshot, d.File, counter); err != nil {
				return note("Dump lesen: %v", err)
			}
		}
	} else {
		before, err := j.count(ctx, id, sc)
		if err != nil {
			return note("Zählen vor dem Einspielen: %v", err)
		}
		if len(before) > 0 && before.Rows() > 0 {
			// Ein zweiter Lauf auf denselben Server: Kam genau dieser Dump
			// schon einmal hier hinein und stimmen die Zeilen je Tabelle noch,
			// ist das kein Fehler. Sonst spielt HostDeck nichts über vorhandene
			// Daten.
			if j.replayedBefore(g, d) && counter != nil {
				if err := j.Repo.Dump(ctx, d.Snapshot, d.File, counter); err == nil {
					if c := counter.Counts(); len(c) > 0 && len(before.Diff(c)) == 0 {
						db.Counts, db.From = c, j.from
						db.Note = "dieser Dump wurde hier schon bei einem früheren Lauf eingespielt; nicht erneut eingespielt"
						j.printf("    ✓ Dieser Dump wurde hier schon bei einem früheren Lauf eingespielt; gleich viele Zeilen je Tabelle (%d Tabellen, %d Zeilen). Nicht erneut eingespielt; was die Projekte hier seitdem geändert haben, bleibt.\n", len(c), c.Rows())
						return db
					}
				}
			}
			if j.owned().db(d) {
				j.printf("    Datenbank vom letzten Lauf (%d Zeilen) wird aus dem frischen Dump ersetzt …\n", before.Rows())
				nid, err := j.resetDB(ctx, g, d, id, sc)
				if err != nil {
					return note("Datenbank vom letzten Lauf leeren: %v", err)
				}
				id = nid
				// Der Zähler hat den Dump eben schon einmal gesehen.
				counter = NewDumpCounter(d.Engine)
				if before, err = j.count(ctx, id, sc); err != nil {
					return note("Zählen nach dem Leeren: %v", err)
				}
			}
		}
		if len(before) > 0 && before.Rows() > 0 {
			how := "Leere die Datenbank (etwa ihr Volume löschen) oder setze den neuen Server neu auf und wiederhole dann den Lauf"
			if j.InstallDocker != nil { // aus dem Leitstand
				how = "Sie stammt nicht aus dem letzten Lauf von HostDeck, darum ersetzt HostDeck sie nicht. Ist es eine ältere Probe-Kopie, räumt „Entfernen“ auf der Seite Umzug sie samt Datenbanken ab; danach den Probe-Umzug wiederholen"
			}
			return note("die Datenbank enthält schon Daten (%d Zeilen). HostDeck spielt einen Dump nur in eine leere Datenbank ein, damit nichts vermischt wird. %s", before.Rows(), how)
		}
		j.printf("    Dump einspielen …\n")
		begin := j.now()
		errs, err := j.replay(ctx, id, sc, d, counter)
		if err != nil {
			return note("%v", err)
		}
		db.Replayed, db.From = true, j.from
		db.Errors = errs
		j.printf("    eingespielt in %s\n", time.Since(begin).Round(time.Second))
	}
	if counter == nil {
		db.Note = "Zeilen lassen sich für diese Datenbank nicht vergleichen"
		return db
	}
	db.Counts = counter.Counts()
	after, err := j.count(ctx, id, sc)
	if err != nil {
		return note("Zählen: %v", err)
	}
	db.Diff = after.Diff(db.Counts)
	if len(db.Diff) == 0 {
		j.printf("    ✓ %d Tabellen, %d Zeilen, wie im Backup\n", len(db.Counts), db.Counts.Rows())
	} else {
		j.printf("    ✗ %d Tabellen weichen ab: %s\n", len(db.Diff), strings.Join(firstN(db.Diff, 3), "; "))
	}
	return db
}

// replayedBefore sagt, ob der letzte Lauf auf diesem Server genau diesen
// Dump (gleiche Quelle, gleicher Lauf) in diese Datenbank eingespielt hat.
func (j *Job) replayedBefore(g Group, d Dump) bool {
	if j.prev == nil {
		return false
	}
	for _, pg := range j.prev.Groups {
		if pg.Name != g.Name {
			continue
		}
		for _, pd := range pg.DBs {
			if pd.Title != d.Title || pd.Service != d.Service || pd.Failed {
				continue
			}
			if pd.From != "" {
				return pd.From == j.from
			}
			// Ältere wiederherstellung.json ohne Herkunft (HostDeck 1e bis
			// c55ca2d): derselbe Lauf, eingespielt oder schon vorhanden.
			return j.prev.Source+"/"+j.prev.Run == j.from &&
				(pd.Replayed || strings.HasPrefix(pd.Note, "hatte schon den Stand aus dem Backup"))
		}
	}
	return false
}

func (j *Job) waitReady(ctx context.Context, id, script string) error {
	deadline := j.now().Add(orDefault(j.ReadyWait, 3*time.Minute))
	for {
		code, err := j.Docker.Exec(ctx, id, script, nil, io.Discard, io.Discard)
		if err == nil && code == 0 {
			return nil
		}
		if running, rerr := j.Docker.Running(ctx, id); rerr == nil && !running {
			return errors.New("der Datenbank-Container läuft nicht (docker logs zeigt warum)")
		}
		if j.now().After(deadline) {
			return errors.New("die Datenbank wurde nicht rechtzeitig bereit")
		}
		if !sleepCtx(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
}

func (j *Job) count(ctx context.Context, id string, sc Scripts) (Counts, error) {
	var out, errb bytes.Buffer
	code, err := j.Docker.Exec(ctx, id, sc.Count, nil, &out, &errb)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("Exit-Code %d: %s", code, lastLine(errb.String()))
	}
	return ParseCounts(out.Bytes())
}

// replay schickt den Dump aus dem Backup in die Datenbank und zählt dabei
// mit. Es liefert die Fehlermeldungen der Datenbank, die nicht zu einem
// frischen Container gehören (etwa ein Index, der sich nicht anlegen ließ).
func (j *Job) replay(ctx context.Context, id string, sc Scripts, d Dump, counter DumpCounter) ([]string, error) {
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		var w io.Writer = pw
		if counter != nil {
			w = io.MultiWriter(pw, counter)
		}
		err := j.Repo.Dump(ctx, d.Snapshot, d.File, w)
		pw.CloseWithError(err)
		errc <- err
	}()
	stderr := &errLines{}
	code, err := j.Docker.Exec(ctx, id, sc.Replay, pr, io.Discard, stderr)
	pr.CloseWithError(errors.New("abgebrochen"))
	derr := <-errc
	if derr != nil {
		return nil, fmt.Errorf("Dump lesen: %v", derr)
	}
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("Einspielen: Exit-Code %d: %s", code, strings.Join(stderr.unexpected(), " | "))
	}
	var errs []string
	if u := stderr.unexpected(); len(u) > 0 {
		for _, l := range u {
			if strings.Contains(l, "ERROR") || strings.Contains(l, "FATAL") {
				errs = append(errs, l)
			}
		}
		if len(errs) > 0 {
			j.printf("    ✗ %d Fehler beim Einspielen: %s\n", len(errs), strings.Join(firstN(errs, 3), " | "))
		} else {
			j.printf("    Meldungen beim Einspielen: %s\n", strings.Join(firstN(u, 3), " | "))
		}
	}
	return errs, nil
}

// errLines sammelt Fehlermeldungen beim Einspielen. „existiert schon“ ist
// bei einem frischen Container normal (Rolle und Datenbank legt das Image an).
type errLines struct {
	part  []byte
	lines []string
}

func (e *errLines) Write(p []byte) (int, error) {
	e.part = append(e.part, p...)
	for {
		i := bytes.IndexByte(e.part, '\n')
		if i < 0 {
			break
		}
		l := strings.TrimSpace(string(e.part[:i]))
		e.part = e.part[i+1:]
		if l != "" && len(e.lines) < 50 {
			e.lines = append(e.lines, l)
		}
	}
	return len(p), nil
}

func (e *errLines) unexpected() []string {
	var out []string
	for _, l := range e.lines {
		if strings.Contains(l, "already exists") || strings.Contains(l, "[Warning]") ||
			strings.Contains(l, "Using a password on the command line") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// loadImage lädt ein Image aus dem Backup und liefert die IDs, unter denen
// es hier liegt.
func (j *Job) loadImage(ctx context.Context, im Image) ([]string, error) {
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		err := j.Repo.Dump(ctx, im.Snapshot, im.File, pw)
		pw.CloseWithError(err)
		errc <- err
	}()
	loaded, lerr := j.Docker.Load(ctx, pr)
	pr.CloseWithError(errors.New("abgebrochen"))
	if derr := <-errc; derr != nil {
		return nil, fmt.Errorf("aus dem Backup lesen: %v", derr)
	}
	if lerr != nil {
		return nil, lerr
	}
	// Namen, unter denen Container das Image nutzen, die aber nicht im
	// Image stehen, kommen dazu: sonst sucht Compose sie im Internet.
	// Ein Name, den es hier schon gibt, wird umgehängt: Zurück kommt, was
	// beim Backup lief, nicht ein anderes Image gleichen Namens.
	// Erst mit der ID, die docker load meldet: Mit dem containerd-Speicher
	// ist es eine andere als auf dem alten Server.
	ids := append(append([]string{}, loaded...), im.ID)
	if err := j.tagImage(ctx, ids, im.Tags); err != nil {
		return nil, err
	}
	// Die ID hier merken: docker load nennt bei Images mit Namen oft nur
	// den Namen.
	var here []string
	for _, id := range loaded {
		if j.Docker.ImageExists(ctx, id) {
			here = append(here, id)
		}
	}
	for _, n := range append(append([]string{}, im.Refs...), im.Tags...) {
		if id := j.Docker.ImageID(ctx, n); id != "" {
			here = append(here, id)
			break
		}
	}
	if len(here) == 0 && j.Docker.ImageExists(ctx, im.ID) {
		here = append(here, im.ID)
	}
	return here, nil
}

// keepStopped trägt ein, was du auf dem alten Server im Leitstand gestoppt
// hattest. Der Leitstand und die Überwachung hier kennen es dann ebenfalls
// als „von dir gestoppt“: keine Warnung, kein Start beim nächsten Backup.
func (j *Job) keepStopped(p *Plan) error {
	if j.UserStoppedFile == "" || len(p.UserStopped) == 0 {
		return nil
	}
	m := betrieb.LoadStopped(j.UserStoppedFile)
	for name, at := range p.UserStopped {
		m[name] = at
	}
	return betrieb.SaveStopped(j.UserStoppedFile, m)
}

func (j *Job) saveRecord(rec *Record) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(j.StateDir, 0o700); err != nil {
		return err
	}
	return outdir.WritePrivate(RecordPath(j.StateDir), append(b, '\n'))
}

func (j *Job) summary(rec *Record) int {
	var bad []string
	for _, g := range rec.Groups {
		for _, e := range g.Errors {
			bad = append(bad, g.Name+": "+e)
		}
		if len(g.NotRunning) > 0 {
			bad = append(bad, g.Name+": läuft nicht: "+strings.Join(g.NotRunning, ", "))
		}
	}
	if len(bad) == 0 {
		j.printf("\nErgebnis: Alles zurückgespielt, alle Projekte laufen.\n")
		j.printf("Später noch einmal prüfen: hostdeck verify\n")
		return ExitOK
	}
	j.printf("\nErgebnis: zurückgespielt, aber nicht alles ist in Ordnung:\n")
	for _, b := range bad {
		j.printf("  - %s\n", b)
	}
	return ExitFailed
}

// LoadRecord liest wiederherstellung.json.
func LoadRecord(stateDir string) (*Record, error) {
	b, err := os.ReadFile(RecordPath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("auf diesem Server wurde noch nichts mit hostdeck restore zurückgespielt")
	}
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return append(append([]string(nil), xs[:n]...), fmt.Sprintf("und %d weitere", len(xs)-n))
	}
	return xs
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

var externalMissing = regexp.MustCompile(`external volume "([^"]+)" not found`)

// missingExternal nennt die Volumes aus der Fehlermeldung von Compose, die
// fehlen und im Backup dieses Projekts sind.
func missingExternal(err error, g Group) []string {
	if err == nil {
		return nil
	}
	ours := map[string]bool{}
	for _, v := range g.Volumes {
		if v.Name != "" {
			ours[v.Name] = true
		}
	}
	var out []string
	for _, m := range externalMissing.FindAllStringSubmatch(err.Error(), -1) {
		if ours[m[1]] && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}
