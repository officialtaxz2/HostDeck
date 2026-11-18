package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/dnscheck"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/restore"
	"github.com/officialtaxz2/hostdeck/internal/umzugstag"
)

// Der Einzug auf beiden Seiten des Umzugs:
//
// Auf dem neuen Server ist der Leitstand nach dem Umzugstag noch eine
// stille Probe-Kopie (probe-kopie.json mit „umzug“). Zeigen die Domains
// hierher, wird er der echte Leitstand: eingezogen.json statt der
// Markierung, dann gehen Backups, Umzüge und Mitteilungen.
//
// Auf dem alten Server wartet der Umzugstag auf die DNS-Umstellung; zeigen
// die Domains auf den neuen Server, ist dieser abgelöst (abgeloest.json).

// arrivedFile liegt im Datenordner, sobald der Leitstand übernommen hat.
const arrivedFile = "eingezogen.json"

// modeFile im Ordner umzug sagt, ob ein Umzug ansteht. Die Kopie bringt
// den Ordner umzug nicht mit, und auf ihr lässt sich nicht umschalten;
// beim Einzug fällt er trotzdem vorsorglich weg, damit danach Alltag gilt.
const modeFile = "modus.json"

// finishFile hält nach dem Einzug fest, was zum Abschluss noch offen ist.
const finishFile = "umzug-abschluss.json"

type arrival struct {
	From    string    `json:"von,omitempty"`
	Copied  time.Time `json:"kopie_seit"`
	Arrived time.Time `json:"uebernommen"`
	How     string    `json:"wie"` // „DNS“ oder „von Hand“
}

// promote macht aus der Probe-Kopie vom Umzugstag den echten Leitstand.
// In wiederherstellung.json (im Ordner state) steht danach, dass der
// Umzug hier abgeschlossen ist: Anhalten, Entfernen und ein weiterer
// Probe-Umzug hierher verweigern dann.
func promote(dataDir, state, how string, now time.Time) error {
	c := loadCopy(dataDir)
	if c == nil {
		return errors.New("Dieser Leitstand ist schon der echte.")
	}
	if !c.Move {
		return errors.New("Das ist eine Probe-Kopie vom Probe-Umzug, nicht vom Umzugstag. Den Umzug startest du im Leitstand des alten Servers.")
	}
	if state != "" {
		if err := restore.MarkAdopted(state, now); err != nil {
			return fmt.Errorf("wiederherstellung.json: %v", err)
		}
	}
	b, _ := json.MarshalIndent(arrival{From: c.From, Copied: c.Since, Arrived: now.UTC(), How: how}, "", "  ")
	if err := outdir.WritePrivate(filepath.Join(dataDir, arrivedFile), append(b, '\n')); err != nil {
		return err
	}
	// Schritt 10 des Lotsen: „Umzug abschließen“ bis zum Ende der
	// Reserve-Woche.
	if fb, err := json.MarshalIndent(dienst.NewFinish(c.From, c.TestDomain, now), "", "  "); err == nil {
		_ = outdir.WritePrivate(filepath.Join(dataDir, finishFile), append(fb, '\n'))
	}
	_ = os.Remove(filepath.Join(dataDir, ort.MoveDir, modeFile))
	// Der Cache von restic stammt vom Backup des alten Servers, aus dem
	// hier zurückgespielt wurde; er wird nie wieder gebraucht.
	if state != "" {
		_ = os.RemoveAll(filepath.Join(state, "cache"))
	}
	return os.Remove(filepath.Join(dataDir, ort.CopyFile))
}

// arrived sagt, ob die Domains hierher zeigen: mindestens eine, und keine
// woandershin. Ohne Domains gibt es keine Umstellung; dann gilt das sofort.
func arrived(ctx context.Context, hosts []string, res *dnscheck.Resolver, local []net.IP) bool {
	if len(hosts) == 0 {
		return true
	}
	here := map[string]bool{}
	for _, ip := range local {
		here[ip.String()] = true
	}
	ok := 0
	for _, h := range hosts {
		a := res.Lookup(ctx, h)
		if a.Err != "" || len(a.Records) == 0 {
			continue
		}
		for _, r := range a.Records {
			if !here[r.IP.String()] {
				return false
			}
		}
		ok++
	}
	return ok > 0
}

// arrivalHosts: die Adresse des Leitstands, sonst alle Domains von Caddy.
func arrivalHosts(ctx context.Context, d *dienst.Dienst) []string {
	if d.Access != nil {
		if dom := d.Access.Domain(); dom != "" {
			return []string{dom}
		}
	}
	c, err := caddy.Find(ctx, caddy.Exec)
	if err != nil {
		return nil
	}
	hosts, _ := c.Hosts(ctx)
	return hosts
}

// watchMove startet beim Dienst die Wächter des Umzugs.
func watchMove(ctx context.Context, d *dienst.Dienst, dataDir string, log io.Writer) {
	note := func(text string) {
		fmt.Fprintln(log, text)
		if d.Journal != nil {
			_ = d.Journal.Add(protokoll.Entry{Kind: protokoll.Lauf, Text: text, Addr: dienst.AdminAddr})
		}
	}
	st := umzugstag.Store{Dir: filepath.Join(dataDir, ort.MoveDir)}
	if st.Recover(time.Now()) {
		note("Umzugstag unterbrochen: Der Dienst von HostDeck startete mitten im Ablauf neu. Auf der Seite Umzug geht es weiter (erneut starten oder Rückweg).")
	}
	state, _ := stateDir()
	pub, _ := d.Access.(*publisher)
	d.Arrive = func() error {
		if err := promote(dataDir, state, "von Hand", time.Now()); err != nil {
			return err
		}
		go settleTestAddress(ctx, pub, d.RunDir, dataDir, note)
		return nil
	}
	if m, ok := d.Moves.(*serverMoves); ok {
		go m.Watch(ctx, time.Minute, note)
	}
	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			if c := loadCopy(dataDir); c == nil || !c.Move {
				continue
			}
			if !arrived(ctx, arrivalHosts(ctx, d), &dnscheck.Resolver{}, localIPs()) {
				continue
			}
			if err := promote(dataDir, state, "DNS", time.Now()); err != nil {
				note("Umzug: Die Domains zeigen hierher, aber der Leitstand konnte nicht übernehmen: " + err.Error())
				continue
			}
			note("Umzug abgeschlossen: Die Domains zeigen hierher. Dieser Leitstand ist jetzt der echte; Backups, Umzüge und Mitteilungen gehen hier.")
			settleTestAddress(ctx, pub, d.RunDir, dataDir, note)
		}
	}()
}

// dropTestAddress nimmt die Testadresse der Probe-Kopie aus dem Zugang
// (freischaltung.json), sobald dieser Leitstand der echte ist. Bliebe sie stehen, holte Caddy weiter Zertifikate für eine Adresse,
// deren DNS-Eintrag nach dem Umzug niemand mehr braucht, und scheiterte
// still, sobald der Eintrag weg ist. gone sind die entfernten Adressen.
func dropTestAddress(dataDir string) (gone []string, err error) {
	if loadCopy(dataDir) != nil {
		return nil, nil // noch Probe-Kopie: die Testadresse wird gebraucht
	}
	st, err := loadAccess(dataDir)
	if err != nil || st == nil || len(st.Extra) == 0 {
		return nil, err
	}
	gone = st.Extra
	st.Extra = nil
	return gone, saveAccess(dataDir, st)
}

// testAddressText ist der Eintrag im Protokoll, wenn die Testadresse weg
// ist. Erst jetzt darf der Rat kommen, den DNS-Eintrag zu löschen.
func testAddressText(gone []string) string {
	list := strings.Join(gone, ", ")
	return "Testadresse " + list + " aus dem Leitstand genommen: Der Umzug ist abgeschlossen, der Leitstand gilt nur noch unter seiner eigenen Adresse. Den DNS-Eintrag für " + list + " kannst du jetzt beim Anbieter löschen."
}

// settleTestAddress räumt nach dem Einzug auf: Testadresse aus dem Zugang,
// dann die Caddy-Datei des Leitstands neu (mit derselben Prüfung und
// demselben Rückweg wie beim Freischalten). Hält gerade ein anderer Lauf die
// Sperre, versucht es HostDeck ein paar Minuten lang; sonst holt das nächste
// Update die Caddy-Datei nach (Refresh sieht dann den Unterschied). Die
// Website behält die Adresse bis zum nächsten Update in ihrer Liste
// erlaubter Adressen; das schadet nicht, weil Caddy sie nicht mehr bedient.
func settleTestAddress(ctx context.Context, p *publisher, runDir, dataDir string, note func(string)) {
	gone, err := dropTestAddress(dataDir)
	if err != nil {
		note("Umzug: Die Testadresse ließ sich nicht aus dem Zugang nehmen: " + err.Error())
		return
	}
	if len(gone) == 0 || p == nil {
		return
	}
	var l *lauf.Lock
	for i := 0; i < 20; i++ {
		if l, err = lauf.TakeIn(runDir, "Testadresse aus Caddy nehmen"); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
	if l == nil {
		note("Umzug: Die Testadresse " + strings.Join(gone, ", ") + " steht noch in der Caddy-Datei des Leitstands, weil ein anderer Lauf arbeitete; das nächste Update nimmt sie heraus. Den DNS-Eintrag bis dahin stehen lassen.")
		return
	}
	defer l.Release()
	if _, err := p.Refresh(ctx); err != nil {
		note("Umzug: Die Testadresse " + strings.Join(gone, ", ") + " ließ sich nicht aus der Caddy-Datei des Leitstands nehmen (" + err.Error() + "); die bisherige Datei gilt weiter, das nächste Update versucht es wieder. Den DNS-Eintrag bis dahin stehen lassen.")
		return
	}
	note(testAddressText(gone))
}
