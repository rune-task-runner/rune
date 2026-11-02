package agent

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// CLIProvider drives an installed agent CLI. It is the v1 concrete Provider.
type CLIProvider struct{}

// Run resolves the configured agent CLI, hands it the prompt and (if present)
// the in-process MCP endpoint so it can call back into allowed tasks, and
// returns its final stdout as the task output.
func (CLIProvider) Run(ctx context.Context, prompt string, opts Options) (string, error) {
	if len(opts.AgentCmd) == 0 {
		return "", &NotConfiguredError{}
	}
	bin := opts.AgentCmd[0]
	if _, err := exec.LookPath(bin); err != nil {
		return "", &NotInstalledError{Name: bin}
	}

	args := append(append([]string{}, opts.AgentCmd[1:]...), prompt)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = opts.Dir
	cmd.Stdin = opts.Stdin
	cmd.Stderr = opts.Stderr

	env := append([]string{}, opts.Env...)
	if opts.MCPURL != "" {
		env = append(env, "RUNE_MCP_URL="+opts.MCPURL)
	}
	if opts.MCPToken != "" {
		env = append(env, "RUNE_MCP_TOKEN="+opts.MCPToken)
	}
	cmd.Env = env

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		// On cancel, Wait reports the kill as an *exec.ExitError rather than
		// the context error, so the context is checked first.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.String(), &AuthError{Name: bin, Err: err}
		}
		return "", &AuthError{Name: bin, Err: err}
	}
	return out.String(), nil
}
