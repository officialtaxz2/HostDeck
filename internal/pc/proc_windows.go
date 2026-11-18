//go:build windows

package pc

import (
	"os/exec"
	"syscall"
)

// setProcessGroup startet ssh in einer eigenen Prozessgruppe. So erreicht
// Strg+C zuerst nur HostDeck, das den Server um einen geordneten Abbruch bittet,
// statt die Verbindung sofort zu kappen. Die Konsole teilen sich beide
// weiter, ssh kann also nach dem Passwort fragen.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

var setThreadExecutionState = syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// keepAwake setzt den Leerlauf-Zähler von Windows zurück, damit der PC
// während eines Laufs nicht in den Standby geht. Ohne ES_CONTINUOUS gilt
// das nur für den Moment, deshalb ruft der Herzschlag es immer wieder auf.
func keepAwake() {
	const esSystemRequired = 0x00000001
	setThreadExecutionState.Call(esSystemRequired)
}
