// Package dockerapi ist ein kleiner Client für die Docker Engine API, der nur
// lesen kann. Die Sperre sitzt im Transport: Jede Anfrage, die nicht GET oder
// HEAD auf einem freigegebenen Lese-Endpunkt ist, wird abgelehnt, bevor eine
// Verbindung zum Docker-Socket entsteht.
package dockerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultSocket ist der übliche Pfad des Docker-Sockets.
const DefaultSocket = "/var/run/docker.sock"

// ErrNotAllowed wird zurückgegeben, wenn eine Anfrage nicht nur lesend ist.
var ErrNotAllowed = errors.New("dockerapi: nur lesende Anfragen erlaubt")

// allowedPaths sind die einzigen Endpunkte, die der Client aufruft. Ein
// optionales Versionspräfix wie /v1.47 ist erlaubt. Bewusst fehlen Endpunkte,
// die per GET Daten verändern oder Container berühren können, etwa
// /containers/{id}/attach/ws, /containers/{id}/export oder /images/get.
var allowedPaths = regexp.MustCompile(`^(/v[0-9]+\.[0-9]+)?/(_ping|version|info|containers/json|containers/[A-Za-z0-9][A-Za-z0-9_.-]*/json|volumes|system/df|images/json|networks)$`)

type readOnlyTransport struct {
	base http.RoundTripper
}

func (t readOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return nil, fmt.Errorf("%w: %s %s", ErrNotAllowed, r.Method, r.URL.Path)
	}
	if !allowedPaths.MatchString(r.URL.Path) {
		return nil, fmt.Errorf("%w: %s %s ist nicht freigegeben", ErrNotAllowed, r.Method, r.URL.Path)
	}
	return t.base.RoundTrip(r)
}

// Client spricht mit dem Docker-Daemon.
type Client struct {
	http *http.Client
	base string
}

// New verbindet sich mit dem Docker-Socket unter socketPath.
func New(socketPath string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		DisableCompression: true,
	}
	return newClient("http://docker", tr)
}

func newClient(base string, tr http.RoundTripper) *Client {
	return &Client{
		http: &http.Client{Transport: readOnlyTransport{base: tr}, Timeout: 5 * time.Minute},
		base: base,
	}
}

// Do sendet eine Anfrage. Sie ist nur exportiert, damit Tests die Sperre
// prüfen können; der Rest des Programms nutzt die typisierten Methoden.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	resp, err := c.Do(ctx, http.MethodGet, path, query)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker %s: %s: %s", path, resp.Status, msg)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Version(ctx context.Context) (Version, error) {
	var v Version
	err := c.get(ctx, "/version", nil, &v)
	return v, err
}

func (c *Client) Info(ctx context.Context) (Info, error) {
	var v Info
	err := c.get(ctx, "/info", nil, &v)
	return v, err
}

// Containers listet alle Container, auch gestoppte. Mit withSize berechnet
// Docker zusätzlich die Größe der beschreibbaren Schicht (SizeRw).
func (c *Client) Containers(ctx context.Context, withSize bool) ([]ContainerSummary, error) {
	q := url.Values{"all": {"1"}}
	if withSize {
		q.Set("size", "1")
	}
	var v []ContainerSummary
	err := c.get(ctx, "/containers/json", q, &v)
	return v, err
}

func (c *Client) Inspect(ctx context.Context, id string) (Container, error) {
	var v Container
	err := c.get(ctx, "/containers/"+url.PathEscape(id)+"/json", nil, &v)
	return v, err
}

func (c *Client) Volumes(ctx context.Context) ([]Volume, error) {
	var v volumeList
	err := c.get(ctx, "/volumes", nil, &v)
	return v.Volumes, err
}

// VolumeSizes liefert die Größe jedes Volumes laut Docker, -1 wenn unbekannt.
// Ab API 1.52 stehen die Einträge nur mit verbose=1 in der Antwort, ab 1.53
// gibt es das alte Format nicht mehr; parseDF liest beide.
func (c *Client) VolumeSizes(ctx context.Context) (map[string]int64, error) {
	resp, err := c.Do(ctx, http.MethodGet, "/system/df", url.Values{"type": {"volume"}, "verbose": {"1"}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("docker /system/df: %s: %s", resp.Status, msg)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	u, err := parseDF(b)
	if err != nil {
		return nil, err
	}
	return u.VolumeSizes, nil
}

// Images listet alle Images. apiVersion ist die API-Version des Daemons; ab
// 1.47 fragt Images zusätzlich den Image-Index ab, ab 1.54 auch die Herkunft
// (identity). Docker beantwortet beides aus seinem eigenen Speicher, ohne eine
// Registry zu fragen. Scheitert die erweiterte Anfrage, etwa weil sich das
// noch als experimentell markierte Format geändert hat, fragt Images ohne
// Zusätze noch einmal.
func (c *Client) Images(ctx context.Context, apiVersion string) ([]Image, error) {
	q := url.Values{}
	if AtLeast(apiVersion, 1, 47) {
		q.Set("manifests", "1")
	}
	if AtLeast(apiVersion, 1, 54) {
		q.Set("identity", "1")
	}
	var v []Image
	err := c.get(ctx, "/images/json", q, &v)
	if err != nil && len(q) > 0 {
		v = nil
		err = c.get(ctx, "/images/json", nil, &v)
	}
	return v, err
}

// AtLeast meldet, ob die API-Version v (etwa "1.56") mindestens major.minor
// ist. Eine unlesbare Version gilt als zu alt.
func AtLeast(v string, major, minor int) bool {
	a, b, ok := strings.Cut(v, ".")
	if !ok {
		return false
	}
	ma, err1 := strconv.Atoi(a)
	mi, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return ma > major || (ma == major && mi >= minor)
}

func (c *Client) Networks(ctx context.Context) ([]Network, error) {
	var v []Network
	err := c.get(ctx, "/networks", nil, &v)
	return v, err
}
