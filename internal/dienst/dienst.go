// Package dienst ist der root-Teil von HostDeck als Dienst. Er hat keinen
// Netzwerk-Port und spricht nur über einen Unix-Socket. Bei jeder
// Verbindung prüft er, wer anruft (SO_PEERCRED): die Website (hostdeck web, ein
// Benutzer ohne Rechte) oder root (Befehle wie hostdeck notausgang). Er nimmt nur
// eine feste Liste von Aufträgen an.
//
// Die Website selbst kommt nicht an Dateien, Docker oder Backups; alles
// geht über diesen Dienst.
package dienst

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/anmeldung"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/neueversion"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/sitzung"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
	"github.com/officialtaxz2/hostdeck/internal/umzugstag"
	"github.com/officialtaxz2/hostdeck/internal/update"
	"github.com/officialtaxz2/hostdeck/internal/zustand"
)

// LinkTTL ist die Gültigkeit eines Einmal-Links.
const LinkTTL = 2 * time.Minute

// maxLinks begrenzt offene Einmal-Links.
const maxLinks = 20

// Dienst ist der root-Teil.
type Dienst struct {
	Data     http.Handler                    // /api/state und /api/save
	RunDir   string                          // Ordner der Laufsperre
	Version  string                          // Version von HostDeck
	WebUID   int                             // Benutzer von hostdeck web
	AdminUID int                             // darf Einmal-Links erzeugen; normalerweise 0 (root)
	Discover func(ctx context.Context) error // Bestandsaufnahme in den Ergebnisordner
	Recover  func(ctx context.Context)       // Container eines abgebrochenen Laufs wieder starten
	Nightly  time.Duration                   // Uhrzeit der nächtlichen Bestandsaufnahme ab Mitternacht; 0 = keine
	Log      io.Writer
	Now      func() time.Time

	Sessions      *sitzung.Store      // Anmeldungen; nil: nur im Speicher
	Password      *anmeldung.Passwort // Leitstand-Passwort; nil: Anmelden nur per Link
	Lockout       *anmeldung.Sperre   // Sperre nach Fehlversuchen; nil: nur im Speicher
	Journal       *protokoll.Log      // Protokoll; nil: keins
	EmergencyGate string              // Eingang, den die Bremse für alle nicht trifft (Notausgang)

	// Update nimmt eine Update-Datei an, prüft sie und stößt das Einspielen
	// an. nil: keine Updates im Leitstand.
	Update func(ctx context.Context, body io.Reader) (update.Header, error)

	// Access schaltet den Leitstand über Caddy frei. nil: geht hier nicht.
	Access Access

	// Backups macht Backups auf dem Server. nil: geht hier nicht.
	Backups Backups

	// Moves macht den Probe-Umzug auf einen anderen Server. nil: geht hier
	// nicht.
	Moves Moves

	// Containers steuert die Container (Betrieb). nil: geht hier nicht.
	Containers Containers

	// Updates aktualisiert Projekte. nil: geht hier nicht.
	Updates Updates
	// Domains verwaltet die Domains von Caddy. nil: geht hier nicht.
	Domains Domains
	// DomainStates rechnet den Zustand der Seiten aus (die eine Quelle).
	// nil: Der Leitstand bekommt keinen Zustand mit.
	DomainStates func(ctx context.Context, sites []domains.Site) []zustand.Domain
	// Cleanup räumt auf (Build-Cache, Images, Papierkorb). nil: geht hier
	// nicht.
	Cleanup Cleanup
	// Space zeigt Ordner und Dateien nach Größe und löscht auf Wunsch
	// (Speicherplatz). nil: geht hier nicht.
	Space Space
	// Security zeigt offene Ports und schließt sie. nil: geht hier nicht.
	Security Security
	// Performance misst und schlägt Log-Grenze und Swap vor. nil: geht
	// hier nicht.
	Performance Performance
	// Benchmarks führt Leistungstests aus (benchmark.sh). nil:
	// geht hier nicht.
	Benchmarks Benchmarks
	// Tuning ruft tune.sh auf. nil: geht hier nicht.
	Tuning Tuner
	// OldTuning liefert das Tuning des alten Servers nach einem Umzug
	// hierher. nil oder Ergebnis nil: keins.
	OldTuning func() (from string, applied *tuning.Applied, prio []tuning.PrioSet)
	// DropOldTuning verwirft dieses Angebot.
	DropOldTuning func() error
	// Hints sammelt, was Aufmerksamkeit braucht (3f). nil: geht hier
	// nicht.
	Hints Hints
	// Monitor misst rund um die Uhr und hebt den Verlauf auf.
	// nil: geht hier nicht.
	Monitor Monitor
	// Events sammelt, was passiert ist (Übersicht). nil:
	// keine Ereignisse.
	Events *ueberwachung.Journal
	// Notify schickt Warnungen nach außen. nil: geht hier nicht.
	Notify Notifier
	// Limits ändert die Grenzen der Warnungen. nil: geht hier nicht.
	Limits LimitSetter
	// Alerts lässt Warnungen bewusst so. nil: geht hier nicht.
	Alerts AlertKeeper
	// Copy sagt, ob dieser Leitstand eine Probe-Kopie auf einem neuen
	// Server ist; nil-Ergebnis: nein. Eine Kopie macht keine
	// Backups und keine Umzüge.
	Copy func() *CopyInfo
	// Replaced sagt, ob dieser Server durch einen neuen abgelöst ist; nil:
	// nie. Der Leitstand zeigt es auf jeder Seite.
	Replaced func() *umzugstag.Done
	// Arrive macht aus der stillen Kopie vom Umzugstag den echten
	// Leitstand (von Hand, falls die DNS-Prüfung es nicht erkennt). nil:
	// geht hier nicht.
	Arrive func() error
	// ModeFile hält fest, ob ein Umzug ansteht (modus.json im Ordner
	// umzug). Leer: der Leitstand kennt keinen Alltag, ein Umzug steht
	// immer an.
	ModeFile string
	// Arrived sagt, ob dieser Leitstand nach einem Umzug hier übernommen
	// hat (eingezogen.json). Ohne Eintrag in ModeFile gilt dann Alltag.
	Arrived func() bool
	// LookFile hält das Aussehen des Leitstands (darstellung.json), damit
	// PC und Handy gleich aussehen. Leer: Der Leitstand merkt es sich nur
	// im Browser.
	LookFile string
	// Release sieht auf Wunsch nach neuen Versionen (versionshinweis.json).
	// nil: geht hier nicht.
	Release *neueversion.Service
	// ProbeFile hält fest, ob ein Probe-Umzug von hier drüben eine
	// Probe-Kopie stehen hat (probe-ziel.json im Ordner umzug).
	ProbeFile string
	// RemoveGrace ist die Frist zwischen vollständigem Download und
	// Löschen vom Server; 0: DefaultRemoveGrace.
	RemoveGrace time.Duration
	// FinishFile ist der Abschluss eines Umzugs auf dem neuen Server
	// (Schritt 10 des Lotsen); leer: gibt es hier nicht.
	FinishFile string
	// TestAddresses sind die weiteren Adressen des Leitstands, die Caddy
	// gerade bedient (etwa die Testadresse der Probe-Kopie); nil: keine.
	TestAddresses func() []string

	lotseMu   sync.Mutex // lotse.json und der Abschluss
	lookMu    sync.Mutex // darstellung.json
	mu        sync.Mutex
	testAt    time.Time // letzte Probe-Nachricht
	job       *AccessJob
	brun      *backupRun
	mrun      *backupRun // Probe-Umzug
	mtarget   string     // sein Ziel, ohne Passwort
	ujob      *UpdateJob // Aktualisieren eines Projekts
	djob      *DomainJob // Änderung an den Domains
	cjob      *CleanJob  // Aufräumen oder Zurückholen
	pjob      *CleanJob  // Löschen unter Speicherplatz
	lrun      *backupRun // Leistungstest
	lastBench string     // Kennung des zuletzt hier gestarteten Tests
	trun      *backupRun // Tuning anwenden oder zurücknehmen
	tcache    tuneCache  // zuletzt gelesener Status von tune.sh
	treboot   *TuneReboot
	tlock     *lauf.Lock // Sperre bis zum Neustart
	downloads map[string]*downloadLink
	loading   int // laufende Downloads
	fetched   *fetchedBackup
	awaiting  bool // awaitRemoval wartet auf seine Frist
	ctx       context.Context
	links     map[[32]byte]time.Time
	disc      DiscoverStatus
	wg        sync.WaitGroup
}

// DiscoverStatus beschreibt die Bestandsaufnahme.
type DiscoverStatus struct {
	Running bool       `json:"laeuft"`
	Last    *time.Time `json:"zuletzt,omitempty"` // Ende der letzten durch den Dienst
	Error   string     `json:"fehler,omitempty"`  // Fehler der letzten
	Next    *time.Time `json:"naechste,omitempty"`
}

// Status ist die Antwort auf /api/lauf.
type Status struct {
	Version          string          `json:"version"`
	Arch             string          `json:"arch"` // Prozessor des Servers, für die passende Update-Datei
	Lauf             *lauf.Info      `json:"lauf"`
	Bestandsaufnahme DiscoverStatus  `json:"bestandsaufnahme"`
	Zugang           *ZugangStatus   `json:"zugang,omitempty"`    // nil: Freischalten geht hier nicht
	Kopie            *CopyInfo       `json:"kopie,omitempty"`     // nil: keine Probe-Kopie
	Abgeloest        *umzugstag.Done `json:"abgeloest,omitempty"` // nil: nicht abgelöst
	// Alltag: Kein Umzug steht an. Der Leitstand zeigt dann Betrieb statt
	// Hinweisen zu Auswahl und Umzug, und ein vollständig
	// heruntergeladenes Backup verschwindet vom Server.
	Alltag bool `json:"alltag,omitempty"`
	// ProbeKopie: Ein Probe-Umzug von hier hat auf einem anderen Server
	// eine Probe-Kopie angelegt, die noch steht.
	ProbeKopie *ProbeCopy `json:"probe_kopie,omitempty"`
	// AbschlussOffen: Nach dem Einzug sind Punkte von „Umzug
	// abschließen“ offen (Schritt 10 des Lotsen).
	AbschlussOffen bool `json:"abschluss_offen,omitempty"`
}

// CopyInfo beschreibt einen Leitstand, der als Probe-Kopie auf einem neuen
// Server läuft.
type CopyInfo struct {
	From       string    `json:"von,omitempty"` // der alte Server
	Since      time.Time `json:"seit"`
	TestDomain string    `json:"testadresse,omitempty"`
	// Move: am Umzugstag eingerichtet; der Leitstand übernimmt, sobald
	// die Domains hierher zeigen.
	Move bool `json:"umzug,omitempty"`
}

// copyBlocked ist der Grund, warum eine Kopie etwas nicht darf; leer: darf.
func (d *Dienst) copyBlocked(what string) string {
	if d.Copy == nil {
		return ""
	}
	c := d.Copy()
	if c == nil {
		return ""
	}
	from := "dem alten Server"
	if c.From != "" {
		from = c.From
	}
	return "Dieser Leitstand ist eine Probe-Kopie von " + from + ". " + what + " gehen hier nicht; dafür ist der Leitstand auf " + from + " da."
}

// ZugangStatus ist der kurze Stand des Zugangs von außen für jede Seite.
type ZugangStatus struct {
	Domain  string `json:"adresse,omitempty"` // freigeschaltet unter
	Running bool   `json:"laeuft,omitempty"`  // Freischalten oder Zurücknehmen läuft
}

// Link ist ein Einmal-Link für den Notausgang.
type Link struct {
	Token string    `json:"token"`
	Until time.Time `json:"gilt_bis"`
}

type ctxKey struct{}

func (d *Dienst) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Dienst) logf(format string, args ...any) {
	if d.Log != nil {
		fmt.Fprintf(d.Log, format+"\n", args...)
	}
}

func (d *Dienst) defaults() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Sessions == nil {
		d.Sessions = sitzung.New("")
	}
	if d.Sessions.Now == nil && d.Now != nil {
		d.Sessions.Now = d.Now
	}
	if d.Lockout == nil {
		d.Lockout = &anmeldung.Sperre{}
	}
	if d.Lockout.Now == nil && d.Now != nil {
		d.Lockout.Now = d.Now
	}
}

// Handler liefert die Aufträge, die der Dienst annimmt. /api/… nur mit
// gültiger Anmeldung (die Website schickt sie mit), /intern/… nur für die
// Website und root.
func (d *Dienst) Handler() http.Handler {
	d.defaults()
	mux := http.NewServeMux()
	if d.Data != nil {
		mux.Handle("GET /api/state", d.session(false, d.Data.ServeHTTP))
		mux.Handle("POST /api/save", d.session(true, d.handleSave))
	}
	mux.HandleFunc("GET /api/lauf", d.session(false, d.handleStatus))
	mux.HandleFunc("POST /api/umzug/modus", d.session(false, d.handleMode))
	mux.HandleFunc("GET /api/umzug/lotse", d.session(false, d.handleLotse))
	mux.HandleFunc("POST /api/umzug/lotse", d.session(false, d.handleLotseSet))
	mux.HandleFunc("GET /api/umzug/abschluss", d.session(false, d.handleWrapUp))
	mux.HandleFunc("POST /api/umzug/abschluss", d.session(false, d.handleWrapUpSet))
	mux.HandleFunc("POST /api/bestandsaufnahme", d.session(false, d.handleDiscover))
	mux.HandleFunc("POST /api/bestaetigen", d.session(false, d.handleConfirm))
	mux.HandleFunc("GET /api/sitzung", d.session(false, d.handleSessionInfo))
	mux.HandleFunc("GET /api/protokoll", d.session(false, d.handleJournal))
	mux.HandleFunc("GET /api/darstellung", d.session(false, d.handleLook))
	mux.HandleFunc("POST /api/darstellung", d.session(false, d.handleLookSet))
	mux.HandleFunc("POST /api/update", d.session(true, d.handleUpdate))
	if d.Release != nil {
		mux.HandleFunc("GET /api/versionshinweis", d.session(false, d.handleNeueVersion))
		mux.HandleFunc("POST /api/versionshinweis", d.session(false, d.handleNeueVersionSet))
	}
	if d.Access != nil {
		mux.HandleFunc("GET /api/zugang", d.session(false, d.handleAccess))
		mux.HandleFunc("POST /api/zugang/pruefen", d.session(false, d.handleAccessCheck))
		mux.HandleFunc("POST /api/zugang/freischalten", d.session(true, d.handlePublish))
		mux.HandleFunc("POST /api/zugang/zuruecknehmen", d.session(true, d.handleUnpublish))
	}
	if d.Backups != nil {
		mux.HandleFunc("GET /api/backup", d.session(false, d.handleBackup))
		mux.HandleFunc("POST /api/backup/probelauf", d.session(false, d.handleBackupPlan))
		mux.HandleFunc("POST /api/backup/starten", d.session(true, d.handleBackupStart))
		mux.HandleFunc("POST /api/backup/antwort", d.session(false, d.handleBackupAnswer))
		mux.HandleFunc("POST /api/backup/abbrechen", d.session(false, d.handleBackupCancel))
		mux.HandleFunc("POST /api/backup/herunterladen", d.session(true, d.handleDownloadLink))
		mux.HandleFunc("GET "+DownloadPrefix+"{token}", d.session(false, d.handleDownload))
		mux.HandleFunc("POST /api/backup/loeschen", d.session(true, d.handleBackupRemove))
	}
	if d.Moves != nil {
		mux.HandleFunc("GET /api/umzug", d.session(false, d.handleMove))
		mux.HandleFunc("POST /api/umzug/probelauf", d.session(true, d.handleMovePlan))
		mux.HandleFunc("POST /api/umzug/starten", d.session(true, d.handleMoveStart))
		mux.HandleFunc("POST /api/umzug/probe-anhalten", d.session(true, d.handleMoveHalt))
		mux.HandleFunc("POST /api/umzug/probe-entfernen", d.session(true, d.handleMoveRemove))
		mux.HandleFunc("GET /api/umzug/dns", d.session(false, d.handleMoveDNS))
		mux.HandleFunc("POST /api/umzug/dns", d.session(false, d.handleMoveDNSExtra))
		mux.HandleFunc("GET /api/umzug/server-ebene", d.session(false, d.handleServerLevel))
		mux.HandleFunc("POST /api/umzug/server-ebene", d.session(false, d.handleServerChoose))
		mux.HandleFunc("GET /api/umzug/umzugstag", d.session(false, d.handleDay))
		mux.HandleFunc("POST /api/umzug/umzugstag", d.session(true, d.handleDayStart))
		mux.HandleFunc("POST /api/umzug/zurueck", d.session(true, d.handleBack))
		mux.HandleFunc("POST /api/umzug/abschliessen", d.session(true, d.handleFinish))
		mux.HandleFunc("POST /api/umzug/uebernehmen", d.session(true, d.handleArrive))
		mux.HandleFunc("POST /api/umzug/antwort", d.session(false, d.handleMoveAnswer))
		mux.HandleFunc("POST /api/umzug/abbrechen", d.session(false, d.handleMoveCancel))
	}
	if d.Containers != nil {
		mux.HandleFunc("GET /api/container", d.session(false, d.handleContainers))
		mux.HandleFunc("POST /api/container/vorschau", d.session(false, d.handleContainerPlan))
		mux.HandleFunc("POST /api/container/aktion", d.session(true, d.handleContainerAction))
		mux.HandleFunc("GET /api/container/logs", d.session(false, d.handleContainerLogs))
		mux.HandleFunc("GET /api/container/details", d.session(false, d.handleContainerDetails))
	}
	if d.Updates != nil {
		mux.HandleFunc("GET /api/aktualisieren", d.session(false, d.handleUpdates))
		mux.HandleFunc("POST /api/aktualisieren/nachsehen", d.session(false, d.handleUpdateCheck))
		mux.HandleFunc("POST /api/aktualisieren/vorschau", d.session(false, d.handleUpdatePlan))
		mux.HandleFunc("POST /api/aktualisieren/starten", d.session(true, d.handleUpdateStart))
		mux.HandleFunc("POST /api/aktualisieren/zurueck/vorschau", d.session(false, d.handleRevertPlan))
		mux.HandleFunc("POST /api/aktualisieren/zurueck", d.session(true, d.handleRevert))
	}
	if d.Domains != nil {
		mux.HandleFunc("GET /api/domains", d.session(false, d.handleDomains))
		mux.HandleFunc("GET /api/domains/zustand", d.session(false, d.handleDomainStates))
		mux.HandleFunc("POST /api/domains/ordnen/vorschau", d.session(false, d.handleReorgPlan))
		mux.HandleFunc("POST /api/domains/ordnen", d.session(true, d.handleReorg))
		mux.HandleFunc("POST /api/domains/aendern/vorschau", d.session(false, d.handleDomainPlan))
		mux.HandleFunc("POST /api/domains/aendern", d.session(true, d.handleDomainChange))
	}
	if d.Cleanup != nil {
		mux.HandleFunc("GET /api/aufraeumen", d.session(false, d.handleCleanup))
		mux.HandleFunc("POST /api/aufraeumen/vorschau", d.session(false, d.handleCleanPlan))
		mux.HandleFunc("POST /api/aufraeumen/starten", d.session(true, d.handleCleanStart))
		mux.HandleFunc("POST /api/aufraeumen/zurueckholen", d.session(true, d.handleCleanRestore))
	}
	if d.Space != nil {
		mux.HandleFunc("GET /api/platz/ordner", d.session(false, d.handleSpaceList))
		mux.HandleFunc("POST /api/platz/messen", d.session(false, d.handleSpaceMeasure))
		mux.HandleFunc("POST /api/platz/vorschau", d.session(false, d.handleSpacePlan))
		mux.HandleFunc("POST /api/platz/loeschen", d.session(true, d.handleSpaceRun))
	}
	if d.Security != nil {
		mux.HandleFunc("GET /api/sicherheit", d.session(false, d.handleSecurity))
		mux.HandleFunc("POST /api/sicherheit/vorschau", d.session(false, d.handleSecurityPlan))
		mux.HandleFunc("POST /api/sicherheit/aendern", d.session(true, d.handleSecurityChange))
		mux.HandleFunc("POST /api/sicherheit/behalten", d.session(false, d.handleSecurityKeep))
		mux.HandleFunc("POST /api/sicherheit/neu-laden", d.session(true, d.handleSecurityReload))
		mux.HandleFunc("POST /api/sicherheit/offen-lassen", d.session(false, d.handleSecurityKeepOpen))
	}
	if d.Hints != nil {
		mux.HandleFunc("GET /api/hinweise", d.session(false, d.handleHints))
	}
	if d.Monitor != nil {
		mux.HandleFunc("GET /api/ueberwachung", d.session(false, d.handleMonitor))
		mux.HandleFunc("GET /api/ueberwachung/verlauf", d.session(false, d.handleHistory))
		mux.HandleFunc("GET /api/ueberwachung/live", d.session(false, d.handleLive))
	}
	if d.Events != nil {
		mux.HandleFunc("GET /api/ereignisse", d.session(false, d.handleEvents))
	}
	if d.Notify != nil {
		mux.HandleFunc("GET /api/ueberwachung/nachrichten", d.session(false, d.handleNotify))
		mux.HandleFunc("POST /api/ueberwachung/nachrichten/geraet", d.session(true, d.handleNotifyDevice))
		mux.HandleFunc("POST /api/ueberwachung/nachrichten/geraet/entfernen", d.session(true, d.handleNotifyRemove))
		mux.HandleFunc("POST /api/ueberwachung/nachrichten/ntfy", d.session(true, d.handleNotifyNtfy))
		mux.HandleFunc("POST /api/ueberwachung/nachrichten/herzschlag", d.session(true, d.handleNotifyBeat))
		mux.HandleFunc("POST /api/ueberwachung/nachrichten/aus", d.session(true, d.handleNotifyOff))
		mux.HandleFunc("POST /api/ueberwachung/nachrichten/probe", d.session(false, d.handleNotifyTest))
	}
	if d.Limits != nil {
		mux.HandleFunc("POST /api/ueberwachung/grenzen", d.session(false, d.handleLimits))
	}
	if d.Alerts != nil {
		mux.HandleFunc("POST /api/ueberwachung/so-lassen", d.session(false, d.handleAlertKeep))
	}
	if d.Performance != nil {
		mux.HandleFunc("GET /api/leistung", d.session(false, d.handlePerformance))
		mux.HandleFunc("POST /api/leistung/vorschau", d.session(false, d.handlePerfPlan))
		mux.HandleFunc("POST /api/leistung/aendern", d.session(true, d.handlePerfChange))
	}
	if d.Benchmarks != nil {
		mux.HandleFunc("GET /api/leistungstest", d.session(false, d.handleBench))
		mux.HandleFunc("GET /api/leistungstest/bericht", d.session(false, d.handleBenchReport))
		mux.HandleFunc("POST /api/leistungstest/starten", d.session(true, d.handleBenchStart))
		mux.HandleFunc("POST /api/leistungstest/abbrechen", d.session(false, d.handleBenchCancel))
		mux.HandleFunc("GET /api/leistungstest/zip", d.session(false, d.handleBenchZip))
		mux.HandleFunc("GET /api/leistungstest/claim", d.session(false, d.handleBenchClaim))
		mux.HandleFunc("POST /api/leistungstest/loeschen", d.session(false, d.handleBenchDelete))
		mux.HandleFunc("POST /api/leistungstest/geekbench-entfernen", d.session(false, d.handleBenchGeekbench))
	}
	if d.Tuning != nil {
		mux.HandleFunc("GET /api/tuning", d.session(false, d.handleTune))
		mux.HandleFunc("POST /api/tuning/vorschau", d.session(false, d.handleTunePlan))
		mux.HandleFunc("POST /api/tuning/anwenden", d.session(true, d.handleTuneApply))
		mux.HandleFunc("POST /api/tuning/zuruecknehmen/vorschau", d.session(false, d.handleTuneRestorePlan))
		mux.HandleFunc("POST /api/tuning/zuruecknehmen", d.session(true, d.handleTuneRestore))
		mux.HandleFunc("GET /api/tuning/dienste", d.session(false, d.handleTuneServices))
		mux.HandleFunc("POST /api/tuning/vorrang", d.session(true, d.handleTunePrio))
		mux.HandleFunc("POST /api/tuning/neustart", d.session(true, d.handleTuneReboot))
		mux.HandleFunc("POST /api/tuning/alter-server/verwerfen", d.session(false, d.handleTuneDropOld))
	}
	mux.HandleFunc("POST /intern/anmelden", d.handleLogin)
	mux.HandleFunc("POST /intern/link/einloesen", d.handleRedeem)
	mux.HandleFunc("POST /intern/sitzung", d.handleCheckSession)
	mux.HandleFunc("POST /intern/abmelden", d.handleLogout)
	mux.HandleFunc("POST /intern/link", d.admin(d.handleNewLink))
	mux.HandleFunc("GET /intern/passwort", d.admin(d.handlePasswordState))
	mux.HandleFunc("POST /intern/passwort", d.admin(d.handleSetPassword))
	mux.HandleFunc("GET /intern/lauf", d.admin(d.handleStatus))
	return d.peer(limitBody(mux))
}

// peer lässt nur die Website und root herein.
func (d *Dienst) peer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := r.Context().Value(ctxKey{}).(int)
		if !ok || (uid != d.WebUID && uid != d.AdminUID) {
			writeJSON(w, http.StatusForbidden, apiError{Error: "Dieser Benutzer darf den HostDeck-Dienst nicht ansprechen."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (d *Dienst) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if uid, _ := r.Context().Value(ctxKey{}).(int); uid != d.AdminUID {
			writeJSON(w, http.StatusForbidden, apiError{Error: "Nur root darf das."})
			return
		}
		next(w, r)
	}
}

// Serve bedient Verbindungen auf ln, bis ctx endet. Dazu laufen die
// nächtliche Bestandsaufnahme und beim Start die Rettung abgebrochener
// Läufe.
func (d *Dienst) Serve(ctx context.Context, ln net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.mu.Lock()
	d.ctx = ctx
	d.mu.Unlock()
	hs := &http.Server{
		Handler:           d.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ConnContext: func(c context.Context, conn net.Conn) context.Context {
			if uid, ok := PeerUID(conn); ok {
				return context.WithValue(c, ctxKey{}, uid)
			}
			return c
		},
	}
	if d.Recover != nil {
		d.wg.Add(1)
		go func() { defer d.wg.Done(); d.rescue(ctx) }()
	}
	if d.Nightly > 0 && d.Discover != nil {
		d.wg.Add(1)
		go func() { defer d.wg.Done(); d.nightly(ctx) }()
	}
	if d.Monitor != nil {
		d.wg.Add(1)
		go func() { defer d.wg.Done(); d.Monitor.Run(ctx) }()
	}
	if n, ok := d.Notify.(interface{ Run(context.Context) }); ok && n != nil {
		d.wg.Add(1)
		go func() { defer d.wg.Done(); n.Run(ctx) }()
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	var err error
	select {
	case <-ctx.Done():
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		_ = hs.Shutdown(sctx)
		c()
	case err = <-errc:
		cancel()
	}
	d.wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// rescue startet beim Start des Dienstes Container, die ein abgebrochener
// Lauf gestoppt hat. Läuft gerade ein Lauf, gehören sie ihm.
func (d *Dienst) rescue(ctx context.Context) {
	l, err := lauf.TakeIn(d.RunDir, "Dienste starten")
	if err != nil {
		d.logf("Container eines abgebrochenen Laufs: nicht geprüft (%v)", err)
		return
	}
	defer l.Release()
	d.Recover(ctx)
}

// ---------- Bestandsaufnahme ----------

// StartDiscover startet eine Bestandsaufnahme im Hintergrund. Läuft schon
// etwas, kommt ein *lauf.BusyError.
func (d *Dienst) StartDiscover(why string) error {
	if d.Discover == nil {
		return errors.New("Bestandsaufnahme ist hier nicht eingerichtet")
	}
	l, err := lauf.TakeIn(d.RunDir, "Bestandsaufnahme")
	if err != nil {
		return err
	}
	d.mu.Lock()
	ctx := d.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	d.disc.Running = true
	d.mu.Unlock()
	d.logf("Bestandsaufnahme startet (%s)", why)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		err := d.Discover(ctx)
		// Erst die Sperre frei, dann „fertig“ melden: Wer den Stand liest,
		// soll nach dem Ende nie noch die Bestandsaufnahme als Lauf sehen.
		l.Release()
		now := d.now()
		d.mu.Lock()
		d.disc.Running = false
		d.disc.Last = &now
		d.disc.Error = ""
		if err != nil {
			d.disc.Error = err.Error()
		}
		d.mu.Unlock()
		if err != nil {
			d.logf("Bestandsaufnahme: Fehler: %v", err)
		} else {
			d.logf("Bestandsaufnahme fertig")
		}
	}()
	return nil
}

// nextAt liefert den nächsten Zeitpunkt nach now, der at nach Mitternacht
// liegt (Ortszeit).
func nextAt(now time.Time, at time.Duration) time.Time {
	y, m, dd := now.Date()
	t := time.Date(y, m, dd, 0, 0, 0, 0, now.Location()).Add(at)
	if !t.After(now) {
		t = time.Date(y, m, dd+1, 0, 0, 0, 0, now.Location()).Add(at)
	}
	return t
}

// retryBusy: Ist nachts gerade ein Lauf aktiv, versucht es der Dienst so
// viel später noch einmal, höchstens dreimal.
const retryBusy = 30 * time.Minute

func (d *Dienst) nightly(ctx context.Context) {
	for {
		next := nextAt(d.now(), d.Nightly)
		tries := 0
		for {
			d.mu.Lock()
			n := next
			d.disc.Next = &n
			d.mu.Unlock()
			t := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			err := d.StartDiscover("nachts")
			var busy *lauf.BusyError
			if errors.As(err, &busy) && tries < 3 {
				tries++
				next = d.now().Add(retryBusy)
				continue
			}
			if err != nil {
				d.logf("Nächtliche Bestandsaufnahme: %v", err)
			}
			break
		}
		// Nachts auch nachsehen, ob es für Projekte Neues gibt. Das liest
		// nur; aktualisiert wird nie von selbst.
		if d.Updates != nil {
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
				defer cancel()
				if _, err := d.Updates.Check(cctx, ""); err != nil {
					d.logf("Nächtliches Nachsehen nach Updates: %v", err)
				}
			}()
		}
		if d.Cleanup != nil {
			d.wg.Add(1)
			go func() { defer d.wg.Done(); d.expireTrash(ctx) }()
		}
		// Nachts die Platte schonend neu messen (Speicherplatz).
		if d.Space != nil {
			d.startMeasure(ctx, "nachts")
		}
	}
}

// ---------- Einmal-Links ----------

// NewLink erzeugt einen Einmal-Link, der LinkTTL gilt.
func (d *Dienst) NewLink() (Link, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return Link{}, err
	}
	tok := hex.EncodeToString(b)
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.links == nil {
		d.links = map[[32]byte]time.Time{}
	}
	for k, until := range d.links {
		if !now.Before(until) {
			delete(d.links, k)
		}
	}
	if len(d.links) >= maxLinks {
		return Link{}, errors.New("zu viele offene Links; bitte zwei Minuten warten")
	}
	until := now.Add(LinkTTL)
	d.links[sha256.Sum256([]byte(tok))] = until
	return Link{Token: tok, Until: until}, nil
}

// Redeem löst einen Einmal-Link ein. Er gilt danach nicht mehr.
func (d *Dienst) Redeem(tok string) bool {
	k := sha256.Sum256([]byte(tok))
	d.mu.Lock()
	defer d.mu.Unlock()
	until, ok := d.links[k]
	if !ok {
		return false
	}
	delete(d.links, k)
	return d.now().Before(until)
}

// ---------- Aufträge ----------

func (d *Dienst) Status() Status {
	st := Status{Version: d.Version, Arch: runtime.GOARCH}
	// Erst den eigenen Stand, dann die Sperre lesen: Die Bestandsaufnahme gibt
	// die Sperre frei, bevor sie „fertig“ meldet. So zeigt ein Stand mit
	// „fertig“ nie noch die Bestandsaufnahme als laufenden Lauf.
	d.mu.Lock()
	st.Bestandsaufnahme = d.disc
	running := d.job != nil && d.job.Running
	d.mu.Unlock()
	if d.Access != nil {
		st.Zugang = &ZugangStatus{Domain: d.Access.Domain(), Running: running}
	}
	if i, err := lauf.CurrentIn(d.RunDir); err == nil {
		st.Lauf = i
	}
	if d.Copy != nil {
		st.Kopie = d.Copy()
	}
	if d.Replaced != nil {
		st.Abgeloest = d.Replaced()
	}
	st.Alltag = d.everyday()
	st.ProbeKopie = d.probeCopy()
	st.AbschlussOffen = d.finishOpen()
	return st
}

func (d *Dienst) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.Status())
}

func (d *Dienst) handleDiscover(w http.ResponseWriter, r *http.Request) {
	err := d.StartDiscover("auf Knopfdruck")
	var busy *lauf.BusyError
	switch {
	case errors.As(err, &busy):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
	default:
		gate, addr := visitor(r)
		d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: "Bestandsaufnahme gestartet", Addr: addr, Gate: gate})
		writeJSON(w, http.StatusAccepted, d.Status())
	}
}

func (d *Dienst) handleNewLink(w http.ResponseWriter, _ *http.Request) {
	l, err := d.NewLink()
	if err != nil {
		writeJSON(w, http.StatusTooManyRequests, apiError{Error: err.Error()})
		return
	}
	d.logf("Einmal-Link für den Notausgang erzeugt, gilt bis %s", l.Until.Local().Format("15:04:05"))
	d.note(protokoll.Entry{Kind: protokoll.Link, Text: "Einmal-Link erzeugt (sudo hostdeck notausgang), gilt bis " + l.Until.Local().Format("15:04"), Addr: AdminAddr})
	writeJSON(w, http.StatusOK, l)
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
