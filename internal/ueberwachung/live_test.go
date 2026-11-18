package ueberwachung

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

type countDocker struct {
	mu    sync.Mutex
	lists int
}

func (d *countDocker) List(context.Context) ([]dockerapi.OpsContainer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lists++
	return nil, nil
}
func (d *countDocker) Inspect(context.Context, string) (dockerapi.Container, error) {
	return dockerapi.Container{}, nil
}

// Live misst nur, solange jemand fragt, und hört nach Idle wieder auf. Die
// ersten Messungen kommen aus dem 10-Sekunden-Verlauf; Prozessor je
// Container als Anteil am Server.
func TestLiveNurBeimZuschauen(t *testing.T) {
	r := newRig(t)
	clock := struct {
		sync.Mutex
		t time.Time
	}{t: time.Date(2026, 11, 16, 23, 0, 0, 0, time.UTC)}
	now := func() time.Time { clock.Lock(); defer clock.Unlock(); return clock.t }
	st := &Store{}
	for i := 0; i < 30; i++ {
		st.Add(Frame{At: clock.t.Add(time.Duration(i-30) * 10 * time.Second), Values: map[string]float64{KeyCPU: 20, ContainerKey("web", ValCPU): 200}})
	}
	d := &countDocker{}
	l := &Live{Collector: &Collector{Docker: d, Proc: r.proc, Sys: r.sys, ListEvery: LiveListEvery}, Interval: 5 * time.Millisecond, Idle: 50 * time.Millisecond, Now: now}
	s := &Service{Collector: r.c, Store: st, Live: l, Now: now}
	s.info.Cores = 4
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.start(ctx)
	if l.Active() {
		t.Fatal("misst ohne Zuschauer")
	}
	v := s.LiveView(0, []string{"s/", "c/*/cpu"})
	// Die Frage weckt Live; eine erste schnelle Messung kann schon in
	// dieser Antwort stehen. Die 30 davor kommen aus dem Verlauf.
	if len(v.Frames) < 30 || v.Frames[0].V[ContainerKey("web", ValCPU)] != 50 || v.Frames[29].T >= now().UnixMilli() {
		t.Fatalf("aus dem Verlauf: %d Messungen, erste %v", len(v.Frames), v.Frames[0].V)
	}
	for _, f := range v.Frames[30:] {
		if v.FineFrom == 0 || f.T < v.FineFrom {
			t.Fatalf("Messung %d nach dem Verlauf ist keine schnelle (ab %d)", f.T, v.FineFrom)
		}
	}
	since := v.Frames[29].T
	deadline := time.Now().Add(2 * time.Second)
	for {
		l.mu.Lock()
		n := len(l.frames)
		l.mu.Unlock()
		if n >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	v2 := s.LiveView(since, nil)
	if !v2.Running || v2.FineFrom == 0 || len(v2.Frames) == 0 {
		t.Fatalf("schneller Takt: %+v", v2)
	}
	for _, f := range v2.Frames {
		if f.T <= since {
			t.Errorf("Messung %d nicht nach seit %d", f.T, since)
		}
	}
	// Nur alle 5 s die Liste von Docker (die Uhr steht im Test).
	d.mu.Lock()
	if d.lists != 1 {
		t.Errorf("%d Abfragen der Container-Liste", d.lists)
	}
	d.mu.Unlock()
	// Ohne Fragen hört Live auf.
	clock.Lock()
	clock.t = clock.t.Add(time.Minute)
	clock.Unlock()
	deadline = time.Now().Add(2 * time.Second)
	for l.Active() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if l.Active() {
		t.Error("misst ohne Zuschauer weiter")
	}
	// Die Zählerstände sind vergessen: Die erste Messung beim nächsten
	// Öffnen ist kein Mittel über die Pause.
	l.Collector.mu.Lock()
	if len(l.Collector.prev) != 0 {
		t.Errorf("%d Zählerstände nach dem Aufhören", len(l.Collector.prev))
	}
	l.Collector.mu.Unlock()
}
