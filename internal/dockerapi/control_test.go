package dockerapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func frame(stream byte, s string) []byte {
	b := make([]byte, 8+len(s))
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:], uint32(len(s)))
	copy(b[8:], s)
	return b
}

func TestControlExecUndStopp(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/containers/db/exec":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["Tty"] != false || body["AttachStdout"] != true {
				t.Errorf("exec-Anfrage: %v", body)
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"Id":"e1"}`))
		case r.URL.Path == "/exec/e1/start":
			w.Write(frame(1, "CREATE TABLE a;\n"))
			w.Write(frame(2, "Hinweis\n"))
			w.Write(frame(1, "-- fertig\n"))
		case r.URL.Path == "/exec/e1/json":
			w.Write([]byte(`{"Running":false,"ExitCode":0}`))
		case r.URL.Path == "/containers/db/stop":
			if r.URL.Query().Get("t") != "30" {
				t.Errorf("stop ohne Zeitlimit: %s", r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusNotModified)
		case r.URL.Path == "/containers/db/start":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/containers/weg/start":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"No such container"}`))
		case r.URL.Path == "/images/get":
			if r.URL.Query().Get("platform") != "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Write([]byte("TAR:" + strings.Join(r.URL.Query()["names"], ",")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newControl(srv.URL, http.DefaultTransport)
	ctx := context.Background()

	var out, errb bytes.Buffer
	code, err := c.Exec(ctx, "db", []string{"sh", "-c", "pg_dumpall"}, &out, &errb)
	if err != nil || code != 0 {
		t.Fatalf("Exec: %d %v", code, err)
	}
	if out.String() != "CREATE TABLE a;\n-- fertig\n" || errb.String() != "Hinweis\n" {
		t.Errorf("Ausgaben falsch getrennt: %q / %q", out.String(), errb.String())
	}
	if err := c.Stop(ctx, "db", 30*time.Second); err != nil {
		t.Errorf("Stop eines schon gestoppten Containers: %v", err)
	}
	if err := c.Start(ctx, "db"); err != nil {
		t.Errorf("Start: %v", err)
	}
	if err := c.Start(ctx, "weg"); !errors.Is(err, ErrNoSuchContainer) {
		t.Errorf("Start eines fehlenden Containers: %v", err)
	}
	var img bytes.Buffer
	if err := c.SaveImage(ctx, []string{"shop:lokal", "shop:1"}, `{"os":"linux"}`, &img); err != nil || img.String() != "TAR:shop:lokal,shop:1" {
		t.Errorf("SaveImage ohne Rückfall auf ohne platform: %q %v", img.String(), err)
	}
}

func TestControlSperrt(t *testing.T) {
	c := newControl("http://docker", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("Anfrage hätte nicht rausgehen dürfen: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("nein")
	}))
	ctx := context.Background()
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/containers/create"},
		{http.MethodDelete, "/containers/db"},
		{http.MethodPost, "/containers/db/kill"},
		{http.MethodPost, "/containers/db/update"},
		{http.MethodPost, "/images/load"},
		{http.MethodDelete, "/images/abc"},
		{http.MethodPost, "/volumes/prune"},
		{http.MethodGet, "/containers/db/export"},
		{http.MethodGet, "/containers/db/archive"},
	} {
		resp, err := c.do(ctx, tc.method, tc.path, nil, nil)
		if resp != nil {
			resp.Body.Close()
		}
		if !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s %s nicht gesperrt: %v", tc.method, tc.path, err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
