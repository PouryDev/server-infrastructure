export type EnvVariable = {
  name: string;
  masked: boolean;
  sensitive: boolean;
  empty: boolean;
};
export type EnvFile = {
  scope: string;
  exists: boolean;
  variables: EnvVariable[];
};
export type EnvServiceInfo = {
  name: string;
  oneShot: boolean;
  usesProjectEnv: boolean;
  hasOverride: boolean;
};
export type EnvState = {
  project: string;
  revision: string;
  services: EnvServiceInfo[];
  files: EnvFile[];
};
export type EnvProject = { name: string; services: string[] };
export type EnvChange = {
  scope: string;
  name: string;
  action: "set" | "delete";
  value?: string;
};
export type EnvChangeSummary = {
  scope: string;
  name: string;
  action: "set" | "delete";
  exists: boolean;
};
export type BuildSource = {
  id: string;
  subdir: string;
  app: string;
  composeFile: string;
  platform: string;
  keepTags: number;
  images: { service: string; context: string; dockerfile: string }[];
  buildArgNames: string[];
  services: string[];
  error?: string;
};
export type PreservedLimit = {
  service: string;
  memoryBytes: number;
  nanoCpus: number;
  memSwapBytes: number;
  composeMemoryBytes: number;
  composeNanoCpus: number;
};
export type BuildInfo = {
  source: string;
  app: string;
  tag: string;
  head: string;
  dirty: boolean;
  platform: string;
  images: { service: string; context: string; dockerfile: string }[];
  buildArgNames: string[];
  unbuilt: string[];
};
export const envNamePattern = /^[A-Z_][A-Z0-9_]*$/;
