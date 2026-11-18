//go:build !windows

package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// activated holt die Sockets, die systemd beim Start übergibt
// (Socket-Aktivierung), nach ihrem Namen (FileDescriptorName).
func activated() (map[string]net.Listener, error) {
	defer func() {
		for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
			os.Unsetenv(k)
		}
	}()
	return listenersFrom(os.Getenv, os.Getpid(), 3)
}

func listenersFrom(env func(string) string, pid, first int) (map[string]net.Listener, error) {
	if p, _ := strconv.Atoi(env("LISTEN_PID")); p != pid {
		return nil, nil
	}
	n, _ := strconv.Atoi(env("LISTEN_FDS"))
	if n <= 0 {
		return nil, nil
	}
	names := strings.Split(env("LISTEN_FDNAMES"), ":")
	out := map[string]net.Listener{}
	for i := 0; i < n; i++ {
		fd := first + i
		syscall.CloseOnExec(fd)
		name := "unbekannt"
		if i < len(names) && names[i] != "" {
			name = names[i]
		}
		f := os.NewFile(uintptr(fd), name)
		ln, err := net.FileListener(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("Socket %s von systemd: %v", name, err)
		}
		out[name] = ln
	}
	return out, nil
}
