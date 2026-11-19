package main

import (
	"context"

	"kandev-plugin-gitea/internal/gitea"
	"kandev-plugin-gitea/internal/sourcecontrol"

	"github.com/kdlbs/kandev/pluginsdk"
)

// giteaPlugin wires the provider-neutral recipe to the Gitea adapter and adds
// the optional Git credential extension.
type giteaPlugin struct {
	pluginsdk.UnimplementedPlugin
	sourcecontrol.Extension
	adapter *gitea.Adapter
}

var (
	_ pluginsdk.Plugin                 = (*giteaPlugin)(nil)
	_ pluginsdk.ActionHandler          = (*giteaPlugin)(nil)
	_ pluginsdk.EntityReferenceHandler = (*giteaPlugin)(nil)
	_ pluginsdk.GitCredentialHandler   = (*giteaPlugin)(nil)
)

func newPlugin() *giteaPlugin {
	p := &giteaPlugin{}
	p.adapter = gitea.NewAdapter(p.Host)
	p.Extension = sourcecontrol.Extension{
		ProviderID:           gitea.ProviderID,
		ReferenceSource:      gitea.ReferenceSource,
		Repositories:         p.adapter,
		RepositoryDetails:    p.adapter,
		AttachedRepositories: p.adapter,
		ChangeRequests:       p.adapter,
		Associations:         p.adapter,
		Reviews:              p.adapter,
		References:           p.adapter,
	}
	return p
}

func (p *giteaPlugin) ResolveGitCredential(ctx context.Context, req *pluginsdk.ResolveGitCredentialRequest) (*pluginsdk.ResolveGitCredentialResponse, error) {
	return p.adapter.ResolveGitCredential(ctx, req)
}

func (p *giteaPlugin) GetGitCredentialBinding(ctx context.Context, req *pluginsdk.GitCredentialBindingRequest) (*pluginsdk.GitCredentialBindingResponse, error) {
	return p.adapter.GetGitCredentialBinding(ctx, req)
}
