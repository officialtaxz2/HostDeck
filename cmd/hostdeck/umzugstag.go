package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/remote"
	"github.com/officialtaxz2/hostdeck/internal/restore"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
	"github.com/officialtaxz2/hostdeck/internal/umzugstag"
	"golang.org/x/crypto/ssh"
)

// Der Umzugstag. Ein Probe-Umzug, bei dem alles klappt, gilt
// drei Tage als Generalprobe. Danach zieht ein fester Ablauf alles auf den
// neuen Server und wartet auf die DNS-Umstellung. Den Stand jedes Schritts
// hält umzugstag.json fest; bis die Domains auf den neuen Server zeigen,
// startet der Rückweg die Projekte hier wieder.

func (m *serverMoves) days() umzugstag.Store { return umzugstag.Store{Dir: m.Dir} }

func (m *serverMoves) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *serverMoves) leftOff() int {
	if m.LeftOff == nil {
		return 0
	}
	return m.LeftOff()
}

// DayState ist der Stand für die Seite Umzug.
func (m *serverMoves) DayState(ctx context.Context) dienst.DayState {
	st := m.days()
	ds := dienst.DayState{Probe: st.Probe(), Day: st.Day(), Done: st.Done(), LeftOff: m.leftOff()}
	host := ""
	if ds.Probe != nil {
		host = ds.Probe.Host
	}
	ds.Problem = m.DayProblem(ctx, host)
	ds.Ready = ds.Problem == ""
	return ds
}

// DayProblem sagt, warum der Umzugstag auf host nicht starten kann.
func (m *serverMoves) DayProblem(ctx context.Context, host string) string {
	st := m.days()
	if d := st.Done(); d != nil {
		return "Dieser Server ist " + d.Text() + "."
	}
	if d := st.Day(); d != nil && d.Phase == umzugstag.PhaseDNS {
		return "Der Umzug ist schon drüben und wartet auf die DNS-Umstellung."
	}
	if s := m.State(ctx); !s.Ready {
		return s.Problem
	}
	return umzugstag.ProbeProblem(st.Probe(), host, m.now())
}

// Finish schließt den Umzug von Hand ab.
func (m *serverMoves) Finish(ctx context.Context) error {
	st := m.days()
	if done := st.Done(); done != nil {
		return errors.New("Schon abgeschlossen: Dieser Server ist " + done.Text() + ".")
	}
	d := st.Day()
	if d == nil || d.Phase != umzugstag.PhaseDNS {
		return errors.New("Abschließen geht, wenn der Umzugstag alles auf den neuen Server gebracht hat und auf die DNS-Umstellung wartet.")
	}
	return m.finish(d, "von Hand")
}

func (m *serverMoves) finish(d *umzugstag.Day, how string) error {
	st := m.days()
	now := m.now()
	if err := st.SaveDone(umzugstag.Done{At: now.UTC(), IP: d.IP, Target: d.Target, How: how}); err != nil {
		return err
	}
	note := "zeigt auf den neuen Server"
	if how != "DNS" {
		note = "von Hand abgeschlossen"
	}
	d.Set(umzugstag.StepDNS, umzugstag.StepOK, note, now)
	d.Phase = umzugstag.PhaseDone
	t := now.UTC()
	d.Ended = &t
	return st.SaveDay(d)
}

// Watch schaut, solange der Umzug auf die DNS-Umstellung wartet, jede
// Minute nach: Zeigt keine Domain mehr hierher und mindestens eine auf den
// neuen Server, ist dieser Server abgelöst. note geht ins Protokoll.
func (m *serverMoves) Watch(ctx context.Context, every time.Duration, note func(string)) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if msg := m.checkSwitched(ctx); msg != "" && note != nil {
			note(msg)
		}
	}
}

// checkSwitched prüft einmal; Ergebnis: Text fürs Protokoll, wenn dieser
// Server jetzt abgelöst ist.
func (m *serverMoves) checkSwitched(ctx context.Context) string {
	st := m.days()
	d := st.Day()
	if d == nil || d.Phase != umzugstag.PhaseDNS || st.Done() != nil {
		return ""
	}
	rep := m.DNS(ctx)
	if !dnsSwitched(rep) {
		return ""
	}
	if err := m.finish(d, "DNS"); err != nil {
		return "Umzug: DNS zeigt auf den neuen Server, aber abgeloest.json ließ sich nicht schreiben: " + err.Error()
	}
	return "Umzug abgeschlossen: Die Domains zeigen auf " + d.IP + "; dieser Server ist abgelöst"
}

// dnsSwitched: Keine Domain zeigt mehr (auch nur teilweise) hierher, und
// mindestens eine zeigt auf den neuen Server. Domains, die woandershin
// zeigen (etwa über einen Proxy), halten den Abschluss nicht auf.
func dnsSwitched(rep dienst.DNSReply) bool {
	if rep.Problem != "" || rep.Target == "" {
		return false
	}
	fresh := 0
	for _, d := range rep.Domains {
		switch d.State {
		case dienst.DNSStateOld, dienst.DNSStateMixed:
			return false
		case dienst.DNSStateNew:
			fresh++
		}
	}
	return fresh > 0
}

// recordProbe hält nach einem Probe-Umzug fest, ob er als Generalprobe
// zählt.
func (m *serverMoves) recordProbe(ctx context.Context, t dienst.MoveTarget, ip, run string, code int, errs []string, copied bool, good, total int, w io.Writer) {
	p := umzugstag.Probe{Target: t.String(), Host: t.Host, IP: ip, At: m.now().UTC(), Run: run}
	if code != restore.ExitOK {
		p.Problems = append(p.Problems, "das Zurückspielen meldete Fehler")
		// Der Lotse nennt die Fehler selbst, statt auf die Ausgabe zu verweisen.
		p.Details = errs
	}
	if !copied {
		p.Problems = append(p.Problems, "der Leitstand kam nicht mit")
	}
	if total > 0 && good < total {
		p.Problems = append(p.Problems, fmt.Sprintf("%d von %d Seiten antworten drüben anders", total-good, total))
	}
	p.OK = len(p.Problems) == 0
	if err := m.days().SaveProbe(p); err != nil {
		fmt.Fprintf(w, "\n! Ergebnis der Generalprobe nicht gespeichert: %v\n", err)
		return
	}
	fmt.Fprintf(w, "\nGeneralprobe\n")
	if p.OK {
		fmt.Fprintf(w, "  ✓ Bestanden. Den Umzugstag kannst du bis %s starten (Seite Umzug).\n", p.At.Add(umzugstag.ProbeMaxAge).Local().Format("02.01. 15:04"))
	} else {
		fmt.Fprintf(w, "  ! Zählt noch nicht als Generalprobe: %s.\n", strings.Join(p.Problems, "; "))
	}
	// Die Kopie läuft weiter, ihre Projekte auch.
	fmt.Fprintf(w, "  Die Probe-Kopie läuft drüben weiter. Haben Projekte eigene Zeitpläne (Erinnerungen, Bots, Mails), laufen die jetzt doppelt; dann unter Umzug „Probe-Kopie anhalten“.\n")
}

// runDay ist der Ablauf des Umzugstags.
func (m *serverMoves) runDay(ctx context.Context, t dienst.MoveTarget, in backup.Input, w io.Writer) int {
	st := m.days()
	fmt.Fprintf(w, "HostDeck %s: Umzugstag nach %s\n", version, t)
	if why := m.DayProblem(ctx, t.Host); why != "" {
		fmt.Fprintf(w, "\n  ✗ %s\n", why)
		return restore.ExitBlocked
	}
	if m.Final == nil {
		fmt.Fprintf(w, "\n  ✗ Der letzte Lauf geht hier nicht.\n")
		return restore.ExitBlocked
	}
	prev := st.Day()
	day := umzugstag.NewDay(t.String(), t.Host, m.now())
	day.IP, _ = m.targetIP(ctx, t)
	// Ein Ablauf, der nach dem letzten Lauf stehen blieb, fängt mit diesem
	// Lauf wieder an: Die Projekte hier sind seitdem aus.
	reuse := ""
	if prev != nil && prev.FinalRun != "" && (prev.Phase == umzugstag.PhaseFailed || prev.Phase == umzugstag.PhaseInterrupted) &&
		prev.FinalRun == m.Stored(ctx).LastRun && m.leftOff() > 0 {
		reuse = prev.FinalRun
	}
	save := func() {
		if err := st.SaveDay(day); err != nil {
			fmt.Fprintf(w, "  ! %s nicht gespeichert: %v\n", umzugstag.DayFile, err)
		}
	}
	n := 0
	begin := func(key string) {
		n++
		s := day.Step(key)
		day.Set(key, umzugstag.StepRunning, "", m.now())
		save()
		fmt.Fprintf(w, "\n━━ Schritt %d von %d: %s\n", n, len(day.Steps), s.Title)
	}
	end := func(key, state, note string) {
		day.Set(key, state, note, m.now())
		save()
	}
	// stop beendet den Ablauf. Sind die Projekte hier noch an, ist nichts
	// passiert; sonst bleibt der Rückweg.
	stop := func(key, note string) int {
		end(key, umzugstag.StepFailed, note)
		tm := m.now().UTC()
		day.Ended = &tm
		if m.leftOff() == 0 {
			day.Phase = umzugstag.PhaseBack
			day.Note = "Abgebrochen, bevor etwas gestoppt war; die Projekte hier liefen weiter."
			save()
			fmt.Fprintf(w, "\nDer Umzugstag ist abgebrochen. Die Projekte hier laufen weiter.\n")
			return restore.ExitFailed
		}
		day.Phase = umzugstag.PhaseFailed
		day.Note = note
		save()
		fmt.Fprintf(w, "\nDer Umzugstag ist bei „%s“ stehen geblieben. Die Projekte hier sind aus.\n", day.Step(key).Title)
		fmt.Fprintf(w, "Auf der Seite Umzug: „Zurück zum alten Server“ startet sie hier wieder, oder starte den Umzugstag erneut.\n")
		return restore.ExitFailed
	}
	save()

	// Erst die Verbindung, dann der letzte Lauf: Antwortet der neue Server
	// nicht, bleibt hier alles an.
	fmt.Fprintf(w, "\nVerbindung\n")
	pw, err := os.ReadFile(m.PasswordFile)
	if err != nil {
		fmt.Fprintf(w, "  ✗ Backup-Passwort nicht lesbar: %v\n", err)
		return stop(umzugstag.StepFinal, "Backup-Passwort nicht lesbar")
	}
	client, err := m.connect(ctx, t, in, w)
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(w, "  ✗ %v\n", err)
		}
		return stop(umzugstag.StepFinal, "keine Verbindung zum neuen Server")
	}
	defer client.Close()
	alive, stopAlive := context.WithCancel(ctx)
	defer stopAlive()
	go m.keepAlive(alive, client)
	m.saveLast(t)
	// Hat drüben schon ein Leitstand übernommen (etwa nach einem Rückweg),
	// spielt HostDeck dort nichts darüber. Das vor dem letzten Lauf sagen, nicht
	// erst, wenn die Seiten hier schon aus sind (Labor S5).
	if why := adoptedProblem(remoteRecord(client)); why != "" {
		fmt.Fprintf(w, "  ✗ %s\n", why)
		return stop(umzugstag.StepFinal, "der neue Server ist schon übernommen")
	}
	fmt.Fprintf(w, "  Seiten hier abfragen, solange sie laufen (zum Vergleich nachher) …\n")
	base := m.siteBaseline(ctx)

	begin(umzugstag.StepFinal)
	if reuse != "" {
		fmt.Fprintf(w, "  Der letzte Lauf %s von vorhin gilt weiter; die Projekte hier sind seitdem aus.\n", reuse)
		day.FinalRun = reuse
		end(umzugstag.StepFinal, umzugstag.StepOK, "Lauf "+reuse+" von vorhin")
	} else {
		fmt.Fprintf(w, "  Ab hier sind deine Seiten aus.\n\n")
		code := m.Final(ctx, in, w)
		if code != backup.ExitOK || ctx.Err() != nil {
			return stop(umzugstag.StepFinal, "der letzte Lauf ging nicht")
		}
		day.FinalRun = m.Stored(ctx).LastRun
		end(umzugstag.StepFinal, umzugstag.StepOK, "Lauf "+day.FinalRun)
	}
	run := day.FinalRun

	begin(umzugstag.StepRemove)
	rw := &lineWatch{w: w}
	code, err := m.agent(ctx, client, dienst.MoveRemove, "", "", &remote.Config{Action: remote.ActionProbeRemove, Confirmed: true}, in, rw)
	if err != nil || code != restore.ExitOK {
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(w, "  ✗ %v\n", err)
		}
		why := "die Probe-Kopie ließ sich nicht ganz entfernen"
		if len(rw.errs) > 0 {
			why += ": " + rw.errs[0]
		}
		return stop(umzugstag.StepRemove, why)
	}
	end(umzugstag.StepRemove, umzugstag.StepOK, "")

	begin(umzugstag.StepRestore)
	watch := &lineWatch{w: w, needle: []byte(restore.CaddyRunningLine)}
	code, err = m.agent(ctx, client, dienst.MoveRun, strings.TrimSpace(string(pw)), run, nil, confirmedRestore{in, w}, watch)
	if err != nil || code != restore.ExitOK {
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(w, "  ✗ %v\n", err)
		}
		why := "das Zurückspielen meldete Fehler"
		if len(watch.errs) > 0 {
			why += ": " + watch.errs[0]
			if n := len(watch.errs) - 1; n > 0 {
				why += fmt.Sprintf(" (und %d weitere, siehe Ausgabe)", n)
			}
		}
		return stop(umzugstag.StepRestore, why)
	}
	fmt.Fprintf(w, "\nÜbertragen aus dem Backup: %s\n", sizeText(m.sent.Load()))
	end(umzugstag.StepRestore, umzugstag.StepOK, sizeText(m.sent.Load())+" übertragen")

	begin(umzugstag.StepCopy)
	tested, copied := m.takeover(ctx, client, t, true, in, w)
	if ctx.Err() != nil {
		return stop(umzugstag.StepCopy, "abgebrochen")
	}
	if copied {
		end(umzugstag.StepCopy, umzugstag.StepOK, "still, bis die Domains dorthin zeigen")
	} else {
		end(umzugstag.StepCopy, umzugstag.StepWarn, "der Leitstand kam nicht mit; die Projekte laufen trotzdem")
	}

	begin(umzugstag.StepServer)
	if m.serverLevel(ctx, client, true, in, w) {
		end(umzugstag.StepServer, umzugstag.StepOK, "")
	} else if ctx.Err() != nil {
		return stop(umzugstag.StepServer, "abgebrochen")
	} else {
		end(umzugstag.StepServer, umzugstag.StepWarn, "nicht alles übernommen, siehe Ausgabe")
	}

	begin(umzugstag.StepCheck)
	good, total := 0, -1
	switch {
	case watch.seen:
		good, total = m.checkSites(ctx, t, base, w)
	case len(base) == 0:
		total = 0
		fmt.Fprintf(w, "  Hier bedient Caddy keine Domains; es gibt keine Seiten zu prüfen.\n")
	default:
		fmt.Fprintf(w, "  ! Drüben läuft kein Caddy von HostDeck; die Seiten prüft HostDeck nicht.\n")
	}
	if tested != "" {
		m.checkTestDomain(ctx, t, tested, w)
	}
	if ctx.Err() != nil {
		return stop(umzugstag.StepCheck, "abgebrochen")
	}
	switch {
	case total > 0 && good == total:
		end(umzugstag.StepCheck, umzugstag.StepOK, fmt.Sprintf("%d von %d Seiten wie vorher", good, total))
	case total > 0:
		end(umzugstag.StepCheck, umzugstag.StepWarn, fmt.Sprintf("%d von %d Seiten antworten anders", total-good, total))
	case total == 0:
		end(umzugstag.StepCheck, umzugstag.StepOK, "keine Domains")
	default:
		end(umzugstag.StepCheck, umzugstag.StepWarn, "nicht geprüft")
	}

	n++
	day.Phase = umzugstag.PhaseDNS
	day.Set(umzugstag.StepDNS, umzugstag.StepRunning, "wartet auf dich", m.now())
	save()
	fmt.Fprintf(w, "\n━━ Schritt %d von %d: DNS umstellen\n", n, len(day.Steps))
	ip := day.IP
	if ip == "" {
		ip = t.Host
	}
	fmt.Fprintf(w, "  Jetzt bist du dran: Stelle bei deinem Domain-Anbieter deine Domains auf %s um, die Leitstand-Adresse zum Schluss. Die Seite Umzug zeigt die Einträge zum Kopieren und hakt jede Domain ab, sobald sie umgestellt ist.\n", ip)
	fmt.Fprintf(w, "  Zeigen alle dorthin, ist dieser Server abgelöst und der neue Leitstand übernimmt. Bis dahin startet „Zurück zum alten Server“ alles hier wieder.\n")
	return restore.ExitOK
}

// runBack ist der Rückweg: Die Projekte, die der letzte Lauf aus gelassen
// hat, starten hier wieder.
func (m *serverMoves) runBack(ctx context.Context, in backup.Input, w io.Writer) int {
	st := m.days()
	fmt.Fprintf(w, "HostDeck %s: Zurück zum alten Server\n\n", version)
	if m.Restart == nil {
		fmt.Fprintf(w, "  ✗ Der Rückweg geht hier nicht.\n")
		return restore.ExitBlocked
	}
	done := st.Done()
	if done != nil {
		fmt.Fprintf(w, "  ! Dieser Server ist %s.\n", done.Text())
		ans, err := in.Ask(ctx, backup.Question{
			Text: "Trotzdem zurück? Tippe zurück:",
			Note: "Die Domains zeigen schon auf den neuen Server. Was dort seit dem Umzug neu entstand (etwa neue Einträge in deinen Apps), fehlt hier. Stelle die DNS-Einträge möglichst zuerst zurück auf diesen Server, dann bestätige hier. " +
				"Auf dem neuen Server (" + done.IP + ") laufen die Projekte danach weiter, samt Bots, Backups und Mitteilungen. Halte sie dort im Leitstand unter Projekte an, sonst laufen sie doppelt.",
			Choices: []backup.Choice{
				{Label: "Zurück zum alten Server", Answer: "zurück"},
				{Label: "Abbrechen", Answer: "nein"},
			},
		})
		if err != nil || strings.TrimSpace(ans) != "zurück" {
			fmt.Fprintf(w, "Nicht gestartet. Verändert wurde nichts.\n")
			return restore.ExitBlocked
		}
	}
	fmt.Fprintf(w, "Starte die Projekte, die der letzte Lauf aus gelassen hat:\n")
	bad := m.Restart(ctx, w)
	now := m.now()
	if done != nil {
		if err := st.ClearDone(); err != nil {
			fmt.Fprintf(w, "  ! %s nicht gelöscht: %v\n", umzugstag.DoneFile, err)
		}
	}
	if d := st.Day(); d != nil {
		d.Phase = umzugstag.PhaseBack
		d.FinalRun = ""
		t := now.UTC()
		d.Ended = &t
		d.Note = "Rückweg am " + now.Local().Format("02.01. 15:04")
		if err := st.SaveDay(d); err != nil {
			fmt.Fprintf(w, "  ! %s nicht gespeichert: %v\n", umzugstag.DayFile, err)
		}
	}
	if len(bad) > 0 {
		fmt.Fprintf(w, "\n✗ Läuft danach nicht: %s. Sieh unter Betrieb nach.\n", strings.Join(bad, ", "))
		return restore.ExitFailed
	}
	fmt.Fprintf(w, "\n✓ Die Projekte laufen wieder hier.")
	if done != nil {
		fmt.Fprintf(w, "\n  Wichtig: Auf dem neuen Server (%s) laufen die Projekte weiter. Halte sie dort im Leitstand unter Projekte an, sonst laufen Bots und Erinnerungen doppelt und Mitteilungen kommen von zwei Servern.", done.IP)
	} else {
		fmt.Fprintf(w, " Die DNS-Einträge zeigen noch hierher; am neuen Server bleibt die stille Kopie, bis du sie unter Umzug entfernst oder den Umzugstag wiederholst.")
	}
	fmt.Fprintln(w)
	return restore.ExitOK
}

// dayBusy nennt für die Überwachung den Umzug, solange er die Projekte
// hier aus hat; für einen abgelösten Server ueberwachung.Abgeloest (dort
// ruht nur die Prüfung der Seiten); sonst "".
func dayBusy(dir string) string {
	st := umzugstag.Store{Dir: dir}
	if st.Done() != nil {
		return ueberwachung.Abgeloest
	}
	if st.Day().Active() {
		return "Umzugstag"
	}
	return ""
}

// confirmedRestore beantwortet am Umzugstag die Frage „Jetzt zurückspielen?“
// selbst: Du hast den Umzugstag schon bestätigt, und ab dem letzten Lauf
// sind deine Seiten aus. Eine zweite Rückfrage verlängerte nur die
// Ausfallzeit, wenn niemand vor dem Bildschirm sitzt (Labor S4). Andere
// Fragen, etwa „überschreiben?“, gehen weiter an dich.
type confirmedRestore struct {
	in backup.Input
	w  io.Writer
}

func (c confirmedRestore) Ask(ctx context.Context, q backup.Question) (string, error) {
	if len(q.Choices) == 2 && q.Choices[0].Answer == "ja" && q.Choices[1].Answer == "nein" && strings.Contains(q.Note, "zurückspielen?") {
		fmt.Fprintln(c.w, "  Zurückspielen: am Umzugstag schon bestätigt.")
		return "ja", nil
	}
	return c.in.Ask(ctx, q)
}

// remoteRecord liest wiederherstellung.json des neuen Servers (bei root
// unter /root/.hostdeck, sonst im Home des Benutzers); leer, wenn es keine gibt
// oder sie nicht lesbar ist. Dann prüfen die Schritte drüben selbst.
func remoteRecord(client *ssh.Client) []byte {
	if client == nil {
		return nil
	}
	sess, err := client.NewSession()
	if err != nil {
		return nil
	}
	defer sess.Close()
	out, _ := sess.Output(`for f in /root/.hostdeck/wiederherstellung.json "$HOME/.hostdeck/wiederherstellung.json"; do cat "$f" 2>/dev/null && break; done`)
	return out
}

// adoptedProblem: Grund, warum der Umzugstag nicht auf diesen Server darf,
// weil dort der Umzug schon übernommen ist; leer sonst.
func adoptedProblem(b []byte) string {
	var rec restore.Record
	if len(b) == 0 || json.Unmarshal(b, &rec) != nil || rec.Adopted == nil {
		return ""
	}
	return "Auf dem neuen Server ist der Umzug seit " + rec.Adopted.Local().Format("02.01.2006 15:04") + " abgeschlossen; dort ist jetzt ein echter Server mit eigenem Leitstand. HostDeck spielt dort nichts darüber. Für einen neuen Versuch setz den neuen Server beim Anbieter neu auf (oder nimm einen anderen) und mach vorher wieder einen Probe-Umzug dorthin."
}
