import type { BridgeEvent, BridgeEventMap, BridgeEventName } from "../types/ws";
import { normalizeBridgeEvent } from "./bridge-contract";

const SOCKET_CONNECTING = 0;
const SOCKET_OPEN = 1;
const SOCKET_CLOSING = 2;
const SOCKET_CLOSED = 3;

const DEFAULT_IDLE_CLOSE_GRACE_MS = 800;
const DEFAULT_RECONNECT_CAP_MS = 10_000;
const NORMAL_CLOSURE_CODE = 1000;

export type BridgeConnectionState =
  | "idle"
  | "connecting"
  | "connected"
  | "reconnecting";

export interface BridgeConnectionSnapshot {
  state: BridgeConnectionState;
  activeConsumers: number;
  reconnectAttempt: number;
  lastError: string | null;
}

export interface WebSocketClientOptions {
  url?: string;
  protocols?: string | string[];
  idleCloseGraceMs?: number;
  reconnectDelayMs?: (attempt: number) => number;
  socketFactory?: WebSocketFactory;
  setTimeoutFn?: typeof setTimeout;
  clearTimeoutFn?: typeof clearTimeout;
}

interface MessageEventLike {
  data: unknown;
}

interface CloseEventLike {
  code?: number;
  reason?: string;
}

interface EventTargetLike<EventMap extends Record<string, unknown>> {
  addEventListener<K extends keyof EventMap>(
    type: K,
    listener: (event: EventMap[K]) => void,
  ): void;
  removeEventListener<K extends keyof EventMap>(
    type: K,
    listener: (event: EventMap[K]) => void,
  ): void;
}

type BridgeSocketEventMap = {
  open: Event;
  message: MessageEventLike;
  close: CloseEventLike;
  error: Event;
};

interface BridgeSocketLike extends EventTargetLike<BridgeSocketEventMap> {
  readonly readyState: number;
  close(code?: number, reason?: string): void;
}

type EventListener<T extends BridgeEventName> = (event: BridgeEventMap[T]) => void;
type AnyEventListener = (event: BridgeEvent) => void;
type ConnectionListener = () => void;
type TimerHandle = ReturnType<typeof setTimeout>;
type WebSocketFactory = (url: string, protocols?: string | string[]) => BridgeSocketLike;

export class BridgeWebSocketClient {
  private readonly url: string;
  private readonly protocols?: string | string[];
  private readonly idleCloseGraceMs: number;
  private readonly reconnectDelayMs: (attempt: number) => number;
  private readonly socketFactory: WebSocketFactory;
  private readonly setTimeoutFn: typeof setTimeout;
  private readonly clearTimeoutFn: typeof clearTimeout;

  private socket: BridgeSocketLike | null = null;
  private readonly listeners = new Map<BridgeEventName, Set<EventListener<BridgeEventName>>>();
  private readonly anyListeners = new Set<AnyEventListener>();
  private readonly connectionListeners = new Set<ConnectionListener>();

  private activeConsumers = 0;
  private reconnectAttempt = 0;
  private lastError: string | null = null;
  private state: BridgeConnectionState = "idle";
  private reconnectTimer: TimerHandle | null = null;
  private idleCloseTimer: TimerHandle | null = null;
  private intentionalCloseSocket: BridgeSocketLike | null = null;

  constructor(options: WebSocketClientOptions = {}) {
    this.url = options.url ?? deriveSocketUrl();
    this.protocols = options.protocols;
    this.idleCloseGraceMs = options.idleCloseGraceMs ?? DEFAULT_IDLE_CLOSE_GRACE_MS;
    this.reconnectDelayMs =
      options.reconnectDelayMs ??
      ((attempt) => Math.min(1000 * 2 ** Math.max(attempt - 1, 0), DEFAULT_RECONNECT_CAP_MS));
    this.socketFactory = options.socketFactory ?? ((url, protocols) => new WebSocket(url, protocols));
    this.setTimeoutFn = options.setTimeoutFn ?? setTimeout;
    this.clearTimeoutFn = options.clearTimeoutFn ?? clearTimeout;
  }

  connect(): () => void {
    return this.retain();
  }

  retain(): () => void {
    this.activeConsumers += 1;
    this.cancelIdleClose();
    this.emitConnectionChange();
    this.ensureConnected();

    let released = false;
    return () => {
      if (released) {
        return;
      }

      released = true;
      this.release();
    };
  }

  disconnect(): void {
    this.activeConsumers = 0;
    this.cancelReconnect();
    this.cancelIdleClose();
    this.closeSocket(true);
    this.reconnectAttempt = 0;
    this.lastError = null;
    this.updateState("idle");
  }

  on<T extends BridgeEventName>(type: T, listener: EventListener<T>): () => void {
    const bucket = this.listeners.get(type) ?? new Set<EventListener<BridgeEventName>>();
    bucket.add(listener as EventListener<BridgeEventName>);
    this.listeners.set(type, bucket);
    return () => {
      bucket.delete(listener as EventListener<BridgeEventName>);
    };
  }

  onAny(listener: AnyEventListener): () => void {
    this.anyListeners.add(listener);
    return () => {
      this.anyListeners.delete(listener);
    };
  }

  subscribeConnection(listener: ConnectionListener): () => void {
    this.connectionListeners.add(listener);
    return () => {
      this.connectionListeners.delete(listener);
    };
  }

  getConnectionSnapshot(): BridgeConnectionSnapshot {
    return {
      state: this.state,
      activeConsumers: this.activeConsumers,
      reconnectAttempt: this.reconnectAttempt,
      lastError: this.lastError,
    };
  }

  private release(): void {
    this.activeConsumers = Math.max(this.activeConsumers - 1, 0);

    if (this.activeConsumers === 0) {
      this.cancelReconnect();
      this.scheduleIdleClose();
    }

    this.emitConnectionChange();
  }

  private ensureConnected(): void {
    if (this.socket && this.socket.readyState <= SOCKET_OPEN) {
      return;
    }

    if (this.reconnectTimer) {
      return;
    }

    this.openSocket();
  }

  private openSocket(): void {
    const socket = this.socketFactory(this.url, this.protocols);
    this.socket = socket;
    this.updateState(this.reconnectAttempt > 0 ? "reconnecting" : "connecting");

    const handleOpen = () => {
      if (this.socket !== socket) {
        return;
      }

      this.reconnectAttempt = 0;
      this.lastError = null;
      this.updateState("connected");
    };

    const handleMessage = (event: MessageEventLike) => {
      if (this.socket !== socket) {
        return;
      }

      const parsed = this.parseEvent(event.data);
      if (!parsed) {
        return;
      }

      this.listeners.get(parsed.type)?.forEach((listener) => listener(parsed as never));
      this.anyListeners.forEach((listener) => listener(parsed));
    };

    const handleError = () => {
      if (this.socket !== socket) {
        return;
      }

      this.lastError = "WebSocket error";
      this.emitConnectionChange();
    };

    const handleClose = (event: CloseEventLike) => {
      if (this.socket === socket) {
        this.socket = null;
      }

      socket.removeEventListener("open", handleOpen);
      socket.removeEventListener("message", handleMessage);
      socket.removeEventListener("error", handleError);
      socket.removeEventListener("close", handleClose);

      if (this.intentionalCloseSocket === socket) {
        this.intentionalCloseSocket = null;
        if (this.activeConsumers === 0) {
          this.reconnectAttempt = 0;
          this.lastError = null;
          this.updateState("idle");
        }
        return;
      }

      if (this.activeConsumers === 0) {
        this.reconnectAttempt = 0;
        this.lastError = null;
        this.updateState("idle");
        return;
      }

      const closeCode = event.code ?? SOCKET_CLOSED;
      const closeReason = event.reason?.trim() ? event.reason : `close code ${closeCode}`;
      this.lastError = `WebSocket closed: ${closeReason}`;
      this.scheduleReconnect();
    };

    socket.addEventListener("open", handleOpen);
    socket.addEventListener("message", handleMessage);
    socket.addEventListener("error", handleError);
    socket.addEventListener("close", handleClose);
  }

  private scheduleReconnect(): void {
    if (this.activeConsumers === 0 || this.reconnectTimer) {
      return;
    }

    this.reconnectAttempt += 1;
    this.updateState("reconnecting");

    const delay = this.reconnectDelayMs(this.reconnectAttempt);
    this.reconnectTimer = this.setTimeoutFn(() => {
      this.reconnectTimer = null;
      if (this.activeConsumers === 0) {
        return;
      }

      this.openSocket();
    }, delay);
  }

  private scheduleIdleClose(): void {
    if (this.idleCloseTimer || !this.socket) {
      return;
    }

    // 路由切换时允许下一个页面在极短窗口内接管共享连接，避免无意义断开。
    this.idleCloseTimer = this.setTimeoutFn(() => {
      this.idleCloseTimer = null;
      if (this.activeConsumers > 0) {
        return;
      }

      this.closeSocket(true);
      this.reconnectAttempt = 0;
      this.lastError = null;
      this.updateState("idle");
    }, this.idleCloseGraceMs);
  }

  private closeSocket(intentional: boolean): void {
    if (!this.socket || this.socket.readyState >= SOCKET_CLOSING) {
      return;
    }

    if (intentional) {
      this.intentionalCloseSocket = this.socket;
    }

    this.socket.close(NORMAL_CLOSURE_CODE, "client_disconnect");
  }

  private cancelReconnect(): void {
    if (!this.reconnectTimer) {
      return;
    }

    this.clearTimeoutFn(this.reconnectTimer);
    this.reconnectTimer = null;
  }

  private cancelIdleClose(): void {
    if (!this.idleCloseTimer) {
      return;
    }

    this.clearTimeoutFn(this.idleCloseTimer);
    this.idleCloseTimer = null;
  }

  private updateState(next: BridgeConnectionState): void {
    this.state = next;
    this.emitConnectionChange();
  }

  private emitConnectionChange(): void {
    this.connectionListeners.forEach((listener) => listener());
  }

  private parseEvent(raw: unknown): BridgeEvent | null {
    if (typeof raw !== "string") {
      return null;
    }

    try {
      return normalizeBridgeEvent(JSON.parse(raw));
    } catch {
      return null;
    }
  }
}

export const bridgeSocket = new BridgeWebSocketClient();

function deriveSocketUrl(): string {
  const configured = readConfiguredSocketUrl();
  if (!configured) {
    return deriveUrlFromLocation();
  }

  if (shouldUseDevProxy(configured)) {
    return deriveUrlFromLocation();
  }

  return configured;
}

function shouldUseDevProxy(target: string): boolean {
  if (!readImportMetaEnv().DEV || typeof window === "undefined") {
    return false;
  }

  try {
    return new URL(target, window.location.href).origin !== window.location.origin;
  } catch {
    return false;
  }
}

function deriveUrlFromLocation(): string {
  if (typeof window === "undefined") {
    return "ws://127.0.0.1/ws";
  }

  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}/ws`;
}

function readConfiguredSocketUrl(): string {
  return (readImportMetaEnv().VITE_WS_URL as string | undefined) ?? "";
}

function readImportMetaEnv(): Record<string, unknown> {
  return ((import.meta as ImportMeta & { env?: Record<string, unknown> }).env ?? {});
}
