package aktualisieren

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Registry fragt Registries, welches Image hinter einem Tag steht. Es liest
// nur: HEAD auf das Manifest, bei Bedarf mit einem Token. Docker Hub zählt
// HEAD-Anfragen nicht gegen die Grenze für Downloads.
type Registry struct {
	Client *http.Client
	// DockerConfig ist die config.json von Docker (für private Registries,
	// nur Einträge mit „auth“). Leer: ohne Anmeldung.
	DockerConfig string
	// Scheme ist https; Tests nehmen http.
	Scheme string
}

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json"

// ErrNoAccess heißt: Die Registry lässt ohne Anmeldung nicht nachsehen.
var ErrNoAccess = errors.New("die Registry verlangt eine Anmeldung")

// ErrNoSuchImage heißt: Die Registry kennt das Image oder den Tag nicht.
var ErrNoSuchImage = errors.New("unbekannt in der Registry")

type noSuchImage struct{ ref string }

func (e noSuchImage) Error() string        { return "die Registry kennt " + e.ref + " nicht (mehr)" }
func (e noSuchImage) Is(target error) bool { return target == ErrNoSuchImage }

func (g *Registry) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second}}
}

func (g *Registry) scheme() string {
	if g.Scheme != "" {
		return g.Scheme
	}
	return "https"
}

// Digest liefert den Digest, den der Tag der Referenz gerade in der
// Registry hat.
func (g *Registry) Digest(ctx context.Context, r Ref) (string, error) {
	if r.Pinned() {
		return r.Digest, nil
	}
	u := g.scheme() + "://" + r.apiHost() + "/v2/" + r.Repo + "/manifests/" + url.PathEscape(r.Tag)
	resp, err := g.manifest(ctx, http.MethodHead, u, "")
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		tok, err := g.token(ctx, r, resp.Header.Get("Www-Authenticate"))
		if err != nil {
			return "", err
		}
		if resp, err = g.manifest(ctx, http.MethodHead, u, tok); err != nil {
			return "", err
		}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrNoAccess
	case http.StatusNotFound:
		return "", noSuchImage{r.Name() + ":" + r.Tag}
	case http.StatusTooManyRequests:
		return "", errors.New("die Registry bremst gerade (zu viele Anfragen); später noch einmal")
	default:
		return "", fmt.Errorf("die Registry antwortet mit %s", resp.Status)
	}
	if d := resp.Header.Get("Docker-Content-Digest"); strings.HasPrefix(d, "sha256:") {
		return d, nil
	}
	// Manche Registries nennen den Digest nur beim GET; dann selbst rechnen.
	auth := resp.Request.Header.Get("Authorization")
	resp2, err := g.manifest(ctx, http.MethodGet, u, strings.TrimPrefix(auth, "Bearer "))
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return "", fmt.Errorf("die Registry antwortet mit %s", resp2.Status)
	}
	if d := resp2.Header.Get("Docker-Content-Digest"); strings.HasPrefix(d, "sha256:") {
		return d, nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(resp2.Body, 4<<20)); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (g *Registry) manifest(ctx context.Context, method, u, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", manifestAccept)
	req.Header.Set("User-Agent", "HostDeck (Nachsehen nach Updates)")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("Registry %s nicht erreichbar: %w", req.URL.Host, err)
	}
	return resp, nil
}

// token holt ein Token für das Lesen, so wie es WWW-Authenticate verlangt.
func (g *Registry) token(ctx context.Context, r Ref, challenge string) (string, error) {
	kind, params := parseChallenge(challenge)
	if !strings.EqualFold(kind, "Bearer") || params["realm"] == "" {
		return "", ErrNoAccess
	}
	realm, err := url.Parse(params["realm"])
	if err != nil || (realm.Scheme != "https" && realm.Scheme != g.scheme()) {
		return "", ErrNoAccess
	}
	q := realm.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	scope := params["scope"]
	if scope == "" {
		scope = "repository:" + r.Repo + ":pull"
	}
	q.Set("scope", scope)
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	if a := g.basicAuth(r.Host); a != "" {
		req.Header.Set("Authorization", "Basic "+a)
	}
	req.Header.Set("User-Agent", "HostDeck (Nachsehen nach Updates)")
	resp, err := g.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("Anmeldung bei der Registry nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ErrNoAccess
	}
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); err != nil {
		return "", fmt.Errorf("Antwort der Registry-Anmeldung unlesbar: %w", err)
	}
	if t.Token == "" {
		t.Token = t.AccessToken
	}
	if t.Token == "" {
		return "", ErrNoAccess
	}
	return t.Token, nil
}

// basicAuth liest den „auth“-Eintrag für host aus der config.json von
// Docker. Hilfsprogramme (credsStore) fragt HostDeck nicht.
func (g *Registry) basicAuth(host string) string {
	if g.DockerConfig == "" {
		return ""
	}
	b, err := os.ReadFile(g.DockerConfig)
	if err != nil {
		return ""
	}
	var cf struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if json.Unmarshal(b, &cf) != nil {
		return ""
	}
	keys := []string{host, "https://" + host}
	if host == "docker.io" {
		keys = append(keys, "https://index.docker.io/v1/", "index.docker.io", "registry-1.docker.io")
	}
	for _, k := range keys {
		if a := cf.Auths[k].Auth; a != "" {
			if _, err := base64.StdEncoding.DecodeString(a); err == nil {
				return a
			}
		}
	}
	return ""
}

// parseChallenge zerlegt „Bearer realm="…",service="…",scope="…"“.
func parseChallenge(h string) (string, map[string]string) {
	kind, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
	params := map[string]string{}
	for rest != "" {
		rest = strings.TrimLeft(rest, " ,")
		k, v, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if strings.HasPrefix(v, `"`) {
			end := strings.Index(v[1:], `"`)
			if end < 0 {
				params[k] = v[1:]
				break
			}
			params[k], rest = v[1:end+1], v[end+2:]
		} else {
			val, r, _ := strings.Cut(v, ",")
			params[k], rest = strings.TrimSpace(val), r
		}
	}
	return kind, params
}
