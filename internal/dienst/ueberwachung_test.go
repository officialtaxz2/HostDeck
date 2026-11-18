package dienst

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/melden"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

type fakeMonitor struct {
	ran      atomic.Bool
	prefixes []string
}

func (f *fakeMonitor) Run(ctx context.Context) { f.ran.Store(true); <-ctx.Done() }
func (f *fakeMonitor) Snapshot() ueberwachung.Snapshot {
	return ueberwachung.Snapshot{Running: true, Interval: 10, Values: map[string]float64{"s/cpu": 12.5}, Cost: ueberwachung.Cost{CPU: 0.3, RAM: 30 << 20}}
}
func (f *fakeMonitor) LiveView(since int64, patterns []string) ueberwachung.LiveView {
	return ueberwachung.LiveView{Interval: 1, Frames: []ueberwachung.LiveFrame{{T: since + 1, V: map[string]float64{"s/cpu": 3}}}}
}
func (f *fakeMonitor) History(rng string, prefixes []string, points int, peaks bool) (ueberwachung.History, error) {
	if _, ok := ueberwachung.Ranges[rng]; !ok {
		return ueberwachung.History{}, errors.New("unbekannter Zeitraum " + rng)
	}
	f.prefixes = prefixes
	return ueberwachung.History{Step: 10, Times: []int64{1}, Series: map[string][]ueberwachung.Value{"s/cpu": {1.5}}}, nil
}

func TestUeberwachungImLeitstand(t *testing.T) {
	m := &fakeMonitor{}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Monitor = m })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/ueberwachung", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	if code, b := r.api(t, "GET", "/api/ueberwachung", sid, ""); code != 200 || !strings.Contains(b, `"laeuft":true`) || !strings.Contains(b, `"kosten":{"cpu":0.3`) {
		t.Fatalf("Stand: %d %s", code, b)
	}
	if code, b := r.api(t, "GET", "/api/ueberwachung/verlauf?zeitraum=1h&serien=s/,c/web/", sid, ""); code != 200 || !strings.Contains(b, `"s/cpu":[1.5]`) {
		t.Fatalf("Verlauf: %d %s", code, b)
	}
	if strings.Join(m.prefixes, "|") != "s/|c/web/" {
		t.Errorf("Filter: %v", m.prefixes)
	}
	if code, b := r.api(t, "GET", "/api/ueberwachung/live?seit=1700000000000", sid, ""); code != 200 || !strings.Contains(b, `"t":1700000000001`) || !strings.Contains(b, `"takt":1`) {
		t.Errorf("Live: %d %s", code, b)
	}
	if code, _ := r.api(t, "GET", "/api/ueberwachung/live", "", ""); code != http.StatusUnauthorized {
		t.Errorf("Live ohne Anmeldung: %d", code)
	}
	if code, _ := r.api(t, "GET", "/api/ueberwachung/verlauf?zeitraum=2h", sid, ""); code != http.StatusBadRequest {
		t.Errorf("unbekannter Zeitraum: %d", code)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !m.ran.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !m.ran.Load() {
		t.Error("Messung läuft nicht im Hintergrund")
	}
}

func TestNachrichtenUndGrenzen(t *testing.T) {
	dir := t.TempDir()
	// Kein Weg ins Netz: Der Herzschlag läuft im Hintergrund los.
	offline := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
	n := &melden.Service{File: filepath.Join(dir, "nachrichten.json"), Client: offline, Base: func() string { return "https://hostdeck.example.de" }}
	rules := &ueberwachung.Rules{File: filepath.Join(dir, "warnungen.json")}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Notify = n; d.Limits = rules })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, b := r.api(t, "GET", "/api/ueberwachung/nachrichten", sid, ""); code != 200 || !strings.Contains(b, `"moeglich":true`) || !strings.Contains(b, `"schluessel":"B`) {
		t.Fatalf("Stand: %d %s", code, b)
	}
	// Einrichten nur mit Passwort.
	if code, b := r.api(t, "POST", "/api/ueberwachung/nachrichten/herzschlag", sid, `{"adresse":"https://hc-ping.com/abc"}`); code != http.StatusForbidden || !strings.Contains(b, "bestaetigen") {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	// Grenzen ohne Passwort, aber im Protokoll.
	if code, b := r.api(t, "POST", "/api/ueberwachung/grenzen", sid, `{"grenzen":{"platte_gelb":0.85,"platte_rot":0.95}}`); code != 200 || !strings.Contains(b, "Platte gelb: 80 % belegt → 85 % belegt") {
		t.Fatalf("Grenzen: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/ueberwachung/grenzen", sid, `{"grenzen":{"platte_gelb":0.3}}`); code != http.StatusBadRequest || !strings.Contains(b, "Platte gelb") {
		t.Fatalf("Unsinn: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, b := r.api(t, "POST", "/api/ueberwachung/nachrichten/herzschlag", sid, `{"adresse":"https://hc-ping.com/abc"}`); code != 200 || !strings.Contains(b, `"herzschlag":{"adresse":"https://hc-ping.com/abc"`) {
		t.Fatalf("Herzschlag: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/ueberwachung/nachrichten/geraet", sid, `{"abo":{"endpoint":"https://evil.example/x","keys":{"p256dh":"x","auth":"y"}},"name":"iPhone"}`); code != http.StatusBadRequest || !strings.Contains(b, "nicht bekannt") {
		t.Fatalf("fremde Push-Adresse: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/ueberwachung/nachrichten/probe", sid, `{}`); code != http.StatusBadRequest || !strings.Contains(b, "kein Gerät") {
		t.Fatalf("Probe ohne Ziel: %d %s", code, b)
	}
	es, err := r.d.Journal.Last(100)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range es {
		texts = append(texts, e.Text+" "+strings.Join(e.Details, "; "))
	}
	all := strings.Join(texts, " | ")
	if !strings.Contains(all, "Grenzen der Warnungen geändert Platte gelb: 80 % belegt → 85 % belegt") || !strings.Contains(all, "Herzschlag eingerichtet") {
		t.Errorf("Protokoll: %s", all)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
