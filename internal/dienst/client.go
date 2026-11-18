package dienst

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/web"
)

// Client spricht mit dem Dienst über seinen Unix-Socket.
type Client struct {
	Socket string
	hc     *http.Client
	tr     *http.Transport
}

// NewClient liefert einen Client für den Socket.
func NewClient(socket string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:    4,
		IdleConnTimeout: time.Minute,
	}
	return &Client{Socket: socket, tr: tr, hc: &http.Client{Transport: tr, Timeout: 30 * time.Second}}
}

var base = &url.URL{Scheme: "http", Host: "hostdeck-dienst"}

// Proxy reicht Anfragen an /api unverändert an den Dienst weiter.
func (c *Client) Proxy() http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(base)
			r.Out.Host = base.Host
		},
		Transport: c.tr,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeJSON(w, http.StatusBadGateway, apiError{Error: "Der HostDeck-Dienst antwortet nicht (" + err.Error() + "). Läuft er? systemctl status hostdeck-dienst"})
		},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base.String()+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("HostDeck-Dienst nicht erreichbar (%w)", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
			Wait  int    `json:"warten"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return &StatusError{Code: resp.StatusCode, Msg: e.Error, Wait: e.Wait}
		}
		return &StatusError{Code: resp.StatusCode, Msg: resp.Status}
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// StatusError ist eine Ablehnung durch den Dienst.
type StatusError struct {
	Code int
	Msg  string
	Wait int // Sekunden, bei einer Sperre
}

func (e *StatusError) Error() string { return e.Msg }

// ErrRejected: Der Dienst hat den Link abgelehnt.
var ErrRejected = errors.New("abgelehnt")

func code(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

// Redeem löst einen Einmal-Link ein und liefert die neue Anmeldung.
func (c *Client) Redeem(ctx context.Context, token string, v web.Visitor) (string, error) {
	var out sessionReply
	err := c.do(ctx, http.MethodPost, "/intern/link/einloesen", doorRequest{Token: token, Gate: v.Gate, Addr: v.Addr}, &out)
	if code(err) == http.StatusForbidden {
		return "", ErrRejected
	}
	return out.Session, err
}

// Door ist die Tür der Website, wenn der Dienst prüft.
func (c *Client) Door() web.Door { return clientDoor{c} }

type clientDoor struct{ c *Client }

func (d clientDoor) Redeem(ctx context.Context, token string, v web.Visitor) (string, error) {
	id, err := d.c.Redeem(ctx, token, v)
	if errors.Is(err, ErrRejected) {
		return "", web.ErrBadLink
	}
	return id, err
}

func (d clientDoor) Login(ctx context.Context, password string, v web.Visitor) (string, error) {
	var out sessionReply
	err := d.c.do(ctx, http.MethodPost, "/intern/anmelden", doorRequest{Password: password, Gate: v.Gate, Addr: v.Addr}, &out)
	var se *StatusError
	if errors.As(err, &se) {
		switch se.Code {
		case http.StatusUnauthorized:
			return "", web.ErrBadPassword
		case http.StatusTooManyRequests:
			return "", &web.LockedError{Wait: time.Duration(se.Wait) * time.Second}
		case http.StatusConflict:
			return "", web.ErrNoPassword
		}
	}
	return out.Session, err
}

func (d clientDoor) Check(ctx context.Context, id string, active bool) error {
	err := d.c.do(ctx, http.MethodPost, "/intern/sitzung", doorRequest{Session: id, Active: active}, nil)
	if code(err) == http.StatusUnauthorized {
		return web.ErrSession
	}
	return err
}

func (d clientDoor) Logout(ctx context.Context, id string, all bool, v web.Visitor) error {
	return d.c.do(ctx, http.MethodPost, "/intern/abmelden", doorRequest{Session: id, All: all, Gate: v.Gate, Addr: v.Addr}, nil)
}

// SetPassword legt das Leitstand-Passwort fest (nur als root).
func (c *Client) SetPassword(ctx context.Context, pw string) error {
	return c.do(ctx, http.MethodPost, "/intern/passwort", doorRequest{Password: pw}, nil)
}

// PasswordState sagt, ob ein Passwort festgelegt ist (nur als root).
func (c *Client) PasswordState(ctx context.Context) (PasswordState, error) {
	var st PasswordState
	err := c.do(ctx, http.MethodGet, "/intern/passwort", nil, &st)
	return st, err
}

// NewLink erzeugt einen Einmal-Link (nur als root).
func (c *Client) NewLink(ctx context.Context) (Link, error) {
	var l Link
	err := c.do(ctx, http.MethodPost, "/intern/link", nil, &l)
	return l, err
}

// Status fragt den Stand ab (nur als root).
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.do(ctx, http.MethodGet, "/intern/lauf", nil, &s)
	return s, err
}
