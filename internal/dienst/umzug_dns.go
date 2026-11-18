package dienst

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// Die DNS-Liste der Seite Umzug: Wohin zeigt jede Domain, die Caddy hier
// bedient, laut ihrem zuständigen Nameserver? Am Umzugstag stellt man
// die Einträge beim Domain-Anbieter um; die Liste zeigt mit Haken, was
// schon auf den neuen Server zeigt.

// Wohin ein DNS-Eintrag zeigt.
const (
	DNSHere  = "hier"   // auf diesen Server
	DNSNew   = "neu"    // auf den neuen Server
	DNSOther = "anders" // woandershin, etwa einen Proxy
)

// Stand einer Domain.
const (
	DNSStateNew   = "neu"      // alles zeigt auf den neuen Server
	DNSStateOld   = "alt"      // alles zeigt noch hierher
	DNSStateMixed = "gemischt" // teils hier, teils neu
	DNSStateOther = "anders"   // mindestens ein Eintrag zeigt woandershin
	DNSStateError = "fehler"
)

// DNSRecord ist ein A- oder AAAA-Eintrag.
type DNSRecord struct {
	Type  string `json:"typ"`
	IP    string `json:"ip"`
	TTL   uint32 `json:"ttl,omitempty"` // Sekunden; 0: unbekannt
	Where string `json:"wohin"`
}

// DNSDomain ist eine Domain mit ihren Einträgen.
type DNSDomain struct {
	Name    string      `json:"domain"`
	Server  string      `json:"nameserver,omitempty"` // leer: normaler DNS-Weg
	CNAME   string      `json:"cname,omitempty"`
	Records []DNSRecord `json:"eintraege"`
	State   string      `json:"stand"`
	Err     string      `json:"fehler,omitempty"`
	Manual  bool        `json:"von_hand,omitempty"` // von Hand eingetragen, nicht aus Caddy
}

// DNSReply ist die Antwort auf GET /api/umzug/dns.
type DNSReply struct {
	// Target ist die Adresse des neuen Servers (vom letzten Probe-Umzug),
	// leer wenn noch keiner lief.
	Target  string      `json:"ziel,omitempty"`
	Here    []string    `json:"hier"`
	Domains []DNSDomain `json:"domains"`
	Problem string      `json:"problem,omitempty"`
	// Extra sind die Domains von Hand (nicht in Caddy).
	Extra []string `json:"von_hand"`
	// TargetIPs sind die Adressen des neuen Servers (aus Target).
	TargetIPs []string `json:"ziel_ips,omitempty"`
	// Own ist die Adresse des Leitstands; Test ihre Testadresse für die
	// Probe-Kopie (hostdeck-neu.…), samt Stand laut Nameserver. Leer: der
	// Leitstand hat keine eigene Domain (nur Notausgang).
	Own  string     `json:"leitstand,omitempty"`
	Test *DNSDomain `json:"testadresse,omitempty"`
	// Zones sind die Zonen der Domains mit ihren zuständigen Nameservern:
	// Dort, beim Anbieter dieser Zone, ändert man die Einträge.
	Zones []DNSZone `json:"zonen"`
	// Suggest sind Namen, die noch hierher zeigen, aber weder in Caddy
	// noch in der Liste von Hand stehen (Zone selbst, www, Ziele der
	// MX-Einträge, übliche SRV-Einträge). Eine Zone auflisten kann HostDeck
	// nicht; das sind Vorschläge zum Übernehmen.
	Suggest []DNSDomain `json:"vorschlaege"`
}

// DNSZone ist eine Zone mit ihren zuständigen Nameservern.
type DNSZone struct {
	Name    string   `json:"zone"`
	Servers []string `json:"nameserver"`
	Domains int      `json:"domains"`
}

// MoveDNS kann Moves zusätzlich: die DNS-Liste.
type MoveDNS interface {
	DNS(ctx context.Context) DNSReply
	// SetExtraDomains ersetzt die Domains von Hand.
	SetExtraDomains(list []string) error
}

// ValidDomain prüft einen Hostnamen für die DNS-Liste.
func ValidDomain(d string) bool {
	return strings.Contains(d, ".") && hostName.MatchString(d) && net.ParseIP(d) == nil
}

// handleMoveDNSExtra: POST /api/umzug/dns {"von_hand": [...]}; ohne
// Passwort wie die Grenzen: Es ändert nur, was die Liste prüft.
func (d *Dienst) handleMoveDNSExtra(w http.ResponseWriter, r *http.Request) {
	m, ok := d.Moves.(MoveDNS)
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Die DNS-Liste gibt es hier nicht."})
		return
	}
	var req struct {
		Extra []string `json:"von_hand"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := m.SetExtraDomains(req.Extra); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	gate, addr := visitor(r)
	d.note(protokoll.Entry{Kind: protokoll.Lauf, Text: fmt.Sprintf("DNS-Liste: %d Domains von Hand", len(req.Extra)), Addr: addr, Gate: gate})
	writeJSON(w, http.StatusOK, m.DNS(r.Context()))
}

// handleMoveDNS: GET /api/umzug/dns
func (d *Dienst) handleMoveDNS(w http.ResponseWriter, r *http.Request) {
	m, ok := d.Moves.(MoveDNS)
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "Die DNS-Liste gibt es hier nicht."})
		return
	}
	writeJSON(w, http.StatusOK, m.DNS(r.Context()))
}

// DNSStateOf fasst die Einträge einer Domain zusammen.
func DNSStateOf(recs []DNSRecord) string {
	if len(recs) == 0 {
		return DNSStateError
	}
	here, fresh := 0, 0
	for _, r := range recs {
		switch r.Where {
		case DNSHere:
			here++
		case DNSNew:
			fresh++
		default:
			return DNSStateOther
		}
	}
	switch {
	case here == 0:
		return DNSStateNew
	case fresh == 0:
		return DNSStateOld
	}
	return DNSStateMixed
}
