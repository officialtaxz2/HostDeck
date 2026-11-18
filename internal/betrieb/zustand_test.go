package betrieb

import (
	"testing"
	"time"
)

// T8: Jede Kombination von Containern
// hat genau ein Wort und eine Stufe, an einer Stelle gerechnet. „von dir
// gestoppt“ geht vor „angelegt“; ein erledigter Job zählt nie als „läuft
// nicht“; verwaltete Server sind nie rot.
func TestGruppenZustand(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour)
	fresh := now.Add(-3 * time.Minute)
	stop := now.Add(-time.Hour)
	run := func(n string) Container {
		return Container{Name: n, Project: "p", State: "running", Running: true, Since: &old}
	}
	exited := func(n string, code int) Container {
		return Container{Name: n, Project: "p", State: "exited", ExitCode: code, Since: &old}
	}
	byYou := func(c Container) Container { c.StoppedByYou = &stop; return c }
	created := func(n string) Container { return Container{Name: n, Project: "p", State: "created"} }
	job := func(n string) Container { c := exited(n, 0); c.Job = true; return c }
	sick := func(n string) Container { c := run(n); c.Health = "unhealthy"; return c }
	loop := func(n string) Container { c := run(n); c.Restarts = 5; c.Since = &fresh; return c }
	moved := func(c Container) Container { c.StoppedByMove = true; return c }
	panel := func(n string, on bool) Container {
		c := Container{Name: n, State: "exited", ManagedBy: "panel"}
		if on {
			c.State, c.Running = "running", true
		}
		return c
	}

	cases := []struct {
		name  string
		cs    []Container
		word  string
		level string
		text  string
	}{
		{"läuft", []Container{run("a")}, WortLaeuft, StufeOK, ""},
		{"beendet und von dir gestoppt", []Container{byYou(exited("a", 0))}, WortVonDir, StufeIdle, ""},
		{"angelegt", []Container{created("a")}, WortAngelegt, StufeIdle, ""},
		{"angelegt und von dir gestoppt", []Container{byYou(created("a"))}, WortVonDir, StufeIdle, ""},
		{"Job erledigt", []Container{job("a")}, WortJob, StufeIdle, ""},
		{"Job neben laufendem Dienst", []Container{run("a"), job("b")}, WortLaeuft, StufeOK, "2 Container: 1 läuft, 1 Job erledigt"},
		{"seit dem letzten Lauf aus", []Container{moved(exited("a", 0))}, WortLetzterLauf, StufeIdle, ""},
		{"Neustart-Schleife", []Container{loop("a"), run("b")}, WortSchleife, StufeErr, "2 Container: 1 läuft, 1 in Neustart-Schleife"},
		{"unhealthy", []Container{sick("a")}, WortUnhealthy, StufeWarn, ""},
		{"unerwartet gestoppt", []Container{exited("a", 1)}, WortUnerwartet, StufeErr, ""},
		{"gemischt: einer unerwartet aus", []Container{run("a"), exited("b", 137)}, "1 von 2 läuft", StufeErr, "2 Container: 1 läuft, 1 unerwartet gestoppt"},
		{"gemischt: einer von dir gestoppt", []Container{run("a"), run("b"), byYou(exited("c", 0))}, "2 von 3 laufen", StufeOK, "3 Container: 2 laufen, 1 von dir gestoppt"},
		{"gemischt: einer unhealthy, einer von dir gestoppt", []Container{run("a"), sick("b"), byYou(exited("c", 0))}, "2 von 3 laufen", StufeWarn, "3 Container: 1 läuft, 1 „unhealthy“, 1 von dir gestoppt"},
		{"ganz von dir gestoppt, mit angelegtem", []Container{byYou(exited("a", 0)), created("b")}, WortVonDir, StufeIdle, "2 Container: 1 von dir gestoppt, 1 angelegt"},
		{"verwaltete Server, einer aus", []Container{panel("x", true), panel("y", false)}, "1 von 2 läuft", StufeOK, "2 Container: 1 läuft, 1 aus"},
		{"verwaltete Server, alle aus", []Container{panel("x", false)}, WortAus, StufeIdle, ""},
	}
	for _, c := range cases {
		cs := append([]Container(nil), c.cs...)
		for i := range cs {
			cs[i].classify(now)
		}
		gs := Groups(cs)
		if len(gs) != 1 {
			t.Fatalf("%s: %d Gruppen", c.name, len(gs))
		}
		g := gs[0]
		if g.Word != c.word || g.Level != c.level || g.Text != c.text {
			t.Errorf("%s: %q/%s/%q, erwartet %q/%s/%q", c.name, g.Word, g.Level, g.Text, c.word, c.level, c.text)
		}
	}
}

// Jeder Container hat genau ein Wort; der Grund eines Absturzes steht daneben.
func TestContainerWort(t *testing.T) {
	now := time.Now()
	stop := now.Add(-time.Hour)
	cases := []struct {
		c          Container
		class, why string
	}{
		{Container{State: "running", Running: true}, KlasseLaeuft, ""},
		{Container{State: "exited", ExitCode: 2}, KlasseUnerwartet, "abgestürzt (Code 2)"},
		{Container{State: "exited", ExitCode: 143}, KlasseUnerwartet, ""},
		{Container{State: "created", StoppedByYou: &stop}, KlasseVonDir, ""},
		{Container{State: "exited", Job: true, StoppedByMove: true}, KlasseLetzterLauf, ""},
		{Container{State: "restarting", Running: true, Restarts: 1}, KlasseSchleife, "1 Neustart"},
	}
	for i, x := range cases {
		x.c.classify(now)
		if x.c.Class != x.class || x.c.Why != x.why || x.c.Word == "" || x.c.Level == "" {
			t.Errorf("%d: %+v, erwartet %s/%q", i, x.c, x.class, x.why)
		}
	}
}

// Die Kachel zählt alle eigenen Container; verwaltete Server und Jobs
// stehen nicht unter „läuft nicht“.
func TestSummeKachel(t *testing.T) {
	now := time.Now()
	stop := now.Add(-time.Hour)
	cs := []Container{
		{Name: "a", Project: "x", State: "running", Running: true},
		{Name: "b", Project: "x", State: "exited", Job: true},
		{Name: "c", Project: "y", State: "exited", StoppedByYou: &stop},
		{Name: "d", State: "running", Running: true, ManagedBy: "panel"},
	}
	for i := range cs {
		cs[i].classify(now)
	}
	s := Sum(cs)
	if s.Total != 3 || s.Running != 1 || s.Jobs != 1 || s.ByYou != 1 || s.Relevant() != 2 {
		t.Errorf("Summe: %+v", s)
	}
}
