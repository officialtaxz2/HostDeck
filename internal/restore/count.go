package restore

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Counts sind Zeilenzahlen je Tabelle, Schlüssel „Datenbank/Tabelle“.
type Counts map[string]int64

// Rows ist die Summe aller Zeilen.
func (c Counts) Rows() int64 {
	var n int64
	for _, v := range c {
		n += v
	}
	return n
}

// Diff vergleicht mit want und liefert lesbare Abweichungen. Tabellen,
// die auf beiden Seiten leer sind oder fehlen, sind gleich.
func (c Counts) Diff(want Counts) []string {
	keys := map[string]bool{}
	for k := range c {
		keys[k] = true
	}
	for k := range want {
		keys[k] = true
	}
	var out []string
	for k := range keys {
		got, ok1 := c[k]
		exp, ok2 := want[k]
		switch {
		case got == exp && (ok1 || exp == 0) && (ok2 || got == 0):
		case !ok1 && exp > 0:
			out = append(out, fmt.Sprintf("%s fehlt (im Backup %d Zeilen)", k, exp))
		default:
			out = append(out, fmt.Sprintf("%s: %d statt %d Zeilen", k, got, exp))
		}
	}
	sort.Strings(out)
	return out
}

// ParseCounts liest die Ausgabe der Zählskripte: „db|tabelle|anzahl“.
func ParseCounts(b []byte) (Counts, error) {
	c := Counts{}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		f := strings.Split(l, "|")
		if len(f) != 3 {
			return nil, fmt.Errorf("unerwartete Zeile beim Zählen: %q", l)
		}
		n, err := strconv.ParseInt(strings.TrimSpace(f[2]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unerwartete Zahl beim Zählen: %q", l)
		}
		c[f[0]+"/"+f[1]] = n
	}
	return c, nil
}

// DumpCounter zählt beim Durchlesen eines Dumps die Zeilen je Tabelle.
type DumpCounter interface {
	io.Writer
	Counts() Counts
}

// NewDumpCounter liefert einen Zähler für die Dumps von hostdeck backup, nil
// für Datenbanken, deren Dumps HostDeck nicht zählen kann.
func NewDumpCounter(engine string) DumpCounter {
	switch engine {
	case "postgres":
		return &pgCounter{counts: Counts{}}
	case "mysql", "mariadb":
		return &mysqlCounter{counts: Counts{}}
	}
	return nil
}

// maxHead ist so viel vom Zeilenanfang, wie zum Erkennen eines Befehls
// nötig ist. Der Rest langer Zeilen wird nur gezählt, nicht gepuffert.
const maxHead = 4096

// pgCounter versteht die Ausgabe von pg_dumpall: \connect wechselt die
// Datenbank, „COPY tabelle (…) FROM stdin;“ leitet Datenzeilen ein, die
// mit „\.“ enden.
type pgCounter struct {
	counts Counts
	db     string
	table  string // in einem COPY-Block
	head   []byte
	long   bool // Zeile ist länger als maxHead
}

func (p *pgCounter) Write(b []byte) (int, error) {
	n := len(b)
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		part := b
		if i >= 0 {
			part = b[:i]
		}
		if room := maxHead - len(p.head); room > 0 {
			if len(part) > room {
				p.head = append(p.head, part[:room]...)
				p.long = true
			} else {
				p.head = append(p.head, part...)
			}
		} else if len(part) > 0 {
			p.long = true
		}
		if i < 0 {
			break
		}
		p.line()
		b = b[i+1:]
	}
	return n, nil
}

func (p *pgCounter) line() {
	l := string(p.head)
	p.head, p.long = p.head[:0], false
	if p.table != "" {
		if l == `\.` {
			p.table = ""
			return
		}
		if p.db != "template0" && p.db != "template1" {
			p.counts[p.db+"/"+p.table]++
		}
		return
	}
	switch {
	case strings.HasPrefix(l, `\connect `):
		p.db = pgConnectDB(strings.TrimPrefix(l, `\connect `))
	case strings.HasPrefix(l, "COPY ") && strings.HasSuffix(l, " FROM stdin;"):
		t := strings.TrimPrefix(l, "COPY ")
		if i := strings.Index(t, " ("); i >= 0 {
			t = t[:i]
		} else {
			t = strings.TrimSuffix(t, " FROM stdin;")
		}
		p.table = strings.ReplaceAll(t, `"`, "")
		if p.db != "template0" && p.db != "template1" {
			if _, ok := p.counts[p.db+"/"+p.table]; !ok {
				p.counts[p.db+"/"+p.table] = 0
			}
		}
	}
}

// pgConnectDB liest den Datenbanknamen aus „\connect name“ oder
// „\connect -reuse-previous=on "dbname='name'"“.
func pgConnectDB(arg string) string {
	if i := strings.Index(arg, "dbname='"); i >= 0 {
		rest := arg[i+len("dbname='"):]
		var b strings.Builder
		for j := 0; j < len(rest); j++ {
			if rest[j] == '\'' {
				if j+1 < len(rest) && rest[j+1] == '\'' {
					b.WriteByte('\'')
					j++
					continue
				}
				break
			}
			if rest[j] == '\\' && j+1 < len(rest) {
				j++
			}
			b.WriteByte(rest[j])
		}
		return b.String()
	}
	f := strings.Fields(arg)
	if len(f) == 0 {
		return ""
	}
	return strings.Trim(f[len(f)-1], `"`)
}

func (p *pgCounter) Counts() Counts { return p.counts }

// mysqlCounter versteht die Ausgabe von mysqldump mit erweiterten INSERTs:
// „USE `db`;“ wechselt die Datenbank, jede Klammer auf oberster Ebene nach
// „INSERT INTO `t` VALUES “ ist eine Zeile.
type mysqlCounter struct {
	counts Counts
	db     string
	head   []byte
	// Im VALUES-Teil eines INSERT:
	table  string
	depth  int
	quote  byte // ' oder ", 0 außerhalb
	escape bool
}

var mysqlSystemDBs = map[string]bool{"mysql": true, "sys": true, "performance_schema": true, "information_schema": true}

func (m *mysqlCounter) Write(b []byte) (int, error) {
	for _, c := range b {
		if m.table != "" {
			m.values(c)
			continue
		}
		if c == '\n' {
			m.lineDone()
			continue
		}
		if len(m.head) < maxHead {
			m.head = append(m.head, c)
		}
		if c == ' ' && bytes.HasPrefix(m.head, []byte("INSERT INTO `")) && bytes.HasSuffix(m.head, []byte(" VALUES ")) {
			t := string(m.head[len("INSERT INTO `") : len(m.head)-len(" VALUES ")])
			if i := strings.LastIndexByte(t, '`'); i >= 0 {
				m.table = strings.ReplaceAll(t[:i], "``", "`")
				m.depth, m.quote, m.escape = 0, 0, false
				m.head = m.head[:0]
			}
		}
	}
	return len(b), nil
}

func (m *mysqlCounter) values(c byte) {
	switch {
	case m.escape:
		m.escape = false
	case m.quote != 0:
		switch c {
		case '\\':
			m.escape = true
		case m.quote:
			m.quote = 0
		}
	case c == '\'' || c == '"':
		m.quote = c
	case c == '(':
		if m.depth == 0 && !mysqlSystemDBs[m.db] {
			m.counts[m.db+"/"+m.table]++
		}
		m.depth++
	case c == ')':
		m.depth--
	case c == ';' && m.depth == 0:
		m.table = ""
	case c == '\n' && m.depth == 0:
		m.table = ""
	}
}

func (m *mysqlCounter) lineDone() {
	l := string(m.head)
	m.head = m.head[:0]
	if strings.HasPrefix(l, "USE `") && strings.HasSuffix(l, "`;") {
		m.db = strings.ReplaceAll(l[len("USE `"):len(l)-2], "``", "`")
	}
}

func (m *mysqlCounter) Counts() Counts { return m.counts }
