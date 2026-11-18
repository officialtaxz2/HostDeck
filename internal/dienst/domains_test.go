package dienst

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

type fakeDomains struct {
	mu      sync.Mutex
	done    []string
	release chan struct{}
}

func (f *fakeDomains) Overview(context.Context) domains.Overview {
	return domains.Overview{Found: true, Sites: []domains.Site{{Hosts: []string{"shop.example.org"}, Kind: domains.KindOwn}}}
}

func (f *fakeDomains) ReorgPlan(_ context.Context, o *domains.Options) (domains.ReorgPlan, error) {
	return domains.ReorgPlan{Stand: "s1", Same: true}, nil
}

func (f *fakeDomains) Reorg(_ context.Context, o *domains.Options, stand string, ev func(caddy.Event)) (string, error) {
	ev(caddy.Event{Step: caddy.StepWrite, State: caddy.StateRunning})
	if f.release != nil {
		<-f.release
	}
	ev(caddy.Event{Step: caddy.StepWrite, State: caddy.StateOK})
	f.mu.Lock()
	f.done = append(f.done, "ordnen "+stand)
	f.mu.Unlock()
	return "Caddy ist geordnet.", nil
}

func (f *fakeDomains) ChangePlan(_ context.Context, r domains.ChangeReq) (domains.ChangePlan, error) {
	if r.Host == "gibts.nicht" {
		return domains.ChangePlan{}, domains.ErrNotFound
	}
	return domains.ChangePlan{Action: r.Action, Host: r.Host, Stand: "s2"}, nil
}

func (f *fakeDomains) Change(_ context.Context, r domains.ChangeReq, stand string, ev func(caddy.Event)) (string, error) {
	f.mu.Lock()
	f.done = append(f.done, r.Action+" "+r.Host)
	f.mu.Unlock()
	if r.Action == domains.ActOff {
		ev(caddy.Event{Step: caddy.StepWrite, State: caddy.StateOK})
		return "", &caddy.ErrUndone{Step: caddy.StepCompare, Err: errors.New("Abweichung")}
	}
	return r.Host + " ist angelegt.", nil
}

func TestDomainsImLeitstand(t *testing.T) {
	fd := &fakeDomains{release: make(chan struct{})}
	r, _ := doorRigWith(t, nil, func(d *Dienst) { d.Domains = fd })
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := r.api(t, "GET", "/api/domains", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", code)
	}
	code, b := r.api(t, "GET", "/api/domains", sid, "")
	if code != 200 || !strings.Contains(b, `"shop.example.org"`) {
		t.Fatalf("Stand: %d %s", code, b)
	}
	if code, b = r.api(t, "POST", "/api/domains/ordnen/vorschau", sid, `{}`); code != 200 || !strings.Contains(b, `"stand":"s1"`) {
		t.Fatalf("Vorschau: %d %s", code, b)
	}
	if code, _ = r.api(t, "POST", "/api/domains/aendern/vorschau", sid, `{"aktion":"aus","domain":"gibts.nicht"}`); code != http.StatusNotFound {
		t.Errorf("unbekannt: %d", code)
	}
	if code, b = r.api(t, "POST", "/api/domains/ordnen", sid, `{"stand":"s1"}`); code != http.StatusForbidden || !strings.Contains(b, `"bestaetigen":true`) {
		t.Fatalf("ohne Bestätigung: %d %s", code, b)
	}
	r.api(t, "POST", "/api/bestaetigen", sid, `{"passwort":"`+testPW+`"}`)
	if code, _ = r.api(t, "POST", "/api/domains/ordnen", sid, `{}`); code != http.StatusBadRequest {
		t.Errorf("ohne Vorschau: %d", code)
	}
	if code, _ = r.api(t, "POST", "/api/domains/aendern", sid, `{"aktion":"loeschen","domain":"a.example","stand":"x"}`); code != http.StatusBadRequest {
		t.Errorf("unbekannte Aktion: %d", code)
	}
	if code, _ = r.api(t, "POST", "/api/domains/aendern", sid, `{"aktion":"aus","domain":"a b","stand":"x"}`); code != http.StatusUnprocessableEntity {
		t.Errorf("Unsinn als Domain: %d", code)
	}
	code, b = r.api(t, "POST", "/api/domains/ordnen", sid, `{"stand":"s1"}`)
	if code != http.StatusAccepted || !strings.Contains(b, `"laeuft":true`) || !strings.Contains(b, `"schritte":[`) {
		t.Fatalf("Start: %d %s", code, b)
	}
	if i, _ := lauf.CurrentIn(r.d.RunDir); i == nil || i.What != "Caddy ordnen" {
		t.Errorf("Sperre: %+v", i)
	}
	if code, _ = r.api(t, "POST", "/api/domains/aendern", sid, `{"aktion":"neu","domain":"neu.example.org","ziel":"4000","stand":"s2"}`); code != http.StatusConflict {
		t.Errorf("zweiter Lauf: %d", code)
	}
	close(fd.release)
	wait := func(want string) string {
		deadline := time.Now().Add(5 * time.Second)
		for {
			_, b := r.api(t, "GET", "/api/domains", sid, "")
			if (strings.Contains(b, `"laeuft":false`) && strings.Contains(b, want)) || time.Now().After(deadline) {
				return b
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	b = wait("geordnet")
	if !strings.Contains(b, `"ergebnis":"Caddy ist geordnet."`) || !strings.Contains(b, `"stand":"ok"`) {
		t.Fatalf("Ergebnis: %s", b)
	}
	for i := 0; i < 100; i++ {
		if c, _ := lauf.CurrentIn(r.d.RunDir); c == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ = r.api(t, "POST", "/api/domains/aendern", sid, `{"aktion":"neu","domain":"Neu.Example.org","ziel":"4000","stand":"s2"}`); code != http.StatusAccepted {
		t.Fatalf("neu: %d", code)
	}
	wait("angelegt")
	for i := 0; i < 100; i++ {
		if c, _ := lauf.CurrentIn(r.d.RunDir); c == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.api(t, "POST", "/api/domains/aendern", sid, `{"aktion":"aus","domain":"shop.example.org","stand":"s2"}`)
	b = wait("Abweichung")
	if !strings.Contains(b, `"wie_vorher":true`) {
		t.Errorf("zurückgelegt nicht gemeldet: %s", b)
	}
	r.d.wg.Wait()
	jr := journal(t, r)
	for _, want := range []string{"domain|Caddy ordnen gestartet", "domain|Caddy ist geordnet.", "Domain anlegen: neu.example.org gestartet", "Domain abschalten: shop.example.org ging nicht", "203.0.113.9"} {
		if !strings.Contains(jr, want) {
			t.Errorf("Protokoll ohne %q: %s", want, jr)
		}
	}
}
