package dienst

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/archiv"
	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
	"github.com/officialtaxz2/hostdeck/internal/units"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

// Backup im Leitstand: Der Dienst führt Probelauf und Backup auf dem
// Server aus, zeigt die Ausgabe, stellt Fragen als Knöpfe und reicht das
// fertige Backup zum Herunterladen durch. Die eigentliche Arbeit macht
// Backups; der Dienst sorgt für Anmeldung, Bestätigung, Laufsperre,
// Fortschritt und Protokoll.

// Backups macht Backups auf dem Server.
type Backups interface {
	// Run führt einen Probelauf (plan) oder ein Backup aus. Fragen gehen an
	// in, die Ausgabe an out. Ergebnis wie backup.Job.Run.
	Run(ctx context.Context, plan bool, in backup.Input, out io.Writer) int
	// Stored beschreibt den Backup-Ordner auf dem Server.
	Stored(ctx context.Context) StoredBackups
	// Archive packt den Backup-Ordner zum Herunterladen.
	Archive() (*archiv.Archive, string, error)
	// Remove leert den Backup-Ordner und sagt, wie viel Platz frei wurde.
	// Der Verlauf der Läufe bleibt.
	Remove(ctx context.Context) (int64, error)
}

// StoredBackups ist der Stand des Backup-Ordners.
type StoredBackups struct {
	// Ready: Ein Backup kann starten. Sonst sagt Problem, was fehlt.
	Ready   bool   `json:"bereit"`
	Problem string `json:"problem,omitempty"`
	Size    int64  `json:"groesse"` // belegt
	Free    int64  `json:"frei"`    // frei auf der Platte
	Runs    int    `json:"laeufe"`  // gültige Läufe im Ordner
	// Last ist der neueste gültige Lauf im Ordner.
	Last *time.Time `json:"letzter,omitempty"`
	// LastRun ist die Kennung dieses Laufs, etwa 20261101-205600.
	LastRun string `json:"lauf,omitempty"`
	Keep    int    `json:"behalten"` // so viele Läufe behält HostDeck
	// Path ist der Backup-Ordner; Speicherplatz zeigt ihn mit Size statt
	// mit der Größe aus der Bestandsaufnahme.
	Path string `json:"pfad,omitempty"`
}

// BackupJob ist der laufende oder letzte Probelauf oder das Backup.
type BackupJob struct {
	Running bool `json:"laeuft"`
	Plan    bool `json:"probelauf"`
	// Kind ist beim Umzug die Art des Laufs (siehe MoveKind).
	Kind    string     `json:"art,omitempty"`
	Started time.Time  `json:"start"`
	Done    *time.Time `json:"ende,omitempty"`
	// Code wie backup.Job: 0 gültig, 1 gescheitert oder ungültig, 2 nicht
	// gestartet (Vorprüfung).
	Code      int          `json:"code"`
	Cancelled bool         `json:"abgebrochen,omitempty"`
	Question  *JobQuestion `json:"frage,omitempty"`
	// Lines ab First; ältere Zeilen sind verworfen.
	Lines []string `json:"zeilen"`
	First int      `json:"erste"`
}

// JobQuestion ist eine offene Frage des Laufs.
type JobQuestion struct {
	ID      int             `json:"id"`
	Text    string          `json:"text"`
	Choices []backup.Choice `json:"auswahl,omitempty"`
}

// BackupReply ist die Antwort auf GET /api/backup.
type BackupReply struct {
	Stored StoredBackups `json:"ordner"`
	Job    *BackupJob    `json:"auftrag,omitempty"`
	// Fetched: Das Backup im Ordner ist vollständig heruntergeladen.
	Fetched *FetchedInfo `json:"heruntergeladen,omitempty"`
}

// FetchedInfo sagt, wann das Backup vollständig heruntergeladen wurde und
// wann HostDeck es vom Server löscht (leer: es bleibt).
type FetchedInfo struct {
	At       time.Time  `json:"am"`
	RemoveAt *time.Time `json:"loeschen_um,omitempty"`
}

func (d *Dienst) backupReply(ctx context.Context, from int) BackupReply {
	rep := BackupReply{Stored: d.Backups.Stored(ctx), Job: d.backupSnapshot(from)}
	d.mu.Lock()
	if f := d.fetched; f != nil && rep.Stored.Runs > 0 {
		rep.Fetched = &FetchedInfo{At: f.at}
		if !f.due.IsZero() {
			due := f.due
			rep.Fetched.RemoveAt = &due
		}
	}
	d.mu.Unlock()
	return rep
}

// maxJobLines begrenzt die Ausgabe, die der Dienst behält.
const maxJobLines = 4000

// backupRun ist der Zustand eines Laufs im Dienst.
type backupRun struct {
	job     *BackupJob
	cancel  context.CancelFunc
	answer  chan string
	qid     int
	addr    string
	gate    string
	partial []byte
	cr      bool // die letzte Zeile endete mit \r
	closing bool // Lauf ist zu Ende, Abbrechen geht nicht mehr
}

// jobInput stellt Fragen im Leitstand.
type jobInput struct {
	d   *Dienst
	run *backupRun
}

func (in jobInput) Ask(ctx context.Context, q backup.Question) (string, error) {
	if q.Secret {
		return "", errors.New("diese Frage geht nur am Server, nicht im Browser")
	}
	in.d.mu.Lock()
	in.run.qid++
	id := in.run.qid
	text := q.Note
	if text == "" {
		text = q.Text
	}
	in.run.job.Question = &JobQuestion{ID: id, Text: text, Choices: q.Choices}
	in.d.mu.Unlock()
	defer func() {
		in.d.mu.Lock()
		if in.run.job.Question != nil && in.run.job.Question.ID == id {
			in.run.job.Question = nil
		}
		in.d.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case a := <-in.run.answer:
			// Antworten kommen mit ihrer Frage; eine verspätete Antwort auf
			// eine frühere Frage zählt nicht.
			if n, rest, ok := strings.Cut(a, "\x00"); ok && n == strconv.Itoa(id) {
				return rest, nil
			}
		}
	}
}

// jobOutput sammelt die Ausgabe zeilenweise.
type jobOutput struct {
	d   *Dienst
	run *backupRun
}

func (o jobOutput) Write(p []byte) (int, error) {
	o.d.mu.Lock()
	defer o.d.mu.Unlock()
	r := o.run
	r.partial = append(r.partial, p...)
	for {
		i := strings.IndexAny(string(r.partial), "\n\r")
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(r.partial[:i]), " ")
		cr := r.partial[i] == '\r'
		r.partial = r.partial[i+1:]
		// \r\n ist ein Zeilenende, nicht zwei.
		skip := i == 0 && !cr && r.cr
		r.cr = cr
		if skip {
			continue
		}
		r.job.Lines = append(r.job.Lines, line)
		if over := len(r.job.Lines) - maxJobLines; over > 0 {
			r.job.Lines = append([]string(nil), r.job.Lines[over:]...)
			r.job.First += over
		}
	}
	if len(r.partial) > 4096 {
		r.job.Lines = append(r.job.Lines, string(r.partial))
		r.partial = nil
	}
	return len(p), nil
}

// backupSnapshot kopiert den Auftrag ab Zeile from.
func (d *Dienst) backupSnapshot(from int) *BackupJob {
	d.mu.Lock()
	defer d.mu.Unlock()
	return runSnapshot(d.brun, from)
}

// runSnapshot kopiert einen Auftrag ab Zeile from. Der Aufrufer hält d.mu.
func runSnapshot(run *backupRun, from int) *BackupJob {
	if run == nil {
		return nil
	}
	j := *run.job
	if j.Question != nil {
		q := *j.Question
		j.Question = &q
	}
	skip := from - j.First
	if skip < 0 {
		skip = 0
	}
	if skip > len(j.Lines) {
		skip = len(j.Lines)
	}
	j.Lines = append([]string{}, j.Lines[skip:]...)
	j.First += skip
	return &j
}

func (d *Dienst) handleBackup(w http.ResponseWriter, r *http.Request) {
	from, _ := strconv.Atoi(r.URL.Query().Get("ab"))
	writeJSON(w, http.StatusOK, d.backupReply(r.Context(), from))
}

func (d *Dienst) handleBackupPlan(w http.ResponseWriter, r *http.Request) {
	d.startBackup(w, r, true)
}

func (d *Dienst) handleBackupStart(w http.ResponseWriter, r *http.Request) {
	d.startBackup(w, r, false)
}

// startBackup startet einen Lauf im Hintergrund. Schließt jemand den
// Browser, läuft er weiter.
func (d *Dienst) startBackup(w http.ResponseWriter, r *http.Request, plan bool) {
	gate, addr := visitor(r)
	what := "Backup"
	if plan {
		what = "Probelauf"
	}
	if why := d.copyBlocked("Backups"); why != "" {
		writeJSON(w, http.StatusConflict, apiError{Error: why})
		return
	}
	if why := d.backupBlocked(r.Context()); why != "" {
		writeJSON(w, http.StatusConflict, apiError{Error: why})
		return
	}
	if !plan {
		if st := d.Backups.Stored(r.Context()); !st.Ready {
			writeJSON(w, http.StatusConflict, apiError{Error: st.Problem})
			return
		}
	}
	l, err := lauf.TakeIn(d.RunDir, what)
	var busy *lauf.BusyError
	switch {
	case errors.As(err, &busy):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	run := &backupRun{
		job:    &BackupJob{Running: true, Plan: plan, Started: d.now(), Lines: []string{}},
		answer: make(chan string, 1),
		addr:   addr, gate: gate,
	}
	d.mu.Lock()
	base := d.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	run.cancel = cancel
	d.brun = run
	d.mu.Unlock()
	d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: what + " gestartet", Addr: addr, Gate: gate})
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		out := jobOutput{d: d, run: run}
		code := d.Backups.Run(ctx, plan, jobInput{d: d, run: run}, out)
		out.Write([]byte("\n"))
		if !plan {
			d.forgetFetched()
		}
		// Erst die Sperre frei und das Ergebnis ins Protokoll, dann „fertig“
		// melden: Wer danach ins Protokoll schaut, findet es dort schon.
		l.Release()
		d.mu.Lock()
		run.closing = true
		run.job.Question = nil
		cancelled := run.job.Cancelled
		d.mu.Unlock()
		var text string
		switch {
		case cancelled:
			text = what + " abgebrochen; gestoppte Container laufen wieder"
		case code == backup.ExitBlocked:
			text = what + " nicht gestartet: die Vorprüfung hat etwas gefunden"
		case plan && code == backup.ExitOK:
			text = "Probelauf fertig: alles bereit für ein Backup"
		case code == backup.ExitOK:
			text = "Backup fertig: GÜLTIG"
		default:
			text = what + " fertig: UNGÜLTIG oder gescheitert, siehe Seite Backup"
		}
		d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: text, Addr: addr, Gate: gate})
		switch {
		case plan || cancelled || code == backup.ExitBlocked:
		case code == backup.ExitOK:
			_ = d.Events.Add(ueberwachung.Happening{Kind: ueberwachung.HapBackup, Level: ueberwachung.LevelOK, Title: "Backup von Hand erstellt", Text: "Geprüft und gültig; es wartet aufs Herunterladen.", Link: "#/backup"})
		default:
			_ = d.Events.Add(ueberwachung.Happening{Kind: ueberwachung.HapBackup, Level: ueberwachung.LevelErr, Title: "Backup gescheitert", Text: "Das Backup ist ungültig oder brach ab. Auf der Seite Backup steht, warum.", Link: "#/backup"})
		}
		now := d.now()
		d.mu.Lock()
		run.job.Running = false
		run.job.Done = &now
		run.job.Code = code
		d.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, d.backupReply(r.Context(), 0))
}

type answerRequest struct {
	ID     int    `json:"id"`
	Answer string `json:"antwort"`
}

func (d *Dienst) handleBackupAnswer(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	run := d.brun
	d.mu.Unlock()
	d.answerRun(w, r, run)
}

// answerRun gibt die Antwort aus dem Leitstand an den Lauf weiter.
func (d *Dienst) answerRun(w http.ResponseWriter, r *http.Request, run *backupRun) {
	var req answerRequest
	if !decode(w, r, &req) {
		return
	}
	d.mu.Lock()
	ok := run != nil && run.job.Running && run.job.Question != nil && run.job.Question.ID == req.ID
	d.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusConflict, apiError{Error: "Diese Frage ist schon beantwortet oder vorbei."})
		return
	}
	select {
	case run.answer <- strconv.Itoa(req.ID) + "\x00" + clip(req.Answer):
	default:
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *Dienst) handleBackupCancel(w http.ResponseWriter, r *http.Request) {
	gate, addr := visitor(r)
	d.mu.Lock()
	run := d.brun
	ok := run != nil && run.job.Running && !run.closing
	if ok {
		run.job.Cancelled = true
	}
	d.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusConflict, apiError{Error: "Es läuft gerade kein Backup."})
		return
	}
	d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: "Abbrechen angefordert", Addr: addr, Gate: gate})
	run.cancel()
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Herunterladen ----------

// DownloadTTL ist die Zeit, in der ein Download-Link angefangen werden
// muss. Fortsetzen geht danach weiter, solange die Anmeldung gilt.
const DownloadTTL = 2 * time.Minute

type downloadLink struct {
	session [32]byte // Fingerabdruck der Anmeldung
	until   time.Time
	used    bool
	// Was davon bei dir angekommen ist, für das Archiv mit etag; auch
	// über fortgesetzte Downloads hinweg.
	etag string
	got  spans
	done bool // vollständig, der Rest ist erledigt
}

// DownloadPrefix ist der Pfad, unter dem ein Backup zum Herunterladen liegt.
const DownloadPrefix = web.DownloadPath

func sessionHash(sid string) [32]byte { return sha256.Sum256([]byte(sid)) }

func (d *Dienst) handleDownloadLink(w http.ResponseWriter, r *http.Request) {
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}
	t := hex.EncodeToString(tok)
	now := d.now()
	d.mu.Lock()
	if d.downloads == nil {
		d.downloads = map[string]*downloadLink{}
	}
	for k, v := range d.downloads {
		if now.Sub(v.until) > 12*time.Hour {
			delete(d.downloads, k)
		}
	}
	if len(d.downloads) >= maxLinks {
		d.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, apiError{Error: "Zu viele offene Downloads. Bitte später noch einmal."})
		return
	}
	d.downloads[t] = &downloadLink{session: sessionHash(r.Header.Get(web.SessionHeader)), until: now.Add(DownloadTTL)}
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"pfad": DownloadPrefix + t})
}

// Downloading sagt, ob gerade jemand das Backup herunterlädt.
func (d *Dienst) Downloading() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.loading > 0
}

// handleDownload liefert das Backup als eine tar-Datei. Browser setzen
// einen abgebrochenen Download mit Range fort; If-Range mit dem ETag sorgt
// dafür, dass sie nur an ein unverändertes Backup anknüpfen.
func (d *Dienst) handleDownload(w http.ResponseWriter, r *http.Request) {
	gate, addr := visitor(r)
	t := r.PathValue("token")
	now := d.now()
	d.mu.Lock()
	link := d.downloads[t]
	ok := link != nil && link.session == sessionHash(r.Header.Get(web.SessionHeader)) && (link.used || now.Before(link.until))
	first := ok && !link.used
	if ok {
		link.used = true
	}
	d.mu.Unlock()
	if ok {
		d.touchFetched()
	}
	if !ok {
		http.Error(w, "Dieser Download-Link gilt nicht (mehr). Bitte im Leitstand neu auf Herunterladen klicken.", http.StatusNotFound)
		return
	}
	// Ein Download hält die Laufsperre nicht (N7): Das Archiv steht mit
	// seiner Dateiliste fest, und restic ändert vorhandene Dateien nicht.
	// Nur während eines Backups wartet er, weil der Stand dann halb ist;
	// und solange er läuft, gibt das Aufräumen nach einem Backup keinen
	// Platz frei (Downloading).
	if i, _ := lauf.CurrentIn(d.RunDir); i != nil && i.What == "Backup" {
		http.Error(w, "Gerade läuft ein Backup ("+i.Text()+"). Bitte danach noch einmal.", http.StatusConflict)
		return
	}
	d.mu.Lock()
	d.loading++
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.loading--
		d.mu.Unlock()
	}()
	a, name, err := d.Backups.Archive()
	if err != nil {
		if st := d.Backups.Stored(r.Context()); st.Runs == 0 {
			http.Error(w, "Auf dem Server liegt kein Backup (mehr). Steht kein Umzug an, löscht HostDeck es kurz nach dem vollständigen Download; ein angefangener Download lässt sich dann nicht mehr fortsetzen. Bei Bedarf im Leitstand unter Backup ein neues erstellen.", http.StatusGone)
			return
		}
		http.Error(w, "Das Backup lässt sich nicht packen: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer a.Close()
	if first {
		d.note(protokoll.Entry{Kind: protokoll.Download, Text: fmt.Sprintf("Herunterladen gestartet: %s (%s)", name, units.HumanSize(a.Size)), Addr: addr, Gate: gate})
	} else if rg := r.Header.Get("Range"); rg != "" {
		d.note(protokoll.Entry{Kind: protokoll.Download, Text: "Herunterladen fortgesetzt: " + name, Addr: addr, Gate: gate})
	}
	h := w.Header()
	h.Set("ETag", a.ETag)
	h.Set("Content-Type", "application/x-tar")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	cw := &countWriter{ResponseWriter: w}
	http.ServeContent(cw, r, name, time.Time{}, io.NewSectionReader(a, 0, a.Size))
	if r.Method != http.MethodGet {
		return
	}
	start, ok := servedFrom(cw.status, h.Get("Content-Range"))
	if !ok || cw.n == 0 {
		return
	}
	d.mu.Lock()
	if link.etag != a.ETag {
		link.etag, link.got, link.done = a.ETag, nil, false
	}
	link.got = link.got.add(start, start+cw.n)
	complete := !link.done && link.got.covers(a.Size)
	if complete {
		link.done = true
	}
	d.mu.Unlock()
	if !complete {
		return
	}
	d.fetchedComplete(a.ETag, name, addr, gate)
}

// countWriter zählt, was wirklich beim Browser ankam.
type countWriter struct {
	http.ResponseWriter
	status int
	n      int64
}

func (c *countWriter) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *countWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}

// servedFrom sagt, ab welchem Byte die Antwort das Archiv lieferte. Mehrere
// Bereiche auf einmal (multipart) zählen nicht; Browser fragen so nicht.
func servedFrom(status int, contentRange string) (int64, bool) {
	switch status {
	case http.StatusOK:
		return 0, true
	case http.StatusPartialContent:
		rest, ok := strings.CutPrefix(contentRange, "bytes ")
		if !ok {
			return 0, false
		}
		from, _, ok := strings.Cut(rest, "-")
		if !ok {
			return 0, false
		}
		n, err := strconv.ParseInt(from, 10, 64)
		return n, err == nil && n >= 0
	}
	return 0, false
}

// spans sind gelieferte Bereiche [von, bis), sortiert und ohne Überlappung.
type spans [][2]int64

func (s spans) add(from, to int64) spans {
	out := spans{}
	placed := false
	for _, x := range s {
		switch {
		case x[1] < from:
			out = append(out, x)
		case to < x[0]:
			if !placed {
				out = append(out, [2]int64{from, to})
				placed = true
			}
			out = append(out, x)
		default:
			from, to = min(from, x[0]), max(to, x[1])
		}
	}
	if !placed {
		out = append(out, [2]int64{from, to})
	}
	return out
}

func (s spans) covers(size int64) bool {
	return len(s) == 1 && s[0][0] == 0 && s[0][1] >= size
}

// handleBackupRemove löscht das Backup auf Knopfdruck vom Server.
func (d *Dienst) handleBackupRemove(w http.ResponseWriter, r *http.Request) {
	if why := d.copyBlocked("Backups"); why != "" {
		writeJSON(w, http.StatusConflict, apiError{Error: why})
		return
	}
	if d.Downloading() {
		writeJSON(w, http.StatusConflict, apiError{Error: "Gerade lädt jemand das Backup herunter. Bitte danach noch einmal."})
		return
	}
	gate, addr := visitor(r)
	d.withLock(w, "Backup vom Server löschen", func() {
		freed, err := d.Backups.Remove(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
			return
		}
		d.forgetFetched()
		d.note(protokoll.Entry{Kind: protokoll.Download, Text: fmt.Sprintf("Backup vom Server gelöscht, %s frei", units.HumanSize(freed)), Addr: addr, Gate: gate})
		writeJSON(w, http.StatusOK, d.backupReply(r.Context(), 0))
	})
}

// context ist der Rahmen des Dienstes für Arbeit nach einer Antwort.
func (d *Dienst) context() context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx == nil {
		return context.Background()
	}
	return d.ctx
}
