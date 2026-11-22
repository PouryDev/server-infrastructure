import type { BuildInfo, EnvChange, EnvChangeSummary, PreservedLimit } from "./env-types";
export type ShipTarget = {
  revision: number;
  mode: "local" | "ssh";
  host: string;
  port: number;
  user: string;
  identity: string;
  infraDir: string;
  appsDir: string;
  dataDir: string;
};
export type LocalImage = {
  id: string;
  ref: string;
  size: number;
  platform: string;
};
export type ShipCatalog = {
  projects: { name: string; services: string[]; error?: string }[];
  images: LocalImage[];
  identities: string[];
};
export type ShipRequest = {
  deployment?: { domain: string; service: string; port: number; migrationService: string; revision: string };
  project: string;
  service: string;
  image: string;
  mode: "deploy" | "image" | "build" | "apply";
  includeEnv: boolean;
  source?: string;
  useComposeLimits?: boolean;
  env?: EnvChange[];
};
export type ShipPlan = {
  id: string;
  createdAt: string;
  target: ShipTarget;
  request: ShipRequest;
  project: string;
  directory: string;
  services: string[];
  images: (LocalImage & { destinationRef: string })[];
  files: string[];
  warnings: string[];
  fingerprint: string;
  build?: BuildInfo;
  envChanges: EnvChangeSummary[];
  preservedLimits: PreservedLimit[];
  affected: string[];
};
export type ShipJob = {
  id: string;
  planId: string;
  actor: string;
  project: string;
  status: string;
  phase: string;
  message: string;
  createdAt: string;
  updatedAt: string;
  steps?: { phase: string; status: "pending" | "running" | "complete" | "failed" }[];
  progress?: { current: number; total?: number; unit: string };
};
