package backup_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/restic"
)

// log sammelt, was Docker und restic in welcher Reihenfolge tun.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func (l *eventLog) index(e string) int { return slices.Index(l.list(), e) }

type fakeDocker struct {
	log     *eventLog
	mu      sync.Mutex
	running map[string]bool // nach ID oder Name
	// stopAfterPolls schaltet verwaltete Server nach so vielen Abfragen aus.
	stopAfterPolls int
	polls          int
}

func (d *fakeDocker) State(_ context.Context, id string) (dockerapi.ContainerState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return dockerapi.ContainerState{Running: d.running[id]}, nil
}

func (d *fakeDocker) Stop(_ context.Context, id string, _ time.Duration) error {
	d.mu.Lock()
	d.running[id] = false
	d.mu.Unlock()
	d.log.add("stop " + id)
	return nil
}

func (d *fakeDocker) Start(_ context.Context, id string) error {
	d.mu.Lock()
	d.running[id] = true
	d.mu.Unlock()
	d.log.add("start " + id)
	return nil
}

func (d *fakeDocker) Running(_ context.Context, names []string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.polls++
	if d.stopAfterPolls > 0 && d.polls > d.stopAfterPolls {
		for _, n := range names {
			d.running[n] = false
		}
	}
	var out []string
	for _, n := range names {
		if d.running[n] {
			out = append(out, n)
		}
	}
	return out, nil
}

type fakeStore struct {
	log    *eventLog
	failOn string
	n      int
}

func (s *fakeStore) Backup(ctx context.Context, a restic.BackupArgs) (restic.Result, error) {
	if err := ctx.Err(); err != nil {
		return restic.Result{}, err
	}
	var key, phase string
	for _, t := range a.Tags {
		if k, ok := strings.CutPrefix(t, "sbm-key="); ok {
			key = k
		}
		if t == "sbm-vorlauf" {
			phase = " vorlauf"
		}
	}
	s.log.add("backup " + key + phase)
	if s.failOn != "" && key == s.failOn {
		return restic.Result{}, errors.New("Verbindung zum PC weg")
	}
	s.n++
	return restic.Result{SnapshotID: strings.Repeat("a", 60) + string(rune('a'+s.n%26)), TotalBytes: 100, DataAdded: 10}, nil
}

type fakeInput struct {
	answers []string
	asked   []string
	notes   []string
}

func (in *fakeInput) Ask(ctx context.Context, q backup.Question) (string, error) {
	in.asked = append(in.asked, q.Text)
	in.notes = append(in.notes, q.Note)
	if len(in.answers) > 0 {
		a := in.answers[0]
		in.answers = in.answers[1:]
		return a, nil
	}
	<-ctx.Done()
	return "", ctx.Err()
}

// smallPlan: ein Projekt mit App, Datenbank und gestopptem Daten-Ordner,
// eines nur mit Dump, ein Panel mit verwaltetem Server und ein Image.
func smallPlan(final bool) *backup.Plan {
	return &backup.Plan{
		Hostname: "test", Final: final,
		Groups: []backup.Group{
			{Name: "shop", Decision: "take",
				Containers: []backup.Ctr{{ID: "shop-db", Name: "shop-db", Running: true, DB: true}, {ID: "shop-app", Name: "shop-app", Running: true}, {ID: "shop-alt", Name: "shop-alt"}},
				Dumps:      []backup.Item{{Key: "volume:shop_db", Title: "Volume shop_db", Strategy: backup.StratDump, Engine: "postgres", Container: "shop-db", Filename: "dumps/shop/db.sql"}},
				Stopped:    []backup.Item{{Key: "project:shop", Title: "Projektordner /opt/shop", Strategy: backup.StratFolder, Paths: []string{"/opt/shop"}}, {Key: "path:/opt/shop/data", Title: "/opt/shop/data", Strategy: backup.StratStop, Paths: []string{"/opt/shop/data"}}},
			},
			{Name: "blog", Decision: "take",
				Containers: []backup.Ctr{{ID: "blog-db", Name: "blog-db", Running: true, DB: true}},
				Live:       []backup.Item{{Key: "project:blog", Title: "Projektordner /opt/blog", Strategy: backup.StratFolder, Paths: []string{"/opt/blog"}}},
				Dumps:      []backup.Item{{Key: "volume:blog_db", Title: "Volume blog_db", Strategy: backup.StratDump, Engine: "mysql", Container: "blog-db", Filename: "dumps/blog/db.sql"}},
			},
			{Name: "panel", Decision: "take",
				Containers: []backup.Ctr{{ID: "panel-web", Name: "panel-web", Running: true}},
				Managed:    []backup.ManagedRef{{Key: "managed:spiel", Name: "spiel", Label: "Survival"}},
				Stopped:    []backup.Item{{Key: "path:/opt/panel/wings", Title: "/opt/panel/wings", Strategy: backup.StratStop, Paths: []string{"/opt/panel/wings"}}},
			},
		},
		Archive: []backup.Item{{Key: "path:/etc", Title: "/etc", Strategy: backup.StratArchive, Paths: []string{"/etc"}, Archive: true}},
		Images:  []backup.Item{{Key: "image:sha256:1", Title: "Image shop:lokal", Strategy: backup.StratImage, ImageID: "sha256:1", Refs: []string{"shop:lokal"}, Filename: "images/1.tar"}},
	}
}

func newRunner(t *testing.T, p *backup.Plan, d backup.Docker, s backup.Store, in *fakeInput) *backup.Runner {
	t.Helper()
	st, err := backup.LoadStopState(filepath.Join(t.TempDir(), "gestoppt.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &backup.Runner{Plan: p, RunID: "20261101-120000", Docker: d, Store: s, Input: in, State: st, Out: io.Discard,
		Self: []string{"hostdeck"}, Settle: time.Millisecond, StartWait: 50 * time.Millisecond, Poll: 5 * time.Millisecond}
}

func running() map[string]bool {
	return map[string]bool{"shop-db": true, "shop-app": true, "blog-db": true, "panel-web": true}
}

func before(t *testing.T, l *eventLog, a, b string) {
	t.Helper()
	ia, ib := l.index(a), l.index(b)
	if ia < 0 || ib < 0 || ia >= ib {
		t.Errorf("%q sollte vor %q kommen:\n%s", a, b, strings.Join(l.list(), "\n"))
	}
}

func TestLaufNormal(t *testing.T) {
	l := &eventLog{}
	d := &fakeDocker{log: l, running: running()}
	s := &fakeStore{log: l}
	r := newRunner(t, smallPlan(false), d, s, &fakeInput{})
	res := r.Run(context.Background())
	if !res.OK() {
		t.Fatalf("Lauf gescheitert: %s", res.Err)
	}
	// Vorlauf im Betrieb, dann Dumps im Betrieb, dann kurz stoppen.
	before(t, l, "backup path:/opt/shop/data vorlauf", "backup volume:shop_db")
	before(t, l, "backup volume:shop_db", "stop shop-app")
	before(t, l, "stop shop-app", "stop shop-db")
	before(t, l, "stop shop-db", "backup path:/opt/shop/data")
	before(t, l, "backup path:/opt/shop/data", "start shop-db")
	before(t, l, "start shop-db", "start shop-app")
	before(t, l, "start shop-app", "stop panel-web")
	before(t, l, "start panel-web", "backup image:sha256:1")
	if l.index("stop blog-db") >= 0 || l.index("start shop-alt") >= 0 {
		t.Errorf("blog (nur Dump) gestoppt oder ein vorher gestoppter Container gestartet:\n%s", strings.Join(l.list(), "\n"))
	}
	if m := backup.Missing(r.Plan, res); len(m) > 0 {
		t.Errorf("ohne Snapshot: %v", m)
	}
	if e := r.State.Entries(); len(e) > 0 {
		t.Errorf("nach dem Lauf noch als gestoppt vermerkt: %v", e)
	}
	if len(res.Groups) != 2 || res.Groups[0].Name != "shop" {
		t.Errorf("Stillstand: %+v", res.Groups)
	}
}

func TestFehlerStartetGestoppteContainerWieder(t *testing.T) {
	l := &eventLog{}
	d := &fakeDocker{log: l, running: running()}
	s := &fakeStore{log: l, failOn: "path:/opt/shop/data"}
	// Erst im zweiten Durchgang scheitern: Vorlauf klappt.
	r := newRunner(t, smallPlan(false), d, &failSecond{fakeStore: s, calls: new(int)}, &fakeInput{})
	res := r.Run(context.Background())
	if res.OK() {
		t.Fatal("Lauf meldet Erfolg trotz Fehler")
	}
	if !d.running["shop-db"] || !d.running["shop-app"] {
		t.Errorf("nach dem Fehler laufen die Container nicht wieder: %v", d.running)
	}
	before(t, l, "start shop-db", "start shop-app")
	if e := r.State.Entries(); len(e) > 0 {
		t.Errorf("noch als gestoppt vermerkt: %v", e)
	}
}

// failSecond lässt den zweiten Snapshot von failOn scheitern.
type failSecond struct {
	*fakeStore
	calls *int
}

func (f *failSecond) Backup(ctx context.Context, a restic.BackupArgs) (restic.Result, error) {
	for _, t := range a.Tags {
		if t == "sbm-key="+f.failOn {
			*f.calls++
			if *f.calls == 2 {
				f.log.add("backup " + f.failOn + " FEHLER")
				return restic.Result{}, errors.New("Verbindung zum PC weg")
			}
		}
	}
	saved := f.failOn
	f.fakeStore.failOn = ""
	defer func() { f.fakeStore.failOn = saved }()
	return f.fakeStore.Backup(ctx, a)
}

func TestAbbruchStartetWieder(t *testing.T) {
	l := &eventLog{}
	d := &fakeDocker{log: l, running: running()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newRunner(t, smallPlan(false), d, &cancelOnStop{fakeDocker: d, cancel: cancel}, &fakeInput{})
	res := r.Run(ctx)
	if !res.Aborted {
		t.Fatalf("Abbruch nicht erkannt: %+v", res.Err)
	}
	if !d.running["shop-app"] || !d.running["shop-db"] {
		t.Errorf("nach Strg+C laufen die Container nicht wieder: %v", d.running)
	}
}

// cancelOnStop bricht den Lauf ab, sobald der erste Container steht, wie
// ein Strg+C mitten im Stopp.
type cancelOnStop struct {
	*fakeDocker
	cancel context.CancelFunc
}

func (c *cancelOnStop) Backup(ctx context.Context, a restic.BackupArgs) (restic.Result, error) {
	c.fakeDocker.mu.Lock()
	stopped := !c.fakeDocker.running["shop-app"]
	c.fakeDocker.mu.Unlock()
	if stopped {
		c.cancel()
		return restic.Result{}, context.Canceled
	}
	return restic.Result{SnapshotID: "x"}, nil
}

func TestLetzterLaufLaesstDiensteAus(t *testing.T) {
	l := &eventLog{}
	d := &fakeDocker{log: l, running: running()}
	s := &fakeStore{log: l}
	r := newRunner(t, smallPlan(true), d, s, &fakeInput{})
	res := r.Run(context.Background())
	if !res.OK() {
		t.Fatalf("Lauf gescheitert: %s", res.Err)
	}
	for _, id := range []string{"shop-app", "shop-db", "blog-db", "panel-web"} {
		if d.running[id] {
			t.Errorf("%s läuft nach dem letzten Lauf", id)
		}
	}
	// Dump erst, wenn die App steht, und bevor die Datenbank stoppt.
	before(t, l, "stop shop-app", "backup volume:shop_db")
	before(t, l, "backup volume:shop_db", "stop shop-db")
	if l.index("start shop-db") >= 0 {
		t.Error("letzter Lauf startet Container wieder")
	}
	e := r.State.Entries()
	if len(e) != 4 {
		t.Fatalf("gestoppt vermerkt: %v", e)
	}
	for _, x := range e {
		if !x.Final {
			t.Errorf("%s nicht als absichtlich aus vermerkt", x.Name)
		}
	}
}

func TestWartetAufVerwalteteServer(t *testing.T) {
	l := &eventLog{}
	run := running()
	run["spiel"] = true
	d := &fakeDocker{log: l, running: run, stopAfterPolls: 2}
	in := &fakeInput{}
	r := newRunner(t, smallPlan(false), d, &fakeStore{log: l}, in)
	res := r.Run(context.Background())
	if !res.OK() {
		t.Fatalf("Lauf gescheitert: %s", res.Err)
	}
	if len(in.asked) != 1 {
		t.Errorf("%d Mal gefragt; einmal reicht, geprüft wird nebenher", len(in.asked))
	}
	// Im Leitstand steht statt „Enter = …“ ein Satz mit den Servern.
	if len(in.notes) == 0 || !strings.Contains(in.notes[0], "Survival") || !strings.Contains(in.notes[0], "im Panel") {
		t.Errorf("Hinweis für den Leitstand: %q", in.notes)
	}
	if l.index("stop spiel") >= 0 {
		t.Error("HostDeck hat den verwalteten Server selbst gestoppt")
	}
	// Am Ende sagt HostDeck, dass sie im Panel wieder starten musst.
	var panel []string
	for _, g := range res.Groups {
		panel = append(panel, g.PanelStopped...)
	}
	if len(panel) != 1 || panel[0] != "Survival" {
		t.Errorf("im Panel gestoppt: %v", panel)
	}
	var sum bytes.Buffer
	(&backup.Report{Plan: r.Plan, Result: res}).Summary(&sum)
	if !strings.Contains(sum.String(), "Im Panel gestoppt, HostDeck startet sie nicht selbst: Survival") {
		t.Errorf("Kurzfassung ohne Hinweis aufs Panel:\n%s", sum.String())
	}

	// Mit „a“ bricht der Lauf ab, bevor das Panel stoppt.
	l2 := &eventLog{}
	run2 := running()
	run2["spiel"] = true
	d2 := &fakeDocker{log: l2, running: run2}
	r2 := newRunner(t, smallPlan(false), d2, &fakeStore{log: l2}, &fakeInput{answers: []string{"a"}})
	res2 := r2.Run(context.Background())
	if !res2.Aborted {
		t.Errorf("„a“ bricht nicht ab: %+v", res2.Err)
	}
	if l2.index("stop panel-web") >= 0 {
		t.Error("Panel trotz Abbruch gestoppt")
	}
}
