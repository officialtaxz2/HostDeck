package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/officialtaxz2/hostdeck/internal/anmeldung"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/ort"
)

// passwortCmd legt das Leitstand-Passwort fest oder ändert es. Das geht nur
// hier, als root auf dem Server; die Website hat keine Seite dafür.
func passwortCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("passwort", flag.ContinueOnError)
	fl.SetOutput(stderr)
	socket := fl.String("dienst", ort.DienstSocket, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "hostdeck passwort läuft auf dem Server als root: sudo hostdeck passwort")
		return 2
	}
	c := dienst.NewClient(*socket)
	if _, err := c.PasswordState(ctx); err != nil {
		fmt.Fprintf(stderr, "Der HostDeck-Dienst antwortet nicht (%v).\nIst HostDeck eingerichtet? sudo ./hostdeck einrichten\n", err)
		return 1
	}
	if err := passwordDialog(ctx, c, newTTYPrompt(os.Stdin, stdout)); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	return 0
}

// prompt liest Eingaben im Terminal; Secret ohne Echo.
type prompt interface {
	Line(q string) (string, error)
	Secret(q string) (string, error)
	Say(format string, args ...any)
	Interactive() bool
}

type ttyPrompt struct {
	in  *os.File
	r   *bufio.Reader
	out io.Writer
}

func newTTYPrompt(in *os.File, out io.Writer) *ttyPrompt {
	return &ttyPrompt{in: in, r: bufio.NewReader(in), out: out}
}

func (p *ttyPrompt) Interactive() bool { return term.IsTerminal(int(p.in.Fd())) }

func (p *ttyPrompt) Say(format string, args ...any) { fmt.Fprintf(p.out, format, args...) }

func (p *ttyPrompt) Line(q string) (string, error) {
	fmt.Fprint(p.out, q)
	s, err := p.r.ReadString('\n')
	if err != nil && (s == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func (p *ttyPrompt) Secret(q string) (string, error) {
	if !p.Interactive() {
		return p.Line(q)
	}
	fmt.Fprint(p.out, q)
	b, err := term.ReadPassword(int(p.in.Fd()))
	fmt.Fprintln(p.out)
	return string(b), err
}

// passwordDialog schlägt ein erzeugtes Passwort vor oder nimmt ein eigenes
// und gibt es dem Dienst. Ohne Terminal liest es das Passwort aus der ersten
// Zeile der Eingabe (für Skripte).
func passwordDialog(ctx context.Context, c *dienst.Client, p prompt) error {
	if !p.Interactive() {
		pw, err := p.Line("")
		if err != nil {
			return fmt.Errorf("kein Passwort in der Eingabe: %w", err)
		}
		if err := c.SetPassword(ctx, pw); err != nil {
			return err
		}
		p.Say("Leitstand-Passwort festgelegt. Alle bisherigen Anmeldungen sind abgemeldet.\n")
		return nil
	}
	suggestion, err := anmeldung.Generate()
	if err != nil {
		return err
	}
	p.Say(`
Leitstand-Passwort festlegen

Mit diesem Passwort meldest du dich im Leitstand an. Mein Vorschlag, zufällig
erzeugt und sehr sicher:

    %s

Speichere ihn jetzt in deinem Passwort-Manager (oder schreib ihn auf).

`, suggestion)
	for {
		own, err := p.Secret(fmt.Sprintf("Enter übernimmt den Vorschlag. Oder tippe ein eigenes Passwort\n(mindestens %d Zeichen, beim Tippen siehst du nichts): ", anmeldung.MinLen))
		if err != nil {
			return err
		}
		pw := suggestion
		if own != "" {
			if err := anmeldung.Validate(own); err != nil {
				p.Say("%v Bitte noch einmal.\n\n", err)
				continue
			}
			again, err := p.Secret("Zur Sicherheit noch einmal: ")
			if err != nil {
				return err
			}
			if again != own {
				p.Say("Die beiden Eingaben sind verschieden. Bitte noch einmal.\n\n")
				continue
			}
			pw = own
		} else {
			ok, err := p.Line("Hast du den Vorschlag gespeichert? Dann tippe ja: ")
			if err != nil {
				return err
			}
			if strings.ToLower(strings.TrimSpace(ok)) != "ja" {
				p.Say("Noch nicht festgelegt. Speichere den Vorschlag oder tippe ein eigenes Passwort.\n\n")
				continue
			}
		}
		if err := c.SetPassword(ctx, pw); err != nil {
			return err
		}
		p.Say("\nLeitstand-Passwort festgelegt. Alle bisherigen Anmeldungen sind abgemeldet.\n")
		return nil
	}
}
