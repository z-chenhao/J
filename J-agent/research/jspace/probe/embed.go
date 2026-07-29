// Package probe embeds the Python replay program so installed J-Space
// services do not depend on a source checkout.
package probe

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed probe.py torch_checkpoint.py
var files embed.FS

// Install writes the embedded probe beside private J-Space runtime state.
func Install(directory string) (string, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create probe directory: %w", err)
	}
	for _, name := range []string{"probe.py", "torch_checkpoint.py"} {
		content, err := files.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("read embedded %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), content, 0o600); err != nil {
			return "", fmt.Errorf("install embedded %s: %w", name, err)
		}
		if err := os.Chmod(filepath.Join(directory, name), 0o600); err != nil {
			return "", fmt.Errorf("protect embedded %s: %w", name, err)
		}
	}
	return filepath.Join(directory, "probe.py"), nil
}
