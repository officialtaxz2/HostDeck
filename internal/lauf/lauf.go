// Package lauf sorgt dafür, dass auf einem Server immer nur ein Lauf von
// HostDeck zur Zeit arbeitet: Backup, Zurückspielen, Dienste starten oder
// Bestandsaufnahme. Egal, ob ihn der Leitstand, ein Befehl im Terminal oder
// hostdeck.exe gestartet hat, alle nehmen dieselbe Sperre (flock auf
// /run/hostdeck/lauf.lock). Stirbt ein Prozess, gibt der Kernel die Sperre frei.
package lauf

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultDir ist der Ordner für Sperre und Angaben zum laufenden Lauf.
// HOSTDECK_RUN_DIR ersetzt ihn, etwa für Tests.
const DefaultDir = "/run/hostdeck"

// Dir liefert den Ordner der Sperre.
func Dir() string {
	if d := os.Getenv("HOSTDECK_RUN_DIR"); d != "" {
		return d
	}
	return DefaultDir
}

const (
	lockFile = "lauf.lock"
	infoFile = "lauf.json"

	busyTries = 10
	busyWait  = 20 * time.Millisecond
)

// Leistungstest ist der Name des Laufs für benchmark.sh. Die Überwachung
// erkennt ihn daran: Er stoppt keine Container, lastet aber den Prozessor
// absichtlich voll aus.
const Leistungstest = "Leistungstest"

// Tuning ist der Name des Laufs für tune.sh (anwenden, zurücknehmen,
// CPU-Vorrang). Es stoppt nichts und lastet nichts aus; die Überwachung
// prüft währenddessen alles weiter.
const Tuning = "Tuning"

// Info beschreibt den Lauf, der die Sperre hält.
type Info struct {
	What  string    `json:"was"`  // etwa „Backup“
	Since time.Time `json:"seit"` // Start
	PID   int       `json:"pid"`
}

// Text ist die Beschreibung für Menschen.
func (i Info) Text() string {
	return fmt.Sprintf("%s seit %s (Prozess %d)", i.What, i.Since.Local().Format("15:04"), i.PID)
}

// BusyError meldet, dass schon ein Lauf arbeitet.
type BusyError struct{ Info *Info }

func (e *BusyError) Error() string {
	if e.Info == nil {
		return "Es läuft schon ein anderer Lauf von HostDeck. Bitte warten, bis er fertig ist."
	}
	return "Es läuft schon ein anderer Lauf von HostDeck: " + e.Info.Text() + ". Bitte warten, bis er fertig ist."
}

// Lock ist eine gehaltene Sperre.
type Lock struct {
	f   *os.File
	dir string
}

// Take nimmt die Sperre für einen Lauf, ohne zu warten. Ist sie belegt,
// kommt ein *BusyError.
func Take(what string) (*Lock, error) {
	return TakeIn(Dir(), what)
}

// TakeIn ist Take mit eigenem Ordner.
func TakeIn(dir, what string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := openLock(filepath.Join(dir, lockFile))
	if err != nil {
		return nil, err
	}
	for try := 0; ; try++ {
		err := tryLock(f)
		if err == nil {
			break
		}
		if !errors.Is(err, errBusy) {
			f.Close()
			return nil, err
		}
		// Ohne lauf.json hält die Sperre meist nur eine Abfrage von Current
		// für einen Augenblick (der Leitstand fragt regelmäßig nach), oder
		// ein Lauf fängt gerade erst an. Kurz warten statt abweisen.
		i := readInfo(dir)
		if i != nil || try >= busyTries {
			f.Close()
			return nil, &BusyError{Info: i}
		}
		time.Sleep(busyWait)
	}
	info := Info{What: what, Since: time.Now(), PID: os.Getpid()}
	b, _ := json.Marshal(info)
	_ = writeFile(filepath.Join(dir, infoFile), b)
	return &Lock{f: f, dir: dir}, nil
}

// Release gibt die Sperre frei.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = os.Remove(filepath.Join(l.dir, infoFile))
	_ = unlock(l.f)
	_ = l.f.Close()
	l.f = nil
}

// Current sagt, ob gerade ein Lauf arbeitet, und wenn ja, welcher. Ohne
// Lauf ist das Ergebnis nil.
func Current() (*Info, error) {
	return CurrentIn(Dir())
}

// CurrentIn ist Current mit eigenem Ordner.
func CurrentIn(dir string) (*Info, error) {
	f, err := os.Open(filepath.Join(dir, lockFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if errors.Is(err, os.ErrPermission) {
		// Die Sperre gehört root und ist nur für root lesbar. Ohne root
		// bleibt nur die Angabe zum Lauf, die jeder lesen darf.
		return readInfo(dir), nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := tryLock(f); err != nil {
		if errors.Is(err, errBusy) {
			if i := readInfo(dir); i != nil {
				return i, nil
			}
			return &Info{What: "ein Lauf"}, nil
		}
		return nil, err
	}
	_ = unlock(f)
	return nil, nil
}

func readInfo(dir string) *Info {
	b, err := os.ReadFile(filepath.Join(dir, infoFile))
	if err != nil {
		return nil
	}
	var i Info
	if json.Unmarshal(b, &i) != nil {
		return nil
	}
	return &i
}

func writeFile(path string, b []byte) error {
	tmp := path + ".neu"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
