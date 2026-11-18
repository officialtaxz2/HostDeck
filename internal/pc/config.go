package pc

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Config sind die Einstellungen auf dem PC. Sie enthalten weder Passwörter
// noch Schlüssel.
type Config struct {
	Server string `json:"server,omitempty"` // etwa root@203.0.113.10
	Folder string `json:"folder,omitempty"` // Backup-Ordner
	Out    string `json:"out,omitempty"`    // Ergebnisordner auf dem Server, leer: HostDeck sucht
	Target string `json:"target,omitempty"` // Server, auf den zuletzt zurückgespielt wurde
}

// ConfigPath ist der Ort der Einstellungen, unter Windows etwa
// %AppData%\hostdeck\pc.json.
func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hostdeck", "pc.json"), nil
}

// LoadConfig liest die Einstellungen; fehlen sie, ist das kein Fehler.
func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

// Save schreibt die Einstellungen.
func (c Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// MinFree ist der Platz, den der Backup-Ordner haben sollte: das Backup von
// vps1 mit Reserve für weitere Läufe.
const MinFree = 60 << 30
