package backup_test

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/backup"
)

// shellExec führt den Dump-Befehl mit einem echten sh aus, mit erfundenen
// Programmen pg_dumpall und mysqldump im PATH. So lässt sich prüfen, dass
// Passwörter nur in der Umgebung stehen, nie in den Argumenten.
type shellExec struct {
	env []string
	bin string
}

func (s shellExec) Exec(ctx context.Context, _ string, cmd []string, stdout, stderr io.Writer) (int, error) {
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Env = append([]string{"PATH=" + s.bin + ":/usr/bin:/bin"}, s.env...)
	c.Stdout, c.Stderr = stdout, stderr
	err := c.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return -1, err
	}
	return c.ProcessState.ExitCode(), nil
}

func fakeTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("pg_dumpall", `echo "ARGS $*"; echo "PW ${PGPASSWORD:-leer}"; echo "-- PostgreSQL database cluster dump complete"`)
	write("mysqldump", `echo "ARGS $*"; echo "PW ${MYSQL_PWD:-leer}"; echo "-- Dump completed on 2026-11-01"`)
	return dir
}

func TestDumpPasswoerterNurInDerUmgebung(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh fehlt")
	}
	bin := fakeTools(t)
	cases := []struct {
		engine string
		env    []string
		args   string
	}{
		{"postgres", []string{"POSTGRES_USER=app", "POSTGRES_PASSWORD=geheim-pg"}, "--username=app"},
		{"mysql", []string{"MYSQL_ROOT_PASSWORD=geheim-root"}, "--user=root"},
		{"mysql", []string{"MYSQL_USER=urls", "MYSQL_PASSWORD=geheim-user", "MYSQL_DATABASE=urls"}, "--databases urls"},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		err := backup.Dump(context.Background(), shellExec{env: c.env, bin: bin}, "ctr", c.engine, &out, &errb)
		if err != nil {
			t.Errorf("%s %v: %v (%s)", c.engine, c.env, err, errb.String())
			continue
		}
		args := ""
		for _, line := range strings.Split(out.String(), "\n") {
			if a, ok := strings.CutPrefix(line, "ARGS "); ok {
				args = a
			}
		}
		if strings.Contains(args, "geheim") {
			t.Errorf("%s: Passwort in den Argumenten: %s", c.engine, args)
		}
		if !strings.Contains(args, c.args) {
			t.Errorf("%s: Argumente %q ohne %q", c.engine, args, c.args)
		}
		if !strings.Contains(out.String(), "PW geheim-") {
			t.Errorf("%s: Passwort kommt nicht über die Umgebung an:\n%s", c.engine, out.String())
		}
	}

	// Ohne Zugangsdaten bricht der MySQL-Dump mit Erklärung ab.
	var out, errb bytes.Buffer
	if err := backup.Dump(context.Background(), shellExec{bin: bin}, "ctr", "mysql", &out, &errb); err == nil {
		t.Error("MySQL-Dump ohne Zugangsdaten meldet keinen Fehler")
	}
}

type fixedExec struct {
	out  string
	code int
}

func (f fixedExec) Exec(_ context.Context, _ string, _ []string, stdout, _ io.Writer) (int, error) {
	io.WriteString(stdout, f.out)
	return f.code, nil
}

func TestDumpErkenntUnvollstaendigeDumps(t *testing.T) {
	ok := "CREATE TABLE a;\n--\n-- PostgreSQL database cluster dump complete\n--\n\n"
	if err := backup.Dump(context.Background(), fixedExec{out: ok}, "c", "postgres", io.Discard, io.Discard); err != nil {
		t.Errorf("vollständiger Dump abgelehnt: %v", err)
	}
	if err := backup.Dump(context.Background(), fixedExec{out: "CREATE TABLE a;\n"}, "c", "postgres", io.Discard, io.Discard); err == nil {
		t.Error("Dump ohne Abschlusszeile angenommen")
	}
	if err := backup.Dump(context.Background(), fixedExec{out: ok, code: 1}, "c", "postgres", io.Discard, io.Discard); err == nil {
		t.Error("Dump mit Exit-Code 1 angenommen")
	}
	if err := backup.Dump(context.Background(), fixedExec{}, "c", "redis", io.Discard, io.Discard); err == nil {
		t.Error("Dump für Redis angenommen")
	}
}

type fakeSaver struct {
	data  []byte
	names []string
}

func (f *fakeSaver) SaveImage(_ context.Context, refs []string, _ string, w io.Writer) error {
	f.names = refs
	_, err := w.Write(f.data)
	return err
}

func imageTar(t *testing.T) []byte {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, name := range []string{"blobs/sha256/abc", "index.json"} {
		body := []byte("{}")
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		tw.Write(body)
	}
	tw.Close()
	return b.Bytes()
}

func TestImageArchivWirdGeprueft(t *testing.T) {
	full := imageTar(t)
	s := &fakeSaver{data: full}
	var out bytes.Buffer
	if err := backup.SaveImage(context.Background(), s, "sha256:abc", []string{"shop:lokal"}, "", &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), full) {
		t.Error("Archiv nicht unverändert weitergegeben")
	}
	if len(s.names) != 1 || s.names[0] != "shop:lokal" {
		t.Errorf("gespeichert unter %v, erwartet den Namen", s.names)
	}
	if err := backup.SaveImage(context.Background(), &fakeSaver{data: full[:700]}, "sha256:abc", nil, "", io.Discard); err == nil {
		t.Error("abgeschnittenes Archiv angenommen")
	}
	noName := &fakeSaver{data: full}
	if err := backup.SaveImage(context.Background(), noName, "sha256:abc", nil, "", io.Discard); err != nil || noName.names[0] != "sha256:abc" {
		t.Errorf("Image ohne Namen nicht über die ID gespeichert: %v %v", noName.names, err)
	}
}

// Passwörter stehen nie in den Argumenten eines Prozesses (ps zeigt sie).
func TestDumpOhnePasswortImArgument(t *testing.T) {
	for _, e := range []string{"postgres", "mysql", "mariadb", "mongo"} {
		cmd, _, err := backup.DumpCommand(e)
		if err != nil {
			t.Fatal(err)
		}
		script := strings.Join(cmd, " ")
		if strings.Contains(script, "--password") || strings.Contains(script, " -p") {
			t.Errorf("%s: Passwort als Argument: %s", e, script)
		}
	}
}
