package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/leistungstest"
	"github.com/officialtaxz2/hostdeck/internal/ort"
)

// leistungstestCmd führt benchmark.sh im Terminal aus, etwa gleich nach
// einer Neuinstallation. Die Ergebnisse landen dort, wo auch der Leitstand
// sie zeigt (Überwachung > Leistungstest).
func leistungstestCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("leistungstest", flag.ContinueOnError)
	fl.SetOutput(stderr)
	withGB := fl.Bool("mit-geekbench", false, "")
	// Früher Standard war Geekbench an; der alte Schalter bleibt erlaubt.
	_ = fl.Bool("ohne-geekbench", false, "")
	secs := fl.Int("sekunden", leistungstest.DefaultSeconds, "")
	data := fl.String("daten", ort.DataDir, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "Der Leistungstest braucht root (er installiert fehlende Pakete): sudo hostdeck leistungstest")
		return 2
	}
	opt := leistungstest.Options{Geekbench: *withGB, Seconds: *secs}
	if err := opt.Check(); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 2
	}
	host, _ := os.Hostname()
	s := &leistungstest.Service{Dir: filepath.Join(*data, ort.BenchDir), Host: host}
	if opt.Geekbench {
		if in := s.Info(); !in.GeekbenchOK {
			fmt.Fprintf(stderr, "%s Ohne Geekbench: sudo hostdeck leistungstest\n", in.GeekbenchWhy)
			return 2
		}
	}
	l, err := lauf.Take(lauf.Leistungstest)
	var busy *lauf.BusyError
	if errors.As(err, &busy) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	defer l.Release()
	res, err := s.Run(ctx, opt, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout)
	switch res.State {
	case leistungstest.StateDone:
		fmt.Fprintf(stdout, "Leistungstest fertig: Sysbench %.2f Events/s mit 1 Thread, %.2f mit %d Threads.\n", res.SysbenchSingle, res.SysbenchMulti, res.Threads)
	case leistungstest.StateCancelled:
		fmt.Fprintln(stdout, "Leistungstest abgebrochen.")
	case leistungstest.StateFailed:
		fmt.Fprintln(stdout, "Leistungstest gescheitert, bevor er messen konnte (siehe Ausgabe oben).")
	default:
		fmt.Fprintln(stdout, "Leistungstest unvollständig: mindestens eine Phase ging schief (siehe Ausgabe oben).")
	}
	if res.ZipSize > 0 {
		// Das Skript nennt oben seinen Arbeitsordner; HostDeck hat beides von dort
		// in den Ergebnisordner gelegt.
		fmt.Fprintf(stdout, "Support-ZIP: %s (HostDeck hat sie aus dem Arbeitsordner, den das Skript oben nennt, hierher gelegt)\n", filepath.Join(s.Dir, res.ID+".zip"))
	}
	if res.Claim {
		fmt.Fprintf(stdout, "Privater Geekbench-Claim-Link (nicht an den Support schicken): %s\n", filepath.Join(s.Dir, res.ID+".claim"))
	}
	fmt.Fprintln(stdout, "Im Leitstand: Überwachung > Leistungstest.")
	if res.State == leistungstest.StateDone {
		return 0
	}
	return 1
}
