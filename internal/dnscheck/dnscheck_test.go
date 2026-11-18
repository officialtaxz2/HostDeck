package dnscheck

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"
)

type rr struct {
	typ   uint16
	ttl   uint32
	data  []byte // A/AAAA
	cname string
}

// fakeNS ist ein Nameserver im Speicher auf 127.0.0.1.
func fakeNS(t *testing.T, zone map[string][]rr, truncate bool) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	port := pc.LocalAddr().(*net.UDPAddr).Port
	var tl net.Listener
	if truncate {
		tl, err = net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Skip("TCP-Port belegt")
		}
		t.Cleanup(func() { tl.Close() })
		go func() {
			for {
				c, err := tl.Accept()
				if err != nil {
					return
				}
				l := make([]byte, 2)
				ioReadFull(c, l)
				q := make([]byte, binary.BigEndian.Uint16(l))
				ioReadFull(c, q)
				resp := answer(q, zone, false)
				binary.BigEndian.PutUint16(l, uint16(len(resp)))
				c.Write(append(l, resp...))
				c.Close()
			}
		}()
	}
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pc.WriteTo(answer(buf[:n], zone, truncate), addr)
		}
	}()
	return port
}

func answer(q []byte, zone map[string][]rr, truncate bool) []byte {
	name, off, _ := readName(q, 12)
	qtype := binary.BigEndian.Uint16(q[off:])
	resp := append([]byte{}, q[:off+4]...)
	resp[2] = 0x84 // Antwort, autoritativ
	if truncate {
		resp[2] |= 0x02
		return resp
	}
	recs, ok := zone[name]
	if !ok {
		resp[3] = 3
		return resp
	}
	n := 0
	for _, r := range recs {
		if r.typ != qtype && r.typ != TypeCNAME {
			continue
		}
		n++
		resp = append(resp, 0xc0, 12) // Verweis auf den Namen der Frage
		resp = binary.BigEndian.AppendUint16(resp, r.typ)
		resp = binary.BigEndian.AppendUint16(resp, 1)
		resp = binary.BigEndian.AppendUint32(resp, r.ttl)
		data := r.data
		if r.typ == TypeCNAME {
			data, _ = encodeName(r.cname)
		}
		resp = binary.BigEndian.AppendUint16(resp, uint16(len(data)))
		resp = append(resp, data...)
	}
	binary.BigEndian.PutUint16(resp[6:], uint16(n))
	return resp
}

func testResolver(port int) *Resolver {
	return &Resolver{
		Port: port, Timeout: time.Second,
		NS: func(_ context.Context, z string) ([]string, error) {
			if z == "beispiel.example" || z == "anderer.example" {
				return []string{"127.0.0.1"}, nil
			}
			return nil, errors.New("no such host")
		},
		Addr: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("192.0.2.99")}, nil },
	}
}

func TestLookupBeimZustaendigenNameserver(t *testing.T) {
	port := fakeNS(t, map[string][]rr{
		"shop.beispiel.example": {{typ: TypeA, ttl: 3600, data: []byte{203, 0, 113, 7}}, {typ: TypeAAAA, ttl: 300, data: net.ParseIP("2001:db8::7")}},
		"www.beispiel.example":  {{typ: TypeCNAME, ttl: 300, cname: "lb.anderer.example"}},
		"lb.anderer.example":    {{typ: TypeA, ttl: 60, data: []byte{198, 51, 100, 1}}},
	}, false)
	r := testResolver(port)
	a := r.Lookup(context.Background(), "Shop.Beispiel.Example.")
	if a.Err != "" || a.Server != "127.0.0.1" || len(a.Records) != 2 {
		t.Fatalf("%+v", a)
	}
	if a.Records[0].Type != "A" || a.Records[0].IP.String() != "203.0.113.7" || a.Records[0].TTL != 3600 ||
		a.Records[1].Type != "AAAA" || a.Records[1].IP.String() != "2001:db8::7" || a.Records[1].TTL != 300 {
		t.Errorf("Einträge: %+v", a.Records)
	}
	w := r.Lookup(context.Background(), "www.beispiel.example")
	if w.CNAME != "lb.anderer.example" || len(w.Records) != 1 || w.Records[0].IP.String() != "198.51.100.1" {
		t.Errorf("CNAME: %+v", w)
	}
	// Gibt es die Domain nicht, ist das die Antwort.
	x := r.Lookup(context.Background(), "fehlt.beispiel.example")
	if x.Server != "127.0.0.1" || len(x.Records) != 0 || x.Err == "" {
		t.Errorf("NXDOMAIN: %+v", x)
	}
	// Ohne erreichbaren Nameserver: normaler Weg.
	r.Port = 1
	y := r.Lookup(context.Background(), "shop.beispiel.example")
	if y.Server != "" || len(y.Records) != 1 || y.Records[0].IP.String() != "192.0.2.99" {
		t.Errorf("Rückfall: %+v", y)
	}
}

func TestLookupUeberTCPWennAbgeschnitten(t *testing.T) {
	port := fakeNS(t, map[string][]rr{"shop.beispiel.example": {{typ: TypeA, ttl: 120, data: []byte{203, 0, 113, 8}}}}, true)
	a := testResolver(port).Lookup(context.Background(), "shop.beispiel.example")
	if a.Server == "" || len(a.Records) != 1 || a.Records[0].TTL != 120 {
		t.Errorf("%+v", a)
	}
}

func TestReadNameSchleife(t *testing.T) {
	msg := make([]byte, 14)
	msg[12], msg[13] = 0xc0, 12
	if _, _, err := readName(msg, 12); err == nil {
		t.Error("Schleife nicht erkannt")
	}
}
