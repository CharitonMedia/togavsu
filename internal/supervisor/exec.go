package supervisor

import (
	"context"
	"os/exec"
)

func execCombined(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}
