package dienst

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/neueversion"
)

// D5 (Veröffentlichung): Der Hinweis auf neue Versionen ist aus, bis man ihn
// einschaltet; eingeschaltet fragt HostDeck gleich nach und zeigt nur einen
// Hinweis, eingespielt wird nichts.
func TestVersionshinweis(t *testing.T) {
	calls := 0
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"tag_name":"v1.1.0"}`))
	}))
	defer gh.Close()
	rel := &neueversion.Service{File: filepath.Join(t.TempDir(), "versionshinweis.json"), Version: "1.0.0", Repo: "beispiel/hostdeck", API: gh.URL}
	r, sid := modeRig(t, func(d *Dienst) { d.Release = rel })
	code, b := r.api(t, "GET", "/api/versionshinweis", sid, "")
	if code != 200 || !strings.Contains(b, `"an":null`) || !strings.Contains(b, `"neuer":false`) || calls != 0 {
		t.Fatalf("ohne Wahl: %d %s (%d Anfragen)", code, b, calls)
	}
	if h := NeueVersionHints(rel)(); len(h) != 0 {
		t.Fatalf("Hinweis ohne Wahl: %+v", h)
	}
	if code, b := r.api(t, "POST", "/api/versionshinweis", sid, `{}`); code != 400 {
		t.Fatalf("ohne Angabe: %d %s", code, b)
	}
	code, b = r.api(t, "POST", "/api/versionshinweis", sid, `{"an":true}`)
	if code != 200 || !strings.Contains(b, `"neueste":"1.1.0"`) || !strings.Contains(b, `"neuer":true`) || calls != 1 ||
		!strings.Contains(b, `"link":"https://github.com/beispiel/hostdeck/releases/tag/v1.1.0"`) {
		t.Fatalf("eingeschaltet: %d %s", code, b)
	}
	h := NeueVersionHints(rel)()
	if len(h) != 1 || h[0].Link != "#/updates" || !strings.Contains(h[0].Title, "1.1.0") {
		t.Fatalf("Hinweis: %+v", h)
	}
	rel.Version = "e0-258afb6"
	if _, b := r.api(t, "GET", "/api/versionshinweis", sid, ""); !strings.Contains(b, `"unbekannt":true`) || !strings.Contains(b, `"neuer":false`) {
		t.Fatalf("Testversion: %s", b)
	}
	if h := NeueVersionHints(rel)(); len(h) != 0 {
		t.Fatalf("Hinweis bei Testversion: %+v", h)
	}
	if code, b := r.api(t, "POST", "/api/versionshinweis", sid, `{"an":false}`); code != 200 || strings.Contains(b, "1.1.0") || !strings.Contains(b, `"an":false`) {
		t.Fatalf("ausgeschaltet: %d %s", code, b)
	}
	es, _ := r.d.Journal.Last(20)
	found := false
	for _, e := range es {
		found = found || strings.HasPrefix(e.Text, "Hinweis auf neue Versionen eingeschaltet")
	}
	if !found {
		t.Errorf("nicht im Protokoll: %+v", es)
	}
}
