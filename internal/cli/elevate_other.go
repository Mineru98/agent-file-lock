//go:build !unix

package cli

func execSudo(exe string, args []string) error { return errNoSudo }
