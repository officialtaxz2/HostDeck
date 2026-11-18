package discover

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// whenText übersetzt gängige Zeitpläne von cron und systemd in Deutsch,
// etwa „30 3 * * *“ in „täglich 03:30“. Was es nicht sicher versteht,
// bleibt leer; dann zeigt HostDeck den Zeitplan, wie er in der Datei steht.
func whenText(kind, schedule string) string {
	switch kind {
	case "cron":
		return cronWhen(schedule)
	case "timer":
		v, ok := strings.CutPrefix(schedule, "OnCalendar=")
		if !ok || strings.Contains(v, ", On") {
			return ""
		}
		return calendarWhen(v)
	}
	return ""
}

var cronWords = map[string]string{
	"@reboot": "beim Start", "@hourly": "stündlich", "@daily": "täglich 00:00", "@midnight": "täglich 00:00",
	"@weekly": "sonntags 00:00", "@monthly": "monatlich am 1. um 00:00", "@yearly": "jährlich am 1.1. um 00:00",
	"@annually": "jährlich am 1.1. um 00:00",
	// cron.daily und Co. stehen schon lesbar da
	"stündlich": "stündlich", "täglich": "täglich", "wöchentlich": "wöchentlich", "monatlich": "monatlich",
}

// Wochentage für cron (0 und 7 = Sonntag) und systemd.
var weekdays = map[string]string{
	"0": "sonntags", "1": "montags", "2": "dienstags", "3": "mittwochs", "4": "donnerstags", "5": "freitags", "6": "samstags", "7": "sonntags",
	"sun": "sonntags", "mon": "montags", "tue": "dienstags", "wed": "mittwochs", "thu": "donnerstags", "fri": "freitags", "sat": "samstags",
}

func cronWhen(s string) string {
	if w, ok := cronWords[s]; ok {
		return w
	}
	f := strings.Fields(s)
	if len(f) != 5 || f[2] != "*" || f[3] != "*" {
		return ""
	}
	day, ok := dayText(f[4])
	if !ok {
		return ""
	}
	mins, ok := numbers(f[0], 59)
	if !ok {
		return ""
	}
	if f[1] == "*" {
		if day != "täglich" || len(mins) != 1 {
			return ""
		}
		return fmt.Sprintf("stündlich zur Minute %d", mins[0])
	}
	hours, ok := numbers(f[1], 23)
	if !ok {
		return ""
	}
	return day + " " + times(hours, mins)
}

// calendarWhen versteht OnCalendar in den einfachen Formen „daily“ und
// „[Wochentag] *-*-* HH:MM[:SS]“ mit Stundenlisten wie 00,12.
func calendarWhen(v string) string {
	switch strings.ToLower(v) {
	case "hourly":
		return "stündlich"
	case "daily":
		return "täglich 00:00"
	case "weekly":
		return "montags 00:00"
	case "monthly":
		return "monatlich am 1. um 00:00"
	}
	f := strings.Fields(v)
	day := "täglich"
	if len(f) == 3 {
		d, ok := weekdays[strings.ToLower(f[0])]
		if !ok {
			return ""
		}
		day, f = d, f[1:]
	}
	if len(f) != 2 || f[0] != "*-*-*" {
		return ""
	}
	hm := strings.Split(f[1], ":")
	if len(hm) < 2 || len(hm) > 3 || (len(hm) == 3 && hm[2] != "00" && hm[2] != "0") {
		return ""
	}
	hours, ok := numbers(hm[0], 23)
	if !ok {
		return ""
	}
	mins, ok := numbers(hm[1], 59)
	if !ok {
		return ""
	}
	return day + " " + times(hours, mins)
}

func dayText(dow string) (string, bool) {
	if dow == "*" {
		return "täglich", true
	}
	if dow == "1-5" || strings.EqualFold(dow, "mon-fri") {
		return "werktags", true
	}
	d, ok := weekdays[strings.ToLower(dow)]
	return d, ok
}

// numbers liest „3“ oder „0,12“; Bereiche und Schritte gelten als unklar.
func numbers(s string, maxVal int) ([]int, bool) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > maxVal {
			return nil, false
		}
		out = append(out, n)
	}
	sort.Ints(out)
	return out, len(out) > 0 && len(out) <= 6
}

func times(hours, mins []int) string {
	var ts []string
	for _, h := range hours {
		for _, m := range mins {
			ts = append(ts, fmt.Sprintf("%02d:%02d", h, m))
		}
	}
	if len(ts) == 1 {
		return ts[0]
	}
	return strings.Join(ts[:len(ts)-1], ", ") + " und " + ts[len(ts)-1]
}
