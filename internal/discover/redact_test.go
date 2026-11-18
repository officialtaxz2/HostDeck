package discover

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"/root/backup.sh >> /var/log/backup.log 2>&1":                                    "/root/backup.sh >> /var/log/backup.log 2>&1",
		"mysqldump -uroot -pgeheim-mysql alle > /backup/db.sql":                          "mysqldump -uroot -p… alle > /backup/db.sql",
		"PGPASSWORD=geheim-pg pg_dump -h localhost app":                                  "PGPASSWORD=… pg_dump -h localhost app",
		"curl -s -H 'Authorization: Bearer geheim-token' https://api.example.org/x":      "curl -s -H 'Authorization: …' https://api.example.org/x",
		"curl -u admin:geheim-pw https://example.org":                                    "curl -u … https://example.org",
		"restic -r sftp:pc:/b --password-file /root/.restic backup /opt":                 "restic -r sftp:pc:/b --password-file … backup /opt",
		"wget -qO- https://user:geheim-url@example.org/hook":                             "wget -qO- https://…@example.org/hook",
		"curl https://hc-ping.com/0f3c9a7e2b1d4c6f8a9b0c1d2e3f4a5b6c7d8e9f":              "curl https://hc-ping.com/…",
		"cd /opt/docker/ein-ziemlich-langer-projektname-ohne-besonderheiten && git pull": "cd /opt/docker/ein-ziemlich-langer-projektname-ohne-besonderheiten && git pull",
		"mkdir -p /opt/x && tar czf /opt/x/a.tgz /etc":                                   "mkdir -p /opt/x && tar czf /opt/x/a.tgz /etc",
		"/usr/bin/app --api-key=geheim-key --verbose":                                    "/usr/bin/app --api-key=… --verbose",
	}
	for in, want := range cases {
		got := redact(in)
		if got != want {
			t.Errorf("redact(%q)\n  = %q\n  erwartet %q", in, got, want)
		}
		if strings.Contains(got, "geheim") {
			t.Errorf("Geheimnis bleibt stehen: %q", got)
		}
	}
	if long := redact(strings.Repeat("echo a; ", 40)); len([]rune(long)) > maxCommand {
		t.Errorf("nicht gekürzt: %d Zeichen", len([]rune(long)))
	}
}
