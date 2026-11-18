// Package aktualisieren bringt Projekte auf einen neuen Stand: Es sieht nach, ob es für ein Projekt Neues gibt (ein neueres Image
// in der Registry oder neue Commits im Git-Ordner), holt oder baut es auf
// Wunsch, startet die Dienste neu und prüft sie. Das alte Image bleibt als
// Rückfall; läuft das neue nicht sauber, stellt es den alten Stand wieder
// her.
package aktualisieren

import (
	"errors"
	"regexp"
	"strings"
)

var validTag = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// Ref ist eine Image-Referenz wie „postgres:17“ oder
// „ghcr.io/c0m4r/kula:latest“, so wie Docker sie versteht.
type Ref struct {
	Host   string // Registry, etwa docker.io oder ghcr.io
	Repo   string // etwa library/postgres
	Tag    string // leer, wenn Digest gesetzt ist
	Digest string // sha256:…, wenn die Referenz fest auf ein Image zeigt
}

// ErrBadRef heißt: Das ist keine Image-Referenz.
var ErrBadRef = errors.New("keine gültige Image-Referenz")

// ParseRef liest eine Referenz und ergänzt, was Docker ergänzt: Registry
// docker.io, „library/“ für offizielle Images, Tag latest.
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return Ref{}, ErrBadRef
	}
	var r Ref
	if name, dg, ok := strings.Cut(s, "@"); ok {
		if !strings.HasPrefix(dg, "sha256:") || len(dg) != 71 {
			return Ref{}, ErrBadRef
		}
		r.Digest, s = dg, name
	}
	// Ein Doppelpunkt nach dem letzten Schrägstrich trennt den Tag ab; einer
	// davor gehört zum Port der Registry.
	if i := strings.LastIndex(s, ":"); i > strings.LastIndex(s, "/") {
		r.Tag, s = s[i+1:], s[:i]
		if !validTag.MatchString(r.Tag) {
			return Ref{}, ErrBadRef
		}
	}
	first, rest, ok := strings.Cut(s, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		r.Host, r.Repo = first, rest
	} else {
		r.Host, r.Repo = "docker.io", s
	}
	if r.Host == "index.docker.io" || r.Host == "registry-1.docker.io" {
		r.Host = "docker.io"
	}
	if r.Host == "docker.io" && !strings.Contains(r.Repo, "/") {
		r.Repo = "library/" + r.Repo
	}
	if r.Repo == "" || strings.HasPrefix(r.Repo, "/") || strings.HasSuffix(r.Repo, "/") || r.Repo != strings.ToLower(r.Repo) {
		return Ref{}, ErrBadRef
	}
	if r.Tag == "" && r.Digest == "" {
		r.Tag = "latest"
	}
	return r, nil
}

// Name ist Registry und Repository, etwa docker.io/library/postgres.
func (r Ref) Name() string { return r.Host + "/" + r.Repo }

// Pinned heißt: Die Referenz zeigt fest auf ein Image; ein neueres gibt es
// für sie nie.
func (r Ref) Pinned() bool { return r.Digest != "" }

// apiHost ist der Rechner, der die Registry-API spricht.
func (r Ref) apiHost() string {
	if r.Host == "docker.io" {
		return "registry-1.docker.io"
	}
	return r.Host
}

// MatchDigest sagt, ob eine der RepoDigests eines Images („postgres@sha256:…“)
// zu dieser Referenz gehört (known) und ob eine davon digest ist (same).
func (r Ref) MatchDigest(repoDigests []string, digest string) (known, same bool) {
	for _, rd := range repoDigests {
		x, err := ParseRef(rd)
		if err != nil || x.Name() != r.Name() {
			continue
		}
		known = true
		if x.Digest == digest {
			same = true
		}
	}
	return known, same
}
