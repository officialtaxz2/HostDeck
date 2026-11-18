//go:build unix

package domains

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
)

// Der Dienst läuft mit UMask 0077. Die Ordner, die HostDeck für Caddy anlegt,
// muss Caddy (eigener Benutzer) trotzdem lesen können, sonst findet
// import sites/*.caddy nichts und die Domains fehlen.
func TestOrdnenOrdnerFuerCaddyLesbar(t *testing.T) {
	e := setup(t)
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	ctx := context.Background()
	p, err := e.s.ReorgPlan(ctx, nil)
	if err != nil || p.Problem != "" {
		t.Fatalf("Plan: %v %s", err, p.Problem)
	}
	if _, err := e.s.Reorg(ctx, nil, p.Stand, nil); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{SitesDir, filepath.Join(SitesDir, OffDir)} {
		fi, err := os.Stat(filepath.Join(e.dir, d))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: %v, erwartet 0755", d, fi.Mode().Perm())
		}
	}
	files, _ := filepath.Glob(filepath.Join(e.dir, SitesDir, "*.caddy"))
	off, _ := filepath.Glob(filepath.Join(e.dir, SitesDir, OffDir, "*.caddy"))
	for _, f := range append(files, off...) {
		fi, _ := os.Stat(f)
		if fi.Mode().Perm()&0o044 != 0o044 {
			t.Errorf("%s: %v, Caddy kann sie nicht lesen", f, fi.Mode().Perm())
		}
	}
	if len(files) == 0 || len(off) == 0 {
		t.Errorf("Dateien: %v, aus: %v", files, off)
	}
}

// Ein sites/ mit 0700 aus einem früheren Lauf (vor dem Fix) richtet HostDeck
// beim nächsten Ordnen; scheitert das Ordnen, bleiben die alten Rechte.
func TestOrdnenRichtetVerschlossenenOrdner(t *testing.T) {
	for _, fail := range []bool{false, true} {
		e := setup(t)
		ctx := context.Background()
		sites := filepath.Join(e.dir, SitesDir)
		if err := os.Mkdir(sites, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sites, 0o700); err != nil {
			t.Fatal(err)
		}
		p, err := e.s.ReorgPlan(ctx, nil)
		if err != nil || p.Problem != "" {
			t.Fatalf("Plan: %v %s", err, p.Problem)
		}
		if !strings.Contains(strings.Join(p.Steps, "\n"), "Rechte: "+sites) {
			t.Errorf("Vorschau nennt die Rechte nicht: %q", p.Steps)
		}
		want := os.FileMode(0o755)
		if fail {
			want = 0o700
			e.s.Probe = func(ctx context.Context, host string) caddy.Answer {
				e.f.mu.Lock()
				defer e.f.mu.Unlock()
				if e.f.reloads == 1 && host == "work.example.org" {
					return caddy.Answer{Status: 502}
				}
				return caddy.Answer{Status: 200}
			}
		}
		_, err = e.s.Reorg(ctx, nil, p.Stand, nil)
		if fail != (err != nil) {
			t.Fatalf("fail=%v: %v", fail, err)
		}
		fi, err := os.Stat(sites)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("fail=%v: sites/ %v, erwartet %v", fail, fi.Mode().Perm(), want)
		}
	}
}

// Auch eine einzelne Änderung richtet ein verschlossenes sites/.
func TestAendernRichtetVerschlossenenOrdner(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	p, _ := e.s.ReorgPlan(ctx, nil)
	if _, err := e.s.Reorg(ctx, nil, p.Stand, nil); err != nil {
		t.Fatal(err)
	}
	sites := filepath.Join(e.dir, SitesDir)
	if err := os.Chmod(sites, 0o700); err != nil {
		t.Fatal(err)
	}
	req := ChangeReq{Action: ActNew, Host: "neu.example.org", Target: "4000"}
	cp, err := e.s.ChangePlan(ctx, req)
	if err != nil || cp.Problem != "" {
		t.Fatalf("Plan: %v %+v", err, cp)
	}
	if len(cp.Steps) == 0 || !strings.HasPrefix(cp.Steps[0], "Rechte: ") {
		t.Errorf("Vorschau nennt die Rechte nicht zuerst: %q", cp.Steps)
	}
	if _, err := e.s.Change(ctx, req, cp.Stand, nil); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(sites); fi.Mode().Perm() != 0o755 {
		t.Errorf("sites/ %v, erwartet 0755", fi.Mode().Perm())
	}
}
