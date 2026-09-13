// ui/src/gitea.ts
var PROVIDER_ID = "gitea";
var REFERENCE = /^(?:https?:\/\/\S+\/pulls\/\d+\/?|[^\s/#]+\/[^\s/#]+#\d+)$/;
function parseReference(reference) {
  const value = reference.trim();
  return REFERENCE.test(value) ? value : null;
}
var record = (value) => value !== null && typeof value === "object" ? value : {};
var text = (value) => typeof value === "string" && value ? value : void 0;
var list = (value) => Array.isArray(value) ? value : [];
var optional = (key, value) => value ? { [key]: value } : {};
function person(value) {
  const source = record(value);
  return {
    name: text(source.name) ?? "unknown",
    ...optional("url", text(source.url)),
    ...optional("avatarUrl", text(source.avatar_url))
  };
}
function toChangeRequestDetail(review, detail) {
  const source = record(detail);
  const status = review.taskStatus;
  return {
    providerId: PROVIDER_ID,
    reviewKey: review.reviewKey,
    number: review.changeRequestNumber,
    title: review.title,
    url: review.url,
    state: review.state === "draft" ? "open" : review.state,
    ...review.state === "draft" ? { draft: true } : {},
    author: person(source.author),
    ...optional("createdAt", text(source.created_at)),
    ...optional("mergedAt", text(source.merged_at)),
    ...optional("closedAt", text(source.closed_at)),
    sourceBranch: text(source.source_branch) ?? "source",
    targetBranch: text(source.target_branch) ?? "target",
    additions: typeof source.additions === "number" ? source.additions : 0,
    deletions: typeof source.deletions === "number" ? source.deletions : 0,
    ...optional("description", text(source.description)),
    ...status?.review ? { reviewState: status.review.state } : {},
    ...status?.review?.requested ? { pendingReviewCount: status.review.requested } : {},
    reviews: list(source.reviews).map((entry) => {
      const item = record(entry);
      return {
        id: text(item.id) ?? "",
        author: person(item.author),
        state: text(item.state) ?? "COMMENTED",
        ...optional("body", text(item.body)),
        ...optional("createdAt", text(item.created_at))
      };
    }),
    requestedReviewers: list(source.requested_reviewers).map(person),
    checks: (status?.checks ?? []).map((check) => ({
      id: check.id,
      name: check.label,
      state: check.state,
      ...optional("url", check.url),
      ...optional("output", check.detail)
    })),
    comments: [],
    ...status?.updatedAt ? { lastSyncedAt: new Date(status.updatedAt).toISOString() } : {}
  };
}

// ui/src/icon.ts
function createGiteaIcon(host) {
  return function GiteaIcon({ className, "aria-hidden": ariaHidden }) {
    return host.jsx(
      "svg",
      {
        xmlns: "http://www.w3.org/2000/svg",
        width: 24,
        height: 24,
        viewBox: "0 0 24 24",
        fill: "none",
        stroke: "currentColor",
        strokeWidth: 2,
        strokeLinecap: "round",
        strokeLinejoin: "round",
        className,
        "aria-hidden": ariaHidden ?? true
      },
      host.jsx("path", { d: "M4 10h12v5a5 5 0 0 1-5 5h-2a5 5 0 0 1-5-5z" }),
      host.jsx("path", { d: "M16 12h1.5a2.5 2.5 0 0 1 0 5H16" }),
      host.jsx("path", { d: "M10 10V6" }),
      host.jsx("path", { d: "M8 3h4v3H8z" })
    );
  };
}

// ui/src/review-store.ts
function createSnapshotStore() {
  const snapshots = /* @__PURE__ */ new Map();
  const listeners = /* @__PURE__ */ new Map();
  const versions = /* @__PURE__ */ new Map();
  let epoch = 0;
  return {
    get: (key) => snapshots.get(key) ?? [],
    subscribe(key, listener) {
      const keyListeners = listeners.get(key) ?? /* @__PURE__ */ new Set();
      keyListeners.add(listener);
      listeners.set(key, keyListeners);
      return () => {
        keyListeners.delete(listener);
        if (keyListeners.size === 0) listeners.delete(key);
      };
    },
    beginRefresh(key) {
      const version = (versions.get(key) ?? 0) + 1;
      versions.set(key, version);
      return { epoch, version };
    },
    commit(key, token, values) {
      if (token.epoch !== epoch || versions.get(key) !== token.version) return false;
      snapshots.set(key, [...values]);
      listeners.get(key)?.forEach((listener) => listener());
      return true;
    },
    clear() {
      epoch += 1;
      versions.clear();
      snapshots.clear();
      listeners.forEach((keyListeners) => keyListeners.forEach((listener) => listener()));
      listeners.clear();
    }
  };
}

// ui/src/register.ts
function record2(value) {
  return value !== null && typeof value === "object" ? value : {};
}
function text2(value) {
  return typeof value === "string" ? value.trim() : "";
}
function finiteNumber(value) {
  return typeof value === "number" && Number.isFinite(value) ? value : void 0;
}
function nonNegativeInteger(value) {
  const number = finiteNumber(value);
  return number !== void 0 && Number.isInteger(number) && number >= 0 ? number : void 0;
}
function positiveInteger(value) {
  const number = nonNegativeInteger(value);
  return number !== void 0 && number > 0 ? number : void 0;
}
function normalizeTaskStatus(value) {
  const source = record2(value);
  const number = positiveInteger(source.number);
  const state = text2(source.state);
  const pipelineState = text2(source.pipeline_state);
  if (number === void 0 || !["open", "merged", "closed", "draft"].includes(
    state
  ) || !["success", "failure", "pending", "neutral"].includes(
    pipelineState
  )) {
    return void 0;
  }
  const checks = Array.isArray(source.checks) ? source.checks.flatMap((value2) => {
    const check = record2(value2);
    const id = text2(check.id);
    const label = text2(check.label);
    const checkState = text2(check.state);
    if (!id || !label || !["success", "failure", "pending", "neutral"].includes(
      checkState
    )) {
      return [];
    }
    const detail = text2(check.detail);
    const url = text2(check.url);
    return [{
      id,
      label,
      state: checkState,
      ...detail ? { detail } : {},
      ...url ? { url } : {}
    }];
  }) : [];
  const reviewSource = record2(source.review);
  const reviewState = text2(reviewSource.state);
  const approved = nonNegativeInteger(reviewSource.approved);
  const required = nonNegativeInteger(reviewSource.required);
  const requested = nonNegativeInteger(reviewSource.requested);
  const review = approved !== void 0 && ["approved", "changes_requested", "pending"].includes(
    reviewState
  ) ? {
    state: reviewState,
    approved,
    ...required === void 0 ? {} : { required },
    ...requested === void 0 ? {} : { requested }
  } : void 0;
  const unresolvedComments = nonNegativeInteger(source.unresolved_comments);
  const updatedAt = finiteNumber(source.updated_at);
  return {
    number,
    state,
    pipelineState,
    checks,
    ...review ? { review } : {},
    ...unresolvedComments === void 0 ? {} : { unresolvedComments },
    ...updatedAt === void 0 ? {} : { updatedAt }
  };
}
function normalizeReview(providerId, value) {
  const source = record2(value);
  const reviewKey = text2(source.review_key);
  const title = text2(source.title);
  const url = text2(source.url);
  const connectionScope = text2(source.connection_scope);
  const repositoryId = text2(source.repository_id);
  const changeRequestNumber = positiveInteger(source.change_request_number);
  if (!reviewKey || !title || !url || !connectionScope || !repositoryId || changeRequestNumber === void 0) {
    return null;
  }
  const state = text2(source.state);
  const taskStatus = normalizeTaskStatus(source.task_status);
  return {
    providerId,
    reviewKey,
    title,
    url,
    connectionScope,
    repositoryId,
    changeRequestNumber,
    state,
    ...taskStatus ? { taskStatus } : {}
  };
}
function normalizeAssociation(providerId, value) {
  const source = record2(value);
  const taskId = text2(source.task_id);
  const reviewKey = text2(source.review_key);
  const connectionScope = text2(source.connection_scope);
  const repositoryId = text2(source.repository_id);
  const changeRequestNumber = positiveInteger(source.change_request_number);
  if (!taskId || !reviewKey || !connectionScope || !repositoryId || changeRequestNumber === void 0) {
    return null;
  }
  return {
    providerId,
    taskId,
    reviewKey,
    connectionScope,
    repositoryId,
    changeRequestNumber
  };
}
function normalizeRepository(providerId, value) {
  const source = record2(value);
  const providerHost = text2(source.provider_host);
  const ownerOrProject = text2(source.owner_or_project);
  const repositoryId = text2(source.repository_id);
  const repositoryName = text2(source.name);
  const cloneUrl = text2(source.clone_url);
  if (!providerHost || !ownerOrProject || !repositoryId || !repositoryName || !cloneUrl) return null;
  const providerScope = text2(source.provider_scope);
  const defaultBranch = text2(source.default_branch);
  return {
    providerId,
    providerHost,
    ...providerScope ? { providerScope } : {},
    ownerOrProject,
    repositoryId,
    repositoryName,
    cloneUrl,
    ...defaultBranch ? { defaultBranch } : {}
  };
}
function detailKey(review) {
  return `${review.connectionScope}\0${review.repositoryId}\0${review.changeRequestNumber}`;
}
function credentialFreeRepository(repository) {
  return {
    provider_id: repository.providerId,
    provider_host: repository.providerHost,
    provider_scope: repository.providerScope ?? "",
    provider_repository_id: repository.repositoryId,
    owner_or_project: repository.ownerOrProject,
    name: repository.repositoryName,
    clone_url: repository.cloneUrl,
    default_branch: repository.defaultBranch ?? ""
  };
}
function registerSourceControlRecipe(registry, host, options) {
  const reviewStore = createSnapshotStore();
  const associationStore = createSnapshotStore();
  const details = /* @__PURE__ */ new Map();
  const overlays = /* @__PURE__ */ new Set();
  async function refreshReviews(taskId, signal, workspaceId) {
    const token = reviewStore.beginRefresh(taskId);
    signal.throwIfAborted();
    const response = await host.api.invokeAction(
      "change_requests.get",
      { ...workspaceId ? { workspaceId } : {}, taskId },
      { signal }
    );
    signal.throwIfAborted();
    reviewStore.commit(
      taskId,
      token,
      (response.reviews ?? []).flatMap((review) => {
        const normalized = normalizeReview(options.providerId, review);
        if (normalized) details.set(detailKey(normalized), record2(review).detail);
        return normalized ? [normalized] : [];
      })
    );
  }
  async function refreshAssociations(workspaceId, signal) {
    const token = associationStore.beginRefresh(workspaceId);
    signal.throwIfAborted();
    const response = await host.api.invokeAction(
      "change_requests.associations",
      { workspaceId },
      { signal }
    );
    signal.throwIfAborted();
    associationStore.commit(
      workspaceId,
      token,
      (response.associations ?? []).flatMap((association) => {
        const normalized = normalizeAssociation(options.providerId, association);
        return normalized ? [normalized] : [];
      })
    );
  }
  async function refreshAfterMutation(workspaceId, taskId, signal) {
    try {
      await Promise.all([
        refreshReviews(taskId, signal, workspaceId),
        refreshAssociations(workspaceId, signal)
      ]);
    } catch {
    }
  }
  const repositoryProvider = {
    id: options.providerId,
    label: options.label,
    ...options.icon ? { icon: options.icon } : {},
    ...options.matchesURL ? { matchesURL: options.matchesURL } : {},
    ...options.supportsDraft === void 0 ? {} : { supportsDraft: options.supportsDraft },
    async listRepositories({ workspaceId, query = "", cursor = "", limit = 100, signal }) {
      signal.throwIfAborted();
      const response = await host.api.invokeAction(
        "repositories.list",
        { workspaceId, body: { query, cursor, limit } },
        { signal }
      );
      signal.throwIfAborted();
      return {
        repositories: (response.repositories ?? []).flatMap((repository) => {
          const normalized = normalizeRepository(options.providerId, repository);
          return normalized ? [normalized] : [];
        }),
        nextCursor: text2(response.next_cursor) || void 0
      };
    },
    async listBranches({ workspaceId, repository, signal }) {
      signal.throwIfAborted();
      const response = await host.api.invokeAction(
        "repositories.branches",
        { workspaceId, body: { repository: credentialFreeRepository(repository) } },
        { signal }
      );
      signal.throwIfAborted();
      return (response.branches ?? []).flatMap((branch) => {
        const name = text2(record2(branch).name);
        return name ? [{ name }] : [];
      });
    },
    async inspectURL({ workspaceId, url, signal }) {
      signal.throwIfAborted();
      const response = await host.api.invokeAction(
        "repositories.inspect",
        { workspaceId, body: { url } },
        { signal }
      );
      signal.throwIfAborted();
      return normalizeRepository(options.providerId, response.repository);
    },
    async createChangeRequest({
      workspaceId,
      taskId,
      sessionId,
      repositoryId,
      title,
      body,
      baseBranch,
      draft,
      signal
    }) {
      signal.throwIfAborted();
      const response = await host.api.invokeAction(
        "change_requests.create",
        {
          workspaceId,
          taskId,
          sessionId,
          repositoryId,
          body: {
            title,
            description: body,
            destination: baseBranch ?? "",
            draft
          }
        },
        { signal }
      );
      const url = text2(response.url);
      if (!url) throw new Error("source-control recipe: create response did not include a URL");
      await refreshAfterMutation(workspaceId, taskId, signal);
      const output = text2(response.output);
      const associationError = text2(response.association_error);
      return {
        url,
        provider: options.providerId,
        ...output ? { output } : {},
        ...typeof response.linked === "boolean" ? { linked: response.linked } : {},
        ...associationError ? { associationError } : {}
      };
    }
  };
  registry.registerRepositoryProvider(repositoryProvider);
  registry.registerTaskAction({
    id: `${options.providerId}-link-change-request`,
    label: `${options.label} ${options.changeRequestNoun}`,
    ...options.icon ? { icon: options.icon } : {},
    placement: "link",
    singleTaskOnly: true,
    async run(context) {
      const dialog = host.openTaskLinkDialog({
        title: `Link ${options.label} ${options.changeRequestNoun}`,
        description: `Enter a ${options.label} ${options.changeRequestNoun} URL or canonical reference.`,
        inputLabel: options.changeRequestNoun,
        emptyError: `Enter a valid ${options.label} ${options.changeRequestNoun} reference.`,
        failureMessage: `Failed to link ${options.label} ${options.changeRequestNoun}.`,
        successMessage: `${options.label} ${options.changeRequestNoun} linked`,
        inputTestId: `${options.providerId}-review-reference`,
        errorTestId: `${options.providerId}-review-reference-error`,
        submitTestId: `${options.providerId}-review-reference-submit`,
        async onSubmit(reference, signal) {
          signal.throwIfAborted();
          const parsed = options.parseReference(reference);
          if (!parsed) {
            throw new Error(`Enter a valid ${options.label} ${options.changeRequestNoun} reference.`);
          }
          await host.api.invokeAction(
            "change_requests.link",
            {
              workspaceId: context.workspaceId,
              taskId: context.taskId,
              body: { reference: parsed }
            },
            { signal }
          );
          await refreshAfterMutation(context.workspaceId, context.taskId, signal);
        }
      });
      overlays.add(dialog);
    }
  });
  registry.registerReviewProvider({
    id: options.providerId,
    label: options.label,
    ...options.icon ? { icon: options.icon } : {},
    changeRequestNoun: options.changeRequestNoun,
    order: options.order ?? 100,
    getSnapshot: (taskId) => reviewStore.get(taskId),
    subscribe: (taskId, listener) => reviewStore.subscribe(taskId, listener),
    refresh: (taskId, signal) => refreshReviews(taskId, signal),
    getAssociationSnapshot: (workspaceId) => associationStore.get(workspaceId),
    subscribeAssociations: (workspaceId, listener) => associationStore.subscribe(workspaceId, listener),
    refreshAssociations,
    async unlink({
      workspaceId,
      taskId,
      connectionScope,
      repositoryId,
      changeRequestNumber,
      signal
    }) {
      const number = typeof changeRequestNumber === "string" && /^\d+$/.test(changeRequestNumber) ? positiveInteger(Number(changeRequestNumber)) : positiveInteger(changeRequestNumber);
      if (!connectionScope.trim() || !repositoryId.trim() || number === void 0) {
        throw new Error("source-control recipe: cannot unlink an incomplete review identity");
      }
      signal.throwIfAborted();
      await host.api.invokeAction(
        "change_requests.unlink",
        {
          workspaceId,
          taskId,
          body: {
            connection_scope: connectionScope,
            repository_id: repositoryId,
            number
          }
        },
        { signal }
      );
    },
    ReviewPanel: (props) => {
      const review = reviewStore.get(props.taskId).find(
        (candidate) => candidate.reviewKey === props.reviewKey && candidate.connectionScope === props.connectionScope && candidate.repositoryId === props.repositoryId && String(candidate.changeRequestNumber) === String(props.changeRequestNumber)
      );
      return host.jsx(host.ui.ChangeRequestDetail, {
        detail: review ? options.toChangeRequestDetail(review, details.get(detailKey(review))) : null,
        presentation: props.presentation,
        loading: false,
        error: null
      });
    }
  });
  return {
    destroy() {
      overlays.forEach((overlay) => overlay.close());
      overlays.clear();
      reviewStore.clear();
      associationStore.clear();
      details.clear();
    }
  };
}

// ui/src/bundle.ts
var PLUGIN_ID = "kandev-plugin-gitea";
var lifecycle;
window.registerKandevPlugin(PLUGIN_ID, {
  initialize(registry, host) {
    lifecycle = registerSourceControlRecipe(registry, host, {
      providerId: PROVIDER_ID,
      label: "Gitea",
      icon: createGiteaIcon(host),
      changeRequestNoun: "pull request",
      order: 40,
      supportsDraft: true,
      parseReference,
      toChangeRequestDetail
    });
  },
  destroy() {
    lifecycle?.destroy();
    lifecycle = void 0;
  }
});
