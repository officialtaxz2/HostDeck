package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/hinweise"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/melden"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/runlog"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
	"github.com/officialtaxz2/hostdeck/internal/zustand"
)

// monitorRules verbindet die Warnungen mit den Teilen des
// Dienstes: Container, Domains, Backups, Ports und Protokoll.
func monitorRules(d *dienst.Dienst, cont *betrieb.Service, dm *domains.Service, out *melden.Service, result, data string) *ueberwachung.Rules {
	r := &ueberwachung.Rules{
		File:    filepath.Join(data, ort.MonitorDir, "warnungen.json"),
		Journal: &ueberwachung.Journal{File: filepath.Join(data, ort.MonitorDir, "ereignisse.json")},
		Boot:    func() time.Time { return ueberwachung.BootTime("") },
		Busy: func() string {
			// Am Umzugstag und danach sind die Projekte hier absichtlich aus;
			// ein Leitstand, der erst übernimmt, wenn die Domains zu ihm
			// zeigen, sieht bis dahin keine Seite antworten.
			if why := dayBusy(filepath.Join(data, ort.MoveDir)); why != "" {
				return why
			}
			if c := copyInfo(data); c != nil && c.Move {
				return "Umzug (wartet auf die DNS-Umstellung)"
			}
			if d == nil {
				return ""
			}
			info, err := lauf.CurrentIn(d.RunDir)
			if err != nil || info == nil {
				return ""
			}
			return info.What
		},
		LastBackup: func() (time.Time, bool) {
			list, err := runlog.Load(result)
			if err != nil {
				return time.Time{}, false
			}
			for _, e := range list { // neueste zuerst
				if e.Valid {
					return e.Started, true
				}
			}
			return time.Time{}, false
		},
	}
	if cont != nil {
		r.Containers = func(ctx context.Context) ([]ueberwachung.ContainerState, error) {
			snap, err := cont.States(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]ueberwachung.ContainerState, 0, len(snap.Containers))
			for _, c := range snap.Containers {
				out = append(out, ueberwachung.ContainerState{Name: c.Name, Project: c.Project, State: c.State, Running: c.Running,
					StoppedByYou: c.StoppedByYou != nil || c.StoppedByMove, Job: c.Job, Managed: c.ManagedBy != "", ExitCode: c.ExitCode, Restarts: c.Restarts})
			}
			return out, nil
		}
	}
	if dm != nil {
		r.Sites = func(ctx context.Context) ([]ueberwachung.SiteCheck, error) {
			sites, err := dm.CheckNow(ctx)
			if err != nil {
				return nil, err
			}
			// Pausiert ist, was die eine Quelle (zustand) so nennt: Ziel
			// oder ganzes Projekt von dir gestoppt.
			var conts []betrieb.Container
			if cont != nil {
				if snap, err := cont.States(ctx); err == nil {
					conts = snap.Containers
				}
			}
			// Jede Domain einzeln, auch weitere Domains eines Caddy-Blocks:
			// dieselbe Liste wie im Leitstand.
			states := zustand.Domains(sites, conts, loadInventory(result), time.Now())
			out := make([]ueberwachung.SiteCheck, 0, len(states))
			for _, z := range states {
				lv := z.Live
				if lv == nil || z.State == zustand.Aus || z.State == zustand.Ungeprueft {
					continue
				}
				sc := ueberwachung.SiteCheck{Host: z.Host, OK: lv.OK, Answer: lv.Answer, Took: lv.Took, CertUntil: lv.CertUntil}
				switch {
				case z.State == zustand.Pausiert:
					sc.Paused, sc.PausedSince = z.Text, z.PausedSince
				case z.State == zustand.Startet:
					// Der Container läuft erst seit kurzem: Ein Fehlversuch
					// zählt noch nicht, wie im Zustand der Domain.
					sc.Starting = true
				case z.Level == zustand.LevelWarn:
					sc.Hint = z.Text
				case z.Level == zustand.LevelErr:
					sc.Word, sc.Why = z.Word, z.Text
				}
				out = append(out, sc)
			}
			return out, nil
		}
	}
	if sec := d.Security; sec != nil {
		// Die Regeln von HostDeck für geschlossene Ports fehlen: höchstens jede
		// Minute nachsehen.
		var mu sync.Mutex
		var at time.Time
		var lost bool
		r.Extra = func(ctx context.Context) []ueberwachung.Finding {
			mu.Lock()
			defer mu.Unlock()
			if time.Since(at) >= time.Minute {
				at = time.Now()
				lost = sec.Overview(ctx).Firewall.OwnLost
			}
			if !lost {
				return nil
			}
			return []ueberwachung.Finding{{Key: "schutz-verloren", Level: ueberwachung.LevelErr, Icon: "sicherheit", Title: "Geschlossene Ports sind wieder offen",
				Text: "Die Regeln von HostDeck fehlen gerade, etwa nach einem Neustart von nftables. Unter Sicherheit lädst du sie neu.",
				Link: "#/sicherheit", Go: "Neu laden"}}
		}
	}
	host, _ := os.Hostname()
	r.Event = func(e ueberwachung.Event) {
		// Stille Hinweise (etwa: eine Warnung endet, weil du das Projekt
		// gestoppt hast) stehen nur im Verlauf und im Protokoll.
		if out != nil && !e.Quiet {
			out.Send(eventMessage(e, out.Base, host))
		}
		if d.Journal == nil {
			return
		}
		text := eventText(e)
		ent := protokoll.Entry{Kind: protokoll.Ueberwachung, Text: text, Addr: "Server (Überwachung)"}
		if e.Text != "" {
			ent.Details = []string{e.Text}
		}
		_ = d.Journal.Add(ent)
	}
	return r
}

// eventText ist die Zeile im Protokoll.
func eventText(e ueberwachung.Event) string {
	switch e.Kind {
	case ueberwachung.EvStart:
		if e.Level == ueberwachung.LevelErr {
			return "Warnung (rot): " + e.Title
		}
		return "Warnung: " + e.Title
	case ueberwachung.EvWorse:
		return "Jetzt dringend: " + e.Title
	case ueberwachung.EvRemind:
		return "Hält noch an: " + e.Title
	case ueberwachung.EvClear:
		return "Entwarnung: " + strings.TrimPrefix(e.Title, "Wieder in Ordnung: ")
	case ueberwachung.EvNote:
		return "Hinweis: " + e.Title
	}
	return e.Title
}

// eventMessage ist die Nachricht nach außen: Anlass, Server und ein Link
// zum Leitstand, nie Inhalte von Projekten.
func eventMessage(e ueberwachung.Event, base func() string, host string) melden.Message {
	m := melden.Message{Title: eventText(e), Text: e.Text, Tag: e.Key}
	switch {
	case e.Kind == ueberwachung.EvClear:
		m.Priority, m.Tags = 2, []string{"white_check_mark"}
	case e.Kind == ueberwachung.EvNote:
		m.Priority, m.Tags = 2, []string{"information_source"}
	case e.Kind == ueberwachung.EvWorse || e.Level == ueberwachung.LevelErr:
		m.Priority, m.Tags = 4, []string{"rotating_light"}
	default:
		m.Priority, m.Tags = 3, []string{"warning"}
	}
	if host != "" {
		if m.Text != "" {
			m.Text += "\n"
		}
		m.Text += "Server: " + host
	}
	if base != nil {
		if b := base(); b != "" {
			link := e.Link
			if !strings.HasPrefix(link, "#/") {
				link = "#/ueberwachung"
			}
			m.Click = b + "/" + link
		}
	}
	return m
}

// loadInventory liest die letzte Bestandsaufnahme; fehlt sie, nil.
func loadInventory(result string) *inventory.Inventory {
	b, err := os.ReadFile(filepath.Join(result, "inventory.json"))
	if err != nil {
		return nil
	}
	var inv inventory.Inventory
	if json.Unmarshal(b, &inv) != nil {
		return nil
	}
	return &inv
}

// alertHints macht aus laufenden Warnungen Hinweise für „Braucht dich“.
// Zeiten stehen darin nur als Dauer, nie als Uhrzeit: Der Server rechnet in
// seiner Zeitzone, der Leitstand in der des Browsers.
func alertHints(r *ueberwachung.Rules) func() []hinweise.Hint {
	return func() []hinweise.Hint {
		v := r.View(0)
		out := make([]hinweise.Hint, 0, len(v.Active))
		for _, a := range v.Active {
			text, lvl, icon := a.Text, a.Level, a.Icon
			if a.GoodFrom != nil {
				// Wieder in Ordnung: wie unter Überwachung blau mit Haken,
				// nicht mehr gelb in „Braucht dich“.
				lvl, icon = hinweise.Acc, "done"
				since := "Gerade eben"
				if d := time.Since(*a.GoodFrom); d >= 30*time.Second {
					since = "Seit " + ueberwachung.Duration(d)
				}
				text += " " + since + " wieder in Ordnung; die Entwarnung kommt nach " + ueberwachung.Duration(v.Limits.ClearAfter) + "."
			}
			out = append(out, hinweise.Hint{Key: a.Key, Level: lvl, Icon: icon, Title: a.Title, Text: text, Link: a.Link, Go: a.Go,
				CanKeep: a.CanAccept && a.GoodFrom == nil, Kept: a.Accepted})
		}
		return out
	}
}
