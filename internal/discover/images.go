package discover

import (
	"context"
	"sort"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

func (s *scan) images(ctx context.Context) error {
	imgs, err := s.d.Docker.Images(ctx, s.inv.Docker.APIVersion)
	if err != nil {
		return err
	}
	byID := map[string]*inventory.Image{}
	for _, img := range imgs {
		ii := &inventory.Image{ID: img.ID, Size: img.Size, Tags: []string{}, Digests: []string{}, UsedBy: []string{}}
		for _, t := range img.RepoTags {
			if !strings.Contains(t, "<none>") {
				ii.Tags = append(ii.Tags, t)
			}
		}
		for _, d := range img.RepoDigests {
			if !strings.Contains(d, "<none>") {
				ii.Digests = append(ii.Digests, d)
			}
		}
		sort.Strings(ii.Tags)
		sort.Strings(ii.Digests)
		ii.Origin, ii.OriginNote, ii.Source = imageOrigin(img, ii)
		ii.Local = ii.Origin != inventory.OriginRegistry || len(ii.Tags) == 0
		byID[img.ID] = ii
	}
	used := map[string]bool{}
	for i := range s.inv.Containers {
		c := &s.inv.Containers[i]
		img := byID[c.ImageID]
		if img == nil {
			continue
		}
		c.ImageLocal = img.Local
		c.ImageOrigin = img.Origin
		img.UsedBy = append(img.UsedBy, c.Key)
		if c.Running {
			img.Running = true
		}
		used[img.ID] = true
	}
	// Nur Images, die ein Container nutzt. Ungenutzte Images werden beim
	// Umzug nicht gebraucht.
	for id := range used {
		s.inv.Images = append(s.inv.Images, *byID[id])
	}
	return nil
}

// imageOrigin bestimmt, woher ein Image stammt, und sagt in einem Halbsatz,
// woran HostDeck das erkennt. source ist das Registry-Repository, wenn Docker es
// kennt.
//
// Mit dem klassischen Image-Speicher (overlay2) ist die Sache einfach: Nur
// Images aus einer Registry haben einen RepoDigest. Mit dem
// containerd-Speicher, Standard ab Docker 29, hat jedes Image einen Digest,
// auch ein lokal gebautes; dort zählt, was Docker selbst über die Herkunft
// weiß (API 1.54 und neuer), und sonst, ob der Image-Index nur teilweise
// geladen ist, wie es nach einem docker pull für ein Image mit mehreren
// Plattformen der Fall ist.
func imageOrigin(img dockerapi.Image, ii *inventory.Image) (origin, note, source string) {
	var pulls []string
	built, partial := false, false
	for _, m := range img.Manifests {
		if !m.Available {
			partial = true
		}
		if m.ImageData == nil || m.ImageData.Identity == nil {
			continue
		}
		for _, p := range m.ImageData.Identity.Pull {
			if p.Repository != "" {
				pulls = appendUnique(pulls, p.Repository)
			}
		}
		if len(m.ImageData.Identity.Build) > 0 {
			built = true
		}
	}
	containerd := len(img.Manifests) > 0
	for _, d := range ii.Digests {
		if strings.HasSuffix(d, "@"+img.ID) {
			containerd = true
		}
	}
	switch {
	case len(pulls) > 0:
		src := pickSource(pulls, ii.Tags)
		return inventory.OriginRegistry, "laut Docker geladen aus " + src, src
	case built:
		return inventory.OriginBuilt, "laut Docker auf diesem Server gebaut", ""
	case !containerd && len(ii.Digests) > 0:
		return inventory.OriginRegistry, "hat einen Registry-Digest", ""
	case img.Labels[labelProject] != "" && img.Labels[labelService] != "":
		return inventory.OriginBuilt, "von docker compose gebaut", ""
	case !containerd:
		return inventory.OriginBuilt, "kein Registry-Digest", ""
	case partial:
		return inventory.OriginRegistry, "per docker pull geholt (Image-Index nur teilweise geladen)", ""
	default:
		return inventory.OriginUnknown, "Docker sagt nicht, ob es aus einer Registry stammt", ""
	}
}

// pickSource wählt aus den Repositories, aus denen Docker ein Image geladen
// hat, das zum Namen passende, sonst das erste.
func pickSource(pulls, tags []string) string {
	sort.Strings(pulls)
	for _, t := range tags {
		want := normalizeRepo(t)
		for _, p := range pulls {
			if p == want {
				return p
			}
		}
	}
	return pulls[0]
}

// normalizeRepo macht aus einem Image-Namen wie postgres:17 das volle
// Repository docker.io/library/postgres, so wie Docker es in der Herkunft
// angibt.
func normalizeRepo(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	first, _, hasSlash := strings.Cut(ref, "/")
	if !hasSlash {
		return "docker.io/library/" + ref
	}
	if !strings.ContainsAny(first, ".:") && first != "localhost" {
		return "docker.io/" + ref
	}
	return ref
}
