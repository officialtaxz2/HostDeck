// Package hinweise sammelt, was im Leitstand Aufmerksamkeit braucht:
// Platte knapp, Arbeitsspeicher knapp, Zertifikat läuft ab,
// Container startet ständig neu, Ports mit hohem Risiko offen, eine
// Änderung an den Ports wartet oder die Regeln von HostDeck fehlen.
//
// Hinweise zeigen nur an; sie ändern nichts. Die Überwachung schickt
// dieselben Hinweise auf Wunsch nach außen.
package hinweise

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/sicherheit"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

// Stufen.
const (
	Err  = "err"
	Warn = "warn"
	// Acc: Eine Warnung ist wieder in Ordnung, die Entwarnung kommt noch.
	// Das braucht dich nicht mehr: blau mit Haken wie unter Überwachung,
	// zählt weder im Kopf noch an Glocke und Abzeichen.
	Acc = "acc"
)

// Hint ist ein Hinweis.
type Hint struct {
	Key   string `json:"schluessel"` // stabil, etwa platz:/ oder zertifikat
	Level string `json:"stufe"`      // err, warn, acc
	Icon  string `json:"symbol"`
	Title string `json:"titel"`
	Text  string `json:"text"`
	Link  string `json:"link,omitempty"` // Seite im Leitstand, etwa #/platz
	Go    string `json:"knopf,omitempty"`
	// CanKeep: Dazu gibt es „Bewusst so lassen“ (eine Warnung der
	// Überwachung, siehe ueberwachung.Acceptable).
	CanKeep bool `json:"so_lassen,omitempty"`
	// Kept: Du hast ihn bewusst so gelassen; er steht dann in
	// Reply.Kept statt in Reply.Hints.
	Kept *time.Time `json:"so_gelassen,omitempty"`
}

// Reply ist die Antwort auf GET /api/hinweise.
type Reply struct {
	Hints []Hint `json:"hinweise"`
	// Kept: was du bewusst so gelassen hast; zählt nirgends mit.
	Kept []Hint    `json:"so_gelassen"`
	At   time.Time `json:"stand_von"`
}

// Grenzen: dieselben wie die Regeln der Überwachung (eine Quelle, T10).
// Ohne Dienst gelten die Voreinstellungen; im Dienst setzt Limits die
// geänderten Grenzen ein. Neustart-Schleife wie im Betrieb.
const (
	LoopRestarts = betrieb.LoopRestarts // so viele Neustarts …
	LoopWindow   = time.Hour
	LoopFresh    = betrieb.LoopFresh // … oder so kurz läuft er seit dem letzten
)

// Service sammelt Hinweise. Jede Quelle ist optional.
type Service struct {
	// Disk misst Größe und freien Platz; DiskPaths sind die Orte (Standard
	// / und /var/lib/docker). Orte auf derselben Platte zählen einmal.
	Disk      func(path string) (total, free int64, err error)
	DiskPaths []string
	// Memory liefert Arbeitsspeicher gesamt und verfügbar.
	Memory     func() (total, avail int64)
	Containers func(ctx context.Context) (betrieb.Snapshot, error)
	Sites      func(ctx context.Context) []domains.Site
	Security   func(ctx context.Context) sicherheit.Overview
	Now        func() time.Time
	// Alerts sind die laufenden Warnungen der Überwachung.
	// Gesetzt, ersetzen sie Platte, Arbeitsspeicher, Neustarts und
	// Zertifikate; die Überwachung prüft dieselben Dinge mit Wartezeit
	// und Entwarnung, damit Leitstand und Nachricht dasselbe sagen.
	Alerts func() []Hint
	// MaxAge: so lange gilt ein Stand (Standard 30 s).
	MaxAge time.Duration
	// Limits sind die geltenden Grenzen der Überwachung; nil:
	// ueberwachung.DefaultLimits.
	Limits func() ueberwachung.Limits
	// Extra sind weitere Hinweise, etwa auf eine neue Version von HostDeck.
	Extra func() []Hint

	mu   sync.Mutex
	last *Reply
	seen map[string][]restartSample // Neustarts je Container
}

type restartSample struct {
	at time.Time
	n  int
}

func (s *Service) limits() ueberwachung.Limits {
	if s.Limits != nil {
		return s.Limits()
	}
	return ueberwachung.DefaultLimits
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Hints liefert die Hinweise; höchstens MaxAge alt.
func (s *Service) Hints(ctx context.Context) Reply {
	age := s.MaxAge
	if age == 0 {
		age = 30 * time.Second
	}
	s.mu.Lock()
	if s.last != nil && s.now().Sub(s.last.At) < age {
		r := *s.last
		s.mu.Unlock()
		return r
	}
	s.mu.Unlock()
	r := s.collect(ctx)
	s.mu.Lock()
	s.last = &r
	s.mu.Unlock()
	return r
}

// Forget verwirft den letzten Stand, etwa nachdem du eine Warnung bewusst so
// gelassen hast; die nächste Abfrage sammelt neu.
func (s *Service) Forget() {
	s.mu.Lock()
	s.last = nil
	s.mu.Unlock()
}

func (s *Service) collect(ctx context.Context) Reply {
	r := Reply{At: s.now(), Hints: []Hint{}, Kept: []Hint{}}
	add := func(h ...Hint) {
		for _, x := range h {
			if x.Kept != nil {
				r.Kept = append(r.Kept, x)
			} else {
				r.Hints = append(r.Hints, x)
			}
		}
	}
	if s.Alerts != nil {
		alerts := s.Alerts()
		add(alerts...)
		if s.Security != nil {
			have := map[string]bool{}
			for _, a := range alerts {
				have[a.Key] = true
			}
			for _, h := range security(s.Security(ctx)) {
				if !have[h.Key] {
					add(h)
				}
			}
		}
		if s.Extra != nil {
			add(s.Extra()...)
		}
		sort.SliceStable(r.Hints, func(i, j int) bool { return r.Hints[i].Level == Err && r.Hints[j].Level != Err })
		return r
	}
	add(s.disk()...)
	add(s.memory()...)
	if s.Containers != nil {
		if snap, err := s.Containers(ctx); err == nil {
			add(s.loops(snap)...)
		}
	}
	if s.Sites != nil {
		add(certs(s.Sites(ctx), s.now(), s.limits())...)
	}
	if s.Security != nil {
		add(security(s.Security(ctx))...)
	}
	if s.Extra != nil {
		add(s.Extra()...)
	}
	sort.SliceStable(r.Hints, func(i, j int) bool { return r.Hints[i].Level == Err && r.Hints[j].Level != Err })
	return r
}

// gb schreibt eine Größe wie der Leitstand (12,4 GB).
func gb(n int64) string {
	f := float64(n) / 1e9
	if f >= 100 {
		return strconv.FormatFloat(f, 'f', 0, 64) + " GB"
	}
	return strings.Replace(strconv.FormatFloat(f, 'f', 1, 64), ".", ",", 1) + " GB"
}

func (s *Service) disk() []Hint {
	if s.Disk == nil {
		return nil
	}
	paths := s.DiskPaths
	if len(paths) == 0 {
		paths = []string{"/", "/var/lib/docker"}
	}
	var out []Hint
	lim := s.limits()
	seen := map[[2]int64]bool{}
	for _, p := range paths {
		total, free, err := s.Disk(p)
		if err != nil || total <= 0 {
			continue
		}
		k := [2]int64{total, free / 1_000_000}
		if seen[k] {
			continue
		}
		seen[k] = true
		used := float64(total-free) / float64(total)
		lvl := ""
		switch {
		case used >= lim.DiskErr || (lim.DiskMinFree > 0 && total >= 4*lim.DiskMinFree && free < lim.DiskMinFree):
			lvl = Err
		case used >= lim.DiskWarn:
			lvl = Warn
		default:
			continue
		}
		// Immer mit Pfad, auch bei „/“.
		where := " (" + p + ")"
		t := "Platte wird knapp"
		if lvl == Err {
			t = "Platte fast voll"
		}
		out = append(out, Hint{Key: "platz:" + p, Level: lvl, Icon: "platz", Title: t + where,
			Text: fmt.Sprintf("%d %% belegt, noch %s frei. Läuft sie voll, bleiben Datenbanken und Container stehen. Unter Speicherplatz siehst du, was weg kann.", int(used*100+0.5), gb(free)),
			Link: "#/platz", Go: "Aufräumen"})
	}
	return out
}

func (s *Service) memory() []Hint {
	if s.Memory == nil {
		return nil
	}
	total, avail := s.Memory()
	if total <= 0 {
		return nil
	}
	share := float64(avail) / float64(total)
	lim := s.limits()
	if share >= lim.MemWarn {
		return nil
	}
	lvl := Warn
	if share < lim.MemErr {
		lvl = Err
	}
	return []Hint{{Key: "speicher", Level: lvl, Icon: "leistung", Title: "Arbeitsspeicher knapp",
		Text: "Nur noch " + gb(avail) + " von " + gb(total) + " frei. Wird es eng, beendet Linux einen Prozess. Unter Leistung siehst du, wer viel braucht.",
		Link: "#/leistung", Go: "Verbrauch ansehen"}}
}

// loops erkennt Container, die ständig neu starten: Docker meldet
// „restarting“, oder die Zahl der Neustarts ist binnen einer Stunde um
// LoopRestarts gestiegen, oder er hat viele Neustarts und läuft erst seit
// Kurzem.
func (s *Service) loops(snap betrieb.Snapshot) []Hint {
	now := s.now()
	s.mu.Lock()
	if s.seen == nil {
		s.seen = map[string][]restartSample{}
	}
	var names []string
	for _, c := range snap.Containers {
		hist := append(s.seen[c.Name], restartSample{at: now, n: c.Restarts})
		for len(hist) > 0 && now.Sub(hist[0].at) > LoopWindow {
			hist = hist[1:]
		}
		if len(hist) > 30 {
			hist = hist[len(hist)-30:]
		}
		s.seen[c.Name] = hist
		grew := len(hist) > 1 && c.Restarts-hist[0].n >= LoopRestarts
		fresh := c.Running && c.Restarts >= LoopRestarts && c.Since != nil && now.Sub(*c.Since) < LoopFresh
		if c.State == "restarting" || grew || fresh {
			names = append(names, c.Name)
		}
	}
	s.mu.Unlock()
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	t := names[0] + " startet ständig neu"
	if len(names) > 1 {
		t = strconv.Itoa(len(names)) + " Container starten ständig neu"
	}
	return []Hint{{Key: "neustarts", Level: Err, Icon: "projekte", Title: t,
		Text: strings.Join(names, ", ") + ". Meist steht der Grund am Ende der Logs. Unter Projekte siehst du sie und kannst sie anhalten.",
		Link: "#/projekte", Go: "Logs ansehen"}}
}

func certs(sites []domains.Site, now time.Time, lim ueberwachung.Limits) []Hint {
	type exp struct {
		host string
		days int
	}
	var list []exp
	for _, st := range sites {
		if st.Kind == domains.KindOff || len(st.Hosts) == 0 {
			continue
		}
		// Jede Domain des Blocks mit ihrer eigenen Prüfung.
		for _, h := range st.Hosts {
			lv, ok := st.Each[h]
			if !ok {
				if h != st.Hosts[0] || st.Live == nil {
					continue
				}
				lv = *st.Live
			}
			if lv.CertUntil == nil {
				continue
			}
			d := int(lv.CertUntil.Sub(now).Hours() / 24)
			if lv.CertUntil.Before(now) {
				d = -1
			}
			if d < lim.CertWarnDays {
				list = append(list, exp{h, d})
			}
		}
	}
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].days < list[j].days })
	lvl := Warn
	if list[0].days < lim.CertErrDays {
		lvl = Err
	}
	var parts []string
	for _, e := range list {
		switch {
		case e.days < 0:
			parts = append(parts, e.host+" (abgelaufen)")
		case e.days == 0:
			parts = append(parts, e.host+" (heute)")
		case e.days == 1:
			parts = append(parts, e.host+" (morgen)")
		default:
			parts = append(parts, e.host+" (in "+strconv.Itoa(e.days)+" Tagen)")
		}
	}
	t := "Zertifikat läuft bald ab"
	if len(list) > 1 {
		t = strconv.Itoa(len(list)) + " Zertifikate laufen bald ab"
	}
	return []Hint{{Key: "zertifikat", Level: lvl, Icon: "domains", Title: t,
		Text: strings.Join(parts, ", ") + ". Caddy erneuert sonst rechtzeitig von selbst; klappt es nicht, erreicht die Domain meist den Server nicht mehr (DNS) oder Port 80 ist zu.",
		Link: "#/domains", Go: "Zertifikate prüfen"}}
}

func security(ov sicherheit.Overview) []Hint {
	var out []Hint
	if ov.Firewall.OwnLost {
		out = append(out, Hint{Key: "schutz-verloren", Level: Err, Icon: "sicherheit", Title: "Geschlossene Ports sind wieder offen",
			Text: "Die Regeln von HostDeck fehlen gerade, etwa nach einem Neustart von nftables. Unter Sicherheit lädst du sie neu.",
			Link: "#/sicherheit", Go: "Neu laden"})
	}
	if ov.Pending != nil {
		out = append(out, Hint{Key: "schutz-wartet", Level: Warn, Icon: "sicherheit", Title: "Änderung an den Ports wartet",
			Text: ov.Pending.What + ". Ohne Bestätigung nimmt HostDeck sie bald zurück.", Link: "#/sicherheit", Go: "Bestätigen"})
	}
	var risky []string
	for _, p := range ov.Ports {
		if p.Suggest && !p.Closed && p.Risk == "hoch" {
			name := strconv.Itoa(p.From) + "/" + p.Proto
			if p.Owner != "" {
				name += " (" + p.Owner + ")"
			}
			risky = append(risky, name)
		}
	}
	if len(risky) > 0 {
		t := "Port mit hohem Risiko offen"
		if len(risky) > 1 {
			t = strconv.Itoa(len(risky)) + " Ports mit hohem Risiko offen"
		}
		out = append(out, Hint{Key: "ports", Level: Warn, Icon: "sicherheit", Title: t,
			Text: strings.Join(risky, ", ") + " ist von außen erreichbar. Unter Sicherheit steht, warum, und du kannst schließen.",
			Link: "#/sicherheit", Go: "Ports prüfen"})
		if len(risky) > 1 {
			out[len(out)-1].Text = strings.Join(risky, ", ") + " sind von außen erreichbar. Unter Sicherheit steht, warum, und du kannst schließen."
		}
	}
	return out
}
