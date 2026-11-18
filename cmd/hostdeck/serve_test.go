package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/fixture"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

func TestServeHilfe(t *testing.T) {
	h := serveHelp(7322, "abc", "198.51.100.7 51234 203.0.113.5 22", "demo")
	for _, want := range []string{"ssh -N -L 7322:127.0.0.1:7322 demo@203.0.113.5", "http://127.0.0.1:7322/#abc", "Strg+C"} {
		if !strings.Contains(h, want) {
			t.Errorf("Hilfe ohne %q:\n%s", want, h)
		}
	}
	h = serveHelp(8000, "abc", "198.51.100.7 51234 203.0.113.5 2222", "")
	if !strings.Contains(h, "ssh -N -L 8000:127.0.0.1:8000 -p 2222 <benutzer>@203.0.113.5") {
		t.Errorf("SSH-Port fehlt:\n%s", h)
	}
	if h := serveHelp(7322, "abc", "", "demo"); !strings.Contains(h, "demo@<server>") {
		t.Errorf("ohne SSH_CONNECTION:\n%s", h)
	}
}

func TestServeOhneBestandsaufnahme(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"serve", "--out", t.TempDir()}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "sudo ./hostdeck discover") {
		t.Errorf("Exit %d\n%s", code, stderr.String())
	}
}

// Ganzer Ablauf: discover schreibt den Ordner, serve liefert ihn aus und
// lauscht nur auf 127.0.0.1.
func TestServeNachDiscover(t *testing.T) {
	out := filepath.Join(t.TempDir(), "hostdeck-ergebnis")
	if code, stdout, stderr := discoverOnce(t, fixture.Beispiel(t, 1), out); code != 0 {
		t.Fatalf("discover: %d\n%s\n%s", code, stdout, stderr)
	}
	srv := &web.Server{Dir: out, Token: strings.Repeat("a", 32)}
	if err := srv.Check(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	done := make(chan int)
	go func() { done <- runServe(ctx, srv, ln, port, &stdout, &stderr) }()

	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/state", nil)
	req.Header.Set("Authorization", "Bearer "+srv.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var st web.State
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(st.Projects) != 12 || st.Manifest.State != web.ManifestOK {
		t.Errorf("%d Projekte, Manifest %q", len(st.Projects), st.Manifest.State)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("Exit %d: %s", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve endet nicht")
	}
	if !strings.Contains(stdout.String(), "/#"+srv.Token) {
		t.Errorf("Link fehlt:\n%s", stdout.String())
	}
}
