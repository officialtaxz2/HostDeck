package caddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Das Journal hält die alten Fassungen der Caddy-Dateien auf der Platte,
// solange eine Änderung läuft. Stürzt HostDeck dabei ab (Strom, Absturz,
// Neustart), legt Recover sie beim nächsten Start zurück. Ohne Journal
// könnte ein halb geändertes Caddyfile liegen bleiben, mit dem Caddy beim
// nächsten Start nicht mehr hochkommt.

type journalFile struct {
	Path    string      `json:"pfad"`
	Existed bool        `json:"war_da"`
	Data    []byte      `json:"inhalt,omitempty"`
	Mode    fs.FileMode `json:"rechte"`
	UID     int         `json:"uid"`
	GID     int         `json:"gid"`
}

type journal struct {
	Note  string        `json:"auftrag"`
	Since time.Time     `json:"seit"`
	Files []journalFile `json:"dateien"`
}

func writeJournal(path, note string, files []saved) error {
	j := journal{Note: note, Since: time.Now()}
	for _, f := range files {
		j.Files = append(j.Files, journalFile{Path: f.path, Existed: f.existed, Data: f.data, Mode: f.mode, UID: f.uid, GID: f.gid})
	}
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := writeFile(path, b, 0o600, -1, -1); err != nil {
		return err
	}
	// Auch den Ordner sichern, damit die Umbenennung einen Stromausfall
	// übersteht.
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// PeekJournal sagt, ob ein Journal liegt und zu welchem Auftrag.
func PeekJournal(path string) (note string, found bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	var j journal
	if err := json.Unmarshal(b, &j); err != nil {
		return "", true, fmt.Errorf("Journal %s nicht lesbar: %v", path, err)
	}
	return j.Note, true, nil
}

// ClearJournal löscht das Journal, wenn die Änderung ganz fertig ist.
func ClearJournal(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Recover legt die Dateien aus einem liegengebliebenen Journal zurück und
// lädt Caddy neu, wenn c nicht nil ist (Caddy läuft). found: Es gab ein
// Journal; note sagt, welcher Auftrag unterbrochen wurde. Das Journal
// bleibt liegen, bis der Aufrufer ClearJournal ruft; ein zweites Recover
// legt dieselben Fassungen noch einmal zurück und schadet nicht.
func Recover(ctx context.Context, c *Caddy, path string) (note string, found bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	var j journal
	if err := json.Unmarshal(b, &j); err != nil {
		return "", true, fmt.Errorf("Journal %s nicht lesbar: %v", path, err)
	}
	var files []saved
	for _, f := range j.Files {
		files = append(files, saved{path: f.Path, existed: f.Existed, data: f.Data, mode: f.Mode, uid: f.UID, gid: f.GID})
	}
	if err := restore(files); err != nil {
		return j.Note, true, fmt.Errorf("Zurücklegen nach einer unterbrochenen Änderung ging nicht: %v. Bitte prüfen: %s", err, paths(files))
	}
	if c != nil {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		if err := c.Reload(rctx); err != nil {
			return j.Note, true, fmt.Errorf("Dateien nach einer unterbrochenen Änderung zurückgelegt, aber %v", err)
		}
	}
	return j.Note, true, nil
}
