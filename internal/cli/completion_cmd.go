package cli

import (
	"fmt"
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/completion"
	"github.com/Mineru98/agent-file-lock/internal/lock"
)

func (e *env) cmdCompletion(args []string) int {
	if len(args) != 1 {
		return e.usageErr("completion requires a shell: %s", strings.Join(completion.Shells, "|"))
	}
	script, err := completion.Script(args[0])
	if err != nil {
		return e.usageErr("%v", err)
	}
	fmt.Fprint(e.stdout, script)
	return lock.ExitOK
}
