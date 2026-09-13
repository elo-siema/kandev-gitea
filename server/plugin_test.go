package main

import (
	"context"
	"testing"

	"kandev-plugin-gitea/internal/gitea"
	"kandev-plugin-gitea/internal/sourcecontrol"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"
)

func TestNewPluginFailsClosedWithoutHost(t *testing.T) {
	p := newPlugin()
	require.Equal(t, gitea.ProviderID, p.ProviderID)
	require.Equal(t, gitea.ReferenceSource, p.ReferenceSource)

	_, err := p.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: sourcecontrol.ActionRepositoriesList,
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1"},
		Body:      []byte(`{}`),
	})
	require.ErrorContains(t, err, "plugin host is not available")

	binding, err := p.GetGitCredentialBinding(context.Background(), &pluginsdk.GitCredentialBindingRequest{
		ProviderID: gitea.ProviderID, WorkspaceID: "w", TaskID: "t", SessionID: "s", RepositoryID: "r",
		Host: "gitea.example.com", Path: "/owner/repo.git",
	})
	require.NoError(t, err)
	require.Empty(t, binding.Binding, "an empty binding is the documented revocation signal")

	_, err = p.ResolveGitCredential(context.Background(), &pluginsdk.ResolveGitCredentialRequest{ProviderID: "github"})
	require.Error(t, err)
}
