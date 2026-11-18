package discover

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"strings"
)

// dpkgDB ist das, was discover aus der Paketdatenbank von Debian und Ubuntu
// braucht: welche Dateien Paketen gehören und wie ihre Konfigurationsdateien
// ursprünglich aussahen. Gelesen werden nur /var/lib/dpkg und /var/lib/ucf.
type dpkgDB struct {
	owned     map[string]string   // Pfad -> Paket, nur unter ownedPrefixes
	conffiles map[string]conffile // Konfigurationsdateien der Pakete
	ucf       map[string]string   // Pfad -> MD5 der Fassung, die ucf verwaltet
	installed map[string]bool     // installierte Pakete
}

type conffile struct {
	pkg, md5 string
}

// ownedPrefixes: Nur hier interessiert, ob eine Datei einem Paket gehört.
var ownedPrefixes = []string{"/etc/", "/usr/local/", "/opt/", "/srv/", "/var/www/"}

// maxDpkgFile begrenzt, wie viel discover aus einer Datei der Paketdatenbank
// liest.
const maxDpkgFile = 64 << 20

func (s *scan) loadDpkg() *dpkgDB {
	status, err := s.d.FS.ReadFile("/var/lib/dpkg/status", maxDpkgFile)
	if err != nil {
		return nil
	}
	db := &dpkgDB{
		owned:     map[string]string{},
		conffiles: map[string]conffile{},
		ucf:       map[string]string{},
		installed: map[string]bool{},
	}
	db.parseStatus(status)
	if ents, err := s.d.FS.ReadDir("/var/lib/dpkg/info"); err == nil {
		for _, e := range ents {
			name, ok := strings.CutSuffix(e.Name(), ".list")
			if !ok {
				continue
			}
			pkg, _, _ := strings.Cut(name, ":")
			b, err := s.d.FS.ReadFile("/var/lib/dpkg/info/"+e.Name(), maxDpkgFile)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(b), "\n") {
				for _, pre := range ownedPrefixes {
					if strings.HasPrefix(line, pre) {
						db.owned[line] = pkg
						break
					}
				}
			}
		}
	}
	if b, err := s.d.FS.ReadFile("/var/lib/ucf/hashfile", maxDpkgFile); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 {
				db.ucf[f[1]] = f[0]
			}
		}
	}
	return db
}

func (db *dpkgDB) parseStatus(b []byte) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var pkg string
	installed := false
	var conf []conffile
	var confPaths []string
	inConf := false
	flush := func() {
		if pkg != "" && installed {
			db.installed[pkg] = true
			for i, c := range conf {
				db.conffiles[confPaths[i]] = c
			}
		}
		pkg, installed, conf, confPaths, inConf = "", false, nil, nil, false
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, " ") && inConf:
			f := strings.Fields(line)
			// „ /etc/x md5“, dahinter evtl. „obsolete“ oder „remove-on-upgrade“
			if len(f) >= 2 && len(f) <= 3 && strings.HasPrefix(f[0], "/") {
				if len(f) == 3 && f[2] == "obsolete" {
					continue
				}
				conf = append(conf, conffile{pkg: pkg, md5: f[1]})
				confPaths = append(confPaths, f[0])
			}
		default:
			inConf = false
			k, v, ok := strings.Cut(line, ":")
			if !ok || strings.HasPrefix(line, " ") {
				continue
			}
			v = strings.TrimSpace(v)
			switch k {
			case "Package":
				pkg = v
			case "Status":
				installed = strings.HasSuffix(v, " installed")
			case "Conffiles":
				inConf = true
			}
		}
	}
	flush()
}

// fileMD5 berechnet die Prüfsumme einer Datei, um sie mit der Fassung des
// Pakets zu vergleichen. Der Inhalt wird nirgends gespeichert.
func (s *scan) fileMD5(p string) (string, bool) {
	if !s.isRegular(p) {
		return "", false // nie aus Pipes oder Geräten lesen
	}
	f, err := os.Open(s.d.FS.Real(p))
	if err != nil {
		return "", false
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxDpkgFile)); err != nil {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}
