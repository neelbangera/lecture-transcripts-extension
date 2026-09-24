/**
 * Durable GitHub-authorization code page.
 *
 * The popup closes as soon as it opens another tab, so the device-flow
 * user_code must live on a page that survives focus loss. This page is opened
 * by the popup with `?code=&url=&expires=` and shows the code alongside the
 * GitHub approval link, with a manual-entry fallback for environments where
 * the prefilled `?user_code=` query is stripped.
 */

interface TabsApi {
  create(createProperties: { url: string }): void;
}

interface ChromeApi {
  tabs?: TabsApi;
}

const chromeApi = (globalThis as { chrome?: ChromeApi }).chrome;

function element<T extends HTMLElement>(id: string): T {
  const value = document.getElementById(id);
  if (!value) throw new Error(`missing auth element: ${id}`);
  return value as T;
}

const authStatus = element<HTMLParagraphElement>("auth-status");
const authCopy = element<HTMLParagraphElement>("auth-copy");
const authCodeRow = element<HTMLElement>("auth-code-row");
const authCode = element<HTMLElement>("auth-code");
const authCopyCode = element<HTMLButtonElement>("auth-copy-code");
const authExpiry = element<HTMLParagraphElement>("auth-expiry");
const authGitHubLink = element<HTMLAnchorElement>("auth-github-link");
const authFallback = element<HTMLParagraphElement>("auth-fallback");
const authKeychain = element<HTMLParagraphElement>("auth-keychain");

let expiryTimer: ReturnType<typeof setInterval> | null = null;

export function isSafeGitHubUrl(value: string | null): value is string {
  if (!value) return false;
  try {
    const url = new URL(value);
    return (
      url.protocol === "https:" &&
      (url.hostname === "github.com" || url.hostname === "www.github.com")
    );
  } catch {
    return false;
  }
}

export function authPageParams(search: string): {
  code: string | null;
  url: string | null;
  expires: string | null;
} {
  const params = new URLSearchParams(search);
  const code = params.get("code");
  const url = params.get("url");
  const expires = params.get("expires");
  return {
    code: code && code.trim() !== "" ? code.trim() : null,
    url: url && url.trim() !== "" ? url.trim() : null,
    expires: expires && expires.trim() !== "" ? expires.trim() : null,
  };
}

export function formatExpiry(expiresAt: string | null, nowMs: number): string {
  if (!expiresAt) return "Waiting for approval on GitHub…";
  const remainingMs = Date.parse(expiresAt) - nowMs;
  if (!Number.isFinite(remainingMs)) return "Waiting for approval on GitHub…";
  if (remainingMs <= 0) return "This code expired. Press Connect GitHub for a new one.";
  const totalSeconds = Math.ceil(remainingMs / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `Waiting for approval on GitHub… expires in ${minutes}:${String(seconds).padStart(2, "0")}`;
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
    const clipboard = (
      globalThis as {
        navigator?: { clipboard?: { writeText(value: string): Promise<void> } };
      }
    ).navigator?.clipboard;
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

export function installAuthPage(search: string, options: { now?: () => number } = {}): void {
  const now = options.now ?? (() => Date.now());
  const { code, url, expires } = authPageParams(search);

  if (code) {
    authCode.textContent = code;
    authCodeRow.classList.remove("hidden");
    authStatus.textContent = "Enter this code if GitHub does not already show it.";
  } else {
    authCodeRow.classList.add("hidden");
    authCode.textContent = "";
    authStatus.textContent = "No code was supplied. Press Connect GitHub in the popup.";
  }

  authCopy.textContent = code
    ? "Approve the lecture-transcripts app in the GitHub tab. This page never receives a credential."
    : "Start the connection from the extension popup to get a code.";
  authKeychain.classList.toggle("hidden", !code);
  authFallback.classList.toggle("hidden", !code);

  if (isSafeGitHubUrl(url)) {
    authGitHubLink.href = url;
    authGitHubLink.classList.remove("hidden");
    openExternal(url);
  } else {
    authGitHubLink.classList.add("hidden");
    authGitHubLink.removeAttribute("href");
    authFallback.classList.remove("hidden");
  }

  const update = () => {
    authExpiry.textContent = code
      ? formatExpiry(expires, now())
      : "";
    authExpiry.classList.toggle("hidden", !code);
  };
  if (expiryTimer !== null) clearInterval(expiryTimer);
  update();
  if (code && expires) {
    expiryTimer = setInterval(update, 1000);
  }

  authCopyCode.addEventListener("click", () => {
    const value = authCode.textContent?.trim();
    if (!value) return;
    void copyText(value).then((copied) => {
      flashButton(authCopyCode, copied ? "Copied" : "Copy failed");
    });
  });
}

const isTest = typeof (globalThis as { __AUTH_PAGE_TEST__?: boolean }).__AUTH_PAGE_TEST__ === "boolean";
if (!isTest) {
  installAuthPage(globalThis.location?.search ?? "");
}
