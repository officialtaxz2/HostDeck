package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/web"
)

const defaultPort = 7322

func serveCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, usage) }
	out := fl.String("out", "hostdeck-ergebnis", "")
	port := fl.Int("port", defaultPort, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() > 0 {
		fmt.Fprintf(stderr, "Unerwartete Angabe %q.\n", fl.Arg(0))
		return 2
	}
	explicit := false
	fl.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "out" })
	if !explicit && serviceInstalled() {
		fmt.Fprintln(stderr, "HostDeck läuft hier als Dienst; der Leitstand läuft schon. Öffnen mit: sudo hostdeck notausgang")
		return 2
	}
	if *port < 1 || *port > 65535 {
		fmt.Fprintf(stderr, "Port %d ist ungültig.\n", *port)
		return 2
	}
	token, err := web.NewToken()
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	srv := &web.Server{Dir: *out, Token: token, Log: stdout}
	if err := srv.Check(); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		if errors.Is(err, web.ErrNoInventory) {
			fmt.Fprintf(stderr, "Bitte zuerst die Bestandsaufnahme machen: sudo ./hostdeck discover\n")
			if *out != "hostdeck-ergebnis" {
				fmt.Fprintf(stderr, "(mit --out %s, falls du einen anderen Ordner nutzt)\n", *out)
			}
		}
		return 1
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: Port %d ist belegt oder nicht nutzbar (%v).\nAnderen Port wählen, z. B.: ./hostdeck serve --port %d\n", *port, err, *port+1)
		return 1
	}
	actual := ln.Addr().(*net.TCPAddr).Port
	return runServe(ctx, srv, ln, actual, stdout, stderr)
}

func runServe(ctx context.Context, srv *web.Server, ln net.Listener, port int, stdout, stderr io.Writer) int {
	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	if os.Geteuid() == 0 {
		fmt.Fprintln(stdout, "Hinweis: hostdeck serve braucht kein root; ./hostdeck serve ohne sudo reicht.")
	}
	fmt.Fprint(stdout, serveHelp(port, srv.Token, os.Getenv("SSH_CONNECTION"), sshUser()))

	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdown)
		fmt.Fprintln(stdout, "\nhostdeck serve beendet.")
		return 0
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return 0
		}
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
}

// serveHelp erklärt, wie man die Seite vom eigenen PC aus öffnet. Die
// Adresse des Servers kommt aus SSH_CONNECTION der aktuellen Sitzung.
func serveHelp(port int, token, sshConn, user string) string {
	target := "<server>"
	sshPort := ""
	if f := strings.Fields(sshConn); len(f) == 4 {
		target = f[2]
		if f[3] != "22" {
			sshPort = " -p " + f[3]
		}
	}
	if user == "" {
		user = "<benutzer>"
	}
	p := strconv.Itoa(port)
	var b strings.Builder
	fmt.Fprintf(&b, `
hostdeck serve läuft. Die Seite ist nur auf diesem Server erreichbar (127.0.0.1:%[1]s).

So öffnest du sie auf deinem PC:

  1. Auf dem PC ein neues Terminal öffnen (Windows: PowerShell) und eingeben:

       ssh -N -L %[1]s:127.0.0.1:%[1]s%[2]s %[3]s@%[4]s

     Das Fenster bleibt danach leer, das ist richtig. Offen lassen.

  2. Im Browser diese Adresse öffnen:

       http://127.0.0.1:%[1]s/#%[5]s

Der Link gilt nur, solange dieses hostdeck serve läuft. Beenden mit Strg+C.

`, p, sshPort, user, target, token)
	return b.String()
}

func sshUser() string {
	for _, k := range []string{"SUDO_USER", "USER", "LOGNAME"} {
		if v := os.Getenv(k); v != "" && v != "root" {
			return v
		}
	}
	return os.Getenv("USER")
}
