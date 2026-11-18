package restore

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Verify prüft einen zurückgespielten Server: laufen die Dienste, sind die
// Ports erreichbar, stimmen die Zeilen der Datenbanken mit dem Backup?
// Neue Zeilen seit dem Zurückspielen sind normal und werden nur gemeldet.
func Verify(ctx context.Context, d Docker, stateDir string, w io.Writer) int {
	rec, err := LoadRecord(stateDir)
	if err != nil {
		fmt.Fprintf(w, "Fehler: %v\n", err)
		return ExitBlocked
	}
	fmt.Fprintf(w, "hostdeck verify: Lauf %s von %s, zurückgespielt am %s\n", rec.Run, rec.Source, rec.Restored.Local().Format("02.01.2006 15:04"))
	problems := 0
	for _, g := range rec.Groups {
		fmt.Fprintf(w, "\n%s\n", g.Name)
		// Was beim Zurückspielen schiefging, kann verify nicht neu prüfen;
		// es bleibt, bis du neu zurückspielst.
		for _, e := range g.Errors {
			fmt.Fprintf(w, "  ✗ beim Zurückspielen: %s\n", e)
			problems++
		}
		if g.Name == "(allgemein)" {
			continue
		}
		if g.Compose {
			cs, err := d.ProjectContainers(ctx, g.Name)
			if err != nil {
				fmt.Fprintf(w, "  ✗ Container: %v\n", err)
				problems++
			} else {
				for _, svc := range g.Services {
					state := "fehlt"
					for _, c := range cs {
						if c.Service == svc {
							state = c.State
						}
					}
					if state == "running" {
						fmt.Fprintf(w, "  ✓ %s läuft\n", svc)
					} else {
						fmt.Fprintf(w, "  ✗ %s: %s\n", svc, state)
						problems++
					}
				}
			}
		}
		for _, port := range g.Ports {
			if portOpen(ctx, port) {
				fmt.Fprintf(w, "  ✓ Port %d antwortet\n", port)
			} else {
				fmt.Fprintf(w, "  ✗ Port %d antwortet nicht\n", port)
				problems++
			}
		}
		for _, db := range g.DBs {
			if db.Failed {
				continue // steht oben schon als Fehler
			}
			if db.Counts == nil {
				fmt.Fprintf(w, "  – %s: %s\n", db.Title, orText(db.Note, "keine Zahlen aus dem Backup"))
				continue
			}
			sc, err := ScriptsFor(db.Engine)
			if err != nil {
				continue
			}
			id, err := d.ServiceContainer(ctx, g.Name, db.Service)
			if err != nil {
				fmt.Fprintf(w, "  ✗ %s: %v\n", db.Title, err)
				problems++
				continue
			}
			j := &Job{Docker: d}
			now, err := j.count(ctx, id, sc)
			if err != nil {
				fmt.Fprintf(w, "  ✗ %s zählen: %v\n", db.Title, err)
				problems++
				continue
			}
			diff := now.Diff(db.Counts)
			missing, grown := 0, 0
			for k, want := range db.Counts {
				if now[k] < want {
					missing++
				} else if now[k] > want {
					grown++
				}
			}
			switch {
			case len(diff) == 0:
				fmt.Fprintf(w, "  ✓ %s: %d Tabellen, %d Zeilen, wie im Backup\n", db.Title, len(db.Counts), db.Counts.Rows())
			case missing == 0:
				fmt.Fprintf(w, "  ✓ %s: alles aus dem Backup da, %d Tabellen sind seitdem gewachsen\n", db.Title, grown)
			default:
				fmt.Fprintf(w, "  ✗ %s: %d Tabellen haben weniger Zeilen als im Backup: %s\n", db.Title, missing, strings.Join(firstN(diff, 3), "; "))
				problems++
			}
		}
	}
	if problems > 0 {
		fmt.Fprintf(w, "\nErgebnis: %d Punkte stimmen nicht.\n", problems)
		return ExitFailed
	}
	fmt.Fprintf(w, "\nErgebnis: alles in Ordnung.\n")
	return ExitOK
}

func portOpen(ctx context.Context, port int) bool {
	var d net.Dialer
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func orText(s, def string) string {
	if s != "" {
		return s
	}
	return def
}
