package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/ort"
)

// Nach dem Einzug nimmt HostDeck die Testadresse der Probe-Kopie aus
// dem Zugang und aus der Caddy-Datei des Leitstands. Erst danach steht im
// Protokoll, dass der DNS-Eintrag weg kann.
func TestTestadresseNachEinzugWeg(t *testing.T) {
	r := newPubRig(t)
	ctx := context.Background()
	if err := r.p.Publish(ctx, "hostdeck.example.de", r.step); err != nil {
		t.Fatalf("%v\n%+v", err, r.steps)
	}
	st, err := loadAccess(r.in.DataDir)
	if err != nil || st == nil {
		t.Fatalf("Zugang: %v", err)
	}
	st.Extra = []string{"hostdeck-neu.example.de"}
	if err := saveAccess(r.in.DataDir, st); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.read(ort.CaddySite), "hostdeck-neu.example.de") {
		t.Fatalf("Vorbedingung: Testadresse fehlt in der Caddy-Datei:\n%s", r.read(ort.CaddySite))
	}

	// Noch Probe-Kopie: Die Testadresse bleibt.
	mk, _ := json.Marshal(copyMarker{From: "alt", Since: time.Now(), Move: true, TestDomain: "hostdeck-neu.example.de"})
	os.WriteFile(filepath.Join(r.in.DataDir, ort.CopyFile), mk, 0o600)
	var notes []string
	note := func(s string) { notes = append(notes, s) }
	settleTestAddress(ctx, r.p, t.TempDir(), r.in.DataDir, note)
	if st, _ := loadAccess(r.in.DataDir); len(st.Extra) != 1 || len(notes) != 0 {
		t.Fatalf("Probe-Kopie verlor die Testadresse: %+v %q", st.Extra, notes)
	}

	// Wie im Labor: Was nicht mehr in der Caddy-Datei steht, antwortet mit
	// einem TLS-Fehler. Das ist beim Herausnehmen gewollt, kein Rückfall.
	r.p.Probe = func(_ context.Context, h string) caddy.Answer {
		if strings.Contains(h, "hostdeck-neu.example.de") && !strings.Contains(r.read(ort.CaddySite), "hostdeck-neu.example.de") {
			return caddy.Answer{Err: "tls: internal error"}
		}
		return caddy.Answer{Status: 200}
	}

	// Eingezogen: weg aus Zugang und Caddy-Datei, ein Eintrag mit dem Rat.
	if err := promote(r.in.DataDir, "", "DNS", time.Now()); err != nil {
		t.Fatal(err)
	}
	settleTestAddress(ctx, r.p, t.TempDir(), r.in.DataDir, note)
	if st, _ := loadAccess(r.in.DataDir); len(st.Extra) != 0 {
		t.Fatalf("Zugang führt noch %v", st.Extra)
	}
	if got := r.read(ort.CaddySite); strings.Contains(got, "hostdeck-neu.example.de") || got != string(siteBlock("hostdeck.example.de")) {
		t.Fatalf("Caddy-Datei:\n%s\n%q", got, notes)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "DNS-Eintrag für hostdeck-neu.example.de kannst du jetzt") {
		t.Fatalf("Protokoll: %q", notes)
	}
	// Ein zweites Mal: nichts zu tun, kein Eintrag.
	settleTestAddress(ctx, r.p, t.TempDir(), r.in.DataDir, note)
	if len(notes) != 1 {
		t.Fatalf("doppelt: %q", notes)
	}
}

// Lehnt Caddy die neue Datei ab, bleibt die alte, und der Rat zum Löschen
// kommt nicht; das nächste Update versucht es wieder.
func TestTestadresseCaddyLehntAb(t *testing.T) {
	r := newPubRig(t)
	ctx := context.Background()
	if err := r.p.Publish(ctx, "hostdeck.example.de", r.step); err != nil {
		t.Fatal(err)
	}
	st, _ := loadAccess(r.in.DataDir)
	st.Extra = []string{"hostdeck-neu.example.de"}
	saveAccess(r.in.DataDir, st)
	r.p.Refresh(ctx)
	before := r.read(ort.CaddySite)
	r.caddy.reject = append(r.caddy.reject, "hostdeck.example.de {")
	var notes []string
	settleTestAddress(ctx, r.p, t.TempDir(), r.in.DataDir, func(s string) { notes = append(notes, s) })
	if r.read(ort.CaddySite) != before {
		t.Fatal("Caddy-Datei geändert, obwohl Caddy ablehnte")
	}
	if len(notes) != 1 || strings.Contains(notes[0], "kannst du jetzt") || !strings.Contains(notes[0], "stehen lassen") {
		t.Fatalf("Protokoll: %q", notes)
	}
	// Update: Refresh sieht den Unterschied und holt es nach.
	r.caddy.reject = r.caddy.reject[:len(r.caddy.reject)-1]
	var buf strings.Builder
	refreshCaddy(ctx, r.p, nil, "", &buf)
	noteTestAddress(r.in.DataDir, []string{"hostdeck-neu.example.de"}, nil, "", &buf)
	if strings.Contains(r.read(ort.CaddySite), "hostdeck-neu") || !strings.Contains(buf.String(), "kannst du jetzt") ||
		!strings.Contains(buf.String(), "bedient hostdeck-neu.example.de nicht mehr") || strings.Contains(buf.String(), "HTTP/3") {
		t.Fatalf("Update holte es nicht nach:\n%s\n%s", r.read(ort.CaddySite), buf.String())
	}
}
