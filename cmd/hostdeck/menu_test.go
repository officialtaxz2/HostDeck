package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeLines struct{ l []string }

func (f *fakeLines) ReadLine() (string, error) {
	if len(f.l) == 0 {
		return "", io.EOF
	}
	l := f.l[0]
	f.l = f.l[1:]
	return l, nil
}

func TestMenu(t *testing.T) {
	cases := []struct {
		in   []string
		want [][]string
		code int
	}{
		{[]string{"1", ""}, [][]string{{"backup", "--plan"}}, 0},
		{[]string{"", "2", ""}, [][]string{{"backup"}}, 0},
		{[]string{"x", "4", ""}, [][]string{{"dienste-starten"}}, 0},
		{[]string{"7", ""}, [][]string{{"restore"}}, 0},
		{[]string{"9", "8", ""}, [][]string{{"verify"}}, 0},
		{[]string{"3", "nein", "3", "ja", ""}, [][]string{{"backup", "--letzter-lauf"}}, 1},
		{[]string{"6", "5", "1", ""}, [][]string{{"server"}, {"ordner"}, {"backup", "--plan"}}, 0},
		{[]string{"6", "0"}, [][]string{{"server"}}, 0},
		{[]string{"0"}, nil, 0},
		{nil, nil, 0},
	}
	for _, c := range cases {
		var got [][]string
		var out bytes.Buffer
		code := menu(&fakeLines{l: c.in}, &out, func(a []string) int {
			got = append(got, a)
			if slices.Contains(a, "--letzter-lauf") {
				return 1
			}
			return 0
		})
		if !slices.EqualFunc(got, c.want, slices.Equal) {
			t.Errorf("%v: Befehle %v, erwartet %v", c.in, got, c.want)
		}
		if code != c.code {
			t.Errorf("%v: Code %d, erwartet %d", c.in, code, c.code)
		}
		runs := len(c.want) > 0 && c.want[len(c.want)-1][0] != "server"
		if runs != strings.Contains(out.String(), "Enter drücken") {
			t.Errorf("%v: Warten vor dem Schließen falsch:\n%s", c.in, out.String())
		}
	}
}

func TestServerAdresse(t *testing.T) {
	cases := []struct {
		in   []string
		want string
		bad  bool
	}{
		{[]string{"root@203.0.113.10"}, "root@203.0.113.10", false},
		{[]string{"203.0.113.10", ""}, "root@203.0.113.10", false},
		{[]string{"203.0.113.10", "demo"}, "demo@203.0.113.10", false},
		{[]string{"ssh root@taxz.example"}, "root@taxz.example", false},
		{[]string{"-oProxyCommand=x"}, "", true},
		{[]string{"root@host name"}, "", true},
		{[]string{""}, "", true},
		{nil, "", true},
	}
	for _, c := range cases {
		var out bytes.Buffer
		got, err := askServer(&fakeLines{l: c.in}, &out)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%v: %q, %v", c.in, got, err)
		}
	}
}

func TestErgebnisordnerEineEbeneTiefer(t *testing.T) {
	if _, err := os.Stat("/root/hostdeck-ergebnis/manifest.yaml"); err == nil {
		t.Skip("/root/hostdeck-ergebnis gibt es hier wirklich")
	}
	root := t.TempDir()
	old := resultGlobs
	resultGlobs = []string{filepath.Join(root, "*", "hostdeck-ergebnis")}
	defer func() { resultGlobs = old }()
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SUDO_USER", "")

	if _, err := findResultDir(""); err == nil || !strings.Contains(err.Error(), "--out") {
		t.Fatalf("ohne Ergebnisordner: %v", err)
	}
	for i, d := range []string{"alt", "hostdeck"} {
		dir := filepath.Join(root, d, "hostdeck-ergebnis")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		m := filepath.Join(dir, "manifest.yaml")
		if err := os.WriteFile(m, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		ts := time.Date(2026, 10, 30+i, 12, 0, 0, 0, time.UTC)
		if err := os.Chtimes(m, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	got, err := findResultDir("")
	if err != nil || got != filepath.Join(root, "hostdeck", "hostdeck-ergebnis") {
		t.Fatalf("gefunden %q, %v; erwartet das neuere", got, err)
	}
}

func TestTunnelZielIstRestServer(t *testing.T) {
	if got := tunnelRepo(18800); got != "rest:http://127.0.0.1:18800/" {
		t.Errorf("restic-Ziel am Tunnel %q; ohne rest: wäre es ein Ordner auf dem Server", got)
	}
}
