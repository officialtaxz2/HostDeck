package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
)

// Execer führt einen Befehl in einem laufenden Container aus.
type Execer interface {
	Exec(ctx context.Context, id string, cmd []string, stdout, stderr io.Writer) (int, error)
}

// Die Dump-Befehle laufen im Container und nehmen die Zugangsdaten aus
// dessen Umgebung. HostDeck liest sie nie selbst; sie erscheinen auch nicht in
// den Argumenten eines Prozesses, sondern nur in Umgebungsvariablen.
const (
	postgresDump = `export PGPASSWORD="${POSTGRES_PASSWORD:-}"
exec pg_dumpall --username="${POSTGRES_USER:-postgres}"`

	mysqlDump = `pw=""
if [ -n "${MYSQL_ROOT_PASSWORD_FILE:-}" ] && [ -r "$MYSQL_ROOT_PASSWORD_FILE" ]; then pw=$(cat "$MYSQL_ROOT_PASSWORD_FILE"); else pw="${MYSQL_ROOT_PASSWORD:-}"; fi
if [ -n "$pw" ] || [ -n "${MYSQL_ALLOW_EMPTY_PASSWORD:-}" ]; then
  export MYSQL_PWD="$pw"; u=root; set -- --all-databases --events
elif [ -n "${MYSQL_USER:-}" ] && [ -n "${MYSQL_DATABASE:-}" ]; then
  export MYSQL_PWD="${MYSQL_PASSWORD:-}"; u="$MYSQL_USER"; set -- --databases "$MYSQL_DATABASE"
else
  echo "HostDeck: im Container fehlen MYSQL_ROOT_PASSWORD oder MYSQL_USER und MYSQL_DATABASE" >&2; exit 3
fi
exec mysqldump --user="$u" --single-transaction --routines --triggers --hex-blob "$@"`

	mariadbDump = `pw="${MARIADB_ROOT_PASSWORD:-${MYSQL_ROOT_PASSWORD:-}}"
d=mysqldump; command -v mariadb-dump >/dev/null 2>&1 && d=mariadb-dump
if [ -n "$pw" ] || [ -n "${MARIADB_ALLOW_EMPTY_ROOT_PASSWORD:-${MYSQL_ALLOW_EMPTY_PASSWORD:-}}" ]; then
  export MYSQL_PWD="$pw"; u=root; set -- --all-databases --events
elif [ -n "${MARIADB_USER:-${MYSQL_USER:-}}" ] && [ -n "${MARIADB_DATABASE:-${MYSQL_DATABASE:-}}" ]; then
  export MYSQL_PWD="${MARIADB_PASSWORD:-${MYSQL_PASSWORD:-}}"; u="${MARIADB_USER:-$MYSQL_USER}"; set -- --databases "${MARIADB_DATABASE:-$MYSQL_DATABASE}"
else
  echo "HostDeck: im Container fehlen MARIADB_ROOT_PASSWORD oder MARIADB_USER und MARIADB_DATABASE" >&2; exit 3
fi
exec "$d" --user="$u" --single-transaction --routines --triggers --hex-blob "$@"`

	// Das Passwort geht über eine Konfigurationsdatei (nur für root lesbar,
	// wird gleich wieder gelöscht) statt über --password, das in ps stünde.
	// --config gibt es in den MongoDB Database Tools ab 100.3.
	mongoDump = `if [ -n "${MONGO_INITDB_ROOT_USERNAME:-}" ]; then
  f=$(mktemp) || exit 3
  trap 'rm -f "$f"' EXIT
  p=$(printf '%s' "${MONGO_INITDB_ROOT_PASSWORD:-}" | sed "s/'/''/g")
  printf "password: '%s'\n" "$p" > "$f"
  mongodump --archive --username "$MONGO_INITDB_ROOT_USERNAME" --config "$f" --authenticationDatabase admin
  exit $?
fi
exec mongodump --archive`
)

// DumpCommand liefert den Befehl im Container und die Zeile, mit der ein
// vollständiger Dump endet. Ohne Endzeile zählt nur der Exit-Code.
func DumpCommand(engine string) (cmd []string, marker string, err error) {
	switch engine {
	case "postgres":
		return []string{"sh", "-c", postgresDump}, "-- PostgreSQL database cluster dump complete", nil
	case "mysql":
		return []string{"sh", "-c", mysqlDump}, "-- Dump completed", nil
	case "mariadb":
		return []string{"sh", "-c", mariadbDump}, "-- Dump completed", nil
	case "mongo":
		return []string{"sh", "-c", mongoDump}, "", nil
	}
	return nil, "", fmt.Errorf("für %q kann HostDeck keinen Dump machen", engine)
}

// lastBytes behält das Ende eines Datenstroms.
type lastBytes struct {
	buf []byte
	max int
}

func (l *lastBytes) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	if len(l.buf) > l.max {
		l.buf = append(l.buf[:0], l.buf[len(l.buf)-l.max:]...)
	}
	return len(p), nil
}

// Dump schreibt den Dump einer Datenbank nach w. Er scheitert, wenn der
// Befehl nicht mit 0 endet oder die Endzeile fehlt; restic legt dann keinen
// Snapshot an.
func Dump(ctx context.Context, d Execer, container, engine string, w, errw io.Writer) error {
	cmd, marker, err := DumpCommand(engine)
	if err != nil {
		return err
	}
	end := &lastBytes{max: 4096}
	code, err := d.Exec(ctx, container, cmd, io.MultiWriter(w, end), errw)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("Dump-Befehl im Container endet mit Exit-Code %d", code)
	}
	if marker != "" && !bytes.Contains(end.buf, []byte(marker)) {
		return fmt.Errorf("Dump unvollständig: die Abschlusszeile %q fehlt", marker)
	}
	return nil
}

// ImageSaver exportiert Images.
type ImageSaver interface {
	SaveImage(ctx context.Context, refs []string, platform string, w io.Writer) error
}

// SaveImage schreibt ein Image als Tar-Archiv nach w und prüft dabei, dass
// das Archiv vollständig ist. Mit Namen gespeichert, bekommt das Image sie
// beim Laden zurück; ein Image ohne Namen wird über seine ID gespeichert.
func SaveImage(ctx context.Context, s ImageSaver, id string, refs []string, platform string, w io.Writer) error {
	names := refs
	if len(names) == 0 {
		names = []string{id}
	}
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		err := s.SaveImage(ctx, names, platform, pw)
		pw.CloseWithError(err)
		errc <- err
	}()
	tr := tar.NewReader(io.TeeReader(pr, w))
	index := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			pr.CloseWithError(err)
			<-errc
			return fmt.Errorf("Image-Archiv unvollständig: %w", err)
		}
		if h.Name == "manifest.json" || h.Name == "index.json" {
			index = true
		}
	}
	// Rest nach dem Tar-Ende (Füllbytes) unverändert weitergeben.
	if _, err := io.Copy(w, pr); err != nil {
		pr.CloseWithError(err)
		<-errc
		return err
	}
	if err := <-errc; err != nil {
		return err
	}
	if !index {
		return errors.New("Image-Archiv ohne manifest.json oder index.json")
	}
	return nil
}
