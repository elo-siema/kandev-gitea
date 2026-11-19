// Package gitea adapts one operator-configured Gitea instance to the
// provider-neutral source-control recipe and Kandev's Git credential broker.
package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/kdlbs/kandev/pluginsdk"
)

// pageSize is Gitea's default [api] MAX_RESPONSE_ITEMS: a larger limit is
// silently clamped, which would make a full page look like the last one.
const pageSize = 50

// Client is a minimal Gitea REST v1 client. base is the instance root
// (https://gitea.example.com or https://example.com/gitea, no trailing slash).
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

func NewClient(base *url.URL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{base: base, token: token, http: httpClient}
}

type User struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	FullName  string `json:"full_name"`
	HTMLURL   string `json:"html_url"`
	AvatarURL string `json:"avatar_url"`
}

type Repository struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Owner         User   `json:"owner"`
	HTMLURL       string `json:"html_url"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
}

// OwnerName returns the owner login, falling back to full_name.
func (r Repository) OwnerName() string {
	if r.Owner.Login != "" {
		return r.Owner.Login
	}
	owner, _, _ := strings.Cut(r.FullName, "/")
	return owner
}

type Branch struct {
	Name   string `json:"name"`
	Commit struct {
		ID string `json:"id"`
	} `json:"commit"`
}

type PRBranch struct {
	Ref    string `json:"ref"`
	Sha    string `json:"sha"`
	RepoID int64  `json:"repo_id"`
}

type PullRequest struct {
	Number             int64    `json:"number"`
	Title              string   `json:"title"`
	Body               string   `json:"body"`
	HTMLURL            string   `json:"html_url"`
	State              string   `json:"state"`
	Draft              bool     `json:"draft"`
	Merged             bool     `json:"merged"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
	MergedAt           *string  `json:"merged_at"`
	ClosedAt           *string  `json:"closed_at"`
	User               User     `json:"user"`
	Head               PRBranch `json:"head"`
	Base               PRBranch `json:"base"`
	RequestedReviewers []User   `json:"requested_reviewers"`
	Additions          int      `json:"additions"`
	Deletions          int      `json:"deletions"`
}

type CreatePullRequest struct {
	Head  string `json:"head"`
	Base  string `json:"base"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

type Review struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	Body        string `json:"body"`
	User        User   `json:"user"`
	Dismissed   bool   `json:"dismissed"`
	SubmittedAt string `json:"submitted_at"`
}

type CommitStatus struct {
	ID          int64  `json:"id"`
	Context     string `json:"context"`
	Status      string `json:"status"`
	Description string `json:"description"`
	TargetURL   string `json:"target_url"`
}

type CombinedStatus struct {
	State    string         `json:"state"`
	Statuses []CommitStatus `json:"statuses"`
}

type Issue struct {
	Number     int64  `json:"number"`
	Title      string `json:"title"`
	HTMLURL    string `json:"html_url"`
	State      string `json:"state"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var u User
	return u, c.get(ctx, "user", nil, &u)
}

// Repositories lists repositories visible to the token: the user's own
// repositories when query is empty, otherwise a server-side search. The
// boolean reports whether another page may exist.
func (c *Client) Repositories(ctx context.Context, query string, page, limit int) ([]Repository, bool, error) {
	limit = min(limit, pageSize)
	q := url.Values{"page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(limit)}}
	var repos []Repository
	if query == "" {
		if err := c.get(ctx, "user/repos", q, &repos); err != nil {
			return nil, false, err
		}
	} else {
		q.Set("q", query)
		q.Set("sort", "updated")
		q.Set("order", "desc")
		var result struct {
			Data []Repository `json:"data"`
		}
		if err := c.get(ctx, "repos/search", q, &result); err != nil {
			return nil, false, err
		}
		repos = result.Data
	}
	return repos, len(repos) >= limit, nil
}

func (c *Client) Repository(ctx context.Context, owner, name string) (Repository, error) {
	var r Repository
	return r, c.get(ctx, repoPath(owner, name), nil, &r)
}

func (c *Client) RepositoryByID(ctx context.Context, id int64) (Repository, error) {
	var r Repository
	return r, c.get(ctx, "repositories/"+strconv.FormatInt(id, 10), nil, &r)
}

func (c *Client) Branches(ctx context.Context, owner, name string) ([]Branch, error) {
	var all []Branch
	for page := 1; page <= 20; page++ {
		var branches []Branch
		q := url.Values{"page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(pageSize)}}
		if err := c.get(ctx, repoPath(owner, name)+"/branches", q, &branches); err != nil {
			return nil, err
		}
		all = append(all, branches...)
		if len(branches) < pageSize {
			break
		}
	}
	return all, nil
}

func (c *Client) PullRequest(ctx context.Context, owner, name string, number int64) (PullRequest, error) {
	var pr PullRequest
	return pr, c.get(ctx, fmt.Sprintf("%s/pulls/%d", repoPath(owner, name), number), nil, &pr)
}

func (c *Client) CreatePullRequest(ctx context.Context, owner, name string, in CreatePullRequest) (PullRequest, error) {
	var pr PullRequest
	return pr, c.do(ctx, http.MethodPost, repoPath(owner, name)+"/pulls", nil, in, &pr)
}

func (c *Client) Reviews(ctx context.Context, owner, name string, number int64) ([]Review, error) {
	var reviews []Review
	q := url.Values{"limit": {strconv.Itoa(pageSize)}}
	return reviews, c.get(ctx, fmt.Sprintf("%s/pulls/%d/reviews", repoPath(owner, name), number), q, &reviews)
}

func (c *Client) CombinedStatus(ctx context.Context, owner, name, sha string) (CombinedStatus, error) {
	var s CombinedStatus
	q := url.Values{"limit": {strconv.Itoa(pageSize)}}
	return s, c.get(ctx, fmt.Sprintf("%s/commits/%s/status", repoPath(owner, name), url.PathEscape(sha)), q, &s)
}

func (c *Client) SearchPullRequests(ctx context.Context, query string, limit int) ([]Issue, error) {
	var issues []Issue
	q := url.Values{"type": {"pulls"}, "state": {"all"}, "q": {query}, "limit": {strconv.Itoa(limit)}}
	return issues, c.get(ctx, "repos/issues/search", q, &issues)
}

func repoPath(owner, name string) string {
	return "repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	endpoint := *c.base
	endpoint.Path = strings.TrimSuffix(c.base.Path, "/") + "/api/v1/" + path
	endpoint.RawQuery = query.Encode()
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return pluginsdk.CategorizeActionError(pluginsdk.ActionErrorUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return statusError(resp.StatusCode, method, path)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func statusError(status int, method, path string) error {
	err := fmt.Errorf("gitea: %s %s returned HTTP %d", method, path, status)
	var code pluginsdk.ActionErrorCode
	switch {
	case status == http.StatusUnauthorized:
		code = pluginsdk.ActionErrorUnauthenticated
	case status == http.StatusForbidden:
		code = pluginsdk.ActionErrorPermissionDenied
	case status == http.StatusNotFound:
		code = pluginsdk.ActionErrorNotFound
	case status == http.StatusConflict:
		code = pluginsdk.ActionErrorConflict
	case status == http.StatusUnprocessableEntity:
		code = pluginsdk.ActionErrorInvalidArgument
	case status == http.StatusTooManyRequests:
		code = pluginsdk.ActionErrorRateLimited
	case status >= 500:
		code = pluginsdk.ActionErrorUnavailable
	default:
		code = pluginsdk.ActionErrorUpstream
	}
	return pluginsdk.CategorizeActionError(code, err)
}

// IsNotFound reports a 404 or 403; Gitea hides inaccessible private
// repositories behind 404, so both mean "not available to this token".
func IsNotFound(err error) bool {
	var actionErr *pluginsdk.ActionError
	return errors.As(err, &actionErr) &&
		(actionErr.Code == pluginsdk.ActionErrorNotFound || actionErr.Code == pluginsdk.ActionErrorPermissionDenied)
}
