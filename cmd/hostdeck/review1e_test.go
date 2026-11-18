package main

// Regressionstests zu früheren Funden. Rot heißt: Befund
// bestätigt; sie prüfen das gewünschte Verhalten.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/restore"
)

// stummerServer nimmt das Passwort an und startet den Befehl, sagt danach
// aber nichts mehr (etwa: Leitung weg ohne RST, hängendes Startskript).
func stummerServer(t *testing.T) (string, int) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				cfg := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
				cfg.AddHostKey(signer)
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				defer sc.Close()
				go func() {
					for r := range reqs {
						// Weiterleitung zusagen (Port 1), sonst nichts.
						r.Reply(r.Type == "tcpip-forward", ssh.Marshal(struct{ P uint32 }{40001}))
					}
				}()
				for nc := range chans {
					ch, creqs, err := nc.Accept()
					if err != nil {
						return
					}
					go func() {
						for r := range creqs {
							r.Reply(r.Type == "exec", nil)
						}
					}()
					_ = ch // schweigt
				}
			}(c)
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	var p int
	fmt.Sscan(port, &p)
	return host, p
}

// Befund M2: Abbrechen wirkt zwischen Anmeldung und Gespräch nicht.
func TestReview1eAbbrechenWaehrendStart(t *testing.T) {
	m, _, _ := moveRig(t)
	host, port := stummerServer(t)
	target := dienst.MoveTarget{Host: host, Port: port, User: "root", Password: "x"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out syncBuilder
	done := make(chan int, 1)
	go func() { done <- m.Run(ctx, target, dienst.MovePlan, &autoInput{}, &out) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "angemeldet als root") {
		if time.Now().After(deadline) {
			t.Fatalf("Anmeldung kam nicht zustande:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("Run kehrt nach Abbrechen nicht zurück (Laufsperre bleibt belegt):\n%s", out.String())
	}
}

// Befund N1 (Schutz vorhanden): Ein Probe-Umzug auf diesen Server selbst
// endet beim Agenten an der Laufsperre, die der Dienst hält.
func TestReview1eZielIstDieserServer(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("serverRestore braucht root")
	}
	dir := t.TempDir()
	t.Setenv("HOSTDECK_RUN_DIR", filepath.Join(dir, "run"))
	t.Setenv("HOSTDECK_STATE_DIR", filepath.Join(dir, "state"))
	held, err := lauf.TakeIn(lauf.Dir(), "Probe-Umzug auf 203.0.113.180")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	var out strings.Builder
	code := serverRestore(context.Background(), restoreRun{stdout: &out, input: &autoInput{}, password: "x"})
	if code != restore.ExitBlocked || !strings.Contains(out.String(), "Probe-Umzug") {
		t.Fatalf("Code %d:\n%s", code, out.String())
	}
	t.Logf("Agent auf demselben Server: %s", strings.TrimSpace(out.String()))
}
