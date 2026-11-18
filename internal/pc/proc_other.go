//go:build !windows

package pc

import "os/exec"

// setProcessGroup lässt ssh unter Linux und macOS in der Prozessgruppe der
// Konsole, sonst könnte es nicht nach dem Passwort fragen. Strg+C beendet
// dort auch ssh; der Server merkt das und startet gestoppte Container
// trotzdem wieder.
func setProcessGroup(*exec.Cmd) {}

// keepAwake gibt es nur unter Windows.
func keepAwake() {}
