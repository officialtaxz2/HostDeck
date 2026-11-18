package leistung

import "testing"

// Swap-Zeilen aus der Zeit, als HostDeck noch sbm hieß, kommen beim
// Entfernen ebenso heraus.
func TestDropSwapLinesVonSbm(t *testing.T) {
	tab := "UUID=1 / ext4 defaults 0 1\n" + legacyFstabNote + "\n/swapfile-sbm none swap sw 0 0\n"
	if got := dropSwapLines(tab, "/swapfile-sbm"); got != "UUID=1 / ext4 defaults 0 1\n" {
		t.Errorf("%q", got)
	}
}
