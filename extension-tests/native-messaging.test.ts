import { describe, expect, it } from "vitest";
import {
  NativeMessagingClient,
  isNativeRequest,
  isNativeResponse,
  makeStatusRequest,
  type NativePortLike,
  type NativeRequest,
  type NativeResponse,
} from "../extension/src/native-messaging";

function emptyCounts(): Record<string, number> {
  return {
    queued: 0,
    uploading: 0,
    uploaded: 0,
    unchanged: 0,
    retryable_error: 0,
    permanent_conflict: 0,
    rejected_missing_identity: 0,
    rejected_ambiguous_metadata: 0,
    rejected_oversized: 0,
    rejected_queue_full: 0,
    rejected_invalid_hash: 0,
    rejected_unsafe_url: 0,
    rejected_unknown_field: 0,
    rejected_invalid_schema: 0,
    rejected_permission: 0,
  };
}

function statusResponse(requestId: string | null): NativeResponse {
  return {
    type: "status",
    protocolVersion: 1,
    requestId,
    extensionVersion: "0.1.0",
    uploaderVersion: "test-uploader",
    authState: "not_connected",
    authorization: {
      userCode: null,
      verificationUri: null,
      verificationUriComplete: null,
      expiresAt: null,
    },
    drainState: "idle",
    counts: emptyCounts() as never,
    jobs: [],
    nextBeforeJobId: null,
  };
}

class FakePort implements NativePortLike {
  readonly messages: NativeRequest[] = [];
  private readonly messageListeners = new Set<(message: unknown) => void>();
  private readonly disconnectListeners = new Set<() => void>();

  onMessage = {
    addListener: (listener: (message: unknown) => void) => {
      this.messageListeners.add(listener);
    },
  };

  onDisconnect = {
    addListener: (listener: () => void) => {
      this.disconnectListeners.add(listener);
    },
  };

  postMessage(message: NativeRequest): void {
    this.messages.push(message);
    const response = statusResponse(message.requestId);
    queueMicrotask(() => this.messageListeners.forEach((listener) => listener(response)));
  }

  disconnect(): void {
    this.disconnectListeners.forEach((listener) => listener());
  }
}

describe("Native Messaging contract", () => {
  it("constructs bounded status requests and rejects unknown request fields", () => {
    const request = makeStatusRequest(undefined, 50, "request-1");
    expect(request).toEqual({
      type: "status_request",
      protocolVersion: 1,
      requestId: "request-1",
      limit: 50,
    });
    expect(isNativeRequest(request)).toBe(true);
    expect(isNativeRequest({ ...request, extra: true })).toBe(false);
    expect(isNativeRequest({ ...request, limit: 51 })).toBe(false);
  });

  it("reuses one persistent port and correlates concurrent responses", async () => {
    const port = new FakePort();
    let factoryCalls = 0;
    const client = new NativeMessagingClient({
      portFactory: () => {
        factoryCalls += 1;
        return port;
      },
      requestIdFactory: (() => {
        let next = 0;
        return () => `request-${++next}`;
      })(),
    });

    const [first, second] = await Promise.all([
      client.statusRequest(),
      client.statusRequest(),
    ]);

    expect(factoryCalls).toBe(1);
    expect(port.messages).toHaveLength(2);
    expect(port.messages[0].requestId).not.toBe(port.messages[1].requestId);
    expect(first.type).toBe("status");
    expect(second.type).toBe("status");
  });

  it("validates exact response envelopes", () => {
    const response = statusResponse("request-1");
    expect(isNativeResponse(response)).toBe(true);
    expect(isNativeResponse({ ...response, transcript: "must never be in status" })).toBe(false);
  });
});

