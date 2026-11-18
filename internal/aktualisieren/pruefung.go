package aktualisieren

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func or(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}

// verify wartet, bis die Dienste laufen, ruhig bleiben und ihre
// Healthchecks grün sind, und fragt dann die Domains ab. Eine Domain, die
// vorher antwortete und jetzt nicht mehr, gilt als Fehler.
func (u *Updater) verify(ctx context.Context, p *Project, services []string, before map[string]caddy.Answer, domains []string, out io.Writer) error {
	wait, settle, poll := or(u.Wait, 120*time.Second), or(u.Settle, 10*time.Second), or(u.Poll, 2*time.Second)
	fmt.Fprintf(out, "Prüfen: %s laufen und bleiben %s ruhig …\n", strings.Join(services, ", "), secs(settle))
	deadline := time.Now().Add(wait)
	since := map[string]time.Time{}
	restarts := map[string]int{}
	ids := map[string]string{}
	var why map[string]string
	for {
		why = map[string]string{}
		list, err := u.Docker.List(ctx)
		if err != nil {
			return fmt.Errorf("Docker antwortet nicht: %v", err)
		}
		now := time.Now()
		for _, name := range services {
			id := ""
			for _, c := range list {
				if c.Labels[labelProject] == p.Name && c.Labels[labelService] == name && !strings.EqualFold(c.Labels[labelOneoff], "true") {
					id = c.ID
					break
				}
			}
			if id == "" {
				why[name] = "kein Container"
				delete(since, name)
				continue
			}
			v, err := u.Docker.Inspect(ctx, id)
			if err != nil {
				why[name] = "nicht lesbar"
				delete(since, name)
				continue
			}
			if ids[name] != id {
				ids[name], restarts[name] = id, v.RestartCount
			}
			switch {
			case v.State.Health != nil && v.State.Health.Status == "unhealthy":
				return fmt.Errorf("%s meldet sich krank (Healthcheck)", name)
			case !v.State.Running || v.State.Restarting:
				why[name] = "läuft nicht"
				if v.State.ExitCode != 0 {
					why[name] = fmt.Sprintf("beendet mit Code %d", v.State.ExitCode)
				}
			case v.RestartCount != restarts[name]:
				why[name] = "startet neu"
				restarts[name] = v.RestartCount
			case v.State.Health != nil && v.State.Health.Status != "healthy":
				why[name] = "Healthcheck noch nicht grün"
			}
			if why[name] != "" {
				delete(since, name)
			} else if since[name].IsZero() {
				since[name] = now
			}
		}
		ok := len(why) == 0
		for _, name := range services {
			if ok && now.Sub(since[name]) < settle {
				ok = false
			}
		}
		if ok {
			break
		}
		if now.After(deadline) {
			var bad []string
			for n, w := range why {
				bad = append(bad, n+": "+w)
			}
			sort.Strings(bad)
			if len(bad) == 0 {
				bad = []string{"nicht lange genug ruhig"}
			}
			return fmt.Errorf("nach %s nicht sauber (%s)", secs(wait), strings.Join(bad, "; "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
	if u.Probe == nil || len(domains) == 0 {
		fmt.Fprintln(out, "  Container laufen.")
		return nil
	}
	fmt.Fprintf(out, "  Container laufen. Domains abfragen: %s\n", strings.Join(domains, ", "))
	// Ein frisch gestarteter Dienst braucht manchmal ein paar Sekunden, bis
	// er antwortet.
	for _, h := range domains {
		var now caddy.Answer
		for i := 0; i < 6; i++ {
			now = u.Probe(ctx, h)
			if !caddy.Worse(before[h], now) {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(poll * 2):
			}
		}
		if caddy.Worse(before[h], now) {
			return fmt.Errorf("%s antwortet schlechter als vorher (vorher %s, jetzt %s)", h, before[h], now)
		}
		fmt.Fprintf(out, "  %s: %s\n", h, now)
	}
	return nil
}

func secs(d time.Duration) string {
	if d < time.Second {
		return d.String()
	}
	return fmt.Sprintf("%d Sekunden", int(d.Seconds()))
}
