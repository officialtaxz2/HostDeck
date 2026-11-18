package restore

import (
	"reflect"
	"strings"
	"testing"
)

// feed schreibt s in Stücken der Größe n, damit Zeilen über Write-Grenzen gehen.
func feed(c DumpCounter, s string, n int) {
	for len(s) > 0 {
		k := min(n, len(s))
		c.Write([]byte(s[:k]))
		s = s[k:]
	}
}

const pgDump = `--
-- PostgreSQL database cluster dump
--
\connect template1
COPY public.nichtzaehlen (id) FROM stdin;
1
\.
\connect -reuse-previous=on "dbname='shop'"
COPY public.kunden (id, name) FROM stdin;
1	Anna
2	Ben (mit Klammer
3	\.nicht das Ende
\.
COPY "public"."Bestellungen" (id) FROM stdin;
1
\.
COPY lager.artikel (id, beschreibung) FROM stdin;
` + "1\t" + "%LANG%" + `
\.
COPY public.leer (id) FROM stdin;
\.
\connect "dbname='o''brien'"
COPY public.t (id) FROM stdin;
7
\.
`

func TestPgCounter(t *testing.T) {
	// Eine Zeile länger als maxHead darf nicht als Befehl gelten.
	dump := strings.Replace(pgDump, "%LANG%", strings.Repeat("x", 3*maxHead), 1)
	want := Counts{
		"shop/public.kunden":       3,
		"shop/public.Bestellungen": 1,
		"shop/lager.artikel":       1,
		"shop/public.leer":         0,
		"o'brien/public.t":         1,
	}
	for _, n := range []int{1, 7, 4096, len(dump)} {
		c := NewDumpCounter("postgres")
		feed(c, dump, n)
		if got := c.Counts(); !reflect.DeepEqual(got, want) {
			t.Errorf("Stücke %d: %v, erwartet %v", n, got, want)
		}
	}
}

const myDump = "-- MySQL dump\n" +
	"USE `mysql`;\n" +
	"INSERT INTO `user` VALUES ('root','x'),('a','b');\n" +
	"CREATE DATABASE `blog`;\n" +
	"USE `blog`;\n" +
	"INSERT INTO `posts` VALUES (1,'Hallo (Welt)','it''s; ok'),(2,'a\\'b)',NULL),(3,\"x\\\"(\",'');\n" +
	"INSERT INTO `posts` VALUES (4,'zweiter Block');\n" +
	"INSERT INTO `kom``mentare` VALUES (1,POINT(1,2));\n" +
	"USE `stats`;\n" +
	"INSERT INTO `besuche` VALUES (1),(2);\n"

func TestMysqlCounter(t *testing.T) {
	want := Counts{"blog/posts": 4, "blog/kom`mentare": 1, "stats/besuche": 2}
	for _, n := range []int{1, 5, len(myDump)} {
		c := NewDumpCounter("mariadb")
		feed(c, myDump, n)
		if got := c.Counts(); !reflect.DeepEqual(got, want) {
			t.Errorf("Stücke %d: %v, erwartet %v", n, got, want)
		}
	}
	if NewDumpCounter("mongo") != nil {
		t.Error("für unbekannte Datenbanken gibt es keinen Zähler")
	}
}

func TestCountsDiff(t *testing.T) {
	want := Counts{"a/x": 3, "a/leer": 0, "a/weg": 2}
	got := Counts{"a/x": 3, "a/neu": 0}
	if d := got.Diff(want); !reflect.DeepEqual(d, []string{"a/weg fehlt (im Backup 2 Zeilen)"}) {
		t.Errorf("Diff: %v", d)
	}
	got = Counts{"a/x": 4, "a/weg": 2, "a/mehr": 1}
	d := got.Diff(want)
	if !reflect.DeepEqual(d, []string{"a/mehr: 1 statt 0 Zeilen", "a/x: 4 statt 3 Zeilen"}) {
		t.Errorf("Diff: %v", d)
	}
	if want.Rows() != 5 {
		t.Errorf("Rows: %d", want.Rows())
	}
}

func TestParseCounts(t *testing.T) {
	c, err := ParseCounts([]byte("shop|public.kunden|1234\n\nblog|posts| 2 \n"))
	if err != nil || !reflect.DeepEqual(c, Counts{"shop/public.kunden": 1234, "blog/posts": 2}) {
		t.Errorf("%v %v", c, err)
	}
	for _, bad := range []string{"a|b", "a|b|zwei"} {
		if _, err := ParseCounts([]byte(bad)); err == nil {
			t.Errorf("%q: Fehler erwartet", bad)
		}
	}
}
