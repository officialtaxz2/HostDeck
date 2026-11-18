// Package spiele erkennt Spielserver auf dem Server am Image und an wenigen
// Dateien, damit der Leitstand sie einordnen und benennen kann.
//
// Grundsatz: Ein Spielserver ist ein Container wie jeder andere. HostDeck fragt
// Spiele nicht ab, erfasst keine Spielerzahlen und geht dafür nicht ins Netz;
// Zustand und Verbrauch kommen aus Docker. Nichts ist auf
// einen bestimmten Server zugeschnitten: Jede Erkennung ist ein
// Baustein, der auf einem Server ohne Spiele still nichts tut. Keine
// Passwörter aus Dateien oder Umgebung.
package spiele

import (
	"strings"
	"unicode"
)

// Arten von Spielen, wie die Dateien oder das Image sie zeigen.
const (
	KindJava    = "java"    // Minecraft Java
	KindBedrock = "bedrock" // Minecraft Bedrock
	KindSource  = "source"  // Steam/Source
)

// image ist ein bekanntes Image: Anfang des Namens und was es sagt.
type image struct {
	prefix string
	kind   string // Art des Spiels
	game   string // Spiel, wenn das Image es sicher sagt; sonst leer
	sure   bool   // ohne weitere Bestätigung ein Spielserver
}

// images sind Images, die sicher oder wahrscheinlich Spielserver sind. Die
// Liste ist klein mit Absicht: Was hier fehlt, erkennen die Dateien.
var images = []image{
	{"itzg/minecraft-server", KindJava, "Minecraft Java", true},
	{"itzg/minecraft-bedrock-server", KindBedrock, "Minecraft Bedrock", true},
	{"ghcr.io/pterodactyl/yolks:java", KindJava, "", false},
	{"ghcr.io/ptero-eggs/yolks:java", KindJava, "", false},
	{"ghcr.io/pelican-eggs/yolks:java", KindJava, "", false},
	{"ghcr.io/parkervcp/yolks:java", KindJava, "", false},
	{"ghcr.io/parkervcp/games:source", KindSource, "", true},
	{"ghcr.io/ptero-eggs/games:source", KindSource, "", true},
	{"ghcr.io/parkervcp/steamcmd", KindSource, "", true},
	{"ghcr.io/ptero-eggs/steamcmd", KindSource, "", true},
	{"cm2network/steamcmd", KindSource, "", true},
	{"steamcmd/steamcmd", KindSource, "", true},
}

// notGames sehen aus wie ein Spielserver, leiten aber nur weiter (Router und
// Proxys vor Minecraft-Servern).
var notGames = []string{"itzg/mc-router", "itzg/mc-proxy", "itzg/bungeecord", "ghcr.io/itzg/mc-router"}

// bareImage nimmt Register docker.io/ und library/ weg und macht klein.
func bareImage(ref string) string {
	r := strings.ToLower(ref)
	r = strings.TrimPrefix(r, "docker.io/")
	r = strings.TrimPrefix(r, "index.docker.io/")
	r = strings.TrimPrefix(r, "library/")
	return r
}

func knownImage(ref string) (image, bool) {
	r := bareImage(ref)
	for _, im := range images {
		if strings.HasPrefix(r, im.prefix) {
			return im, true
		}
	}
	return image{}, false
}

func isRouter(ref string) bool {
	r := bareImage(ref)
	for _, p := range notGames {
		if strings.HasPrefix(r, p) {
			return true
		}
	}
	return false
}

// sourceFolders: Ordner eines Source-Spiels (neben srcds_run) und sein Name.
var sourceFolders = map[string]string{
	"left4dead2": "Left 4 Dead 2",
	"left4dead":  "Left 4 Dead",
	"csgo":       "Counter-Strike: Global Offensive",
	"cstrike":    "Counter-Strike: Source",
	"tf":         "Team Fortress 2",
	"garrysmod":  "Garry’s Mod",
	"hl2mp":      "Half-Life 2: Deathmatch",
	"dod":        "Day of Defeat: Source",
	"insurgency": "Insurgency",
	"nmrih":      "No More Room in Hell",
}

// short ist das Kürzel für die Karte: bekannte Spiele fest, sonst die
// Anfangsbuchstaben (höchstens vier).
func short(game string) string {
	switch game {
	case "Minecraft Java":
		return "MC"
	case "Minecraft Bedrock":
		return "MCB"
	case "Left 4 Dead 2":
		return "L4D2"
	case "Left 4 Dead":
		return "L4D"
	case "Team Fortress 2", "Team Fortress":
		return "TF2"
	case "Garry’s Mod", "Garry's Mod":
		return "GM"
	case "Counter-Strike 2":
		return "CS2"
	}
	var b strings.Builder
	for _, w := range strings.FieldsFunc(game, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		r := []rune(w)
		if unicode.IsDigit(r[0]) {
			b.WriteString(w)
		} else {
			b.WriteRune(unicode.ToUpper(r[0]))
		}
		if b.Len() >= 4 {
			break
		}
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}

// clean macht aus einem Text von außen (Datei, Container) eine
// Zeile zum Anzeigen: Minecraft-Farbcodes (§x) und Steuerzeichen weg,
// Leerraum zusammengefasst, höchstens max Zeichen.
func clean(s string, max int) string {
	var out strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '§' {
			i++
			continue
		}
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			out.WriteByte(' ')
			continue
		}
		out.WriteRune(r)
	}
	res := strings.Join(strings.Fields(out.String()), " ")
	if r := []rune(res); len(r) > max {
		res = string(r[:max-1]) + "…"
	}
	return res
}
