package caddy

import (
	"errors"
	"fmt"
	"strings"
)

// Arten von Abschnitten auf oberster Ebene eines Caddyfiles.
const (
	SegGlobal  = "global"  // { … } ganz oben: allgemeine Einstellungen
	SegSnippet = "snippet" // (name) { … }
	SegImport  = "import"  // import … ohne Block
	SegSite    = "site"    // adresse[, adresse] { … }
	SegOther   = "other"   // alles andere; HostDeck ordnet so ein Caddyfile nicht um
	SegTrail   = "trail"   // nur Kommentare und Leerzeilen am Ende
)

// Segment ist ein Abschnitt auf oberster Ebene, mit den Kommentaren und
// Leerzeilen, die direkt davor stehen. Alle Segmente hintereinander ergeben
// die Datei Byte für Byte.
type Segment struct {
	Kind  string
	Text  string   // genau so, wie es in der Datei steht
	Addrs []string // Adressen eines Site-Blocks
	Args  []string // Wörter der ersten Zeile, ohne „{“
	Line  int      // erste Zeile mit Inhalt, ab 1
}

// token ist ein Wort einer Zeile; quoted heißt: stand in Anführungszeichen.
type token struct {
	s      string
	quoted bool
}

// lineTokens zerlegt eine Zeile in Wörter, ohne Kommentar. heredoc ist die
// Endmarke, wenn die Zeile ein Heredoc (<<MARKE) beginnt.
func lineTokens(s string) (toks []token, heredoc string) {
	var cur strings.Builder
	in := byte(0) // '"' oder '`'
	have, quoted := false, false
	flush := func() {
		if have {
			toks = append(toks, token{cur.String(), quoted})
		}
		cur.Reset()
		have, quoted = false, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case in != 0:
			if c == '\\' && in == '"' && i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i++
				continue
			}
			if c == in {
				in = 0
				continue
			}
			cur.WriteByte(c)
		case c == '"' || c == '`':
			in, have, quoted = c, true, true
		case c == '#' && !have:
			flush()
			return toks, ""
		case c == ' ' || c == '\t' || c == '\r':
			flush()
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	flush()
	if n := len(toks); n > 0 && !toks[n-1].quoted && strings.HasPrefix(toks[n-1].s, "<<") && len(toks[n-1].s) > 2 {
		heredoc = toks[n-1].s[2:]
	}
	return toks, heredoc
}

// ErrUnbalanced: Die geschweiften Klammern gehen nicht auf.
var ErrUnbalanced = errors.New("die geschweiften Klammern im Caddyfile gehen nicht auf")

// Segments zerlegt ein Caddyfile in seine Abschnitte auf oberster Ebene.
func Segments(b []byte) ([]Segment, error) {
	lines := strings.SplitAfter(string(b), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	var out []Segment
	var pending, text strings.Builder // pending: Kommentare und Leerzeilen davor
	var cur *Segment
	depth, opened, heredoc, first := 0, false, "", true
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\n")
		if heredoc != "" {
			rest, end := heredocEnd(line, heredoc)
			if !end {
				text.WriteString(raw)
				continue
			}
			heredoc, line = "", rest
		}
		toks, hd := lineTokens(line)
		if cur == nil {
			if len(toks) == 0 {
				pending.WriteString(raw)
				continue
			}
			cur, opened = &Segment{Line: i + 1}, false
			text.Reset()
			text.WriteString(pending.String())
			pending.Reset()
		}
		text.WriteString(raw)
		heredoc = hd
		for _, t := range toks {
			switch {
			case !t.quoted && t.s == "{":
				if depth == 0 {
					opened = true
				}
				depth++
			case !t.quoted && t.s == "}":
				depth--
				if depth < 0 {
					return nil, fmt.Errorf("%w (Zeile %d)", ErrUnbalanced, i+1)
				}
			case depth == 0 && !opened:
				cur.Args = append(cur.Args, t.s)
			}
		}
		if depth > 0 || heredoc != "" {
			continue
		}
		if n := len(cur.Args); !opened && n > 0 && strings.HasSuffix(cur.Args[n-1], ",") {
			continue // Adressliste geht in der nächsten Zeile weiter
		}
		cur.Text = text.String()
		cur.Kind = kindOf(cur, opened, first)
		if cur.Kind == SegSite {
			cur.Addrs = addrs(cur.Args)
		}
		first = false
		out = append(out, *cur)
		cur = nil
	}
	if heredoc != "" || depth != 0 || cur != nil {
		return nil, ErrUnbalanced
	}
	if pending.Len() > 0 {
		out = append(out, Segment{Kind: SegTrail, Text: pending.String()})
	}
	return out, nil
}

func kindOf(s *Segment, block, first bool) string {
	a := s.Args
	switch {
	case first && block && len(a) == 0:
		return SegGlobal
	case block && len(a) == 1 && strings.HasPrefix(a[0], "(") && strings.HasSuffix(a[0], ")"):
		return SegSnippet
	case !block && len(a) >= 2 && a[0] == "import":
		return SegImport
	case block && len(a) > 0 && a[0] != "import":
		return SegSite
	}
	return SegOther
}

// addrs macht aus „a.de, b.de“ oder „a.de b.de“ eine Liste.
func addrs(args []string) []string {
	var out []string
	for _, a := range args {
		for _, p := range strings.Split(a, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// HostOf macht aus einer Site-Adresse den Hostnamen: ohne Schema, Port
// und Pfad, klein geschrieben. Leer bei Adressen ohne Host (:8080) und mit
// Platzhaltern ({$DOMAIN}).
func HostOf(addr string) string {
	a := strings.ToLower(addr)
	if strings.Contains(a, "{") {
		return ""
	}
	if i := strings.Index(a, "://"); i >= 0 {
		a = a[i+3:]
	}
	if i := strings.IndexByte(a, '/'); i >= 0 {
		a = a[:i]
	}
	if strings.HasPrefix(a, "[") {
		if i := strings.IndexByte(a, ']'); i > 0 {
			return a[1:i]
		}
	}
	if i := strings.LastIndexByte(a, ':'); i >= 0 {
		a = a[:i]
	}
	return a
}

// Directive ist eine Zeile innerhalb eines Blocks.
type Directive struct {
	Depth int      // 1: direkt im Site-Block
	Words []string // Wörter ohne „{“
	Line  int      // Zeile im Segment, ab 0
	Block bool     // öffnet einen Unterblock
}

// Directives liefert die Zeilen eines Blocks mit ihrer Tiefe.
func Directives(text string) []Directive {
	var out []Directive
	depth := 0
	heredoc := ""
	for i, raw := range strings.Split(text, "\n") {
		line := raw
		if heredoc != "" {
			rest, end := heredocEnd(raw, heredoc)
			if !end {
				continue
			}
			heredoc, line = "", rest
		}
		toks, hd := lineTokens(line)
		heredoc = hd
		start := depth
		var words []string
		block := false
		for _, t := range toks {
			if !t.quoted && t.s == "{" {
				depth++
				block = true
				continue
			}
			if !t.quoted && t.s == "}" {
				depth--
				continue
			}
			words = append(words, t.s)
		}
		if len(words) > 0 && start > 0 {
			out = append(out, Directive{Depth: start, Words: words, Line: i, Block: block})
		}
	}
	return out
}

// heredocEnd sagt, ob die Zeile ein Heredoc mit marker beendet; rest sind
// die Wörter danach (etwa der Status bei respond).
func heredocEnd(line, marker string) (rest string, end bool) {
	t := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(t, marker) {
		return "", false
	}
	r := t[len(marker):]
	if r != "" && r[0] != ' ' && r[0] != '\t' && r[0] != '\r' {
		return "", false
	}
	return r, true
}
