// Package completion embeds static shell completion scripts.
package completion

import (
	_ "embed"
	"fmt"
)

//go:embed afl.bash
var bash string

//go:embed _afl
var zsh string

//go:embed afl.fish
var fish string

// Script returns the completion script for the named shell.
func Script(shell string) (string, error) {
	switch shell {
	case "bash":
		return bash, nil
	case "zsh":
		return zsh, nil
	case "fish":
		return fish, nil
	}
	return "", fmt.Errorf("unsupported shell %q (bash, zsh, fish)", shell)
}

// Shells lists the supported shells in display order.
var Shells = []string{"bash", "zsh", "fish"}
