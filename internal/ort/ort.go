// Package ort legt fest, wo HostDeck als Dienst auf einem Server liegt.
//
// Das Programm liegt an einem festen Ort, der root gehört; Agent, Dienst und
// Website nutzen nur diese eine Datei. Die Daten (Auswahl, Bestandsaufnahme,
// Verlauf) liegen in einem eigenen Ordner, den nur root lesen darf.
package ort

const (
	ProgDir = "/usr/local/lib/hostdeck"
	Prog    = ProgDir + "/hostdeck"
	// BinLink ist die Verknüpfung auf das Programm, damit „sudo hostdeck …“
	// überall geht.
	BinLink = "/usr/local/bin/hostdeck"

	DataDir   = "/var/lib/hostdeck"
	ResultDir = DataDir + "/ergebnis" // inventory.json, manifest.yaml, …

	// Dateien des Dienstes in DataDir.
	PasswordFile  = "anmeldung.json"       // Fingerabdruck des Leitstand-Passworts
	LockoutFile   = "sperre.json"          // Fehlversuche und Sperren
	SessionsFile  = "sitzungen.json"       // Anmeldungen (nur Fingerabdrücke)
	JournalFile   = "protokoll.jsonl"      // Protokoll
	AccessFile    = "freischaltung.json"   // Zugang von außen: Adresse, seit wann
	CaddyJournal  = "caddy-rueckweg.json"  // alte Caddy-Dateien, solange eine Änderung läuft
	BackupRepo    = "backup"               // Backups auf dem Server (restic-Repository)
	RunLogs       = "laeufe"               // Ausgabe der Backup-Läufe aus dem Leitstand
	MoveDir       = "umzug"                // Probe-Umzug: bekannte Server, letztes Ziel, Ausgaben
	StoppedFile   = "gestoppt.json"        // Container, die du im Leitstand gestoppt hast
	UpdatesDir    = "aktualisieren"        // Prüfstand und Rückfall beim Aktualisieren von Projekten
	DomainsFile   = "domains.json"         // Prüfsummen der Caddy-Dateien, die HostDeck geschrieben hat
	TrashDir      = "papierkorb"           // Papierkorb beim Aufräumen (restic-Repository, nur root)
	ShieldRules   = "schutz.nft"           // nftables-Tabelle von HostDeck mit den geschlossenen Ports
	ShieldFile    = "schutz.json"          // welche Ports HostDeck geschlossen hat
	PerfDir       = "leistung"             // Vorher-Stände von daemon.json und fstab
	MonitorDir    = "ueberwachung"         // Verlauf der Messwerte
	BenchDir      = "leistungstest"        // Ergebnisse von benchmark.sh; arbeit/ hält Geekbench
	CopyFile      = "probe-kopie.json"     // Leitstand ist eine Probe-Kopie
	OldServerFile = "alter-server.json"    // was HostDeck auf dem alten Server eingestellt hatte
	ReleaseFile   = "versionshinweis.json" // Hinweis auf neue Versionen: an/aus und letzte Antwort von GitHub
	LookFile      = "darstellung.json"     // Aussehen des Leitstands (Farbe, Hintergrund, …), auf allen Geräten gleich

	// ShieldUnit lädt die Tabelle von HostDeck beim Start des Servers.
	ShieldUnit = "hostdeck-schutz.service"

	GateNotausgang = "Notausgang" // Name des Eingangs über SSH

	RunDir       = "/run/hostdeck" // gehört dem Dienst (root)
	DienstSocket = RunDir + "/dienst.sock"

	WebRunDir        = "/run/hostdeck-web" // gehört hostdeck web
	NotausgangSocket = WebRunDir + "/notausgang.sock"

	// Eingang für Caddy, ab dem Freischalten. systemd legt den Socket an
	// (hostdeck-web.socket), mit der Gruppe von Caddy, und reicht ihn an hostdeck web.
	CaddyRunDir = "/run/hostdeck-caddy"
	CaddySocket = CaddyRunDir + "/web.sock"
	CaddySite   = "hostdeck.caddy" // eigene Datei neben dem Caddyfile

	WebUser = "hostdeck-web" // Benutzer ohne Rechte für hostdeck web

	UnitDir    = "/etc/systemd/system"
	UnitDienst = "hostdeck-dienst.service"
	UnitWeb    = "hostdeck-web.service"
	UnitSocket = "hostdeck-web.socket"
)

// Units sind die systemd-Units, die HostDeck selbst anlegt. Die
// Bestandsaufnahme zählt sie nicht als Aufgaben des Servers.
var Units = []string{UnitDienst, UnitWeb, UnitSocket}

// IsOwnUnit sagt, ob eine Unit zu HostDeck selbst gehört.
func IsOwnUnit(name string) bool {
	for _, u := range Units {
		if u == name {
			return true
		}
	}
	return false
}
