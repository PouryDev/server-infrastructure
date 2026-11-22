export type Container = {
  id: string;
  name: string;
  project: string;
  service: string;
  state: string;
  health: string;
  managed: boolean;
  cpu: number | null;
  memory: number | null;
  memoryLimit: number | null;
  networkRX: number | null;
  networkTX: number | null;
  error?: string;
};
export type Policy = {
  project: string;
  suspended: boolean;
  expiresAt: string | null;
  revision: number;
};
export type Metric = {
  project: string;
  name: string;
  value: number;
  unit: string;
};
export type Project = {
  name: string;
  status: string;
  policy: Policy;
  containers: Container[];
  metrics: Metric[];
};
export type Snapshot = {
  projects: Project[];
  infrastructure: Container[];
  syncedAt: string | null;
  metricsAt: string | null;
  error: string;
  metricsError: string;
  syncIntervalSeconds: number;
};
export type AuditEvent = {
  id: number;
  at: string;
  actor: string;
  project: string;
  action: string;
  outcome: string;
  detail: string;
};
export type Session = { user: string; csrf: string };

export type Page =
  | "vpn"
  | "projects"
  | "infrastructure"
  | "audit"
  | "shipping"
  | "buildship"
  | "environment"
  | "storefront"
  | "deployments";
export type Change = {
  project: Project;
  action: "expiration" | "renew" | "suspend";
};
