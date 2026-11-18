package web

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

// Jede Klasse, die das Frontend setzt, braucht eine Regel in app.css. So
// fällt auf, wenn beim Aufräumen eine noch benutzte Regel verschwindet.
func TestKlassenHabenRegeln(t *testing.T) {
	var js string
	for _, f := range []string{"static/leitstand.js", "static/app.js"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		js += string(b)
	}
	css, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	// Nur Haken für Skripte und Tests, ohne eigene Darstellung.
	hooks := map[string]bool{"ls-nav-grp": true, "ls-selall": true}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`class: '([^']*)'`).FindAllStringSubmatch(js, -1) {
		for _, c := range strings.Fields(m[1]) {
			// „b-“ und Ähnliches setzt der Code mit einem Zusatz zusammen.
			if strings.HasSuffix(c, "-") || hooks[c] || seen[c] {
				continue
			}
			seen[c] = true
			if !regexp.MustCompile(`\.` + regexp.QuoteMeta(c) + `([^\w-]|$)`).Match(css) {
				t.Errorf("Klasse %q hat keine Regel in app.css", c)
			}
		}
	}
	if len(seen) < 100 {
		t.Fatalf("nur %d Klassen gefunden; liest der Test die Dateien richtig?", len(seen))
	}
}

// Eine fehlende Klammer in app.css lässt alle folgenden Regeln still
// verfallen; das fiel bei U8 erst im Bild auf.
func TestCSSKlammernAusgeglichen(t *testing.T) {
	b, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(b), "")
	depth := 0
	for i, r := range css {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				t.Fatalf("app.css: '}' ohne Gegenstück bei Zeichen %d", i)
			}
		}
	}
	if depth != 0 {
		t.Errorf("app.css: %d '{' ohne Gegenstück", depth)
	}
}

// N22: Eine Regel wie „span.warn“ wiegt mehr als jede einzelne Klasse und
// überstimmte so die gelben Bausteine des Leitstands: Gelbe Abzeichen
// standen nicht rechts und in anderer Schrift, die Zahl an der Glocke war
// gelb auf gelb, der Punkt im Kopf rutschte in eine eigene Zeile. Stufen
// (warn, err, ok, acc, idle) an einem Element ohne Vorfahren tragen darum
// höchstens Klassen-Gewicht, etwa :where(p, span).warn.
func TestStufenUeberstimmenKeineBausteine(t *testing.T) {
	b, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(b), "")
	re := regexp.MustCompile(`(?m)(?:^|[,{}])\s*([a-z][a-z0-9]*\.(?:warn|err|ok|acc|idle))\b`)
	for _, m := range re.FindAllStringSubmatch(css, -1) {
		t.Errorf("%s überstimmt Bausteine wie .ls-dot.%s; :where(…) nutzen oder einen Vorfahren nennen", m[1], strings.SplitN(m[1], ".", 2)[1])
	}
}

// R8: Ohne Dienst färbt die Seite Platte und Arbeitsspeicher nach den
// Voreinstellungen der Überwachung; die kommen mit dem Stand, nicht als
// zweite Zahl im Leitstand.
func TestStandBringtVoreinstellungen(t *testing.T) {
	c := newClient(t, prepare(t, nil, true))
	_, raw := c.do("GET", "/api/state", nil, nil)
	var st struct {
		Grenzen map[string]any `json:"grenzen"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(ueberwachung.DefaultLimits)
	var w map[string]any
	_ = json.Unmarshal(want, &w)
	for _, k := range []string{"platte_gelb", "platte_rot", "speicher_gelb", "prozessor_gelb"} {
		if st.Grenzen[k] == nil || st.Grenzen[k] != w[k] {
			t.Errorf("%s: %v, Voreinstellung %v", k, st.Grenzen[k], w[k])
		}
	}
	js, err := os.ReadFile("static/leitstand.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "s && s.grenzen ? s.grenzen") {
		t.Error("Leitstand nimmt ohne Dienst nicht die Grenzen aus dem Stand")
	}
}
