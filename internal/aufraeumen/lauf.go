package aufraeumen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

// Schritte eines Laufs.
const (
	StepState   = "Stand prüfen"
	StepCache   = "Build-Cache leeren"
	StepImages  = "Images löschen"
	StepSpace   = "Platz für den Papierkorb prüfen"
	StepSave    = "In den Papierkorb sichern"
	StepCheck   = "Papierkorb prüfen"
	StepDelete  = "Gesichertes löschen"
	StepCreate  = "Volume anlegen"
	StepRestore = "Aus dem Papierkorb zurückspielen"
	StepForget  = "Aus dem Papierkorb nehmen"

	StateRunning = "laeuft"
	StateOK      = "ok"
	StateFailed  = "fehler"
)

// Event ist ein Schritt eines Laufs.
type Event struct {
	Step, State, Detail string
}

// Plan ist die Vorschau eines Laufs.
type Plan struct {
	Items   []Item   `json:"eintraege"`
	Steps   []string `json:"schritte"`
	Free    int64    `json:"frei_jetzt"`
	Gain    int64    `json:"gewinn"`             // höchstens so viel wird frei
	Trash   int64    `json:"papierkorb_groesse"` // so viel kommt in den Papierkorb
	Need    int64    `json:"platz_noetig"`       // so viel muss vorher frei sein
	Until   string   `json:"papierkorb_bis"`     // so lange lässt es sich zurückholen
	Problem string   `json:"problem,omitempty"`  // geht so nicht
	Notes   []string `json:"hinweise,omitempty"` // gut zu wissen
	Stand   string   `json:"stand"`
}

func (s *Service) pick(st *state, keys []string) ([]Item, error) {
	if len(keys) == 0 {
		return nil, ErrNothing
	}
	seen := map[string]bool{}
	var out []Item
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		it, ok := st.items[k]
		if !ok {
			return nil, fmt.Errorf("%s: %w", k, ErrNotFound)
		}
		out = append(out, it)
	}
	order := map[string]int{KindCache: 0, KindImage: 1, KindVolume: 2, KindFile: 3}
	sort.SliceStable(out, func(a, b int) bool { return order[out[a].Kind] < order[out[b].Kind] })
	return out, nil
}

// Plan zeigt, was ein Lauf mit den Vorschlägen keys täte.
func (s *Service) Plan(ctx context.Context, keys []string) (Plan, error) {
	st := s.state(ctx, false)
	if st.ov.Problem != "" {
		return Plan{}, fmt.Errorf("%w: %s", ErrBlocked, st.ov.Problem)
	}
	items, err := s.pick(st, keys)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Items: items, Free: st.ov.Disk.Free, Stand: fingerprint(items)}
	var cache, images, trash int
	var cacheGain int64
	for _, it := range items {
		if it.Size > 0 {
			p.Gain += it.Size
		}
		switch it.Kind {
		case KindCache:
			cache++
			cacheGain = it.Size
		case KindImage:
			images++
		default:
			trash++
			if it.Size > 0 {
				p.Trash += it.Size
			}
		}
	}
	if cache > 0 {
		p.Steps = append(p.Steps, "Build-Cache leeren: "+human(cacheGain)+", ohne Sicherung. Docker legt ihn beim nächsten Bauen neu an.")
	}
	if images > 0 {
		p.Steps = append(p.Steps, fmt.Sprintf("%d %s löschen, ohne Sicherung und ohne Zwang: Braucht ein Container eins doch, lehnt Docker ab und es bleibt.", images, plural(images, "Image", "Images")))
		p.Notes = append(p.Notes, "Images teilen sich oft Schichten. Frei wird deshalb manchmal weniger als ihre Größe zusammen.")
	}
	if trash > 0 {
		p.Need = p.Trash + s.reserve()
		until := s.now().Add(s.keep())
		p.Until = until.Format("02.01.2006")
		p.Steps = append(p.Steps,
			fmt.Sprintf("Prüfen, ob %s frei sind (Papierkorb %s und %s Reserve).", human(p.Need), human(p.Trash), human(s.reserve())),
			fmt.Sprintf("%d %s in den Papierkorb auf diesem Server sichern.", trash, plural(trash, "Eintrag", "Einträge")),
			"Den Papierkorb vollständig lesen und prüfen.",
			"Erst dann löschen. Was ein Container inzwischen doch braucht, bleibt.",
		)
		p.Notes = append(p.Notes, "Zurückholen geht bis "+p.Until+" unter „Papierkorb“ auf dieser Seite. Danach leert HostDeck den Papierkorb nachts von selbst.",
			"Der Papierkorb liegt auf dieser Platte. Was hineinkommt, ist sofort aus Docker weg, belegt aber bis zum "+p.Until+" noch Platz (restic packt es meist kleiner).")
		if st.ov.Disk.Free > 0 && st.ov.Disk.Free+cacheGain < p.Need {
			p.Problem = fmt.Sprintf("Für den Papierkorb fehlen %s: frei sind %s, nötig %s. Nimm weniger auf einmal oder leere vorher den Build-Cache.", human(p.Need-st.ov.Disk.Free-cacheGain), human(st.ov.Disk.Free), human(p.Need))
		}
	}
	return p, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// human schreibt eine Größe wie die Seite: 1,5 GB.
func human(b int64) string {
	const (
		kb = 1000
		mb = kb * 1000
		gb = mb * 1000
	)
	f := func(v float64, unit string) string {
		s := fmt.Sprintf("%.1f", v)
		if strings.HasSuffix(s, ".0") || v >= 100 {
			s = fmt.Sprintf("%.0f", v)
		}
		return strings.Replace(s, ".", ",", 1) + " " + unit
	}
	switch {
	case b >= gb:
		return f(float64(b)/gb, "GB")
	case b >= mb:
		return f(float64(b)/mb, "MB")
	case b >= kb:
		return f(float64(b)/kb, "KB")
	}
	return fmt.Sprintf("%d Byte", b)
}

// Run räumt die Vorschläge keys auf, wie die Vorschau mit stand es
// gezeigt hat, und meldet das Ergebnis in einem Satz.
func (s *Service) Run(ctx context.Context, keys []string, stand string, ev func(Event)) (string, error) {
	if ev == nil {
		ev = func(Event) {}
	}
	defer s.Refresh()
	ev(Event{StepState, StateRunning, ""})
	st := s.state(ctx, true)
	if st.ov.Problem != "" {
		ev(Event{StepState, StateFailed, st.ov.Problem})
		return "", fmt.Errorf("%w: %s", ErrBlocked, st.ov.Problem)
	}
	items, err := s.pick(st, keys)
	if err == nil && fingerprint(items) != stand {
		err = ErrChanged
	}
	if errors.Is(err, ErrNotFound) {
		err = ErrChanged
	}
	if err != nil {
		ev(Event{StepState, StateFailed, err.Error()})
		return "", err
	}
	ev(Event{StepState, StateOK, ""})
	before := s.free()

	var images, trash []Item
	var notes []string
	for _, it := range items {
		switch it.Kind {
		case KindCache:
			ev(Event{StepCache, StateRunning, human(it.Size)})
			n, err := s.Docker.PruneBuildCache(ctx)
			if err != nil {
				ev(Event{StepCache, StateFailed, err.Error()})
				return "", fmt.Errorf("Build-Cache leeren: %w", err)
			}
			ev(Event{StepCache, StateOK, human(n) + " frei"})
		case KindImage:
			images = append(images, it)
		default:
			trash = append(trash, it)
		}
	}
	if len(images) > 0 {
		ev(Event{StepImages, StateRunning, fmt.Sprintf("%d %s", len(images), plural(len(images), "Image", "Images"))})
		gone, stay := 0, 0
		for _, it := range images {
			if err := s.removeImage(ctx, it); err != nil {
				if !errors.Is(err, dockerapi.ErrInUse) && !errors.Is(err, dockerapi.ErrGone) {
					ev(Event{StepImages, StateFailed, it.Name + ": " + err.Error()})
					return "", fmt.Errorf("Image %s löschen: %w", it.Name, err)
				}
				if errors.Is(err, dockerapi.ErrInUse) {
					stay++
					continue
				}
			}
			gone++
		}
		d := fmt.Sprintf("%d gelöscht", gone)
		if stay > 0 {
			d += fmt.Sprintf(", %d %s, weil Docker %s noch braucht", stay, plural(stay, "bleibt", "bleiben"), plural(stay, "es", "sie"))
			notes = append(notes, fmt.Sprintf("%d %s, weil Docker %s noch braucht.", stay, plural(stay, "Image bleibt", "Images bleiben"), plural(stay, "es", "sie")))
		}
		ev(Event{StepImages, StateOK, d})
	}
	var saved int
	if len(trash) > 0 {
		n, more, err := s.trashRun(ctx, trash, ev)
		if err != nil {
			return "", err
		}
		saved = n
		notes = append(notes, more...)
	}
	after := s.free()
	msg := "Aufgeräumt."
	if before >= 0 && after >= 0 {
		msg = fmt.Sprintf("Aufgeräumt: %s frei geworden (vorher %s frei, jetzt %s).", human(max(after-before, 0)), human(before), human(after))
	}
	if saved > 0 {
		msg += fmt.Sprintf(" %d %s im Papierkorb bis %s.", saved, plural(saved, "Eintrag liegt", "Einträge liegen"), s.now().Add(s.keep()).Format("02.01.2006"))
	}
	if len(notes) > 0 {
		msg += " " + strings.Join(notes, " ")
	}
	return msg, nil
}

// free ist der freie Platz auf der Platte der Seite; -1 unbekannt.
func (s *Service) free() int64 {
	if s.Disk == nil {
		return -1
	}
	_, f, err := s.Disk(s.diskPath())
	if err != nil {
		return -1
	}
	return f
}

func (s *Service) removeImage(ctx context.Context, it Item) error {
	// Hat ein Image mehrere Namen, nimmt Docker beim Löschen über die ID
	// ohne Zwang nichts weg. Über die Namen verschwindet es mit dem letzten.
	var last error
	for _, r := range it.refs {
		if err := s.Docker.RemoveImage(ctx, r); err != nil {
			if errors.Is(err, dockerapi.ErrGone) {
				continue
			}
			last = err
		}
	}
	return last
}

type saved struct {
	it    Item
	entry Entry
}

func (s *Service) trashRun(ctx context.Context, items []Item, ev func(Event)) (int, []string, error) {
	var need int64
	for _, it := range items {
		if it.Size > 0 {
			need += it.Size
		}
	}
	need += s.reserve()
	ev(Event{StepSpace, StateRunning, ""})
	if s.Disk != nil {
		if err := os.MkdirAll(s.Dir, 0o700); err != nil {
			ev(Event{StepSpace, StateFailed, err.Error()})
			return 0, nil, err
		}
		_, free, err := s.Disk(s.Dir)
		if err != nil {
			ev(Event{StepSpace, StateFailed, err.Error()})
			return 0, nil, err
		}
		if free < need {
			d := fmt.Sprintf("frei %s, nötig %s", human(free), human(need))
			ev(Event{StepSpace, StateFailed, d})
			return 0, nil, fmt.Errorf("%w (%s); nichts davon ist gelöscht", ErrNoSpace, d)
		}
		ev(Event{StepSpace, StateOK, fmt.Sprintf("frei %s, nötig %s", human(free), human(need))})
	} else {
		ev(Event{StepSpace, StateOK, "nicht messbar"})
	}

	ev(Event{StepSave, StateRunning, fmt.Sprintf("0 von %d", len(items))})
	var done []saved
	var notes []string
	forget := func(ids []string) {
		if len(ids) > 0 {
			_ = s.Trash.Forget(context.WithoutCancel(ctx), ids)
		}
	}
	allIDs := func() []string {
		var ids []string
		for _, d := range done {
			ids = append(ids, d.entry.ID)
		}
		return ids
	}
	for i, it := range items {
		e := Entry{Kind: it.Kind, Name: it.Name, Path: it.path, Size: it.Size, Labels: it.label}
		if it.Kind == KindVolume {
			e.Name = strings.TrimPrefix(it.Key, KindVolume+":")
		}
		if fi, err := os.Lstat(it.path); err == nil {
			e.Mode = uint32(fi.Mode().Perm())
			e.UID, e.GID = owner(fi)
		} else {
			ev(Event{StepSave, StateFailed, it.Name + ": " + err.Error()})
			forget(allIDs())
			return 0, nil, fmt.Errorf("%s: %w; nichts davon ist gelöscht", it.Name, err)
		}
		tags := []string{trashTag, "art:" + it.Kind}
		id, unreadable, err := s.Trash.Save(ctx, []string{it.path}, tags)
		if err != nil {
			ev(Event{StepSave, StateFailed, it.Name + ": " + err.Error()})
			forget(allIDs())
			return 0, nil, fmt.Errorf("%s sichern: %w; nichts davon ist gelöscht", it.Name, err)
		}
		if len(unreadable) > 0 {
			forget([]string{id})
			notes = append(notes, it.Name+" bleibt: "+fmt.Sprint(len(unreadable))+" Dateien darin waren nicht lesbar.")
			continue
		}
		e.ID = id
		done = append(done, saved{it, e})
		ev(Event{StepSave, StateRunning, fmt.Sprintf("%d von %d", i+1, len(items))})
	}
	ev(Event{StepSave, StateOK, fmt.Sprintf("%d gesichert", len(done))})
	if len(done) == 0 {
		return 0, notes, nil
	}

	ev(Event{StepCheck, StateRunning, ""})
	if err := s.Trash.Check(ctx); err != nil {
		ev(Event{StepCheck, StateFailed, err.Error()})
		forget(allIDs())
		return 0, nil, fmt.Errorf("Papierkorb prüfen: %w; nichts davon ist gelöscht", err)
	}
	ev(Event{StepCheck, StateOK, ""})

	ev(Event{StepDelete, StateRunning, ""})
	n := 0
	now := s.now()
	var gone []string
	for _, d := range done {
		var err error
		switch d.it.Kind {
		case KindVolume:
			err = s.Docker.RemoveVolume(ctx, d.entry.Name)
		case KindFile:
			err = removeIfSame(d.it)
		case KindPath:
			var partial bool
			partial, err = d.it.remove(ctx)
			d.entry.Partial = partial
		}
		if err != nil {
			forget([]string{d.entry.ID})
			why := err.Error()
			if errors.Is(err, dockerapi.ErrInUse) {
				why = "ein Container braucht es inzwischen"
			}
			notes = append(notes, d.it.Name+" bleibt: "+why+".")
			continue
		}
		d.entry.Deleted, d.entry.Until = now, now.Add(s.keep())
		if err := s.addEntry(d.entry); err != nil {
			// Gelöscht ist es; der Snapshot ohne Eintrag fällt nach Keep
			// bei der nächtlichen Pflege weg (Expire).
			notes = append(notes, d.it.Name+": Liste des Papierkorbs nicht geschrieben ("+err.Error()+").")
		}
		n++
		if k := selectionKey(d.it); k != "" {
			gone = append(gone, k)
		}
	}
	ev(Event{StepDelete, StateOK, fmt.Sprintf("%d gelöscht", n)})
	return n, append(notes, s.unselect(gone)...), nil
}

// selectionKey ist der Schlüssel, unter dem die Auswahl (manifest.yaml)
// einen Eintrag führt.
func selectionKey(it Item) string {
	switch it.Kind {
	case KindVolume:
		return it.Key
	case KindPath:
		return "" // trägt der Aufrufer samt allem darin aus
	}
	return "path:" + it.path
}

// unselect trägt Gelöschtes aus der Auswahl aus und sagt es in einem Satz.
func (s *Service) unselect(keys []string) []string {
	if s.Unselect == nil || len(keys) == 0 {
		return nil
	}
	n, err := s.Unselect(keys)
	switch {
	case err != nil:
		return []string{"Deine Auswahl ist nicht angepasst (" + err.Error() + "); vor dem nächsten Backup unter Auswahl die nicht mehr gefundenen Einträge entfernen."}
	case n > 0:
		return []string{fmt.Sprintf("%d %s aus deiner Auswahl ausgetragen.", n, plural(n, "Eintrag", "Einträge"))}
	}
	return nil
}

// removeIfSame löscht eine Datei nur, wenn sie seit der Vorschau gleich
// geblieben ist.
func removeIfSame(it Item) error {
	fi, err := os.Lstat(it.path)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%s|%d|%d", it.path, fi.Size(), fi.ModTime().UnixNano()) != it.id || !fi.Mode().IsRegular() {
		return errors.New("hat sich inzwischen geändert")
	}
	return os.Remove(it.path)
}

// Restore holt den Eintrag id aus dem Papierkorb zurück.
func (s *Service) Restore(ctx context.Context, id string, ev func(Event)) (string, error) {
	if ev == nil {
		ev = func(Event) {}
	}
	defer s.Refresh()
	var e *Entry
	for _, x := range s.entries() {
		if x.ID == id {
			x := x
			e = &x
		}
	}
	if e == nil {
		return "", ErrNotFound
	}
	switch e.Kind {
	case KindVolume:
		if err := s.restoreVolume(ctx, *e, ev); err != nil {
			return "", err
		}
	case KindFile:
		if err := s.restoreFile(ctx, *e, ev); err != nil {
			return "", err
		}
	case KindPath:
		if err := s.restorePath(ctx, *e, ev); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unbekannte Art %q", e.Kind)
	}
	key := e.Kind + ":" + e.Path
	if e.Kind == KindVolume {
		key = KindVolume + ":" + e.Name
	}
	_ = s.markRestored(key)
	ev(Event{StepForget, StateRunning, ""})
	_ = s.Trash.Forget(context.WithoutCancel(ctx), []string{e.ID})
	if err := s.dropEntries(map[string]bool{e.ID: true}); err != nil {
		ev(Event{StepForget, StateFailed, err.Error()})
	} else {
		ev(Event{StepForget, StateOK, ""})
	}
	return e.Name + " ist zurück.", nil
}

func (s *Service) restoreVolume(ctx context.Context, e Entry, ev func(Event)) error {
	ev(Event{StepCreate, StateRunning, e.Name})
	vols, err := s.Docker.Volumes(ctx)
	if err != nil {
		ev(Event{StepCreate, StateFailed, err.Error()})
		return err
	}
	for _, v := range vols {
		if v.Name == e.Name {
			err := fmt.Errorf("%w: Es gibt schon wieder ein Volume %s; HostDeck überschreibt es nicht", ErrBlocked, e.Name)
			ev(Event{StepCreate, StateFailed, err.Error()})
			return err
		}
	}
	mp, err := s.Docker.CreateVolume(ctx, e.Name, e.Labels)
	if err == nil && !cleanVolumePath(mp) {
		err = fmt.Errorf("Docker legt das Volume an einem ungewöhnlichen Ort an: %s", mp)
	}
	if err != nil {
		ev(Event{StepCreate, StateFailed, err.Error()})
		return err
	}
	ev(Event{StepCreate, StateOK, ""})
	ev(Event{StepRestore, StateRunning, human(e.Size)})
	if err := s.Trash.Restore(ctx, e.ID, e.Path, mp); err != nil {
		ev(Event{StepRestore, StateFailed, err.Error()})
		_ = s.Docker.RemoveVolume(context.WithoutCancel(ctx), e.Name)
		return fmt.Errorf("zurückspielen: %w; das leere Volume ist wieder weg", err)
	}
	if e.Mode != 0 {
		_ = os.Chmod(mp, os.FileMode(e.Mode))
	}
	_ = os.Lchown(mp, e.UID, e.GID)
	ev(Event{StepRestore, StateOK, ""})
	return nil
}

func (s *Service) restoreFile(ctx context.Context, e Entry, ev func(Event)) error {
	ev(Event{StepRestore, StateRunning, e.Path})
	if _, err := os.Lstat(e.Path); err == nil {
		err := fmt.Errorf("%w: %s gibt es schon wieder; HostDeck überschreibt es nicht", ErrBlocked, e.Path)
		ev(Event{StepRestore, StateFailed, err.Error()})
		return err
	}
	tmp, err := os.MkdirTemp(s.Dir, "zurueck-")
	if err != nil {
		ev(Event{StepRestore, StateFailed, err.Error()})
		return err
	}
	defer os.RemoveAll(tmp)
	if err := s.Trash.Restore(ctx, e.ID, "", tmp); err != nil {
		ev(Event{StepRestore, StateFailed, err.Error()})
		return err
	}
	if err := copyBack(filepath.Join(tmp, e.Path), e); err != nil {
		ev(Event{StepRestore, StateFailed, err.Error()})
		return err
	}
	ev(Event{StepRestore, StateOK, ""})
	return nil
}

// restorePath legt einen Ordner oder eine Datei aus dem Papierkorb an
// den alten Ort zurück: erst vollständig in einen Ordner neben dem
// Papierkorb, dann mit einem Umbenennen an Ort und Stelle. So steht dort
// nie eine halbe Kopie, und nichts wird überschrieben.
func (s *Service) restorePath(ctx context.Context, e Entry, ev func(Event)) error {
	ev(Event{StepRestore, StateRunning, e.Path})
	fail := func(err error) error {
		ev(Event{StepRestore, StateFailed, err.Error()})
		return err
	}
	// Der Schutz von Speicherplatz gilt auch beim Zurücklegen: Ist der Ort
	// inzwischen gesperrt (etwa ein neues Projekt), legt HostDeck nichts hin.
	if s.RestoreCheck != nil {
		if err := s.RestoreCheck(ctx, e.Path); err != nil {
			return fail(fmt.Errorf("%w: %v", ErrBlocked, err))
		}
	}
	if _, err := os.Lstat(e.Path); err == nil && !e.Partial {
		return fail(fmt.Errorf("%w: %s gibt es schon wieder; HostDeck überschreibt es nicht", ErrBlocked, e.Path))
	}
	if s.Disk != nil && e.Size > 0 {
		if _, free, err := s.Disk(s.Dir); err == nil && free < e.Size+s.reserve() {
			return fail(fmt.Errorf("%w (frei %s, nötig %s)", ErrNoSpace, human(free), human(e.Size+s.reserve())))
		}
	}
	tmp, err := os.MkdirTemp(s.Dir, "zurueck-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(tmp)
	if err := s.Trash.Restore(ctx, e.ID, "", tmp); err != nil {
		return fail(err)
	}
	src := filepath.Join(tmp, e.Path)
	if _, err := os.Lstat(src); err != nil {
		return fail(fmt.Errorf("im Papierkorb fehlt %s", e.Path))
	}
	// Ordner für Ordner ohne Verknüpfungen bis an den alten Ort; dort wird
	// nichts überschrieben. War nur ein Teil gelöscht, kommt das Fehlende
	// dazu.
	kept, err := placeBack(src, e.Path, e.Partial)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fail(fmt.Errorf("%w: %s gibt es schon wieder; HostDeck überschreibt es nicht", ErrBlocked, e.Path))
		}
		return fail(fmt.Errorf("an den alten Ort legen: %w", err))
	}
	d := ""
	if kept > 0 {
		d = fmt.Sprintf("%d %s schon da und %s geblieben", kept, plural(kept, "Eintrag war", "Einträge waren"), plural(kept, "ist", "sind"))
	}
	if s.Restored != nil {
		s.Restored(ctx, e.Path)
	}
	ev(Event{StepRestore, StateOK, d})
	return nil
}

// copyBack legt die zurückgespielte Datei src an ihren alten Ort, ohne
// etwas zu überschreiben.
func copyBack(src string, e Entry) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	mode := os.FileMode(e.Mode)
	if mode == 0 {
		mode = 0o644
	}
	out, finish, err := createNew(e.Path, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		_ = out.Chmod(mode)
		_ = out.Chown(e.UID, e.GID)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	finish(err == nil)
	return err
}

// Expire leert, was länger als Keep im Papierkorb liegt, und gibt den
// Platz frei. Snapshots ohne Eintrag in der Liste (etwa nach einem Absturz
// mitten im Lauf) fallen nach Keep ebenso weg. Es meldet, was es getan
// hat; nichts zu tun: "".
func (s *Service) Expire(ctx context.Context) (string, error) {
	now := s.now()
	old := map[string]bool{}
	known := map[string]bool{}
	var ids, names []string
	for _, e := range s.entries() {
		known[e.ID] = true
		if !e.Until.After(now) {
			old[e.ID] = true
			ids = append(ids, e.ID)
			names = append(names, e.Name)
		}
	}
	snaps, err := s.Trash.List(ctx)
	if err != nil {
		return "", err
	}
	orphans := 0
	for _, sn := range snaps {
		if !known[sn.ID] && !sn.Time.Add(s.keep()).After(now) {
			ids = append(ids, sn.ID)
			orphans++
		}
	}
	if len(ids) == 0 {
		return "", nil
	}
	if err := s.Trash.Forget(ctx, ids); err != nil {
		return "", err
	}
	if err := s.dropEntries(old); err != nil {
		return "", err
	}
	if err := s.Trash.Prune(ctx); err != nil {
		return "", err
	}
	s.Refresh()
	days := int(s.keep() / (24 * time.Hour))
	if len(names) == 0 {
		return fmt.Sprintf("Papierkorb: %d alte Sicherungen ohne Eintrag nach %d Tagen gelöscht.", orphans, days), nil
	}
	return fmt.Sprintf("Papierkorb: %d %s nach %d Tagen endgültig gelöscht (%s).", len(names), plural(len(names), "Eintrag", "Einträge"), days, strings.Join(names, ", ")), nil
}

// PathItem ist ein Ordner oder eine Datei, die unter Speicherplatz vor dem
// Löschen in den Papierkorb kommt.
type PathItem struct {
	Path string
	Size int64 // belegt, für den Platz im Papierkorb
	// Remove löscht den Eintrag, nachdem er gesichert und der Papierkorb
	// geprüft ist. Ein Fehler heißt: nichts gelöscht, die Sicherung fällt
	// weg. partial: ein Teil blieb stehen; die Sicherung bleibt, und
	// Zurückholen legt das Fehlende wieder hin.
	Remove func(ctx context.Context) (partial bool, err error)
}

// Reserve ist, was neben dem Papierkorb frei bleiben muss.
func (s *Service) ReserveBytes() int64 { return s.reserve() }

// KeepUntil ist der Tag, bis zu dem sich heute Gelöschtes zurückholen
// lässt.
func (s *Service) KeepUntil() time.Time { return s.now().Add(s.keep()) }

// TrashPaths sichert items in den Papierkorb, prüft ihn vollständig und
// löscht erst dann, jeden Eintrag mit seinem Remove. Es meldet, wie viele
// gelöscht sind, und Hinweise zu denen, die bleiben.
func (s *Service) TrashPaths(ctx context.Context, items []PathItem, ev func(Event)) (int, []string, error) {
	if ev == nil {
		ev = func(Event) {}
	}
	if s.Trash == nil {
		return 0, nil, fmt.Errorf("%w: kein Papierkorb", ErrBlocked)
	}
	defer s.Refresh()
	its := make([]Item, 0, len(items))
	for _, p := range items {
		if p.Remove == nil {
			return 0, nil, errors.New("TrashPaths: Remove fehlt")
		}
		its = append(its, Item{Key: KindPath + ":" + p.Path, Kind: KindPath, Name: p.Path, Size: p.Size, path: p.Path, remove: p.Remove})
	}
	return s.trashRun(ctx, its, ev)
}
