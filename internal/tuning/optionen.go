package tuning

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// ErrOptions meldet ungültige Einstellungen.
var ErrOptions = errors.New("ungültige Einstellung")

// Switch ist ein Schalter von „apply“ (--preempt, --haltpoll …). Die Werte
// sind genau die, die tune.sh annimmt (Test).
type Switch struct {
	Key    string   `json:"schluessel"` // ohne --, etwa preempt
	Label  string   `json:"name"`
	Values []string `json:"werte,omitempty"` // leer: freier Text (iface)
	Text   string   `json:"text"`
	// Boot: Der Schalter ändert Bootparameter; sie wirken erst nach einem
	// Neustart.
	Boot bool `json:"boot,omitempty"`
}

// Switches in der Reihenfolge der Hilfe von tune.sh.
var Switches = []Switch{
	{Key: "preempt", Label: "Preemption", Values: []string{"none", "voluntary", "full", "keep"}, Boot: true,
		Text: "Bootparameter preempt=…, wenn der Kernel es kann (CONFIG_PREEMPT_DYNAMIC). full: kurze Reaktionszeiten für Gameserver und Web; none: Durchsatz für Rechenjobs; keep: nicht anfassen."},
	{Key: "haltpoll", Label: "Guest-Haltpoll", Values: []string{"on", "off"}, Boot: true,
		Text: "Bootparameter cpuidle_haltpoll.force=1, nur in einer KVM-VM: Eine leere vCPU wartet kurz aktiv, statt sofort zu schlafen. Weniger Verzögerung beim Aufwachen."},
	{Key: "mitigations", Label: "Schutz gegen CPU-Lücken", Values: []string{"off", "keep"}, Boot: true,
		Text: "off setzt den Bootparameter mitigations=off: kein Schutz gegen Lücken wie Spectre und Meltdown im Gastkernel. Schneller, aber Prozesse in dieser VM können sich leichter gegenseitig oder den Kernel angreifen."},
	{Key: "hardening", Label: "Kernel-Härtung", Values: []string{"off", "keep"}, Boot: true,
		Text: "off setzt init_on_alloc=0 und randomize_kstack_offset=off (soweit der Kernel sie standardmäßig an hat): Speicher wird nicht mehr genullt, der Kernel-Stack nicht mehr zufällig versetzt."},
	{Key: "net", Label: "BBR und fq", Values: []string{"on", "off"},
		Text: "TCP-Staukontrolle BBR mit der Warteschlange fq, kein Langsamstart nach Leerlauf. Sofort wirksam und in /etc/sysctl.d eingetragen."},
	{Key: "thp", Label: "Transparent Huge Pages", Values: []string{"always", "madvise", "never", "keep"},
		Text: "Große Speicherseiten: always für alle, madvise nur auf Anfrage eines Programms, never aus; keep lässt den Wert."},
	{Key: "defrag", Label: "THP-Defragmentierung", Values: []string{"always", "defer", "defer+madvise", "madvise", "never", "keep"},
		Text: "Wann der Kernel Speicher für große Seiten zusammenschiebt. defer+madvise räumt im Hintergrund auf und blockiert nur Programme, die große Seiten ausdrücklich wollen."},
	{Key: "rps", Label: "Receive Packet Steering", Values: []string{"all", "nocpu0", "off", "leave"},
		Text: "Verteilt eingehende Netzwerkpakete auf alle CPUs (all), auf alle außer CPU 0 (nocpu0) oder gar nicht (off); leave lässt es."},
	{Key: "iface", Label: "Netzwerkkarte",
		Text: "Für Warteschlange und RPS. Leer: die Karte der Standardroute, wie das Skript sie findet."},
	{Key: "udp", Label: "UDP-Puffer", Values: []string{"on", "off"},
		Text: "net.core.rmem_max und wmem_max auf 7,5 MB, hilft QUIC und Gameservern. Höhere Werte bleiben."},
	{Key: "mtu", Label: "TCP-MTU-Probing", Values: []string{"on", "off"},
		Text: "net.ipv4.tcp_mtu_probing=1: TCP sucht die passende Paketgröße, wenn unterwegs große Pakete verschwinden."},
	{Key: "io", Label: "Schreibpuffer", Values: []string{"on", "off"},
		Text: "Der Kernel schreibt ab 128 MB im Hintergrund und bremst schreibende Programme ab 512 MB (vm.dirty_*). In allen Presets aus."},
	{Key: "zram", Label: "zram-Swap", Values: []string{"on", "off"},
		Text: "Swap im RAM, komprimiert, 25 % des Arbeitsspeichers, als Notreserve. Eigener Dienst vps-tune-zram. In allen Presets aus."},
}

// Preset ist ein Preset von tune.sh mit den Werten, die preset_defaults
// setzt (Test).
type Preset struct {
	Name   string            `json:"name"`
	Title  string            `json:"titel"`
	Text   string            `json:"text"`
	Values map[string]string `json:"werte"`
	// Recommended: Die Hilfe von tune.sh empfiehlt es.
	Recommended bool `json:"empfohlen,omitempty"`
}

// lean sind die Werte, die preset_defaults für alle Presets gleich setzt.
func lean(m map[string]string) map[string]string {
	for k, v := range map[string]string{"thp": "keep", "defrag": "defer+madvise", "rps": "leave", "io": "off", "udp": "on", "mtu": "on", "zram": "off"} {
		m[k] = v
	}
	return m
}

// Presets wie in tune.sh. Die Texte beschreiben, was der Code tut; die
// Hilfe des Skripts nennt zusätzlich Schreibpuffer, zram, APT-Zeiten und
// sysstat, die das Skript in 2.3 nicht mehr einrichtet (Plan A1).
var Presets = []Preset{
	{Name: "mixed", Title: "Gemischt", Recommended: true,
		Text:   "Gameserver, Web und Streaming: preempt=full, Guest-Haltpoll, mitigations=off, init_on_alloc=0, randomize_kstack_offset=off, BBR und fq, THP-Defrag defer+madvise, UDP-Puffer 7,5 MB, TCP-MTU-Probing.",
		Values: lean(map[string]string{"preempt": "full", "haltpoll": "on", "mitigations": "off", "hardening": "off", "net": "on"})},
	{Name: "throughput", Title: "Durchsatz",
		Text:   "Wie Gemischt, aber preempt=none und ohne Haltpoll: für Rechenjobs und Batch.",
		Values: lean(map[string]string{"preempt": "none", "haltpoll": "off", "mitigations": "off", "hardening": "off", "net": "on"})},
	{Name: "safe", Title: "Sicher",
		Text:   "Nur BBR und fq, UDP-Puffer, TCP-MTU-Probing und THP-Defrag. Keine Sicherheits- oder Kerneländerung, kein Neustart nötig.",
		Values: lean(map[string]string{"preempt": "keep", "haltpoll": "off", "mitigations": "keep", "hardening": "keep", "net": "on"})},
}

// PresetByName liefert ein Preset; ok false, wenn es keins gibt.
func PresetByName(name string) (Preset, bool) {
	for _, p := range Presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}

func switchByKey(key string) (Switch, bool) {
	for _, s := range Switches {
		if s.Key == key {
			return s, true
		}
	}
	return Switch{}, false
}

var ifaceRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

// Options ist ein Aufruf von „apply“: Preset und die Schalter, die davon
// abweichen sollen.
type Options struct {
	Preset string `json:"preset"`
	// Set: Schalter (ohne --) und Wert. Fehlt einer oder ist er leer, gilt
	// der Wert des Presets.
	Set map[string]string `json:"schalter,omitempty"`
}

// Check prüft wie das Skript, nur vorher.
func (o Options) Check() error {
	if _, ok := PresetByName(o.Preset); !ok {
		return fmt.Errorf("%w: Unbekanntes Preset %q (mixed, throughput oder safe)", ErrOptions, o.Preset)
	}
	for k, v := range o.Set {
		sw, ok := switchByKey(k)
		switch {
		case !ok:
			return fmt.Errorf("%w: Unbekannter Schalter %q", ErrOptions, k)
		case v == "":
		case sw.Values == nil && !ifaceRe.MatchString(v):
			return fmt.Errorf("%w: Ungültiger Name der Netzwerkkarte %q", ErrOptions, v)
		case sw.Values != nil && !slices.Contains(sw.Values, v):
			return fmt.Errorf("%w: --%s kennt nur %v", ErrOptions, k, sw.Values)
		}
	}
	return nil
}

// Args sind die Argumente für tune.sh, ohne --dry-run und --yes.
func (o Options) Args() []string {
	a := []string{"apply", o.Preset}
	for _, sw := range Switches {
		if v := o.Set[sw.Key]; v != "" {
			a = append(a, "--"+sw.Key, v)
		}
	}
	return a
}

// Effective sind die Werte, mit denen das Skript rechnet: Preset plus
// Schalter.
func (o Options) Effective() map[string]string {
	p, _ := PresetByName(o.Preset)
	m := map[string]string{}
	for k, v := range p.Values {
		m[k] = v
	}
	for k, v := range o.Set {
		if v != "" {
			m[k] = v
		}
	}
	return m
}

// Prio-Stufen von „prio“ mit CPUWeight.
var Levels = map[string]int{"high": 1000, "normal": 0, "low": 20}

var unitRe = regexp.MustCompile(`^[A-Za-z0-9@_.:-]+$`)

// ValidUnit prüft den Namen eines systemd-Dienstes wie das Skript.
func ValidUnit(u string) bool { return unitRe.MatchString(u) && len(u) <= 256 }
