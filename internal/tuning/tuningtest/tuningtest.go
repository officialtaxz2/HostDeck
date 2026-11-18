// Package tuningtest baut für Tests einen kleinen Server nach, auf dem
// tune.sh echt läuft: /proc, /sys, /etc und /boot unter VT_PREFIX und
// Ersatzprogramme für update-grub, tc, ip, dmesg und systemctl. Nichts davon
// braucht root, und nichts verändert den echten Rechner.
package tuningtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/tuning"
)

// Rig ist der nachgebaute Server.
type Rig struct {
	t    testing.TB
	Root string // VT_PREFIX
	Bin  string // Ersatzprogramme
	S    *tuning.Service
}

// New baut ihn; ohne bash wird der Test übersprungen.
func New(t testing.TB) *Rig {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("ohne bash")
	}
	base := t.TempDir()
	g := &Rig{t: t, Root: filepath.Join(base, "root"), Bin: filepath.Join(base, "bin")}
	files := map[string]string{
		"proc/cmdline": "BOOT_IMAGE=/vmlinuz root=/dev/vda1 ro quiet",
		"proc/sys/net/ipv4/tcp_congestion_control":           "cubic",
		"proc/sys/net/ipv4/tcp_available_congestion_control": "reno cubic bbr",
		"proc/sys/net/ipv4/tcp_slow_start_after_idle":        "1",
		"proc/sys/net/ipv4/tcp_mtu_probing":                  "0",
		"proc/sys/net/core/default_qdisc":                    "fq_codel",
		"proc/sys/net/core/rmem_max":                         "212992",
		"proc/sys/net/core/wmem_max":                         "212992",
		"proc/sys/vm/dirty_background_ratio":                 "10",
		"proc/sys/vm/dirty_ratio":                            "20",
		"proc/sys/vm/dirty_background_bytes":                 "0",
		"proc/sys/vm/dirty_bytes":                            "0",
		"proc/meminfo":                                       "MemTotal:        8000000 kB",
		"proc/swaps":                                         "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority",
		"sys/devices/system/cpu/online":                      "0-1",
		"sys/devices/system/cpu/cpuidle/current_driver":      "haltpoll",
		"sys/devices/system/cpu/vulnerabilities/spectre_v2":  "Mitigation: Retpolines",
		"sys/kernel/mm/transparent_hugepage/enabled":         "always [madvise] never",
		"sys/kernel/mm/transparent_hugepage/defrag":          "always defer defer+madvise [madvise] never",
		"sys/kernel/debug/sched/preempt":                     "none voluntary (full)",
		"sys/class/net/eth0/device/vendor":                   "0x1af4",
		"sys/class/net/eth0/queues/rx-0/rps_cpus":            "0",
		"boot/config-6.12.0-test":                            "CONFIG_PREEMPT_DYNAMIC=y\nCONFIG_HALTPOLL_CPUIDLE=y\nCONFIG_INIT_ON_ALLOC_DEFAULT_ON=y\nCONFIG_RANDOMIZE_KSTACK_OFFSET_DEFAULT=y\nCONFIG_ZRAM=m",
		"etc/default/grub":                                   `GRUB_CMDLINE_LINUX_DEFAULT="quiet"`,
		"boot/grub/grub.cfg":                                 "linux /vmlinuz root=/dev/vda1 ro quiet",
	}
	for p, c := range files {
		g.Write(p, c+"\n")
	}
	progs := map[string]string{
		"update-grub": `f="$VT_PREFIX/etc/default/grub.d/99-vps-tune.cfg"
p=""; [ -f "$f" ] && p=$(sed -n 's/.*GRUB_CMDLINE_LINUX_DEFAULT} \([^"]*\)".*/\1/p' "$f")
echo "linux /vmlinuz root=/dev/vda1 ro quiet $p" > "$VT_PREFIX/boot/grub/grub.cfg"`,
		"tc": `st="$VT_PREFIX/tc.state"; [ -f "$st" ] || echo fq_codel > "$st"
case "$1 $2" in "qdisc show") echo "qdisc $(cat "$st") 0: root refcnt 2" ;; "qdisc replace") echo "$6" > "$st" ;; esac`,
		"ip":    `echo "default via 10.0.0.1 dev eth0 proto dhcp"`,
		"dmesg": `echo "[    0.000000] mem auto-init: stack:off, heap alloc:on, heap free:off"`,
		// systemctl merkt sich CPUWeight je Dienst in $VT_PREFIX/cpuweight/.
		"systemctl": `echo "systemctl $*" >> "$VT_PREFIX/systemctl.log"
w="$VT_PREFIX/cpuweight"; mkdir -p "$w"
case "$1" in
list-units) printf 'caddy.service loaded active running Caddy\ndocker.service loaded active running Docker Application Container Engine\nssh.service loaded active running OpenBSD Secure Shell server\n' ;;
set-property) echo "${3#CPUWeight=}" > "$w/$2"
  d="$VT_PREFIX/etc/systemd/system.control/$2.d"; mkdir -p "$d"
  printf '# This is a drop-in unit file extension, created via "systemctl set-property"\n[Service]\n%s\n' "$3" > "$d/50-CPUWeight.conf" ;;
show) shift; while [ "$1" != "--" ]; do shift; done; shift
  for u in "$@"; do v="[not set]"; [ -f "$w/$u" ] && v=$(cat "$w/$u"); printf 'Id=%s\nDescription=Dienst %s\nCPUWeight=%s\n\n' "$u" "$u" "$v"; done ;;
esac
exit 0`,
		"modprobe":         "exit 0",
		"dpkg-reconfigure": "exit 0",
	}
	for n, c := range progs {
		p := filepath.Join(g.Bin, n)
		if err := os.MkdirAll(g.Bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+c+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g.S = &tuning.Service{RunDir: filepath.Join(base, "run"), Prefix: g.Root, Env: []string{"VT_KREL=6.12.0-test", "VT_VIRT=kvm"},
		Path: g.Bin + ":/usr/bin:/bin"}
	return g
}

// Write legt eine Datei unter der Wurzel an.
func (g *Rig) Write(p, c string) {
	g.t.Helper()
	p = filepath.Join(g.Root, p)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		g.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
		g.t.Fatal(err)
	}
}

// Read liest eine Datei unter der Wurzel; leer, wenn es sie nicht gibt.
func (g *Rig) Read(p string) string {
	b, _ := os.ReadFile(filepath.Join(g.Root, p))
	return strings.TrimSpace(string(b))
}

// Rebooted spielt einen Neustart: Die Bootparameter aus grub.cfg stehen in
// /proc/cmdline, und /run ist leer.
func (g *Rig) Rebooted() {
	g.t.Helper()
	cfg := strings.TrimPrefix(g.Read("boot/grub/grub.cfg"), "linux ")
	g.Write("proc/cmdline", "BOOT_IMAGE="+cfg+"\n")
	os.Remove(filepath.Join(g.S.RunDir, tuning.RebootFile))
}
