package protokoll

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSchreibenLesenUndBegrenzen(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	l := &Log{File: filepath.Join(dir, "protokoll.jsonl"), MaxSize: 2000, Now: func() time.Time { return now }}
	for i := 0; i < 100; i++ {
		now = now.Add(time.Second)
		if err := l.Add(Entry{Kind: Anmeldung, Text: fmt.Sprintf("Eintrag %d", i), Addr: "Notausgang"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"protokoll.jsonl", "protokoll.1.jsonl"} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil || fi.Size() > 2000 || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, fi, err)
		}
	}
	got, err := l.Last(5)
	if err != nil || len(got) != 5 || got[0].Text != "Eintrag 99" || got[4].Text != "Eintrag 95" {
		t.Fatalf("%+v %v", got, err)
	}
	all, _ := l.Last(1000)
	if len(all) < 20 || len(all) >= 100 {
		t.Fatalf("Begrenzung: %d Einträge", len(all))
	}
}

func TestOhneDatei(t *testing.T) {
	l := &Log{File: filepath.Join(t.TempDir(), "p.jsonl")}
	got, err := l.Last(10)
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	var nl *Log
	if err := nl.Add(Entry{Text: "x"}); err != nil {
		t.Fatal(err)
	}
}
