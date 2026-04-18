import type { BridgeEvent, BridgeEventMap, BridgeEventName } from "../types/ws";
import { normalizeBridgeEvent } from "./bridge-contract";

export interface WebSocketClientOptions {
  url?: string;
  protocols?: string | string[];
}

type EventListener<T extends BridgeEventName> = (event: BridgeEventMap[T]) => void;
type AnyEventListener = (event: BridgeEvent) => void;

export class BridgeWebSocketClient {
  private readonly url: string;
  private readonly protocols?: string | string[];
  private socket: WebSocket | null = null;
  private readonly listeners = new Map<BridgeEventName, Set<EventListener<BridgeEventName>>>();
  private readonly anyListeners = new Set<AnyEventListener>();

  constructor(options: WebSocketClientOptions = {}) {
    this.url = options.url ?? deriveSocketUrl();
    this.protocols = options.protocols;
  }

  connect(): void {
    if (this.socket && this.socket.readyState <= WebSocket.OPEN) {
      return;
    }

    this.socket = new WebSocket(this.url, this.protocols);
    this.socket.addEventListener("message", (event) => {
      const parsed = this.parseEvent(event.data);
      if (!parsed) {
        return;
      }

      this.listeners.get(parsed.type)?.forEach((listener) => listener(parsed as never));
      this.anyListeners.forEach((listener) => listener(parsed));
    });
  }

  disconnect(): void {
    this.socket?.close();
    this.socket = null;
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

  private parseEvent(raw: string): BridgeEvent | null {
    try {
      return normalizeBridgeEvent(JSON.parse(raw));
    } catch {
      return null;
    }
  }

  private deriveUrlFromLocation(): string {
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    return `${protocol}//${window.location.host}/ws`;
  }
}

export const bridgeSocket = new BridgeWebSocketClient();

function deriveSocketUrl(): string {
  const configured = (import.meta.env.VITE_WS_URL as string | undefined) ?? "";
  if (!configured) {
    return deriveUrlFromLocation();
  }

  if (shouldUseDevProxy(configured)) {
    return deriveUrlFromLocation();
  }

  return configured;
}

function shouldUseDevProxy(target: string): boolean {
  if (!import.meta.env.DEV || typeof window === "undefined") {
    return false;
  }

  try {
    return new URL(target, window.location.href).origin !== window.location.origin;
  } catch {
    return false;
  }
}

function deriveUrlFromLocation(): string {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}/ws`;
}
