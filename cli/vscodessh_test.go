package cli_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/sloggers/slogtest"

	"github.com/coder/coder/agent"
	"github.com/coder/coder/cli/clitest"
	"github.com/coder/coder/coderd/coderdtest"
	"github.com/coder/coder/codersdk"
	"github.com/coder/coder/codersdk/agentsdk"
	"github.com/coder/coder/pty/ptytest"
	"github.com/coder/coder/testutil"
)

// TestVSCodeSSH ensures the agent connects properly with SSH
// and that network information is properly written to the FS.
func TestVSCodeSSH(t *testing.T) {
	t.Parallel()
	ctx := testutil.Context(t, testutil.WaitLong)
	client, workspace, agentToken := setupWorkspaceForAgent(t, nil)
	user, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	agentClient := agentsdk.New(client.URL)
	agentClient.SetSessionToken(agentToken)
	agentCloser := agent.New(agent.Options{
		Client: agentClient,
		Logger: slogtest.Make(t, nil).Named("agent"),
	})
	defer func() {
		_ = agentCloser.Close()
	}()
	coderdtest.AwaitWorkspaceAgents(t, client, workspace.ID)

	fs := afero.NewMemMapFs()
	err = afero.WriteFile(fs, "/url", []byte(client.URL.String()), 0o600)
	require.NoError(t, err)
	err = afero.WriteFile(fs, "/token", []byte(client.SessionToken()), 0o600)
	require.NoError(t, err)

	//nolint:revive,staticcheck
	ctx = context.WithValue(ctx, "fs", fs)

	inv, _ := clitest.New(t,
		"vscodessh",
		"--url-file", "/url",
		"--session-token-file", "/token",
		"--network-info-dir", "/net",
		"--network-info-interval", "25ms",
		fmt.Sprintf("coder-vscode--%s--%s", user.Username, workspace.Name),
	)
	ptytest.New(t).Attach(inv)

	waiter := clitest.StartWithWaiter(t, inv.WithContext(ctx))

	assert.Eventually(t, func() bool {
		entries, err := afero.ReadDir(fs, "/net")
		if err != nil {
			return false
		}
		return len(entries) > 0
	}, testutil.WaitLong, testutil.IntervalFast)
	waiter.Cancel()

	if err := waiter.Wait(); err != nil {
		waiter.RequireIs(context.Canceled)
	}
}
