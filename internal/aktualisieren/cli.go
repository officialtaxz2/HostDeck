package aktualisieren

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CLI ruft Programme auf: docker (für Compose) und git.
type CLI interface {
	// Run führt name mit args in dir aus. Die Standardausgabe kommt zurück;
	// ist out gesetzt, laufen Ausgabe und Fehlerausgabe zusätzlich dorthin.
	Run(ctx context.Context, dir string, out io.Writer, name string, args ...string) ([]byte, error)
}

// OwnerCLI kann ein Programm als anderer Benutzer starten (Git als
// Besitzer des Projektordners).
type OwnerCLI interface {
	RunAs(ctx context.Context, o Owner, dir string, out io.Writer, name string, args ...string) ([]byte, error)
}

// Exec ist CLI mit echten Programmen.
type Exec struct{}

func (Exec) Run(ctx context.Context, dir string, out io.Writer, name string, args ...string) ([]byte, error) {
	return run(ctx, nil, dir, out, name, args...)
}

// RunAs startet name als o.
func (Exec) RunAs(ctx context.Context, o Owner, dir string, out io.Writer, name string, args ...string) ([]byte, error) {
	return run(ctx, &o, dir, out, name, args...)
}

func run(ctx context.Context, o *Owner, dir string, out io.Writer, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	env := os.Environ()
	if name == "git" {
		env = gitEnv(env)
	}
	if o != nil {
		kept := env[:0:0]
		for _, e := range env {
			if !strings.HasPrefix(e, "HOME=") && !strings.HasPrefix(e, "USER=") && !strings.HasPrefix(e, "LOGNAME=") && !strings.HasPrefix(e, "XDG_") {
				kept = append(kept, e)
			}
		}
		env = append(kept, "HOME="+o.Home)
		setOwner(&cmd.SysProcAttr, o)
	}
	cmd.Env = append(env,
		// Nie auf eine Eingabe warten: kein Passwort, keine Rückfrage zum
		// Schlüssel eines Git-Servers.
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=15",
		"LC_ALL=C",
	)
	if o == nil && os.Getenv("HOME") == "" {
		cmd.Env = append(cmd.Env, "HOME=/root")
	}
	var stdout bytes.Buffer
	tail := &tailBuf{max: 2000}
	if out != nil {
		cmd.Stdout = io.MultiWriter(&stdout, out)
		cmd.Stderr = io.MultiWriter(tail, out)
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = tail
	}
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(tail.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), &RunError{Cmd: name + " " + firstArgs(args), Msg: msg, Code: exitCode(err)}
	}
	return stdout.Bytes(), nil
}

// RunError ist ein gescheiterter Aufruf.
type RunError struct {
	Cmd  string
	Msg  string
	Code int
}

func (e *RunError) Error() string { return fmt.Sprintf("%s: %s", e.Cmd, e.Msg) }

func exitCode(err error) int {
	if x, ok := err.(*exec.ExitError); ok {
		return x.ExitCode()
	}
	return -1
}

// firstArgs nennt den Befehl kurz, etwa „compose“ oder „pull“.
func firstArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// tailBuf behält das Ende einer Ausgabe.
type tailBuf struct {
	b   []byte
	max int
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append([]byte("…"), t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuf) String() string { return string(t.b) }
