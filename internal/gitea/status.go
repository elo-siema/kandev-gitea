package gitea

import (
	"strconv"
	"time"

	"kandev-plugin-gitea/internal/sourcecontrol"
)

func prState(pr PullRequest) string {
	switch {
	case pr.Merged:
		return "merged"
	case pr.State == "closed":
		return "closed"
	case pr.Draft:
		return "draft"
	default:
		return "open"
	}
}

// checkState maps a Gitea commit-status state to Kandev's semantic vocabulary.
func checkState(state string) string {
	switch state {
	case "success":
		return "success"
	case "pending":
		return "pending"
	case "failure", "error":
		return "failure"
	default:
		return "neutral"
	}
}

func taskStatus(pr PullRequest, status CombinedStatus, reviews []Review) *sourcecontrol.ReviewTaskStatus {
	ts := &sourcecontrol.ReviewTaskStatus{
		Number: pr.Number, State: prState(pr), PipelineState: "neutral",
		Checks: []sourcecontrol.ReviewTaskStatusCheck{},
	}
	if len(status.Statuses) > 0 {
		ts.PipelineState = checkState(status.State)
	}
	for _, s := range status.Statuses {
		ts.Checks = append(ts.Checks, sourcecontrol.ReviewTaskStatusCheck{
			ID: strconv.FormatInt(s.ID, 10), Label: s.Context, State: checkState(s.Status),
			Detail: s.Description, URL: s.TargetURL,
		})
	}
	approved, changes := reviewVerdicts(reviews)
	if requested := len(pr.RequestedReviewers); approved+changes+requested > 0 {
		state := "pending"
		if changes > 0 {
			state = "changes_requested"
		} else if approved > 0 {
			state = "approved"
		}
		ts.Review = &sourcecontrol.ReviewTaskReview{State: state, Approved: approved, Requested: requested}
	}
	if t, err := time.Parse(time.RFC3339, pr.UpdatedAt); err == nil {
		ts.UpdatedAt = t.UnixMilli()
	}
	return ts
}

// reviewVerdicts keeps each reviewer's latest non-dismissed verdict; the
// Gitea API returns reviews oldest first.
func reviewVerdicts(reviews []Review) (approved, changes int) {
	latest := map[string]string{}
	for _, r := range reviews {
		if r.Dismissed {
			continue
		}
		if r.State == "APPROVED" || r.State == "REQUEST_CHANGES" {
			latest[r.User.Login] = r.State
		}
	}
	for _, state := range latest {
		if state == "APPROVED" {
			approved++
		} else {
			changes++
		}
	}
	return approved, changes
}

func detail(pr PullRequest, reviews []Review) *sourcecontrol.ReviewDetail {
	d := &sourcecontrol.ReviewDetail{
		Author: person(pr.User), Description: pr.Body,
		SourceBranch: pr.Head.Ref, TargetBranch: pr.Base.Ref,
		Additions: pr.Additions, Deletions: pr.Deletions,
		CreatedAt: pr.CreatedAt, MergedAt: deref(pr.MergedAt), ClosedAt: deref(pr.ClosedAt),
		Reviews: []sourcecontrol.ReviewDetailReview{}, RequestedReviewers: []sourcecontrol.Person{},
	}
	states := map[string]string{"APPROVED": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED", "COMMENT": "COMMENTED"}
	for _, r := range reviews {
		state := states[r.State]
		if r.Dismissed || state == "" {
			continue
		}
		d.Reviews = append(d.Reviews, sourcecontrol.ReviewDetailReview{
			ID: strconv.FormatInt(r.ID, 10), Author: person(r.User), State: state, Body: r.Body, CreatedAt: r.SubmittedAt,
		})
	}
	for _, u := range pr.RequestedReviewers {
		d.RequestedReviewers = append(d.RequestedReviewers, person(u))
	}
	return d
}

func person(u User) sourcecontrol.Person {
	name := u.FullName
	if name == "" {
		name = u.Login
	}
	return sourcecontrol.Person{Name: name, URL: u.HTMLURL, AvatarURL: u.AvatarURL}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
