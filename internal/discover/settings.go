package discover

import (
	"io/fs"
	"path"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// generatedEtc sind Dateien in /etc, die beim Einrichten oder von
// Paketskripten automatisch entstehen oder nur zu dieser Maschine gehören.
// Auf dem neuen Server entstehen sie wieder oder müssen anders sein; in der
// Liste wären sie nur Rauschen, auch wenn sie geändert wurden. Im Archiv
// von /etc liegen sie trotzdem.
// Muster mit /* am Ende gelten für alles darunter, Muster ohne / auch für
// Dateinamen in Unterordnern, wenn sie * enthalten.
var generatedEtc = []string{
	// Benutzer, Gruppen, Rechnername, Netz
	"passwd", "passwd-", "shadow", "shadow-", "group", "group-", "gshadow", "gshadow-",
	"subuid", "subuid-", "subgid", "subgid-", ".pwd.lock", "machine-id", "hostname", "hosts",
	"mtab", "resolv.conf", ".resolv.conf.systemd-resolved.bak", "localtime", "timezone", "adjtime",
	"fstab", "mailname", "netplan/*", "cloud/*", "*cloud-init*", "*cloudimg*", "*curtin*",
	"udev/rules.d/70-persistent-*", "iscsi/initiatorname.iscsi", "machine-info",
	// von Paketskripten erzeugt
	"ld.so.cache", "shells", "ca-certificates.conf", ".updated", "papersize", "printcap",
	"vconsole.conf", "locale.gen", "default/locale", "default/keyboard", "default/console-setup",
	"console-setup/*", "X11/Xwrapper.config", "initramfs-tools/conf.d/resume", "docker/key.json",
	"lvm/archive/*", "lvm/backup/*", "lvm/devices/*", "apparmor.d/cache/*", "apparmor.d/local/*",
	"ssl/certs/*", "ssl/private/ssl-cert-snakeoil.key", "ssh/ssh_host_*", "alternatives/*", "rc?.d/*",
	"dconf/db/*", "python3*/*", "ufw/*.rules", "ufw/*.rules.*", "ufw/*.init",
	"apt/apt.conf.d/20auto-upgrades", "apt/apt.conf.d/01autoremove-kernels",
	"apt/apt.conf.d/50command-not-found", "apt/apt.conf.d/99needrestart",
	"polkit-1/localauthority/*", "pki/*", "fonts/conf.d/*", "systemd/network/*",
	"systemd/system/snap-*", "systemd/system/snap.*", "systemd/system/var-lib-snapd*",
	"pam.d/common-*", "nsswitch.conf", "networks", "environment", "locale.conf", "security/opasswd",
	"xml/*", ".java/*", "apt/apt.conf.d/*autoremove*", "gai.conf", "subuid.lock", "subgid.lock",
	// gehören zur Maschine (Start, Platten, Netz beim Anbieter) oder zum
	// Abbild des Anbieters; auf dem neuen Server sind sie anders oder
	// entstehen neu
	"crypttab", "modules", "default/grub", "default/grub.d/*", "grub.d/*", "initramfs-tools/*",
	"mdadm/*", "default/mdadm", "multipath/*", "multipath.conf", "kernel-img.conf",
	"network/interfaces", "network/interfaces.d/*", "ec2_version", "overlayroot.local.conf",
	"vmware-tools/*", "sysstat/sysstat", "sgml/*",
	// legen Paketskripte als Kopie an statt als Konfigurationsdatei
	"profile", "inputrc", "hosts.allow", "hosts.deny", "apparmor.d/tunables/home.d/*",
	"apparmor.d/tunables/xdg-user-dirs.d/*", "tmpfiles.d/screen-cleanup.conf",
	"ufw/ufw.conf", // ob ufw an ist, steht in der Checkliste
	// Paketquellen stehen in der Checkliste
	"apt/sources.list", "apt/sources.list.d/*", "apt/keyrings/*", "apt/trusted.gpg", "apt/trusted.gpg~", "apt/trusted.gpg.d/*",
	// Reste von Paket-Updates und Versionsverwaltung von /etc
	"*.dpkg-dist", "*.dpkg-old", "*.dpkg-new", "*.dpkg-bak", "*.ucf-dist", "*.ucf-old", "*.ucf-new",
	".git/*", ".gitignore", ".etckeeper",
}

func generatedEtcFile(rel string) bool {
	base := path.Base(rel)
	for _, pat := range generatedEtc {
		if dir, ok := strings.CutSuffix(pat, "/*"); ok {
			for a := path.Dir(rel); a != "." && a != "/"; a = path.Dir(a) {
				if m, _ := path.Match(dir, a); m {
					return true
				}
			}
			continue
		}
		if m, _ := path.Match(pat, rel); m {
			return true
		}
		if !strings.Contains(pat, "/") && strings.Contains(pat, "*") {
			if m, _ := path.Match(pat, base); m {
				return true
			}
		}
	}
	return false
}

// settings sucht in /etc nach Dateien, die geändert oder selbst angelegt
// wurden. Geändert heißt: Die Prüfsumme weicht von der ab, die die
// Paketdatenbank (oder ucf) für die Datei kennt. Inhalte werden dabei nicht
// gespeichert. Ohne Paketdatenbank (kein Debian/Ubuntu) bleibt die Liste
// leer; /etc wird trotzdem archiviert.
func (s *scan) settings() {
	if s.dpkg == nil {
		return
	}
	taskFiles := map[string]bool{}
	for _, t := range s.inv.Tasks {
		taskFiles[t.Source] = true
		if t.Timer != "" {
			taskFiles["/etc/systemd/system/"+t.Timer] = true
		}
	}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		ents, err := s.d.FS.ReadDir(dir)
		if err != nil || depth > 8 {
			return
		}
		for _, e := range ents {
			p := dir + "/" + e.Name()
			switch {
			case e.IsDir():
				if !generatedEtcFile(relEtc(p) + "/x") {
					walk(p, depth+1)
				}
				continue
			case e.Type()&fs.ModeType != 0:
				continue // Symlinks, Geräte, Pipes
			}
			if taskFiles[p] || generatedEtcFile(relEtc(p)) {
				continue
			}
			db := s.dpkg
			if c, ok := db.conffiles[p]; ok {
				if m, ok := s.fileMD5(p); ok && m != c.md5 {
					s.inv.Settings = append(s.inv.Settings, inventory.Setting{Path: p, Change: inventory.ChangeModified, Package: c.pkg})
				}
				continue
			}
			if want, ok := db.ucf[p]; ok {
				if m, ok := s.fileMD5(p); ok && m != want {
					s.inv.Settings = append(s.inv.Settings, inventory.Setting{Path: p, Change: inventory.ChangeModified, Package: db.owned[p]})
				}
				continue
			}
			if db.owned[p] != "" {
				continue
			}
			s.inv.Settings = append(s.inv.Settings, inventory.Setting{Path: p, Change: inventory.ChangeAdded})
		}
	}
	walk("/etc", 0)
}
