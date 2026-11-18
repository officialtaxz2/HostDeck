package dockerapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// Control darf, was hostdeck backup braucht und discover nie bekommt: Container
// stoppen und starten, in einem Container einen Befehl ausführen (für
// Datenbank-Dumps) und Images exportieren. Auch hier gilt eine feste Liste
// von Endpunkten; Container anlegen, löschen oder verändern kann Control
// nicht.
type Control struct {
	http *http.Client
	base string
}

var controlPaths = map[string]*regexp.Regexp{
	http.MethodGet:  regexp.MustCompile(`^(/v[0-9]+\.[0-9]+)?/(_ping|version|containers/json|containers/[A-Za-z0-9][A-Za-z0-9_.-]*/json|exec/[A-Za-z0-9]+/json|images/get)$`),
	http.MethodPost: regexp.MustCompile(`^(/v[0-9]+\.[0-9]+)?/(containers/[A-Za-z0-9][A-Za-z0-9_.-]*/(stop|start|exec)|exec/[A-Za-z0-9]+/start)$`),
}

type controlTransport struct {
	base http.RoundTripper
}

func (t controlTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	re := controlPaths[r.Method]
	if re == nil || !re.MatchString(r.URL.Path) {
		return nil, fmt.Errorf("%w: %s %s ist für hostdeck backup nicht freigegeben", ErrNotAllowed, r.Method, r.URL.Path)
	}
	return t.base.RoundTrip(r)
}

// NewControl verbindet sich mit dem Docker-Socket unter socketPath.
func NewControl(socketPath string) *Control {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		DisableCompression: true,
	}
	return newControl("http://docker", tr)
}

func newControl(base string, tr http.RoundTripper) *Control {
	// Kein Gesamt-Zeitlimit: Dumps und Images können lange laufen. Jeder
	// Aufruf hat seinen eigenen Kontext.
	return &Control{http: &http.Client{Transport: controlTransport{base: tr}}, base: base}
}

func (c *Control) do(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
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

func statusError(path string, resp *http.Response) error {
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(msg, &m) == nil && m.Message != "" {
		return fmt.Errorf("docker %s: %s: %s", path, resp.Status, m.Message)
	}
	return fmt.Errorf("docker %s: %s: %s", path, resp.Status, bytes.TrimSpace(msg))
}

// ErrNoSuchContainer heißt: Den Container gibt es nicht (mehr).
var ErrNoSuchContainer = errors.New("Container gibt es nicht")

// State liefert den Zustand eines Containers.
func (c *Control) State(ctx context.Context, id string) (ContainerState, error) {
	path := "/containers/" + url.PathEscape(id) + "/json"
	resp, err := c.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return ContainerState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ContainerState{}, fmt.Errorf("%s: %w", id, ErrNoSuchContainer)
	}
	if resp.StatusCode != http.StatusOK {
		return ContainerState{}, statusError(path, resp)
	}
	var v Container
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return ContainerState{}, err
	}
	return v.State, nil
}

// Running listet die Namen der laufenden Container aus names.
func (c *Control) Running(ctx context.Context, names []string) ([]string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/containers/json", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError("/containers/json", resp)
	}
	var list []ContainerSummary
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []string
	for _, s := range list {
		for _, n := range s.Names {
			n = trimSlash(n)
			if want[n] && s.State == "running" {
				out = append(out, n)
			}
		}
	}
	return out, nil
}

func trimSlash(s string) string {
	if len(s) > 0 && s[0] == '/' {
		return s[1:]
	}
	return s
}

// Stop stoppt einen Container und wartet höchstens timeout, bevor Docker
// ihn hart beendet; 0 heißt: so lange, wie für den Container eingestellt
// ist (Standard 10 s). Ein schon gestoppter Container ist kein Fehler.
func (c *Control) Stop(ctx context.Context, id string, timeout time.Duration) error {
	path := "/containers/" + url.PathEscape(id) + "/stop"
	var q url.Values
	if timeout > 0 {
		q = url.Values{"t": {fmt.Sprint(int(timeout.Seconds()))}}
	}
	resp, err := c.do(ctx, http.MethodPost, path, q, nil)
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

// Start startet einen Container. Ein laufender Container ist kein Fehler.
func (c *Control) Start(ctx context.Context, id string) error {
	path := "/containers/" + url.PathEscape(id) + "/start"
	resp, err := c.do(ctx, http.MethodPost, path, nil, nil)
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

// Exec führt cmd im laufenden Container aus, schreibt dessen Ausgaben nach
// stdout und stderr und gibt den Exit-Code zurück.
func (c *Control) Exec(ctx context.Context, id string, cmd []string, stdout, stderr io.Writer) (int, error) {
	path := "/containers/" + url.PathEscape(id) + "/exec"
	resp, err := c.do(ctx, http.MethodPost, path, nil, map[string]any{
		"AttachStdout": true, "AttachStderr": true, "Tty": false, "Cmd": cmd,
	})
	if err != nil {
		return -1, err
	}
	var created struct {
		ID string `json:"Id"`
	}
	if resp.StatusCode != http.StatusCreated {
		err := statusError(path, resp)
		resp.Body.Close()
		return -1, err
	}
	err = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if err != nil || created.ID == "" {
		return -1, fmt.Errorf("docker exec: keine ID erhalten (%v)", err)
	}

	start := "/exec/" + url.PathEscape(created.ID) + "/start"
	resp, err = c.do(ctx, http.MethodPost, start, nil, map[string]any{"Detach": false, "Tty": false})
	if err != nil {
		return -1, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSwitchingProtocols {
		err := statusError(start, resp)
		resp.Body.Close()
		return -1, err
	}
	err = Demux(resp.Body, stdout, stderr)
	resp.Body.Close()
	if err != nil {
		return -1, err
	}

	inspect := "/exec/" + url.PathEscape(created.ID) + "/json"
	resp, err = c.do(ctx, http.MethodGet, inspect, nil, nil)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return -1, statusError(inspect, resp)
	}
	var st struct {
		Running  bool `json:"Running"`
		ExitCode int  `json:"ExitCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return -1, err
	}
	if st.Running {
		return -1, errors.New("docker exec: Befehl läuft nach Ende der Ausgabe noch")
	}
	return st.ExitCode, nil
}

// Demux trennt den Datenstrom von docker exec ohne TTY in Standardausgabe
// und Fehlerausgabe. Jeder Block beginnt mit 8 Byte: Strom (1 = stdout,
// 2 = stderr), drei Nullen und die Länge.
func Demux(r io.Reader, stdout, stderr io.Writer) error {
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("docker exec: Datenstrom bricht ab: %w", err)
		}
		n := int64(binary.BigEndian.Uint32(hdr[4:]))
		var w io.Writer = io.Discard
		switch hdr[0] {
		case 1:
			w = stdout
		case 2:
			w = stderr
		}
		if _, err := io.CopyN(w, r, n); err != nil {
			return fmt.Errorf("docker exec: Datenstrom bricht ab: %w", err)
		}
	}
}

// SaveImage schreibt die Images refs als Tar-Archiv nach w, wie docker save.
// platform (etwa {"os":"linux","architecture":"amd64"}) wählt bei
// Images mit mehreren Plattformen die des Servers; Docker vor API 1.48
// kennt das nicht, dann geht es ohne.
func (c *Control) SaveImage(ctx context.Context, refs []string, platform string, w io.Writer) error {
	q := url.Values{"names": refs}
	if platform != "" {
		q.Set("platform", platform)
	}
	resp, err := c.do(ctx, http.MethodGet, "/images/get", q, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusBadRequest && platform != "" {
		resp.Body.Close()
		return c.SaveImage(ctx, refs, "", w)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError("/images/get", resp)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// Version liefert die Docker-Version.
func (c *Control) Version(ctx context.Context) (Version, error) {
	resp, err := c.do(ctx, http.MethodGet, "/version", nil, nil)
	if err != nil {
		return Version{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Version{}, statusError("/version", resp)
	}
	var v Version
	err = json.NewDecoder(resp.Body).Decode(&v)
	return v, err
}
