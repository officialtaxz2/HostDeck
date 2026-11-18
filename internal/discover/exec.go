package discover

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// ExecGit liest den Git-Stand über das git-Programm. Es arbeitet auf echten
// Host-Pfaden und wird deshalb nur mit hostfs-Root „/“ verwendet.
type ExecGit struct{}

// ErrGitNotInstalled: git fehlt auf dem Server.
var ErrGitNotInstalled = errors.New("git ist nicht installiert")

func (ExecGit) Inspect(ctx context.Context, dir string) (*inventory.Git, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, ErrGitNotInstalled
	}
	// git läuft als Eigentümer des Ordners, nicht als root. Eine Repo-
	// Konfiguration kann Befehle auslösen (etwa core.fsmonitor oder
	// Filter); so laufen sie höchstens mit den Rechten dieses Benutzers,
	// genau wie wenn er selbst git aufruft.
	attr := runAsOwner(dir)
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// --no-optional-locks: git status schreibt den Index nicht neu.
		full := append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-C", dir}, args...)
		cmd := exec.CommandContext(ctx, "git", full...)
		cmd.SysProcAttr = attr
		cmd.Dir = "/"
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=/nonexistent",
			"XDG_CONFIG_HOME=/nonexistent",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_OPTIONAL_LOCKS=0",
			"GIT_TERMINAL_PROMPT=0",
			"LC_ALL=C",
		}
		out, err := cmd.Output()
		return string(out), err
	}
	root, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := strings.TrimSpace(string(ee.Stderr))
			if strings.Contains(msg, "not a git repository") {
				return nil, nil
			}
			if i := strings.LastIndex(msg, "\n"); i >= 0 {
				msg = msg[i+1:]
			}
			return nil, errors.New(msg)
		}
		return nil, err
	}
	g := &inventory.Git{Root: strings.TrimSpace(root)}
	if out, err := run("rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		g.Branch = strings.TrimSpace(out)
	}
	if out, err := run("rev-parse", "HEAD"); err == nil {
		g.Commit = strings.TrimSpace(out)
	}
	if out, err := run("config", "--get", "remote.origin.url"); err == nil {
		g.Remote = StripCredentials(strings.TrimSpace(out))
	}
	if out, err := run("status", "--porcelain"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) != "" {
				g.Dirty++
			}
		}
	}
	return g, nil
}

// StripCredentials entfernt Benutzername und Token aus einer Remote-URL wie
// https://user:token@github.com/x/y.git. SSH-Adressen wie git@github.com:x/y
// bleiben unverändert, der Benutzer „git“ ist kein Geheimnis.
func StripCredentials(remote string) string {
	u, err := url.Parse(remote)
	if err != nil || u.Scheme == "" || u.User == nil {
		return remote
	}
	if u.Scheme == "ssh" && u.User.Username() != "" {
		if _, hasPw := u.User.Password(); !hasPw {
			return remote
		}
	}
	u.User = nil
	return u.String()
}

// ExecCaddy ruft „caddy adapt“ auf. Der Befehl übersetzt nur die
// Konfiguration in JSON; er lädt nichts neu und startet nichts.
type ExecCaddy struct{}

func (ExecCaddy) Adapt(ctx context.Context, configPath string) ([]byte, error) {
	bin, err := exec.LookPath("caddy")
	if err != nil {
		return nil, errors.New("caddy ist nicht im PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "adapt", "--config", configPath, "--adapter", "caddyfile")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("%v: %s", err, msg)
	}
	return stdout.Bytes(), nil
}
