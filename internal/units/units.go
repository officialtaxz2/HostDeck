// Package units formatiert Größen für Menschen.
package units

import (
	"fmt"
	"strings"
)

// HumanSize formatiert Bytes wie docker mit Dezimal-Einheiten (1 GB = 1000³
// Byte) und deutschem Komma, damit die Zahlen zu docker ps -s passen.
func HumanSize(b int64) string {
	if b < 0 {
		return "?"
	}
	units := []string{"B", "kB", "MB", "GB", "TB"}
	f := float64(b)
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	var s string
	switch {
	case i == 0:
		s = fmt.Sprintf("%d", b)
	case f >= 100:
		s = fmt.Sprintf("%.0f", f)
	case f >= 10:
		s = fmt.Sprintf("%.1f", f)
	default:
		s = fmt.Sprintf("%.2f", f)
	}
	return strings.Replace(s, ".", ",", 1) + " " + units[i]
}
