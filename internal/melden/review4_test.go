package melden

// Regressionstests zu früheren Funden
// übernommen: Sie halten die Fixes M2 und M3 fest.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// M2: Der Knopf „Neues Thema“ schickt token: "" (leitstand.js) und SetNtfy
// übernimmt das: Das Token eines eigenen ntfy-Servers ist danach weg.
func TestReview4NeuesThemaBehaeltToken(t *testing.T) {
	s := &Service{File: filepath.Join(t.TempDir(), "nachrichten.json")}
	if _, err := s.SetNtfy("https://ntfy.example.org", "tk_geheim", false); err != nil {
		t.Fatal(err)
	}
	v, err := s.SetNtfy("https://ntfy.example.org", "", true) // wie der Knopf „Neues Thema“
	if err != nil {
		t.Fatal(err)
	}
	if !v.Ntfy.WithAuth {
		t.Errorf("Nach „Neues Thema“ ist das Token weg; ein eigener Server mit Anmeldung lehnt danach jede Nachricht ab (401/403, sofort aufgegeben)")
	}
}

// M3: Hängt eine Warnung in der Wiederholung, kann die spätere Entwarnung
// zuerst ankommen; die alte Warnung kommt danach und ersetzt (gleicher Tag)
// die Entwarnung auf dem Gerät.
func TestReview4AlteWarnungNachEntwarnung(t *testing.T) {
	var mu sync.Mutex
	fail := true
	var got []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		defer mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		got = append(got, m["title"].(string))
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	s := &Service{Client: srv.Client(), Now: func() time.Time { return now }}
	if _, err := s.SetNtfy(srv.URL, "", false); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s.Send(Message{Title: "Warnung: Platte wird knapp", Tag: "platz:/"})
	s.step(ctx) // 503 -> nächster Versuch in 30 s
	now = now.Add(20 * time.Second)
	mu.Lock()
	fail = false
	mu.Unlock()
	s.Send(Message{Title: "Entwarnung: Platte wird knapp", Tag: "platz:/"})
	s.step(ctx)
	now = now.Add(15 * time.Second)
	s.step(ctx)
	mu.Lock()
	defer mu.Unlock()
	if len(got) > 0 && got[len(got)-1] != "Entwarnung: Platte wird knapp" {
		t.Errorf("Reihenfolge beim Gerät: %q; zuletzt steht die alte Warnung da", got)
	}
}
