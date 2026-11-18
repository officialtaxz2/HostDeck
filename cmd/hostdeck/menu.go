package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/pc"
	"golang.org/x/term"
)

// console ist die eine Konsole des Programms. Mehrere hätten je einen
// eigenen Puffer und würden sich Eingaben wegnehmen.
var console = &pc.StdConsole{}

const menuText = `
HostDeck – Backup deines Servers auf diesen PC

  1  Probelauf: zeigt nur, was ein Backup tun würde
  2  Backup jetzt machen
  3  Letzter Lauf am Umzugstag: Dienste bleiben danach aus
  4  Dienste wieder starten (nach Abbruch oder abgesagtem Umzug)
  5  Backup-Ordner neu wählen
  6  Server-Adresse ändern

  Auf dem neuen Server
  7  Backup dorthin zurückspielen (zeigt erst, was passiert)
  8  Prüfen, ob dort alles läuft und die Daten stimmen

  0  Beenden

Deine Wahl: `

const finalWarning = `
Beim letzten Lauf bleiben alle Dienste, die mit umziehen, nach dem Backup
aus. Sie laufen erst wieder auf dem neuen Server, oder hier mit Punkt 4.
Wirklich starten? (ja/nein): `

// lineReader liest eine Zeile von der Konsole.
type lineReader interface {
	ReadLine() (string, error)
}

// doubleClicked meldet, ob hostdeck.exe per Doppelklick ohne Befehl gestartet
// wurde. Dann gibt es ein Menü statt der Hilfe, und das Fenster bleibt am
// Ende offen.
func doubleClicked() bool {
	return runtime.GOOS == "windows" && term.IsTerminal(int(os.Stdin.Fd()))
}

// menu zeigt die Auswahl und führt Punkte aus. Nach einer Einstellung
// (Ordner, Server) kommt das Menü wieder. Nach einem Lauf wartet es auf
// Enter und schließt dann: Ein zweiter Lauf im selben Fenster könnte sich
// mit ssh um Eingaben wie das Passwort streiten.
func menu(con lineReader, stdout io.Writer, do func(args []string) int) int {
	pc.PrepareConsole()
	for {
		fmt.Fprint(stdout, menuText)
		l, err := con.ReadLine()
		if err != nil {
			return 0
		}
		var args []string
		switch strings.TrimSpace(l) {
		case "1":
			args = []string{"backup", "--plan"}
		case "2":
			args = []string{"backup"}
		case "3":
			fmt.Fprint(stdout, finalWarning)
			a, err := con.ReadLine()
			if err != nil {
				return 0
			}
			if !strings.EqualFold(strings.TrimSpace(a), "ja") {
				fmt.Fprintln(stdout, "Nicht gestartet.")
				continue
			}
			args = []string{"backup", "--letzter-lauf"}
		case "4":
			args = []string{"dienste-starten"}
		case "7":
			args = []string{"restore"}
		case "8":
			args = []string{"verify"}
		case "5", "6":
			fmt.Fprintln(stdout)
			if strings.TrimSpace(l) == "5" {
				do([]string{"ordner"})
			} else {
				do([]string{"server"})
			}
			continue
		case "0":
			return 0
		case "":
			continue
		default:
			fmt.Fprintln(stdout, "Bitte eine Zahl von 0 bis 8 eingeben.")
			continue
		}
		fmt.Fprintln(stdout)
		code := do(args)
		if code == 0 {
			fmt.Fprint(stdout, "\nFertig. Zum Schließen Enter drücken.")
		} else {
			fmt.Fprint(stdout, "\nNicht geklappt, siehe oben. Zum Schließen Enter drücken.")
		}
		_, _ = con.ReadLine()
		return code
	}
}
