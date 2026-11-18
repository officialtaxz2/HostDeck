package dienst

import (
	"fmt"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// Nach dem Download vom Server löschen: Was der Server geschrieben hat,
// muss beim Browser noch nicht angekommen sein (Puffer in Caddy, im Netz,
// im Browser). Darum löscht HostDeck erst eine Weile nach dem letzten Byte, und
// jede neue Anfrage zum Herunterladen schiebt das Löschen wieder hinaus.
// Startet der Dienst in der Zeit neu, bleibt das Backup liegen; der Knopf
// „Vom Server löschen“ steht dann auf der Seite Backup.

// DefaultRemoveGrace ist die Frist nach einem vollständigen Download.
const DefaultRemoveGrace = 30 * time.Minute

// fetchedBackup ist das zuletzt vollständig heruntergeladene Backup.
type fetchedBackup struct {
	etag string
	at   time.Time // vollständig heruntergeladen
	due  time.Time // dann löscht HostDeck es; leer: es bleibt
	addr string
	gate string
}

func (d *Dienst) removeGrace() time.Duration {
	if d.RemoveGrace > 0 {
		return d.RemoveGrace
	}
	return DefaultRemoveGrace
}

func graceText(g time.Duration) string {
	if g < time.Minute {
		return "gleich"
	}
	if g == time.Minute {
		return "in einer Minute"
	}
	return fmt.Sprintf("in %d Minuten", int(g/time.Minute))
}

// fetchedComplete hält fest, dass das Backup mit etag vollständig
// heruntergeladen ist, und merkt im Alltag das Löschen vor.
func (d *Dienst) fetchedComplete(etag, name, addr, gate string) {
	g := d.removeGrace()
	everyday := d.everyday()
	d.mu.Lock()
	f := &fetchedBackup{etag: etag, at: d.now(), addr: addr, gate: gate}
	start := false
	if everyday {
		f.due = time.Now().Add(g)
		start = !d.awaiting // sonst übernimmt der wartende
		d.awaiting = true
	}
	d.fetched = f
	d.mu.Unlock()
	text := "Backup vollständig heruntergeladen: " + name
	if everyday {
		text += ". HostDeck löscht es " + graceText(g) + " vom Server, wenn der Download nicht noch einmal anläuft"
	}
	d.note(protokoll.Entry{Kind: protokoll.Download, Text: text, Addr: addr, Gate: gate})
	_ = d.Events.Add(ueberwachung.Happening{Kind: ueberwachung.HapBackup, Level: ueberwachung.LevelOK, Title: "Backup heruntergeladen", Text: name, Link: "#/backup"})
	d.finishBackupDone()
	if start {
		d.wg.Add(1)
		go d.awaitRemoval()
	}
}

// touchFetched schiebt ein vorgemerktes Löschen hinaus: Jemand lädt
// (wieder) herunter.
func (d *Dienst) touchFetched() {
	d.mu.Lock()
	if f := d.fetched; f != nil && !f.due.IsZero() {
		f.due = time.Now().Add(d.removeGrace())
	}
	d.mu.Unlock()
}

// keepFetched sagt ein vorgemerktes Löschen ab: Ein Umzug steht an.
func (d *Dienst) keepFetched() {
	d.mu.Lock()
	if f := d.fetched; f != nil {
		f.due = time.Time{}
	}
	d.mu.Unlock()
}

// armFetched merkt nach dem Umschalten auf Alltag das Löschen eines schon
// vollständig heruntergeladenen Backups (wieder) vor: Im Alltag bleibt
// kein Backup liegen, das du schon hast.
func (d *Dienst) armFetched(addr, gate string) {
	g := d.removeGrace()
	d.mu.Lock()
	f := d.fetched
	if f == nil || !f.due.IsZero() {
		d.mu.Unlock()
		return
	}
	f.due = time.Now().Add(g)
	f.addr, f.gate = addr, gate
	start := !d.awaiting
	d.awaiting = true
	d.mu.Unlock()
	d.note(protokoll.Entry{Kind: protokoll.Download, Text: "Das Backup ist schon vollständig heruntergeladen: HostDeck löscht es " + graceText(g) + " vom Server, wenn der Download nicht noch einmal anläuft", Addr: addr, Gate: gate})
	if start {
		d.wg.Add(1)
		go d.awaitRemoval()
	}
}

// forgetFetched vergisst den Download: Das Backup im Ordner ist ein
// anderes oder gelöscht.
func (d *Dienst) forgetFetched() {
	d.mu.Lock()
	d.fetched = nil
	d.mu.Unlock()
}

// awaitRemoval wartet die Frist ab und löscht dann.
func (d *Dienst) awaitRemoval() {
	defer d.wg.Done()
	ctx := d.context()
	for {
		d.mu.Lock()
		f := d.fetched
		if f == nil || f.due.IsZero() {
			d.awaiting = false
			d.mu.Unlock()
			return
		}
		wait := time.Until(f.due)
		if wait <= 0 && d.loading > 0 {
			f.due = time.Now().Add(d.removeGrace())
			wait = time.Until(f.due)
		}
		if wait <= 0 {
			f.due = time.Time{}
			d.awaiting = false
			etag, addr, gate := f.etag, f.addr, f.gate
			d.mu.Unlock()
			d.removeFetched(etag, addr, gate)
			return
		}
		d.mu.Unlock()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			d.mu.Lock()
			d.awaiting = false
			d.mu.Unlock()
			return
		}
	}
}

// removeFetched löscht das Backup vom Server, wenn es noch das
// heruntergeladene ist und kein Umzug ansteht.
func (d *Dienst) removeFetched(etag, addr, gate string) {
	keep := func(why string) {
		d.note(protokoll.Entry{Kind: protokoll.Download, Text: "Backup bleibt vorerst auf dem Server: " + why + ". Löschen geht unter Backup mit „Vom Server löschen“.", Addr: addr, Gate: gate})
	}
	if !d.everyday() {
		keep("Inzwischen ist ein Umzug geplant")
		return
	}
	l, err := lauf.TakeIn(d.RunDir, "Backup vom Server löschen")
	if err != nil {
		keep("Gerade läuft etwas anderes (" + err.Error() + ")")
		return
	}
	defer l.Release()
	if d.Downloading() {
		keep("Gerade lädt es noch jemand herunter")
		return
	}
	a, _, err := d.Backups.Archive()
	if err != nil {
		keep("Das Backup lässt sich nicht prüfen (" + err.Error() + ")")
		return
	}
	same := a.ETag == etag
	a.Close()
	if !same {
		keep("Seit dem Download hat sich das Backup geändert")
		return
	}
	freed, err := d.Backups.Remove(d.context())
	if err != nil {
		keep("Löschen ging nicht: " + err.Error())
		return
	}
	d.forgetFetched()
	d.note(protokoll.Entry{Kind: protokoll.Download, Text: fmt.Sprintf("Backup nach dem Download vom Server gelöscht, %s frei", units.HumanSize(freed)), Addr: addr, Gate: gate})
}
