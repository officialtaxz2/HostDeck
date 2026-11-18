package dockerapi

import (
	"bytes"
	"context"
	"encoding/json"
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

// Ops ist der Zugang für den Betrieb im Leitstand: Container
// auflisten, ansehen, ihre Logs und Messwerte lesen, sie starten, stoppen
// und neu starten, und ein Image über seine ID ansehen. Mehr nicht:
// Container anlegen, löschen oder verändern, Befehle darin ausführen oder
// Images verändern kann Ops nicht. Die Liste
// sitzt wie bei Client und Control im Transport.
type Ops struct {
	http *http.Client
	base string
}

var opsPaths = map[string]*regexp.Regexp{
	http.MethodGet:  regexp.MustCompile(`^(/v[0-9]+\.[0-9]+)?/(_ping|containers/json|containers/[A-Za-z0-9][A-Za-z0-9_.-]*/(json|logs|stats|top)|images/sha256:[0-9a-f]{64}/json)$`),
	http.MethodPost: regexp.MustCompile(`^(/v[0-9]+\.[0-9]+)?/containers/[A-Za-z0-9][A-Za-z0-9_.-]*/(start|stop|restart|update)$`),
}

// restartPolicies sind die Neustart-Regeln, die Ops setzen darf. Mehr als
// die Regel ändert Ops an einem Container nie.
var restartPolicies = map[string]bool{"no": true, "always": true, "unless-stopped": true, "on-failure": true}

// checkUpdate lässt bei POST …/update nur einen Körper durch, der allein die
// Neustart-Regel setzt.
func checkUpdate(r *http.Request) error {
	if r.Body == nil {
		return fmt.Errorf("%w: %s ohne Inhalt", ErrNotAllowed, r.URL.Path)
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	r.Body.Close()
	if err != nil {
		return err
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(b, &v) != nil || len(v) != 1 || v["RestartPolicy"] == nil {
		return fmt.Errorf("%w: %s darf nur die Neustart-Regel ändern", ErrNotAllowed, r.URL.Path)
	}
	var rp map[string]json.RawMessage
	var name string
	if json.Unmarshal(v["RestartPolicy"], &rp) != nil || len(rp) != 1 || json.Unmarshal(rp["Name"], &name) != nil || !restartPolicies[name] {
		return fmt.Errorf("%w: %s mit unbekannter Neustart-Regel", ErrNotAllowed, r.URL.Path)
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	r.ContentLength = int64(len(b))
	return nil
}

var opsStream = regexp.MustCompile(`/(logs|stats|top)$`)

type opsTransport struct {
	base http.RoundTripper
}

func (t opsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	re := opsPaths[r.Method]
	if re == nil || !re.MatchString(r.URL.Path) {
		return nil, fmt.Errorf("%w: %s %s ist für den Betrieb nicht freigegeben", ErrNotAllowed, r.Method, r.URL.Path)
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/update") {
		if err := checkUpdate(r); err != nil {
			return nil, err
		}
	}
	if r.Method == http.MethodGet {
		// Nie mitlaufen lassen: Ein offener Strom hielte den Dienst fest.
		// Docker liefert Messwerte ohne stream=false als Strom.
		q := r.URL.Query()
		switch m := opsStream.FindStringSubmatch(r.URL.Path); {
		case m == nil:
		case m[1] == "logs" && q.Get("follow") != "" && q.Get("follow") != "0" && q.Get("follow") != "false":
			return nil, fmt.Errorf("%w: %s mit laufendem Strom", ErrNotAllowed, r.URL.Path)
		case m[1] == "stats" && q.Get("stream") != "false" && q.Get("stream") != "0":
			return nil, fmt.Errorf("%w: %s nur mit stream=false", ErrNotAllowed, r.URL.Path)
		case m[1] == "top" && (len(q) != 1 || q.Get("ps_args") != TopArgs):
			// Nur die feste Spaltenliste: ohne Befehlszeile, denn darin
			// stehen manchmal Passwörter.
			return nil, fmt.Errorf("%w: %s nur mit festen Spalten", ErrNotAllowed, r.URL.Path)
		}
	}
	return t.base.RoundTrip(r)
}

// NewOps verbindet sich mit dem Docker-Socket unter socketPath.
func NewOps(socketPath string) *Ops {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		DisableCompression: true,
	}
	return newOps("http://docker", tr)
}

func newOps(base string, tr http.RoundTripper) *Ops {
	return &Ops{http: &http.Client{Transport: opsTransport{base: tr}, Timeout: 3 * time.Minute}, base: base}
}

func (c *Ops) do(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
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

// OpsContainer ist ein Eintrag aus GET /containers/json mit dem, was der
// Betrieb braucht.
type OpsContainer struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	State   string            `json:"State"`  // running, exited, restarting, paused, created, dead
	Status  string            `json:"Status"` // etwa „Up 3 hours“
	Created int64             `json:"Created"`
	Labels  map[string]string `json:"Labels"`
	Mounts  []MountPoint      `json:"Mounts"`
	Ports   []ContainerPort   `json:"Ports"`
	// Netz des Containers (Spielserver fragt HostDeck über die Adresse des
	// Containers, wenn er keinen Port veröffentlicht).
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// ContainerPort ist ein Port eines Containers; PublicPort 0 heißt: nicht
// veröffentlicht.
type ContainerPort struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"` // tcp, udp, sctp
}

// Name ist der Name ohne führenden Schrägstrich.
func (c OpsContainer) Name() string {
	if len(c.Names) == 0 {
		return c.ID
	}
	return trimSlash(c.Names[0])
}

// List listet alle Container, auch gestoppte.
func (c *Ops) List(ctx context.Context) ([]OpsContainer, error) {
	resp, err := c.do(ctx, http.MethodGet, "/containers/json", url.Values{"all": {"1"}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError("/containers/json", resp)
	}
	var v []OpsContainer
	err = json.NewDecoder(resp.Body).Decode(&v)
	return v, err
}

// Inspect liefert die Einzelheiten eines Containers.
func (c *Ops) Inspect(ctx context.Context, id string) (Container, error) {
	path := "/containers/" + url.PathEscape(id) + "/json"
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return Container{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Container{}, fmt.Errorf("%s: %w", id, ErrNoSuchContainer)
	}
	if resp.StatusCode != http.StatusOK {
		return Container{}, statusError(path, resp)
	}
	var v Container
	err = json.NewDecoder(resp.Body).Decode(&v)
	return v, err
}

// Usage ist ein Messwert eines laufenden Containers.
type Usage struct {
	CPU      float64 // Prozent eines Kerns mal Kerne, wie docker stats
	Mem      int64   // belegt ohne Seiten-Cache
	MemLimit int64   // Grenze; ohne eigene Grenze der ganze Speicher
}

type statsReply struct {
	CPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
		Online int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	Mem struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

// Stats misst einen laufenden Container einmal. Docker wartet dafür etwa
// eine Sekunde, um zwei Werte für den Prozessor zu haben.
func (c *Ops) Stats(ctx context.Context, id string) (Usage, error) {
	path := "/containers/" + url.PathEscape(id) + "/stats"
	resp, err := c.do(ctx, http.MethodGet, path, url.Values{"stream": {"false"}})
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Usage{}, statusError(path, resp)
	}
	var s statsReply
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&s); err != nil {
		return Usage{}, err
	}
	return s.usage(), nil
}

func (s statsReply) usage() Usage {
	var u Usage
	cpu := float64(s.CPU.Usage.Total) - float64(s.PreCPU.Usage.Total)
	sys := float64(s.CPU.System) - float64(s.PreCPU.System)
	if cpu > 0 && sys > 0 {
		n := s.CPU.Online
		if n < 1 {
			n = 1
		}
		u.CPU = cpu / sys * float64(n) * 100
	}
	mem := s.Mem.Usage
	// Wie docker stats: Seiten-Cache zählt nicht (cgroup v2: inactive_file,
	// cgroup v1: total_inactive_file).
	for _, k := range []string{"inactive_file", "total_inactive_file"} {
		if v, ok := s.Mem.Stats[k]; ok && v < mem {
			mem -= v
			break
		}
	}
	u.Mem = int64(mem)
	u.MemLimit = int64(s.Mem.Limit)
	return u
}

// Logs liest die letzten tail Zeilen eines Containers, Standard- und
// Fehlerausgabe zusammen, mit Zeitstempel. tty: Der Container hat ein
// Terminal; dann liefert Docker den Strom ohne Rahmen.
func (c *Ops) Logs(ctx context.Context, id string, tail int, tty bool, max int64) ([]byte, error) {
	path := "/containers/" + url.PathEscape(id) + "/logs"
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "tail": {strconv.Itoa(tail)}}
	resp, err := c.do(ctx, http.MethodGet, path, q)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", id, ErrNoSuchContainer)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(path, resp)
	}
	var out limitBuf
	out.max = max
	body := io.Reader(resp.Body)
	if tty {
		_, err = io.Copy(&out, body)
	} else {
		err = Demux(body, &out, &markLines{w: &out, start: true})
	}
	if err != nil && !out.full {
		return nil, err
	}
	return out.b.Bytes(), nil
}

// StderrMark steht in den Logs vor jeder Zeile, die der Container auf
// stderr schrieb (nur ohne TTY; mit TTY gibt es nur einen Strom).
const StderrMark = "\x02"

// markLines setzt StderrMark an jeden Zeilenanfang.
type markLines struct {
	w     io.Writer
	start bool
}

func (m *markLines) Write(p []byte) (int, error) {
	var b bytes.Buffer
	for _, c := range p {
		if m.start {
			b.WriteString(StderrMark)
			m.start = false
		}
		b.WriteByte(c)
		if c == '\n' {
			m.start = true
		}
	}
	if _, err := m.w.Write(b.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Top ist, was docker top über die Prozesse eines Containers sagt.
type Top struct {
	Titles    []string   `json:"Titles"`
	Processes [][]string `json:"Processes"`
}

// TopArgs sind die Spalten, nach denen Ops Docker fragt: Kennung, Nutzer,
// Prozessor, Speicher, Laufzeit und Programmname. Die volle Befehlszeile
// (args) fehlt mit Absicht, weil Programme dort manchmal Passwörter bekommen.
const TopArgs = "-o pid,user,pcpu,rss,etime,comm"

// Top listet die Prozesse eines laufenden Containers (nur lesen).
func (c *Ops) Top(ctx context.Context, id string) (Top, error) {
	path := "/containers/" + url.PathEscape(id) + "/top"
	resp, err := c.do(ctx, http.MethodGet, path, url.Values{"ps_args": {TopArgs}})
	if err != nil {
		return Top{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Top{}, fmt.Errorf("%s: %w", id, ErrNoSuchContainer)
	}
	if resp.StatusCode != http.StatusOK {
		return Top{}, statusError(path, resp)
	}
	var t Top
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&t); err != nil {
		return Top{}, err
	}
	return t, nil
}

// limitBuf nimmt höchstens max Byte und verwirft den Rest still.
type limitBuf struct {
	b    bytes.Buffer
	max  int64
	full bool
}

func (l *limitBuf) Write(p []byte) (int, error) {
	n := len(p)
	if room := l.max - int64(l.b.Len()); l.max > 0 && int64(len(p)) > room {
		l.full = true
		if room > 0 {
			l.b.Write(p[:room])
		}
		return n, nil
	}
	l.b.Write(p)
	return n, nil
}

func (c *Ops) post(ctx context.Context, id, verb string, q url.Values) error {
	path := "/containers/" + url.PathEscape(id) + "/" + verb
	resp, err := c.do(ctx, http.MethodPost, path, q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusNotModified:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", id, ErrNoSuchContainer)
	}
	return statusError(path, resp)
}

func timeoutQuery(timeout time.Duration) url.Values {
	if timeout <= 0 {
		return nil
	}
	return url.Values{"t": {strconv.Itoa(int(timeout.Seconds()))}}
}

// SetRestartPolicy setzt die Neustart-Regel eines Containers (no, always,
// unless-stopped, on-failure). Sonst ändert es nichts am Container.
func (c *Ops) SetRestartPolicy(ctx context.Context, id, policy string) error {
	if !restartPolicies[policy] {
		return fmt.Errorf("%w: Neustart-Regel %q", ErrNotAllowed, policy)
	}
	path := "/containers/" + url.PathEscape(id) + "/update"
	b, _ := json.Marshal(map[string]any{"RestartPolicy": map[string]string{"Name": policy}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", id, ErrNoSuchContainer)
	}
	return statusError(path, resp)
}

// Start startet einen Container. Ein laufender ist kein Fehler.
func (c *Ops) Start(ctx context.Context, id string) error { return c.post(ctx, id, "start", nil) }

// Stop stoppt einen Container; nach timeout beendet Docker ihn hart (0: wie
// für den Container eingestellt). Ein gestoppter ist kein Fehler.
func (c *Ops) Stop(ctx context.Context, id string, timeout time.Duration) error {
	return c.post(ctx, id, "stop", timeoutQuery(timeout))
}

// Restart startet einen Container neu, auch einen gestoppten.
func (c *Ops) Restart(ctx context.Context, id string, timeout time.Duration) error {
	return c.post(ctx, id, "restart", timeoutQuery(timeout))
}

// ImageInfo ist ein Ausschnitt der Antwort von GET /images/{id}/json.
type ImageInfo struct {
	ID          string   `json:"Id"`
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
	Created     string   `json:"Created"`
}

// ImageInspect liefert Herkunft und Alter eines Images. Nur über die ID
// (sha256:…), damit kein Name die Liste der erlaubten Pfade aufweicht.
func (c *Ops) ImageInspect(ctx context.Context, id string) (ImageInfo, error) {
	path := "/images/" + id + "/json"
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return ImageInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ImageInfo{}, statusError(path, resp)
	}
	var v ImageInfo
	err = json.NewDecoder(resp.Body).Decode(&v)
	return v, err
}
