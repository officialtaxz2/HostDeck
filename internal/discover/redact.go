package discover

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Befehle aus Cronjobs und Diensten landen im Bericht. Darin stehen manchmal
// Passwörter oder Tokens. redact ersetzt alles, was danach aussieht, durch
// „…“. Lieber zu viel als zu wenig: Der Bericht soll zeigen, was läuft, nicht
// womit.
var (
	// KEY=wert, wenn der Name nach einem Geheimnis klingt
	redactAssign = regexp.MustCompile(`(?i)\b([A-Z0-9_.-]*(?:pass|pwd|secret|token|key|auth|cred|private)[A-Z0-9_.-]*)=("[^"]*"|'[^']*'|\S+)`)
	// --password wert, --token=wert
	redactFlag = regexp.MustCompile(`(?i)(--?[a-z0-9-]*(?:pass|pwd|secret|token|key|auth|cred)[a-z0-9-]*)([ =])("[^"]*"|'[^']*'|\S+)`)
	// curl -u benutzer:passwort
	redactUser = regexp.MustCompile(`(\s(?:-u|--user)[ =])\S+:\S+`)
	// -pGeheim (mysql), direkt angehängt
	redactShortP = regexp.MustCompile(`(\s)-p[^\s-]\S*`)
	// Authorization-Header und ähnliche
	redactHeader = regexp.MustCompile(`(?i)((?:authorization|x-api-key|api-key|private-token|cookie)\s*:\s*)("[^"]*"|'[^']*'|[^'"]+?)(["']|\s-|$)`)
	// Benutzer:Passwort@ in URLs
	redactUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`)
	// lange Zufallsfolgen, etwa Tokens ohne Namen
	redactRandom = regexp.MustCompile(`[A-Za-z0-9+_=-]{32,}`)
)

const maxCommand = 160

func redact(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	cmd = redactUserinfo.ReplaceAllString(cmd, "${1}…@")
	cmd = redactHeader.ReplaceAllString(cmd, "${1}…${3}")
	cmd = redactAssign.ReplaceAllString(cmd, "${1}=…")
	cmd = redactFlag.ReplaceAllString(cmd, "${1}${2}…")
	cmd = redactUser.ReplaceAllString(cmd, "${1}…")
	cmd = redactShortP.ReplaceAllString(cmd, "${1}-p…")
	cmd = redactRandom.ReplaceAllStringFunc(cmd, func(m string) string {
		// Lange Namen ohne Ziffern, etwa Projektordner, bleiben stehen.
		if !mixed(m) {
			return m
		}
		return "…"
	})
	if utf8.RuneCountInString(cmd) > maxCommand {
		r := []rune(cmd)
		cmd = string(r[:maxCommand-1]) + "…"
	}
	return cmd
}

// mixed meldet, ob s Ziffern und Buchstaben enthält, wie Tokens und Hashes.
func mixed(s string) bool {
	digit, letter := false, false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			letter = true
		}
	}
	return digit && letter
}
