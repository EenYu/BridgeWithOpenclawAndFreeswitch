import assert from "node:assert/strict";
import { BridgeWebSocketClient } from "../src/lib/ws";

type EventName = "open" | "message" | "close" | "error";

class FakeSocket {
  static instances: FakeSocket[] = [];

  readonly listeners = new Map<EventName, Set<(event: any) => void>>();
  readonly closeCalls: Array<{ code?: number; reason?: string }> = [];
  readyState = 0;

  constructor(
    readonly url: string,
    readonly protocols?: string | string[],
  ) {
    FakeSocket.instances.push(this);
  }

  addEventListener(type: EventName, listener: (event: any) => void) {
    const bucket = this.listeners.get(type) ?? new Set<(event: any) => void>();
    bucket.add(listener);
    this.listeners.set(type, bucket);
  }

  removeEventListener(type: EventName, listener: (event: any) => void) {
    this.listeners.get(type)?.delete(listener);
  }

  close(code?: number, reason?: string) {
    this.closeCalls.push({ code, reason });
    this.readyState = 3;
    this.emit("close", { code, reason });
  }

  emit(type: EventName, event: Record<string, unknown> = {}) {
    if (type === "open") {
      this.readyState = 1;
    }

    if (type === "close") {
      this.readyState = 3;
    }

    this.listeners.get(type)?.forEach((listener) => listener(event));
  }
}

async function main() {
  await verifySharedConnectionLifecycle();
  await verifyUnexpectedCloseReconnect();
  console.log("ws regression passed");
}

async function verifySharedConnectionLifecycle() {
  FakeSocket.instances = [];

  const client = new BridgeWebSocketClient({
    url: "ws://fake/shared",
    idleCloseGraceMs: 20,
    reconnectDelayMs: () => 0,
    socketFactory: (url, protocols) => new FakeSocket(url, protocols),
  });

  const releaseDashboard = client.retain();
  assert.equal(FakeSocket.instances.length, 1, "first subscriber should open exactly one socket");
  FakeSocket.instances[0].emit("open");

  const releaseDetail = client.retain();
  assert.equal(FakeSocket.instances.length, 1, "second subscriber must reuse the existing socket");
  assert.equal(client.getConnectionSnapshot().activeConsumers, 2);

  releaseDashboard();
  assert.equal(FakeSocket.instances[0].closeCalls.length, 0, "socket must stay open while another page still subscribes");

  releaseDetail();
  await wait(5);
  assert.equal(FakeSocket.instances[0].closeCalls.length, 0, "idle grace window should prevent immediate close during route transitions");

  const releaseNextPage = client.retain();
  await wait(30);
  assert.equal(FakeSocket.instances.length, 1, "route transition during grace window must not create a replacement socket");
  assert.equal(FakeSocket.instances[0].closeCalls.length, 0, "grace window hand-off must cancel the pending close");

  releaseNextPage();
  await wait(30);
  assert.equal(FakeSocket.instances[0].closeCalls.length, 1, "socket should close once the last subscriber leaves past the grace window");
  assert.equal(client.getConnectionSnapshot().state, "idle");
}

async function verifyUnexpectedCloseReconnect() {
  FakeSocket.instances = [];

  const client = new BridgeWebSocketClient({
    url: "ws://fake/reconnect",
    idleCloseGraceMs: 10,
    reconnectDelayMs: () => 0,
    socketFactory: (url, protocols) => new FakeSocket(url, protocols),
  });

  const connectionStates: string[] = [];
  const seenEvents: string[] = [];
  const offConnection = client.subscribeConnection(() => {
    connectionStates.push(client.getConnectionSnapshot().state);
  });
  const offAny = client.onAny((event) => {
    seenEvents.push(event.type);
  });

  const releasePage = client.retain();
  assert.equal(FakeSocket.instances.length, 1, "first reconnect scenario subscriber should open one socket");

  const firstSocket = FakeSocket.instances[0];
  firstSocket.emit("open");
  firstSocket.emit("message", {
    data: JSON.stringify({
      type: "session.created",
      sessionId: "sess_test",
      timestamp: "2026-04-20T00:00:00Z",
      data: {
        session: {
          id: "sess_test",
          callId: "call-test",
          caller: "9003",
          state: "listening",
          startedAt: "2026-04-20T00:00:00Z",
        },
      },
    }),
  });

  assert.deepEqual(seenEvents, ["session.created"], "message dispatch should survive the shared-connection refactor");

  firstSocket.emit("close", { code: 1006, reason: "network_lost" });
  await wait(10);
  assert.equal(FakeSocket.instances.length, 2, "unexpected close should trigger a replacement socket");
  assert.equal(client.getConnectionSnapshot().state, "reconnecting");

  const secondSocket = FakeSocket.instances[1];
  secondSocket.emit("open");
  assert.equal(client.getConnectionSnapshot().state, "connected", "replacement socket should restore connected state");

  releasePage();
  await wait(20);
  assert.equal(secondSocket.closeCalls.length, 1, "final subscriber release should close the replacement socket");
  assert.ok(
    connectionStates.includes("reconnecting"),
    "connection status subscribers should observe reconnecting transitions",
  );
  assert.ok(
    connectionStates.includes("connected"),
    "connection status subscribers should observe connected transitions",
  );

  offAny();
  offConnection();
}

function wait(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

void main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
