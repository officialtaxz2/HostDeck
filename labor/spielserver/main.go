//go:build ignore

// Nachgebauter Spielserver für das Prüflabor: beantwortet den Server List Ping von Minecraft Java
// (TCP) oder A2S_INFO von Steam/Source mit Challenge (UDP), sonst nichts.
// Kein Spiel, keine Anmeldung. Gebaut von labor/alt/spiele.sh:
//
//	CGO_ENABLED=0 go build -o labor/lauf/spielserver labor/spielserver/main.go
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"strconv"
)

func main() {
	kind := flag.String("art", "java", "java oder source")
	port := flag.Int("port", 25565, "Port")
	name := flag.String("name", "Labor", "MOTD oder Servername")
	version := flag.String("version", "1.21.4", "Version")
	players := flag.Int("spieler", 0, "Spieler online")
	maxp := flag.Int("max", 20, "Spieler höchstens")
	mapName := flag.String("karte", "c1m1_hotel", "Karte (Source)")
	folder := flag.String("ordner", "left4dead2", "Spielordner (Source)")
	game := flag.String("spiel", "Left 4 Dead 2", "Spiel (Source)")
	flag.Parse()
	addr := ":" + strconv.Itoa(*port)
	if *kind == "source" {
		source(addr, *name, *mapName, *folder, *game, *version, byte(*players), byte(*maxp))
		return
	}
	js, _ := json.Marshal(map[string]any{
		"version":     map[string]any{"name": *version, "protocol": 769},
		"players":     map[string]any{"online": *players, "max": *maxp},
		"description": map[string]any{"text": "", "extra": []any{map[string]any{"text": *name, "color": "gold"}}},
	})
	java(addr, js)
}

func varint(b *bytes.Buffer, v int) {
	u := uint32(v)
	for u&^0x7f != 0 {
		b.WriteByte(byte(u&0x7f | 0x80))
		u >>= 7
	}
	b.WriteByte(byte(u))
}

func readVarint(r *bufio.Reader) (int, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint32(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return int(v), nil
		}
	}
	return 0, io.ErrUnexpectedEOF
}

func java(addr string, js []byte) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			r := bufio.NewReader(c)
			// Handshake und Status-Anfrage überlesen (je Länge und Inhalt).
			for i := 0; i < 2; i++ {
				n, err := readVarint(r)
				if err != nil || n < 0 || n > 1024 {
					return
				}
				if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
					return
				}
			}
			var body bytes.Buffer
			varint(&body, 0)
			varint(&body, len(js))
			body.Write(js)
			var out bytes.Buffer
			varint(&out, body.Len())
			out.Write(body.Bytes())
			_, _ = c.Write(out.Bytes())
		}(c)
	}
}

func source(addr, name, mapName, folder, game, version string, players, maxp byte) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		log.Fatal(err)
	}
	challenge := []byte{0x4c, 0x61, 0x62, 0x6f}
	buf := make([]byte, 1500)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			continue
		}
		q := buf[:n]
		if n < 25 || !bytes.Equal(q[:5], []byte{0xff, 0xff, 0xff, 0xff, 'T'}) {
			continue
		}
		if n < 29 || !bytes.Equal(q[25:29], challenge) {
			_, _ = pc.WriteTo(append([]byte{0xff, 0xff, 0xff, 0xff, 0x41}, challenge...), from)
			continue
		}
		var b bytes.Buffer
		b.Write([]byte{0xff, 0xff, 0xff, 0xff, 0x49, 17})
		for _, s := range []string{name, mapName, folder, game} {
			b.WriteString(s)
			b.WriteByte(0)
		}
		_ = binary.Write(&b, binary.LittleEndian, uint16(550))
		b.Write([]byte{players, maxp, 0, 'd', 'l', 0, 1})
		b.WriteString(version)
		b.WriteByte(0)
		_, _ = pc.WriteTo(b.Bytes(), from)
	}
}
