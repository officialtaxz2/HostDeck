package aktualisieren

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// GitState ist der Stand eines Git-Ordners, aus dem Compose baut.
type GitState struct {
	Dir        string    `json:"ordner"`
	Branch     string    `json:"zweig,omitempty"`
	Remote     string    `json:"gegenstelle,omitempty"` // etwa origin
	Head       string    `json:"stand"`
	HeadTime   time.Time `json:"stand_zeit"`
	RemoteHead string    `json:"stand_gegenstelle,omitempty"`
	Dirty      bool      `json:"geaendert,omitempty"` // nicht committete Änderungen
	merge      string    // etwa refs/heads/main
}

// git ruft git in dir auf, nur in einem geprüften Git-Ordner und mit
// festgehaltenen Schaltern (siehe gitschutz.go).
func (u *Updater) git(ctx context.Context, dir string, out io.Writer, args ...string) (string, error) {
	check := u.GitCheck
	if check == nil {
		check = CheckGitDir
	}
	repo, err := check(dir)
	if err != nil {
		return "", err
	}
	full := append([]string{"-c", "safe.directory=" + repo.Top}, gitFlags...)
	full = append(append(full, "-C", dir), args...)
	var b []byte
	if oc, ok := u.CLI.(OwnerCLI); ok && repo.Owner != nil {
		b, err = oc.RunAs(ctx, *repo.Owner, dir, out, "git", full...)
	} else {
		b, err = u.CLI.Run(ctx, dir, out, "git", full...)
	}
	return strings.TrimSpace(string(b)), err
}

var errNoGit = errors.New("kein Git-Ordner")

// gitState liest den Stand des Git-Ordners, in dem dir liegt. Ohne remote
// fragt es die Gegenstelle nicht.
func (u *Updater) gitState(ctx context.Context, dir string, remote bool) (*GitState, error) {
	top, err := u.git(ctx, dir, nil, "rev-parse", "--show-toplevel")
	var refused *GitRefused
	if errors.As(err, &refused) {
		return nil, err
	}
	if err != nil || top == "" {
		return nil, errNoGit
	}
	g := &GitState{Dir: top}
	if g.Head, err = u.git(ctx, top, nil, "rev-parse", "HEAD"); err != nil {
		return nil, errors.New("Git-Ordner ohne Commit")
	}
	if t, err := u.git(ctx, top, nil, "log", "-1", "--format=%ct", "HEAD"); err == nil {
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			g.HeadTime = time.Unix(n, 0).UTC()
		}
	}
	if st, err := u.git(ctx, top, nil, "status", "--porcelain", "--untracked-files=no"); err == nil && st != "" {
		g.Dirty = true
	}
	g.Branch, _ = u.git(ctx, top, nil, "symbolic-ref", "--short", "-q", "HEAD")
	if g.Branch != "" {
		g.Remote, _ = u.git(ctx, top, nil, "config", "--get", "branch."+g.Branch+".remote")
		g.merge, _ = u.git(ctx, top, nil, "config", "--get", "branch."+g.Branch+".merge")
	}
	if !remote || g.Remote == "" || g.merge == "" || g.Remote == "." {
		return g, nil
	}
	line, err := u.git(ctx, top, nil, "ls-remote", "--", g.Remote, g.merge)
	if err != nil {
		return g, errors.New("Gegenstelle " + g.Remote + " nicht erreichbar: " + firstLine(err.Error()))
	}
	if f := strings.Fields(line); len(f) > 0 && len(f[0]) == 40 {
		g.RemoteHead = f[0]
	} else {
		return g, errors.New("die Gegenstelle " + g.Remote + " kennt den Zweig " + strings.TrimPrefix(g.merge, "refs/heads/") + " nicht")
	}
	return g, nil
}

// ahead sagt, ob der lokale Stand den der Gegenstelle schon enthält.
// known ist false, wenn der Commit der Gegenstelle lokal noch fehlt.
func (u *Updater) ahead(ctx context.Context, g *GitState) (ahead, known bool) {
	_, err := u.git(ctx, g.Dir, nil, "merge-base", "--is-ancestor", g.RemoteHead, "HEAD")
	if err == nil {
		return true, true
	}
	var re *RunError
	if errors.As(err, &re) && re.Code == 1 {
		return false, true
	}
	return false, false
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 200 {
		s = s[:200] + " …"
	}
	return s
}

// ShortID kürzt einen Commit oder eine Image-ID für die Anzeige.
func ShortID(s string) string {
	s = strings.TrimPrefix(s, "sha256:")
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
