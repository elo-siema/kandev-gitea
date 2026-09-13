import type { ReviewSummary } from "@kandev/plugin-sdk";

export const PROVIDER_ID = "gitea";

const REFERENCE = /^(?:https?:\/\/\S+\/pulls\/\d+\/?|[^\s/#]+\/[^\s/#]+#\d+)$/;

/** Accepts owner/repo#42 or a Gitea pull request URL; the backend resolves it. */
export function parseReference(reference: string): string | null {
  const value = reference.trim();
  return REFERENCE.test(value) ? value : null;
}

const record = (value: unknown): Record<string, unknown> =>
  value !== null && typeof value === "object" ? (value as Record<string, unknown>) : {};
const text = (value: unknown): string | undefined =>
  typeof value === "string" && value ? value : undefined;
const list = (value: unknown): unknown[] => (Array.isArray(value) ? value : []);
const optional = <Key extends string>(key: Key, value: string | undefined) =>
  value ? ({ [key]: value } as Record<Key, string>) : {};

function person(value: unknown) {
  const source = record(value);
  return {
    name: text(source.name) ?? "unknown",
    ...optional("url", text(source.url)),
    ...optional("avatarUrl", text(source.avatar_url)),
  };
}

/** Shapes a review snapshot plus the backend `detail` payload for host.ui.ChangeRequestDetail. */
export function toChangeRequestDetail(review: ReviewSummary, detail: unknown) {
  const source = record(detail);
  const status = review.taskStatus;
  return {
    providerId: PROVIDER_ID,
    reviewKey: review.reviewKey,
    number: review.changeRequestNumber,
    title: review.title,
    url: review.url,
    state: review.state === "draft" ? "open" : review.state,
    ...(review.state === "draft" ? { draft: true } : {}),
    author: person(source.author),
    ...optional("createdAt", text(source.created_at)),
    ...optional("mergedAt", text(source.merged_at)),
    ...optional("closedAt", text(source.closed_at)),
    sourceBranch: text(source.source_branch) ?? "source",
    targetBranch: text(source.target_branch) ?? "target",
    additions: typeof source.additions === "number" ? source.additions : 0,
    deletions: typeof source.deletions === "number" ? source.deletions : 0,
    ...optional("description", text(source.description)),
    ...(status?.review ? { reviewState: status.review.state } : {}),
    ...(status?.review?.requested ? { pendingReviewCount: status.review.requested } : {}),
    reviews: list(source.reviews).map((entry) => {
      const item = record(entry);
      return {
        id: text(item.id) ?? "",
        author: person(item.author),
        state: text(item.state) ?? "COMMENTED",
        ...optional("body", text(item.body)),
        ...optional("createdAt", text(item.created_at)),
      };
    }),
    requestedReviewers: list(source.requested_reviewers).map(person),
    checks: (status?.checks ?? []).map((check) => ({
      id: check.id,
      name: check.label,
      state: check.state,
      ...optional("url", check.url),
      ...optional("output", check.detail),
    })),
    comments: [],
    ...(status?.updatedAt ? { lastSyncedAt: new Date(status.updatedAt).toISOString() } : {}),
  };
}
