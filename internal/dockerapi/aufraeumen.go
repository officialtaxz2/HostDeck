package dockerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Cleaner ist der Zugang zum Aufräumen: lesen, was Platz
// belegt, den Build-Cache leeren, ein Image ohne Zwang löschen, ein
// Volume löschen und ein Volume für das Zurückholen anlegen. Docker lehnt
// das Löschen eines Images oder Volumes, das ein Container braucht, ohne
// Zwang selbst ab; Zwang („force“) lässt der Transport nie durch. Images
// des Rückfalls beim Aktualisieren (hostdeck-rueckfall/…) löscht Cleaner nie.
type Cleaner struct {
	http *http.Client
	base string
}

// RueckfallRepo ist der Name, unter dem das Aktualisieren den alten Stand
// eines Projekts aufhebt. Diese Images fasst das Aufräumen nie an.
const RueckfallRepo = "hostdeck-rueckfall/"

// legacyRueckfallRepo: So hießen diese Images, als HostDeck noch sbm hieß.
// Ältere Rückfälle bleiben genauso geschützt.
const legacyRueckfallRepo = "sbm-rueckfall/"

// IsFallback sagt, ob ref (oder ein Pfad, der ihn enthält) ein Rückfall-Image ist.
func IsFallback(ref string) bool {
	return strings.Contains(ref, RueckfallRepo) || strings.Contains(ref, legacyRueckfallRepo)
}

var cleanPaths = map[string]*regexp.Regexp{
	http.MethodGet:    regexp.MustCompile(`^(/v[0-9]+\.[0-9]+)?/(_ping|containers/json|images/json|volumes|system/df)$`),
	http.MethodDelete: regexp.MustCompile(`^(/v[0-9]+\.[0-9]+)?/(volumes/[A-Za-z0-9][A-Za-z0-9_.-]*|images/(sha256:[0-9a-f]{64}|[a-z0-9][a-z0-9._/:@-]*))$`),
	http.MethodPost:   regexp.MustCompile(`^(/v[0-9]+\.[0-9]+)?/(build/prune|volumes/create)$`),
}

type cleanTransport struct {
	base http.RoundTripper
}

func (t cleanTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	re := cleanPaths[r.Method]
	if re == nil || !re.MatchString(r.URL.Path) {
		return nil, fmt.Errorf("%w: %s %s ist für das Aufräumen nicht freigegeben", ErrNotAllowed, r.Method, r.URL.Path)
	}
	q := r.URL.Query()
	if v := q.Get("force"); v != "" && v != "0" && v != "false" {
		return nil, fmt.Errorf("%w: %s mit Zwang", ErrNotAllowed, r.URL.Path)
	}
	if r.Method == http.MethodDelete && IsFallback(r.URL.Path) {
		return nil, fmt.Errorf("%w: %s ist ein Rückfall-Image", ErrNotAllowed, r.URL.Path)
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/build/prune") && q.Get("filters") != "" {
		return nil, fmt.Errorf("%w: %s mit Filtern", ErrNotAllowed, r.URL.Path)
	}
	return t.base.RoundTrip(r)
}

// NewCleaner verbindet sich mit dem Docker-Socket unter socketPath.
func NewCleaner(socketPath string) *Cleaner {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		DisableCompression: true,
	}
	return newCleaner("http://docker", tr)
}

func newCleaner(base string, tr http.RoundTripper) *Cleaner {
	// Das Leeren des Build-Caches und das Messen der Volumes dauern auf
	// großen Platten Minuten.
	return &Cleaner{http: &http.Client{Transport: cleanTransport{base: tr}, Timeout: 20 * time.Minute}, base: base}
}

func (c *Cleaner) do(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

func (c *Cleaner) get(ctx context.Context, path string, q url.Values, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError(path, resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ErrInUse: Docker lehnt das Löschen ab, weil etwas das Image oder Volume
// noch braucht, oder weil ein Image mehrere Namen hat.
var ErrInUse = errors.New("wird noch gebraucht")

// ErrGone: Das Image oder Volume gibt es nicht (mehr).
var ErrGone = errors.New("gibt es nicht")

// Containers listet alle Container, auch gestoppte.
func (c *Cleaner) Containers(ctx context.Context) ([]OpsContainer, error) {
	var v []OpsContainer
	err := c.get(ctx, "/containers/json", url.Values{"all": {"1"}}, &v)
	return v, err
}

// Images listet alle Images, auch Zwischenstände ohne Namen.
func (c *Cleaner) Images(ctx context.Context) ([]Image, error) {
	var v []Image
	err := c.get(ctx, "/images/json", url.Values{"all": {"0"}}, &v)
	return v, err
}

// CleanVolume ist ein Volume mit dem, was das Aufräumen braucht. Options
// liest Cleaner nur, um Volumes mit besonderen Einstellungen (etwa ein
// Netzlaufwerk) auszulassen; sie können Zugangsdaten enthalten und
// werden nirgends gespeichert.
type CleanVolume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	Labels     map[string]string `json:"Labels"`
	Options    map[string]string `json:"Options"`
	CreatedAt  string            `json:"CreatedAt"`
}

// Volumes listet alle Volumes.
func (c *Cleaner) Volumes(ctx context.Context) ([]CleanVolume, error) {
	var v struct {
		Volumes []CleanVolume `json:"Volumes"`
	}
	err := c.get(ctx, "/volumes", nil, &v)
	return v.Volumes, err
}

// CacheRecord ist ein Eintrag im Build-Cache.
type CacheRecord struct {
	ID     string `json:"ID"`
	Size   int64  `json:"Size"`
	InUse  bool   `json:"InUse"`
	Shared bool   `json:"Shared"`
}

// DiskUse ist der Platz, den Docker belegt, wie docker system df.
type DiskUse struct {
	Images      int64            // alle Images zusammen
	Containers  int64            // beschreibbare Schichten der Container
	Volumes     int64            // alle Volumes zusammen
	BuildCache  int64            // ganzer Build-Cache
	CacheFree   int64            // davon nicht in Gebrauch
	VolumeSizes map[string]int64 // je Volume; -1 unbekannt
	ImageSizes  map[string]int64 // je Image-ID
}

type dfImage struct {
	ID         string `json:"Id"`
	Size       int64  `json:"Size"`
	SharedSize int64  `json:"SharedSize"`
}

type dfContainer struct {
	SizeRw int64 `json:"SizeRw"`
}

type dfVolume struct {
	Name      string `json:"Name"`
	UsageData *struct {
		Size int64 `json:"Size"`
	} `json:"UsageData"`
}

type dfReply struct {
	// Bis API 1.52.
	LayersSize int64         `json:"LayersSize"`
	Images     []dfImage     `json:"Images"`
	Containers []dfContainer `json:"Containers"`
	Volumes    []dfVolume    `json:"Volumes"`
	BuildCache []CacheRecord `json:"BuildCache"`
	// Ab API 1.52; die Einträge nur mit verbose=1.
	ImageUsage      *dfUsage[dfImage]     `json:"ImageUsage"`
	ContainerUsage  *dfUsage[dfContainer] `json:"ContainerUsage"`
	VolumeUsage     *dfUsage[dfVolume]    `json:"VolumeUsage"`
	BuildCacheUsage *dfUsage[CacheRecord] `json:"BuildCacheUsage"`
}

type dfUsage[T any] struct {
	TotalSize int64 `json:"TotalSize"`
	Items     []T   `json:"Items"`
}

// parseDF liest beide Formate von GET /system/df: das alte mit Listen
// direkt in der Antwort und das ab API 1.52 mit Summen und Einträgen.
func parseDF(b []byte) (DiskUse, error) {
	var r dfReply
	if err := json.Unmarshal(b, &r); err != nil {
		return DiskUse{}, err
	}
	u := DiskUse{VolumeSizes: map[string]int64{}, ImageSizes: map[string]int64{}}
	images, conts, vols, cache := r.Images, r.Containers, r.Volumes, r.BuildCache
	if r.ImageUsage != nil {
		images = r.ImageUsage.Items
		u.Images = r.ImageUsage.TotalSize
	}
	if r.ContainerUsage != nil {
		conts = r.ContainerUsage.Items
		u.Containers = r.ContainerUsage.TotalSize
	}
	if r.VolumeUsage != nil {
		vols = r.VolumeUsage.Items
		u.Volumes = r.VolumeUsage.TotalSize
	}
	if r.BuildCacheUsage != nil {
		cache = r.BuildCacheUsage.Items
		u.BuildCache = r.BuildCacheUsage.TotalSize
	}
	var sum int64
	for _, i := range images {
		u.ImageSizes[i.ID] = i.Size
		sum += i.Size - i.SharedSize
	}
	if r.ImageUsage == nil {
		u.Images = r.LayersSize
		if u.Images == 0 {
			u.Images = sum
		}
	}
	sum = 0
	for _, c := range conts {
		sum += c.SizeRw
	}
	if r.ContainerUsage == nil {
		u.Containers = sum
	}
	sum = 0
	for _, v := range vols {
		s := int64(-1)
		if v.UsageData != nil {
			s = v.UsageData.Size
		}
		u.VolumeSizes[v.Name] = s
		if s > 0 {
			sum += s
		}
	}
	if r.VolumeUsage == nil {
		u.Volumes = sum
	}
	sum = 0
	for _, c := range cache {
		if !c.Shared {
			sum += c.Size
		}
		if !c.InUse && !c.Shared {
			u.CacheFree += c.Size
		}
	}
	if r.BuildCacheUsage == nil {
		u.BuildCache = sum
	}
	return u, nil
}

// DiskUsage misst, was Docker belegt. Docker zählt dafür die Volumes durch;
// das dauert auf großen Platten eine Weile.
func (c *Cleaner) DiskUsage(ctx context.Context) (DiskUse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/system/df", url.Values{"verbose": {"1"}}, nil)
	if err != nil {
		return DiskUse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DiskUse{}, statusError("/system/df", resp)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return DiskUse{}, err
	}
	return parseDF(b)
}

func (c *Cleaner) remove(ctx context.Context, path string) error {
	resp, err := c.do(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", path, ErrGone)
	case http.StatusConflict:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: %s", ErrInUse, apiMessage(msg))
	}
	return statusError(path, resp)
}

func apiMessage(b []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &m) == nil && m.Message != "" {
		return m.Message
	}
	return strings.TrimSpace(string(b))
}

// RemoveImage löscht ein Image über seine ID (sha256:…) oder einen seiner
// Namen, nie mit Zwang. Hat es mehrere Namen, nimmt Docker über einen
// Namen nur diesen Namen weg; das Image verschwindet mit dem letzten.
func (c *Cleaner) RemoveImage(ctx context.Context, ref string) error {
	if ref == "" || IsFallback(ref) {
		return fmt.Errorf("%w: Image %q", ErrNotAllowed, ref)
	}
	return c.remove(ctx, "/images/"+ref)
}

// RemoveVolume löscht ein Volume, nie mit Zwang. Braucht es ein Container,
// lehnt Docker ab (ErrInUse).
func (c *Cleaner) RemoveVolume(ctx context.Context, name string) error {
	return c.remove(ctx, "/volumes/"+url.PathEscape(name))
}

// PruneBuildCache leert den Build-Cache bis auf das, was gerade in
// Gebrauch ist, und meldet den frei gewordenen Platz.
func (c *Cleaner) PruneBuildCache(ctx context.Context) (int64, error) {
	resp, err := c.do(ctx, http.MethodPost, "/build/prune", url.Values{"all": {"1"}}, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, statusError("/build/prune", resp)
	}
	var v struct {
		SpaceReclaimed int64 `json:"SpaceReclaimed"`
	}
	err = json.NewDecoder(resp.Body).Decode(&v)
	return v.SpaceReclaimed, err
}

// CreateVolume legt ein Volume mit dem Treiber local an und liefert seinen
// Ort auf der Platte.
func (c *Cleaner) CreateVolume(ctx context.Context, name string, labels map[string]string) (string, error) {
	body := map[string]any{"Name": name, "Driver": "local", "Labels": labels}
	resp, err := c.do(ctx, http.MethodPost, "/volumes/create", nil, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", statusError("/volumes/create", resp)
	}
	var v CleanVolume
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return v.Mountpoint, nil
}
