# Zustände und wo sie angezeigt werden

Ein Wort je Zustand (Glossar), dann jede Stelle
im Code, die einen sichtbaren Zustand ableitet, mit den Ausnahmezuständen, die sie beachten muss.

## Glossar (ein Wort je Zustand)

| Bereich | Zustand | Wort in der Oberfläche | Stufe |
|---|---|---|---|
| Container | läuft | läuft | ok |
| | von dir im Leitstand gestoppt | von dir gestoppt | neutral |
| | ohne dein Zutun aus (Code ≠ 0) | unerwartet aus | rot |
| | Einmal-Job mit Code 0 | Job erledigt | neutral |
| | angelegt, nie gestartet | angelegt | neutral |
| | startet ständig neu | Neustart-Schleife | rot |
| | eigene Prüfung schlägt fehl | „unhealthy“ | gelb |
| | von einem Verwalter angelegt (Spiele-Panel) | verwalteter Server, „steuert das Panel“ | neutral |
| | von einem HostDeck-Lauf kurz gestoppt | (Lauf läuft; Befunde ruhen) | neutral |
| | nach dem letzten Lauf aus (Umzugstag) | seit dem letzten Lauf aus | neutral, Band mit Rückweg |
| Domain | antwortet | antwortet | ok |
| | Ziel oder ganzes Projekt von dir gestoppt | pausiert (Satz: „Du hast … gestoppt“) | neutral |
| | im Leitstand abgeschaltet | aus | neutral |
| | anderer Container des Projekts von dir gestoppt, Domain antwortet nicht | antwortet nicht, mit „Du hast … gestoppt; liegt es daran?“ | gelb |
| | Ziel unerwartet aus | antwortet nicht | rot |
| | Ziel läuft erst seit < 2 Min. | startet | neutral |
| | am Ziel lauscht nichts | zeigt ins Leere | rot |
| | noch nicht geprüft | wird geprüft | neutral |
| | aus der Bestandsaufnahme | „Ziel war aus“, „zeigte ins Leere“ + „Stand der Bestandsaufnahme vom …“ | neutral bzw. rot, nie als Live-Zustand |
| | Domain außerhalb von Caddy | von Hand | neutral |
| | Testadresse der Probe-Kopie | Testadresse | neutral; fehlt sie vor dem Anlegen: „noch nicht angelegt“ |
| Server-Rolle | kein Umzug geplant | Alltag | – |
| | Umzug geplant | Lotse „Schritt X von 10“ | – |
| | Probe-Kopie auf diesem Server | Probe-Kopie | neutral, Band |
| | Probe-Kopie drüben läuft | Probe-Kopie (Abzeichen) | neutral (vorher gelb, siehe Befunde) |
| | Umzugstag | läuft · DNS · Fehler · unterbrochen · zurück · fertig | Fehler/unterbrochen rot |
| | alter Server nach dem Umzug | abgelöst | neutral |
| | neuer Server nach dem Umzug | eingezogen; „Umzug abschließen“ bis zum Ende der Reserve-Woche | neutral |
| Lauf | einer der Läufe von HostDeck | „<Lauf> läuft“ (Chip oben, Abzeichen „läuft“) | neutral |
| Backup | keins · gültig · ungültig · heruntergeladen, Löschen vorgemerkt · vom Server gelöscht | wie links | ungültig gelb |
| Auswahl | gespeichert · offen · geändert, nicht gespeichert · nicht mehr gefunden | wie links | offen/nicht gefunden gelb (nur bei geplantem Umzug in „Braucht dich“, sonst nur auf der Seite) |
| Überwachung | Warnung aktiv · wieder gut, Entwarnung wartet · ruht (Lauf, Umzug, Testfenster) | Warnung · Entwarnung · – | Warnung gelb/rot |

## Statische Inventur

Spalten: G = beachtet „von dir gestoppt“, J = Einmal-Job, V = verwaltet, L = laufender Lauf, A = Alltag,
Ab = abgelöst, K = Probe-Kopie, D = Domain aus. ✓ beachtet, – trifft dort nicht zu, ✗ beachtet nicht
(Kandidat für die Matrix).

| Stelle | Datenquelle | G | J | V | L | A | Ab | K | D |
|---|---|---|---|---|---|---|---|---|---|
| `internal/zustand/zustand.go` `Domains` (eine Quelle für Domains) | Caddy-Sites, Live-Container, Bestandsaufnahme | ✓ | ✓ | ✓ | – | – | – | – | ✓ |
| `internal/ueberwachung/regeln.go` Seiten-Befunde (`seite:`, `zertifikat:`) | `SiteCheck.Paused/Hint` aus `zustand` | ✓ | ✓ | ✓ | ✓ (`pausedDuringRun`) | – | ✗ (S6: auf abgelöstem Server prüfen) | ✗ | ✓ |
| `regeln.go` Container-Befunde (`aus:`, `neustarts:`, `gesundheit:`) | Docker-Stand | ✓ | ✓ | ✓ | ✓ | – | ✗ | ✗ | – |
| `internal/melden` (Mitteilungen) | Ereignisse der Überwachung | über Regeln | | | ✓ | – | ✓ (`Quiet`) | ✓ (`Quiet`) | – |
| `internal/hinweise` (Platte, Speicher, Schleifen, Zertifikate, Ports) | System, Überwachung | über Regeln | | | | – | ✗ | ✗ | – |
| `internal/discover/findings.go` „Tote Route“ | Bestandsaufnahme | ✓ (neu: Hinweis „Pausiert“, K6) | – | – | – | – | – | – | – |
| `leitstand.js` `liveSum`/`liveStatus` (Projekte) | `/api/betrieb` | ✓ | ✓ | ✓ | – | – | – | – | – |
| `leitstand.js` `projBadge` | wie `liveSum` | ✓ | ✓ | ✓ | – | – | – | – | – |
| `leitstand.js` `liveDomainBadge` (Abzeichen Domains) | `/api/domains/zustand` | ✓ (vorher ✗: zählte pausierte rot) | ✓ | ✓ | – | – | – | – | ✓ |
| `leitstand.js` `attention` („Braucht dich“) | Hinweise, Lotse | über Quellen | | | | ✓ | ✓ (neu: keine Umzugspunkte) | ✓ (neu) | – |
| `leitstand.js` `plan`/`planIn`/`routeDead` (Lageplan) | `zOf` aus `/api/domains/zustand` | ✓ (vorher ✗, K3) | ✓ | ✓ | – | – | – | – | ✓ |
| `leitstand.js` `domSum`/`domHead` (Kacheln, Domains-Kopf) | dieselbe Quelle | ✓ (vorher ✗, K2) | ✓ | ✓ | – | – | – | – | ✓ |
| `leitstand.js` `dmLive` (Domains live) | dieselbe Quelle | ✓ | ✓ | ✓ | – | – | – | – | ✓ |
| `leitstand.js` `pageDomainsInv` | Bestandsaufnahme, gekennzeichnet | – (I9: nur „Stand vom …“) | | | | | | | |
| `leitstand.js` `uwSites` (Überwachung, Seiten) | `SiteCheck` | ✓ (Text aus `zustand`) | ✓ | ✓ | ✓ | – | – | – | ✓ |
| `leitstand.js` `pvOverview` (Projektseite, Domains) | `routeSt` aus derselben Quelle | ✓ (vorher ✗, K4) | ✓ | ✓ | – | – | – | – | ✓ |
| `app.js` `ROUTE`/`routeInfo` (Auswahl, Caddy-Bereich) | Live-Zustand, sonst „Stand der Bestandsaufnahme“ | ✓ (vorher ✗, K5/K7) | – | – | – | – | – | – | ✓ |
| `leitstand.js` `moveBadge`, `probeBand` | Status `probe_kopie` | – | – | – | ✓ | ✓ | ✓ | ✓ | – |
| `leitstand.js` Lotse (`lotse`, `stepBox`) | Umzug, Umzugstag, DNS, `lotse.json` | – | – | – | ✓ (Kasten zeigt den Lauf) | ✓ (nur bei geplantem Umzug) | ✓ (Schritt 10) | ✓ (Probe-Kopie hat eigene Seite) | – |
| `leitstand.js` `finishBox` (neuer Server) | `/api/umzug/abschluss` | – | – | – | – | ✓ (einzige Ausnahme zu I3) | – | – | – |

Die mit ✗ markierten Zellen prüft das Drehbuch S6 im Labor (Überwachung auf einem abgelösten Server und auf
einer Probe-Kopie).
