package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/hinweise"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

func TestNachrichtZuEreignis(t *testing.T) {
	base := func() string { return "https://hostdeck.example.de" }
	m := eventMessage(ueberwachung.Event{Kind: ueberwachung.EvStart, Key: "platz:/", Level: ueberwachung.LevelErr, Title: "Platte fast voll", Text: "91 % belegt.", Link: "#/platz"}, base, "vps1")
	if m.Title != "Warnung (rot): Platte fast voll" || m.Priority != 4 || m.Text != "91 % belegt.\nServer: vps1" || m.Click != "https://hostdeck.example.de/#/platz" || m.Tag != "platz:/" {
		t.Errorf("Warnung: %+v", m)
	}
	m = eventMessage(ueberwachung.Event{Kind: ueberwachung.EvClear, Key: "platz:/", Title: "Wieder in Ordnung: Platte fast voll"}, func() string { return "" }, "")
	if m.Title != "Entwarnung: Platte fast voll" || m.Priority != 2 || m.Click != "" || m.Text != "" {
		t.Errorf("Entwarnung: %+v", m)
	}
	m = eventMessage(ueberwachung.Event{Kind: ueberwachung.EvStart, Level: ueberwachung.LevelWarn, Title: "x", Link: "https://evil.example"}, base, "h")
	if m.Priority != 3 || m.Click != "https://hostdeck.example.de/#/ueberwachung" {
		t.Errorf("gelb: %+v", m)
	}
}

// N23: Eine Warnung, die wieder in Ordnung ist (die Entwarnung kommt noch),
// steht in „Braucht dich“ wie unter Überwachung blau mit Haken und zählt
// nicht als gelb. Vorher war sie auf der Übersicht, an Glocke und Abzeichen
// gelb, während Überwachung schon grün war (Prüfer I17, Zustand f).
func TestAlertHintsWiederInOrdnung(t *testing.T) {
	f := filepath.Join(t.TempDir(), "regeln.json")
	now := time.Now().UTC()
	good := now.Add(-time.Minute)
	st := map[string]any{"aktiv": map[string]any{
		"prozessor": map[string]any{"schluessel": "prozessor", "stufe": "warn", "symbol": "leistung", "titel": "Prozessor dauerhaft voll",
			"text": "Seit mindestens 1 Minute über 50 % ausgelastet.", "seit": now.Add(-5 * time.Minute), "gut_seit": good, "gemeldet": now},
		"speicher": map[string]any{"schluessel": "speicher", "stufe": "warn", "symbol": "leistung", "titel": "Arbeitsspeicher knapp",
			"text": "Über 90 % belegt.", "seit": now.Add(-5 * time.Minute), "gemeldet": now},
	}}
	b, _ := json.Marshal(st)
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	got := map[string]hinweise.Hint{}
	for _, h := range alertHints(&ueberwachung.Rules{File: f})() {
		got[h.Key] = h
	}
	if h := got["prozessor"]; h.Level != hinweise.Acc || h.Icon != "done" || !strings.Contains(h.Text, "wieder in Ordnung") {
		t.Errorf("wieder in Ordnung: %+v", h)
	}
	if h := got["speicher"]; h.Level != hinweise.Warn || h.Icon != "leistung" {
		t.Errorf("läuft noch: %+v", h)
	}
}
