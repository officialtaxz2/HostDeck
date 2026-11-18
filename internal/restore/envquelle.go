package restore

import (
	"os"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// envSource sucht in den Compose-Dateien eines Projekts, woher die
// Variable key des Dienstes service ihren Wert bekommt. expr ist der
// Ausdruck hinter dem Schlüssel (etwa „${START_MODUS:-}“), names sind die
// Variablen, die Compose dafür beim Start einsetzt. found ist false, wenn
// der Schlüssel in keiner Compose-Datei steht. Spätere Dateien überschreiben
// frühere, wie bei docker compose -f a -f b.
func envSource(files []string, service, key string) (expr string, names []string, found bool) {
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var doc struct {
			Services map[string]struct {
				Environment yaml.Node `yaml:"environment"`
			} `yaml:"services"`
		}
		if yaml.Unmarshal(b, &doc) != nil {
			continue
		}
		svc, ok := doc.Services[service]
		if !ok {
			continue
		}
		if e, ok := envEntry(&svc.Environment, key); ok {
			expr, found = e, true
		}
	}
	if !found {
		return "", nil, false
	}
	return expr, varNames(expr), true
}

// envEntry liest einen Eintrag aus „environment:“, als Liste („KEY=wert“
// oder nur „KEY“) oder als Abbildung („KEY: wert“). Ein Eintrag ohne Wert
// reicht die gleichnamige Variable durch; er steht dann als ${KEY} da.
func envEntry(n *yaml.Node, key string) (string, bool) {
	switch n.Kind {
	case yaml.SequenceNode:
		for _, it := range n.Content {
			k, v, has := strings.Cut(it.Value, "=")
			if k != key {
				continue
			}
			if !has {
				return "${" + key + "}", true
			}
			return v, true
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value != key {
				continue
			}
			v := n.Content[i+1]
			if v.Tag == "!!null" {
				return "${" + key + "}", true
			}
			return v.Value, true
		}
	}
	return "", false
}

var varRef = regexp.MustCompile(`\$\$|\$\{([A-Za-z_][A-Za-z0-9_]*)|\$([A-Za-z_][A-Za-z0-9_]*)`)

// varNames nennt die Variablen, die Compose in expr einsetzt; „$$“ ist ein
// wörtliches Dollarzeichen.
func varNames(expr string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range varRef.FindAllStringSubmatch(expr, -1) {
		name := m[1] + m[2]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}
