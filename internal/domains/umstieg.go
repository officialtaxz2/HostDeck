package domains

import (
	"bytes"
	"os"
)

// Retitle ersetzt beim Umstieg von sbm auf HostDeck den Vermerk in der
// ersten Zeile der Caddy-Dateien, die die frühere Fassung geschrieben hat
// („# x.example: angelegt von sbm.“), und trägt die neue Prüfsumme ein. Dateien,
// die seitdem jemand geändert hat, bleiben unberührt. n: so viele Dateien.
func (s *Service) Retitle(from, to string) (int, error) {
	state := s.loadState()
	n := 0
	for file, want := range state {
		b, err := os.ReadFile(file)
		if err != nil || sum(b) != want {
			continue
		}
		first, rest, _ := bytes.Cut(b, []byte("\n"))
		if !bytes.HasPrefix(first, []byte("# ")) || !bytes.HasSuffix(first, []byte(from)) {
			continue
		}
		nb := append(append(bytes.TrimSuffix(bytes.Clone(first), []byte(from)), to...), '\n')
		nb = append(nb, rest...)
		fi, err := os.Stat(file)
		if err != nil {
			continue
		}
		tmp := file + ".neu"
		if err := os.WriteFile(tmp, nb, fi.Mode().Perm()); err != nil {
			return n, err
		}
		_ = os.Chmod(tmp, fi.Mode().Perm())
		if err := os.Rename(tmp, file); err != nil {
			os.Remove(tmp)
			return n, err
		}
		state[file] = sum(nb)
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.saveState(state)
}
