package afspec

import "embed"

//go:embed schemas/*.json
var schemaFS embed.FS

// Schemas returns the bundled JSON Schema files as a map of schema file names
// to their raw bytes. The schemas are embedded at compile time from the
// schemas/ directory via //go:embed. The returned map is never nil and is
// never empty because schema files are embedded at compile time.
func Schemas() map[string][]byte {
	entries, err := schemaFS.ReadDir("schemas")
	if err != nil {
		// Should never happen since schemas are embedded at compile time.
		return map[string][]byte{}
	}
	result := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := schemaFS.ReadFile("schemas/" + entry.Name())
		if err != nil {
			continue
		}
		result[entry.Name()] = data
	}
	return result
}
