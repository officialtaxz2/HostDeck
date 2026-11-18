// hostdeck-signieren erzeugt signierte Update-Dateien (.hdu) für HostDeck. Es gehört
// nicht zum Release; nur wer den privaten Schlüssel hat, kann Updates bauen.
//
//	hostdeck-signieren schluessel DATEI
//	    erzeugt ein neues Schlüsselpaar, schreibt den privaten Schlüssel nach
//	    DATEI (0600) und zeigt den öffentlichen für internal/update.
//	hostdeck-signieren paket -schluessel DATEI -version V -os linux -arch amd64 PROGRAMM AUSGABE
//	    signiert PROGRAMM und schreibt die Update-Datei AUSGABE.
//	hostdeck-signieren summen -schluessel DATEI SHA256SUMS
//	    signiert die Prüfsummen eines Releases für das Installationsskript
//	    (SHA256SUMS.sig, prüfbar mit ssh-keygen -Y verify).
//	hostdeck-signieren pruefzeile
//	    zeigt die Zeile für allowed_signers im Installationsskript.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"

	"github.com/officialtaxz2/hostdeck/internal/update"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Fehler: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("Aufruf: hostdeck-signieren schluessel DATEI | paket … | summen … | pruefzeile")
	}
	switch args[0] {
	case "schluessel":
		if len(args) != 2 {
			return errors.New("Aufruf: hostdeck-signieren schluessel DATEI")
		}
		if _, err := os.Stat(args[1]); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s gibt es schon; ein neuer Schlüssel würde alle bisherigen Versionen aussperren", args[1])
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err := os.WriteFile(args[1], []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
			return err
		}
		fmt.Printf("Öffentlicher Schlüssel: %s\n", base64.StdEncoding.EncodeToString(pub))
		return nil
	case "pruefzeile":
		pub, err := update.ParsePublicKey(update.PublicKey)
		if err != nil {
			return err
		}
		fmt.Println(update.AllowedSigners(pub))
		return nil
	case "summen":
		fl := flag.NewFlagSet("summen", flag.ContinueOnError)
		keyFile := fl.String("schluessel", "", "privater Schlüssel")
		if err := fl.Parse(args[1:]); err != nil {
			return err
		}
		if fl.NArg() != 1 || *keyFile == "" {
			return errors.New("Aufruf: hostdeck-signieren summen -schluessel DATEI SHA256SUMS")
		}
		priv, err := loadKey(*keyFile)
		if err != nil {
			return err
		}
		sums, err := os.ReadFile(fl.Arg(0))
		if err != nil {
			return err
		}
		return os.WriteFile(fl.Arg(0)+".sig", update.SSHSign(priv, sums), 0o644)
	case "paket":
		fl := flag.NewFlagSet("paket", flag.ContinueOnError)
		keyFile := fl.String("schluessel", "", "privater Schlüssel")
		version := fl.String("version", "", "Version")
		goos := fl.String("os", "linux", "Betriebssystem")
		arch := fl.String("arch", "amd64", "Prozessor")
		if err := fl.Parse(args[1:]); err != nil {
			return err
		}
		if fl.NArg() != 2 || *keyFile == "" || *version == "" {
			return errors.New("Aufruf: hostdeck-signieren paket -schluessel DATEI -version V [-os linux] [-arch amd64] PROGRAMM AUSGABE")
		}
		priv, err := loadKey(*keyFile)
		if err != nil {
			return err
		}
		pub := priv.Public().(ed25519.PublicKey)
		bin, err := os.ReadFile(fl.Arg(0))
		if err != nil {
			return err
		}
		var out bytes.Buffer
		if err := update.Write(&out, priv, update.Header{Version: *version, OS: *goos, Arch: *arch}, bin); err != nil {
			return err
		}
		// Gegenprobe wie auf dem Server.
		if _, err := update.Verify(bytes.NewReader(out.Bytes()), pub, &bytes.Buffer{}); err != nil {
			return fmt.Errorf("Gegenprobe: %v", err)
		}
		return os.WriteFile(fl.Arg(1), out.Bytes(), 0o644)
	}
	return fmt.Errorf("unbekannt: %s", args[0])
}

// loadKey liest den privaten Schlüssel und prüft, dass er zum eingebauten
// öffentlichen passt.
func loadKey(file string) (ed25519.PrivateKey, error) {
	k, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	priv, err := update.ParsePrivateKey(string(k))
	if err != nil {
		return nil, err
	}
	pub, err := update.ParsePublicKey(update.PublicKey)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(priv.Public().(ed25519.PublicKey), pub) {
		return nil, errors.New("der private Schlüssel passt nicht zum eingebauten öffentlichen")
	}
	return priv, nil
}
