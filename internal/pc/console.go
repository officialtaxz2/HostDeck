package pc

import (
	"bufio"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

// StdConsole liest von der Konsole. Ein Programm sollte nur eine davon
// benutzen, sonst verschluckt der Puffer der einen Eingaben der anderen.
type StdConsole struct {
	once  sync.Once
	mu    sync.Mutex
	lines chan string
	br    *bufio.Reader
}

func (c *StdConsole) reader() *bufio.Reader {
	if c.br == nil {
		c.br = bufio.NewReader(os.Stdin)
	}
	return c.br
}

// Lines startet das dauerhafte Lesen von Zeilen.
func (c *StdConsole) Lines() <-chan string {
	c.once.Do(func() {
		ch := make(chan string)
		br := c.reader()
		c.mu.Lock()
		c.lines = ch
		c.mu.Unlock()
		go func() {
			defer close(ch)
			for {
				l, err := br.ReadString('\n')
				if l != "" || err == nil {
					ch <- strings.TrimRight(l, "\r\n")
				}
				if err != nil {
					return
				}
			}
		}()
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lines
}

func (c *StdConsole) started() <-chan string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lines
}

// Secret liest eine Eingabe, ohne sie anzuzeigen.
func (c *StdConsole) Secret() (string, error) {
	fd := int(os.Stdin.Fd())
	if c.started() == nil && term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		return strings.TrimSpace(string(b)), err
	}
	return c.ReadLine()
}

// ReadLine liest eine Zeile. Läuft Lines schon, kommt sie von dort.
func (c *StdConsole) ReadLine() (string, error) {
	if ch := c.started(); ch != nil {
		l, ok := <-ch
		if !ok {
			return "", io.EOF
		}
		return strings.TrimSpace(l), nil
	}
	l, err := c.reader().ReadString('\n')
	if err != nil && l == "" {
		return "", err
	}
	return strings.TrimSpace(l), nil
}
