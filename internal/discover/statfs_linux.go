//go:build linux

package discover

import "syscall"

// statfs gibt Größe und freien Platz des Dateisystems zurück, wie df: Die
// Reserve für root zählt weder als belegt noch als frei, sonst passt die
// Summe der Ordner nie zum Belegten.
func statfs(p string) (total, free int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return 0, 0, err
	}
	bs := int64(st.Bsize)
	used := int64(st.Blocks-st.Bfree) * bs
	free = int64(st.Bavail) * bs
	return used + free, free, nil
}
