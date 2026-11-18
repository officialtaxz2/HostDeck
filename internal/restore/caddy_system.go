package restore

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
)

// Offizielle Paketquelle von Caddy (https://caddyserver.com/docs/install).
const (
	CaddyKeyURL = "https://dl.cloudsmith.io/public/caddy/stable/gpg.key"
	// CaddyKeyFingerprint ist der Schlüssel, mit dem die Pakete von Caddy
	// signiert sind („Caddy Web Server <contact@caddyserver.com>“). Ein
	// anderer Schlüssel unter der Adresse wird abgelehnt.
	CaddyKeyFingerprint = "65760C51EDEA2017CEA2CA15155B6D79CA56EA34"
	CaddyKeyring        = "/usr/share/keyrings/caddy-stable-archive-keyring.gpg"
	CaddySourceList     = "/etc/apt/sources.list.d/caddy-stable.list"
	caddySource         = "deb [signed-by=" + CaddyKeyring + "] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main\n"
)

// SystemCaddy ist Caddy als systemd-Dienst aus dem Paket (Debian, Ubuntu).
type SystemCaddy struct {
	Run  caddy.Runner // nil: caddy.Exec
	HTTP *http.Client // nil: eigener mit Zeitlimit
	// Root ist für Tests; leer heißt /.
	Root string
}

func (s *SystemCaddy) run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	if s.Run != nil {
		return s.Run(ctx, env, name, args...)
	}
	return caddy.Exec(ctx, env, name, args...)
}

func (s *SystemCaddy) path(p string) string { return s.Root + p }

func (s *SystemCaddy) Present(ctx context.Context) bool {
	out, err := s.run(ctx, nil, "systemctl", "show", caddy.Unit, "--property=LoadState")
	return err == nil && strings.TrimSpace(string(out)) == "LoadState=loaded"
}

func (s *SystemCaddy) Install(ctx context.Context) error {
	if !debianLike(s.path("/etc/os-release")) {
		return errors.New("HostDeck installiert Caddy nur auf Debian und Ubuntu selbst; bitte Caddy installieren (caddyserver.com/docs/install) und noch einmal zurückspielen")
	}
	// Ein halb installiertes Paket hilft niemandem: Abbrechen wartet, bis
	// apt fertig ist.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()
	key, err := s.fetchKey(ctx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path(CaddyKeyring), key, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(s.path(CaddySourceList), []byte(caddySource), 0o644); err != nil {
		return err
	}
	env := []string{"DEBIAN_FRONTEND=noninteractive"}
	if out, err := s.run(ctx, env, "apt-get", "update"); err != nil {
		return fmt.Errorf("apt-get update: %s", lastLines(out, err))
	}
	if out, err := s.run(ctx, env, "apt-get", "install", "-y", "-o", "Dpkg::Options::=--force-confold", "caddy"); err != nil {
		return fmt.Errorf("apt-get install caddy: %s", lastLines(out, err))
	}
	return nil
}

// fetchKey lädt den Schlüssel der Paketquelle und prüft seinen
// Fingerabdruck.
func (s *SystemCaddy) fetchKey(ctx context.Context) ([]byte, error) {
	cl := s.HTTP
	if cl == nil {
		cl = &http.Client{Timeout: time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CaddyKeyURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Schlüssel von Caddy laden: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Schlüssel von Caddy laden: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, fmt.Errorf("Schlüssel von Caddy laden: %v", err)
	}
	return CheckKey(b, CaddyKeyFingerprint)
}

// CheckKey liest einen OpenPGP-Schlüssel (mit oder ohne ASCII-Hülle) und
// prüft den Fingerabdruck des Hauptschlüssels. Zurück kommt er ohne
// Hülle, so wie apt ihn in signed-by liest.
func CheckKey(b []byte, fingerprint string) ([]byte, error) {
	bin := b
	if bytes.Contains(b, []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----")) {
		var err error
		if bin, err = dearmor(b); err != nil {
			return nil, fmt.Errorf("Schlüssel von Caddy lesen: %v", err)
		}
	}
	fp, err := primaryFingerprint(bin)
	if err != nil {
		return nil, fmt.Errorf("Schlüssel von Caddy lesen: %v", err)
	}
	if !strings.EqualFold(fp, fingerprint) {
		return nil, fmt.Errorf("Schlüssel von Caddy hat den Fingerabdruck %s statt %s; HostDeck installiert nichts", fp, fingerprint)
	}
	key, err := firstKey(bin)
	if err != nil {
		return nil, fmt.Errorf("Schlüssel von Caddy lesen: %v", err)
	}
	return key, nil
}

func dearmor(b []byte) ([]byte, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	var body strings.Builder
	state := 0 // 0 vor BEGIN, 1 Kopfzeilen, 2 Inhalt
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		switch {
		case state == 0 && l == "-----BEGIN PGP PUBLIC KEY BLOCK-----":
			state = 1
		case state == 1 && l == "":
			state = 2
		case state == 1 && !strings.Contains(l, ":"):
			state = 2
			body.WriteString(l)
		case state == 2 && strings.HasPrefix(l, "-----END"):
			return base64.StdEncoding.DecodeString(body.String())
		case state == 2 && strings.HasPrefix(l, "="):
			// Prüfsumme; der Fingerabdruck sichert ohnehin alles ab.
		case state == 2:
			body.WriteString(l)
		}
	}
	return nil, errors.New("keine vollständige ASCII-Hülle")
}

// packet liest den Kopf des OpenPGP-Pakets am Anfang von b: Typ, Beginn
// und Länge des Inhalts.
func packet(b []byte) (tag, off, n int, err error) {
	if len(b) < 2 || b[0]&0x80 == 0 {
		return 0, 0, 0, errors.New("kein OpenPGP-Paket")
	}
	if b[0]&0x40 != 0 { // neues Format
		tag = int(b[0] & 0x3f)
		switch o := int(b[1]); {
		case o < 192:
			n, off = o, 2
		case o < 224 && len(b) > 2:
			n, off = (o-192)<<8+int(b[2])+192, 3
		case o == 255 && len(b) > 5:
			n, off = int(b[2])<<24|int(b[3])<<16|int(b[4])<<8|int(b[5]), 6
		default:
			return 0, 0, 0, errors.New("Paketlänge nicht lesbar")
		}
	} else {
		tag = int(b[0]>>2) & 0x0f
		switch b[0] & 3 {
		case 0:
			n, off = int(b[1]), 2
		case 1:
			if len(b) < 3 {
				return 0, 0, 0, errors.New("zu kurz")
			}
			n, off = int(b[1])<<8|int(b[2]), 3
		case 2:
			if len(b) < 5 {
				return 0, 0, 0, errors.New("zu kurz")
			}
			n, off = int(b[1])<<24|int(b[2])<<16|int(b[3])<<8|int(b[4]), 5
		default:
			return 0, 0, 0, errors.New("Paketlänge nicht lesbar")
		}
	}
	if n < 0 || off+n > len(b) {
		return 0, 0, 0, errors.New("Paket abgeschnitten")
	}
	return tag, off, n, nil
}

// firstKey ist der erste übertragbare Schlüssel in b: das erste Paket
// (ein Hauptschlüssel) samt allem bis vor den nächsten Hauptschlüssel.
// Was danach kommt, prüft der Fingerabdruck nicht; es darf nicht mit in den
// Schlüsselbund von apt.
func firstKey(b []byte) ([]byte, error) {
	for i := 0; i < len(b); {
		tag, off, n, err := packet(b[i:])
		if err != nil {
			return nil, err
		}
		if i > 0 && tag == 6 {
			return b[:i], nil
		}
		i += off + n
	}
	return b, nil
}

// primaryFingerprint ist der V4-Fingerabdruck des ersten Pakets (RFC 4880,
// 12.2): SHA-1 über 0x99, Länge und Inhalt des Public-Key-Pakets.
func primaryFingerprint(b []byte) (string, error) {
	tag, off, n, err := packet(b)
	if err != nil {
		return "", err
	}
	if tag != 6 {
		return "", fmt.Errorf("erstes Paket ist kein öffentlicher Schlüssel (Typ %d)", tag)
	}
	if n <= 0 || n > 0xffff {
		return "", errors.New("Paket abgeschnitten")
	}
	body := b[off : off+n]
	if body[0] != 4 {
		return "", fmt.Errorf("Schlüssel der Version %d wird nicht unterstützt", body[0])
	}
	h := sha1.New()
	h.Write([]byte{0x99, byte(n >> 8), byte(n)})
	h.Write(body)
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}

func debianLike(osRelease string) bool {
	b, err := os.ReadFile(osRelease)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || (k != "ID" && k != "ID_LIKE") {
			continue
		}
		for _, id := range strings.Fields(strings.Trim(v, `"'`)) {
			if id == "debian" || id == "ubuntu" {
				return true
			}
		}
	}
	return false
}

func (s *SystemCaddy) Stop(ctx context.Context) error {
	if out, err := s.run(ctx, nil, "systemctl", "stop", caddy.Unit); err != nil {
		return errors.New(lastLines(out, err))
	}
	return nil
}

func (s *SystemCaddy) Validate(ctx context.Context, config string) error {
	bin, err := exec.LookPath("caddy")
	if err != nil {
		bin = "/usr/bin/caddy"
	}
	// caddy validate legt Logdateien an, die es noch nicht gibt, und zwar
	// als root. Caddy selbst könnte dann nicht hineinschreiben; die neuen
	// bekommen deshalb danach den Benutzer caddy.
	logDir := s.path("/var/log/caddy")
	before := map[string]bool{}
	if es, err := os.ReadDir(logDir); err == nil {
		for _, e := range es {
			before[e.Name()] = true
		}
	}
	c := &caddy.Caddy{Bin: bin, Config: config, Run: s.Run}
	if c.Run == nil {
		c.Run = caddy.Exec
	}
	verr := c.ValidateFile(ctx, config)
	if es, err := os.ReadDir(logDir); err == nil {
		var fresh []string
		for _, e := range es {
			if !before[e.Name()] {
				fresh = append(fresh, logDir+"/"+e.Name())
			}
		}
		if len(fresh) > 0 {
			if uid, gid, err := lookupCaddy(); err == nil {
				for _, f := range fresh {
					_ = os.Lchown(f, uid, gid)
				}
			}
		}
	}
	return verr
}

func (s *SystemCaddy) Start(ctx context.Context) (bool, string) {
	if out, err := s.run(ctx, nil, "systemctl", "enable", caddy.Unit); err != nil {
		return false, "systemctl enable: " + lastLines(out, err)
	}
	if out, err := s.run(ctx, nil, "systemctl", "restart", caddy.Unit); err != nil {
		return false, s.why(ctx, lastLines(out, err))
	}
	deadline := time.Now().Add(caddyWait)
	for {
		out, _ := s.run(ctx, nil, "systemctl", "is-active", caddy.Unit)
		state := strings.TrimSpace(string(out))
		if state == "active" {
			return true, ""
		}
		if state == "failed" || time.Now().After(deadline) || ctx.Err() != nil {
			return false, s.why(ctx, "Zustand "+state)
		}
		sleepCtx(ctx, time.Second)
	}
}

// why holt die letzte Meldung von Caddy aus dem Journal.
func (s *SystemCaddy) why(ctx context.Context, fallback string) string {
	out, err := s.run(ctx, nil, "journalctl", "--unit", caddy.Unit, "--lines", "20", "--no-pager", "--output", "cat")
	if err != nil {
		return fallback
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		var j map[string]any
		if json.Unmarshal([]byte(l), &j) == nil {
			if m, ok := j["error"].(string); ok && m != "" {
				return m
			}
			if lv, _ := j["level"].(string); lv == "error" || lv == "fatal" {
				if m, ok := j["msg"].(string); ok && m != "" {
					return m
				}
			}
			continue
		}
		if strings.Contains(strings.ToLower(l), "error") {
			return l
		}
	}
	return fallback
}

func (s *SystemCaddy) Disable(ctx context.Context) error {
	if !s.Present(ctx) {
		return nil
	}
	if out, err := s.run(ctx, nil, "systemctl", "disable", "--now", caddy.Unit); err != nil {
		return errors.New(lastLines(out, err))
	}
	return nil
}

func (s *SystemCaddy) Remove(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()
	env := []string{"DEBIAN_FRONTEND=noninteractive"}
	if out, err := s.run(ctx, env, "apt-get", "purge", "-y", "caddy"); err != nil {
		return fmt.Errorf("apt-get purge caddy: %s", lastLines(out, err))
	}
	// Die Quelle nur, wenn sie genau die ist, die HostDeck geschrieben hat.
	if b, err := os.ReadFile(s.path(CaddySourceList)); err == nil && string(b) == caddySource {
		if err := os.Remove(s.path(CaddySourceList)); err != nil {
			return err
		}
		if err := os.Remove(s.path(CaddyKeyring)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func lastLines(out []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var keep []string
	for i := len(lines) - 1; i >= 0 && len(keep) < 2; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			keep = append([]string{l}, keep...)
		}
	}
	if len(keep) == 0 {
		return err.Error()
	}
	s := strings.Join(keep, " / ")
	if len(s) > 400 {
		s = s[:400] + " …"
	}
	return s
}
