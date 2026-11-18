package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Volumes mit o=bind binden einen Ordner des Servers ein.
func TestBindVolume(t *testing.T) {
	for _, c := range []struct {
		opt  map[string]string
		want bool
	}{
		{map[string]string{"type": "none", "o": "bind", "device": "/opt/daten"}, true},
		{map[string]string{"o": "rbind,ro", "device": "/opt/daten"}, true},
		{map[string]string{"o": "bind", "device": "relativ"}, false},
		{map[string]string{"type": "nfs", "o": "addr=1.2.3.4", "device": ":/export"}, false},
		{nil, false},
	} {
		if got := bindVolume(c.opt); got != c.want {
			t.Errorf("%v: %v", c.opt, got)
		}
	}
}

func TestPasswdHomes(t *testing.T) {
	f := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(f, []byte("root:x:0:0:root:/root:/bin/bash\ngit:x:998:998::/srv/git:/usr/bin/git-shell\nkaputt\nleer:x:1:1::relativ:/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := passwdHomes(f)
	if len(h) != 2 || h["root"] != "/root" || h["git"] != "/srv/git" {
		t.Errorf("%v", h)
	}
}
