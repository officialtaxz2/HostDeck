package restore

import "fmt"

// Die Skripte laufen im Datenbank-Container und nehmen Zugangsdaten nur aus
// dessen Umgebung, wie beim Backup. Ausgabe der Zählskripte: eine Zeile
// „db|tabelle|anzahl“ je Tabelle.

const pgEnv = `export PGPASSWORD="${POSTGRES_PASSWORD:-}"
u="${POSTGRES_USER:-postgres}"
`

const pgReady = pgEnv + `exec pg_isready -q -h 127.0.0.1 -U "$u"`

const pgCount = pgEnv + `psql -X -At -U "$u" -d postgres -c "select datname from pg_database where datallowconn and not datistemplate order by 1" | while IFS= read -r db; do
  psql -X -At -F '|' -U "$u" -d "$db" -c "select current_database(), n.nspname || '.' || c.relname, (xpath('/row/c/text()', query_to_xml(format('select count(*) as c from %I.%I', n.nspname, c.relname), false, true, '')))[1]::text from pg_class c join pg_namespace n on n.oid = c.relnamespace where c.relkind = 'r' and n.nspname not in ('pg_catalog', 'information_schema') and n.nspname not like 'pg_toast%' order by 2" || exit 1
done`

const pgReplay = pgEnv + `exec psql -X -q -v ON_ERROR_STOP=0 -U "$u" -d postgres`

// mysqlLogin wählt root, wenn dessen Passwort da ist, sonst den App-Benutzer.
const mysqlLogin = `pw=""
if [ -n "${MYSQL_ROOT_PASSWORD_FILE:-}" ] && [ -r "$MYSQL_ROOT_PASSWORD_FILE" ]; then pw=$(cat "$MYSQL_ROOT_PASSWORD_FILE"); else pw="${MARIADB_ROOT_PASSWORD:-${MYSQL_ROOT_PASSWORD:-}}"; fi
if [ -n "$pw" ] || [ -n "${MYSQL_ALLOW_EMPTY_PASSWORD:-}${MARIADB_ALLOW_EMPTY_ROOT_PASSWORD:-}" ]; then
  export MYSQL_PWD="$pw"; u=root
else
  export MYSQL_PWD="${MARIADB_PASSWORD:-${MYSQL_PASSWORD:-}}"; u="${MARIADB_USER:-${MYSQL_USER:-root}}"
fi
c=mysql; command -v mysql >/dev/null 2>&1 || c=mariadb
a=mysqladmin; command -v mysqladmin >/dev/null 2>&1 || a=mariadb-admin
`

const mysqlReady = mysqlLogin + `exec "$a" ping -h 127.0.0.1 --protocol=tcp --silent --user="$u"`

const mysqlCount = mysqlLogin + `"$c" -N -B --user="$u" -e "select table_schema, table_name from information_schema.tables where table_type = 'BASE TABLE' and table_schema not in ('mysql', 'sys', 'performance_schema', 'information_schema') order by 1, 2" | while IFS="$(printf '\t')" read -r d t; do
  n=$("$c" -N -B --user="$u" -e "select count(*) from ` + "\\`$d\\`.\\`$t\\`" + `") || exit 1
  echo "$d|$t|$n"
done`

const mysqlReplay = mysqlLogin + `"$c" --user="$u" || exit $?
[ "$u" = root ] && "$c" --user="$u" -e "FLUSH PRIVILEGES"
exit 0`

// Scripts sind die Befehle für eine Datenbank-Art.
type Scripts struct {
	Ready, Count, Replay string
}

// ScriptsFor liefert die Skripte für engine.
func ScriptsFor(engine string) (Scripts, error) {
	switch engine {
	case "postgres":
		return Scripts{Ready: pgReady, Count: pgCount, Replay: pgReplay}, nil
	case "mysql", "mariadb":
		return Scripts{Ready: mysqlReady, Count: mysqlCount, Replay: mysqlReplay}, nil
	}
	return Scripts{}, fmt.Errorf("für %q kann HostDeck noch keinen Dump einspielen", engine)
}
