package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/archiv"
	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/restic"
	"github.com/officialtaxz2/hostdeck/internal/runlog"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// Backup auf dem Server: Der Dienst sichert in einen Ordner unter
// /var/lib/hostdeck, verschlüsselt mit dem Backup-Passwort, und behält die
// letzten Läufe zum Herunterladen.

// keepRuns ist die Zahl gültiger Läufe, die auf dem Server bleiben.
const keepRuns = 2

// TargetServer steht im Verlauf bei Backups auf dem Server.
const targetServer = "server"

// spaceReserve bleibt auf der Platte frei, damit der Server weiterläuft.
const spaceReserve = 2 << 30

// serverBackups führt Backups für den Dienst aus.
type serverBackups struct {
	Repo   string // Backup-Ordner (restic-Repository)
	State  string // Ordner von HostDeck mit Passwort und restic, etwa /root/.hostdeck
	Result string // Ergebnisordner mit Auswahl und Verlauf
	Socket string // Docker
	LogDir string // Ausgabe der Läufe, je Lauf eine Datei
	Restic string // restic; leer: das von HostDeck verwaltete
	Host   string // Name des Servers für den Dateinamen
	Free   func(path string) (int64, error)
	// Downloading: Gerade lädt jemand das Backup herunter. Dann gibt das
	// Aufräumen nach dem Backup keinen Platz frei, damit der Download
	// nicht abbricht (N7).
	Downloading func() bool
	freeCache   struct {
		sync.Mutex
		at time.Time
		st dienst.StoredBackups
	}
}

func (b *serverBackups) free(path string) (int64, error) {
	if b.Free != nil {
		return b.Free(path)
	}
	return diskFree(path)
}

// Run führt einen Probelauf oder ein Backup aus und räumt danach auf.
func (b *serverBackups) Run(ctx context.Context, plan bool, in backup.Input, out io.Writer) int {
	return b.run(ctx, plan, false, in, out)
}

// RunFinal ist der letzte Lauf am Umzugstag: Die Projekte bleiben danach
// aus, die Datenbanken kommen zusätzlich als Dateien mit.
func (b *serverBackups) RunFinal(ctx context.Context, in backup.Input, out io.Writer) int {
	return b.run(ctx, false, true, in, out)
}

func (b *serverBackups) run(ctx context.Context, plan, final bool, in backup.Input, out io.Writer) int {
	w, closeLog := b.logTo(out, plan)
	defer closeLog()
	if !plan {
		if p := b.problem(); p != "" {
			fmt.Fprintf(w, "✗ %s\n", p)
			return backup.ExitBlocked
		}
	}
	r := serverRun{
		plan: plan, final: final, repoURL: b.Repo, resticPath: b.Restic, socket: b.Socket,
		where: "diesen Server (" + b.Repo + ")", input: in, stdout: w, screen: io.Discard,
		target:     targetServer,
		planHint:   "Das echte Backup startest du im Leitstand mit „Backup erstellen“.",
		checkSpace: b.checkSpace,
		exclude:    []string{b.Repo, filepath.Join(b.State, "cache")},
	}
	code := runBackupJob(ctx, r, b.State, b.Result)
	b.forgetCache()
	if code == backup.ExitOK && !plan {
		b.cleanup(context.WithoutCancel(ctx), w)
		b.forgetCache()
	}
	return code
}

// logTo schreibt die Ausgabe zusätzlich in eine Datei je Lauf.
func (b *serverBackups) logTo(out io.Writer, plan bool) (io.Writer, func()) {
	if b.LogDir == "" || os.MkdirAll(b.LogDir, 0o700) != nil {
		return out, func() {}
	}
	kind := "backup"
	if plan {
		kind = "probelauf"
	}
	f, err := os.OpenFile(filepath.Join(b.LogDir, time.Now().Format("20060102-150405")+"-"+kind+".log"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return out, func() {}
	}
	pruneLogs(b.LogDir, 30)
	return io.MultiWriter(out, f), func() { f.Close() }
}

// pruneLogs behält die neuesten keep Dateien.
func pruneLogs(dir string, keep int) {
	es, err := os.ReadDir(dir)
	if err != nil || len(es) <= keep {
		return
	}
	sort.Slice(es, func(i, j int) bool { return es[i].Name() > es[j].Name() })
	for _, e := range es[keep:] {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".log") {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// problem sagt, warum gerade kein Backup starten kann. Der Browser zeigt
// das Backup-Passwort nie an: Fehlt es oder ist es nicht bestätigt, gibt es
// das nur am Server.
func (b *serverBackups) problem() string {
	pw := backupPasswordFile(b.State)
	backup.RemoveDanglingPassword(pw)
	if _, err := os.Stat(pw); err != nil {
		return "Es gibt noch kein Backup-Passwort. Lege es einmal am Server fest: sudo hostdeck backup-passwort"
	}
	if _, err := os.Stat(backup.UnconfirmedPath(pw)); err == nil {
		return "Du hast noch nicht bestätigt, dass du das Backup-Passwort notiert hast. Am Server: sudo hostdeck backup-passwort"
	}
	return ""
}

func (b *serverBackups) checkSpace(total int64) error {
	free, err := b.free(existingParent(b.Repo))
	if err != nil {
		return nil // nicht messbar: restic meldet volle Platten selbst
	}
	used, _ := dirSize(b.Repo)
	// Neu kommt höchstens, was noch nicht im Ordner liegt; restic
	// dedupliziert und packt. Ohne Ordner zählt alles.
	need := total - used
	if need < 0 {
		need = total / 10
	}
	if free-need < spaceReserve {
		return fmt.Errorf("zu wenig Platz: frei sind %s, das Backup braucht bis zu %s und %s sollen frei bleiben", units.HumanSize(free), units.HumanSize(need), units.HumanSize(spaceReserve))
	}
	return nil
}

func existingParent(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		q := filepath.Dir(p)
		if q == p {
			return p
		}
		p = q
	}
}

// dirSize zählt die Bytes aller Dateien unter dir.
func dirSize(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return n, err
}

// serverRuns sind die gültigen Läufe auf dem Server, neueste zuerst.
func (b *serverBackups) serverRuns() []runlog.Entry {
	list, err := runlog.Load(b.Result)
	if err != nil {
		return nil
	}
	var out []runlog.Entry
	for _, e := range list {
		if e.Target == targetServer && e.Valid {
			out = append(out, e)
		}
	}
	return out
}

// cleanup behält die neuesten keepRuns gültigen Läufe und gibt den Platz
// der älteren frei.
func (b *serverBackups) cleanup(ctx context.Context, w io.Writer) {
	runs := b.serverRuns()
	if len(runs) == 0 {
		return
	}
	bin, err := resticBinary(ctx, b.Restic, b.State, io.Discard)
	if err != nil {
		fmt.Fprintf(w, "Hinweis: Aufräumen ging nicht: %v\n", err)
		return
	}
	repo := &restic.Repo{Bin: bin, URL: b.Repo, PasswordFile: backupPasswordFile(b.State), CacheDir: filepath.Join(b.State, "cache")}
	snaps, err := repo.Snapshots(ctx)
	if err != nil {
		fmt.Fprintf(w, "Hinweis: Aufräumen ging nicht: %v\n", err)
		return
	}
	ids, gone := outdated(snaps, runs)
	if len(ids) == 0 {
		return
	}
	if b.Downloading != nil && b.Downloading() {
		fmt.Fprintln(w, "\nAufräumen übersprungen: Gerade lädt jemand das Backup herunter. Ältere Läufe räumt HostDeck beim nächsten Backup weg.")
		return
	}
	old := fmt.Sprintf("%d ältere Läufe kommen", gone)
	if gone == 1 {
		old = "1 älterer Lauf kommt"
	}
	fmt.Fprintf(w, "\nAufräumen: Auf dem Server bleiben die letzten %d gültigen Backups; %s weg …\n", keepRuns, old)
	if err := repo.Forget(ctx, ids); err != nil {
		fmt.Fprintf(w, "Hinweis: Aufräumen ging nicht: %v\n", err)
		return
	}
	before, _ := dirSize(b.Repo)
	if err := repo.Prune(ctx); err != nil {
		fmt.Fprintf(w, "Hinweis: Platz freigeben ging nicht: %v\n", err)
		return
	}
	after, _ := dirSize(b.Repo)
	fmt.Fprintf(w, "  ✓ %s freigegeben\n", units.HumanSize(max(before-after, 0)))
}

// outdated wählt die Snapshots, die weg können: alle von Läufen, die älter
// sind als der älteste behaltene gültige Lauf. runs sind die gültigen
// Läufe, neueste zuerst. Was nach dem ältesten behaltenen kam, bleibt,
// auch ungültige Läufe; es gehört vielleicht zu einem Lauf, der gerade
// fertig wurde.
func outdated(snaps []restic.Snapshot, runs []runlog.Entry) (ids []string, gone int) {
	if len(runs) == 0 {
		return nil, 0
	}
	oldestKept := runs[min(len(runs), keepRuns)-1].Run
	seen := map[string]bool{}
	for _, s := range snaps {
		run := ""
		for _, t := range s.Tags {
			if v, ok := strings.CutPrefix(t, "sbm-run="); ok {
				run = v
			}
		}
		// Lauf-Kennungen sind Zeitstempel (20060102-150405) und lassen sich
		// als Text vergleichen.
		if run == "" || run >= oldestKept {
			continue
		}
		ids = append(ids, s.ID)
		if !seen[run] {
			seen[run] = true
			gone++
		}
	}
	return ids, gone
}

func (b *serverBackups) forgetCache() {
	b.freeCache.Lock()
	b.freeCache.at = time.Time{}
	b.freeCache.Unlock()
}

// Stored beschreibt den Backup-Ordner. Größe und freier Platz kosten
// etwas, deshalb höchstens alle 30 Sekunden neu.
func (b *serverBackups) Stored(context.Context) dienst.StoredBackups {
	b.freeCache.Lock()
	defer b.freeCache.Unlock()
	if time.Since(b.freeCache.at) < 30*time.Second {
		st := b.freeCache.st
		st.Problem = b.problem()
		st.Ready = st.Problem == ""
		return st
	}
	st := dienst.StoredBackups{Keep: keepRuns, Path: b.Repo}
	st.Problem = b.problem()
	st.Ready = st.Problem == ""
	st.Size, _ = dirSize(b.Repo)
	st.Free, _ = b.free(existingParent(b.Repo))
	runs := b.serverRuns()
	if st.Size > 0 {
		st.Runs = min(len(runs), keepRuns)
		if len(runs) > 0 {
			t := runs[0].Started
			st.Last, st.LastRun = &t, runs[0].Run
		}
	}
	b.freeCache.st, b.freeCache.at = st, time.Now()
	return st
}

// readme liegt im heruntergeladenen Archiv neben den Daten.
const readme = `Backup von HostDeck
==============

Dieser Ordner ist ein restic-Repository (restic %s), verschlüsselt mit
deinem Backup-Passwort. Ohne das Passwort lässt sich nichts davon lesen.

Darin liegen die letzten gültigen Backups von %s, wie sie der
Leitstand unter Backup zeigt.

Zurückspielen geht mit HostDeck (Umzug im Leitstand) oder von Hand mit restic:

    restic -r hostdeck-backup snapshots
    restic -r hostdeck-backup restore <snapshot> --target /

Stand des neuesten Backups: %s
`

// Archive packt den Backup-Ordner als tar-Datei.
func (b *serverBackups) Archive() (*archiv.Archive, string, error) {
	if _, err := os.Stat(filepath.Join(b.Repo, "config")); err != nil {
		return nil, "", errors.New("auf dem Server liegt noch kein Backup")
	}
	now := time.Now()
	host := b.Host
	if host == "" {
		host, _ = os.Hostname()
	}
	day := now.Format("2006-01-02")
	if runs := b.serverRuns(); len(runs) > 0 {
		day = runs[0].Started.Local().Format("2006-01-02")
	}
	name := "hostdeck-backup-" + safeName(host) + "-" + day + ".tar"
	a, err := archiv.Build(b.Repo, "hostdeck-backup", []string{"locks"},
		archiv.Extra{Name: "LIESMICH.txt", Body: []byte(fmt.Sprintf(readme, restic.Version, host, day))})
	if err != nil {
		return nil, "", err
	}
	return a, name, nil
}

// Remove leert den Backup-Ordner. Der Ordner selbst bleibt; das nächste
// Backup legt darin mit demselben Backup-Passwort neu an. Der Verlauf in
// Result bleibt, damit „letztes Backup“ weiter stimmt.
func (b *serverBackups) Remove(context.Context) (int64, error) {
	defer b.forgetCache()
	// Der Cache von restic gehört zum Inhalt des Ordners; ohne ihn baut
	// restic ihn beim nächsten Lauf neu auf.
	var cached int64
	if b.State != "" {
		cache := filepath.Join(b.State, "cache")
		cached, _ = dirSize(cache)
		if os.RemoveAll(cache) != nil {
			cached = 0
		}
	}
	before, _ := dirSize(b.Repo)
	es, err := os.ReadDir(b.Repo)
	if errors.Is(err, fs.ErrNotExist) {
		return cached, nil
	}
	if err != nil {
		return cached, err
	}
	for _, e := range es {
		if err := os.RemoveAll(filepath.Join(b.Repo, e.Name())); err != nil {
			after, _ := dirSize(b.Repo)
			return cached + max(before-after, 0), err
		}
	}
	return cached + before, nil
}

func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		}
		return '-'
	}, s)
	if s == "" {
		return "server"
	}
	return s
}

// backupPasswortCmd zeigt das Backup-Passwort oder legt es fest. Das geht
// nur hier, als root auf dem Server: Der Browser zeigt es nie an.
func backupPasswortCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("backup-passwort", flag.ContinueOnError)
	fl.SetOutput(stderr)
	show := fl.Bool("zeigen", false, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "hostdeck backup-passwort läuft auf dem Server als root: sudo hostdeck backup-passwort")
		return 2
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	p := newTTYPrompt(os.Stdin, stdout)
	if !p.Interactive() {
		fmt.Fprintln(stderr, "hostdeck backup-passwort braucht ein Terminal.")
		return 2
	}
	if err := backupPasswordDialog(p, dir, filepath.Join(ort.DataDir, ort.BackupRepo), *show); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	return 0
}

// backupPasswordDialog sorgt dafür, dass es ein Backup-Passwort gibt und
// dass es notiert ist. Ist beides schon so, zeigt es das Passwort nur mit
// show.
func backupPasswordDialog(p prompt, dir, repo string, show bool) error {
	pw := backupPasswordFile(dir)
	backup.RemoveDanglingPassword(pw)
	b, err := os.ReadFile(pw)
	switch {
	case err == nil:
		_, uerr := os.Stat(backup.UnconfirmedPath(pw))
		if uerr != nil && !show {
			p.Say("Das Backup-Passwort ist festgelegt und notiert. Zeigen: sudo hostdeck backup-passwort --zeigen\n")
			return nil
		}
		return confirmBackupPassword(p, pw, strings.TrimSpace(string(b)), "Dein Backup-Passwort")
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if _, err := os.Stat(filepath.Join(repo, "config")); err == nil {
		return fmt.Errorf("im Backup-Ordner %s liegen schon Backups, aber das Passwort dazu fehlt in %s. Lege diese Datei mit deinem Backup-Passwort an (nur für root lesbar)", repo, pw)
	}
	np, err := backup.NewPassword()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := outdir.WritePrivate(backup.UnconfirmedPath(pw), nil); err != nil {
		return err
	}
	if err := outdir.WritePrivate(pw, []byte(np+"\n")); err != nil {
		os.Remove(backup.UnconfirmedPath(pw))
		return err
	}
	return confirmBackupPassword(p, pw, np, "Neues Backup-Passwort")
}

func confirmBackupPassword(p prompt, file, pw, title string) error {
	p.Say(`
%s

Damit sind alle Backups verschlüsselt, auch die, die du herunterlädst.
Ohne dieses Passwort lässt sich kein Backup je wieder öffnen, auch von
niemand anderem. Der Leitstand zeigt es nie an; nur für den Probe-Umzug
tippst du es dort ein, und HostDeck vergleicht es bloß.

    %s

Speichere es jetzt in deinem Passwort-Manager (oder schreib es auf). Auf
dem Server liegt es nur für root lesbar in %s.

`, title, pw, file)
	for {
		ans, err := p.Line("Hast du es gespeichert? Dann tippe ja: ")
		if err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(ans), "ja") {
			os.Remove(backup.UnconfirmedPath(file))
			p.Say("\nBackup-Passwort notiert. Backups startest du ab jetzt im Leitstand.\n")
			return nil
		}
	}
}
