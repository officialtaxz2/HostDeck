package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/restic"
	"github.com/officialtaxz2/hostdeck/internal/runlog"
)

// scriptPrompt beantwortet Fragen der Reihe nach und merkt sich alles,
// was es zeigt.
type scriptPrompt struct {
	answers []string
	out     strings.Builder
}

func (p *scriptPrompt) Line(q string) (string, error) {
	p.out.WriteString(q)
	if len(p.answers) == 0 {
		return "", io.EOF
	}
	a := p.answers[0]
	p.answers = p.answers[1:]
	return a, nil
}

func (p *scriptPrompt) Secret(q string) (string, error) { return p.Line(q) }
func (p *scriptPrompt) Say(f string, args ...any)       { fmt.Fprintf(&p.out, f, args...) }
func (p *scriptPrompt) Interactive() bool               { return true }

func TestBackupPasswortDialog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".hostdeck")
	repo := filepath.Join(t.TempDir(), "backup")
	b := &serverBackups{State: dir, Repo: repo}
	if p := b.problem(); !strings.Contains(p, "noch kein Backup-Passwort") || !strings.Contains(p, "sudo hostdeck backup-passwort") {
		t.Fatalf("ohne Passwort: %q", p)
	}

	// Neu: Es zeigt das Passwort und will ein „ja“.
	p := &scriptPrompt{answers: []string{"", "vielleicht", "ja"}}
	if err := backupPasswordDialog(p, dir, repo, false); err != nil {
		t.Fatal(err)
	}
	pw, err := os.ReadFile(backupPasswordFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimSpace(string(pw))
	if len(secret) < 20 || !strings.Contains(p.out.String(), secret) || !strings.Contains(p.out.String(), "Neues Backup-Passwort") {
		t.Fatalf("Ausgabe:\n%s", p.out.String())
	}
	if fi, _ := os.Stat(backupPasswordFile(dir)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("Rechte %v", fi.Mode().Perm())
	}
	if b.problem() != "" {
		t.Fatalf("nach dem Bestätigen: %q", b.problem())
	}

	// Noch einmal: zeigt es nur auf Wunsch.
	p = &scriptPrompt{}
	if err := backupPasswordDialog(p, dir, repo, false); err != nil || strings.Contains(p.out.String(), secret) || !strings.Contains(p.out.String(), "--zeigen") {
		t.Fatalf("%v\n%s", err, p.out.String())
	}
	p = &scriptPrompt{answers: []string{"ja"}}
	if err := backupPasswordDialog(p, dir, repo, true); err != nil || !strings.Contains(p.out.String(), secret) {
		t.Fatalf("zeigen: %v\n%s", err, p.out.String())
	}
	got, _ := os.ReadFile(backupPasswordFile(dir))
	if string(got) != string(pw) {
		t.Fatal("Zeigen hat das Passwort geändert")
	}

	// Abgebrochen, bevor es notiert ist: Das Backup wartet.
	dir2 := filepath.Join(t.TempDir(), ".hostdeck")
	if err := backupPasswordDialog(&scriptPrompt{}, dir2, repo, false); !errors.Is(err, io.EOF) {
		t.Fatalf("abgebrochen: %v", err)
	}
	if p := (&serverBackups{State: dir2, Repo: repo}).problem(); !strings.Contains(p, "nicht bestätigt") {
		t.Fatalf("unbestätigt: %q", p)
	}

	// Liegen schon Backups da, erfindet HostDeck kein neues Passwort.
	os.MkdirAll(repo, 0o700)
	os.WriteFile(filepath.Join(repo, "config"), []byte("x"), 0o600)
	dir3 := filepath.Join(t.TempDir(), ".hostdeck")
	if err := backupPasswordDialog(&scriptPrompt{}, dir3, repo, false); err == nil || !strings.Contains(err.Error(), "Passwort dazu fehlt") {
		t.Fatalf("Ordner ohne Passwort: %v", err)
	}
	if _, err := os.Stat(backupPasswordFile(dir3)); err == nil {
		t.Fatal("neues Passwort für einen Ordner mit Backups")
	}
}

func TestPlatzVorDemBackup(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "backup")
	free := int64(10 << 30)
	b := &serverBackups{Repo: repo, Free: func(p string) (int64, error) {
		if p != filepath.Dir(repo) {
			return 0, fmt.Errorf("falscher Ordner %s", p)
		}
		return free, nil
	}}
	if err := b.checkSpace(7 << 30); err != nil {
		t.Fatalf("7 GB bei 10 GB frei: %v", err)
	}
	err := b.checkSpace(9 << 30)
	if err == nil || !strings.Contains(err.Error(), "zu wenig Platz") || !strings.Contains(err.Error(), "frei bleiben") {
		t.Fatalf("9 GB bei 10 GB frei: %v", err)
	}
	// Was schon im Ordner liegt, kommt nicht noch einmal dazu.
	os.MkdirAll(repo, 0o700)
	os.WriteFile(filepath.Join(repo, "pack"), bytes.Repeat([]byte("x"), 4<<20), 0o600)
	free = 3<<30 + 1<<20
	if err := b.checkSpace(1<<30 + 4<<20); err != nil {
		t.Fatalf("mit Ordner: %v", err)
	}
	// Nicht messbar: restic meldet volle Platten selbst.
	b.Free = func(string) (int64, error) { return 0, errors.New("geht nicht") }
	if err := b.checkSpace(1 << 50); err != nil {
		t.Fatal(err)
	}
}

func TestAlteLaeufeAufraeumen(t *testing.T) {
	snap := func(id, run string) restic.Snapshot {
		return restic.Snapshot{ID: id, Tags: []string{"hostdeck", "sbm-run=" + run}}
	}
	snaps := []restic.Snapshot{
		snap("a1", "20261101-100000"), snap("a2", "20261101-100000"),
		snap("b1", "20261101-110000"), // ungültig, älter als der älteste behaltene
		snap("c1", "20261101-120000"),
		snap("d1", "20261101-130000"), // ungültig, neuer: bleibt
		snap("e1", "20261101-140000"),
		{ID: "x1", Tags: []string{"fremd"}},
	}
	runs := []runlog.Entry{{Run: "20261101-140000", Valid: true}, {Run: "20261101-120000", Valid: true}, {Run: "20261101-100000", Valid: true}}
	ids, gone := outdated(snaps, runs)
	if strings.Join(ids, ",") != "a1,a2,b1" || gone != 2 {
		t.Fatalf("weg: %v (%d Läufe)", ids, gone)
	}
	if ids, _ := outdated(snaps, runs[:1]); strings.Join(ids, ",") != "a1,a2,b1,c1,d1" {
		t.Fatalf("ein Lauf: %v", ids)
	}
	if ids, _ := outdated(snaps, nil); ids != nil {
		t.Fatalf("ohne Lauf: %v", ids)
	}
}

func TestBackupOrdnerZumHerunterladen(t *testing.T) {
	root := t.TempDir()
	b := &serverBackups{Repo: filepath.Join(root, "backup"), Result: filepath.Join(root, "ergebnis"), State: filepath.Join(root, ".hostdeck"), Host: "taxz vps",
		Free: func(string) (int64, error) { return 50 << 30, nil }}
	if _, _, err := b.Archive(); err == nil || !strings.Contains(err.Error(), "noch kein Backup") {
		t.Fatalf("leer: %v", err)
	}
	st := b.Stored(context.Background())
	if st.Ready || st.Runs != 0 || st.Last != nil || st.Keep != keepRuns || st.Free != 50<<30 {
		t.Fatalf("leer: %+v", st)
	}
	os.MkdirAll(filepath.Join(b.Repo, "locks"), 0o700)
	os.MkdirAll(b.Result, 0o700)
	os.WriteFile(filepath.Join(b.Repo, "config"), []byte("k"), 0o600)
	os.WriteFile(filepath.Join(b.Repo, "locks", "l"), []byte("l"), 0o600)
	started := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)
	for _, e := range []runlog.Entry{
		{Run: "pc", Started: started.Add(time.Hour), Valid: true},
		{Run: "s1", Started: started.Add(-48 * time.Hour), Valid: true, Target: targetServer},
		{Run: "s2", Started: started, Valid: true, Target: targetServer},
		{Run: "s3", Started: started.Add(time.Minute), Valid: false, Target: targetServer},
	} {
		if err := runlog.Append(b.Result, e); err != nil {
			t.Fatal(err)
		}
	}
	a, name, err := b.Archive()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if name != "hostdeck-backup-taxz-vps-2026-11-02.tar" {
		t.Fatalf("Name %q", name)
	}
	body, _ := io.ReadAll(io.NewSectionReader(a, 0, a.Size))
	if !bytes.Contains(body, []byte("LIESMICH.txt")) || bytes.Contains(body, []byte("hostdeck-backup/locks/l")) {
		t.Fatal("Inhalt falsch")
	}
	b.forgetCache()
	st = b.Stored(context.Background())
	if st.Runs != 2 || st.Last == nil || !st.Last.Equal(started) || st.Size == 0 {
		t.Fatalf("mit Läufen: %+v", st)
	}
}

func TestEinrichtenFragtNachAdresse(t *testing.T) {
	r := newPubRig(t)
	ctx := context.Background()
	var out, errOut bytes.Buffer
	// Erst eine Adresse ohne DNS-Eintrag, dann Enter für den Vorschlag.
	p := &scriptPrompt{answers: []string{"nix.example.de", ""}}
	offerPublish(ctx, r.p, p, strings.NewReader("ja\n"), &out, &errOut)
	if !strings.Contains(p.out.String(), "Enter = hostdeck.example.de") || !strings.Contains(p.out.String(), "Noch einmal versuchen") {
		t.Fatalf("Fragen:\n%s", p.out.String())
	}
	if !strings.Contains(out.String(), "erreichbar unter https://hostdeck.example.de") {
		t.Fatalf("Ausgabe:\n%s\n%s", out.String(), errOut.String())
	}
	if st := r.p.State(ctx); st.Domain != "hostdeck.example.de" {
		t.Fatalf("nicht freigeschaltet: %+v", st)
	}
	// Ist schon freigeschaltet, fragt es nicht mehr.
	p = &scriptPrompt{}
	offerPublish(ctx, r.p, p, strings.NewReader(""), &out, &errOut)
	if p.out.Len() != 0 {
		t.Fatalf("fragt trotzdem:\n%s", p.out.String())
	}

	// „n“ heißt später; nichts ändert sich.
	r2 := newPubRig(t)
	p = &scriptPrompt{answers: []string{"n"}}
	offerPublish(ctx, r2.p, p, strings.NewReader(""), &out, &errOut)
	r2.nothingChanged(t)
}

// Aufräumen mit echtem restic: Die letzten beiden gültigen Läufe bleiben.
func TestAufraeumenMitRestic(t *testing.T) {
	bin := os.Getenv("HOSTDECK_RESTIC")
	if bin == "" {
		t.Skip("restic nicht gefunden (HOSTDECK_RESTIC setzen)")
	}
	ctx := context.Background()
	root := t.TempDir()
	b := &serverBackups{Repo: filepath.Join(root, "backup"), Result: filepath.Join(root, "ergebnis"), State: filepath.Join(root, ".hostdeck"), Restic: bin}
	os.MkdirAll(b.Result, 0o700)
	os.MkdirAll(b.State, 0o700)
	os.WriteFile(backupPasswordFile(b.State), []byte("test-passwort-lang-genug\n"), 0o600)
	repo := &restic.Repo{Bin: bin, URL: b.Repo, PasswordFile: backupPasswordFile(b.State), CacheDir: filepath.Join(b.State, "cache"), Host: "test"}
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "daten")
	os.MkdirAll(data, 0o700)
	start := time.Date(2026, 11, 1, 10, 0, 0, 0, time.Local)
	for i, valid := range []bool{true, true, false, true} {
		run := start.Add(time.Duration(i) * time.Hour).Format("20060102-150405")
		os.WriteFile(filepath.Join(data, "datei"), bytes.Repeat([]byte{byte('a' + i)}, 200000), 0o600)
		if _, err := repo.Backup(ctx, restic.BackupArgs{Paths: []string{data}, Tags: []string{"hostdeck", "sbm-run=" + run}}); err != nil {
			t.Fatal(err)
		}
		runlog.Append(b.Result, runlog.Entry{Run: run, Started: start.Add(time.Duration(i) * time.Hour), Valid: valid, Target: targetServer})
	}
	var out bytes.Buffer
	// Lädt gerade jemand herunter, bleibt alles (N7).
	b.Downloading = func() bool { return true }
	b.cleanup(ctx, &out)
	if s, _ := repo.Snapshots(ctx); len(s) != 4 || !strings.Contains(out.String(), "Aufräumen übersprungen") {
		t.Fatalf("trotz Download aufgeräumt: %d\n%s", len(s), out.String())
	}
	b.Downloading = nil
	out.Reset()
	b.cleanup(ctx, &out)
	snaps, err := repo.Snapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, s := range snaps {
		for _, tag := range s.Tags {
			if v, ok := strings.CutPrefix(tag, "sbm-run="); ok {
				left = append(left, v[9:11])
			}
		}
	}
	// Läufe um 10, 11 (gültig), 12 (ungültig), 13 (gültig): 11 und 13
	// bleiben, dazwischen auch 12.
	if strings.Join(left, ",") != "11,12,13" || !strings.Contains(out.String(), "1 älterer Lauf kommt weg") || !strings.Contains(out.String(), "freigegeben") {
		t.Fatalf("übrig: %v\n%s", left, out.String())
	}

	// Die heruntergeladene Datei ist ein vollständiges restic-Repository.
	a, _, err := b.Archive()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	dst := t.TempDir()
	tr := tar.NewReader(io.NewSectionReader(a, 0, a.Size))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dst, filepath.FromSlash(h.Name))
		if h.Typeflag == tar.TypeDir {
			os.MkdirAll(p, 0o700)
			continue
		}
		body, _ := io.ReadAll(tr)
		if err := os.WriteFile(p, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dl := &restic.Repo{Bin: bin, URL: filepath.Join(dst, "hostdeck-backup"), PasswordFile: backupPasswordFile(b.State), CacheDir: t.TempDir()}
	if err := dl.Check(ctx, "1/1"); err != nil {
		t.Fatalf("Kopie kaputt: %v", err)
	}
	if got, err := dl.Snapshots(ctx); err != nil || len(got) != 3 {
		t.Fatalf("Kopie: %d Snapshots, %v", len(got), err)
	}
}
