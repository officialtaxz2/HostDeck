package betrieb

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Zustand eines Containers und einer Gruppe (Compose-Projekt, Container
// allein, verwaltete Server) in Worten. Das ist die eine Quelle dafür:
// Übersicht, Lageplan,
// Überwachung und Projekte übernehmen Wort, Stufe und Zahlen nur, statt sie
// je Stelle selbst und in anderer Reihenfolge auszurechnen.

// Klassen eines Containers; je Klasse ein Wort und eine Stufe.
const (
	KlasseLaeuft      = "laeuft"
	KlasseUnhealthy   = "unhealthy"
	KlasseSchleife    = "schleife"
	KlasseVonDir      = "von-dir"
	KlasseLetzterLauf = "letzter-lauf"
	KlasseJob         = "job"
	KlasseAngelegt    = "angelegt"
	KlasseUnerwartet  = "unerwartet"
	KlasseAus         = "aus" // verwalteter Server, der nicht läuft: nur Anzeige
)

// Wörter der Oberfläche; überall dieselben.
const (
	WortLaeuft      = "läuft"
	WortUnhealthy   = "läuft, aber „unhealthy“"
	WortSchleife    = "Neustart-Schleife"
	WortVonDir      = "von dir gestoppt"
	WortLetzterLauf = "seit dem letzten Lauf aus"
	WortJob         = "Job erledigt"
	WortAngelegt    = "angelegt"
	WortUnerwartet  = "unerwartet gestoppt"
	WortAus         = "aus"
)

// Stufen wie in der Oberfläche (ls-st ok, idle, warn, err).
const (
	StufeOK   = "ok"
	StufeIdle = "idle"
	StufeWarn = "warn"
	StufeErr  = "err"
)

// Neustart-Schleife aus Sicht von HostDeck: Docker sagt „restarting“, oder der
// Container läuft erst seit kurzem und hat schon mehrmals neu gestartet
// (Docker sagt dann nur „running“). Dieselbe Grenze nutzt die Überwachung
// (ueberwachung.LoopFresh).
const (
	LoopRestarts = 3
	LoopFresh    = 15 * time.Minute
)

// Looping sagt, ob der Container in einer Neustart-Schleife steckt.
func (c *Container) Looping(now time.Time) bool {
	if c.ManagedBy != "" || c.Job {
		return false
	}
	return c.State == "restarting" || (c.Running && c.Restarts >= LoopRestarts && c.Since != nil && now.Sub(*c.Since) < LoopFresh)
}

// classify setzt Klasse, Wort, Stufe und Grund. Die Reihenfolge ist die
// eine Regel: „von dir gestoppt“ geht vor „angelegt“ und vor „Job erledigt“.
func (c *Container) classify(now time.Time) {
	set := func(class, word, level, text string) {
		c.Class, c.Word, c.Level, c.Why = class, word, level, text
	}
	switch {
	// Verwaltete Server (etwa Spielserver eines Panels) haben dieselben
	// Zustände wie jeder Container, soweit HostDeck sie sicher kennt: Neustart
	// durch Docker und „unhealthy“ wie überall. Aus ist bei ihnen kein
	// Fehler, denn das Panel startet und stoppt sie (Datenprüfung 18.11.).
	case c.ManagedBy != "" && c.State == "restarting":
		set(KlasseSchleife, WortSchleife, StufeErr, plural(c.Restarts, "Neustart", "Neustarts"))
	case c.ManagedBy != "" && c.Running && c.Health == "unhealthy":
		set(KlasseUnhealthy, WortUnhealthy, StufeWarn, "")
	case c.ManagedBy != "" && c.Running:
		set(KlasseLaeuft, WortLaeuft, StufeOK, "")
	case c.ManagedBy != "":
		// Ohne Warnung, aber mit Grund, wenn er abgestürzt ist.
		set(KlasseAus, WortAus, StufeIdle, crashWhy(c.ExitCode))
	case c.Looping(now):
		set(KlasseSchleife, WortSchleife, StufeErr, plural(c.Restarts, "Neustart", "Neustarts"))
	case c.Running && c.Health == "unhealthy":
		set(KlasseUnhealthy, WortUnhealthy, StufeWarn, "")
	case c.Running:
		set(KlasseLaeuft, WortLaeuft, StufeOK, "")
	case c.StoppedByYou != nil:
		set(KlasseVonDir, WortVonDir, StufeIdle, "")
	case c.StoppedByMove:
		set(KlasseLetzterLauf, WortLetzterLauf, StufeIdle, "")
	case c.Job:
		set(KlasseJob, WortJob, StufeIdle, "")
	case c.State == "created":
		set(KlasseAngelegt, WortAngelegt, StufeIdle, "nie gestartet")
	default:
		set(KlasseUnerwartet, WortUnerwartet, StufeErr, crashWhy(c.ExitCode))
	}
}

// crashWhy: „abgestürzt (Code N)“, außer bei 0, 137 und 143 (beendet wie
// bei docker stop, nicht abgestürzt).
func crashWhy(code int) string {
	if code != 0 && code != 137 && code != 143 {
		return "abgestürzt (Code " + strconv.Itoa(code) + ")"
	}
	return ""
}

// Group ist der Zustand einer Gruppe von Containern: ein Wort, eine Stufe
// und die Zahlen dahinter. Verwaltete Server zählen nur in ihrer eigenen
// Gruppe; erledigte Jobs zählen nie als „läuft nicht“.
type Group struct {
	Key   string `json:"schluessel"` // p:<projekt>, c:<container>, panel:<verwalter>
	Name  string `json:"name"`
	Word  string `json:"wort"`
	Level string `json:"stufe"`
	// Text nennt die Zusammensetzung, etwa „2 Container: 1 läuft, 1 Job
	// erledigt“; leer bei einem einzelnen Container.
	Text string `json:"text,omitempty"`

	Total      int  `json:"gesamt"`
	Running    int  `json:"laufen"` // läuft (auch „unhealthy“), ohne Schleife
	Unhealthy  int  `json:"unhealthy"`
	Looping    int  `json:"schleife"`
	ByYou      int  `json:"von_dir"`
	ByMove     int  `json:"letzter_lauf"`
	Jobs       int  `json:"jobs"`
	Created    int  `json:"angelegt"`
	Unexpected int  `json:"unerwartet"`
	Managed    bool `json:"verwaltet,omitempty"`
}

// Relevant sind die Container, die laufen sollen: ohne erledigte Jobs und
// ohne angelegte, die nie liefen.
func (g Group) Relevant() int { return g.Total - g.Jobs - g.Created }

// GroupKey ist der Schlüssel der Gruppe eines Containers.
func GroupKey(c Container) (key, name string) {
	switch {
	case c.ManagedBy != "":
		return "panel:" + c.ManagedBy, c.ManagedBy
	case c.Project != "":
		return "p:" + c.Project, c.Project
	}
	return "c:" + c.Name, c.Name
}

// Groups fasst die Container nach Gruppen zusammen, sortiert nach Schlüssel.
// Die Container müssen klassifiziert sein (States tut das).
func Groups(cs []Container) []Group {
	by := map[string]*Group{}
	var keys []string
	for _, c := range cs {
		k, n := GroupKey(c)
		g := by[k]
		if g == nil {
			g = &Group{Key: k, Name: n, Managed: c.ManagedBy != ""}
			by[k] = g
			keys = append(keys, k)
		}
		g.add(c)
	}
	out := make([]Group, 0, len(keys))
	for _, k := range keys {
		g := by[k]
		g.finish()
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Sum zählt alle eigenen Container zusammen (ohne verwaltete Server): die
// Zahlen der Container-Kachel.
func Sum(cs []Container) Group {
	g := Group{Key: "summe", Name: "alle"}
	for _, c := range cs {
		if c.ManagedBy == "" {
			g.add(c)
		}
	}
	g.finish()
	return g
}

func (g *Group) add(c Container) {
	g.Total++
	switch c.Class {
	case KlasseLaeuft:
		g.Running++
	case KlasseUnhealthy:
		g.Running++
		g.Unhealthy++
	case KlasseSchleife:
		g.Looping++
	case KlasseVonDir:
		g.ByYou++
	case KlasseLetzterLauf:
		g.ByMove++
	case KlasseJob:
		g.Jobs++
	case KlasseAngelegt:
		g.Created++
	case KlasseUnerwartet:
		g.Unexpected++
	}
}

// finish setzt Wort, Stufe und Text aus den Zahlen.
func (g *Group) finish() {
	set := func(word, level string) { g.Word, g.Level = word, level }
	rel := g.Relevant()
	switch {
	case g.Total == 0:
		set("keine Container", StufeIdle)
	case g.Managed:
		// Verwaltete Server sind nur Anzeige: nie rot oder gelb.
		switch {
		case g.Running == g.Total:
			set(WortLaeuft, StufeOK)
		case g.Running == 0:
			set(WortAus, StufeIdle)
		default:
			set(runOf(g.Running, g.Total), StufeOK)
		}
	case g.Looping > 0:
		set(WortSchleife, StufeErr)
	case g.Unexpected > 0 && g.Running == 0:
		set(WortUnerwartet, StufeErr)
	case g.Unexpected > 0:
		set(runOf(g.Running, rel), StufeErr)
	case rel == 0 && g.Created > 0:
		set(WortAngelegt, StufeIdle)
	case rel == 0:
		set(WortJob, StufeIdle)
	case g.Running == 0 && g.ByYou > 0:
		set(WortVonDir, StufeIdle)
	case g.Running == 0:
		set(WortLetzterLauf, StufeIdle)
	case g.Running == rel && g.Unhealthy > 0:
		set(WortUnhealthy, StufeWarn)
	case g.Unhealthy > 0:
		// Einer „unhealthy“, ein anderer von dir gestoppt: gelb wie die
		// Kachel des Containers, nicht grün.
		set(runOf(g.Running, rel), StufeWarn)
	case g.Running == rel:
		set(WortLaeuft, StufeOK)
	default:
		// Teilweise von dir (oder vom letzten Lauf) gestoppt: kein Fehler.
		set(runOf(g.Running, rel), StufeOK)
	}
	if g.Total > 1 {
		g.Text = plural(g.Total, "Container", "Container") + ": " + g.parts()
	}
}

// parts zählt die Container je Zustand, in fester Reihenfolge.
func (g *Group) parts() string {
	var out []string
	add := func(n int, one, many string) {
		if n > 0 {
			out = append(out, plural(n, one, many))
		}
	}
	add(g.Running-g.Unhealthy, "läuft", "laufen")
	if g.Managed {
		add(g.Total-g.Running, "aus", "aus")
		return strings.Join(out, ", ")
	}
	add(g.Unhealthy, "„unhealthy“", "„unhealthy“")
	add(g.Looping, "in Neustart-Schleife", "in Neustart-Schleife")
	add(g.Unexpected, "unerwartet gestoppt", "unerwartet gestoppt")
	add(g.ByYou, "von dir gestoppt", "von dir gestoppt")
	add(g.ByMove, "seit dem letzten Lauf aus", "seit dem letzten Lauf aus")
	add(g.Created, "angelegt", "angelegt")
	add(g.Jobs, "Job erledigt", "Jobs erledigt")
	return strings.Join(out, ", ")
}

// runOf ist „1 von 2 läuft“ oder „2 von 3 laufen“.
func runOf(run, of int) string {
	verb := "laufen"
	if run == 1 {
		verb = "läuft"
	}
	return strconv.Itoa(run) + " von " + strconv.Itoa(of) + " " + verb
}
