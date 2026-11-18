package dienst

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

func modeRig(t *testing.T, set func(d *Dienst)) (*rig, string) {
	t.Helper()
	dir := t.TempDir()
	r, _ := doorRigWith(t, nil, func(d *Dienst) {
		d.ModeFile = filepath.Join(dir, "umzug", "modus.json")
		d.ProbeFile = filepath.Join(dir, "umzug", "probe-ziel.json")
		if set != nil {
			set(d)
		}
	})
	ctx := context.Background()
	if err := r.c.SetPassword(ctx, testPW); err != nil {
		t.Fatal(err)
	}
	sid, err := r.c.Door().Login(ctx, testPW, web.Visitor{Gate: "Test", Addr: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	return r, sid
}

func TestProbeKopieMerken(t *testing.T) {
	r, _ := modeRig(t, nil)
	d := r.d
	tg := MoveTarget{Host: "198.51.100.20", User: "root", Password: "geheim"}
	d.noteProbe(MoveRun, tg)
	p := d.probeCopy()
	if p == nil || p.Host != "198.51.100.20" || p.Halted || strings.Contains(p.Target, "geheim") {
		t.Fatalf("nach Probe-Umzug: %+v", p)
	}
	d.noteProbe(MoveHalt, MoveTarget{Host: "203.0.113.1"})
	if p := d.probeCopy(); p == nil || p.Halted {
		t.Fatalf("anderer Server angehalten: %+v", p)
	}
	d.noteProbe(MoveHalt, tg)
	if p := d.probeCopy(); p == nil || !p.Halted {
		t.Fatalf("nach Anhalten: %+v", p)
	}
	d.noteProbe(MoveRemove, tg)
	if p := d.probeCopy(); p != nil {
		t.Fatalf("nach Entfernen: %+v", p)
	}
	d.noteProbe(MoveRun, tg)
	d.noteProbe(MoveDay, tg)
	if p := d.probeCopy(); p != nil {
		t.Fatalf("nach dem Umzugstag: %+v", p)
	}
}

func TestKeinUmzugTrotzProbeKopie(t *testing.T) {
	r, sid := modeRig(t, nil)
	r.d.noteProbe(MoveRun, MoveTarget{Host: "198.51.100.20", User: "root"})
	code, b := r.api(t, "GET", "/api/lauf", sid, "")
	var st Status
	if err := json.Unmarshal([]byte(b), &st); code != 200 || err != nil || st.ProbeKopie == nil || st.ProbeKopie.Host != "198.51.100.20" {
		t.Fatalf("Status: %d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/umzug/modus", sid, `{"geplant":false}`); code != http.StatusConflict || !strings.Contains(b, "Probe-Kopie") {
		t.Fatalf("ohne trotzdem: %d %s", code, b)
	}
	if r.d.everyday() {
		t.Fatal("trotz Sperre umgeschaltet")
	}
	if code, b := r.api(t, "POST", "/api/umzug/modus", sid, `{"geplant":false,"trotzdem":true}`); code != http.StatusOK {
		t.Fatalf("trotzdem: %d %s", code, b)
	}
	// Angehalten braucht kein trotzdem.
	r.d.noteProbe(MoveHalt, MoveTarget{Host: "198.51.100.20"})
	setMode(t, r, sid, true)
	setMode(t, r, sid, false)
}

func TestKeinUmzugMittenImUmzug(t *testing.T) {
	r, sid := modeRig(t, nil)
	l, err := lauf.TakeIn(r.d.RunDir, "Umzugstag auf root@198.51.100.20")
	if err != nil {
		t.Fatal(err)
	}
	if code, b := r.api(t, "POST", "/api/umzug/modus", sid, `{"geplant":false,"trotzdem":true}`); code != http.StatusConflict || !strings.Contains(b, "Umzugstag") {
		t.Errorf("während des Umzugstags: %d %s", code, b)
	}
	// Umzug planen geht immer.
	if code, b := r.api(t, "POST", "/api/umzug/modus", sid, `{"geplant":true}`); code != http.StatusOK {
		t.Errorf("planen: %d %s", code, b)
	}
	l.Release()
	setMode(t, r, sid, false)
}

func TestKeinUmzugAufProbeKopie(t *testing.T) {
	r, sid := modeRig(t, func(d *Dienst) { d.Copy = func() *CopyInfo { return &CopyInfo{From: "vps1"} } })
	if code, b := r.api(t, "POST", "/api/umzug/modus", sid, `{"geplant":false}`); code != http.StatusConflict {
		t.Errorf("auf der Probe-Kopie: %d %s", code, b)
	}
}
