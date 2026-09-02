package cli

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

type doctorReport struct {
	Version   string           `json:"version"`
	OS        string           `json:"os"`
	Arch      string           `json:"arch"`
	EUID      int              `json:"euid"`
	IsRoot    bool             `json:"is_root"`
	IsWSL     bool             `json:"is_wsl"`
	StrongOK  bool             `json:"strong_ok"`
	StrongWhy string           `json:"strong_reason,omitempty"`
	Paths     []doctorPathInfo `json:"paths"`
}

type doctorPathInfo struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	FSType    string `json:"fstype,omitempty"`
	StrongOK  bool   `json:"strong_supported"`
	StrongWhy string `json:"strong_reason,omitempty"`
	State     string `json:"state,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (e *env) cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("afl doctor", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	asJSON := fs.Bool("json", false, "json output")
	paths, err := parseInterleaved(fs, args)
	if err != nil {
		return lock.ExitUsage
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	ok, why := e.deps.StrongOK()
	rep := doctorReport{
		Version: Version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		EUID: os.Geteuid(), IsRoot: e.deps.IsRoot(), IsWSL: platform.IsWSL(),
		StrongOK: ok, StrongWhy: why,
	}
	worst := lock.ExitOK
	for _, p := range paths {
		info := doctorPathInfo{Path: p}
		st, err := e.deps.Locker.Status(p)
		if err != nil {
			info.Error = err.Error()
		} else {
			info.Exists = true
			info.FSType = st.FSType
			info.State = stateName(st)
		}
		info.StrongOK, info.StrongWhy = e.deps.Locker.Supports(p, platform.LevelStrong)
		if !info.StrongOK {
			worst = lock.ExitUnsupported
		}
		rep.Paths = append(rep.Paths, info)
	}
	if *asJSON {
		writeJSON(e.stdout, rep)
		return worst
	}
	w := e.stdout
	fmt.Fprintf(w, "afl %s\n", rep.Version)
	fmt.Fprintf(w, "platform:   %s/%s", rep.OS, rep.Arch)
	if rep.IsWSL {
		fmt.Fprint(w, " (WSL)")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "euid:       %d (root: %v)\n", rep.EUID, rep.IsRoot)
	if rep.StrongOK {
		fmt.Fprintln(w, "strong:     available (immutable flag can be set and cleared)")
	} else {
		fmt.Fprintf(w, "strong:     NOT available — %s\n", rep.StrongWhy)
	}
	fmt.Fprintln(w, "user:       available (chmod a-w)")
	for _, p := range rep.Paths {
		fmt.Fprintf(w, "path:       %s\n", p.Path)
		if p.Error != "" {
			fmt.Fprintf(w, "  error:    %s\n", p.Error)
			continue
		}
		if p.FSType != "" {
			fmt.Fprintf(w, "  fstype:   %s\n", p.FSType)
		}
		fmt.Fprintf(w, "  state:    %s\n", p.State)
		if p.StrongOK {
			fmt.Fprintln(w, "  strong:   supported")
		} else {
			fmt.Fprintf(w, "  strong:   unsupported — %s\n", p.StrongWhy)
		}
	}
	if rep.IsWSL {
		fmt.Fprintln(w, "note:       on WSL keep protected files inside the Linux filesystem (e.g. under ~), not /mnt/<drive>")
	}
	return worst
}
