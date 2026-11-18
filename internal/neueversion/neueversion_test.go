package neueversion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		cur, latest string
		want        bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.0.0", "v1.1.0", true},
		{"1.9.0", "1.10.0", true},
		{"1.0.0-rc.1", "1.0.0", true},
		{"1.0.0-rc.1", "1.0.0-rc.2", true},
		{"1.0.0-rc.2", "1.0.0-rc.10", true},
		{"1.0.0-alpha", "1.0.0-alpha.1", true},
		{"1.0.0-alpha.1", "1.0.0-beta", true},
		{"1.0.0+abc", "1.0.0", false},
		{"1.0.1", "1.0.0", false},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.0.0-rc.1", false},
		{"e0-258afb6", "1.0.0", false},
		{"dev", "1.0.0", false},
		{"1.0.0", "kaputt", false},
		{"01.0.0", "1.0.1", false},
		{"1.0.0", "1.0.1-a/b", false},
		{"1.0.0", "1.0.1+x?y", false},
	} {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, erwartet %v", c.cur, c.latest, got, c.want)
		}
	}
}

func rig(t *testing.T, h http.HandlerFunc) (*Service, *time.Time) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s := &Service{File: filepath.Join(t.TempDir(), "versionshinweis.json"), Version: "1.0.0", Repo: "beispiel/hostdeck", API: srv.URL, Now: func() time.Time { return now }}
	return s, &now
}

func TestCheckNurWennAn(t *testing.T) {
	calls := 0
	s, now := rig(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/repos/beispiel/hostdeck/releases/latest" || r.Header.Get("User-Agent") != "hostdeck/1.0.0" {
			t.Errorf("Anfrage: %s %q", r.URL.Path, r.Header.Get("User-Agent"))
		}
		w.Write([]byte(`{"tag_name":"v1.2.0","html_url":"https://evil.example/x","draft":false,"prerelease":false}`))
	})
	if s.Due() {
		t.Fatal("ohne Wahl fällig")
	}
	if _, err := s.Check(context.Background()); err == nil || calls != 0 {
		t.Fatalf("aus, aber nachgefragt: %v %d", err, calls)
	}
	if err := s.SetOn(true); err != nil {
		t.Fatal(err)
	}
	if !s.Due() {
		t.Fatal("an, aber nicht fällig")
	}
	st, err := s.Check(context.Background())
	if err != nil || st.Latest != "1.2.0" || st.Link != "https://github.com/beispiel/hostdeck/releases/tag/v1.2.0" || calls != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	if s.Due() {
		t.Fatal("gleich wieder fällig")
	}
	*now = now.Add(Interval)
	if !s.Due() {
		t.Fatal("nach einem Tag nicht fällig")
	}
	if err := s.SetOn(false); err != nil {
		t.Fatal(err)
	}
	if st := s.Load(); st.Latest != "" || st.Enabled() || st.On == nil {
		t.Fatalf("aus vergisst nicht: %+v", st)
	}
}

func TestCheckFehler(t *testing.T) {
	for body, want := range map[string]string{
		`{"tag_name":"latest"}`:                        "unerwartete Versionsangabe",
		`{"tag_name":"v2.0.0-rc.1","prerelease":true}`: "unerwartete Versionsangabe",
		`{"tag_name":"v1.0.0/../../x"}`:                "unerwartete Versionsangabe",
		`kein json`:                                    "nicht lesbar",
	} {
		s, _ := rig(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
		_ = s.SetOn(true)
		st, err := s.Check(context.Background())
		if err == nil || !strings.Contains(err.Error(), want) || st.Error == "" || st.Checked == nil || st.Latest != "" {
			t.Errorf("%s: %+v %v", body, st, err)
		}
	}
	s, _ := rig(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	_ = s.SetOn(true)
	if _, err := s.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "noch kein Release") {
		t.Errorf("404: %v", err)
	}
}
