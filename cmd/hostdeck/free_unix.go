//go:build unix

package main

import "syscall"

// diskFree ist der freie Platz für normale Benutzer auf der Platte von path.
func diskFree(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// diskSize ist Größe und freier Platz (für normale Benutzer) der Platte
// von path, wie df.
func diskSize(path string) (total, free int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Blocks) * int64(st.Bsize), int64(st.Bavail) * int64(st.Bsize), nil
}

// diskReserve ist, was das Dateisystem von path für root zurückhält.
func diskReserve(path string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil || st.Bfree < st.Bavail {
		return 0
	}
	return int64(st.Bfree-st.Bavail) * int64(st.Bsize)
}

// diskDF ist Größe und freier Platz wie in der Spalte Use% von df: Die
// Reserve für root (bei ext4 meist 5 %) zählt weder als belegt noch als
// frei. Sonst stünden auf einer leeren 160-GB-Platte 8 GB „belegt“.
func diskDF(path string) (total, free int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	used := int64(st.Blocks-st.Bfree) * int64(st.Bsize)
	free = int64(st.Bavail) * int64(st.Bsize)
	return used + free, free, nil
}
