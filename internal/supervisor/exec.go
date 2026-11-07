package supervisor

import (
	"context"
	"os/exec"
)

func execCombined(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := boundDocker(ctx, dockerWaitLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}
