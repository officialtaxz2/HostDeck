package aktualisieren

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Git als root in einem fremden Ordner ist gefährlich: Die .git/config des
// Ordners kann Programme starten (core.fsmonitor beim status, filter.* beim
// pull, credential.helper, include …). Wer in den Projektordner schreiben
// darf, etwa ein Container mit „.:/app“, würde so root. Darum:
//
//  1. HostDeck liest die lokale Konfiguration selbst, ohne Git, und lässt nur
//     eine feste Liste harmloser Schlüssel zu; sonst kein Git-Weg.
//  2. Auf der Befehlszeile hält HostDeck die gefährlichen Schalter fest (die
//     Befehlszeile gewinnt gegen die Konfiguration des Ordners).
//  3. safe.directory gilt nur für genau diesen Ordner.
//  4. Gehört der Ordner nicht root, läuft Git als dessen Besitzer.

// GitRepo ist ein geprüfter Git-Ordner.
type GitRepo struct {
	Top   string // Arbeitsordner (mit .git darin)
	Owner *Owner // nil: gehört root, Git läuft wie HostDeck
}

// Owner ist der Besitzer eines Ordners, unter dem Git dann läuft.
type Owner struct {
	UID, GID uint32
	Home     string
}

// GitRefused: Der Ordner ist ein Git-Ordner, aber HostDeck ruft dort kein Git
// auf. Der Grund steht im Text.
type GitRefused struct {
	Dir, Why string
}

func (e *GitRefused) Error() string {
	return "Git-Ordner " + e.Dir + ": " + e.Why + "; HostDeck ruft dort kein Git auf (Sicherheit: die Einstellungen könnten Programme als root starten)"
}

// gitAllowed sind die Schlüssel, die in .git/config stehen dürfen. Der
// Abschnitt (klein geschrieben) führt zu den erlaubten Namen; sub sagt, ob
// der Abschnitt einen Unterabschnitt hat ([remote "origin"]).
var gitAllowed = map[string]struct {
	sub  bool
	keys []string
}{
	"core":       {false, []string{"repositoryformatversion", "filemode", "bare", "logallrefupdates", "ignorecase", "precomposeunicode", "symlinks", "autocrlf", "eol"}},
	"remote":     {true, []string{"url", "fetch", "pushurl", "tagopt", "prune"}},
	"branch":     {true, []string{"remote", "merge", "rebase", "pushremote", "description"}},
	"user":       {false, []string{"name", "email"}},
	"pull":       {false, []string{"ff", "rebase"}},
	"fetch":      {false, []string{"prune"}},
	"init":       {false, []string{"defaultbranch"}},
	"extensions": {false, []string{"objectformat", "refstorage"}},
	"submodule":  {true, []string{"url", "active"}},
	"lfs":        {false, []string{"repositoryformatversion"}},
	"gc":         {false, []string{"auto"}},
}

// gitFlags hält die gefährlichen Schalter fest, ganz gleich, was im Ordner
// steht. safe.directory kommt je Ordner dazu.
var gitFlags = []string{
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.fsmonitor=false",
	"-c", "credential.helper=",
	"-c", "core.askPass=",
	"-c", "protocol.allow=never",
	"-c", "protocol.https.allow=always",
	"-c", "protocol.ssh.allow=always",
	"-c", "protocol.git.allow=always",
}

// CheckGitDir sucht den Git-Ordner, in dem dir liegt, und prüft ihn.
// errNoGit: kein Git-Ordner; *GitRefused: Git-Ordner, aber nicht sicher.
func CheckGitDir(dir string) (GitRepo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return GitRepo{}, errNoGit
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	for d := abs; ; {
		g := filepath.Join(d, ".git")
		fi, err := os.Lstat(g)
		if err == nil {
			if !fi.IsDir() {
				return GitRepo{}, &GitRefused{d, ".git ist kein Ordner (Worktree, Submodul oder Verweis)"}
			}
			return checkGitMeta(d, g, fi)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return GitRepo{}, errNoGit
		}
		d = parent
	}
}

func checkGitMeta(top, git string, fi os.FileInfo) (GitRepo, error) {
	for _, extra := range []string{"config.worktree", "commondir"} {
		if _, err := os.Lstat(filepath.Join(git, extra)); err == nil {
			return GitRepo{}, &GitRefused{top, ".git/" + extra + " vorhanden"}
		}
	}
	cfg := filepath.Join(git, "config")
	cfi, err := os.Lstat(cfg)
	switch {
	case err != nil:
		return GitRepo{}, &GitRefused{top, ".git/config fehlt"}
	case !cfi.Mode().IsRegular():
		return GitRepo{}, &GitRefused{top, ".git/config ist keine gewöhnliche Datei"}
	case cfi.Size() > 1<<20:
		return GitRepo{}, &GitRefused{top, ".git/config ist ungewöhnlich groß"}
	}
	f, err := os.Open(cfg)
	if err != nil {
		return GitRepo{}, &GitRefused{top, ".git/config nicht lesbar"}
	}
	defer f.Close()
	if key, err := checkGitConfig(bufio.NewScanner(f)); err != nil {
		return GitRepo{}, &GitRefused{top, err.Error()}
	} else if key != "" {
		return GitRepo{}, &GitRefused{top, "in .git/config steht „" + key + "“"}
	}
	return GitRepo{Top: top, Owner: ownerOf(fi)}, nil
}

// checkGitConfig liest eine Git-Konfiguration und gibt den ersten nicht
// erlaubten Schlüssel zurück ("" wenn alles passt). Was HostDeck nicht sicher
// lesen kann, ist ein Fehler.
func checkGitConfig(sc *bufio.Scanner) (string, error) {
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	section, sub, has := "", "", false
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if strings.HasSuffix(line, `\`) {
			return "", fmt.Errorf(".git/config Zeile %d geht in der nächsten weiter", n)
		}
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			var rest string
			var err error
			section, sub, rest, err = gitSection(line)
			if err != nil {
				return "", fmt.Errorf(".git/config Zeile %d: %v", n, err)
			}
			has = true
			allowed, ok := gitAllowed[section]
			if !ok || allowed.sub != (sub != "") {
				return gitName(section, sub, ""), nil
			}
			line = rest
			if line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
		}
		if !has {
			return "", fmt.Errorf(".git/config Zeile %d steht vor dem ersten Abschnitt", n)
		}
		key := gitKey(line)
		if key == "" {
			return "", fmt.Errorf(".git/config Zeile %d ist nicht lesbar", n)
		}
		ok := false
		for _, k := range gitAllowed[section].keys {
			if k == key {
				ok = true
				break
			}
		}
		if !ok {
			return gitName(section, sub, key), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", errors.New(".git/config nicht lesbar")
	}
	return "", nil
}

func gitName(section, sub, key string) string {
	s := section
	if sub != "" {
		s += "." + sub
	}
	if key != "" {
		s += "." + key
	}
	return s
}

// gitSection liest „[abschnitt]“, „[abschnitt "unter"]“ oder
// „[abschnitt.unter]“; rest ist, was danach in der Zeile steht.
func gitSection(line string) (section, sub, rest string, err error) {
	i := 1
	for i < len(line) && (isAlnum(line[i]) || line[i] == '-' || line[i] == '.') {
		i++
	}
	name := line[1:i]
	if name == "" {
		return "", "", "", errors.New("Abschnitt ohne Namen")
	}
	if a, b, ok := strings.Cut(name, "."); ok {
		name, sub = a, b
		if sub == "" {
			return "", "", "", errors.New("leerer Unterabschnitt")
		}
	}
	if i < len(line) && line[i] == ' ' {
		if sub != "" {
			return "", "", "", errors.New("Abschnitt doppelt unterteilt")
		}
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) || line[i] != '"' {
			return "", "", "", errors.New("Unterabschnitt ohne Anführungszeichen")
		}
		i++
		var b strings.Builder
		for {
			if i >= len(line) {
				return "", "", "", errors.New("Unterabschnitt nicht geschlossen")
			}
			c := line[i]
			if c == '"' {
				i++
				break
			}
			if c == '\\' && i+1 < len(line) {
				i++
				c = line[i]
			}
			b.WriteByte(c)
			i++
		}
		sub = b.String()
		if sub == "" {
			return "", "", "", errors.New("leerer Unterabschnitt")
		}
	}
	if i >= len(line) || line[i] != ']' {
		return "", "", "", errors.New("Abschnitt nicht geschlossen")
	}
	return strings.ToLower(name), sub, strings.TrimSpace(line[i+1:]), nil
}

// gitKey liest den Namen eines Eintrags (klein geschrieben); "" heißt
// nicht lesbar.
func gitKey(line string) string {
	i := 0
	for i < len(line) && (isAlnum(line[i]) || line[i] == '-') {
		i++
	}
	if i == 0 || !isLetter(line[0]) {
		return ""
	}
	rest := strings.TrimLeft(line[i:], " \t")
	if rest != "" && rest[0] != '=' && rest[0] != '#' && rest[0] != ';' {
		return ""
	}
	return strings.ToLower(line[:i])
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isAlnum(c byte) bool  { return isLetter(c) || c >= '0' && c <= '9' }

// gitEnv entfernt GIT_* aus der Umgebung (etwa GIT_DIR) und schaltet die
// systemweite Konfiguration ab.
func gitEnv(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "GIT_") {
			out = append(out, e)
		}
	}
	return append(out, "GIT_CONFIG_NOSYSTEM=1")
}
