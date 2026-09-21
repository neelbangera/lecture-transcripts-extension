import type { NativeTranscriptJob } from "./native-messaging";
import {
  type ExtensionSnapshot,
  type LastOutcome,
  type OverflowNotice,
  type UploaderStatus,
} from "./status";

export const MAX_PENDING_HANDOFFS = 3;
export const MAX_OVERFLOW_NOTICES = 20;
export const STORAGE_KEY = "lectureTranscriptsExtensionState";

export interface PendingHandoff {
  lectureKey: string;
  contentHash: string;
  lectureDate: string;
  capturedAt: string;
  job: NativeTranscriptJob;
}

interface PersistedState {
  pendingHandoffs: PendingHandoff[];
  overflowNotices: OverflowNotice[];
  lastStatus: UploaderStatus | null;
  lastOutcome: LastOutcome | null;
  updatedAt: string;
}

export interface StorageAreaLike {
  get(keys?: string | string[] | null): Promise<Record<string, unknown>>;
  set(items: Record<string, unknown>): Promise<void>;
}

export class OutboxFullError extends Error {
  constructor() {
    super("pending handoff outbox is full");
    this.name = "OutboxFullError";
  }
}

export class OverflowNoticeFullError extends Error {
  constructor() {
    super("overflow notice list is full");
    this.name = "OverflowNoticeFullError";
  }
}

function defaultStorageArea(): StorageAreaLike {
  const chromeApi = (globalThis as { chrome?: { storage?: { local?: StorageAreaLike } } }).chrome;
  if (!chromeApi?.storage?.local) throw new Error("chrome.storage.local is unavailable");
  return chromeApi.storage.local;
}

function nowIso(): string {
  return new Date().toISOString();
}

function clone<T>(value: T): T {
  return structuredClone(value);
}

function emptyState(): PersistedState {
  return {
    pendingHandoffs: [],
    overflowNotices: [],
    lastStatus: null,
    lastOutcome: null,
    updatedAt: nowIso(),
  };
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function readPersistedState(value: unknown): PersistedState {
  if (!isObject(value)) return emptyState();
  const pendingHandoffs = Array.isArray(value.pendingHandoffs) ? value.pendingHandoffs : [];
  const overflowNotices = Array.isArray(value.overflowNotices) ? value.overflowNotices : [];
  return {
    pendingHandoffs: pendingHandoffs.slice(0, MAX_PENDING_HANDOFFS) as PendingHandoff[],
    overflowNotices: overflowNotices.slice(0, MAX_OVERFLOW_NOTICES) as OverflowNotice[],
    lastStatus: (value.lastStatus ?? null) as UploaderStatus | null,
    lastOutcome: (value.lastOutcome ?? null) as LastOutcome | null,
    updatedAt: typeof value.updatedAt === "string" ? value.updatedAt : nowIso(),
  };
}

export class ExtensionStorage {
  private operation: Promise<unknown> = Promise.resolve();

  constructor(private readonly area: StorageAreaLike = defaultStorageArea()) {}

  async snapshot(): Promise<ExtensionSnapshot> {
    const state = await this.read();
    return {
      uploader: state.lastStatus ? clone(state.lastStatus) : null,
      pendingHandoffs: state.pendingHandoffs.length,
      overflowNotices: clone(state.overflowNotices),
      lastOutcome: state.lastOutcome ? clone(state.lastOutcome) : null,
      updatedAt: state.updatedAt,
    };
  }

  async pendingHandoffs(): Promise<PendingHandoff[]> {
    const state = await this.read();
    return clone(state.pendingHandoffs);
  }

  async addPendingHandoff(job: NativeTranscriptJob): Promise<PendingHandoff> {
    return this.serial(async () => {
      const state = await this.readUnserialized();
      const existing = state.pendingHandoffs.find(
        (entry) => entry.lectureKey === job.lectureKey && entry.contentHash === job.contentHash,
      );
      if (existing) return clone(existing);
      if (state.pendingHandoffs.length >= MAX_PENDING_HANDOFFS) throw new OutboxFullError();

      const entry: PendingHandoff = {
        lectureKey: job.lectureKey,
        contentHash: job.contentHash,
        lectureDate: job.lectureDate,
        capturedAt: job.capturedAt,
        job: clone(job),
      };
      state.pendingHandoffs.push(entry);
      await this.write(state);
      return clone(entry);
    });
  }

  async removePendingHandoff(lectureKey: string, contentHash: string): Promise<boolean> {
    return this.serial(async () => {
      const state = await this.readUnserialized();
      const next = state.pendingHandoffs.filter(
        (entry) => !(entry.lectureKey === lectureKey && entry.contentHash === contentHash),
      );
      if (next.length === state.pendingHandoffs.length) return false;
      state.pendingHandoffs = next;
      await this.write(state);
      return true;
    });
  }

  async addOverflowNotice(notice: OverflowNotice): Promise<void> {
    return this.serial(async () => {
      const state = await this.readUnserialized();
      const duplicate = state.overflowNotices.some(
        (entry) =>
          entry.lectureKey === notice.lectureKey &&
          entry.lectureDate === notice.lectureDate &&
          entry.capturedAt === notice.capturedAt &&
          entry.reason === notice.reason,
      );
      if (duplicate) return;
      if (state.overflowNotices.length >= MAX_OVERFLOW_NOTICES) throw new OverflowNoticeFullError();
      // Keep notices newest-first for the popup, but never evict an older notice.
      state.overflowNotices.unshift(clone(notice));
      await this.write(state);
    });
  }

  async removeOverflowNotice(index: number): Promise<boolean> {
    return this.serial(async () => {
      const state = await this.readUnserialized();
      if (!Number.isInteger(index) || index < 0 || index >= state.overflowNotices.length) return false;
      state.overflowNotices.splice(index, 1);
      await this.write(state);
      return true;
    });
  }

  async setUploaderStatus(status: UploaderStatus): Promise<void> {
    return this.serial(async () => {
      const state = await this.readUnserialized();
      state.lastStatus = clone(status);
      await this.write(state);
    });
  }

  async recordOutcome(outcome: Omit<LastOutcome, "at"> & { at?: string }): Promise<void> {
    return this.serial(async () => {
      const state = await this.readUnserialized();
      state.lastOutcome = {
        status: outcome.status,
        lectureKey: outcome.lectureKey,
        contentHash: outcome.contentHash,
        message: outcome.message.slice(0, 256),
        action: outcome.action,
        at: outcome.at ?? nowIso(),
      };
      await this.write(state);
    });
  }

  async clearStatus(): Promise<void> {
    return this.serial(async () => {
      const state = await this.readUnserialized();
      state.lastStatus = null;
      await this.write(state);
    });
  }

  private async read(): Promise<PersistedState> {
    return this.readUnserialized();
  }

  private async readUnserialized(): Promise<PersistedState> {
    const result = await this.area.get(STORAGE_KEY);
    return readPersistedState(result[STORAGE_KEY]);
  }

  private async write(state: PersistedState): Promise<void> {
    state.updatedAt = nowIso();
    await this.area.set({ [STORAGE_KEY]: state });
  }

  private async serial<T>(operation: () => Promise<T>): Promise<T> {
    const previous = this.operation;
    let release!: () => void;
    this.operation = new Promise<void>((resolve) => {
      release = resolve;
    });
    await previous;
    try {
      return await operation();
    } finally {
      release();
    }
  }
}

