package web

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Die Farben stehen als Rollen oben in app.css, einmal hell und einmal
// dunkel. Dieser Test hält sie auf WCAG 2.2 AA: Text 4,5:1, Rahmen von
// Bedienelementen und Grafik 3:1. Wer eine Farbe ändert, merkt hier, ob
// sie noch reicht.

var (
	// Text auf Fläche, wie in app.css verwendet.
	textPairs = [][2]string{
		{"ink", "bg"}, {"ink", "sf"}, {"ink", "sf2"}, {"ink", "acs"}, {"ink", "tks"}, {"ink", "ars"}, {"ink", "sks"},
		{"ink", "ops"}, {"ink", "nws"}, {"ink", "errs"}, {"ink", "sfk"},
		{"ink2", "sf"}, {"ink2", "sf2"},
		{"mut", "bg"}, {"mut", "sf"}, {"mut", "sf2"}, {"mut", "sfk"}, {"mut", "tks"}, {"mut", "ars"}, {"mut", "sks"}, {"mut", "ln2"}, {"mut", "acs"},
		{"mut2", "bg"}, {"mut2", "sf"}, {"mut2", "sf2"}, {"mut2", "sfk"}, {"mut2", "ln2"},
		{"ac", "bg"}, {"ac", "sf"}, {"ac", "sf2"}, {"ac", "sfk"}, {"ac", "acs"}, {"ac", "nws"}, {"ac", "tks"}, {"ac", "ars"}, {"ac", "sks"},
		{"aci", "ac"}, {"aci", "ach"}, {"aci", "acd"},
		{"tk", "tks"}, {"tk", "sf"}, {"tki", "tks"},
		{"ar", "ars"}, {"ari", "ars"},
		{"skt", "sks"}, {"ski", "sks"},
		{"op", "ops"}, {"op", "sf"}, {"op", "sfk"}, {"op", "bg"}, {"opi", "ops"},
		{"err", "sf"}, {"err", "sf2"}, {"err", "bg"}, {"erri", "errs"},
		{"bg", "ink"},
		// Leitstand: Seitenleiste, Flächen mit Verlauf, Zeiger, Zustandsfarben.
		{"ink", "bg2"}, {"ink", "sfh"}, {"ink", "hov"}, {"ink2", "bg2"}, {"ink2", "sfh"}, {"ink2", "bg"},
		{"mut", "bg2"}, {"mut", "sfh"}, {"mut", "hov"}, {"mut2", "bg2"}, {"mut2", "sfh"}, {"mut2", "hov"},
		{"ac", "bg2"}, {"ac", "sfh"}, {"ac", "hov"},
		{"ok", "sf"}, {"ok", "sfh"}, {"ok", "bg"}, {"ok", "sf2"}, {"warn", "sf"}, {"warn", "sfh"}, {"warn", "bg"}, {"warn", "bg2"},
		{"err", "sfh"}, {"err", "bg2"}, {"info", "sf"}, {"info", "sfh"}, {"info", "sf2"},
		{"ok", "bg2"}, {"ink2", "hov"}, {"aci", "ok"}, {"ach", "hov"},
	}
	// Rahmen und Flächen, die man erkennen muss.
	shapePairs = [][2]string{
		{"ctl", "sf"}, {"ctl", "bg"}, {"ctl", "sf2"}, {"ctl", "sfk"},
		{"ac", "sf"}, {"ac", "bg"}, {"ac", "sfk"}, {"ac", "acs"},
		{"tk", "tks"}, {"ar", "ars"}, {"skt", "sks"}, {"arb", "ars"},
		{"tk", "sf"}, {"ar", "sf"}, {"sk", "sf"}, {"err", "sf"},
		{"ctl", "bg2"}, {"ctl", "sfh"}, {"wire", "sf"}, {"wire", "sf2"}, {"wire", "bg"}, {"ac", "bg2"}, {"ac", "sfh"},
	}
)

func TestFarbkontraste(t *testing.T) {
	css, err := static.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	themes := cssThemes(t, string(css))
	// Die zusätzlichen Akzentfarben (Einstellungen › Darstellung) ersetzen
	// nur ein paar Rollen; mit ihnen gelten dieselben Paare.
	accents := cssAccents(t, string(css))
	if len(accents) == 0 {
		t.Fatal("keine Akzentfarben gefunden")
	}
	for _, ac := range accents {
		base := themes[ac.theme]
		merged := map[string]string{}
		for k, v := range base {
			merged[k] = v
		}
		for k, v := range ac.roles {
			merged[k] = v
		}
		themes[ac.theme+"/"+ac.name] = merged
	}
	for name, roles := range themes {
		check := func(pairs [][2]string, need float64, kind string) {
			for _, p := range pairs {
				fg, bg := roles[p[0]], roles[p[1]]
				if fg == "" || bg == "" {
					t.Errorf("%s: Rolle --%s oder --%s fehlt", name, p[0], p[1])
					continue
				}
				if r := contrast(t, fg, bg); r < need {
					t.Errorf("%s: %s --%s auf --%s hat %.2f:1, gebraucht %.1f:1", name, kind, p[0], p[1], r, need)
				}
			}
		}
		check(textPairs, 4.5, "Text")
		check(shapePairs, 3, "Rahmen")
		// Jede Regel, die Hintergrund und Schrift zugleich setzt, muss als
		// Text lesbar sein, auch wenn sie oben nicht aufgezählt ist.
		for _, p := range ruleColorPairs(string(css)) {
			if r := contrast(t, roles[p[0]], roles[p[1]]); r < 4.5 {
				t.Errorf("%s: Regel mit color --%s auf background --%s hat %.2f:1", name, p[0], p[1], r)
			}
		}
	}
}

var (
	roleRe  = regexp.MustCompile(`--([a-z0-9]+):\s*(#[0-9a-fA-F]{6})`)
	blockRe = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	bgRe    = regexp.MustCompile(`background:\s*var\(--([a-z0-9]+)\)`)
	colorRe = regexp.MustCompile(`(?:^|[;\s])color:\s*var\(--([a-z0-9]+)\)`)
)

// cssThemes liest die Rollen aus :root (hell) und :root[data-theme="dark"].
// Der Block für prefers-color-scheme muss dieselben Werte haben wie der für
// die ausdrückliche Wahl.
func cssThemes(t *testing.T, css string) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{}
	var mediaDark map[string]string
	for _, m := range blockRe.FindAllStringSubmatch(css, -1) {
		sel := strings.TrimSpace(m[1])
		if i := strings.LastIndex(sel, "\n"); i >= 0 {
			sel = strings.TrimSpace(sel[i+1:])
		}
		roles := map[string]string{}
		for _, r := range roleRe.FindAllStringSubmatch(m[2], -1) {
			roles[r[1]] = strings.ToLower(r[2])
		}
		if len(roles) < 20 {
			continue
		}
		switch {
		case strings.HasSuffix(sel, `:root:not([data-theme="light"])`):
			mediaDark = roles
		case sel == `:root[data-theme="dark"]`:
			out["dunkel"] = roles
		case sel == ":root":
			out["hell"] = roles
		}
	}
	if out["hell"] == nil || out["dunkel"] == nil || mediaDark == nil {
		t.Fatalf("Farbrollen nicht gefunden: hell %v, dunkel %v, System-dunkel %v", out["hell"] != nil, out["dunkel"] != nil, mediaDark != nil)
	}
	if len(out["hell"]) != len(out["dunkel"]) {
		t.Errorf("hell hat %d Rollen, dunkel %d", len(out["hell"]), len(out["dunkel"]))
	}
	for k, v := range out["dunkel"] {
		if mediaDark[k] != v {
			t.Errorf("--%s: dunkel %s, aber im System-dunkel-Block %s", k, v, mediaDark[k])
		}
	}
	return out
}

type accentRoles struct {
	name, theme string
	roles       map[string]string
}

var accentSel = regexp.MustCompile(`^:root(\[data-theme="dark"\]|:not\(\[data-theme="light"\]\))?\[data-accent="([a-z]+)"\]$`)

// cssAccents liest die Blöcke :root[data-accent=…] (hell) und
// :root[data-theme="dark"][data-accent=…] (dunkel). Der Block für
// prefers-color-scheme muss dem dunklen gleichen.
func cssAccents(t *testing.T, css string) []accentRoles {
	t.Helper()
	var out []accentRoles
	media := map[string]map[string]string{}
	dark := map[string]map[string]string{}
	for _, m := range blockRe.FindAllStringSubmatch(css, -1) {
		sel := strings.TrimSpace(m[1])
		if i := strings.LastIndexAny(sel, "\n{"); i >= 0 {
			sel = strings.TrimSpace(sel[i+1:])
		}
		if i := strings.LastIndex(sel, ") { "); i >= 0 {
			sel = strings.TrimSpace(sel[i+3:])
		}
		x := accentSel.FindStringSubmatch(sel)
		if x == nil {
			continue
		}
		roles := map[string]string{}
		for _, r := range roleRe.FindAllStringSubmatch(m[2], -1) {
			roles[r[1]] = strings.ToLower(r[2])
		}
		switch {
		case x[1] == "":
			out = append(out, accentRoles{name: x[2], theme: "hell", roles: roles})
		case strings.HasPrefix(x[1], ":not"):
			media[x[2]] = roles
		default:
			dark[x[2]] = roles
			out = append(out, accentRoles{name: x[2], theme: "dunkel", roles: roles})
		}
	}
	for name, roles := range dark {
		for k, v := range roles {
			if media[name][k] != v {
				t.Errorf("Akzent %s --%s: dunkel %s, aber im System-dunkel-Block %s", name, k, v, media[name][k])
			}
		}
	}
	return out
}

// ruleColorPairs findet Regeln, die background und color über Rollen setzen.
func ruleColorPairs(css string) [][2]string {
	var out [][2]string
	for _, m := range blockRe.FindAllStringSubmatch(css, -1) {
		bg := bgRe.FindStringSubmatch(m[2])
		fg := colorRe.FindStringSubmatch(m[2])
		if bg != nil && fg != nil {
			out = append(out, [2]string{fg[1], bg[1]})
		}
	}
	return out
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("Farbe %q", hex)
	}
	var c [3]float64
	for i := range c {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatal(err)
		}
		s := float64(v) / 255
		if s <= 0.04045 {
			c[i] = s / 12.92
		} else {
			c[i] = math.Pow((s+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

// Live und Spielserver: Zustand neben
// dem Titel (Live, verzögert, angehalten), der Punkt darin und sein Puls
// als Form, Abzeichen „Spiel“, Kürzel der Spielserver, Zahl am Linienende
// und „angehalten, solange du liest“.
func TestLiveUndSpielserverKontraste(t *testing.T) {
	css, err := static.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	text := [][2]string{{"tk", "tks"}, {"op", "ops"}, {"mut2", "sf2"}, {"ar", "ars"}, {"ari", "ars"}, {"mut", "sf2"}, {"ink", "sf"}, {"ink", "sfh"}, {"ink2", "sf2"}}
	shape := [][2]string{{"tk", "tks"}, {"op", "ops"}, {"tk", "bg"}, {"op", "bg"}, {"mut2", "sf2"}}
	for name, roles := range cssThemes(t, string(css)) {
		for _, set := range []struct {
			pairs [][2]string
			need  float64
		}{{text, 4.5}, {shape, 3}} {
			for _, p := range set.pairs {
				if r := contrast(t, roles[p[0]], roles[p[1]]); r < set.need {
					t.Errorf("%s: --%s auf --%s hat %.2f:1, gebraucht %.1f:1", name, p[0], p[1], r, set.need)
				}
			}
		}
	}
	for _, want := range []string{".lv-state { ", "background: var(--tks)", ".lv-state.stale { color: var(--op); background: var(--ops); }"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("app.css: %q fehlt (Farben des Live-Zustands)", want)
		}
	}
}

// Akzentfarben (Einstellungen › Darstellung): Jede Farbe ersetzt den
// Akzent hell und dunkel und muss dieselben Paare schaffen wie Violett.
// Der Block für prefers-color-scheme muss dem für die ausdrückliche Wahl
// gleichen.
func TestAkzentfarben(t *testing.T) {
	css, err := static.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	themes := cssThemes(t, string(css))
	accRe := regexp.MustCompile(`(?m)^(@media \(prefers-color-scheme: dark\) \{ )?:root(:not\(\[data-theme="light"\]\))?(\[data-theme="dark"\])?\[data-accent="([a-z]+)"\] \{([^{}]*)\}`)
	type key struct{ name, theme string }
	blocks := map[key]map[string]string{}
	media := map[string]map[string]string{}
	for _, m := range accRe.FindAllStringSubmatch(string(css), -1) {
		roles := map[string]string{}
		for _, r := range roleRe.FindAllStringSubmatch(m[5], -1) {
			roles[r[1]] = strings.ToLower(r[2])
		}
		switch {
		case m[1] != "":
			media[m[4]] = roles
		case m[3] != "":
			blocks[key{m[4], "dunkel"}] = roles
		default:
			blocks[key{m[4], "hell"}] = roles
		}
	}
	names := []string{"eisblau", "gruen", "gelb", "koralle", "rosa"}
	for _, n := range names {
		for _, th := range []string{"hell", "dunkel"} {
			acc := blocks[key{n, th}]
			if len(acc) < 6 {
				t.Errorf("%s %s: Block fehlt oder unvollständig (%d Rollen)", n, th, len(acc))
				continue
			}
			roles := map[string]string{}
			for k, v := range themes[th] {
				roles[k] = v
			}
			for k, v := range acc {
				roles[k] = v
			}
			for _, p := range [][2]string{{"ac", "bg"}, {"ac", "sf"}, {"ac", "sf2"}, {"ac", "sfh"}, {"ac", "bg2"}, {"ac", "hov"}, {"ac", "acs"}, {"ac", "nws"},
				{"ink", "acs"}, {"ink", "nws"}, {"mut", "acs"}, {"aci", "ac"}, {"aci", "ach"}, {"aci", "acd"}, {"ach", "hov"}} {
				if r := contrast(t, roles[p[0]], roles[p[1]]); r < 4.5 {
					t.Errorf("%s %s: --%s auf --%s hat %.2f:1, gebraucht 4.5:1", n, th, p[0], p[1], r)
				}
			}
		}
		for k, v := range blocks[key{n, "dunkel"}] {
			if media[n][k] != v {
				t.Errorf("%s: --%s dunkel %s, im System-dunkel-Block %s", n, k, v, media[n][k])
			}
		}
	}
}
