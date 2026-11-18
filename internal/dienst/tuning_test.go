package dienst

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
	"github.com/officialtaxz2/hostdeck/internal/tuning/tuningtest"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

func tuneState(t *testing.T, r *rig, sid string) TuneReply {
	t.Helper()
	code, b := r.api(t, "GET", "/api/tuning", sid, "")
	if code != 200 {
		t.Fatalf("Stand: %d %s", code, b)
	}
	var rep TuneReply
	if err := json.Unmarshal([]byte(b), &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

func waitTune(t *testing.T, r *rig, sid string) TuneReply {
	t.Helper()
	for i := 0; i < 500; i++ {
		if rep := tuneState(t, r, sid); rep.Job != nil && !rep.Job.Running {
			return rep
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Tuning wird nicht fertig")
	return TuneReply{}
}

// Der ganze Weg im Leitstand mit dem echten tune.sh auf einem
// nachgebauten Server.
func TestTuningImLeitstand(t *testing.T) {
	g := tuningtest.New(t)
	var reboots atomic.Int32
	g.S.RebootCmd = func(context.Context) error { reboots.Add(1); return nil }
	old := RebootDelay
	RebootDelay = 0
	t.Cleanup(func() { RebootDelay = old })
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Tuning = g.S })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/tuning", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	rep := tuneState(t, r, sid)
	if rep.Status == nil || rep.Status.Applied != nil || rep.Status.Reboot || len(rep.Presets) != 3 || len(rep.Switches) != 13 {
		t.Fatalf("Anfang: %+v", rep)
	}
	// Neustart nur, wenn etwas wartet.
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, b := r.api(t, "POST", "/api/tuning/neustart", sid, ""); code != http.StatusConflict || !strings.Contains(b, "wartet nichts") {
		t.Fatalf("Neustart ohne Grund: %d %s", code, b)
	}

	// Vorschau ohne Bestätigung, ändert nichts.
	code, b := r.api(t, "POST", "/api/tuning/vorschau", sid, `{"preset":"mixed","schalter":{"zram":"off"}}`)
	if code != 200 || !strings.Contains(b, `"riskant":["mitigations=off","init_on_alloc=0","randomize_kstack_offset=off"]`) || !strings.Contains(b, "ACHTUNG: ") {
		t.Fatalf("Vorschau: %d %s", code, b)
	}
	if code, _ := r.api(t, "POST", "/api/tuning/vorschau", sid, `{"preset":"turbo"}`); code != http.StatusBadRequest {
		t.Fatalf("Preset turbo: %d", code)
	}
	if g.Read("var/lib/vps-tune/applied.conf") != "" {
		t.Fatal("Vorschau hat angewendet")
	}

	// Anwenden ohne „verstanden“ geht bei mitigations=off nicht.
	if code, b := r.api(t, "POST", "/api/tuning/anwenden", sid, `{"preset":"mixed"}`); code != http.StatusConflict || !strings.Contains(b, "Ich habe verstanden") {
		t.Fatalf("ohne verstanden: %d %s", code, b)
	}
	// Ein anderer Lauf hält die Sperre.
	other, err := lauf.TakeIn(r.d.RunDir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	if code, b := r.api(t, "POST", "/api/tuning/anwenden", sid, `{"preset":"mixed","verstanden":true}`); code != http.StatusConflict || !strings.Contains(b, "Backup") {
		t.Fatalf("während Backup: %d %s", code, b)
	}
	other.Release()
	if code, b := r.api(t, "POST", "/api/tuning/anwenden", sid, `{"preset":"mixed","verstanden":true}`); code != http.StatusAccepted {
		t.Fatalf("Anwenden: %d %s", code, b)
	}
	rep = waitTune(t, r, sid)
	if rep.Job.Code != 0 || rep.Job.Kind != "anwenden" || !strings.Contains(strings.Join(rep.Job.Lines, "\n"), "GRUB aktualisiert") {
		t.Fatalf("nach Anwenden: %+v", rep.Job)
	}
	if rep.Status == nil || rep.Status.Applied == nil || rep.Status.Applied.Preset != "mixed" || !rep.Status.Reboot {
		t.Fatalf("Stand nach Anwenden: %+v", rep.Status)
	}
	if i, _ := lauf.CurrentIn(r.d.RunDir); i != nil {
		t.Fatalf("Sperre noch gehalten: %+v", i)
	}

	// CPU-Vorrang.
	code, b = r.api(t, "GET", "/api/tuning/dienste", sid, "")
	if code != 200 || !strings.Contains(b, `"name":"docker.service"`) {
		t.Fatalf("Dienste: %d %s", code, b)
	}
	if code, _ := r.api(t, "POST", "/api/tuning/vorrang", sid, `{"dienst":"docker; reboot","stufe":"high"}`); code != http.StatusBadRequest {
		t.Fatalf("falscher Dienst: %d", code)
	}
	code, b = r.api(t, "POST", "/api/tuning/vorrang", sid, `{"dienst":"docker.service","stufe":"high"}`)
	if code != 200 || !strings.Contains(b, `"stufe":"high"`) || !strings.Contains(b, "CPUWeight=1000") {
		t.Fatalf("Vorrang: %d %s", code, b)
	}
	// „normal“ löscht das Drop-in ohne Sicherung, darum nur
	// für Dienste aus prio.list.
	g.Write("etc/systemd/system.control/ssh.service.d/50-CPUWeight.conf", "[Service]\nCPUWeight=500\n")
	if code, b := r.api(t, "POST", "/api/tuning/vorrang", sid, `{"dienst":"ssh","stufe":"normal"}`); code != http.StatusConflict || !strings.Contains(b, "nicht vps-tune") {
		t.Fatalf("normal für fremden Dienst: %d %s", code, b)
	}
	if g.Read("etc/systemd/system.control/ssh.service.d/50-CPUWeight.conf") == "" {
		t.Fatal("fremdes Drop-in gelöscht")
	}

	// Neustart: angestoßen, die Sperre bleibt bis dahin.
	if code, b := r.api(t, "POST", "/api/tuning/neustart", sid, ""); code != http.StatusAccepted {
		t.Fatalf("Neustart: %d %s", code, b)
	}
	for i := 0; i < 200 && reboots.Load() == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if reboots.Load() != 1 {
		t.Fatal("kein Neustart")
	}
	if i, _ := lauf.CurrentIn(r.d.RunDir); i == nil || i.What != "Neustart des Servers" {
		t.Fatalf("Sperre beim Neustart: %+v", i)
	}
	if rep := tuneState(t, r, sid); rep.Reboot == nil {
		t.Fatal("Neustart nicht im Stand")
	}
	// Der Server ist wieder da (neuer Dienst, /run leer).
	r.d.mu.Lock()
	r.d.treboot = nil
	r.d.tlock.Release()
	r.d.mu.Unlock()
	g.Rebooted()
	// Gelesener Stand gilt ein paar Sekunden.
	r.d.tuneStale()
	if rep := tuneState(t, r, sid); rep.Status.Reboot {
		t.Fatalf("nach Neustart: %+v", rep.Status)
	}

	// Zurücknehmen.
	code, b = r.api(t, "POST", "/api/tuning/zuruecknehmen/vorschau", sid, "")
	if code != 200 || !strings.Contains(b, "docker.service: CPUWeight zurück") {
		t.Fatalf("restore-Vorschau: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/tuning/zuruecknehmen", sid, ""); code != http.StatusAccepted {
		t.Fatalf("Zurücknehmen: %d %s", code, b)
	}
	rep = waitTune(t, r, sid)
	if rep.Job.Code != 0 || rep.Status.Applied != nil || !rep.Status.Reboot {
		t.Fatalf("nach Zurücknehmen: %+v %+v", rep.Job, rep.Status)
	}

	es, _ := r.d.Journal.Last(50)
	var texts []string
	for _, e := range es {
		texts = append(texts, e.Text)
	}
	all := strings.Join(texts, "|")
	for _, want := range []string{"Tuning anwenden: mixed", "Tuning angewendet (mixed). Die Bootparameter wirken nach einem Neustart.",
		"CPU-Vorrang für docker.service: hoch", "Server-Neustart aus dem Leitstand: Diese Bootparameter wirken erst nach einem Neustart", "Tuning zurückgenommen. Für die Bootparameter ist ein Neustart nötig."} {
		if !strings.Contains(all, want) {
			t.Errorf("Protokoll ohne %q: %s", want, all)
		}
	}
}

// Anwenden, Zurücknehmen, Vorrang und Neustart brauchen das Passwort.
func TestTuningBrauchtBestaetigung(t *testing.T) {
	g := tuningtest.New(t)
	g.S.RebootCmd = func(context.Context) error { return errors.New("nein") }
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Tuning = g.S })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/tuning/anwenden", "/api/tuning/zuruecknehmen", "/api/tuning/vorrang", "/api/tuning/neustart"} {
		if code, b := r.api(t, "POST", p, sid, `{"preset":"safe"}`); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
			t.Errorf("%s ohne Bestätigung: %d %s", p, code, b)
		}
	}
	if g.Read("var/lib/vps-tune/applied.conf") != "" {
		t.Fatal("angewendet")
	}
}

// Nach dem Umzug bietet der Leitstand das Tuning des alten
// Servers an, ohne Netzwerkkarte; nach dem Anwenden steht „gleich“, und
// Verwerfen nimmt das Angebot weg.
func TestTuningVomAltenServer(t *testing.T) {
	g := tuningtest.New(t)
	old := &tuning.Applied{Preset: "mixed", Values: map[string]string{"preempt": "full", "zram": "on", "iface": "eth9"}, Boot: []string{"mitigations=off"}}
	prio := []tuning.PrioSet{{Unit: "docker.service", Level: "high"}}
	dropped := false
	r, _ := doorRigWith(t, nil, func(d *Dienst) {
		d.Tuning = g.S
		d.OldTuning = func() (string, *tuning.Applied, []tuning.PrioSet) {
			if dropped {
				return "", nil, nil
			}
			return "vps1", old, prio
		}
		d.DropOldTuning = func() error { dropped = true; return nil }
	})
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	rep := tuneState(t, r, sid)
	o := rep.Old
	if o == nil || o.From != "vps1" || o.Same || o.Offer == nil || o.Offer.Preset != "mixed" ||
		!reflect.DeepEqual(o.Offer.Set, map[string]string{"zram": "on"}) || len(o.Prio) != 1 {
		t.Fatalf("Angebot: %+v", o)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, b := r.api(t, "POST", "/api/tuning/anwenden", sid, `{"preset":"mixed","schalter":{"zram":"on"},"verstanden":true}`); code != http.StatusAccepted {
		t.Fatalf("Anwenden: %d %s", code, b)
	}
	waitTune(t, r, sid)
	r.d.tuneStale()
	if rep := tuneState(t, r, sid); rep.Old == nil || !rep.Old.Same {
		t.Fatalf("nach Anwenden: %+v %+v", rep.Old, rep.Status.Applied)
	}
	code, b := r.api(t, "POST", "/api/tuning/alter-server/verwerfen", sid, "")
	if code != 200 || strings.Contains(b, "alter_server") || !dropped {
		t.Fatalf("Verwerfen: %d %s", code, b)
	}
	es, _ := r.d.Journal.Last(20)
	found := false
	for _, e := range es {
		found = found || e.Text == "Tuning vom alten Server nicht übernommen."
	}
	if !found {
		t.Error("Verwerfen nicht im Protokoll")
	}
}

// Startet der Server trotz erfolgreichem systemctl reboot
// nicht neu, gibt HostDeck die Sperre nach der Frist frei.
func TestNeustartKommtNicht(t *testing.T) {
	g := tuningtest.New(t)
	g.S.RebootCmd = func(context.Context) error { return nil }
	oldD, oldG := RebootDelay, RebootGiveUp
	RebootDelay, RebootGiveUp = 0, 50*time.Millisecond
	t.Cleanup(func() { RebootDelay, RebootGiveUp = oldD, oldG })
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Tuning = g.S })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, b := r.api(t, "POST", "/api/tuning/anwenden", sid, `{"preset":"mixed","verstanden":true}`); code != http.StatusAccepted {
		t.Fatalf("Anwenden: %d %s", code, b)
	}
	waitTune(t, r, sid)
	if code, b := r.api(t, "POST", "/api/tuning/neustart", sid, ""); code != http.StatusAccepted {
		t.Fatalf("Neustart: %d %s", code, b)
	}
	given := func() {
		t.Helper()
		for i := 0; i < 200; i++ {
			if rep := tuneState(t, r, sid); rep.Reboot != nil && rep.Reboot.Error != "" {
				if !strings.Contains(rep.Reboot.Error, "nicht neu gestartet") {
					t.Fatalf("Meldung: %q", rep.Reboot.Error)
				}
				if i, _ := lauf.CurrentIn(r.d.RunDir); i != nil {
					t.Fatalf("Sperre noch gehalten: %+v", i)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("Sperre bleibt ohne Neustart stehen")
	}
	given()
	// Noch einmal geht.
	if code, b := r.api(t, "POST", "/api/tuning/neustart", sid, ""); code != http.StatusAccepted {
		t.Fatalf("zweiter Neustart: %d %s", code, b)
	}
	given()
}
