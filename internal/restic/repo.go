package restic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Repo ist ein restic-Repository. Zugangsdaten stehen nur in der Umgebung
// des restic-Prozesses, nie in seinen Argumenten.
type Repo struct {
	Bin          string // Pfad zu restic
	URL          string // etwa rest:http://127.0.0.1:18800/ oder ein Ordner
	PasswordFile string // Datei mit dem Repository-Passwort
	RestUser     string // Zugang zum Empfänger auf dem PC
	RestPass     string
	CacheDir     string // Cache von restic; Standard: der von restic
	Host         string // Rechnername in den Snapshots
	// Log bekommt die Fehlerausgabe von restic, Zeile für Zeile.
	Log io.Writer
}

// Exit-Codes von restic.
const (
	exitPartial      = 3  // Snapshot angelegt, aber manche Dateien unlesbar
	exitNoRepo       = 10 // Repository gibt es nicht
	exitLocked       = 11
	exitWrongPass    = 12
	exitInterrupted  = 130
	progressInterval = "0.2" // Fortschritt alle 5 Sekunden
)

// Error ist ein Fehler von restic mit Exit-Code und den letzten Zeilen
// seiner Fehlerausgabe.
type Error struct {
	Op   string
	Code int
	Tail string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("restic %s: Exit-Code %d", e.Op, e.Code)
	switch e.Code {
	case exitNoRepo:
		msg = fmt.Sprintf("restic %s: Das Backup-Ziel enthält noch kein Repository", e.Op)
	case exitLocked:
		msg = fmt.Sprintf("restic %s: Das Repository ist gerade gesperrt (läuft noch ein anderer Lauf?)", e.Op)
	case exitWrongPass:
		msg = fmt.Sprintf("restic %s: Das Passwort passt nicht zum Repository", e.Op)
	case exitInterrupted:
		msg = fmt.Sprintf("restic %s: abgebrochen", e.Op)
	}
	if e.Tail != "" {
		msg += ": " + e.Tail
	}
	return msg
}

// WrongPassword meldet, ob err ein falsches Passwort bedeutet.
func WrongPassword(err error) bool {
	var re *Error
	return errors.As(err, &re) && re.Code == exitWrongPass
}

func (r *Repo) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "RESTIC_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"RESTIC_REPOSITORY="+r.URL,
		"RESTIC_PASSWORD_FILE="+r.PasswordFile,
		"RESTIC_PROGRESS_FPS="+progressInterval,
	)
	if r.RestUser != "" {
		env = append(env, "RESTIC_REST_USERNAME="+r.RestUser, "RESTIC_REST_PASSWORD="+r.RestPass)
	}
	if r.CacheDir != "" {
		env = append(env, "RESTIC_CACHE_DIR="+r.CacheDir)
	}
	return env
}

func (r *Repo) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Env = r.env()
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 30 * time.Second
	return cmd
}

// tail behält die letzten Zeilen einer Ausgabe.
type tail struct {
	lines []string
	part  []byte
	log   io.Writer
}

func (t *tail) Write(p []byte) (int, error) {
	t.part = append(t.part, p...)
	for {
		i := bytes.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(t.part[:i]), "\r")
		t.part = t.part[i+1:]
		if line == "" {
			continue
		}
		if t.log != nil {
			fmt.Fprintln(t.log, line)
		}
		t.lines = append(t.lines, line)
		if len(t.lines) > 6 {
			t.lines = t.lines[1:]
		}
	}
	return len(p), nil
}

func (t *tail) String() string {
	if len(t.part) > 0 {
		t.lines = append(t.lines, string(t.part))
		t.part = nil
	}
	return strings.Join(t.lines, " | ")
}

func (r *Repo) run(ctx context.Context, op string, args ...string) ([]byte, error) {
	return r.runLog(ctx, r.Log, op, args...)
}

// runLog ist run mit eigenem Ziel für die Fehlerausgabe (nil: nur im Fehler).
func (r *Repo) runLog(ctx context.Context, log io.Writer, op string, args ...string) ([]byte, error) {
	cmd := r.command(ctx, args...)
	errs := &tail{log: log}
	cmd.Stderr = errs
	out, err := cmd.Output()
	if err != nil {
		return out, wrap(op, err, errs)
	}
	return out, nil
}

func wrap(op string, err error, errs *tail) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &Error{Op: op, Code: ee.ExitCode(), Tail: errs.String()}
	}
	return fmt.Errorf("restic %s: %w", op, err)
}

// Exists prüft, ob es das Repository schon gibt. Die Fehlerausgabe geht
// nicht ins Protokoll: Beim ersten Backup ist „repository does not exist“
// die erwartete Antwort und kein Fehler (Labor, Matrix „laufendes Backup“);
// bei einem echten Fehler steht sie in der Meldung.
func (r *Repo) Exists(ctx context.Context) (bool, error) {
	_, err := r.runLog(ctx, nil, "cat config", "cat", "config", "--no-lock")
	var re *Error
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &re) && re.Code == exitNoRepo:
		return false, nil
	default:
		return false, err
	}
}

// Init legt das Repository an.
func (r *Repo) Init(ctx context.Context) error {
	_, err := r.run(ctx, "init", "init")
	return err
}

// BackupArgs beschreibt einen Snapshot.
type BackupArgs struct {
	Paths    []string // Ordner und Dateien
	Excludes []string // absolute Pfade, die nicht mitkommen
	Tags     []string
	// Command liefert die Daten über seine Standardausgabe, etwa ein
	// Datenbank-Dump. Endet es mit einem Fehler, entsteht kein Snapshot.
	Command  []string
	Filename string // Dateiname im Snapshot für Command
	// Progress bekommt regelmäßig den Fortschritt.
	Progress func(Status)
}

// Status ist ein Fortschrittsstand von restic.
type Status struct {
	PercentDone float64 `json:"percent_done"`
	TotalBytes  int64   `json:"total_bytes"`
	BytesDone   int64   `json:"bytes_done"`
	FilesDone   int64   `json:"files_done"`
	TotalFiles  int64   `json:"total_files"`
}

// Result ist die Zusammenfassung eines Snapshots.
type Result struct {
	SnapshotID     string  `json:"snapshot_id"`
	FilesNew       int64   `json:"files_new"`
	FilesChanged   int64   `json:"files_changed"`
	FilesUnchanged int64   `json:"files_unmodified"`
	DataAdded      int64   `json:"data_added"`
	DataAddedPack  int64   `json:"data_added_packed"`
	TotalFiles     int64   `json:"total_files_processed"`
	TotalBytes     int64   `json:"total_bytes_processed"`
	Duration       float64 `json:"total_duration"`
	// Unreadable listet Dateien, die restic nicht lesen konnte. Der
	// Snapshot ist trotzdem entstanden.
	Unreadable []string `json:"-"`
}

type message struct {
	Type string `json:"message_type"`
	Status
	Result
	Item  string          `json:"item"`
	Error json.RawMessage `json:"error"`
}

// Backup legt einen Snapshot an.
func (r *Repo) Backup(ctx context.Context, a BackupArgs) (Result, error) {
	args := []string{"backup", "--json", "--host", r.Host}
	if len(a.Command) > 0 {
		// Bei Dumps gibt es nichts zu zählen. Bei Ordnern zählt restic
		// nebenher, damit der Fortschritt Prozente zeigen kann.
		args = append(args, "--no-scan")
	}
	for _, t := range a.Tags {
		args = append(args, "--tag", t)
	}
	// Als Argumente statt als Datei: In Ausschlussdateien setzt restic
	// Umgebungsvariablen ein, ein $ im Pfad würde dort verfälscht.
	for _, e := range a.Excludes {
		args = append(args, "--exclude", EscapePattern(e))
	}
	switch {
	case len(a.Command) > 0:
		args = append(args, "--stdin-from-command", "--stdin-filename", a.Filename, "--")
		args = append(args, a.Command...)
	case len(a.Paths) > 0:
		args = append(args, "--")
		args = append(args, a.Paths...)
	default:
		return Result{}, errors.New("restic backup: nichts zu sichern")
	}
	cmd := r.command(ctx, args...)
	errs := &tail{log: r.Log}
	cmd.Stderr = errs
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("restic backup: %w", err)
	}
	var res Result
	var unreadable []string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Type {
		case "status":
			if a.Progress != nil {
				a.Progress(m.Status)
			}
		case "summary":
			res = m.Result
		case "error":
			if m.Item != "" && len(unreadable) < 50 {
				unreadable = append(unreadable, m.Item)
			}
		}
	}
	_, _ = io.Copy(io.Discard, stdout)
	err = cmd.Wait()
	res.Unreadable = unreadable
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == exitPartial && res.SnapshotID != "" {
			return res, nil
		}
		return res, wrap("backup", err, errs)
	}
	if res.SnapshotID == "" {
		return res, errors.New("restic backup: keine Zusammenfassung erhalten")
	}
	return res, nil
}

// Check prüft die Struktur des Repositorys und liest subset der Daten
// zurück, etwa „3/20“. Leer heißt: nur die Struktur.
func (r *Repo) Check(ctx context.Context, subset string) error {
	args := []string{"check"}
	if subset != "" {
		args = append(args, "--read-data-subset", subset)
	}
	_, err := r.run(ctx, "check", args...)
	return err
}

// Snapshot ist ein Eintrag aus restic snapshots.
type Snapshot struct {
	ID    string    `json:"id"`
	Short string    `json:"short_id"`
	Time  time.Time `json:"time"`
	Paths []string  `json:"paths"`
	Tags  []string  `json:"tags"`
	Host  string    `json:"hostname"`
}

// Snapshots listet die Snapshots mit allen angegebenen Tags.
func (r *Repo) Snapshots(ctx context.Context, tags ...string) ([]Snapshot, error) {
	args := []string{"snapshots", "--json", "--no-lock"}
	if len(tags) > 0 {
		args = append(args, "--tag", strings.Join(tags, ","))
	}
	out, err := r.run(ctx, "snapshots", args...)
	if err != nil {
		return nil, err
	}
	var s []Snapshot
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("restic snapshots: %w", err)
	}
	return s, nil
}

// Forget löscht Snapshots. Den Platz gibt erst Prune frei.
func (r *Repo) Forget(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.run(ctx, "forget", append([]string{"forget"}, ids...)...)
	return err
}

// Prune entfernt Daten, die kein Snapshot mehr braucht.
func (r *Repo) Prune(ctx context.Context) error {
	_, err := r.run(ctx, "prune", "prune")
	return err
}

// EscapePattern macht aus einem Pfad ein Ausschlussmuster für restic, das
// genau diesen Pfad trifft, auch wenn er *, ? oder [ enthält.
func EscapePattern(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch r {
		case '*', '?', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Restore spielt einen Snapshot zurück, ohne das Repository zu sperren
// oder zu verändern. Mit sub nur diesen Pfad aus dem Snapshot, sonst
// alles; target ist der Zielordner, „/“ heißt: an die alten Orte.
func (r *Repo) Restore(ctx context.Context, snapshot, sub, target string) error {
	id := snapshot
	if sub != "" {
		id += ":" + sub
	}
	_, err := r.run(ctx, "restore", "restore", "--no-lock", id, "--target", target)
	return err
}

// Dump schreibt eine Datei aus einem Snapshot nach w.
func (r *Repo) Dump(ctx context.Context, snapshot, file string, w io.Writer) error {
	cmd := r.command(ctx, "dump", "--no-lock", snapshot, file)
	errs := &tail{log: r.Log}
	cmd.Stderr = errs
	cmd.Stdout = w
	if err := cmd.Run(); err != nil {
		return wrap("dump", err, errs)
	}
	return nil
}

// CheckPassword prüft, ob das Passwort zum Repository passt.
func (r *Repo) CheckPassword(ctx context.Context) error {
	_, err := r.runLog(ctx, nil, "cat config", "cat", "config", "--no-lock")
	return err
}
