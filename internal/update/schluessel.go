package update

// PublicKey ist der öffentliche Schlüssel, zu dem jede Update-Datei passen
// muss. Der private liegt nicht im Repository, sondern im Projektordner
// (hostdeck-signatur/privat.key); damit baut cmd/hostdeck-signieren die .hdu-Dateien
// eines Releases.
var PublicKey = "paMA+fXgT6qjxoN5MfSDNpH/F6Q9HZPr/P7vbjrhlo8="
