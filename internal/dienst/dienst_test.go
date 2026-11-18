package dienst

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/sitzung"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

type rig struct {
	d      *Dienst
	c      *Client
	cancel context.CancelFunc
	done   chan error
}

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hostdeckd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func start(t *testing.T, d *Dienst) *rig {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("SO_PEERCRED gibt es nur unter Linux")
	}
	dir := shortDir(t)
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if d.RunDir == "" {
		d.RunDir = filepath.Join(dir, "run")
	}
	d.defaults()
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{d: d, c: NewClient(sock), cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- d.Serve(ctx, ln) }()
	t.Cleanup(func() {
		// Verbindungen, die der Client auf Vorrat geöffnet und nie benutzt
		// hat, hält http.Server.Shutdown bis zu 5 s für aktiv. Darum erst
		// schließen und länger warten, als Shutdown höchstens braucht.
		r.c.tr.CloseIdleConnections()
		cancel()
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
			t.Error("Dienst endet nicht")
		}
	})
	return r
}

func me() int { return os.Getuid() }

// session legt eine Anmeldung per Link an, wie nach dem Notausgang.
func (r *rig) session(t *testing.T) string {
	t.Helper()
	id, err := r.d.Sessions.NewSession(sitzung.ViaLink, "Notausgang", "Notausgang")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEinmalLink(t *testing.T) {
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.Local)
	var mu sync.Mutex
	r := start(t, &Dienst{WebUID: me(), AdminUID: me(), Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }})
	ctx := context.Background()
	l, err := r.c.NewLink(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Token) != 32 || !l.Until.Equal(now.Add(LinkTTL)) {
		t.Fatalf("Link: %+v", l)
	}
	id, err := r.c.Redeem(ctx, l.Token, web.Visitor{Gate: "Notausgang", Addr: "Notausgang"})
	if err != nil || id == "" {
		t.Fatalf("erstes Einlösen: %v", err)
	}
	if info, err := r.d.Sessions.Check(id, false); err != nil || info.Via != sitzung.ViaLink {
		t.Fatalf("Anmeldung nach dem Link: %+v %v", info, err)
	}
	if _, err := r.c.Redeem(ctx, l.Token, web.Visitor{}); !errors.Is(err, ErrRejected) {
		t.Fatalf("zweites Einlösen: %v, erwartet abgelehnt", err)
	}
	l2, _ := r.c.NewLink(ctx)
	mu.Lock()
	now = now.Add(LinkTTL + time.Second)
	mu.Unlock()
	if _, err := r.c.Redeem(ctx, l2.Token, web.Visitor{}); !errors.Is(err, ErrRejected) {
		t.Fatalf("abgelaufener Link: %v", err)
	}
	if _, err := r.c.Redeem(ctx, strings.Repeat("0", 32), web.Visitor{}); !errors.Is(err, ErrRejected) {
		t.Fatalf("erfundener Link: %v", err)
	}
}

func TestFremdeBenutzerAbgewiesen(t *testing.T) {
	r := start(t, &Dienst{WebUID: me() + 4711, AdminUID: me() + 4712})
	_, err := r.c.Status(context.Background())
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusForbidden {
		t.Fatalf("fremder Benutzer: %v", err)
	}
}

func TestNurRootErzeugtLinks(t *testing.T) {
	r := start(t, &Dienst{WebUID: me(), AdminUID: me() + 4711})
	if _, err := r.c.NewLink(context.Background()); err == nil || !strings.Contains(err.Error(), "Nur root") {
		t.Fatalf("Website erzeugt Link: %v", err)
	}
	if _, err := r.c.Status(context.Background()); code(err) != http.StatusForbidden {
		t.Fatalf("Website fragt den Stand über /intern ab: %v", err)
	}
}

func TestBestandsaufnahmeUndSperre(t *testing.T) {
	release := make(chan struct{})
	var runs atomic.Int32
	d := &Dienst{WebUID: me(), AdminUID: me(), Discover: func(ctx context.Context) error {
		runs.Add(1)
		<-release
		return nil
	}}
	r := start(t, d)
	ctx := context.Background()
	sid := r.session(t)
	post := func() (int, string) {
		req, _ := http.NewRequest(http.MethodPost, "http://hostdeck-dienst/api/bestandsaufnahme", nil)
		req.Header.Set(web.SessionHeader, sid)
		req.Header.Set(web.GateHeader, "Notausgang")
		resp, err := r.c.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, b := post(); code != http.StatusAccepted {
		t.Fatalf("Start: %d %s", code, b)
	}
	st, _ := r.c.Status(ctx)
	if !st.Bestandsaufnahme.Running || st.Lauf == nil || st.Lauf.What != "Bestandsaufnahme" {
		t.Fatalf("Stand während des Laufs: %+v", st)
	}
	if code, b := post(); code != http.StatusConflict || !strings.Contains(b, "Bestandsaufnahme seit") {
		t.Fatalf("zweiter Start: %d %s", code, b)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _ = r.c.Status(ctx)
		if !st.Bestandsaufnahme.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Bestandsaufnahme endet nicht")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Lauf != nil || st.Bestandsaufnahme.Last == nil || st.Bestandsaufnahme.Error != "" {
		t.Fatalf("Stand danach: %+v", st)
	}
	// Ein Backup aus einem anderen Prozess sperrt die Bestandsaufnahme.
	l, err := lauf.TakeIn(d.RunDir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if code, b := post(); code != http.StatusConflict || !strings.Contains(b, "Backup seit") {
		t.Fatalf("während eines Backups: %d %s", code, b)
	}
	if runs.Load() != 1 {
		t.Fatalf("%d Bestandsaufnahmen, erwartet 1", runs.Load())
	}
}

func TestRettungNurOhneLaufendenLauf(t *testing.T) {
	dir := shortDir(t)
	var called atomic.Int32
	start(t, &Dienst{WebUID: me(), AdminUID: me(), RunDir: dir, Recover: func(context.Context) { called.Add(1) }})
	deadline := time.Now().Add(2 * time.Second)
	for called.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if called.Load() != 1 {
		t.Fatalf("Rettung %d-mal, erwartet 1", called.Load())
	}

	dir2 := shortDir(t)
	l, err := lauf.TakeIn(dir2, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	var called2 atomic.Int32
	start(t, &Dienst{WebUID: me(), AdminUID: me(), RunDir: dir2, Recover: func(context.Context) { called2.Add(1) }})
	time.Sleep(200 * time.Millisecond)
	if called2.Load() != 0 {
		t.Fatal("Rettung lief, obwohl ein Backup die Container gerade selbst steuert")
	}
}

func TestNaechsteNacht(t *testing.T) {
	at := 3*time.Hour + 30*time.Minute
	loc := time.Local
	cases := map[time.Time]time.Time{
		time.Date(2026, 11, 1, 2, 0, 0, 0, loc):   time.Date(2026, 11, 1, 3, 30, 0, 0, loc),
		time.Date(2026, 11, 1, 3, 30, 0, 0, loc):  time.Date(2026, 11, 2, 3, 30, 0, 0, loc),
		time.Date(2026, 11, 1, 15, 0, 0, 0, loc):  time.Date(2026, 11, 2, 3, 30, 0, 0, loc),
		time.Date(2026, 12, 31, 23, 0, 0, 0, loc): time.Date(2027, 1, 1, 3, 30, 0, 0, loc),
	}
	for now, want := range cases {
		if got := nextAt(now, at); !got.Equal(want) {
			t.Errorf("%v: %v, erwartet %v", now, got, want)
		}
	}
}

func TestWeiterleitungAnDaten(t *testing.T) {
	data := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"pfad":"`+r.URL.Path+`"}`)
	})
	r := start(t, &Dienst{WebUID: me(), AdminUID: me(), Data: data})
	ts := httptest.NewServer(r.c.Proxy())
	defer ts.Close()
	if resp, err := http.Get(ts.URL + "/api/state"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %v %v", resp, err)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/state", nil)
	req.Header.Set(web.SessionHeader, r.session(t))
	req.Header.Set(web.GateHeader, "Notausgang")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"/api/state"`) {
		t.Fatalf("Weiterleitung: %d %s", resp.StatusCode, b)
	}
	resp, err = http.Get(ts.URL + "/api/gibtsnicht")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unbekannter Auftrag: %d", resp.StatusCode)
	}
}
