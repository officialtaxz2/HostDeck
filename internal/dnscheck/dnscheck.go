// Package dnscheck fragt den zuständigen Nameserver einer Domain direkt,
// wohin sie zeigt. Das ist der Stand, den alle sehen, sobald alte
// Antworten in Zwischenspeichern abgelaufen sind (TTL); ein normaler
// Resolver kann noch eine ältere Antwort liefern.
package dnscheck

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Typen von DNS-Einträgen.
const (
	TypeA     uint16 = 1
	TypeNS    uint16 = 2
	TypeCNAME uint16 = 5
	TypeAAAA  uint16 = 28
)

// Record ist ein A- oder AAAA-Eintrag.
type Record struct {
	Type string // "A" oder "AAAA"
	IP   net.IP
	TTL  uint32 // Sekunden; 0 wenn unbekannt
}

// Answer ist, was der zuständige Nameserver zu einer Domain sagt.
type Answer struct {
	Host    string
	Server  string // gefragter Nameserver; leer: normaler DNS-Weg
	CNAME   string // die Domain ist ein Verweis auf diesen Namen
	Records []Record
	Err     string
}

// Resolver fragt Nameserver. Leere Felder nehmen das System.
type Resolver struct {
	// NS liefert die Nameserver einer Zone; nil: net.DefaultResolver.
	NS func(ctx context.Context, zone string) ([]string, error)
	// Addr löst einen Namen über den normalen DNS-Weg auf; nil:
	// net.DefaultResolver.
	Addr func(ctx context.Context, host string) ([]net.IP, error)
	// Port der Nameserver; 0: 53. Für Tests.
	Port    int
	Timeout time.Duration
}

func (r *Resolver) ns(ctx context.Context, zone string) ([]string, error) {
	if r.NS != nil {
		return r.NS(ctx, zone)
	}
	nss, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range nss {
		out = append(out, strings.TrimSuffix(n.Host, "."))
	}
	return out, nil
}

func (r *Resolver) addr(ctx context.Context, host string) ([]net.IP, error) {
	if r.Addr != nil {
		return r.Addr(ctx, host)
	}
	as, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, a := range as {
		out = append(out, a.IP)
	}
	return out, nil
}

// Zone sucht die Zone von host (host selbst oder ein Elternname mit
// NS-Einträgen) und ihre Nameserver.
func (r *Resolver) Zone(ctx context.Context, host string) (zone string, servers []string, err error) {
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	for i := 0; i < len(labels)-1; i++ {
		z := strings.Join(labels[i:], ".")
		ns, err := r.ns(ctx, z)
		if err == nil && len(ns) > 0 {
			return z, ns, nil
		}
	}
	return "", nil, fmt.Errorf("keine Nameserver für %s gefunden", host)
}

// Lookup fragt den zuständigen Nameserver nach A und AAAA von host. Geht
// das nicht, fragt es über den normalen DNS-Weg (ohne TTL).
func (r *Resolver) Lookup(ctx context.Context, host string) Answer {
	return r.lookup(ctx, strings.ToLower(strings.TrimSuffix(host, ".")), 0)
}

func (r *Resolver) lookup(ctx context.Context, host string, depth int) Answer {
	a := Answer{Host: host}
	_, servers, err := r.Zone(ctx, host)
	if err == nil {
		var last error
		for _, s := range servers {
			ip, err := r.serverIP(ctx, s)
			if err != nil {
				last = err
				continue
			}
			got, err := r.ask(ctx, ip, host)
			var re rcodeError
			if errors.As(err, &re) {
				// Der Nameserver hat geantwortet: Das ist der Stand.
				return Answer{Host: host, Server: s, Err: re.Error()}
			}
			if err != nil {
				last = fmt.Errorf("%s: %v", s, err)
				continue
			}
			got.Server = s
			if got.CNAME != "" && len(got.Records) == 0 && depth < 4 {
				target := r.lookup(ctx, got.CNAME, depth+1)
				got.Records, got.Err = target.Records, target.Err
			}
			return got
		}
		err = last
	}
	// Nameserver nicht direkt erreichbar: normaler DNS-Weg.
	ips, aerr := r.addr(ctx, host)
	if aerr != nil {
		if err != nil {
			a.Err = err.Error()
		} else {
			a.Err = aerr.Error()
		}
		return a
	}
	for _, ip := range ips {
		t := "AAAA"
		if ip.To4() != nil {
			t = "A"
		}
		a.Records = append(a.Records, Record{Type: t, IP: ip})
	}
	return a
}

func (r *Resolver) serverIP(ctx context.Context, server string) (string, error) {
	if ip := net.ParseIP(server); ip != nil {
		return server, nil
	}
	ips, err := r.addr(ctx, server)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip.String(), nil
		}
	}
	if len(ips) > 0 {
		return ips[0].String(), nil
	}
	return "", fmt.Errorf("%s hat keine Adresse", server)
}

// ask fragt einen Nameserver nach A und AAAA.
func (r *Resolver) ask(ctx context.Context, server, host string) (Answer, error) {
	a := Answer{Host: host}
	for _, qt := range []uint16{TypeA, TypeAAAA} {
		msg, err := r.exchange(ctx, server, host, qt)
		if err != nil {
			return a, err
		}
		recs, cname, err := parse(msg, qt)
		if err != nil {
			return a, err
		}
		if cname != "" {
			a.CNAME = cname
		}
		a.Records = append(a.Records, recs...)
	}
	return a, nil
}

func (r *Resolver) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 3 * time.Second
}

// exchange schickt eine Frage per UDP, bei abgeschnittener Antwort per TCP.
func (r *Resolver) exchange(ctx context.Context, server, host string, qtype uint16) ([]byte, error) {
	q, id, err := query(host, qtype)
	if err != nil {
		return nil, err
	}
	port := r.Port
	if port == 0 {
		port = 53
	}
	addr := net.JoinHostPort(server, fmt.Sprint(port))
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	if _, err := c.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return nil, err
		}
		if n < 12 || binary.BigEndian.Uint16(buf) != id {
			continue // fremde Antwort
		}
		msg := buf[:n]
		if msg[2]&0x02 == 0 { // nicht abgeschnitten
			return append([]byte{}, msg...), nil
		}
		break
	}
	// Abgeschnitten: noch einmal über TCP.
	tc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer tc.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = tc.SetDeadline(dl)
	}
	l := make([]byte, 2)
	binary.BigEndian.PutUint16(l, uint16(len(q)))
	if _, err := tc.Write(append(l, q...)); err != nil {
		return nil, err
	}
	if _, err := ioReadFull(tc, l); err != nil {
		return nil, err
	}
	msg := make([]byte, binary.BigEndian.Uint16(l))
	if _, err := ioReadFull(tc, msg); err != nil {
		return nil, err
	}
	if len(msg) < 12 || binary.BigEndian.Uint16(msg) != id {
		return nil, errors.New("falsche Antwort über TCP")
	}
	return msg, nil
}

func ioReadFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// query baut eine Frage ohne Rekursionswunsch.
func query(host string, qtype uint16) ([]byte, uint16, error) {
	var idb [2]byte
	_, _ = rand.Read(idb[:])
	id := binary.BigEndian.Uint16(idb[:])
	b := make([]byte, 12, 64)
	binary.BigEndian.PutUint16(b, id)
	binary.BigEndian.PutUint16(b[4:], 1) // eine Frage
	name, err := encodeName(host)
	if err != nil {
		return nil, 0, err
	}
	b = append(b, name...)
	b = binary.BigEndian.AppendUint16(b, qtype)
	b = binary.BigEndian.AppendUint16(b, 1) // IN
	return b, id, nil
}

func encodeName(host string) ([]byte, error) {
	var b []byte
	for _, l := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if l == "" || len(l) > 63 {
			return nil, fmt.Errorf("ungültiger Name %q", host)
		}
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	if len(b) > 254 {
		return nil, fmt.Errorf("Name zu lang: %q", host)
	}
	return append(b, 0), nil
}

// parse liest die Antworten vom Typ qtype und einen CNAME.
func parse(msg []byte, qtype uint16) ([]Record, string, error) {
	if len(msg) < 12 {
		return nil, "", errors.New("Antwort zu kurz")
	}
	switch rcode := msg[3] & 0x0f; rcode {
	case 0:
	case 3:
		return nil, "", rcodeError("Domain gibt es beim Nameserver nicht (NXDOMAIN)")
	default:
		// etwa verweigert (5) oder Serverfehler (2): anderen Nameserver
		// oder den normalen Weg versuchen.
		return nil, "", fmt.Errorf("Nameserver antwortet mit Fehler %d", rcode)
	}
	qd := int(binary.BigEndian.Uint16(msg[4:]))
	an := int(binary.BigEndian.Uint16(msg[6:]))
	off := 12
	var err error
	for i := 0; i < qd; i++ {
		if _, off, err = readName(msg, off); err != nil {
			return nil, "", err
		}
		off += 4
	}
	var recs []Record
	cname := ""
	for i := 0; i < an; i++ {
		if _, off, err = readName(msg, off); err != nil {
			return nil, "", err
		}
		if off+10 > len(msg) {
			return nil, "", errors.New("Antwort abgeschnitten")
		}
		typ := binary.BigEndian.Uint16(msg[off:])
		ttl := binary.BigEndian.Uint32(msg[off+4:])
		n := int(binary.BigEndian.Uint16(msg[off+8:]))
		off += 10
		if off+n > len(msg) {
			return nil, "", errors.New("Antwort abgeschnitten")
		}
		data := msg[off : off+n]
		switch {
		case typ == qtype && typ == TypeA && n == 4:
			recs = append(recs, Record{Type: "A", IP: net.IP(append([]byte{}, data...)), TTL: ttl})
		case typ == qtype && typ == TypeAAAA && n == 16:
			recs = append(recs, Record{Type: "AAAA", IP: net.IP(append([]byte{}, data...)), TTL: ttl})
		case typ == TypeCNAME:
			if cname, _, err = readName(msg, off); err != nil {
				return nil, "", err
			}
		}
		off += n
	}
	return recs, cname, nil
}

// rcodeError: Der Nameserver hat eindeutig geantwortet, etwa dass es die
// Domain nicht gibt.
type rcodeError string

func (e rcodeError) Error() string { return string(e) }

// readName liest einen Namen samt Verweisen (Kompression).
func readName(msg []byte, off int) (string, int, error) {
	var labels []string
	end := -1
	for jumps := 0; ; {
		if off >= len(msg) {
			return "", 0, errors.New("Name abgeschnitten")
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return strings.Join(labels, "."), end, nil
		case l&0xc0 == 0xc0:
			if off+1 >= len(msg) {
				return "", 0, errors.New("Name abgeschnitten")
			}
			if end < 0 {
				end = off + 2
			}
			if jumps++; jumps > 20 {
				return "", 0, errors.New("Name mit Schleife")
			}
			off = int(binary.BigEndian.Uint16(msg[off:]) & 0x3fff)
		case l > 63:
			return "", 0, errors.New("Name ungültig")
		default:
			if off+1+l > len(msg) {
				return "", 0, errors.New("Name abgeschnitten")
			}
			labels = append(labels, strings.ToLower(string(msg[off+1:off+1+l])))
			off += 1 + l
		}
	}
}
