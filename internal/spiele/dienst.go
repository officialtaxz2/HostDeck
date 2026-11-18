package spiele

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
)

// Server ist ein erkannter Spielserver für den Leitstand. HostDeck fragt das
// Spiel nicht ab: Zustand und Verbrauch kommen wie bei jedem Container aus
// Docker; der Server sagt nur, dass es ein Spielserver ist und wie er heißt
// (keine Spielerzahlen, keine Sonderbehandlung).
type Server struct {
	ID        string `json:"id"`                  // Container-Name, bei gestoppten Panel-Servern der Ordner
	Game      string `json:"spiel"`               // „Minecraft Java“, „Left 4 Dead 2“, „Steam-Spiel“
	Short     string `json:"kuerzel"`             // „MC“, „L4D2“
	Name      string `json:"name"`                // aus den Dateien oder dem Container
	State     string `json:"zustand"`             // laeuft, startet, pausiert, aus
	Panel     string `json:"panel,omitempty"`     // Container des Panels
	Project   string `json:"projekt,omitempty"`   // Compose-Projekt
	Container string `json:"container,omitempty"` // fehlt, wenn das Panel ihn beim Stoppen gelöscht hat
	Found     string `json:"erkannt"`             // image, dateien
}

// Lister ist, was spiele von Docker braucht.
type Lister interface {
	List(ctx context.Context) ([]dockerapi.OpsContainer, error)
}

// Service sucht im Takt nach Spielservern. Es liest nur die Liste der
// Container und wenige Dateien; ins Netz geht es nicht.
type Service struct {
	Docker Lister
	FS     hostfs.FS
	// Every: so oft sucht HostDeck (Standard 30 s).
	Every time.Duration
	Now   func() time.Time

	mu      sync.Mutex
	servers []Server
	cache   map[string]files
	last    time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) every() time.Duration {
	if s.Every > 0 {
		return s.Every
	}
	return 30 * time.Second
}

// Run sucht, bis ctx endet.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(s.every())
	defer tick.Stop()
	s.Scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.Scan(ctx)
		}
	}
}

// Scan sucht einmal.
func (s *Service) Scan(ctx context.Context) {
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	list, err := s.Docker.List(lctx)
	cancel()
	if err != nil {
		return // beim nächsten Mal wieder; die letzte Liste bleibt
	}
	s.mu.Lock()
	fc := &fileCache{old: s.cache}
	s.mu.Unlock()
	cands := detect(list, reader{s.FS}, fc)
	var out []Server
	for _, c := range cands {
		if c.game == "" {
			continue // nicht erkannt: bleibt ein verwalteter Server
		}
		out = append(out, Server{ID: c.id, Game: c.game, Short: short(c.game), Name: nameOf(c), State: c.state, Panel: c.panel, Project: c.project,
			Container: c.container, Found: c.found})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].State == StateOff) != (out[j].State == StateOff) {
			return out[j].State == StateOff
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) || (strings.EqualFold(out[i].Name, out[j].Name) && out[i].ID < out[j].ID)
	})
	s.mu.Lock()
	s.servers, s.cache, s.last = out, fc.used, s.now()
	s.mu.Unlock()
}

// Standardnamen der Spiele: kein Name, an dem man einen Server erkennt.
var defaultNames = map[string]bool{"a minecraft server": true, "dedicated server": true, "": true}

// nameOf: Name aus den Dateien, sonst der Container (bei einer UUID die
// ersten 8 Zeichen).
func nameOf(c candidate) string {
	if n := clean(c.files.name, 60); !defaultNames[strings.ToLower(n)] {
		return n
	}
	if w := clean(c.files.level, 40); w != "" && w != "world" && w != "Bedrock level" {
		return "Welt " + w
	}
	if serverDirName.MatchString(c.id) {
		return c.id[:8]
	}
	return c.id
}

// Servers ist die letzte Liste (für /api/ueberwachung).
func (s *Service) Servers() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Server, len(s.servers))
	copy(out, s.servers)
	return out
}

// List wie Servers, typisiert (für Tests und andere Pakete).
func (s *Service) List() []Server {
	return s.Servers().([]Server)
}
