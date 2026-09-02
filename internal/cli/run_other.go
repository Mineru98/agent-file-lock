//go:build !unix

package cli

import "errors"

func runCommand(argv []string, drop *Credential) (int, error) {
	return 126, errors.New("afl run is not supported on this platform")
}
