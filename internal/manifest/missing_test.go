package manifest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// /vmlinuz unter Debian: Die Bestandsaufnahme zählt es zum System, die
// Datei gibt es aber noch. Das ist kein „nicht mehr gefunden“.
func TestMissingLaesstVorhandenePfadeWeg(t *testing.T) {
	dir := t.TempDir()
	there := filepath.Join(dir, "vmlinuz")
	if err := os.Symlink("boot/vmlinuz-6.12", there); err != nil {
		t.Fatal(err)
	}
	got := Missing([]string{"path:" + there, "path:" + filepath.Join(dir, "weg"), "volume:alt"})
	want := []string{"path:" + filepath.Join(dir, "weg"), "volume:alt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Missing = %v, want %v", got, want)
	}
}
