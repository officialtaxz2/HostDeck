package discover_test

import (
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/fixture"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

func imageByTag(inv *inventory.Inventory, tag string) *inventory.Image {
	for i := range inv.Images {
		for _, t := range inv.Images[i].Tags {
			if t == tag {
				return &inv.Images[i]
			}
		}
		if tag == "" && len(inv.Images[i].Tags) == 0 {
			return &inv.Images[i]
		}
	}
	return nil
}

// Mit dem containerd-Speicher von Docker 29 hat jedes Image einen Digest.
// Auf dem echten vps1 galt deshalb nur eines von acht lokal gebauten
// Images als lokal.
func TestImageHerkunftContainerd(t *testing.T) {
	type want struct {
		origin, note, source string
		local                bool
	}
	cases := []struct {
		api    string
		images map[string]want
		local  int // laufende Images, die per docker save gesichert werden
	}{
		{"1.56", map[string]want{
			"postgres:17-alpine":             {inventory.OriginRegistry, "laut Docker geladen aus docker.io/library/postgres", "docker.io/library/postgres", false},
			"ghcr.io/calagopus/panel:latest": {inventory.OriginRegistry, "laut Docker geladen aus ghcr.io/calagopus/panel", "ghcr.io/calagopus/panel", false},
			"itzg/mc-router:latest":          {inventory.OriginRegistry, "", "docker.io/itzg/mc-router", false},
			"pensum-app:latest":              {inventory.OriginBuilt, "von docker compose gebaut", "", true},
			"saftbar-app:latest":             {inventory.OriginBuilt, "laut Docker auf diesem Server gebaut", "", true},
			"my-neko/helium:latest":          {inventory.OriginUnknown, "", "", true},
			"":                               {inventory.OriginRegistry, "", "docker.io/library/caddy", true}, // ohne Namen: per ID sichern
		}, 8},
		{"1.50", map[string]want{
			"postgres:17-alpine":    {inventory.OriginRegistry, "per docker pull geholt (Image-Index nur teilweise geladen)", "", false},
			"saftbar-app:latest":    {inventory.OriginBuilt, "von docker compose gebaut", "", true},
			"my-neko/helium:latest": {inventory.OriginUnknown, "", "", true},
			"":                      {inventory.OriginRegistry, "", "", true},
		}, 8},
		// Ohne Image-Index weiß HostDeck bei keinem Image mit Namen, woher es
		// kommt; dann wird zur Sicherheit alles gesichert, was nicht von
		// Compose gebaut wurde.
		{"1.45", map[string]want{
			"postgres:17-alpine":    {inventory.OriginUnknown, "", "", true},
			"pensum-app:latest":     {inventory.OriginBuilt, "von docker compose gebaut", "", true},
			"my-neko/helium:latest": {inventory.OriginUnknown, "", "", true},
			"":                      {inventory.OriginBuilt, "kein Registry-Digest", "", true},
		}, -1},
	}
	for _, tc := range cases {
		s := fixture.Beispiel(t, 1)
		s.Docker.Containerd = true
		s.Docker.API = tc.api
		inv := s.Run(t)
		for tag, w := range tc.images {
			img := imageByTag(inv, tag)
			if img == nil {
				t.Fatalf("API %s: Image %q fehlt", tc.api, tag)
			}
			if img.Origin != w.origin || img.Local != w.local || img.Source != w.source || (w.note != "" && img.OriginNote != w.note) {
				t.Errorf("API %s, %q: Herkunft %q (%q, Quelle %q), lokal %v; erwartet %q (%q, Quelle %q), lokal %v",
					tc.api, tag, img.Origin, img.OriginNote, img.Source, img.Local, w.origin, w.note, w.source, w.local)
			}
			if img.OriginNote == "" {
				t.Errorf("API %s, %q: keine Begründung für die Herkunft", tc.api, tag)
			}
			for _, ck := range img.UsedBy {
				if c := inv.Container(ck); c.ImageLocal != img.Local || c.ImageOrigin != img.Origin {
					t.Errorf("API %s, %s: Container sagt lokal %v/%q, Image %v/%q", tc.api, ck, c.ImageLocal, c.ImageOrigin, img.Local, img.Origin)
				}
			}
		}
		local := 0
		for _, img := range inv.Images {
			if img.Local && img.Running {
				local++
			}
		}
		if tc.local >= 0 && local != tc.local {
			t.Errorf("API %s: %d laufende Images werden gesichert, erwartet %d", tc.api, local, tc.local)
		}
		f := findings(inv, "image-herkunft-unklar")
		if len(f) != 1 || (tc.local >= 0 && !strings.Contains(f[0].Message, "my-neko/helium:latest")) {
			t.Errorf("API %s: Hinweis zur unklaren Herkunft fehlt oder falsch: %+v", tc.api, f)
		}
		b, _ := inv.Marshal()
		if strings.Contains(string(b), "geheim-build-ref") {
			t.Errorf("API %s: Build-Referenz im Inventar", tc.api)
		}
	}
}

// Mit dem klassischen Speicher bleibt alles wie bisher: Ein Digest heißt
// Registry.
func TestImageHerkunftKlassisch(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	for tag, origin := range map[string]string{
		"postgres:17-alpine": inventory.OriginRegistry,
		"pensum-app:latest":  inventory.OriginBuilt,
		"":                   inventory.OriginBuilt,
	} {
		if img := imageByTag(inv, tag); img == nil || img.Origin != origin {
			t.Errorf("%q: %+v, erwartet Herkunft %q", tag, img, origin)
		}
	}
	if n := len(findings(inv, "image-herkunft-unklar")); n != 0 {
		t.Errorf("%d Hinweise zur unklaren Herkunft, erwartet keinen", n)
	}
}
