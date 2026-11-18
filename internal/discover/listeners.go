package discover

import (
	"encoding/hex"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

var cgroupID = regexp.MustCompile(`[0-9a-f]{64}`)

// listeners liest die offenen TCP-Ports des Hosts aus /proc/net und ordnet sie,
// soweit lesbar, Prozessen und Containern zu. Ohne root fehlt die Zuordnung,
// die Ports selbst sind trotzdem sichtbar.
func (s *scan) listeners() {
	type sock struct {
		proto, addr string
		port        int
		inode       string
	}
	var socks []sock
	for _, f := range []string{"tcp", "tcp6"} {
		b, err := s.d.FS.ReadFile("/proc/net/"+f, 4<<20)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if i == 0 || len(fields) < 10 || fields[3] != "0A" { // 0A = LISTEN
				continue
			}
			addr, port, ok := parseProcAddr(fields[1])
			if !ok {
				continue
			}
			socks = append(socks, sock{"tcp", addr, port, fields[9]})
		}
	}
	if len(socks) == 0 {
		return
	}

	// Socket-Inode -> Prozess.
	owner := map[string]string{} // inode -> pid
	if procs, err := s.d.FS.ReadDir("/proc"); err == nil {
		for _, p := range procs {
			if _, err := strconv.Atoi(p.Name()); err != nil {
				continue
			}
			fdDir := path.Join("/proc", p.Name(), "fd")
			fds, err := s.d.FS.ReadDir(fdDir)
			if err != nil {
				continue
			}
			for _, fd := range fds {
				link, err := s.d.FS.Readlink(path.Join(fdDir, fd.Name()))
				if err != nil || !strings.HasPrefix(link, "socket:[") {
					continue
				}
				owner[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = p.Name()
			}
		}
	}

	seen := map[string]bool{}
	for _, sk := range socks {
		id := sk.proto + "|" + sk.addr + "|" + strconv.Itoa(sk.port)
		if seen[id] {
			continue
		}
		seen[id] = true
		l := inventory.Listener{Proto: sk.proto, Addr: sk.addr, Port: sk.port}
		if pid := owner[sk.inode]; pid != "" {
			if b, err := s.d.FS.ReadFile(path.Join("/proc", pid, "comm"), 64); err == nil {
				l.Process = strings.TrimSpace(string(b))
			}
			if b, err := s.d.FS.ReadFile(path.Join("/proc", pid, "cgroup"), 8<<10); err == nil {
				if m := cgroupID.FindString(string(b)); m != "" {
					l.Container = s.byID[m]
				}
			}
		}
		s.inv.Listeners = append(s.inv.Listeners, l)
	}
}

// parseProcAddr übersetzt „0100007F:0BBD“ aus /proc/net/tcp in 127.0.0.1 und
// 3005. Die Adresse steht in 32-Bit-Wörtern in Host-Byte-Reihenfolge
// (Little Endian auf amd64 und arm64).
func parseProcAddr(s string) (string, int, bool) {
	a, p, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, false
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return "", 0, false
	}
	raw, err := hex.DecodeString(a)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", 0, false
	}
	ip := make(net.IP, len(raw))
	for w := 0; w < len(raw); w += 4 {
		ip[w], ip[w+1], ip[w+2], ip[w+3] = raw[w+3], raw[w+2], raw[w+1], raw[w]
	}
	if v4 := ip.To4(); v4 != nil && len(raw) == 16 && !ip.Equal(net.IPv6zero) {
		ip = v4
	}
	return ip.String(), int(port), true
}

func isLoopback(addr string) bool {
	ip := net.ParseIP(addr)
	return addr == "localhost" || (ip != nil && ip.IsLoopback())
}

func isWildcard(addr string) bool {
	ip := net.ParseIP(addr)
	return addr == "" || (ip != nil && ip.IsUnspecified())
}

// ParseProcAddr übersetzt eine Adresse aus /proc/net/tcp oder udp in
// Adresse und Port.
func ParseProcAddr(s string) (string, int, bool) { return parseProcAddr(s) }
