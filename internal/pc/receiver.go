// Package pc ist die Seite von HostDeck, die auf dem PC läuft: Sie nimmt das
// Backup entgegen, baut die SSH-Verbindung zum Server auf und startet dort
// den Lauf.
package pc

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/restic/rest-server/repo"
)

// Receiver ist der Empfänger auf dem PC: der Repository-Teil von
// rest-server, nur im Modus „nur anhängen“. Der Server kann darüber
// Snapshots hinzufügen, aber nichts löschen oder überschreiben; Ausnahme
// sind restic-Sperrdateien, die rest-server dafür vorsieht.
type Receiver struct {
	user, key string
	nonce     string
	reports   string
	repo      http.Handler
	// ReadOnly lässt nur Lesen zu, etwa beim Zurückspielen.
	ReadOnly bool
}

// NewReceiver legt einen Empfänger für das Repository in repoDir an.
// Berichte der Läufe landen in reportsDir.
func NewReceiver(repoDir, reportsDir, user, key, nonce string) (*Receiver, error) {
	h, err := repo.New(repoDir, repo.Options{AppendOnly: true, FsyncWarning: new(sync.Once)})
	if err != nil {
		return nil, err
	}
	return &Receiver{user: user, key: key, nonce: nonce, reports: reportsDir, repo: h}, nil
}

// quietLog nimmt die Meldungen von rest-server auf. Dass es beim ersten
// Lauf den Backup-Ordner anlegt, ist normal und wird nicht gezeigt.
type quietLog struct{ w io.Writer }

func (q quietLog) Write(p []byte) (int, error) {
	if bytes.HasPrefix(p, []byte("Creating repository directories")) {
		return len(p), nil
	}
	return q.w.Write(p)
}

// NewSecret erzeugt einen zufälligen Wert mit n Byte, als Hex.
func NewSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

var runID = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}$`)

func (rc *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, p, ok := r.BasicAuth()
	if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(rc.user)) != 1 ||
		subtle.ConstantTimeCompare([]byte(p), []byte(rc.key)) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="hostdeck"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/.hostdeck/hallo":
		// Damit der Server prüfen kann, dass am Tunnel wirklich dieser PC
		// antwortet und nicht ein anderes Programm auf dem Port.
		fmt.Fprint(w, rc.nonce)
		return
	case "/.hostdeck/bericht":
		if rc.ReadOnly {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		rc.report(w, r)
		return
	}
	if rc.ReadOnly && r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	rc.repo.ServeHTTP(w, r)
}

func (rc *Receiver) report(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("lauf")
	if r.Method != http.MethodPut || !runID.MatchString(id) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(rc.reports, 0o700); err != nil {
		log.Printf("Berichtsordner: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	name := filepath.Join(rc.reports, "backup-bericht-"+id+".txt")
	if err := os.WriteFile(name, body, 0o600); err != nil {
		log.Printf("Bericht speichern: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListRepos nennt die Unterordner von folder, in denen ein Backup liegt.
func ListRepos(folder string) []string {
	es, err := os.ReadDir(folder)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(folder, e.Name(), "config")); err != nil {
			continue
		}
		if fi, err := os.Stat(filepath.Join(folder, e.Name(), "keys")); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}
