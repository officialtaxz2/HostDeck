// Verlaufs-Generator des Prüflabors. Der Labor-Host hat cgroup v1; Werte je Container misst
// HostDeck dort nicht. Dieses Programm schreibt darum 30 Tage künstlichen Verlauf
// über den echten Speicher-Code (ueberwachung.Store), nicht von Hand: Reihen
// des Servers (s/), der Platte (d/), je Container (c/<name>/cpu|ram) und je
// Compose-Projekt (p/<projekt>/cpu|ram), mit Spitzen, einer Lücke (HostDeck lief
// nicht) und einem Neustart des Dienstes mitten im Zeitraum.
//
// Nur fürs Labor. Läuft auf dem Labor-Server, während hostdeck-dienst steht:
//
//	verlauf -dir /var/lib/hostdeck/ueberwachung -kerne 4 -ram 16000000000 \
//	  -platte /=60000000000:21000000000 -container app-web-1=app,panel-1=
//
// labor/zustand.sh verlauf setzt Kerne, Arbeitsspeicher und Platte aus dem
// Labor-Server ein, damit der Verlauf am Ende zum Live-Wert passt.
//
// Die Werte sind erfunden, aber in sich stimmig: Der Prozessor des Servers
// ist nie kleiner als die Summe der Container (Teile ≤ Ganzes), der
// Arbeitsspeicher ebenso.
package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

type cont struct {
	name, proj string
	cpu        float64 // Grundlast in % eines Kerns
	ram        float64 // Byte
	seed       float64
}

func main() {
	dir := flag.String("dir", "/var/lib/hostdeck/ueberwachung", "Ordner des Verlaufs")
	days := flag.Int("tage", 30, "so viele Tage zurück")
	cores := flag.Int("kerne", 4, "Kerne des Servers")
	ram := flag.Int64("ram", 16_000_000_000, "Arbeitsspeicher gesamt (Byte)")
	disk := flag.String("platte", "/=60000000000:21000000000", "Laufwerk=gesamt:belegt (Byte)")
	list := flag.String("container", "", "name=projekt,… (projekt leer: kein Compose-Projekt)")
	heavy := flag.String("viel", "", "Container mit hoher Last (Präfix des Namens)")
	untilS := flag.String("bis", "", "Ende (RFC 3339), Standard jetzt")
	flag.Parse()

	until := time.Now()
	if *untilS != "" {
		t, err := time.Parse(time.RFC3339, *untilS)
		if err != nil {
			fail(err)
		}
		until = t
	}
	until = until.Truncate(10 * time.Second)
	from := until.Add(-time.Duration(*days) * 24 * time.Hour)

	var cs []cont
	for i, part := range strings.Split(*list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, proj, _ := strings.Cut(part, "=")
		c := cont{name: name, proj: proj, cpu: 0.4 + float64(i%5)*0.6, ram: float64(15+i*7) * 1e6, seed: float64(i) * 1.7}
		if *heavy != "" && strings.HasPrefix(name, *heavy) {
			c.cpu, c.ram = 7, 0.25e9
		}
		cs = append(cs, c)
	}
	mount, sizes, _ := strings.Cut(*disk, "=")
	ts, us, _ := strings.Cut(sizes, ":")
	diskTotal, _ := strconv.ParseFloat(ts, 64)
	diskUsed, _ := strconv.ParseFloat(us, 64)

	// Lücke: vor 12 Tagen von 02:00 bis 08:00 lief HostDeck nicht. Neustart des
	// Dienstes vor 5 Tagen um 12:00:05 (mitten in einem Abschnitt).
	day := func(n int, h, m int) time.Time {
		d := until.AddDate(0, 0, -n)
		return time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, time.UTC)
	}
	gapFrom, gapTo := day(12, 2, 0), day(12, 8, 0)
	restart := day(5, 12, 0).Add(5 * time.Second)
	// Spitzen: vor 20 Stunden 30 s lang (in 24 Std sichtbar, „gestern“ in
	// Ortszeit), vor 9 Tagen eine längere (nur in 30 Tagen), und vor 40
	// Minuten eine kleine (in 1 Std).
	spikes := []struct {
		at  time.Time
		len time.Duration
		cpu float64
	}{
		{until.Add(-20 * time.Hour).Truncate(10 * time.Second), 30 * time.Second, 96},
		{day(9, 14, 10), 4 * time.Minute, 88},
		{until.Add(-40 * time.Minute).Truncate(10 * time.Second), 20 * time.Second, 61},
	}

	rng := rand.New(rand.NewSource(42))
	st := &ueberwachung.Store{Dir: *dir}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		fail(err)
	}
	lastFlush := from
	n := 0
	for t := from; !t.After(until); t = t.Add(10 * time.Second) {
		if !t.Before(gapFrom) && t.Before(gapTo) {
			continue
		}
		if !t.Before(restart) && t.Add(-10*time.Second).Before(restart) {
			if err := st.Close(t); err != nil {
				fail(err)
			}
			st = &ueberwachung.Store{Dir: *dir}
			if err := st.Load(t); err != nil {
				fail(err)
			}
			lastFlush = t
		}
		hour := float64(t.Hour()) + float64(t.Minute())/60
		daily := 0.5 + 0.5*math.Sin((hour-9)/24*2*math.Pi) // nachmittags mehr
		v := map[string]float64{}
		sumCPU, sumRAM := 0.0, 0.0
		proj := map[string][2]float64{}
		for _, c := range cs {
			cpu := c.cpu * (0.6 + 0.8*daily) * (0.85 + 0.3*rng.Float64())
			cpu += 0.5 * c.cpu * math.Max(0, math.Sin(float64(t.Unix())/900+c.seed))
			ramv := c.ram * (0.92 + 0.08*math.Sin(float64(t.Unix())/7200+c.seed))
			v[ueberwachung.ContainerKey(c.name, ueberwachung.ValCPU)] = cpu
			v[ueberwachung.ContainerKey(c.name, ueberwachung.ValRAM)] = ramv
			sumCPU += cpu
			sumRAM += ramv
			if c.proj != "" {
				p := proj[c.proj]
				proj[c.proj] = [2]float64{p[0] + cpu, p[1] + ramv}
			}
		}
		for p, x := range proj {
			v[ueberwachung.ProjectKey(p, ueberwachung.ValCPU)] = x[0]
			v[ueberwachung.ProjectKey(p, ueberwachung.ValRAM)] = x[1]
		}
		// Server: Summe der Container (in Prozent aller Kerne) plus etwas
		// für den Host selbst; nie weniger als die Teile.
		cpu := sumCPU/float64(*cores) + 1.5 + 2*daily + rng.Float64()
		for _, s := range spikes {
			if !t.Before(s.at) && t.Before(s.at.Add(s.len)) {
				cpu = math.Max(cpu, s.cpu)
			}
		}
		v[ueberwachung.KeyCPU] = math.Min(100, cpu)
		v[ueberwachung.KeySteal] = 0.2 + 0.6*rng.Float64()*daily
		v[ueberwachung.KeyIOWait] = 0.1 + 0.4*rng.Float64()
		v[ueberwachung.KeyLoad] = cpu / 100 * float64(*cores)
		v[ueberwachung.KeyRAM] = math.Min(float64(*ram)*0.95, sumRAM+0.5e9+1e8*daily)
		v[ueberwachung.KeySwap] = 0
		v[ueberwachung.KeyRead] = 20e3 + 80e3*rng.Float64()*daily
		v[ueberwachung.KeyWrite] = 60e3 + 120e3*rng.Float64()
		v[ueberwachung.KeyNetIn] = 4e3 + 30e3*daily*rng.Float64()
		v[ueberwachung.KeyNetOut] = 3e3 + 18e3*daily*rng.Float64()
		// Platte wächst über 30 Tage um 4 % bis auf den heutigen Stand.
		frac := float64(until.Sub(t)) / float64(until.Sub(from))
		v["d/"+mount] = diskUsed - 0.04*diskTotal*frac
		st.Add(ueberwachung.Frame{At: t, Values: v})
		n++
		if t.Sub(lastFlush) >= 6*time.Hour {
			if err := st.Flush(t); err != nil {
				fail(err)
			}
			lastFlush = t
		}
	}
	if err := st.Close(until); err != nil {
		fail(err)
	}
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, c.name)
	}
	sort.Strings(names)
	fmt.Printf("Verlauf geschrieben: %d Messungen von %s bis %s (Lücke %s–%s, Neustart %s), Container: %s\n",
		n, from.Format(time.RFC3339), until.Format(time.RFC3339), gapFrom.Format("02.01. 15:04"), gapTo.Format("15:04"), restart.Format("02.01. 15:04:05"), strings.Join(names, ", "))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "verlauf:", err)
	os.Exit(1)
}
