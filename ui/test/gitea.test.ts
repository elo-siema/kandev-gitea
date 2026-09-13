import { describe, expect, it } from "vitest";
import { parseReference, toChangeRequestDetail } from "../src/gitea";

describe("parseReference", () => {
  it("accepts owner/repo#number and pull request URLs", () => {
    expect(parseReference("  tools/widgets#42 ")).toBe("tools/widgets#42");
    expect(parseReference("https://gitea.example.com/tools/widgets/pulls/42")).toBe(
      "https://gitea.example.com/tools/widgets/pulls/42",
    );
    expect(parseReference("https://example.com/gitea/tools/widgets/pulls/7/")).toBe(
      "https://example.com/gitea/tools/widgets/pulls/7/",
    );
  });

  it("rejects incomplete references", () => {
    expect(parseReference("#42")).toBeNull();
    expect(parseReference("widgets#42")).toBeNull();
    expect(parseReference("https://gitea.example.com/tools/widgets")).toBeNull();
    expect(parseReference("")).toBeNull();
  });
});

describe("toChangeRequestDetail", () => {
  it("maps the snapshot and backend detail onto the host model", () => {
    const detail = toChangeRequestDetail(
      {
        providerId: "gitea",
        reviewKey: "tools/widgets#42",
        title: "Add hooks",
        url: "https://gitea.example.com/tools/widgets/pulls/42",
        connectionScope: "gitea.example.com",
        repositoryId: "9",
        changeRequestNumber: 42,
        state: "draft",
        taskStatus: {
          number: 42,
          state: "draft",
          pipelineState: "failure",
          checks: [{ id: "1", label: "build", state: "failure", detail: "boom", url: "https://ci" }],
          review: { state: "changes_requested", approved: 1, requested: 2 },
          updatedAt: 1_765_000_000_000,
        },
      },
      {
        author: { name: "Alice", url: "https://gitea.example.com/alice" },
        source_branch: "task/42",
        target_branch: "main",
        additions: 3,
        deletions: 1,
        description: "Body",
        created_at: "2026-09-01T00:00:00Z",
        reviews: [{ id: "5", author: { name: "Bob" }, state: "CHANGES_REQUESTED", body: "fix" }],
        requested_reviewers: [{ name: "Carol" }],
      },
    );

    expect(detail).toEqual({
      providerId: "gitea",
      reviewKey: "tools/widgets#42",
      number: 42,
      title: "Add hooks",
      url: "https://gitea.example.com/tools/widgets/pulls/42",
      state: "open",
      draft: true,
      author: { name: "Alice", url: "https://gitea.example.com/alice" },
      createdAt: "2026-09-01T00:00:00Z",
      sourceBranch: "task/42",
      targetBranch: "main",
      additions: 3,
      deletions: 1,
      description: "Body",
      reviewState: "changes_requested",
      pendingReviewCount: 2,
      reviews: [{ id: "5", author: { name: "Bob" }, state: "CHANGES_REQUESTED", body: "fix" }],
      requestedReviewers: [{ name: "Carol" }],
      checks: [{ id: "1", name: "build", state: "failure", url: "https://ci", output: "boom" }],
      comments: [],
      lastSyncedAt: new Date(1_765_000_000_000).toISOString(),
    });
  });

  it("renders a usable model without detail", () => {
    const detail = toChangeRequestDetail(
      {
        providerId: "gitea",
        reviewKey: "k",
        title: "t",
        url: "u",
        connectionScope: "s",
        repositoryId: "9",
        changeRequestNumber: 1,
        state: "open",
      },
      undefined,
    );
    expect(detail).toMatchObject({
      state: "open",
      author: { name: "unknown" },
      sourceBranch: "source",
      targetBranch: "target",
      additions: 0,
      reviews: [],
      checks: [],
    });
    expect(detail).not.toHaveProperty("draft");
  });
});
