package gitea

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"kandev-plugin-gitea/internal/sourcecontrol"

	"github.com/kdlbs/kandev/pluginsdk"
)

const (
	ProviderID      = "gitea"
	ReferenceSource = "gitea_pull_requests"
	linksStateKey   = "links"
)

var (
	errNoHost                = errors.New("gitea: plugin host is not available yet")
	errCredentialUnavailable = errors.New("gitea: credential is not available for this scope")
	scpURL                   = regexp.MustCompile(`^[\w.-]+@([^:/\s]+):(.+)$`)
	shortReference           = regexp.MustCompile(`^([^\s/#]+)/([^\s/#]+)#(\d+)$`)
)

// Adapter implements the source-control recipe ports and the Git credential
// extension for the Gitea instance configured in the plugin settings.
type Adapter struct {
	host func() pluginsdk.Host
	// HTTP overrides the client used for Gitea API calls (tests).
	HTTP *http.Client
}

func NewAdapter(host func() pluginsdk.Host) *Adapter { return &Adapter{host: host} }

var (
	_ sourcecontrol.RepositoryLister           = (*Adapter)(nil)
	_ sourcecontrol.RepositoryDetails          = (*Adapter)(nil)
	_ sourcecontrol.AttachedRepositoryResolver = (*Adapter)(nil)
	_ sourcecontrol.ChangeRequestService       = (*Adapter)(nil)
	_ sourcecontrol.AssociationStore           = (*Adapter)(nil)
	_ sourcecontrol.ReviewReader               = (*Adapter)(nil)
	_ sourcecontrol.ReferenceService           = (*Adapter)(nil)
	_ pluginsdk.GitCredentialHandler           = (*Adapter)(nil)
)

// connection is the configured instance, resolved per request: config saves
// restart the plugin process, so there is nothing worth caching.
type connection struct {
	base   *url.URL
	origin string // https://host — Kandev's provider_host
	scope  string // host[/sub-path] — the connection scope on every identity
	token  string
	client *Client
}

func (a *Adapter) hostOrErr() (pluginsdk.Host, error) {
	if h := a.host(); h != nil {
		return h, nil
	}
	return nil, errNoHost
}

func (a *Adapter) connect(ctx context.Context) (connection, error) {
	host, err := a.hostOrErr()
	if err != nil {
		return connection{}, err
	}
	cfg, err := host.GetConfig(ctx)
	if err != nil {
		return connection{}, fmt.Errorf("gitea: read plugin config: %w", err)
	}
	rawBase, _ := cfg["base_url"].(string)
	token, _ := cfg["token"].(string)
	base, err := url.Parse(strings.TrimSpace(rawBase))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return connection{}, errors.New("gitea: base_url must be a credential-free https:// URL (Settings > Plugins > Gitea)")
	}
	if token = strings.TrimSpace(token); token == "" {
		return connection{}, errors.New("gitea: access token is not configured (Settings > Plugins > Gitea)")
	}
	base.Host = strings.ToLower(base.Host)
	base.Path = strings.TrimSuffix(base.Path, "/")
	return connection{
		base: base, origin: "https://" + base.Host, scope: base.Host + base.Path, token: token,
		client: NewClient(base, token, a.HTTP),
	}, nil
}

func (c connection) repository(r Repository) sourcecontrol.Repository {
	return sourcecontrol.Repository{
		ProviderID: ProviderID, ProviderHost: c.origin, ConnectionScope: c.scope,
		RepositoryID: strconv.FormatInt(r.ID, 10), OwnerOrProject: r.OwnerName(), Name: r.Name,
		CloneURL: c.base.String() + "/" + r.FullName + ".git", DefaultBranch: r.DefaultBranch,
	}
}

// --- sourcecontrol.RepositoryLister

func (a *Adapter) ConnectionScope(ctx context.Context, _ string) (string, error) {
	c, err := a.connect(ctx)
	return c.scope, err
}

func (a *Adapter) List(ctx context.Context, _ string, query string, cursor sourcecontrol.RepositoryCursor, limit int) (sourcecontrol.RepositoryPage, error) {
	c, err := a.connect(ctx)
	if err != nil {
		return sourcecontrol.RepositoryPage{}, err
	}
	page := 1
	if cursor.Remote != "" {
		if page, err = strconv.Atoi(cursor.Remote); err != nil || page < 1 {
			return sourcecontrol.RepositoryPage{}, errors.New("gitea: invalid repository cursor")
		}
	}
	repos, more, err := c.client.Repositories(ctx, query, page, limit)
	if err != nil {
		return sourcecontrol.RepositoryPage{}, err
	}
	out := sourcecontrol.RepositoryPage{Repositories: make([]sourcecontrol.Repository, 0, len(repos))}
	for _, r := range repos {
		out.Repositories = append(out.Repositories, c.repository(r))
	}
	if more && len(repos) > 0 {
		out.Next = sourcecontrol.RepositoryCursor{
			Remote: strconv.Itoa(page + 1), AfterRepositoryID: out.Repositories[len(out.Repositories)-1].RepositoryID,
		}
	}
	return out, nil
}

// --- sourcecontrol.RepositoryDetails

func (a *Adapter) Inspect(ctx context.Context, _ string, raw string) (*sourcecontrol.Repository, error) {
	c, err := a.connect(ctx)
	if err != nil {
		return nil, err
	}
	owner, name, ok := c.parseRepositoryURL(raw)
	if !ok {
		return nil, nil
	}
	r, err := c.client.Repository(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	repo := c.repository(r)
	return &repo, nil
}

// parseRepositoryURL accepts https://host[/sub]/owner/name[.git][/...] and
// scp-style git@host:owner/name.git. Anything on another host is not owned
// by this provider.
func (c connection) parseRepositoryURL(raw string) (owner, name string, ok bool) {
	raw = strings.TrimSpace(raw)
	var host, path string
	if m := scpURL.FindStringSubmatch(raw); m != nil {
		host, path = m[1], "/"+m[2]
	} else {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		host, path = u.Hostname(), strings.TrimPrefix(u.Path, c.base.Path)
	}
	if !strings.EqualFold(host, c.base.Hostname()) {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		return "", "", false
	}
	if name = strings.TrimSuffix(parts[1], ".git"); name == "" {
		return "", "", false
	}
	return parts[0], name, true
}

func (a *Adapter) Resolve(ctx context.Context, _ string, id sourcecontrol.RepositoryIdentity) (sourcecontrol.Repository, error) {
	c, err := a.connect(ctx)
	if err != nil {
		return sourcecontrol.Repository{}, err
	}
	if id.ConnectionScope != c.scope {
		return sourcecontrol.Repository{}, errors.New("gitea: repository belongs to a different Gitea connection")
	}
	repoID, err := strconv.ParseInt(id.RepositoryID, 10, 64)
	if err != nil {
		return sourcecontrol.Repository{}, errors.New("gitea: repository id must be numeric")
	}
	r, err := c.client.RepositoryByID(ctx, repoID)
	if err != nil {
		return sourcecontrol.Repository{}, err
	}
	return c.repository(r), nil
}

func (a *Adapter) ListBranches(ctx context.Context, _ string, repo sourcecontrol.Repository) ([]sourcecontrol.Branch, error) {
	c, err := a.connect(ctx)
	if err != nil {
		return nil, err
	}
	branches, err := c.client.Branches(ctx, repo.OwnerOrProject, repo.Name)
	if err != nil {
		return nil, err
	}
	out := make([]sourcecontrol.Branch, 0, len(branches))
	for _, b := range branches {
		out = append(out, sourcecontrol.Branch{Name: b.Name, Commit: b.Commit.ID, IsDefault: b.Name == repo.DefaultBranch})
	}
	return out, nil
}

// --- sourcecontrol.AttachedRepositoryResolver

func (a *Adapter) ResolveAttached(ctx context.Context, action pluginsdk.VerifiedActionContext) (sourcecontrol.Repository, error) {
	c, err := a.connect(ctx)
	if err != nil {
		return sourcecontrol.Repository{}, err
	}
	stored, err := a.storedRepository(ctx, action.WorkspaceID, action.RepositoryID)
	if err != nil {
		return sourcecontrol.Repository{}, err
	}
	if stored.ProviderScope != "" && stored.ProviderScope != c.scope {
		return sourcecontrol.Repository{}, errors.New("gitea: repository belongs to a different Gitea connection")
	}
	return sourcecontrol.Repository{
		ProviderID: ProviderID, ProviderHost: c.origin, ConnectionScope: c.scope,
		RepositoryID: stored.ProviderRepositoryID, OwnerOrProject: stored.OwnerOrProject, Name: stored.ProviderName,
		CloneURL: stored.RemoteURL, DefaultBranch: deref(stored.DefaultBranch),
	}, nil
}

// storedRepository finds one Kandev repository record and checks that this
// provider owns it. Browser descriptors are never authority here.
func (a *Adapter) storedRepository(ctx context.Context, workspaceID, repositoryID string) (pluginsdk.Repository, error) {
	host, err := a.hostOrErr()
	if err != nil {
		return pluginsdk.Repository{}, err
	}
	page := pluginsdk.Page{Limit: 100}
	for {
		repos, info, err := host.Repositories().List(ctx, workspaceID, page)
		if err != nil {
			return pluginsdk.Repository{}, fmt.Errorf("gitea: list workspace repositories: %w", err)
		}
		for _, r := range repos {
			if r.ID != repositoryID {
				continue
			}
			if r.ProviderID != ProviderID || r.ProviderRepositoryID == "" || r.OwnerOrProject == "" || r.ProviderName == "" {
				return pluginsdk.Repository{}, errors.New("gitea: repository is not a Gitea provider repository")
			}
			return r, nil
		}
		if info == nil || !info.HasMore || info.NextCursor == "" {
			return pluginsdk.Repository{}, errors.New("gitea: repository is not attached to this workspace")
		}
		page.Cursor = info.NextCursor
	}
}

// --- sourcecontrol.ChangeRequestService

// parseReference accepts owner/repo#42 or a pull request URL on the
// configured instance.
func (c connection) parseReference(reference string) (owner, name string, number int64, err error) {
	reference = strings.TrimSpace(reference)
	if m := shortReference.FindStringSubmatch(reference); m != nil {
		number, _ = strconv.ParseInt(m[3], 10, 64)
		return m[1], m[2], number, nil
	}
	invalid := errors.New("gitea: reference must be owner/repo#number or a pull request URL on the configured Gitea")
	u, perr := url.Parse(reference)
	if perr != nil || !strings.EqualFold(u.Hostname(), c.base.Hostname()) {
		return "", "", 0, invalid
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(u.Path, c.base.Path), "/"), "/")
	if len(parts) < 4 || parts[2] != "pulls" {
		return "", "", 0, invalid
	}
	if number, perr = strconv.ParseInt(parts[3], 10, 64); perr != nil || number <= 0 {
		return "", "", 0, invalid
	}
	return parts[0], parts[1], number, nil
}

func (c connection) changeRequest(pr PullRequest, repoID int64) sourcecontrol.ChangeRequest {
	return sourcecontrol.ChangeRequest{
		Identity: sourcecontrol.ChangeRequestIdentity{
			ConnectionScope: c.scope, RepositoryID: strconv.FormatInt(repoID, 10), Number: pr.Number,
		},
		Title: pr.Title, URL: pr.HTMLURL,
	}
}

func (a *Adapter) ResolveReference(ctx context.Context, _ string, reference string) (sourcecontrol.ChangeRequest, error) {
	c, err := a.connect(ctx)
	if err != nil {
		return sourcecontrol.ChangeRequest{}, err
	}
	owner, name, number, err := c.parseReference(reference)
	if err != nil {
		return sourcecontrol.ChangeRequest{}, err
	}
	pr, err := c.client.PullRequest(ctx, owner, name, number)
	if err != nil {
		return sourcecontrol.ChangeRequest{}, err
	}
	return c.changeRequest(pr, pr.Base.RepoID), nil
}

func (a *Adapter) Create(ctx context.Context, repo sourcecontrol.Repository, head string, in sourcecontrol.CreateChangeRequestInput) (sourcecontrol.ChangeRequest, error) {
	c, err := a.connect(ctx)
	if err != nil {
		return sourcecontrol.ChangeRequest{}, err
	}
	base := strings.TrimSpace(in.Destination)
	if base == "" {
		base = repo.DefaultBranch
	}
	if base == "" { // Older stored records may lack default_branch; ask Gitea.
		if repoID, err := strconv.ParseInt(repo.RepositoryID, 10, 64); err == nil {
			if r, err := c.client.RepositoryByID(ctx, repoID); err == nil {
				base = r.DefaultBranch
			}
		}
	}
	if base == "" {
		return sourcecontrol.ChangeRequest{}, errors.New("gitea: destination branch is required")
	}
	title := strings.TrimSpace(in.Title)
	if upper := strings.ToUpper(title); in.Draft && !strings.HasPrefix(upper, "WIP:") && !strings.HasPrefix(upper, "[WIP]") {
		title = "WIP: " + title // Gitea marks drafts by title prefix.
	}
	pr, err := c.client.CreatePullRequest(ctx, repo.OwnerOrProject, repo.Name, CreatePullRequest{
		Head: head, Base: base, Title: title, Body: in.Description,
	})
	if err != nil {
		return sourcecontrol.ChangeRequest{}, err
	}
	repoID := pr.Base.RepoID
	if repoID == 0 {
		repoID, _ = strconv.ParseInt(repo.RepositoryID, 10, 64)
	}
	return c.changeRequest(pr, repoID), nil
}

// --- sourcecontrol.AssociationStore: one workspace-scoped Host state object
// holding every task <-> pull-request link.

type link struct {
	TaskID          string `json:"task_id"`
	ConnectionScope string `json:"connection_scope"`
	RepositoryID    string `json:"repository_id"`
	Number          int64  `json:"number"`
	Repository      string `json:"repository,omitempty"` // owner/name at link time; display only
}

func (l link) identity() sourcecontrol.ChangeRequestIdentity {
	return sourcecontrol.ChangeRequestIdentity{ConnectionScope: l.ConnectionScope, RepositoryID: l.RepositoryID, Number: l.Number}
}

func (l link) reviewKey() string {
	repo := l.Repository
	if repo == "" {
		repo = l.RepositoryID
	}
	return fmt.Sprintf("%s#%d", repo, l.Number)
}

func (a *Adapter) links(ctx context.Context, workspaceID string) ([]link, error) {
	host, err := a.hostOrErr()
	if err != nil {
		return nil, err
	}
	value, found, err := host.GetState(ctx, "workspace", workspaceID, linksStateKey)
	if err != nil {
		return nil, fmt.Errorf("gitea: read task links: %w", err)
	}
	if !found {
		return nil, nil
	}
	raw, err := json.Marshal(value["items"])
	if err != nil {
		return nil, err
	}
	var links []link
	if err := json.Unmarshal(raw, &links); err != nil {
		return nil, fmt.Errorf("gitea: decode task links: %w", err)
	}
	return links, nil
}

func (a *Adapter) saveLinks(ctx context.Context, workspaceID string, links []link) error {
	host, err := a.hostOrErr()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(links)
	if err != nil {
		return err
	}
	items := []any{}
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	if items == nil {
		items = []any{}
	}
	return host.SetState(ctx, "workspace", workspaceID, linksStateKey, map[string]any{"items": items})
}

func (a *Adapter) taskWorkspace(ctx context.Context, taskID string) (string, error) {
	host, err := a.hostOrErr()
	if err != nil {
		return "", err
	}
	task, err := host.Tasks().Get(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("gitea: load task: %w", err)
	}
	if task == nil || task.WorkspaceID == "" {
		return "", errors.New("gitea: task workspace is unknown")
	}
	return task.WorkspaceID, nil
}

func (a *Adapter) Link(ctx context.Context, taskID string, id sourcecontrol.ChangeRequestIdentity) error {
	workspaceID, err := a.taskWorkspace(ctx, taskID)
	if err != nil {
		return err
	}
	links, err := a.links(ctx, workspaceID)
	if err != nil {
		return err
	}
	for _, l := range links {
		if l.TaskID == taskID && l.identity() == id {
			return nil
		}
	}
	entry := link{TaskID: taskID, ConnectionScope: id.ConnectionScope, RepositoryID: id.RepositoryID, Number: id.Number}
	if c, err := a.connect(ctx); err == nil {
		if repoID, err := strconv.ParseInt(id.RepositoryID, 10, 64); err == nil {
			if r, err := c.client.RepositoryByID(ctx, repoID); err == nil {
				entry.Repository = r.FullName
			}
		}
	}
	return a.saveLinks(ctx, workspaceID, append(links, entry))
}

func (a *Adapter) Unlink(ctx context.Context, taskID string, id sourcecontrol.ChangeRequestIdentity) error {
	workspaceID, err := a.taskWorkspace(ctx, taskID)
	if err != nil {
		return err
	}
	links, err := a.links(ctx, workspaceID)
	if err != nil {
		return err
	}
	kept := make([]link, 0, len(links))
	for _, l := range links {
		if l.TaskID != taskID || l.identity() != id {
			kept = append(kept, l)
		}
	}
	return a.saveLinks(ctx, workspaceID, kept)
}

// --- sourcecontrol.ReviewReader

func (a *Adapter) Associations(ctx context.Context, workspaceID string) ([]sourcecontrol.ReviewAssociation, error) {
	links, err := a.links(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]sourcecontrol.ReviewAssociation, 0, len(links))
	for _, l := range links {
		out = append(out, sourcecontrol.ReviewAssociation{
			ProviderID: ProviderID, TaskID: l.TaskID, ReviewKey: l.reviewKey(),
			ConnectionScope: l.ConnectionScope, RepositoryID: l.RepositoryID, ChangeRequestNumber: l.Number,
		})
	}
	return out, nil
}

func (a *Adapter) ForTask(ctx context.Context, workspaceID, taskID string) ([]sourcecontrol.ReviewSummary, error) {
	links, err := a.links(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	c, err := a.connect(ctx)
	if err != nil {
		return nil, err
	}
	out := []sourcecontrol.ReviewSummary{}
	for _, l := range links {
		if l.TaskID != taskID {
			continue
		}
		summary, err := c.review(ctx, l)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			log.Printf("gitea: refresh %s: %v", l.reviewKey(), err)
			summary = sourcecontrol.ReviewSummary{
				ReviewKey: l.reviewKey(), Title: l.reviewKey() + " (unavailable)", URL: c.base.String(), State: "unknown",
			}
		}
		summary.ProviderID, summary.ConnectionScope = ProviderID, l.ConnectionScope
		summary.RepositoryID, summary.ChangeRequestNumber = l.RepositoryID, l.Number
		out = append(out, summary)
	}
	return out, nil
}

func (c connection) review(ctx context.Context, l link) (sourcecontrol.ReviewSummary, error) {
	if l.ConnectionScope != c.scope {
		return sourcecontrol.ReviewSummary{}, errors.New("linked on a different Gitea connection")
	}
	repoID, err := strconv.ParseInt(l.RepositoryID, 10, 64)
	if err != nil {
		return sourcecontrol.ReviewSummary{}, err
	}
	repo, err := c.client.RepositoryByID(ctx, repoID)
	if err != nil {
		return sourcecontrol.ReviewSummary{}, err
	}
	owner, name := repo.OwnerName(), repo.Name
	pr, err := c.client.PullRequest(ctx, owner, name, l.Number)
	if err != nil {
		return sourcecontrol.ReviewSummary{}, err
	}
	// Reviews and CI are best-effort decorations; only cancellation aborts.
	reviews, err := c.client.Reviews(ctx, owner, name, l.Number)
	if err != nil && ctx.Err() != nil {
		return sourcecontrol.ReviewSummary{}, err
	}
	status, err := c.client.CombinedStatus(ctx, owner, name, pr.Head.Sha)
	if err != nil && ctx.Err() != nil {
		return sourcecontrol.ReviewSummary{}, err
	}
	return sourcecontrol.ReviewSummary{
		ReviewKey: fmt.Sprintf("%s#%d", repo.FullName, pr.Number), Title: pr.Title, URL: pr.HTMLURL, State: prState(pr),
		TaskStatus: taskStatus(pr, status, reviews), Detail: detail(pr, reviews),
	}, nil
}

// --- sourcecontrol.ReferenceService

func (a *Adapter) Search(ctx context.Context, _ string, query string, limit int) ([]pluginsdk.EntityReferenceCandidate, error) {
	c, err := a.connect(ctx)
	if err != nil {
		return nil, err
	}
	issues, err := c.client.SearchPullRequests(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]pluginsdk.EntityReferenceCandidate, 0, len(issues))
	for _, i := range issues {
		repoID := strconv.FormatInt(i.Repository.ID, 10)
		out = append(out, pluginsdk.EntityReferenceCandidate{
			ProviderLocalID: repoID + "/" + strconv.FormatInt(i.Number, 10),
			Title:           fmt.Sprintf("%s#%d %s", i.Repository.FullName, i.Number, i.Title),
			URL:             i.HTMLURL,
			Attributes: map[string]any{
				"connection_scope": c.scope, "repository_id": repoID, "number": i.Number,
				"repository": i.Repository.FullName, "state": i.State,
			},
		})
	}
	return out, nil
}

// Authorize re-checks live access to the referenced pull request. Kandev
// hands the candidate's provider-local id back as "id"; anything else fails
// closed.
func (a *Adapter) Authorize(ctx context.Context, _ string, _ string, reference map[string]any) (bool, error) {
	c, err := a.connect(ctx)
	if err != nil {
		return false, err
	}
	id, _ := reference["id"].(string)
	repoRaw, numberRaw, ok := strings.Cut(id, "/")
	repoID, repoErr := strconv.ParseInt(repoRaw, 10, 64)
	number, numberErr := strconv.ParseInt(numberRaw, 10, 64)
	if !ok || repoErr != nil || numberErr != nil || repoID <= 0 || number <= 0 {
		return false, nil
	}
	repo, err := c.client.RepositoryByID(ctx, repoID)
	if err == nil {
		_, err = c.client.PullRequest(ctx, repo.OwnerName(), repo.Name, number)
	}
	switch {
	case err == nil:
		return true, nil
	case IsNotFound(err):
		return false, nil
	default:
		return false, err
	}
}

// --- pluginsdk.GitCredentialHandler

func (a *Adapter) ResolveGitCredential(ctx context.Context, req *pluginsdk.ResolveGitCredentialRequest) (*pluginsdk.ResolveGitCredentialResponse, error) {
	if req == nil {
		return nil, errCredentialUnavailable
	}
	c, err := a.credentialScope(ctx, req.ProviderID, req.WorkspaceID, req.TaskID, req.SessionID, req.RepositoryID, req.Host, req.Path)
	if err != nil {
		return nil, err
	}
	user, err := c.client.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	return &pluginsdk.ResolveGitCredentialResponse{Username: user.Login, Secret: c.token}, nil
}

// GetGitCredentialBinding returns a non-secret revision derived from the
// configured connection: it changes when the URL or token changes and is
// empty (revoked) when the lease scope is no longer valid.
func (a *Adapter) GetGitCredentialBinding(ctx context.Context, req *pluginsdk.GitCredentialBindingRequest) (*pluginsdk.GitCredentialBindingResponse, error) {
	if req == nil {
		return &pluginsdk.GitCredentialBindingResponse{}, nil
	}
	c, err := a.credentialScope(ctx, req.ProviderID, req.WorkspaceID, req.TaskID, req.SessionID, req.RepositoryID, req.Host, req.Path)
	if err != nil {
		return &pluginsdk.GitCredentialBindingResponse{}, nil
	}
	sum := sha256.Sum256([]byte(c.scope + "\n" + c.token))
	return &pluginsdk.GitCredentialBindingResponse{Binding: "gitea-token:" + hex.EncodeToString(sum[:8])}, nil
}

// credentialScope verifies a host-issued lease scope against the configured
// instance and the stored repository and task before any secret is read.
func (a *Adapter) credentialScope(ctx context.Context, providerID, workspaceID, taskID, sessionID, repositoryID, host, path string) (connection, error) {
	if providerID != ProviderID || workspaceID == "" || taskID == "" || sessionID == "" || repositoryID == "" || host == "" || !strings.HasPrefix(path, "/") {
		return connection{}, errCredentialUnavailable
	}
	c, err := a.connect(ctx)
	if err != nil {
		return connection{}, err
	}
	if !strings.EqualFold(host, c.base.Host) {
		return connection{}, errCredentialUnavailable
	}
	stored, err := a.storedRepository(ctx, workspaceID, repositoryID)
	if err != nil {
		return connection{}, fmt.Errorf("%w: %v", errCredentialUnavailable, err)
	}
	remote, err := url.Parse(stored.RemoteURL)
	if err != nil || !strings.EqualFold(remote.Host, host) || clonePath(remote.Path) != clonePath(path) {
		return connection{}, errCredentialUnavailable
	}
	task, err := a.host().Tasks().Get(ctx, taskID)
	if err != nil || task == nil || task.WorkspaceID != workspaceID || !attaches(task, repositoryID) {
		return connection{}, errCredentialUnavailable
	}
	return c, nil
}

func attaches(task *pluginsdk.Task, repositoryID string) bool {
	for _, r := range task.Repositories {
		if r.RepositoryID == repositoryID {
			return true
		}
	}
	return false
}

func clonePath(p string) string {
	return strings.TrimSuffix(strings.TrimSuffix(p, "/"), ".git")
}
