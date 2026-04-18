import type { HealthStatus } from "../types/health";
import type { ProviderSettings } from "../types/providers";
import type { SessionDetail, SessionSummary } from "../types/session";
import {
  normalizeHealthResponse,
  normalizeProviderSettings,
  normalizeSessionDetail,
  normalizeSessionListResponse,
  serializeProviderSettings,
} from "./bridge-contract";

export interface ApiClientOptions {
  baseUrl?: string;
  fetcher?: typeof fetch;
}

export interface DashboardSnapshot {
  health: HealthStatus;
  providerSettings: ProviderSettings;
  sessions: SessionSummary[];
}

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = "ApiError";
  }
}

export class BridgeApiClient {
  private readonly baseUrl: string;
  private readonly fetcher: typeof fetch;

  constructor(options: ApiClientOptions = {}) {
    this.baseUrl = options.baseUrl ?? deriveApiBaseUrl();
    this.fetcher = options.fetcher ?? ((input, init) => globalThis.fetch(input, init));
  }

  async getHealth(): Promise<HealthStatus> {
    const raw = await this.request("/api/health");
    return normalizeHealthResponse(raw);
  }

  async getProviderSettings(): Promise<ProviderSettings> {
    const raw = await this.request("/api/health");
    return normalizeProviderSettings(raw.providers);
  }

  async getDashboardSnapshot(): Promise<DashboardSnapshot> {
    const [healthRaw, sessionsRaw] = await Promise.all([
      this.request("/api/health"),
      this.request("/api/sessions"),
    ]);

    return {
      health: normalizeHealthResponse(healthRaw),
      providerSettings: normalizeProviderSettings(healthRaw.providers),
      sessions: normalizeSessionListResponse(sessionsRaw),
    };
  }

  async getSessions(): Promise<SessionSummary[]> {
    const raw = await this.request("/api/sessions");
    return normalizeSessionListResponse(raw);
  }

  async getSession(id: string): Promise<SessionDetail> {
    const raw = await this.request(`/api/sessions/${id}`);
    return normalizeSessionDetail(raw);
  }

  async saveProviderSettings(settings: ProviderSettings): Promise<ProviderSettings> {
    const raw = await this.request("/api/settings/providers", {
      method: "POST",
      body: JSON.stringify(serializeProviderSettings(settings)),
    });
    return normalizeProviderSettings(raw);
  }

  async interruptSession(id: string): Promise<void> {
    await this.request(`/api/sessions/${id}/interrupt`, {
      method: "POST",
    });
  }

  private async request(path: string, init?: RequestInit): Promise<any> {
    const response = await this.fetcher(`${this.baseUrl}${path}`, {
      headers: {
        "Content-Type": "application/json",
        ...(init?.headers ?? {}),
      },
      ...init,
    });

    if (!response.ok) {
      const message = await this.extractErrorMessage(response);
      throw new ApiError(response.status, message);
    }

    if (response.status === 204) {
      return undefined;
    }

    return await response.json();
  }

  private async extractErrorMessage(response: Response): Promise<string> {
    try {
      const data = (await response.json()) as { message?: string; error?: string };
      return data.message ?? data.error ?? response.statusText;
    } catch {
      return response.statusText || "Unknown API error";
    }
  }
}

export const apiClient = new BridgeApiClient();

function deriveApiBaseUrl(): string {
  const configured = (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? "";
  if (!configured) {
    return "";
  }

  if (shouldUseDevProxy(configured)) {
    return "";
  }

  return configured;
}

function shouldUseDevProxy(target: string): boolean {
  if (!import.meta.env.DEV || typeof window === "undefined") {
    return false;
  }

  try {
    return new URL(target, window.location.origin).origin !== window.location.origin;
  } catch {
    return false;
  }
}
