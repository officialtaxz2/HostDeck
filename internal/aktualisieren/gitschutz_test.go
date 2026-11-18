package aktualisieren

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitKonfigPositivliste(t *testing.T) {
	ok := "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n\tlogallrefupdates = true\n" +
		"[remote \"origin\"]\n\turl = https://github.com/beispiel/shop.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n" +
		"[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n; Kommentar\n# noch einer\n[user]\n\tname = Demo # Name\n"
	for name, tc := range map[string]struct{ cfg, key string }{
		"normal":            {ok, ""},
		"alt geschrieben":   {"[remote.origin]\n url = x\n", ""},
		"gross geschrieben": {"[CORE]\n\tBare = false\n", ""},
		"fsmonitor":         {ok + "[core]\n\tfsmonitor = touch /tmp/x\n", "core.fsmonitor"},
		"FsMonitor":         {"[Core]\n\tFsMonitor=x\n", "core.fsmonitor"},
		"in einer Zeile":    {"[core] fsmonitor = x\n", "core.fsmonitor"},
		"filter":            {"[filter \"lfs\"]\n\tsmudge = sh -c x\n", "filter.lfs"},
		"include":           {"[include]\n\tpath = /tmp/x\n", "include"},
		"includeIf":         {"[includeIf \"gitdir:/opt/\"]\n\tpath = /tmp/x\n", "includeif.gitdir:/opt/"},
		"credential":        {"[credential]\n\thelper = !sh -c x\n", "credential"},
		"sshCommand":        {"[core]\n\tsshCommand = sh -c x\n", "core.sshcommand"},
		"hooksPath":         {"[core]\n\thooksPath = /tmp\n", "core.hookspath"},
		"worktree":          {"[core]\n\tworktree = /\n", "core.worktree"},
		"uploadpack":        {"[remote \"origin\"]\n\tuploadpack = sh -c x\n", "remote.origin.uploadpack"},
		"vcs":               {"[remote \"origin\"]\n\tvcs = x\n", "remote.origin.vcs"},
		"submodule update":  {"[submodule \"a\"]\n\tupdate = !sh\n", "submodule.a.update"},
		"core mit Unter":    {"[core \"x\"]\n\tbare = false\n", "core.x"},
		"remote ohne Unter": {"[remote]\n\turl = x\n", "remote"},
		"Escapes":           {"[branch \"a\\\"b\"]\n\tremote = origin\n", ""},
	} {
		key, err := checkGitConfig(bufio.NewScanner(strings.NewReader(tc.cfg)))
		if err != nil || key != tc.key {
			t.Errorf("%s: %q %v, erwartet %q", name, key, err, tc.key)
		}
	}
	for name, cfg := range map[string]string{
		"Fortsetzung":        "[core]\n\tbare = false \\\n\tfsmonitor = x\n",
		"vor Abschnitt":      "bare = false\n",
		"offen":              "[remote \"origin\n",
		"nicht lesbar":       "[core]\n\t=x\n",
		"Unsinn nach Name":   "[core]\n\tbare false\n",
		"ohne Klammer":       "[core\n",
		"leerer Abschnitt":   "[]\n",
		"doppelt unterteilt": "[a.b \"c\"]\n",
	} {
		if key, err := checkGitConfig(bufio.NewScanner(strings.NewReader(cfg))); err == nil {
			t.Errorf("%s: angenommen (%q)", name, key)
		}
	}
}

func gitRepo(t *testing.T, cfg ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git fehlt")
	}
	repo := filepath.Join(t.TempDir(), "projekt")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.MkdirAll(filepath.Join(repo, "app"), 0o755)
	run("init", "-q")
	os.WriteFile(filepath.Join(repo, "app", "f"), []byte("x"), 0o644)
	run("add", ".")
	run("commit", "-qm", "x")
	for i := 0; i+1 < len(cfg); i += 2 {
		run("config", cfg[i], cfg[i+1])
	}
	return repo
}

func TestGitOrdnerPruefen(t *testing.T) {
	repo := gitRepo(t)
	r, err := CheckGitDir(filepath.Join(repo, "app"))
	if err != nil || r.Top != repo {
		t.Fatalf("Unterordner: %+v %v", r, err)
	}
	if _, err := CheckGitDir(t.TempDir()); !errors.Is(err, errNoGit) {
		t.Fatalf("ohne Git: %v", err)
	}
	// Worktree oder Submodul: .git ist eine Datei.
	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+repo+"/.git\n"), 0o644)
	var refused *GitRefused
	if _, err := CheckGitDir(wt); !errors.As(err, &refused) {
		t.Fatalf(".git als Datei: %v", err)
	}
	// Verweis statt Datei.
	link := gitRepo(t)
	cfg := filepath.Join(link, ".git", "config")
	os.Rename(cfg, cfg+".echt")
	os.Symlink(cfg+".echt", cfg)
	if _, err := CheckGitDir(link); !errors.As(err, &refused) {
		t.Fatalf("config als Verweis: %v", err)
	}
	bad := gitRepo(t, "filter.x.smudge", "touch /tmp/hostdeck-nie")
	if _, err := CheckGitDir(bad); !errors.As(err, &refused) || !strings.Contains(err.Error(), "filter.x") {
		t.Fatalf("filter: %v", err)
	}
}

// Auch wenn die Prüfung etwas durchließe: Die Befehlszeile hält
// core.fsmonitor fest.
func TestGitSchalterGewinnen(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ausgefuehrt")
	repo := gitRepo(t, "core.fsmonitor", "touch "+marker+"; echo 0 #")
	u := &Updater{CLI: Exec{}, GitCheck: func(string) (GitRepo, error) { return GitRepo{Top: repo}, nil }}
	g, err := u.gitState(context.Background(), repo, false)
	if err != nil || g.Head == "" {
		t.Fatalf("gitState: %+v %v", g, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("core.fsmonitor lief trotz Schalter auf der Befehlszeile")
	}
	// Ohne Prüfung (echter Weg): abgelehnt, mit Grund.
	u.GitCheck = nil
	var refused *GitRefused
	if _, err := u.gitState(context.Background(), repo, false); !errors.As(err, &refused) || !strings.Contains(err.Error(), "core.fsmonitor") {
		t.Fatalf("ohne Attrappe: %v", err)
	}
}

func TestGitAlsBesitzer(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("braucht root")
	}
	repo := gitRepo(t)
	if err := filepath.Walk(repo, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, 65534, 65534)
	}); err != nil {
		t.Fatal(err)
	}
	// Der Weg dorthin muss für nobody begehbar sein.
	// Nur die Ordner des Tests, nie /tmp selbst.
	base := filepath.Clean(os.TempDir())
	for d := filepath.Dir(repo); strings.HasPrefix(d, base+string(filepath.Separator)); d = filepath.Dir(d) {
		os.Chmod(d, 0o755)
	}
	r, err := CheckGitDir(repo)
	if err != nil || r.Owner == nil || r.Owner.UID != 65534 {
		t.Fatalf("Besitzer: %+v %v", r, err)
	}
	out, err := Exec{}.RunAs(context.Background(), *r.Owner, repo, nil, "id", "-u")
	if err != nil || strings.TrimSpace(string(out)) != "65534" {
		t.Fatalf("id als Besitzer: %q %v", out, err)
	}
	u := &Updater{CLI: Exec{}}
	if g, err := u.gitState(context.Background(), repo, false); err != nil || g.Head == "" {
		t.Fatalf("gitState als Besitzer: %+v %v", g, err)
	}
}
