package discover

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// serverLabel liest aus der server.properties eines Minecraft-Servers nur
// motd und level-name, damit man Server mit Zufallsnamen erkennt. Andere
// Werte wie rcon.password bleiben ungelesen.
func (s *scan) serverLabel(dir string) string {
	b, ok := s.readRegular(dir+"/server.properties", 256<<10)
	if !ok {
		return ""
	}
	var motd, level string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok {
			switch strings.TrimSpace(k) {
			case "motd":
				motd = cleanMOTD(v)
			case "level-name":
				level = cleanMOTD(v)
			}
		}
	}
	var parts []string
	if motd != "" && motd != "A Minecraft Server" {
		parts = append(parts, "„"+motd+"“")
	}
	if level != "" && level != "world" {
		parts = append(parts, "Welt "+level)
	}
	return strings.Join(parts, ", ")
}

// cleanMOTD löst Escapes von Java-Properties auf und entfernt Farbcodes
// (§a, §l …) und Zeilenumbrüche.
func cleanMOTD(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '\\' || i+1 == len(v) {
			b.WriteByte(c)
			continue
		}
		i++
		switch v[i] {
		case 'u':
			if i+4 < len(v) {
				if n, err := strconv.ParseUint(v[i+1:i+5], 16, 32); err == nil {
					b.WriteRune(rune(n))
					i += 4
					continue
				}
			}
			b.WriteByte('u')
		case 'n', 'r', 't':
			b.WriteByte(' ')
		default:
			b.WriteByte(v[i])
		}
	}
	var out strings.Builder
	rs := []rune(b.String())
	for i := 0; i < len(rs); i++ {
		if rs[i] == '§' {
			i++ // Farbcode überspringen
			continue
		}
		if rs[i] < ' ' {
			continue
		}
		out.WriteRune(rs[i])
	}
	res := strings.Join(strings.Fields(out.String()), " ")
	if utf8.RuneCountInString(res) > 60 {
		res = string([]rune(res)[:59]) + "…"
	}
	return res
}
