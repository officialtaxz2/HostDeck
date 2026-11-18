package dockerapi

import (
	"context"
	"errors"
	"testing"
)

// Rückfall-Images aus der Zeit, als HostDeck noch sbm hieß, bleiben geschützt.
func TestRueckfallVonSbmGeschuetzt(t *testing.T) {
	for _, ref := range []string{"hostdeck-rueckfall/shop-web:vorher", "sbm-rueckfall/shop-web:vorher"} {
		if !IsFallback(ref) {
			t.Errorf("%s nicht als Rückfall erkannt", ref)
		}
		if err := (&Cleaner{}).RemoveImage(context.Background(), ref); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s: %v", ref, err)
		}
	}
	if IsFallback("nginx:latest") {
		t.Error("nginx als Rückfall")
	}
}
