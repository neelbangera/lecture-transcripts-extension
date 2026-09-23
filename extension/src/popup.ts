import type { BackgroundResponse, ExtensionMessage } from "./background";
import {
  canDiscardJob,
  canRetryJob,
  clearableJobs,
  clearUploadedConfirmText,
  discardConfirmText,
} from "./job-actions";
import {
  statusLabel,
  type ExtensionSnapshot,
  type JobSummary,
  type OverflowNotice,
  type QueueStatus,
  type UploaderStatus,
} from "./status";
import { canOpenOptionsPage, openOptionsPage } from "./settings-actions";

interface TabsApi {
  create(createProperties: { url: string }): void;
}

interface RuntimeMessageApi {
  id?: string;
  sendMessage(message: ExtensionMessage, callback: (response: BackgroundResponse) => void): void;
  openOptionsPage?(callback?: () => void): void | Promise<void>;
}

interface ChromeApi {
  runtime?: RuntimeMessageApi;
  tabs?: TabsApi;
}

const chromeApi = (globalThis as { chrome?: ChromeApi }).chrome;

function element<T extends HTMLElement>(id: string): T {
  const value = document.getElementById(id);
  if (!value) throw new Error(`missing popup element: ${id}`);
  return value as T;
}

const connectionSummary = element<HTMLParagraphElement>("connection-summary");
const drainState = element<HTMLSpanElement>("drain-state");
const authorization = element<HTMLElement>("authorization");
const authorizationCopy = element<HTMLParagraphElement>("authorization-copy");
const authorizationCodeRow = element<HTMLElement>("authorization-code-row");
const authorizationCode = element<HTMLElement>("authorization-code");
const copyCodeButton = element<HTMLButtonElement>("copy-code-button");
const authorizationExpiry = element<HTMLParagraphElement>("authorization-expiry");
const authorizationLink = element<HTMLAnchorElement>("authorization-link");
const authorizationKeychain = element<HTMLParagraphElement>("authorization-keychain");
const connectButton = element<HTMLButtonElement>("connect-button");
const resetButton = element<HTMLButtonElement>("reset-button");
const refreshButton = element<HTMLButtonElement>("refresh-button");
const settingsButton = element<HTMLButtonElement>("settings-button");
const lastOutcome = element<HTMLParagraphElement>("last-outcome");
const pendingCount = element<HTMLSpanElement>("pending-count");
const pendingCopy = element<HTMLParagraphElement>("pending-copy");
const overflowSection = element<HTMLElement>("overflow-section");
const overflowCount = element<HTMLSpanElement>("overflow-count");
const overflowList = element<HTMLUListElement>("overflow-list");
const queueCount = element<HTMLSpanElement>("queue-count");
const queueCopy = element<HTMLParagraphElement>("queue-copy");
const jobList = element<HTMLUListElement>("job-list");
const clearUploadedButton = element<HTMLButtonElement>("clear-uploaded-button");
const loadMoreButton = element<HTMLButtonElement>("load-more-button");
const versions = element<HTMLSpanElement>("versions");
const extensionId = element<HTMLElement>("extension-id");
const copyIdButton = element<HTMLButtonElement>("copy-id-button");

let loadedJobs: JobSummary[] = [];
let nextBeforeJobId: number | null = null;
let loading = false;
/** Challenges whose GitHub page has already been opened this popup session. */
const openedChallenges = new Set<string>();
let expiryTimer: ReturnType<typeof setInterval> | null = null;
let currentExpiresAt: string | null = null;

type NoticeVariant = "info" | "success" | "warn" | "error";

const OUTCOME_SUCCESS = new Set(["uploaded", "unchanged", "discarded", "reset"]);
const OUTCOME_WARN = new Set([
  "retryable_error",
  "not_ready",
  "waiting_for_uploader",
  "skipped_section",
]);

function outcomeVariant(status: string): NoticeVariant {
  if (OUTCOME_SUCCESS.has(status)) return "success";
  if (status === "permanent_conflict" || status.startsWith("rejected_")) {
    return "error";
  }
  if (OUTCOME_WARN.has(status)) return "warn";
  return "info";
}

function setOutcome(message: string, variant: NoticeVariant = "info"): void {
  lastOutcome.textContent = message;
  lastOutcome.classList.remove(
    "notice-info",
    "notice-success",
    "notice-warn",
    "notice-error",
  );
  if (variant !== "info") lastOutcome.classList.add(`notice-${variant}`);
}

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
  clearUploadedButton.disabled = value;
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

function openExternal(url: string): void {
  try {
    chromeApi?.tabs?.create({ url });
    return;
  } catch {
    // Fall through to window.open when the tabs bridge is unavailable.
  }
  try {
    window.open(url, "_blank", "noopener,noreferrer");
  } catch {
    // The visible link remains as the fallback.
  }
}

async function copyText(text: string): Promise<boolean> {
  try {
    const clipboard = (globalThis as { navigator?: { clipboard?: { writeText(value: string): Promise<void> } } }).navigator?.clipboard;
    if (clipboard?.writeText) {
      await clipboard.writeText(text);
      return true;
    }
  } catch {
    // Clipboard access can be denied; report failure instead of throwing.
  }
  return false;
}

function flashButton(button: HTMLButtonElement, label: string): void {
  const original = button.textContent;
  button.textContent = label;
  setTimeout(() => {
    button.textContent = original;
  }, 1500);
}

function stopExpiryCountdown(): void {
  if (expiryTimer !== null) {
    clearInterval(expiryTimer);
    expiryTimer = null;
  }
  currentExpiresAt = null;
  authorizationExpiry.textContent = "";
  authorizationExpiry.classList.add("hidden");
}

function renderExpiry(expiresAt: string | null): void {
  stopExpiryCountdown();
  currentExpiresAt = expiresAt;
  const update = () => {
    if (!currentExpiresAt) {
      authorizationExpiry.textContent = "Waiting for approval on GitHub…";
      authorizationExpiry.classList.remove("hidden");
      return;
    }
    const remainingMs = Date.parse(currentExpiresAt) - Date.now();
    if (!Number.isFinite(remainingMs)) {
      authorizationExpiry.textContent = "Waiting for approval on GitHub…";
      authorizationExpiry.classList.remove("hidden");
      return;
    }
    if (remainingMs <= 0) {
      stopExpiryCountdown();
      authorizationExpiry.textContent = "This code expired. Press Connect GitHub for a new one.";
      authorizationExpiry.classList.remove("hidden");
      return;
    }
    const totalSeconds = Math.ceil(remainingMs / 1000);
    const minutes = Math.floor(totalSeconds / 60);
    const seconds = totalSeconds % 60;
    authorizationExpiry.textContent = `Waiting for approval on GitHub… expires in ${minutes}:${String(seconds).padStart(2, "0")}`;
    authorizationExpiry.classList.remove("hidden");
  };
  update();
  if (currentExpiresAt) {
    expiryTimer = setInterval(update, 1000);
  }
}

function renderAuthorization(status: UploaderStatus | null): void {
  const auth = status?.authorization;
  if (!status || status.authState !== "authorizing" || !auth) {
    authorization.classList.add("hidden");
    authorizationCopy.textContent = "";
    authorizationCodeRow.classList.add("hidden");
    authorizationCode.textContent = "";
    authorizationLink.classList.add("hidden");
    authorizationLink.removeAttribute("href");
    authorizationKeychain.classList.add("hidden");
    stopExpiryCountdown();
    return;
  }

  authorization.classList.remove("hidden");
  authorizationKeychain.classList.remove("hidden");
  authorizationCopy.textContent = auth.userCode
    ? "Approve the lecture-transcripts app in the browser tab that just opened. This popup never receives a credential."
    : "Finish the GitHub authorization in your browser. The local uploader owns the credential.";

  if (auth.userCode) {
    authorizationCode.textContent = auth.userCode;
    authorizationCodeRow.classList.remove("hidden");
    copyCodeButton.hidden = false;
    copyCodeButton.textContent = "Copy code";
  } else {
    authorizationCodeRow.classList.add("hidden");
    authorizationCode.textContent = "";
  }

  const link = auth.verificationUriComplete ?? auth.verificationUri;
  if (isSafeGitHubUrl(link)) {
    authorizationLink.href = link;
    authorizationLink.classList.remove("hidden");
    const challengeKey = auth.userCode ?? link;
    if (!openedChallenges.has(challengeKey)) {
      openedChallenges.add(challengeKey);
      openExternal(link);
    }
  } else {
    authorizationLink.classList.add("hidden");
    authorizationLink.removeAttribute("href");
  }

  renderExpiry(auth.expiresAt);
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
    meta.textContent = notice.staleLectureKey
      ? `${statusLabel(notice.reason)} · was ${notice.staleLectureKey} · ${notice.lectureDate}`
      : `${statusLabel(notice.reason)} · ${notice.lectureDate} · captured ${notice.capturedAt}`;
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
  meta.textContent = [job.targetPath, remote].filter(Boolean).join(" · ");
  item.append(title, meta);

  const canRetry = canRetryJob(job.status);
  const canDiscard = canDiscardJob(job.status);
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
  queueCopy.classList.toggle("hidden", total > 0);
  clearUploadedButton.classList.toggle("hidden", clearableJobs(loadedJobs).length === 0);
  loadMoreButton.classList.toggle("hidden", nextBeforeJobId === null);
}

function render(snapshot: ExtensionSnapshot, append = false): void {
  const status = snapshot.uploader;
  connectionSummary.textContent = status ? statusLabel(status.authState) : "Local uploader not connected";
  drainState.textContent = status?.drainState ?? "idle";
  versions.textContent = `Extension ${status?.extensionVersion ?? "—"} · Uploader ${status?.uploaderVersion ?? "—"}`;
  const runtimeId = chromeApi?.runtime?.id;
  extensionId.textContent = runtimeId ? `ID ${runtimeId}` : "ID —";
  copyIdButton.classList.toggle("hidden", !runtimeId);
  pendingCount.textContent = String(snapshot.pendingHandoffs);
  pendingCopy.textContent = snapshot.pendingHandoffs > 0
    ? "Captured, not yet acknowledged by the uploader. The outbox replays automatically."
    : "Every capture has been acknowledged by the uploader.";
  setOutcome(
    snapshot.lastOutcome
      ? `${statusLabel(snapshot.lastOutcome.status)}${snapshot.lastOutcome.lectureKey ? ` · ${snapshot.lastOutcome.lectureKey}` : ""}`
      : "",
    snapshot.lastOutcome ? outcomeVariant(snapshot.lastOutcome.status) : "info",
  );
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
    if (!response.ok && response.message) setOutcome(response.message, "error");
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
    if (!response.ok && response.message) setOutcome(response.message, "error");
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
    if (!response.ok && response.message) setOutcome(response.message, "error");
  } finally {
    setBusy(false);
  }
}

async function discardJob(job: JobSummary): Promise<void> {
  if (loading || !window.confirm(discardConfirmText(job))) return;
  setBusy(true);
  try {
    const response = await send({ type: "popup_discard", jobId: job.jobId });
    if (response.snapshot) render(response.snapshot);
    if (!response.ok && response.message) setOutcome(response.message, "error");
  } finally {
    setBusy(false);
  }
}

async function clearUploaded(): Promise<void> {
  if (loading) return;
  const targets = clearableJobs(loadedJobs);
  if (targets.length === 0) return;
  if (!window.confirm(clearUploadedConfirmText(targets.length))) return;
  setBusy(true);
  let failures = 0;
  try {
    for (const job of targets) {
      const response = await send({ type: "popup_discard", jobId: job.jobId });
      if (!response.ok) failures += 1;
    }
  } finally {
    setBusy(false);
  }
  await refresh();
  if (failures > 0) {
    setOutcome(`${failures} uploaded ${failures === 1 ? "row was" : "rows were"} not cleared.`, "error");
  }
}

connectButton.addEventListener("click", () => void connect());
resetButton.addEventListener("click", () => void reset());
refreshButton.addEventListener("click", () => void refresh());
clearUploadedButton.addEventListener("click", () => void clearUploaded());
loadMoreButton.addEventListener("click", () => void loadMore());
copyCodeButton.addEventListener("click", () => {
  const code = authorizationCode.textContent?.trim();
  if (!code) return;
  void copyText(code).then((copied) => {
    flashButton(copyCodeButton, copied ? "Copied" : "Copy failed");
  });
});
copyIdButton.addEventListener("click", () => {
  const id = chromeApi?.runtime?.id;
  if (!id) return;
  void copyText(id).then((copied) => {
    flashButton(copyIdButton, copied ? "Copied" : "Copy failed");
  });
});

if (canOpenOptionsPage(chromeApi?.runtime)) {
  settingsButton.addEventListener("click", () => {
    if (!openOptionsPage(chromeApi?.runtime)) settingsButton.classList.add("hidden");
  });
} else {
  settingsButton.classList.add("hidden");
}

void refresh();
