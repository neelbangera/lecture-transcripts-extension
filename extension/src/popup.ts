import type { BackgroundResponse, ExtensionMessage } from "./background";
import {
  statusLabel,
  type ExtensionSnapshot,
  type JobSummary,
  type OverflowNotice,
  type QueueStatus,
  type UploaderStatus,
} from "./status";

interface RuntimeMessageApi {
  sendMessage(message: ExtensionMessage, callback: (response: BackgroundResponse) => void): void;
}

const chromeApi = (globalThis as { chrome?: { runtime?: RuntimeMessageApi } }).chrome;

function element<T extends HTMLElement>(id: string): T {
  const value = document.getElementById(id);
  if (!value) throw new Error(`missing popup element: ${id}`);
  return value as T;
}

const connectionSummary = element<HTMLParagraphElement>("connection-summary");
const drainState = element<HTMLSpanElement>("drain-state");
const authorization = element<HTMLElement>("authorization");
const authorizationCopy = element<HTMLParagraphElement>("authorization-copy");
const authorizationLink = element<HTMLAnchorElement>("authorization-link");
const connectButton = element<HTMLButtonElement>("connect-button");
const resetButton = element<HTMLButtonElement>("reset-button");
const refreshButton = element<HTMLButtonElement>("refresh-button");
const lastOutcome = element<HTMLParagraphElement>("last-outcome");
const pendingCount = element<HTMLSpanElement>("pending-count");
const pendingCopy = element<HTMLParagraphElement>("pending-copy");
const overflowSection = element<HTMLElement>("overflow-section");
const overflowCount = element<HTMLSpanElement>("overflow-count");
const overflowList = element<HTMLUListElement>("overflow-list");
const queueCount = element<HTMLSpanElement>("queue-count");
const jobList = element<HTMLUListElement>("job-list");
const loadMoreButton = element<HTMLButtonElement>("load-more-button");
const versions = element<HTMLSpanElement>("versions");

let loadedJobs: JobSummary[] = [];
let nextBeforeJobId: number | null = null;
let loading = false;

function send(message: ExtensionMessage): Promise<BackgroundResponse> {
  return new Promise((resolve) => {
    if (!chromeApi?.runtime?.sendMessage) {
      resolve({ ok: false, errorCategory: "host_unavailable", message: "Extension runtime is unavailable" });
      return;
    }
    chromeApi.runtime.sendMessage(message, (response) => {
      resolve(response ?? { ok: false, errorCategory: "internal", message: "No response from extension" });
    });
  });
}

function setBusy(value: boolean): void {
  loading = value;
  connectButton.disabled = value;
  resetButton.disabled = value;
  refreshButton.disabled = value;
  loadMoreButton.disabled = value;
}

function isSafeGitHubUrl(value: string | null): value is string {
  if (!value) return false;
  try {
    const url = new URL(value);
    return url.protocol === "https:" && (url.hostname === "github.com" || url.hostname === "www.github.com");
  } catch {
    return false;
  }
}

function renderAuthorization(status: UploaderStatus | null): void {
  const auth = status?.authorization;
  if (!status || status.authState !== "authorizing" || !auth) {
    authorization.classList.add("hidden");
    authorizationCopy.textContent = "";
    authorizationLink.classList.add("hidden");
    authorizationLink.removeAttribute("href");
    return;
  }

  authorization.classList.remove("hidden");
  authorizationCopy.textContent = auth.userCode
    ? `Enter code ${auth.userCode} on GitHub. The local uploader is polling; this popup never receives a credential.`
    : "Finish the GitHub authorization in your browser. The local uploader owns the credential.";

  const link = auth.verificationUriComplete ?? auth.verificationUri;
  if (isSafeGitHubUrl(link)) {
    authorizationLink.href = link;
    authorizationLink.classList.remove("hidden");
  } else {
    authorizationLink.classList.add("hidden");
    authorizationLink.removeAttribute("href");
  }
}

function renderOverflow(notices: OverflowNotice[]): void {
  overflowCount.textContent = String(notices.length);
  overflowList.replaceChildren();
  overflowSection.classList.toggle("hidden", notices.length === 0);
  for (const notice of notices) {
    const item = document.createElement("li");
    item.className = "job";
    const title = document.createElement("span");
    title.className = "job-title";
    title.textContent = notice.lectureKey;
    const meta = document.createElement("span");
    meta.className = "job-meta";
    meta.textContent = `${statusLabel(notice.reason)} · ${notice.lectureDate} · captured ${notice.capturedAt}`;
    item.append(title, meta);
    overflowList.append(item);
  }
}

function renderJob(job: JobSummary): HTMLLIElement {
  const item = document.createElement("li");
  item.className = "job";

  const title = document.createElement("span");
  title.className = "job-title";
  title.textContent = `${job.lectureKey} — ${statusLabel(job.status)}`;

  const meta = document.createElement("span");
  meta.className = "job-meta";
  const remote = job.remoteFileKind === "malformed"
    ? "remote file is malformed"
    : job.remoteContentHash
      ? `remote hash ${job.remoteContentHash}`
      : job.remoteFileKind
        ? `remote ${job.remoteFileKind}`
        : "";
  meta.textContent = [job.targetPath, `local hash ${job.contentHash}`, remote].filter(Boolean).join(" · ");
  item.append(title, meta);

  const canRetry = job.status === "retryable_error" || job.status === "permanent_conflict" || job.status === "rejected_permission";
  const canDiscard = job.status === "permanent_conflict" || job.status.startsWith("rejected_");
  if (canRetry || canDiscard) {
    const actions = document.createElement("div");
    actions.className = "job-actions";
    if (canRetry) {
      const retry = document.createElement("button");
      retry.className = "button secondary";
      retry.type = "button";
      retry.textContent = "Retry";
      retry.addEventListener("click", () => {
        void retryJob(job);
      });
      actions.append(retry);
    }
    if (canDiscard) {
      const discard = document.createElement("button");
      discard.className = "button secondary";
      discard.type = "button";
      discard.textContent = "Discard local row";
      discard.addEventListener("click", () => {
        void discardJob(job);
      });
      actions.append(discard);
    }
    item.append(actions);
  }
  return item;
}

function renderJobs(status: UploaderStatus | null, append = false): void {
  if (!append) {
    loadedJobs = [];
    jobList.replaceChildren();
  }
  const previousLength = loadedJobs.length;
  if (status) {
    const seen = new Set(loadedJobs.map((job) => job.jobId));
    for (const job of status.jobs) {
      if (!seen.has(job.jobId)) loadedJobs.push(job);
    }
    nextBeforeJobId = status.nextBeforeJobId;
  }
  const jobsToRender = append ? loadedJobs.slice(previousLength) : loadedJobs;
  for (const job of jobsToRender) {
    jobList.append(renderJob(job));
  }
  const total = status ? Object.values(status.counts).reduce((sum, count) => sum + count, 0) : loadedJobs.length;
  queueCount.textContent = String(total);
  loadMoreButton.classList.toggle("hidden", nextBeforeJobId === null);
}

function render(snapshot: ExtensionSnapshot, append = false): void {
  const status = snapshot.uploader;
  connectionSummary.textContent = status ? statusLabel(status.authState) : "Local uploader not connected";
  drainState.textContent = status?.drainState ?? "idle";
  versions.textContent = `Extension ${status?.extensionVersion ?? "—"} · Uploader ${status?.uploaderVersion ?? "—"}`;
  pendingCount.textContent = String(snapshot.pendingHandoffs);
  pendingCopy.textContent = snapshot.pendingHandoffs > 0
    ? "Waiting for a definitive uploader acknowledgement."
    : "No unacknowledged captures.";
  lastOutcome.textContent = snapshot.lastOutcome
    ? `${statusLabel(snapshot.lastOutcome.status)}${snapshot.lastOutcome.lectureKey ? ` · ${snapshot.lastOutcome.lectureKey}` : ""}`
    : "";
  connectButton.textContent = status?.authState === "connected" ? "Reconnect GitHub" : "Connect GitHub";
  renderAuthorization(status);
  renderOverflow(snapshot.overflowNotices);
  renderJobs(status, append);
}

async function refresh(): Promise<void> {
  if (loading) return;
  setBusy(true);
  try {
    loadedJobs = [];
    nextBeforeJobId = null;
    const current = await send({ type: "popup_snapshot" });
    if (current.snapshot) render(current.snapshot);
    const response = await send({ type: "popup_status" });
    if (response.snapshot) render(response.snapshot);
  } finally {
    setBusy(false);
  }
}

async function loadMore(): Promise<void> {
  if (loading || nextBeforeJobId === null) return;
  setBusy(true);
  try {
    const response = await send({ type: "popup_status", beforeJobId: nextBeforeJobId });
    if (response.snapshot) render(response.snapshot, true);
  } finally {
    setBusy(false);
  }
}

async function connect(): Promise<void> {
  if (loading) return;
  setBusy(true);
  try {
    const response = await send({ type: "popup_connect" });
    if (response.snapshot) render(response.snapshot);
    if (!response.ok && response.message) lastOutcome.textContent = response.message;
  } finally {
    setBusy(false);
  }
}

async function reset(): Promise<void> {
  if (loading || !window.confirm("Reset the GitHub connection? Queued transcripts will be kept.")) return;
  setBusy(true);
  try {
    const response = await send({ type: "popup_reset" });
    if (response.snapshot) render(response.snapshot);
    if (!response.ok && response.message) lastOutcome.textContent = response.message;
  } finally {
    setBusy(false);
  }
}

async function retryJob(job: JobSummary): Promise<void> {
  if (loading) return;
  if (job.status === "permanent_conflict" && !window.confirm("Only retry after deleting or correcting the remote file in GitHub. Continue?")) return;
  setBusy(true);
  try {
    const response = await send({ type: "popup_retry", jobId: job.jobId });
    if (response.snapshot) render(response.snapshot);
    if (!response.ok && response.message) lastOutcome.textContent = response.message;
  } finally {
    setBusy(false);
  }
}

async function discardJob(job: JobSummary): Promise<void> {
  if (loading || !window.confirm(`Discard the local ${job.lectureKey} row? This never deletes a GitHub file.`)) return;
  setBusy(true);
  try {
    const response = await send({ type: "popup_discard", jobId: job.jobId });
    if (response.snapshot) render(response.snapshot);
    if (!response.ok && response.message) lastOutcome.textContent = response.message;
  } finally {
    setBusy(false);
  }
}

connectButton.addEventListener("click", () => void connect());
resetButton.addEventListener("click", () => void reset());
refreshButton.addEventListener("click", () => void refresh());
loadMoreButton.addEventListener("click", () => void loadMore());
void refresh();
