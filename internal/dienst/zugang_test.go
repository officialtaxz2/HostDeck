package dienst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/anmeldung"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

// fakeAccess schaltet zum Schein frei; Publish wartet auf release.
type fakeAccess struct {
	mu      sync.Mutex
	domain  string
	release chan error
}

func (f *fakeAccess) State(context.Context) AccessState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return AccessState{Caddy: true, Domain: f.domain, Suggest: "hostdeck.example.de"}
}

func (f *fakeAccess) Domain() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.domain
}

func (f *fakeAccess) Check(_ context.Context, d string) (AccessPlan, error) {
	if d != "hostdeck.example.de" {
		return AccessPlan{}, errors.New("Für " + d + " gibt es noch keinen DNS-Eintrag.")
	}
	return AccessPlan{Domain: d, IPs: []string{"203.0.113.10"}, Pages: 13}, nil
}

func (f *fakeAccess) Publish(_ context.Context, d string, step func(Step)) error {
	step(Step{Name: "Adresse prüfen", State: "ok"})
	step(Step{Name: "Caddy prüft die Dateien", State: "laeuft"})
	err := <-f.release
	if err != nil {
		step(Step{Name: "Caddy prüft die Dateien", State: "fehler", Detail: err.Error()})
		return err
	}
	step(Step{Name: "Caddy prüft die Dateien", State: "ok"})
	f.mu.Lock()
	f.domain = d
	f.mu.Unlock()
	return nil
}

func (f *fakeAccess) Unpublish(context.Context, func(Step)) error {
	f.mu.Lock()
	f.domain = ""
	f.mu.Unlock()
	return nil
}

func waitJob(t *testing.T, r *rig, sid string) AccessReply {
	t.Helper()
	for i := 0; i < 200; i++ {
		_, b := r.api(t, "GET", "/api/zugang", sid, "")
		var rep AccessReply
		if err := json.Unmarshal([]byte(b), &rep); err != nil {
			t.Fatal(err)
		}
		if rep.Job != nil && !rep.Job.Running {
			return rep
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Auftrag endet nicht")
	return AccessReply{}
}

func TestZugangVonAussen(t *testing.T) {
	fa := &fakeAccess{release: make(chan error, 1)}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Access = fa })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/zugang", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	sid, _ := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Notausgang", Addr: "Notausgang"})
	if code, b := r.api(t, "GET", "/api/zugang", sid, ""); code != 200 || !strings.Contains(b, `"vorschlag":"hostdeck.example.de"`) {
		t.Fatalf("Stand: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/zugang/pruefen", sid, `{"adresse":"neu.example.de"}`); code != http.StatusUnprocessableEntity || !strings.Contains(b, "DNS-Eintrag") {
		t.Fatalf("Prüfen: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/zugang/pruefen", sid, `{"adresse":"hostdeck.example.de"}`); code != 200 || !strings.Contains(b, `"seiten":13`) {
		t.Fatalf("Prüfen: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/zugang/freischalten", sid, `{"adresse":"hostdeck.example.de"}`); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, b := r.api(t, "POST", "/api/zugang/freischalten", sid, `{"adresse":"neu.example.de"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("falsche Adresse: %d %s", code, b)
	}
	code, b := r.api(t, "POST", "/api/zugang/freischalten", sid, `{"adresse":"hostdeck.example.de"}`)
	if code != http.StatusAccepted || !strings.Contains(b, `"laeuft":true`) {
		t.Fatalf("Freischalten: %d %s", code, b)
	}
	// Während des Auftrags gilt die Laufsperre.
	if code, b := r.api(t, "POST", "/api/zugang/freischalten", sid, `{"adresse":"hostdeck.example.de"}`); code != http.StatusConflict || !strings.Contains(b, "Freischalten") {
		t.Fatalf("zweites Freischalten: %d %s", code, b)
	}
	if code, b := r.api(t, "GET", "/api/lauf", sid, ""); code != 200 || !strings.Contains(b, `"Freischalten"`) || !strings.Contains(b, `"zugang":{"laeuft":true}`) {
		t.Fatalf("Lauf: %d %s", code, b)
	}
	fa.release <- nil
	rep := waitJob(t, r, sid)
	if rep.Job.Error != "" || rep.State.Domain != "hostdeck.example.de" || len(rep.Job.Steps) != 2 || rep.Job.Steps[1].State != "ok" || rep.Job.Done == nil {
		t.Fatalf("Ergebnis: %+v", rep)
	}

	// Zurücknehmen, dann zweimal: nichts zurückzunehmen.
	if code, b := r.api(t, "POST", "/api/zugang/zuruecknehmen", sid, ""); code != http.StatusAccepted {
		t.Fatalf("Zurücknehmen: %d %s", code, b)
	}
	if rep := waitJob(t, r, sid); rep.State.Domain != "" || rep.Job.Action != "zuruecknehmen" {
		t.Fatalf("nach dem Zurücknehmen: %+v", rep)
	}
	if code, _ := r.api(t, "POST", "/api/zugang/zuruecknehmen", sid, ""); code != http.StatusConflict {
		t.Fatalf("zweimal zurücknehmen: %d", code)
	}

	// Ein Fehler steht im Auftrag und im Protokoll.
	r.api(t, "POST", "/api/zugang/freischalten", sid, `{"adresse":"hostdeck.example.de"}`)
	fa.release <- errors.New("caddy validate: kaputt. Alles ist zurückgelegt, Caddy läuft wie vorher.")
	if rep := waitJob(t, r, sid); !strings.Contains(rep.Job.Error, "zurückgelegt") || rep.State.Domain != "" {
		t.Fatalf("Fehler: %+v", rep)
	}
	j := journal(t, r)
	for _, want := range []string{
		"zugang|Freischalten gestartet: hostdeck.example.de|203.0.113.9",
		"zugang|Freigeschaltet: https://hostdeck.example.de|203.0.113.9",
		"zugang|Zugang von außen zurückgenommen",
		"zugang|Freischalten von hostdeck.example.de ging nicht: caddy validate: kaputt.",
	} {
		if !strings.Contains(j, want) {
			t.Errorf("Protokoll ohne %q:\n%s", want, j)
		}
	}
}

func TestEinmalLinkNurAmNotausgang(t *testing.T) {
	r, _ := doorRig(t, nil)
	ctx := context.Background()
	l, err := r.c.NewLink(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.c.Door().Redeem(ctx, l.Token, web.Visitor{Gate: "hostdeck.example.de", Addr: "198.51.100.1"}); err == nil {
		t.Fatal("Einmal-Link über Caddy eingelöst")
	}
	// Der Link ist dadurch nicht verbraucht.
	if _, err := r.c.Door().Redeem(ctx, l.Token, web.Visitor{Gate: "Notausgang", Addr: "Notausgang"}); err != nil {
		t.Fatalf("am Notausgang: %v", err)
	}
	if j := journal(t, r); !strings.Contains(j, "fehlversuch|Einmal-Link außerhalb des Notausgangs abgelehnt|198.51.100.1") {
		t.Errorf("Protokoll:\n%s", j)
	}
}

func TestSperreGiltFuerGanzesIPv6Netz(t *testing.T) {
	r, _ := doorRig(t, nil)
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	door := r.c.Door()
	for i := 0; i < 5; i++ {
		door.Login(ctx, "falsch", web.Visitor{Gate: "hostdeck.example.de", Addr: "2001:db8:1:2::" + string(rune('a'+i))})
	}
	var locked *web.LockedError
	if _, err := door.Login(ctx, testPW, web.Visitor{Gate: "hostdeck.example.de", Addr: "2001:db8:1:2:ffff::1"}); !errors.As(err, &locked) {
		t.Fatalf("neue Adresse im selben /64: %v", err)
	}
	if _, err := door.Login(ctx, testPW, web.Visitor{Gate: "hostdeck.example.de", Addr: "2001:db8:1:3::1"}); err != nil {
		t.Fatalf("anderes /64: %v", err)
	}
	if lockKey("203.0.113.9") != "203.0.113.9" || lockKey("Notausgang") != "Notausgang" || lockKey("2001:db8::1") != "2001:db8::/64" {
		t.Fatal("lockKey")
	}
}

// Gleichzeitige falsche Bestätigungen: höchstens fünf werden geprüft, dann
// ist die Anmeldung beendet. Die Bremse für alle gilt beim Bestätigen nicht.
func TestGleichzeitigeBestaetigungen(t *testing.T) {
	r, _ := doorRig(t, nil)
	if err := r.d.Password.Set(testPW); err != nil {
		t.Fatal(err)
	}
	sid, _ := r.d.Sessions.NewSession("Passwort", "Test", "203.0.113.9")
	const n = 40
	codes := make(chan int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func(i int) {
			<-start
			c, _ := r.api(t, "POST", "/api/bestaetigen", sid, fmt.Sprintf(`{"passwort":"falsch-%02d-xxxxxx"}`, i))
			codes <- c
		}(i)
	}
	close(start)
	count := map[int]int{}
	for i := 0; i < n; i++ {
		count[<-codes]++
	}
	if count[http.StatusUnauthorized] > anmeldung.MaxFails || count[http.StatusUnauthorized]+count[http.StatusTooManyRequests] != n {
		t.Fatalf("Antworten: %v", count)
	}
	// Nach fünf falschen Bestätigungen ist die Anmeldung weg.
	for i := count[http.StatusUnauthorized]; i < anmeldung.MaxFails; i++ {
		time.Sleep(10 * time.Millisecond)
		r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"falsch-nochmal-xx"}`)
	}
	if code, _ := r.api(t, "GET", "/api/sitzung", sid, ""); code != http.StatusUnauthorized {
		t.Fatalf("Anmeldung nach fünf falschen Bestätigungen: %d", code)
	}
	if j := journal(t, r); !strings.Contains(j, "Abgemeldet nach 5 falschen Bestätigungen") {
		t.Errorf("Protokoll:\n%s", j)
	}
}

// Eine Anmeldung vom Notausgang gilt nicht über die öffentliche Adresse.
func TestAnmeldungNurAmEigenenEingang(t *testing.T) {
	r, _ := doorRig(t, nil)
	sid, _ := r.d.Sessions.NewSession("Link", "Notausgang", "Notausgang")
	req, _ := http.NewRequest("GET", "http://hostdeck-dienst/api/sitzung", nil)
	req.Header.Set(web.SessionHeader, sid)
	req.Header.Set(web.GateHeader, "hostdeck.example.de")
	req.Header.Set(web.AddrHeader, "198.51.100.1")
	if code, _ := r.do(t, req); code != http.StatusUnauthorized {
		t.Fatalf("Anmeldung vom Notausgang über Caddy: %d", code)
	}
	req.Header.Set(web.GateHeader, "Notausgang")
	if code, _ := r.do(t, req); code != 200 {
		t.Fatalf("am Notausgang: %d", code)
	}
}
