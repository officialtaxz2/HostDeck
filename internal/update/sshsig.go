package update

import (
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// Die Prüfsummen eines Releases (SHA256SUMS) tragen eine Signatur im Format
// von OpenSSH (SSHSIG), mit demselben Schlüssel wie die Update-Dateien. So
// prüft das Installationsskript sie mit ssh-keygen -Y verify, das auf jedem
// Server mit OpenSSH schon da ist.

// SigNamespace trennt diese Signaturen von anderen mit demselben Schlüssel.
const SigNamespace = "hostdeck-release"

// SignerName steht in der allowed_signers-Zeile des Installationsskripts.
const SignerName = "hostdeck"

func sshString(b []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(b)))
	return append(out, b...)
}

// sshPublic ist der öffentliche Schlüssel im Format von SSH.
func sshPublic(pub ed25519.PublicKey) []byte {
	return append(sshString([]byte("ssh-ed25519")), sshString(pub)...)
}

// AuthorizedKey ist der öffentliche Schlüssel als „ssh-ed25519 AAAA…“.
func AuthorizedKey(pub ed25519.PublicKey) string {
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(sshPublic(pub))
}

// AllowedSigners ist die Zeile für ssh-keygen -Y verify -f.
func AllowedSigners(pub ed25519.PublicKey) string {
	return SignerName + ` namespaces="` + SigNamespace + `" ` + AuthorizedKey(pub)
}

// SSHSign signiert msg wie „ssh-keygen -Y sign -n hostdeck-release“
// (Hash SHA-512) und liefert die Signatur in Textform.
func SSHSign(priv ed25519.PrivateKey, msg []byte) []byte {
	h := sha512.Sum512(msg)
	var signed []byte
	signed = append(signed, "SSHSIG"...)
	signed = append(signed, sshString([]byte(SigNamespace))...)
	signed = append(signed, sshString(nil)...)
	signed = append(signed, sshString([]byte("sha512"))...)
	signed = append(signed, sshString(h[:])...)
	sig := ed25519.Sign(priv, signed)

	var blob []byte
	blob = append(blob, "SSHSIG"...)
	blob = binary.BigEndian.AppendUint32(blob, 1)
	blob = append(blob, sshString(sshPublic(priv.Public().(ed25519.PublicKey)))...)
	blob = append(blob, sshString([]byte(SigNamespace))...)
	blob = append(blob, sshString(nil)...)
	blob = append(blob, sshString([]byte("sha512"))...)
	blob = append(blob, sshString(append(sshString([]byte("ssh-ed25519")), sshString(sig)...))...)

	enc := base64.StdEncoding.EncodeToString(blob)
	var b strings.Builder
	b.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	for len(enc) > 70 {
		b.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	b.WriteString(enc + "\n-----END SSH SIGNATURE-----\n")
	return []byte(b.String())
}
