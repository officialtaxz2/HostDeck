package main

// Umstieg von sbm auf HostDeck.
//
// Bis zur Version 1.0 hieß HostDeck „sbm“: Programm in /usr/local/lib/sbm,
// Daten in /var/lib/sbm und /root/.sbm, Dienste sbm-dienst und sbm-web
// (Benutzer sbm-web), Eingang für Caddy in sbm.caddy, Port-Schutz in der
// Tabelle sbm_schutz. Einrichten und Update erkennen eine solche
// Installation und ziehen sie einmal um. Daten, Passwörter, Backups und die
// Freischaltung bleiben dabei, wie sie sind; es ändern sich nur Namen.
//
// Antwortet die neue Version nicht oder lässt sich Caddy nicht umstellen,
// legt der Umstieg alles zurück, und sbm läuft weiter wie vorher.
//
// Diese Datei kann weg, sobald keine Installation von sbm mehr läuft.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/serverebene"
	"github.com/officialtaxz2/hostdeck/internal/sicherheit"
)

// legacyPaths sind die Orte und Namen von sbm. Felder für Tests.
type legacyPaths struct {
	ProgDir, BinLink, DataDir, RunDir, Socket, WebUser string
	UnitDienst, UnitWeb, UnitSocket, ShieldUnit        string
	Table, CaddySite, ImportComment, CaddyBackupDir    string
	StateDir                                           string // Ordner im Home von root
	// Files ziehen um, wenn es sie gibt: vom Umzug angelegte Dateien in
	// /etc (alter Name, neuer Name).
	Files [][2]string
}

var legacy = legacyPaths{
	ProgDir: "/usr/local/lib/sbm", BinLink: "/usr/local/bin/sbm", DataDir: "/var/lib/sbm",
	RunDir: "/run/sbm", Socket: "/run/sbm/dienst.sock", WebUser: "sbm-web",
	UnitDienst: "sbm-dienst.service", UnitWeb: "sbm-web.service", UnitSocket: "sbm-web.socket", ShieldUnit: "sbm-schutz.service",
	Table: "sbm_schutz", CaddySite: "sbm.caddy", CaddyBackupDir: "sbm-sicherung", StateDir: ".sbm",
	ImportComment: "# Leitstand von sbm; zurücknehmen mit: sudo sbm freischalten --zuruecknehmen",
	Files: [][2]string{
		{"/etc/sysctl.d/90-sbm-umzug.conf", serverebene.SysctlFile},
		{"/etc/cron.d/sbm-crontab", serverebene.CrontabFile},
	},
}

type umstieg struct {
	in          *installer
	old         legacyPaths
	home        string // Home von root; .sbm wird dort zu .hostdeck
	GroupExists func(name string) bool
	// Nft führt nft und systemctl für den Port-Schutz aus.
	Nft sicherheit.Runner
	// FindCaddy sucht Caddy für die Vermerke im Caddyfile.
	FindCaddy func(ctx context.Context) (*caddy.Caddy, error)

	units   []string // Units von sbm, die es gab
	newProg bool     // ProgDir von HostDeck gab es vorher nicht
	undo    []func(ctx context.Context) error
	lock    *lauf.Lock
	notes   []string // für das Protokoll
}

// findUmstieg sagt, ob hier noch sbm eingerichtet ist; nil: nein.
func findUmstieg(in *installer) *umstieg {
	home := "/root"
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		home = u.HomeDir
	}
	return findUmstiegWith(in, legacy, home)
}

func findUmstiegWith(in *installer, old legacyPaths, home string) *umstieg {
	unit := exists(filepath.Join(in.UnitDir, old.UnitDienst))
	data := exists(old.DataDir) && emptyOrMissing(in.DataDir)
	if !unit && !data {
		return nil
	}
	return &umstieg{
		in: in, old: old, home: home,
		GroupExists: func(n string) bool { _, err := user.LookupGroup(n); return err == nil },
		Nft:         runCombined,
		FindCaddy:   func(ctx context.Context) (*caddy.Caddy, error) { return caddy.Find(ctx, caddy.Exec) },
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// emptyOrMissing: Den Ordner gibt es nicht, oder er ist leer (systemd legt
// ihn für StateDirectory= schon beim ersten Start an).
func emptyOrMissing(dir string) bool {
	es, err := os.ReadDir(dir)
	return errors.Is(err, fs.ErrNotExist) || (err == nil && len(es) == 0)
}

func (u *umstieg) push(f func(ctx context.Context) error) { u.undo = append(u.undo, f) }

// move benennt um und merkt sich den Rückweg.
func (u *umstieg) move(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	u.in.printf("  %s → %s\n", from, to)
	u.push(func(context.Context) error { return os.Rename(to, from) })
	return nil
}

// run zieht sbm um und bringt HostDeck in Betrieb. running fragt die
// Version des neuen Dienstes ab. Geht etwas schief, ist danach alles wie
// vorher.
func (u *umstieg) run(ctx context.Context, pub *publisher, running func(ctx context.Context) (string, error), wait time.Duration, addr string) error {
	before := u.oldVersion(ctx)
	fail := func(why error) error {
		u.in.printf("Umstieg fehlgeschlagen: %v\nLege alles zurück, sbm läuft gleich wieder …\n", why)
		// Eigener Kontext: Auch nach Strg+C muss der Rückweg durchlaufen.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer cancel()
		err := u.rollback(rctx)
		_ = os.Remove(u.in.Exe + headSuffix)
		dir := u.old.DataDir
		if !exists(dir) {
			dir = u.in.DataDir
		}
		text := fmt.Sprintf("Umstieg auf HostDeck %s fehlgeschlagen (%v); sbm %s läuft wieder", version, why, before)
		if err != nil {
			text = fmt.Sprintf("Umstieg auf HostDeck %s fehlgeschlagen (%v); Zurücklegen unvollständig: %v", version, why, err)
		}
		_ = (&protokoll.Log{File: filepath.Join(dir, ort.JournalFile)}).Add(protokoll.Entry{Kind: protokoll.Update, Text: text, Addr: addr})
		return errors.New(text)
	}
	if err := u.prepare(ctx); err != nil {
		return fail(err)
	}
	if err := u.in.install(ctx); err != nil {
		return fail(err)
	}
	if err := waitForVersion(ctx, running, wait); err != nil {
		return fail(err)
	}
	if err := u.caddy(ctx, pub); err != nil {
		return fail(fmt.Errorf("Caddy ließ sich nicht auf HostDeck umstellen: %v", err))
	}
	notes := append(u.notes, u.cleanup(ctx)...)
	text := fmt.Sprintf("Umstieg von sbm %s auf HostDeck %s: Programm, Daten, Dienste und Benutzer heißen jetzt hostdeck", before, version)
	if len(notes) > 0 {
		text += ". " + strings.Join(notes, " ")
	}
	_ = (&protokoll.Log{File: filepath.Join(u.in.DataDir, ort.JournalFile)}).Add(protokoll.Entry{Kind: protokoll.Update, Text: text, Addr: addr})
	u.in.printf("%s\n", text)
	return nil
}

func (u *umstieg) oldVersion(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := dienst.NewClient(u.old.Socket).Status(ctx)
	if err != nil || st.Version == "" {
		return "(Version unbekannt)"
	}
	return st.Version
}

// prepare hält sbm an und benennt um. Jeder Schritt merkt sich seinen
// Rückweg.
func (u *umstieg) prepare(ctx context.Context) error {
	in := u.in
	in.printf("Umstieg von sbm auf HostDeck …\n")
	// Kein Lauf von sbm darf mittendrin sein.
	l, ok := waitForLaufIn(ctx, u.old.RunDir, "Umstieg auf HostDeck", in.Out)
	if !ok {
		return errors.New("die Laufsperre von sbm ist nicht frei")
	}
	u.lock = l
	if !emptyOrMissing(in.DataDir) && exists(u.old.DataDir) {
		return fmt.Errorf("%s und %s gibt es beide; ich ändere nichts", u.old.DataDir, in.DataDir)
	}
	if st, _ := loadAccess(u.old.DataDir); st != nil && filepath.Base(st.Site) == u.old.CaddySite {
		if p := filepath.Join(filepath.Dir(st.Site), ort.CaddySite); exists(p) {
			return fmt.Errorf("%s gibt es schon; ich ändere nichts", p)
		}
	}
	u.newProg = !exists(in.ProgDir)
	for _, n := range []string{u.old.UnitSocket, u.old.UnitWeb, u.old.UnitDienst} {
		if exists(filepath.Join(in.UnitDir, n)) {
			u.units = append(u.units, n)
		}
	}
	if len(u.units) > 0 {
		in.printf("  Halte sbm an (%s) …\n", strings.Join(u.units, ", "))
		u.push(func(ctx context.Context) error {
			if err := in.Run(ctx, "systemctl", "daemon-reload"); err != nil {
				return err
			}
			return in.Run(ctx, "systemctl", append([]string{"enable", "--now"}, u.units...)...)
		})
		if err := in.Run(ctx, "systemctl", append([]string{"disable", "--now"}, u.units...)...); err != nil {
			return err
		}
	}
	if exists(u.old.DataDir) {
		if exists(in.DataDir) {
			if err := os.Remove(in.DataDir); err != nil {
				return err
			}
		}
		if err := u.move(u.old.DataDir, in.DataDir); err != nil {
			return err
		}
		// Beim Update liegt die neue Datei selbst im alten Datenordner.
		if rel, ok := strings.CutPrefix(in.Exe, u.old.DataDir+"/"); ok {
			was := in.Exe
			in.Exe = filepath.Join(in.DataDir, rel)
			u.push(func(context.Context) error { in.Exe = was; return nil })
		}
	}
	if u.home != "" {
		from, to := filepath.Join(u.home, u.old.StateDir), filepath.Join(u.home, ".hostdeck")
		if exists(from) && !exists(to) {
			if err := u.move(from, to); err != nil {
				return err
			}
		}
	}
	if in.UserExists(u.old.WebUser) && !in.UserExists(in.WebUser) {
		if err := in.Run(ctx, "usermod", "-l", in.WebUser, u.old.WebUser); err != nil {
			return err
		}
		in.printf("  Benutzer %s → %s\n", u.old.WebUser, in.WebUser)
		u.push(func(ctx context.Context) error { return in.Run(ctx, "usermod", "-l", u.old.WebUser, in.WebUser) })
		if u.GroupExists(u.old.WebUser) && !u.GroupExists(in.WebUser) {
			if err := in.Run(ctx, "groupmod", "-n", in.WebUser, u.old.WebUser); err != nil {
				return err
			}
			u.push(func(ctx context.Context) error { return in.Run(ctx, "groupmod", "-n", u.old.WebUser, in.WebUser) })
		}
	}
	for _, f := range u.old.Files {
		if exists(f[0]) && !exists(f[1]) {
			if err := u.move(f[0], f[1]); err != nil {
				return err
			}
		}
	}
	// Port-Schutz: die eigene Tabelle laden, bevor der neue Dienst startet;
	// die alte bleibt, bis alles geklappt hat.
	shield := u.shield()
	rulesBefore, rerr := os.ReadFile(shield.RuleFile)
	u.push(func(ctx context.Context) error {
		err := shield.Drop(ctx)
		if rerr == nil {
			if werr := os.WriteFile(shield.RuleFile, rulesBefore, 0o600); err == nil {
				err = werr
			}
		}
		return err
	})
	msg, err := shield.Adopt(ctx)
	if err != nil {
		return fmt.Errorf("Port-Schutz: %v", err)
	}
	if msg != "" {
		u.notes = append(u.notes, msg)
	}
	return nil
}

func (u *umstieg) shield() *sicherheit.Service {
	return &sicherheit.Service{
		Run:       u.Nft,
		RuleFile:  filepath.Join(u.in.DataDir, ort.ShieldRules),
		StateFile: filepath.Join(u.in.DataDir, ort.ShieldFile),
		UnitFile:  filepath.Join(u.in.UnitDir, ort.ShieldUnit),
	}
}

// rollback nimmt HostDeck wieder heraus und legt alles zurück, wie es bei
// sbm war; zuletzt startet sbm wieder.
func (u *umstieg) rollback(ctx context.Context) error {
	in := u.in
	var errs []string
	var present []string
	for _, n := range []string{ort.UnitSocket, ort.UnitWeb, ort.UnitDienst} {
		if exists(filepath.Join(in.UnitDir, n)) {
			present = append(present, n)
		}
	}
	if len(present) > 0 {
		_ = in.Run(ctx, "systemctl", append([]string{"disable", "--now"}, present...)...)
		for _, n := range present {
			if err := os.Remove(filepath.Join(in.UnitDir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err.Error())
			}
		}
		_ = in.Run(ctx, "systemctl", "daemon-reload")
	}
	if t, err := os.Readlink(in.BinLink); err == nil && t == in.prog() {
		_ = os.Remove(in.BinLink)
	}
	if u.newProg {
		_ = os.RemoveAll(in.ProgDir)
	}
	for i := len(u.undo) - 1; i >= 0; i-- {
		if err := u.undo[i](ctx); err != nil {
			errs = append(errs, err.Error())
		}
	}
	u.undo = nil
	_ = os.Remove(in.DataDir) // nur, wenn leer (systemd legt ihn an)
	u.release()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (u *umstieg) release() {
	if u.lock != nil {
		u.lock.Release()
		u.lock = nil
	}
}

// caddy stellt einen freigeschalteten Leitstand um: sbm.caddy wird zu
// hostdeck.caddy (mit dem neuen Eingang), die import-Zeile im Caddyfile
// zeigt darauf. Mit derselben Prüfung wie beim Freischalten; geht sie
// schief, liegen die alten Dateien wieder da.
func (u *umstieg) caddy(ctx context.Context, p *publisher) error {
	st, err := loadAccess(u.in.DataDir)
	if err != nil || st == nil || filepath.Base(st.Site) != u.old.CaddySite {
		return err
	}
	oldSite := st.Site
	newSite := filepath.Join(filepath.Dir(oldSite), ort.CaddySite)
	if exists(newSite) {
		return fmt.Errorf("%s gibt es schon", newSite)
	}
	oldData, err := os.ReadFile(oldSite)
	if err != nil {
		return err
	}
	cf, err := os.ReadFile(st.Caddyfile)
	if err != nil {
		return err
	}
	newCF, imp := u.caddyfile(cf, oldSite, newSite, filepath.Dir(st.Caddyfile), st.Import)
	c, err := p.Find(ctx)
	if err != nil {
		return fmt.Errorf("Caddy ist gerade nicht erreichbar (%v)", err)
	}
	u.in.printf("  Caddy: %s → %s …\n", oldSite, newSite)
	var failed []string
	a := p.applier(c, func(s dienst.Step) {
		if s.State == caddy.StateFailed && s.Detail != "" {
			failed = append(failed, s.Name+": "+s.Detail)
		}
	})
	a.Journal, a.Note = p.journalPath(), "umstieg"
	ch := caddy.Change{
		Files: []caddy.File{
			{Path: st.Caddyfile, Data: newCF, Was: cf},
			{Path: newSite, Data: siteBlock(st.Domain, st.Extra...)},
			{Path: oldSite, Data: nil, Was: oldData},
		},
		Skip:  append(append([]string{st.Domain}, st.Extra...), blockHosts(oldData)...),
		Check: func(ctx context.Context) error { return p.waitSite(ctx, st.Domain, func(string, string, string) {}) },
	}
	if err := a.Apply(ctx, ch); err != nil {
		var undone *caddy.ErrUndone
		if errors.As(err, &undone) {
			_ = caddy.ClearJournal(a.Journal)
		}
		if f := strings.Join(failed, "; "); f != "" && !strings.Contains(err.Error(), f) {
			err = fmt.Errorf("%v (%s)", err, f)
		}
		return err
	}
	st.Site, st.Import = newSite, imp
	if err := saveAccess(u.in.DataDir, st); err != nil {
		return err
	}
	return caddy.ClearJournal(a.Journal)
}

// caddyfile setzt die import-Zeile auf die neue Datei, am selben Platz,
// und benennt die Vermerke von sbm in Kommentaren um. imp: Die import-Zeile
// stammt von HostDeck.
func (u *umstieg) caddyfile(cf []byte, oldSite, newSite, dir string, imp bool) ([]byte, bool) {
	out := cf
	oldBlock := []byte(u.old.ImportComment + "\nimport " + oldSite + "\n")
	if bytes.Contains(out, oldBlock) {
		out = bytes.Replace(out, oldBlock, []byte(caddy.ImportComment+"\nimport "+newSite+"\n"), 1)
	} else {
		for _, t := range []string{oldSite, filepath.Base(oldSite)} {
			if r, ok := caddy.RemoveImport(out, t); ok {
				out = r
			}
		}
		if !caddy.Covered(out, dir, newSite) {
			out, imp = caddy.AddImport(out, newSite), true
		}
	}
	return u.comments(out), imp
}

// comments benennt in Kommentarzeilen des Caddyfiles die Vermerke von sbm
// um (Ordnen der Domains, Ordner mit den alten Fassungen).
func (u *umstieg) comments(cf []byte) []byte {
	lines := strings.Split(string(cf), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		l = strings.ReplaceAll(l, "Von sbm geordnet", "Von HostDeck geordnet")
		lines[i] = strings.ReplaceAll(l, u.old.CaddyBackupDir+"/", domains.BackupDir+"/")
	}
	return []byte(strings.Join(lines, "\n"))
}

// cleanup räumt nach geglücktem Umstieg auf: Port-Schutz, alte Dienste,
// altes Programm, Vermerke bei Caddy. Fehler hier legen nichts zurück,
// HostDeck läuft ja schon; sie stehen in notes.
func (u *umstieg) cleanup(ctx context.Context) (notes []string) {
	in := u.in
	defer u.release()
	if err := u.shield().DropOld(ctx, u.old.Table, filepath.Join(in.UnitDir, u.old.ShieldUnit)); err != nil {
		notes = append(notes, "Die alte Tabelle von sbm ließ sich nicht entfernen ("+err.Error()+"); sie schadet nicht, schließt dieselben Ports.")
	}
	for _, n := range u.units {
		if err := os.Remove(filepath.Join(in.UnitDir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			notes = append(notes, err.Error())
		}
	}
	_ = in.Run(ctx, "systemctl", "daemon-reload")
	if t, err := os.Readlink(u.old.BinLink); err == nil && strings.HasPrefix(t, u.old.ProgDir+"/") {
		_ = os.Remove(u.old.BinLink)
	}
	if err := os.RemoveAll(u.old.ProgDir); err != nil {
		notes = append(notes, err.Error())
	}
	_ = os.RemoveAll(u.old.RunDir)
	notes = append(notes, u.caddyNotes(ctx)...)
	return notes
}

// caddyNotes benennt die Vermerke von sbm bei Caddy um, die nichts an der
// Konfiguration ändern: Kommentare im Caddyfile, den Ordner mit alten
// Fassungen und die erste Zeile selbst angelegter Domain-Dateien.
func (u *umstieg) caddyNotes(ctx context.Context) (notes []string) {
	c, err := u.FindCaddy(ctx)
	if err != nil {
		return nil
	}
	cfg := c.Config
	if b, err := os.ReadFile(cfg); err == nil {
		if nb := u.comments(b); !bytes.Equal(nb, b) {
			if fi, err := os.Stat(cfg); err == nil {
				tmp := cfg + ".hostdeck-neu"
				if err := os.WriteFile(tmp, nb, fi.Mode().Perm()); err == nil {
					_ = os.Chmod(tmp, fi.Mode().Perm())
					if err := os.Rename(tmp, cfg); err != nil {
						os.Remove(tmp)
						notes = append(notes, err.Error())
					}
				}
			}
		}
	}
	dir := filepath.Dir(cfg)
	if from, to := filepath.Join(dir, u.old.CaddyBackupDir), filepath.Join(dir, domains.BackupDir); exists(from) && !exists(to) {
		if err := os.Rename(from, to); err != nil {
			notes = append(notes, err.Error())
		}
	}
	ds := &domains.Service{StateFile: filepath.Join(u.in.DataDir, ort.DomainsFile)}
	if _, err := ds.Retitle("angelegt von sbm.", "angelegt von HostDeck."); err != nil {
		notes = append(notes, err.Error())
	}
	return notes
}

// waitForVersion wartet, bis der Dienst mit dieser Version antwortet.
func waitForVersion(ctx context.Context, running func(ctx context.Context) (string, error), wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		v, err := running(ctx)
		if err == nil && v == version {
			return nil
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = fmt.Errorf("der Dienst meldet Version %s", v)
			}
			return fmt.Errorf("neue Version antwortet nicht: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
