export type ServiceStatus = "ok" | "degraded" | "error";

export interface ServiceHealth {
  name: string;
  status: ServiceStatus;
  detail: string;
  latencyMs?: number;
}

export interface HealthStatus {
  status: ServiceStatus;
  version: string;
  checkedAt: string;
  activeSessions: number;
  services: ServiceHealth[];
}
