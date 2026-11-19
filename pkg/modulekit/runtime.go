package modulekit

import "github.com/CharitonMedia/togavsu/internal/modules"

// RuntimeSupported reports a behavior built and tested in this pinned core.
func RuntimeSupported(criterion string) bool { return modules.RuntimeSupported(criterion) }
