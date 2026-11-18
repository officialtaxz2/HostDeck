package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/remote"
	"github.com/officialtaxz2/hostdeck/internal/restore"
	"github.com/officialtaxz2/hostdeck/internal/serverebene"
)

// Nach Projekten und Leitstand kommt die Server-Ebene. Beim
// Probe-Umzug prüft der neue Server nur, was am Umzugstag dazukäme; dann
// sucht HostDeck hier noch nach der alten Adresse in Dateien.

const moveServer dienst.MoveKind = "server-ebene"

// serverChoiceFile hält die Haken der Server-Ebene, im Ordner des Umzugs.
const serverChoiceFile = "server-ebene.json"

type serverChoice struct {
	Chosen []string `json:"angekreuzt"`
}

func (m *serverMoves) source() (serverebene.Source, error) {
	host, _ := os.Hostname()
	src := serverebene.Source{Root: m.Root, Host: host}
	if m.Inventory == nil {
		return src, errors.New("keine Bestandsaufnahme")
	}
	inv, err := m.Inventory()
	if err != nil {
		return src, err
	}
	src.Inv = inv
	return src, nil
}

func (m *serverMoves) chosen() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(m.Dir, serverChoiceFile))
	if err != nil {
		return out
	}
	var c serverChoice
	if json.Unmarshal(b, &c) == nil {
		for _, k := range c.Chosen {
			out[k] = true
		}
	}
	return out
}

// ChooseServer speichert die Haken; nur Punkte, die es hier gibt.
func (m *serverMoves) ChooseServer(keys []string) error {
	src, err := m.source()
	if err != nil {
		return fmt.Errorf("Bestandsaufnahme fehlt: %v", err)
	}
	ok := map[string]bool{}
	for _, it := range src.Items() {
		if it.Selectable() {
			ok[it.Key] = true
		}
	}
	c := serverChoice{Chosen: []string{}}
	seen := map[string]bool{}
	for _, k := range keys {
		if !ok[k] {
			return fmt.Errorf("Unbekannter Punkt %q", k)
		}
		if !seen[k] {
			seen[k] = true
			c.Chosen = append(c.Chosen, k)
		}
	}
	sort.Strings(c.Chosen)
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	return outdir.WritePrivate(filepath.Join(m.Dir, serverChoiceFile), append(b, '\n'))
}

// ServerLevel ist die Liste für die Seite Umzug, ohne Inhalte.
func (m *serverMoves) ServerLevel(ctx context.Context) dienst.ServerLevel {
	rep := dienst.ServerLevel{Items: []dienst.ServerItem{}, Chosen: []string{}, Rest: []string{}}
	src, err := m.source()
	if err != nil {
		rep.Problem = "Ohne Bestandsaufnahme kennt HostDeck die Aufgaben und Zugänge dieses Servers nicht. Starte sie unter Übersicht."
		return rep
	}
	chosen := m.chosen()
	for _, it := range src.Items() {
		rep.Items = append(rep.Items, dienst.ServerItem{Key: it.Key, Kind: it.Kind, Title: it.Title, Detail: it.Detail, Manual: it.Manual, Selectable: it.Selectable()})
		if it.Selectable() && chosen[it.Key] {
			rep.Chosen = append(rep.Chosen, it.Key)
		}
	}
	rep.Rest = append(rep.Rest, src.Rest()...)
	return rep
}

// serverLevel lässt den neuen Server die Server-Ebene prüfen oder, am
// Umzugstag (apply), übernehmen. ok: ohne ✗.
func (m *serverMoves) serverLevel(ctx context.Context, client *ssh.Client, apply bool, in backup.Input, w io.Writer) (ok bool) {
	if m.Inventory == nil {
		return true
	}
	if apply {
		fmt.Fprintf(w, "\nServer-Ebene\n")
	} else {
		fmt.Fprintf(w, "\nServer-Ebene (Vorschau)\n")
	}
	src, err := m.source()
	if err != nil {
		fmt.Fprintf(w, "  ! Ohne Bestandsaufnahme kennt HostDeck die Aufgaben und Zugänge dieses Servers nicht (%v).\n", err)
		return false
	}
	items := src.Items()
	chosen := m.chosen()
	lv := serverebene.Choose(items, chosen, apply)
	open := 0
	for _, it := range items {
		if it.Selectable() && !chosen[it.Key] {
			open++
		}
	}
	if !apply {
		fmt.Fprintf(w, "  Beim Probe-Umzug ändert HostDeck dort nichts: Aufgaben liefen sonst doppelt. Am Umzugstag übernimmt es, was hier steht.\n")
	}
	switch {
	case open == 1 && apply:
		fmt.Fprintf(w, "  1 Aufgabe oder Zugang ist nicht angekreuzt und bleibt hier.\n")
	case open > 1 && apply:
		fmt.Fprintf(w, "  %d Aufgaben und Zugänge sind nicht angekreuzt und bleiben hier.\n", open)
	case open == 1:
		fmt.Fprintf(w, "  1 Aufgabe oder Zugang ist nicht angekreuzt; die Haken setzt du unter Umzug, Server-Ebene.\n")
	case open > 1:
		fmt.Fprintf(w, "  %d Aufgaben und Zugänge sind nicht angekreuzt; die Haken setzt du unter Umzug, Server-Ebene.\n", open)
	}
	ok = true
	if len(lv.Items) == 0 {
		fmt.Fprintf(w, "  Nichts weiter: keine eigene Zeitzone, keine Kernel-Einstellungen, nichts angekreuzt.\n")
	} else {
		cfg := &remote.Config{Action: remote.ActionServer, Server: &lv}
		lines := &lineWatch{w: w, needle: []byte("  ✗ ")}
		code, err := m.agent(ctx, client, moveServer, "", "", cfg, in, lines)
		switch {
		case err != nil && ctx.Err() != nil:
			fmt.Fprintln(w, "  Abgebrochen.")
			return false
		case err != nil:
			fmt.Fprintf(w, "  ✗ %v\n", err)
			return false
		}
		ok = code == restore.ExitOK && !lines.seen
	}
	if rest := src.Rest(); len(rest) > 0 {
		what := fmt.Sprintf("%d geänderte Dateien", len(rest))
		if len(rest) == 1 {
			what = "1 geänderte Datei"
		}
		fmt.Fprintf(w, "  Von Hand vergleichen: %s in /etc, die HostDeck nicht übernimmt, weil Systeme sie verschieden haben: %s\n", what, shortPaths(rest, 8))
	}
	return ok
}

func shortPaths(list []string, n int) string {
	if len(list) <= n {
		return strings.Join(list, ", ")
	}
	return strings.Join(list[:n], ", ") + fmt.Sprintf(" und %d weitere", len(list)-n)
}

// oldAddress sucht in Projektordnern und /etc nach der Adresse dieses
// Servers. Nach dem Umzug zeigt dort sonst etwas ins Leere.
func (m *serverMoves) oldAddress(ctx context.Context, target string, w io.Writer) {
	if m.Inventory == nil {
		return
	}
	inv, err := m.Inventory()
	if err != nil {
		return
	}
	local := m.LocalIPs
	if local == nil {
		local = hostIPs
	}
	var addrs []string
	for _, ip := range local() {
		if ip.IsGlobalUnicast() && !ip.IsPrivate() && ip.String() != target {
			addrs = append(addrs, ip.String())
		}
	}
	fmt.Fprintf(w, "\nAlte Adresse in Dateien\n")
	if len(addrs) == 0 {
		fmt.Fprintf(w, "  Dieser Server hat keine öffentliche Adresse an einer Schnittstelle; HostDeck sucht darum nicht.\n")
		return
	}
	dirs, skip := scanDirs(inv)
	hits, cut := serverebene.FindAddress(ctx, m.Root, dirs, skip, addrs, serverebene.DefaultLimits)
	if ctx.Err() != nil {
		return
	}
	if len(hits) == 0 {
		fmt.Fprintf(w, "  ✓ %s steht in keiner Textdatei der Projektordner und von /etc\n", strings.Join(addrs, ", "))
	} else {
		fmt.Fprintf(w, "  ! %s steht noch in %s. Nach dem Umzug zeigt das auf den alten Server; prüfe, ob dort die neue Adresse hingehört:\n", strings.Join(addrs, ", "), countFiles(hits))
		for i, h := range hits {
			if i == 30 {
				fmt.Fprintf(w, "    und %d weitere Stellen\n", len(hits)-i)
				break
			}
			fmt.Fprintf(w, "    %s, Zeile %d\n", h.Path, h.Line)
		}
	}
	if cut {
		fmt.Fprintf(w, "  Die Suche hat bei sehr vielen Dateien aufgehört; Spielstände und Uploads sind meist der Grund.\n")
	}
}

// hostIPs sind die Adressen der Schnittstellen mit der Standardroute,
// also die, über die der Server im Internet erreichbar ist. Netze von
// Docker, VPNs und Brücken fallen damit heraus, auch wenn sie Adressen aus
// öffentlichen Bereichen nutzen (auf vps1 172.32.0.1).
func hostIPs() []net.IP {
	routes := defaultRouteIfaces("/proc/net/route", "/proc/net/ipv6_route")
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, in := range ifs {
		if in.Flags&net.FlagLoopback != 0 || in.Flags&net.FlagUp == 0 || virtualIface(in.Name) {
			continue
		}
		if len(routes) > 0 && !routes[in.Name] {
			continue
		}
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.IsGlobalUnicast() {
				out = append(out, n.IP)
			}
		}
	}
	return out
}

// defaultRouteIfaces liest aus /proc, welche Schnittstellen eine
// Standardroute haben (IPv4: Ziel 00000000, Maske 00000000; IPv6: Ziel ::/0).
func defaultRouteIfaces(v4, v6 string) map[string]bool {
	out := map[string]bool{}
	if b, err := os.ReadFile(v4); err == nil {
		for _, line := range strings.Split(string(b), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) >= 8 && f[1] == "00000000" && f[7] == "00000000" {
				out[f[0]] = true
			}
		}
	}
	if b, err := os.ReadFile(v6); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 10 && f[0] == strings.Repeat("0", 32) && f[1] == "00" && f[9] != "lo" {
				out[f[9]] = true
			}
		}
	}
	return out
}

func virtualIface(name string) bool {
	for _, pre := range []string{"docker", "br-", "veth", "virbr", "vnet", "tailscale", "wg", "tun", "tap", "cni", "flannel", "cali", "lxc", "lxd", "podman", "zt", "kube", "vxlan", "nebula", "ham"} {
		if strings.HasPrefix(name, pre) {
			return true
		}
	}
	return false
}

func countFiles(hits []serverebene.Hit) string {
	files := map[string]bool{}
	for _, h := range hits {
		files[h.Path] = true
	}
	if len(files) == 1 {
		return "1 Datei"
	}
	return fmt.Sprintf("%d Dateien", len(files))
}

// scanDirs: Projektordner und Ordner der Projekte, ohne Datenbanken und
// Volumes; dazu /etc.
func scanDirs(inv *inventory.Inventory) (dirs, skip []string) {
	dirs = []string{"/etc"}
	// Netz und Cloud-Abbild gehören zur Maschine; der neue Server hat
	// eigene.
	skip = []string{"/var/lib/docker", "/etc/ssl", "/etc/alternatives", "/etc/netplan", "/etc/cloud", "/etc/network"}
	for _, p := range inv.Projects {
		if p.DirExists && p.WorkingDir != "" {
			dirs = append(dirs, p.WorkingDir)
		}
	}
	for _, p := range inv.Paths {
		switch {
		case p.DB != nil:
			skip = append(skip, p.Path)
		case p.Kind == inventory.KindParent, len(p.Mounts) == 0:
			// Elternordner und Ordner, die kein Container nutzt, kommen
			// meist nur ins Archiv, etwa alte Messergebnisse.
		case p.Exists && p.IsDir && !strings.HasPrefix(p.Path, "/var/lib/docker/"):
			dirs = append(dirs, p.Path)
		}
	}
	return dirs, skip
}

// serverLevelCmd prüft oder übernimmt auf dem neuen Server die Server-Ebene.
func serverLevelCmd(ctx context.Context, cfg remote.Config, w io.Writer) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(w, "  ✗ Dafür braucht HostDeck root.")
		return restore.ExitBlocked
	}
	if cfg.Server == nil {
		fmt.Fprintln(w, "  ✗ Keine Punkte bekommen.")
		return restore.ExitBlocked
	}
	if cfg.Server.Apply {
		lock, ok := takeLauf("Server-Ebene übernehmen", w)
		if !ok {
			return restore.ExitBlocked
		}
		defer lock.Release()
	}
	t := &serverebene.Target{Run: runCombined}
	res := t.Do(ctx, *cfg.Server, w)
	switch {
	case ctx.Err() != nil:
		return restore.ExitFailed
	case res.Warn > 0:
		fmt.Fprintf(w, "  %d Punkte zum Nachsehen (siehe !).\n", res.Warn)
	}
	return restore.ExitOK
}
