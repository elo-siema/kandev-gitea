package gitea

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"kandev-plugin-gitea/internal/sourcecontrol"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"
)

type fakeTasks struct {
	pluginsdk.TaskReader
	tasks map[string]*pluginsdk.Task
}

func (f fakeTasks) Get(_ context.Context, id string) (*pluginsdk.Task, error) {
	if t, ok := f.tasks[id]; ok {
		return t, nil
	}
	return nil, errors.New("task not found")
}

type fakeRepos struct {
	pluginsdk.RepositoryReader
	repos []pluginsdk.Repository
}

func (f fakeRepos) List(_ context.Context, workspaceID string, _ pluginsdk.Page) ([]pluginsdk.Repository, *pluginsdk.PageInfo, error) {
	var out []pluginsdk.Repository
	for _, r := range f.repos {
		if r.WorkspaceID == workspaceID {
			out = append(out, r)
		}
	}
	return out, &pluginsdk.PageInfo{}, nil
}

// fakeHost stores state through a JSON round trip, mirroring the protobuf
// Struct conversion of the real Host (numbers come back as float64).
type fakeHost struct {
	pluginsdk.UnimplementedHostData
	mu     sync.Mutex
	config map[string]any
	state  map[string]map[string]any
	tasks  fakeTasks
	repos  fakeRepos
}

func (h *fakeHost) GetState(_ context.Context, scope, scopeID, key string) (map[string]any, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.state[scope+"/"+scopeID+"/"+key]
	return v, ok, nil
}

func (h *fakeHost) SetState(_ context.Context, scope, scopeID, key string, value map[string]any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		return err
	}
	h.state[scope+"/"+scopeID+"/"+key] = stored
	return nil
}

func (h *fakeHost) DeleteState(context.Context, string, string, string) error { return nil }
func (h *fakeHost) ListState(context.Context, string, string) ([]pluginsdk.StateEntry, error) {
	return nil, nil
}
func (h *fakeHost) GetConfig(context.Context) (map[string]any, error)       { return h.config, nil }
func (h *fakeHost) RevealSecret(context.Context, string) (string, error)    { return "", nil }
func (h *fakeHost) GetSecret(context.Context, string) (string, bool, error) { return "", false, nil }
func (h *fakeHost) SetSecret(context.Context, string, string) error         { return nil }
func (h *fakeHost) DeleteSecret(context.Context, string) error              { return nil }
func (h *fakeHost) EmitEvent(context.Context, string, map[string]any) error { return nil }
func (h *fakeHost) Tasks() pluginsdk.TaskReader                             { return h.tasks }
func (h *fakeHost) Repositories() pluginsdk.RepositoryReader                { return h.repos }

var _ pluginsdk.Host = (*fakeHost)(nil)

// fakeGitea serves the subset of the Gitea API the adapter uses, mounted
// under a sub-path to prove base_url paths are honoured.
type fakeGitea struct {
	mu       sync.Mutex
	requests []string
	created  *CreatePullRequest
	pr       PullRequest
	reviews  []Review
	status   CombinedStatus
}

func (g *fakeGitea) handler(t *testing.T) http.Handler {
	widgets := Repository{ID: 9, Name: "widgets", FullName: "tools/widgets", Owner: User{Login: "tools"}, DefaultBranch: "main"}
	gadgets := Repository{ID: 10, Name: "gadgets", FullName: "tools/gadgets", Owner: User{Login: "tools"}, DefaultBranch: "dev"}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.requests = append(g.requests, r.Method+" "+r.URL.RequestURI())
		g.mu.Unlock()
		if r.Header.Get("Authorization") != "token secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		write := func(v any) { require.NoError(t, json.NewEncoder(w).Encode(v)) }
		path := strings.TrimPrefix(r.URL.Path, "/gitea/api/v1")
		switch {
		case path == "/user":
			write(User{ID: 1, Login: "bot"})
		case path == "/user/repos":
			write([]Repository{widgets, gadgets})
		case path == "/repos/search":
			write(map[string]any{"ok": true, "data": []Repository{widgets}})
		case path == "/repos/tools/widgets", path == "/repositories/9":
			write(widgets)
		case path == "/repos/tools/widgets/branches":
			write([]Branch{{Name: "main"}, {Name: "task/42"}})
		case path == "/repos/tools/widgets/pulls" && r.Method == http.MethodPost:
			var in CreatePullRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
			g.created = &in
			write(PullRequest{Number: 43, Title: in.Title, HTMLURL: "https://gitea.example/tools/widgets/pulls/43", State: "open",
				Head: PRBranch{Ref: in.Head}, Base: PRBranch{Ref: in.Base, RepoID: 9}})
		case path == "/repos/tools/widgets/pulls/42":
			write(g.pr)
		case path == "/repos/tools/widgets/pulls/42/reviews":
			write(g.reviews)
		case strings.HasPrefix(path, "/repos/tools/widgets/commits/") && strings.HasSuffix(path, "/status"):
			write(g.status)
		case path == "/repos/issues/search":
			issue := Issue{Number: 42, Title: "Add hooks", HTMLURL: "https://gitea.example/tools/widgets/pulls/42", State: "open"}
			issue.Repository.ID, issue.Repository.FullName = 9, "tools/widgets"
			write([]Issue{issue})
		default:
			w.WriteHeader(http.StatusNotFound)
			write(map[string]string{"message": "not found"})
		}
	})
}

func (g *fakeGitea) requested(t *testing.T, substring string) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range g.requests {
		if strings.Contains(r, substring) {
			return
		}
	}
	t.Fatalf("no request containing %q in %v", substring, g.requests)
}

type harness struct {
	adapter *Adapter
	host    *fakeHost
	gitea   *fakeGitea
	base    string // https://127.0.0.1:port/gitea
	hostPrt string // 127.0.0.1:port
	scope   string
}

func newHarness(t *testing.T) *harness {
	g := &fakeGitea{pr: PullRequest{
		Number: 42, Title: "Add hooks", Body: "Body", HTMLURL: "https://gitea.example/tools/widgets/pulls/42", State: "open",
		UpdatedAt: "2026-09-01T10:00:00Z", CreatedAt: "2026-08-30T10:00:00Z", User: User{Login: "alice", FullName: "Alice"},
		Head: PRBranch{Ref: "task/42", Sha: "abc123", RepoID: 9}, Base: PRBranch{Ref: "main", RepoID: 9},
		RequestedReviewers: []User{{Login: "dave"}}, Additions: 3, Deletions: 1,
	}}
	server := httptest.NewTLSServer(g.handler(t))
	t.Cleanup(server.Close)
	hostPort := strings.TrimPrefix(server.URL, "https://")
	base := server.URL + "/gitea"
	host := &fakeHost{
		config: map[string]any{"base_url": base + "/", "token": "secret-token"},
		state:  map[string]map[string]any{},
		tasks: fakeTasks{tasks: map[string]*pluginsdk.Task{
			"task-1": {ID: "task-1", WorkspaceID: "workspace-1", Repositories: []pluginsdk.TaskRepository{{RepositoryID: "repo-1"}}},
			"task-2": {ID: "task-2", WorkspaceID: "workspace-1"},
		}},
		repos: fakeRepos{repos: []pluginsdk.Repository{{
			ID: "repo-1", WorkspaceID: "workspace-1", ProviderID: ProviderID, ProviderRepositoryID: "9",
			ProviderHost: "https://" + hostPort, ProviderScope: hostPort + "/gitea", OwnerOrProject: "tools",
			ProviderName: "widgets", RemoteURL: base + "/tools/widgets.git",
		}}},
	}
	adapter := NewAdapter(func() pluginsdk.Host { return host })
	adapter.HTTP = server.Client()
	return &harness{adapter: adapter, host: host, gitea: g, base: base, hostPrt: hostPort, scope: hostPort + "/gitea"}
}

func TestListRepositoriesPagesThroughGitea(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	page, err := h.adapter.List(ctx, "workspace-1", "", sourcecontrol.RepositoryCursor{}, 2)
	require.NoError(t, err)
	h.gitea.requested(t, "GET /gitea/api/v1/user/repos?limit=2&page=1")
	require.Len(t, page.Repositories, 2)
	require.Equal(t, sourcecontrol.Repository{
		ProviderID: "gitea", ProviderHost: "https://" + h.hostPrt, ConnectionScope: h.scope, RepositoryID: "9",
		OwnerOrProject: "tools", Name: "widgets", CloneURL: h.base + "/tools/widgets.git", DefaultBranch: "main",
	}, page.Repositories[0])
	require.Equal(t, sourcecontrol.RepositoryCursor{Remote: "2", AfterRepositoryID: "10"}, page.Next)

	page, err = h.adapter.List(ctx, "workspace-1", "wid", sourcecontrol.RepositoryCursor{Remote: "2"}, 50)
	require.NoError(t, err)
	h.gitea.requested(t, "GET /gitea/api/v1/repos/search?limit=50&order=desc&page=2&q=wid&sort=updated")
	require.Len(t, page.Repositories, 1)
	require.Empty(t, page.Next.Remote, "a short page ends pagination")

	_, err = h.adapter.List(ctx, "workspace-1", "", sourcecontrol.RepositoryCursor{Remote: "zero"}, 50)
	require.ErrorContains(t, err, "invalid repository cursor")
}

func TestConnectRequiresHTTPSAndToken(t *testing.T) {
	h := newHarness(t)
	h.host.config["base_url"] = "http://" + h.hostPrt
	_, err := h.adapter.ConnectionScope(context.Background(), "workspace-1")
	require.ErrorContains(t, err, "https://")
	h.host.config = map[string]any{"base_url": h.base}
	_, err = h.adapter.ConnectionScope(context.Background(), "workspace-1")
	require.ErrorContains(t, err, "access token")
}

func TestInspectOwnsOnlyConfiguredHost(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	repo, err := h.adapter.Inspect(ctx, "workspace-1", "https://github.com/tools/widgets")
	require.NoError(t, err)
	require.Nil(t, repo, "other hosts are not owned")

	repo, err = h.adapter.Inspect(ctx, "workspace-1", h.base+"/tools/widgets.git")
	require.NoError(t, err)
	require.Equal(t, "9", repo.RepositoryID)
	require.Equal(t, h.base+"/tools/widgets.git", repo.CloneURL)

	repo, err = h.adapter.Inspect(ctx, "workspace-1", "git@"+strings.Split(h.hostPrt, ":")[0]+":tools/widgets.git")
	require.NoError(t, err)
	require.Equal(t, "widgets", repo.Name)

	_, err = h.adapter.Inspect(ctx, "workspace-1", h.base+"/tools/missing")
	require.True(t, IsNotFound(err), "unknown repositories surface as not found: %v", err)
}

func TestResolveAndBranchesUseImmutableIdentity(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	_, err := h.adapter.Resolve(ctx, "workspace-1", sourcecontrol.RepositoryIdentity{ConnectionScope: "other.example", RepositoryID: "9"})
	require.ErrorContains(t, err, "different Gitea connection")

	repo, err := h.adapter.Resolve(ctx, "workspace-1", sourcecontrol.RepositoryIdentity{ConnectionScope: h.scope, RepositoryID: "9"})
	require.NoError(t, err)
	h.gitea.requested(t, "GET /gitea/api/v1/repositories/9")

	branches, err := h.adapter.ListBranches(ctx, "workspace-1", repo)
	require.NoError(t, err)
	require.Equal(t, []sourcecontrol.Branch{{Name: "main", IsDefault: true}, {Name: "task/42"}}, branches)
}

func TestCreateUsesAttachedRepositoryAndDraftPrefix(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	repo, err := h.adapter.ResolveAttached(ctx, pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1", RepositoryID: "repo-1"})
	require.NoError(t, err)
	require.Equal(t, "9", repo.RepositoryID)
	require.Equal(t, "widgets", repo.Name)

	_, err = h.adapter.ResolveAttached(ctx, pluginsdk.VerifiedActionContext{WorkspaceID: "workspace-1", RepositoryID: "repo-9"})
	require.ErrorContains(t, err, "not attached")

	created, err := h.adapter.Create(ctx, repo, "task/42", sourcecontrol.CreateChangeRequestInput{Title: "Add hooks", Description: "Body", Draft: true})
	require.NoError(t, err)
	require.Equal(t, &CreatePullRequest{Head: "task/42", Base: "main", Title: "WIP: Add hooks", Body: "Body"}, h.gitea.created)
	require.Equal(t, sourcecontrol.ChangeRequest{
		Identity: sourcecontrol.ChangeRequestIdentity{ConnectionScope: h.scope, RepositoryID: "9", Number: 43},
		Title:    "WIP: Add hooks", URL: "https://gitea.example/tools/widgets/pulls/43",
	}, created)
}

func TestResolveReferenceAcceptsShortAndURLForms(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	want := sourcecontrol.ChangeRequestIdentity{ConnectionScope: h.scope, RepositoryID: "9", Number: 42}

	change, err := h.adapter.ResolveReference(ctx, "workspace-1", " tools/widgets#42 ")
	require.NoError(t, err)
	require.Equal(t, want, change.Identity)
	require.Equal(t, "Add hooks", change.Title)

	change, err = h.adapter.ResolveReference(ctx, "workspace-1", h.base+"/tools/widgets/pulls/42")
	require.NoError(t, err)
	require.Equal(t, want, change.Identity)

	_, err = h.adapter.ResolveReference(ctx, "workspace-1", "https://github.com/tools/widgets/pull/42")
	require.ErrorContains(t, err, "configured Gitea")
}

func TestLinksAndReviewsRoundTripThroughHostState(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.gitea.reviews = []Review{
		{ID: 1, State: "APPROVED", User: User{Login: "bob", FullName: "Bob"}},
		{ID: 2, State: "REQUEST_CHANGES", User: User{Login: "carol"}, Body: "fix it"},
		{ID: 3, State: "APPROVED", User: User{Login: "erin"}, Dismissed: true},
		{ID: 4, State: "COMMENT", User: User{Login: "bob"}},
	}
	h.gitea.status = CombinedStatus{State: "failure", Statuses: []CommitStatus{
		{ID: 1, Context: "build", Status: "success"},
		{ID: 2, Context: "test", Status: "failure", Description: "1 failed", TargetURL: "https://ci.example/2"},
	}}
	identity := sourcecontrol.ChangeRequestIdentity{ConnectionScope: h.scope, RepositoryID: "9", Number: 42}

	require.NoError(t, h.adapter.Link(ctx, "task-1", identity))
	require.NoError(t, h.adapter.Link(ctx, "task-1", identity), "linking twice is idempotent")
	require.NoError(t, h.adapter.Link(ctx, "task-2", identity))
	require.Error(t, h.adapter.Link(ctx, "task-missing", identity))

	associations, err := h.adapter.Associations(ctx, "workspace-1")
	require.NoError(t, err)
	require.Equal(t, []sourcecontrol.ReviewAssociation{
		{ProviderID: "gitea", TaskID: "task-1", ReviewKey: "tools/widgets#42", ConnectionScope: h.scope, RepositoryID: "9", ChangeRequestNumber: 42},
		{ProviderID: "gitea", TaskID: "task-2", ReviewKey: "tools/widgets#42", ConnectionScope: h.scope, RepositoryID: "9", ChangeRequestNumber: 42},
	}, associations)

	reviews, err := h.adapter.ForTask(ctx, "workspace-1", "task-1")
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	review := reviews[0]
	require.Equal(t, "gitea", review.ProviderID)
	require.Equal(t, identity, sourcecontrol.ChangeRequestIdentity{ConnectionScope: review.ConnectionScope, RepositoryID: review.RepositoryID, Number: review.ChangeRequestNumber})
	require.Equal(t, "open", review.State)
	require.Equal(t, &sourcecontrol.ReviewTaskStatus{
		Number: 42, State: "open", PipelineState: "failure",
		Checks: []sourcecontrol.ReviewTaskStatusCheck{
			{ID: "1", Label: "build", State: "success"},
			{ID: "2", Label: "test", State: "failure", Detail: "1 failed", URL: "https://ci.example/2"},
		},
		Review:    &sourcecontrol.ReviewTaskReview{State: "changes_requested", Approved: 1, Requested: 1},
		UpdatedAt: 1788256800000, // 2026-09-01T10:00:00Z
	}, review.TaskStatus)
	require.Equal(t, sourcecontrol.Person{Name: "Alice"}, review.Detail.Author)
	require.Equal(t, "task/42", review.Detail.SourceBranch)
	require.Len(t, review.Detail.Reviews, 3, "dismissed reviews are hidden, comments kept")
	require.Equal(t, "CHANGES_REQUESTED", review.Detail.Reviews[1].State)
	require.Equal(t, "COMMENTED", review.Detail.Reviews[2].State)
	require.Equal(t, []sourcecontrol.Person{{Name: "dave"}}, review.Detail.RequestedReviewers)

	require.NoError(t, h.adapter.Unlink(ctx, "task-1", identity))
	reviews, err = h.adapter.ForTask(ctx, "workspace-1", "task-1")
	require.NoError(t, err)
	require.Empty(t, reviews)
	associations, err = h.adapter.Associations(ctx, "workspace-1")
	require.NoError(t, err)
	require.Len(t, associations, 1)
}

func TestForTaskDegradesUnavailablePullRequests(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	require.NoError(t, h.adapter.Link(ctx, "task-1", sourcecontrol.ChangeRequestIdentity{ConnectionScope: h.scope, RepositoryID: "9", Number: 999}))

	reviews, err := h.adapter.ForTask(ctx, "workspace-1", "task-1")
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.Equal(t, "unknown", reviews[0].State)
	require.Equal(t, int64(999), reviews[0].ChangeRequestNumber, "identity stays intact so the link can be removed")
	require.Nil(t, reviews[0].TaskStatus)
}

func TestReferenceSearchAndLiveAuthorization(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	candidates, err := h.adapter.Search(ctx, "workspace-1", "hooks", 10)
	require.NoError(t, err)
	h.gitea.requested(t, "GET /gitea/api/v1/repos/issues/search?limit=10&q=hooks&state=all&type=pulls")
	require.Len(t, candidates, 1)
	require.Equal(t, "9/42", candidates[0].ProviderLocalID)
	require.Equal(t, "tools/widgets#42 Add hooks", candidates[0].Title)
	require.Equal(t, "9", candidates[0].Attributes["repository_id"])

	allowed, err := h.adapter.Authorize(ctx, "workspace-1", "submission", map[string]any{"id": "9/42"})
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, err = h.adapter.Authorize(ctx, "workspace-1", "submission", map[string]any{"id": "9/999"})
	require.NoError(t, err)
	require.False(t, allowed, "a vanished pull request fails closed")

	allowed, err = h.adapter.Authorize(ctx, "workspace-1", "submission", map[string]any{"id": "garbage"})
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestGitCredentialsRequireExactLeaseScope(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	valid := pluginsdk.ResolveGitCredentialRequest{
		ProviderID: "gitea", WorkspaceID: "workspace-1", TaskID: "task-1", SessionID: "session-1", RepositoryID: "repo-1",
		Host: h.hostPrt, Path: "/gitea/tools/widgets.git",
	}

	credential, err := h.adapter.ResolveGitCredential(ctx, &valid)
	require.NoError(t, err)
	require.Equal(t, &pluginsdk.ResolveGitCredentialResponse{Username: "bot", Secret: "secret-token"}, credential)

	binding, err := h.adapter.GetGitCredentialBinding(ctx, &pluginsdk.GitCredentialBindingRequest{
		ProviderID: "gitea", WorkspaceID: "workspace-1", TaskID: "task-1", SessionID: "session-1", RepositoryID: "repo-1",
		Host: h.hostPrt, Path: "/gitea/tools/widgets",
	})
	require.NoError(t, err)
	require.NotEmpty(t, binding.Binding)
	require.NotContains(t, binding.Binding, "secret-token")

	h.host.config["token"] = "rotated-token"
	rotated, err := h.adapter.GetGitCredentialBinding(ctx, &pluginsdk.GitCredentialBindingRequest{
		ProviderID: "gitea", WorkspaceID: "workspace-1", TaskID: "task-1", SessionID: "session-1", RepositoryID: "repo-1",
		Host: h.hostPrt, Path: "/gitea/tools/widgets.git",
	})
	require.NoError(t, err)
	require.NotEqual(t, binding.Binding, rotated.Binding, "rotation changes the binding")
	h.host.config["token"] = "secret-token"

	for name, mutate := range map[string]func(*pluginsdk.ResolveGitCredentialRequest){
		"other provider": func(r *pluginsdk.ResolveGitCredentialRequest) { r.ProviderID = "github" },
		"other host":     func(r *pluginsdk.ResolveGitCredentialRequest) { r.Host = "gitea.example.com" },
		"other path":     func(r *pluginsdk.ResolveGitCredentialRequest) { r.Path = "/gitea/tools/gadgets.git" },
		"unattached":     func(r *pluginsdk.ResolveGitCredentialRequest) { r.TaskID = "task-2" },
		"no session":     func(r *pluginsdk.ResolveGitCredentialRequest) { r.SessionID = "" },
	} {
		req := valid
		mutate(&req)
		_, err := h.adapter.ResolveGitCredential(ctx, &req)
		require.ErrorIs(t, err, errCredentialUnavailable, name)
		binding, err := h.adapter.GetGitCredentialBinding(ctx, &pluginsdk.GitCredentialBindingRequest{
			ProviderID: req.ProviderID, WorkspaceID: req.WorkspaceID, TaskID: req.TaskID, SessionID: req.SessionID,
			RepositoryID: req.RepositoryID, Host: req.Host, Path: req.Path,
		})
		require.NoError(t, err, name)
		require.Empty(t, binding.Binding, name)
	}
}

func TestCancelledContextStopsBeforeProviderIO(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.adapter.List(ctx, "workspace-1", "", sourcecontrol.RepositoryCursor{}, 10)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, h.gitea.requests)
}
