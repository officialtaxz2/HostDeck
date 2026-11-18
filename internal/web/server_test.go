package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/fixture"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
	"github.com/officialtaxz2/hostdeck/internal/runlog"
)

const testToken = "0123456789abcdef0123456789abcdef"

const (
	wings   = "/opt/calagopus/wings"
	backups = "/opt/calagopus/wings/backups"
)

// prepare legt einen Ergebnisordner an, wie ihn hostdeck discover hinterlässt.
// Wings bekommt Größen wie auf vps1, damit es „größte Unterordner“ gibt.
func prepare(t *testing.T, inv *inventory.Inventory, withManifest bool) string {
	t.Helper()
	if inv == nil {
		inv = fixture.Beispiel(t, 1).Run(t)
	}
	if e := inv.Path("path:" + wings); e != nil {
		e.Size = 53_000_000_000
		e.Largest = []inventory.SubSize{{Path: backups, Size: 37_000_000_000}, {Path: wings + "/volumes", Size: 16_000_000_000}}
	}
	dir := t.TempDir()
	b, err := inv.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, InventoryFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if withManifest {
		mb, err := manifest.Propose(inv).Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ManifestFile), mb, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type client struct {
	t   *testing.T
	srv *httptest.Server
}

func newClient(t *testing.T, dir string) *client {
	s := &Server{Dir: dir, Token: testToken}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &client{t: t, srv: ts}
}

func (c *client) do(method, path string, body any, hdr map[string]string) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (c *client) state() *State {
	c.t.Helper()
	code, b := c.do("GET", "/api/state", nil, nil)
	if code != 200 {
		c.t.Fatalf("state: %d %s", code, b)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		c.t.Fatal(err)
	}
	return &st
}

func (c *client) save(version string, edits ...Edit) (int, []byte) {
	c.t.Helper()
	return c.do("POST", "/api/save", SaveRequest{Version: version, Edits: edits}, nil)
}

func str(s string) *string { return &s }

func readManifest(t *testing.T, dir string) *manifest.Manifest {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := manifest.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestZugriffNurMitSchluessel(t *testing.T) {
	c := newClient(t, prepare(t, nil, true))
	for name, auth := range map[string]string{"ohne": "", "falsch": "Bearer ffffffffffffffffffffffffffffffff", "ohne Bearer": testToken} {
		if code, _ := c.do("GET", "/api/state", nil, map[string]string{"Authorization": auth}); code != http.StatusUnauthorized {
			t.Errorf("%s: %d, erwartet 401", name, code)
		}
		if code, _ := c.do("POST", "/api/save", SaveRequest{}, map[string]string{"Authorization": auth}); code != http.StatusUnauthorized {
			t.Errorf("%s (speichern): %d, erwartet 401", name, code)
		}
	}
	if code, _ := c.do("GET", "/api/state", nil, nil); code != 200 {
		t.Errorf("mit Schlüssel: %d", code)
	}
}

func TestNurLocalhost(t *testing.T) {
	c := newClient(t, prepare(t, nil, true))
	cases := map[string]int{
		"127.0.0.1:7322":      200,
		"localhost:9000":      200, // Tunnel mit anderem lokalem Port
		"[::1]:7322":          200,
		"evil.example:7322":   403, // DNS-Rebinding
		"example.net":         403,
		"127.0.0.1.nip.io:80": 403,
	}
	for host, want := range cases {
		if code, _ := c.do("GET", "/api/state", nil, map[string]string{"Host": host}); code != want {
			t.Errorf("Host %s: %d, erwartet %d", host, code, want)
		}
		if code, _ := c.do("GET", "/", nil, map[string]string{"Host": host}); code != want {
			t.Errorf("Seite mit Host %s: %d, erwartet %d", host, code, want)
		}
	}
	code, _ := c.do("POST", "/api/save", SaveRequest{}, map[string]string{"Origin": "https://evil.example"})
	if code != http.StatusForbidden {
		t.Errorf("fremder Origin: %d, erwartet 403", code)
	}
}

func TestSeiteUndSicherheitsHeader(t *testing.T) {
	c := newClient(t, prepare(t, nil, true))
	resp, err := http.Get(c.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "app.js") {
		t.Fatalf("Seite: %d %s", resp.StatusCode, body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "font-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP ohne %q: %s", want, csp)
		}
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("Header fehlen: %v", resp.Header)
	}
	// Alles, was die Seite braucht, kommt vom Server selbst, mit passendem
	// Inhaltstyp (nosniff lässt sonst etwa die Schriften nicht zu).
	files := map[string]string{
		"/app.js":                           "text/javascript",
		"/theme.js":                         "text/javascript",
		"/app.css":                          "text/css",
		"/fonts/outfit-latin.woff2":         "font/woff2",
		"/fonts/jetbrains-mono-latin.woff2": "font/woff2",
		"/bg/berge-dunkel.svg":              "image/svg+xml",
	}
	got := map[string][]byte{}
	for f, typ := range files {
		r, err := http.Get(c.srv.URL + f)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), typ) {
			t.Errorf("%s: %d %q, erwartet %s", f, r.StatusCode, r.Header.Get("Content-Type"), typ)
		}
		got[f] = b
	}
	// Die Seite selbst enthält keine Daten des Servers.
	if strings.Contains(string(body), "pensum") {
		t.Error("index.html enthält Serverdaten")
	}
	// Die Seite lädt nichts aus dem Netz (die CSP würde es ohnehin sperren).
	for _, bad := range []string{`src="http`, `href="http`, `src="//`, `href="//`} {
		if strings.Contains(string(body), bad) {
			t.Errorf("index.html lädt von außen (%s)", bad)
		}
	}
	for _, bad := range []string{"url(http", "url(//", "@import"} {
		if strings.Contains(string(got["/app.css"]), bad) {
			t.Errorf("app.css lädt von außen (%s)", bad)
		}
	}
	// Beim Drucken als PDF darf die mitlaufende Kopfzeile nicht über dem
	// Inhalt liegen.
	if css := string(got["/app.css"]); !strings.Contains(css, "@media print") || !strings.Contains(css, ".hd, .agenda, .aside { position: static; }") {
		t.Error("app.css hat keine Druckregeln für die mitlaufenden Leisten")
	}
	// style-src 'self' verbietet style-Attribute; der Browser ignoriert sie
	// still (so blieb der Platzbalken leer). Stile nur über CSS-Klassen
	// oder element.style setzen.
	if strings.Contains(string(body), " style=") {
		t.Error("index.html hat ein style-Attribut; das blockiert die CSP")
	}
	for _, f := range []string{"/app.js", "/theme.js"} {
		for _, bad := range []string{"style: ", "style:'", "'style'", "\"style\"", "cssText"} {
			if strings.Contains(string(got[f]), bad) {
				t.Errorf("%s setzt Stile als Attribut (%s); das blockiert die CSP", f, bad)
			}
		}
	}
}

func TestAnsicht(t *testing.T) {
	c := newClient(t, prepare(t, nil, true))
	code, raw := c.do("GET", "/api/state", nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	if strings.Contains(string(raw), "geheim") {
		t.Error("Ansicht enthält ein Geheimnis")
	}
	st := c.state()
	if st.Manifest.State != ManifestOK || len(st.Manifest.Added) != 0 || len(st.Gone) != 0 {
		t.Errorf("Manifest: %+v, weg: %v", st.Manifest, st.Gone)
	}
	if st.Host.Hostname != "vps1" || len(st.Projects) != 12 {
		t.Fatalf("Host %q, %d Projekte", st.Host.Hostname, len(st.Projects))
	}
	proj := map[string]ProjectView{}
	for _, p := range st.Projects {
		proj[p.Name] = p
	}
	mc := proj["mcserver"]
	// Ein Spielserver läuft, der andere ist aus und hat keinen Container.
	if len(mc.Managed) != 2 {
		t.Fatalf("verwaltete Server: %+v", mc.Managed)
	}
	idle, game := mc.Managed[0], mc.Managed[1]
	if game.Name != fixture.GameServer || game.Manager != "calagopus-web" || !game.Container || !game.Running ||
		game.Path != wings+"/volumes/"+fixture.GameServer || game.Size <= 0 {
		t.Errorf("laufender Spielserver: %+v", game)
	}
	if idle.Name != fixture.IdleServer || idle.Container || idle.Running || idle.Path != wings+"/volumes/"+fixture.IdleServer || idle.Size <= 0 {
		t.Errorf("Spielserver ohne Container: %+v", idle)
	}
	// Fünf Compose-Container und der laufende Spielserver; der ausgeschaltete
	// zählt nicht mit.
	if mc.Running != 6 || mc.Total != 6 {
		t.Errorf("mcserver: %d von %d laufen, erwartet 6 von 6", mc.Running, mc.Total)
	}
	var w *DataView
	for i := range mc.Data {
		if mc.Data[i].Key == "path:"+wings {
			w = &mc.Data[i]
		}
	}
	if w == nil || len(w.Largest) != 2 || w.Largest[0].Path != backups {
		t.Fatalf("Wings ohne größte Unterordner: %+v", w)
	}
	if p := proj["work"]; p.DirExists || p.Decision != "open" {
		t.Errorf("work: %+v", p)
	}
	if p := proj["pensum"]; len(p.Domains) != 1 || p.Domains[0] != "work.example.net" || len(p.Services) != 2 {
		t.Errorf("pensum: %+v", p)
	}
	for _, s := range proj["mcserver"].Services {
		if s.Name == "mc-packs" && !strings.Contains(s.ImageName, "keinen Namen") {
			t.Errorf("mc-packs-Image: %q", s.ImageName)
		}
	}
	for _, s := range proj["pensum"].Services {
		want := map[string]string{"app": "gebaut", "db": "registry"}[s.Name]
		if s.Origin != want || s.OriginNote == "" {
			t.Errorf("pensum/%s: Herkunft %q (%q), erwartet %q", s.Name, s.Origin, s.OriginNote, want)
		}
	}
	loose := map[string]DataView{}
	for _, d := range st.Loose {
		loose[d.Key] = d
	}
	if d, ok := loose["volume:work_db_data"]; !ok || !d.Orphan || d.Engine != "postgres" || d.Strategy != "archive" {
		t.Errorf("work_db_data: %+v", d)
	}
	if d, ok := loose["path:/opt/docker/neko"]; !ok || d.Git == nil {
		t.Errorf("alter neko-Ordner: %+v", d)
	}
	if st.Caddy == nil || len(st.Caddy.Routes) != 12 || st.Caddy.Decision != "take" {
		t.Errorf("Caddy: %+v", st.Caddy)
	}
	if len(st.System) == 0 || len(st.Findings) == 0 {
		t.Error("System oder Hinweise fehlen")
	}
	// Am Server selbst: Ordner mit Bereich, Caches als Unterordner mit
	// Größe, dazu Checkliste, Aufgaben, Einstellungen und Platz.
	areas := map[string]string{}
	var cache *DataView
	for i, d := range st.Loose {
		if d.Area != "" {
			areas[d.Label] = d.Area
		}
		if d.Key == "path:/root/.cache" {
			cache = &st.Loose[i]
		}
	}
	if areas["/root"] != "home" || areas["/etc"] != "einstellungen" || areas["/minecraft-alt"] != "fund" {
		t.Errorf("Bereiche: %v", areas)
	}
	if cache == nil || cache.SplitOf != "path:/root" || cache.Strategy != "skip" || cache.Size <= 0 {
		t.Errorf("Cache in /root: %+v", cache)
	}
	if len(st.Checks) == 0 || len(st.Tasks) == 0 || len(st.Settings) == 0 || st.Space == nil {
		t.Errorf("Am Server selbst fehlt etwas: %d Punkte, %d Aufgaben, %d Einstellungen, Platz %v", len(st.Checks), len(st.Tasks), len(st.Settings), st.Space)
	}
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), "geheim") {
		t.Error("Ansicht enthält ein Geheimnis")
	}
}

func TestSpeichern(t *testing.T) {
	dir := prepare(t, nil, true)
	c := newClient(t, dir)
	st := c.state()
	code, body := c.save(st.Version,
		Edit{Key: "project:arcane", Decision: str("skip"), Reason: str("Arcane nicht übernehmen")},
		Edit{Key: "project:arbeitszeit", Decision: str("archive"), Reason: str("pensum ist der Nachfolger")},
		Edit{Key: "path:" + backups, SplitOf: "path:" + wings, Strategy: str("skip"), Reason: str("nur aktueller Stand")},
		Edit{Key: "volume:streamingroom_uploads", Strategy: str("skip"), Reason: str("kann weg")},
		Edit{Key: "compose:neko/neko", Image: str("rebuild")},
		Edit{Key: "path:/opt/calagopus/cache", Strategy: str("rebuild")},
		Edit{Key: "host:caddy", Decision: str("take")},
	)
	if code != 200 {
		t.Fatalf("speichern: %d %s", code, body)
	}
	fi, err := os.Stat(filepath.Join(dir, ManifestFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("manifest.yaml: %v %v", err, fi.Mode())
	}
	m := readManifest(t, dir)
	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s: %q, erwartet %q", what, got, want)
		}
	}
	check("arcane", m.Projects["arcane"].Decision, "skip")
	check("arcane Grund", m.Projects["arcane"].Reason, "Arcane nicht übernehmen")
	check("arbeitszeit", m.Projects["arbeitszeit"].Decision, "archive")
	check("Backups", m.Projects["mcserver"].Data["path:"+backups].Strategy, "skip")
	check("streamingroom", m.Unassigned["volume:streamingroom_uploads"].Strategy, "skip")
	check("neko-Image", m.Projects["neko"].Services["neko"].Image, "rebuild")
	check("Valkey", m.Projects["mcserver"].Data["path:/opt/calagopus/cache"].Strategy, "rebuild")
	check("Hinweis bleibt", m.Projects["mcserver"].Data["path:/opt/calagopus/postgres"].Note, "Version 18")

	// Die Antwort ist der neue Stand; der Unterordner hängt an Wings.
	var st2 State
	if err := json.Unmarshal(body, &st2); err != nil {
		t.Fatal(err)
	}
	if st2.Version == st.Version {
		t.Error("Version hat sich nicht geändert")
	}
	found := false
	for _, p := range st2.Projects {
		for _, d := range p.Data {
			if d.Key == "path:"+backups {
				found = d.SplitOf == "path:"+wings && d.Size == 37_000_000_000
			}
		}
	}
	if !found {
		t.Error("Unterordner nicht unter Wings oder ohne Größe")
	}
	if len(st2.Gone) != 0 {
		t.Errorf("eigener Unterordner gilt als weggefallen: %v", st2.Gone)
	}

	// Einen Spielserver, der aus ist, nicht sichern: eigener Unterordner mit
	// skip, der an Wings hängt und die Größe des Servers trägt.
	idleDir := wings + "/volumes/" + fixture.IdleServer
	code, body = c.save(st2.Version, Edit{Key: "path:" + idleDir, SplitOf: "path:" + wings, Strategy: str("skip"), Reason: str("alter Server")})
	if code != 200 {
		t.Fatalf("Server weglassen: %d %s", code, body)
	}
	if err := json.Unmarshal(body, &st2); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, p := range st2.Projects {
		for _, d := range p.Data {
			if d.Key == "path:"+idleDir {
				found = d.SplitOf == "path:"+wings && d.Size > 0 && d.Strategy == "skip"
			}
		}
	}
	if !found || len(st2.Gone) != 0 {
		t.Errorf("Server-Ordner nicht als Unterordner von Wings: gefunden=%v weg=%v", found, st2.Gone)
	}
	if m := readManifest(t, dir); m.Projects["mcserver"].Managed[fixture.IdleServer] == nil {
		t.Error("Server ohne Container fehlt im Manifest")
	}
	code, body = c.save(st2.Version, Edit{Key: "path:" + idleDir, Remove: true})
	if code != 200 {
		t.Fatalf("Server wieder sichern: %d %s", code, body)
	}
	if err := json.Unmarshal(body, &st2); err != nil {
		t.Fatal(err)
	}

	// Unterordner wieder entfernen.
	code, body = c.save(st2.Version, Edit{Key: "path:" + backups, Remove: true})
	if code != 200 {
		t.Fatalf("entfernen: %d %s", code, body)
	}
	if _, ok := readManifest(t, dir).Projects["mcserver"].Data["path:"+backups]; ok {
		t.Error("Unterordner noch im Manifest")
	}
}

func TestSpeichernAbgelehnt(t *testing.T) {
	dir := prepare(t, nil, true)
	before, _ := os.ReadFile(filepath.Join(dir, ManifestFile))
	c := newClient(t, dir)
	v := c.state().Version
	cases := map[string]struct {
		edit Edit
		want string
	}{
		"skip ohne Grund":     {Edit{Key: "project:pensum", Decision: str("skip")}, "skip braucht eine Begründung"},
		"ungültiger Wert":     {Edit{Key: "project:pensum", Decision: str("vielleicht")}, "ist ungültig"},
		"archive im Projekt":  {Edit{Key: "volume:pensum_pensum_db", Strategy: str("archive")}, "archive gibt es nur unter unassigned"},
		"unbekannter Eintrag": {Edit{Key: "project:gibtsnicht", Decision: str("take")}, "nicht gefunden"},
		"Unterordner außerhalb": {Edit{Key: "path:/etc/shadow", SplitOf: "path:" + wings, Strategy: str("skip"), Reason: str("x")},
			"liegt nicht in"},
		"Unterordner mit ..": {Edit{Key: "path:" + wings + "/../../etc", SplitOf: "path:" + wings, Strategy: str("skip"), Reason: str("x")},
			"absoluter Pfad"},
		"Unterordner eines Volumes": {Edit{Key: "path:/var/lib/docker/volumes/x/_data/y", SplitOf: "volume:pensum_pensum_db", Strategy: str("skip"), Reason: str("x")},
			"nur bei Ordnern"},
		"vorhandenes entfernen": {Edit{Key: "volume:pensum_pensum_db", Remove: true}, "kann nicht entfernt werden"},
		"/etc übernehmen":       {Edit{Key: "path:/etc", Strategy: str("live-copy")}, "nur archivieren oder weglassen"},
	}
	for name, cs := range cases {
		code, body := c.save(v, cs.edit)
		if code != http.StatusUnprocessableEntity || !strings.Contains(string(body), cs.want) {
			t.Errorf("%s: %d %s, erwartet 422 mit %q", name, code, body, cs.want)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, ManifestFile))
	if !bytes.Equal(before, after) {
		t.Error("abgelehnte Änderungen haben manifest.yaml verändert")
	}
	if code, _ := c.do("POST", "/api/save", map[string]any{"version": v, "edits": []any{}, "extra": 1}, nil); code != http.StatusBadRequest {
		t.Errorf("unbekanntes Feld: %d, erwartet 400", code)
	}
	if code, _ := c.do("POST", "/api/save", strings.Repeat("x", maxBody+10), nil); code != http.StatusBadRequest {
		t.Errorf("zu große Anfrage: %d, erwartet 400", code)
	}
}

func TestKonfliktNachAenderungVonHand(t *testing.T) {
	dir := prepare(t, nil, true)
	c := newClient(t, dir)
	v := c.state().Version
	f := filepath.Join(dir, ManifestFile)
	b, _ := os.ReadFile(f)
	os.WriteFile(f, append(b, []byte("# von Hand\n")...), 0o600)
	code, body := c.save(v, Edit{Key: "project:arcane", Decision: str("skip"), Reason: str("x")})
	if code != http.StatusConflict || !strings.Contains(string(body), "Lade die Seite neu") {
		t.Errorf("%d %s, erwartet 409", code, body)
	}
}

func TestNeueUndWeggefalleneEintraege(t *testing.T) {
	dir := prepare(t, nil, true)
	// Neuer Lauf: ein Dienst heißt anders, ein Volume ist weg.
	s := fixture.Beispiel(t, 2)
	s.Docker.Find("mcguides-guide-1").C.Config.Labels["com.docker.compose.service"] = "guide2"
	vols := s.Docker.Vols[:0]
	for _, v := range s.Docker.Vols {
		if v.Name != "streamingroom_uploads" {
			vols = append(vols, v)
		}
	}
	s.Docker.Vols = vols
	inv := s.Run(t)
	b, _ := inv.Marshal()
	os.WriteFile(filepath.Join(dir, InventoryFile), b, 0o600)

	c := newClient(t, dir)
	st := c.state()
	if strings.Join(st.Manifest.Added, ",") != "compose:mcguides/guide2" {
		t.Errorf("neu: %v", st.Manifest.Added)
	}
	gone := map[string]bool{}
	for _, g := range st.Gone {
		gone[g.Key] = true
	}
	if !gone["compose:mcguides/guide"] || !gone["volume:streamingroom_uploads"] {
		t.Errorf("weggefallen: %+v", st.Gone)
	}
	for _, d := range st.Loose {
		if d.Key == "volume:streamingroom_uploads" {
			t.Error("weggefallenes Volume steht noch bei den Daten")
		}
	}
	code, body := c.save(st.Version, Edit{Key: "volume:streamingroom_uploads", Remove: true})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	m := readManifest(t, dir)
	if _, ok := m.Unassigned["volume:streamingroom_uploads"]; ok {
		t.Error("weggefallenes Volume nicht entfernt")
	}
	if m.Projects["mcguides"].Services["guide2"] == nil {
		t.Error("neuer Dienst nicht übernommen")
	}
	if m.Projects["mcguides"].Services["guide"] == nil {
		t.Error("nicht markierter weggefallener Dienst wurde trotzdem entfernt")
	}
}

func TestUngueltigesOderFehlendesManifest(t *testing.T) {
	dir := prepare(t, nil, false)
	c := newClient(t, dir)
	st := c.state()
	if st.Manifest.State != ManifestMissing {
		t.Errorf("Zustand %q", st.Manifest.State)
	}
	if code, body := c.save(st.Version); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	readManifest(t, dir)

	bad := "version: 1\nsource: {hostname: vps1}\nprojects:\n  pensum: {decision: vielleicht}\n"
	os.WriteFile(filepath.Join(dir, ManifestFile), []byte(bad), 0o600)
	st = c.state()
	if st.Manifest.State != ManifestInvalid || len(st.Manifest.Problems) == 0 || !strings.Contains(st.Manifest.Problems[0], "vielleicht") {
		t.Errorf("Manifest: %+v", st.Manifest)
	}
	if code, body := c.save(st.Version, Edit{Key: "project:pensum", Decision: str("take")}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if m := readManifest(t, dir); m.Projects["pensum"].Decision != "take" {
		t.Error("pensum nicht gespeichert")
	}
	old, err := os.ReadFile(filepath.Join(dir, InvalidFile))
	if err != nil || string(old) != bad {
		t.Errorf("ungültige Datei nicht aufgehoben: %v", err)
	}
}

func TestOhneBestandsaufnahme(t *testing.T) {
	s := &Server{Dir: t.TempDir(), Token: testToken}
	if err := s.Check(); err != ErrNoInventory {
		t.Errorf("Fehler %v, erwartet ErrNoInventory", err)
	}
}

func TestSchluessel(t *testing.T) {
	a, err1 := NewToken()
	b, err2 := NewToken()
	if err1 != nil || err2 != nil || len(a) != 32 || a == b {
		t.Errorf("%q %q %v %v", a, b, err1, err2)
	}
}

// Veraltete Hinweise im Manifest (etwa vom letzten Lauf) zeigt die Seite mit
// dem aktuellen Stand; beim Speichern landen sie so auch in der Datei.
func TestHinweiseAktuell(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	dir := prepare(t, inv, true)
	m := readManifest(t, dir)
	fresh := m.Unassigned["path:/etc"].Note
	m.Unassigned["path:/etc"].Note = "veraltet: 4 geänderte Dateien"
	m.Unassigned["path:/etc"].Strategy = "skip"
	m.Unassigned["path:/etc"].Reason = "nicht nötig"
	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, ManifestFile), b, 0o600)

	c := newClient(t, dir)
	_, body := c.do("GET", "/api/state", nil, nil)
	if strings.Contains(string(body), "veraltet") || !strings.Contains(string(body), fresh) {
		t.Errorf("Seite zeigt nicht den aktuellen Hinweis %q", fresh)
	}
	st := c.state()
	if code, body := c.save(st.Version, Edit{Key: "project:pensum", Decision: str("archive")}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	saved := readManifest(t, dir).Unassigned["path:/etc"]
	if saved.Note != fresh || saved.Strategy != "skip" || saved.Reason != "nicht nötig" {
		t.Errorf("gespeichert: %+v", saved)
	}
}

func TestBackupLaeufeUndStandInDerAnsicht(t *testing.T) {
	dir := prepare(t, nil, true)
	c := newClient(t, dir)
	st := c.state()
	if st.Backups == nil || len(st.Backups) != 0 || st.BackupsNote != "" {
		t.Fatalf("ohne Läufe: %+v %q", st.Backups, st.BackupsNote)
	}
	if st.Manifest.Saved == nil || st.Host.Docker == "" {
		t.Errorf("Speicherzeit %v oder Docker-Version %q fehlt", st.Manifest.Saved, st.Host.Docker)
	}
	t0 := time.Date(2026, 11, 1, 9, 12, 0, 0, time.UTC)
	if err := runlog.Append(dir, runlog.Entry{Run: "20261101-091200", Started: t0, Finished: t0.Add(38 * time.Minute), Valid: true, Verdict: "GÜLTIG", Size: 24_700_000_000, Items: 31, Projects: 10}); err != nil {
		t.Fatal(err)
	}
	st = c.state()
	if len(st.Backups) != 1 || !st.Backups[0].Valid || st.Backups[0].Projects != 10 {
		t.Fatalf("Lauf nicht in der Ansicht: %+v", st.Backups)
	}
	// Eine kaputte Liste nimmt der Seite nicht alles andere.
	if err := os.WriteFile(filepath.Join(dir, runlog.File), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	st = c.state()
	if len(st.Backups) != 0 || !strings.Contains(st.BackupsNote, runlog.File) || len(st.Projects) == 0 {
		t.Errorf("kaputte Liste: %+v %q", st.Backups, st.BackupsNote)
	}
}

// Das Aufräumen trägt gelöschte Volumes selbst aus der Auswahl aus; der
// Rest bleibt, wie du ihn gespeichert hast.
func TestUnselectNachDemAufraeumen(t *testing.T) {
	dir := prepare(t, nil, true)
	if _, ok := readManifest(t, dir).Unassigned["volume:streamingroom_uploads"]; !ok {
		t.Fatal("Volume fehlt schon vorher in der Auswahl")
	}
	n, err := Unselect(dir, []string{"volume:streamingroom_uploads", "path:/etc/caddy/Caddyfile.bak", "volume:gibtsnicht"})
	if err != nil || n != 1 {
		t.Fatalf("ausgetragen %d, %v", n, err)
	}
	m := readManifest(t, dir)
	if _, ok := m.Unassigned["volume:streamingroom_uploads"]; ok {
		t.Error("Volume noch in der Auswahl")
	}
	if m.Projects["mcguides"] == nil {
		t.Error("Rest der Auswahl verloren")
	}
	if n, err := Unselect(dir, []string{"volume:streamingroom_uploads"}); err != nil || n != 0 {
		t.Errorf("zweimal: %d %v", n, err)
	}
	if n, err := Unselect(t.TempDir(), []string{"volume:x"}); err != nil || n != 0 {
		t.Errorf("ohne Auswahl: %d %v", n, err)
	}
}

// Umzugstag 04.11.2026: Auf dem neuen Server fehlte das Projekt arcane samt
// Volume. „Alle entfernen“ schickt Projekt und Volume; das Projekt nimmt
// sein Volume schon mit, das Entfernen des Volumes darf dann nicht scheitern.
func TestAlleWeggefallenenEntfernenSamtProjekt(t *testing.T) {
	dir := prepare(t, nil, true)
	s := fixture.Beispiel(t, 2)
	ctrs := s.Docker.Ctrs[:0]
	for _, c := range s.Docker.Ctrs {
		if c.C.Config.Labels["com.docker.compose.project"] != "arcane" {
			ctrs = append(ctrs, c)
		}
	}
	s.Docker.Ctrs = ctrs
	vols := s.Docker.Vols[:0]
	for _, v := range s.Docker.Vols {
		if v.Name != "arcane_arcane-data" {
			vols = append(vols, v)
		}
	}
	s.Docker.Vols = vols
	b, _ := s.Run(t).Marshal()
	os.WriteFile(filepath.Join(dir, InventoryFile), b, 0o600)

	c := newClient(t, dir)
	st := c.state()
	var edits []Edit
	has := map[string]bool{}
	for _, g := range st.Gone {
		edits = append(edits, Edit{Key: g.Key, Remove: true})
		has[g.Key] = true
	}
	if !has["project:arcane"] || !has["volume:arcane_arcane-data"] {
		t.Fatalf("weggefallen: %+v", st.Gone)
	}
	if code, body := c.save(st.Version, edits...); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if st2 := newClient(t, dir).state(); len(st2.Gone) != 0 {
		t.Errorf("noch weggefallen: %+v", st2.Gone)
	}
	// Ein Schlüssel, den es nie gab, bleibt ein Fehler.
	st3 := newClient(t, dir).state()
	if code, body := c.save(st3.Version, Edit{Key: "volume:gab-es-nie", Remove: true}); code == http.StatusOK {
		t.Errorf("unbekannter Schlüssel entfernt: %s", body)
	}
}

// Löschen unter Speicherplatz trägt alles unter dem gelöschten Ordner aus.
func TestUnselectUnder(t *testing.T) {
	dir := prepare(t, nil, true)
	var paths []string
	for _, k := range readManifest(t, dir).Keys() {
		if p, ok := strings.CutPrefix(k, "path:"); ok {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		t.Skip("keine Pfade in der Auswahl")
	}
	base := filepath.Dir(paths[0])
	want := 0
	for _, p := range paths {
		if p == base || strings.HasPrefix(p, base+"/") {
			want++
		}
	}
	n, err := UnselectUnder(dir, []string{base + "/"})
	if err != nil || n != want {
		t.Fatalf("ausgetragen %d (erwartet %d), %v", n, want, err)
	}
	for _, k := range readManifest(t, dir).Keys() {
		if p, ok := strings.CutPrefix(k, "path:"); ok && strings.HasPrefix(p, base+"/") {
			t.Errorf("%s noch in der Auswahl", k)
		}
	}
}
