//go:build windows

package pc

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// pickFolderScript öffnet den Windows-Dialog „Ordner suchen“. Die Ausgabe
// ist UTF-8, damit Umlaute im Pfad heil ankommen.
const pickFolderScript = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = 'Wo soll das Backup auf diesem PC liegen? Gebraucht werden etwa 60 GB.'
$d.ShowNewFolderButton = $true
$d.RootFolder = [System.Environment+SpecialFolder]::MyComputer
$owner = New-Object System.Windows.Forms.Form -Property @{TopMost = $true}
if ($d.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.SelectedPath }`

// ErrCancelled heißt: Der Benutzer hat den Dialog abgebrochen.
var ErrCancelled = errors.New("abgebrochen")

// PickFolder zeigt den normalen Windows-Dialog zur Ordnerwahl.
func PickFolder() (string, error) {
	// Kein HideWindow: Das würde auch den Dialog verstecken. PowerShell
	// teilt sich ohnehin die Konsole von HostDeck, ein zweites Fenster gibt es nicht.
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", pickFolderScript)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(strings.TrimPrefix(string(out), "\uFEFF"))
	if p == "" {
		return "", ErrCancelled
	}
	return p, nil
}

// HasDialog meldet, ob es einen grafischen Dialog gibt.
func HasDialog() bool { return true }

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFree    = kernel32.NewProc("GetDiskFreeSpaceExW")
	procSetConsoleCP   = kernel32.NewProc("SetConsoleCP")
	procSetConsoleOutC = kernel32.NewProc("SetConsoleOutputCP")
)

// FreeSpace meldet den freien Platz auf dem Laufwerk von path.
func FreeSpace(path string) (int64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	r, _, err := procGetDiskFree.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)),
		uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return 0, err
	}
	return int64(free), nil
}

// PrepareConsole stellt die Konsole auf UTF-8, damit Umlaute und Haken
// richtig erscheinen.
func PrepareConsole() {
	procSetConsoleOutC.Call(65001)
	procSetConsoleCP.Call(65001)
}
