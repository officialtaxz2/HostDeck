package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Das Docker-Skript kommt ohne curl auf den Server: frische Debian-Server
// haben es nicht immer.
func TestFetchFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("#!/bin/sh\necho docker\n"))
		case "/gross":
			w.Write([]byte(strings.Repeat("x", 8<<20+10)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	ctx := context.Background()
	p := filepath.Join(dir, "ok.sh")
	if err := fetchFile(ctx, srv.URL+"/ok", p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "#!/bin/sh\necho docker\n" {
		t.Fatalf("Inhalt: %q", b)
	}
	if err := fetchFile(ctx, srv.URL+"/ok", p); err == nil {
		t.Fatal("vorhandene Datei überschrieben")
	}
	for _, x := range []string{"/fehlt", "/gross"} {
		q := filepath.Join(dir, strings.TrimPrefix(x, "/"))
		if err := fetchFile(ctx, srv.URL+x, q); err == nil {
			t.Fatalf("%s: kein Fehler", x)
		}
		if _, err := os.Stat(q); !os.IsNotExist(err) {
			t.Fatalf("%s: Datei bleibt liegen", x)
		}
	}
}
