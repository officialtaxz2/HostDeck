package discover

import (
	"strings"
	"testing"
)

func TestZeitplanLesbar(t *testing.T) {
	for _, c := range []struct{ kind, in, want string }{
		{"cron", "30 3 * * *", "täglich 03:30"},
		{"cron", "0 0,12 * * *", "täglich 00:00 und 12:00"},
		{"cron", "30 4 * * 1", "montags 04:30"},
		{"cron", "0 6 * * 1-5", "werktags 06:00"},
		{"cron", "17 * * * *", "stündlich zur Minute 17"},
		{"cron", "@reboot", "beim Start"},
		{"cron", "täglich", "täglich"},
		{"cron", "*/5 * * * *", ""},
		{"cron", "0 3 1 * *", ""},
		{"cron", "0 2-4 * * *", ""},
		{"timer", "OnCalendar=*-*-* 00,12:00:00", "täglich 00:00 und 12:00"},
		{"timer", "OnCalendar=*-*-* 04:15:00", "täglich 04:15"},
		{"timer", "OnCalendar=Mon *-*-* 04:00", "montags 04:00"},
		{"timer", "OnCalendar=daily", "täglich 00:00"},
		{"timer", "OnCalendar=*-*-01 00:00:00", ""},
		{"timer", "OnCalendar=*-*-* 04:15:30", ""},
		{"timer", "OnBootSec=5min", ""},
		{"timer", "OnCalendar=daily, OnBootSec=5min", ""},
		{"dienst", "", ""},
	} {
		if got := whenText(c.kind, c.in); got != c.want {
			t.Errorf("whenText(%q, %q) = %q, erwartet %q", c.kind, c.in, got, c.want)
		}
	}
}

func TestMOTDLesbar(t *testing.T) {
	for in, want := range map[string]string{
		`§aDemos §lServer`:          "Demos Server",
		`§6Survival§r\n§7seit 2024`: "Survival seit 2024",
		`Wort\:mit\=Zeichen`:        "Wort:mit=Zeichen",
		`  viel   Platz  `:          "viel Platz",
		strings.Repeat("x", 80):     strings.Repeat("x", 59) + "…",
		`äöü`:                       "äöü",
		`kaputt\u00`:                "kaputtu00",
	} {
		if got := cleanMOTD(in); got != want {
			t.Errorf("cleanMOTD(%q) = %q, erwartet %q", in, got, want)
		}
	}
}
