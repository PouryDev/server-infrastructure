export type ResourceRequest = {
  project: string;
  service: string;
  memoryMiB: number;
  cpus: number;
  gitMode: "local" | "commit" | "push";
  revision: string;
};
export type ResourcePreview = {
  project: string;
  service: string;
  file: string;
  revision: string;
  branch: string;
  gitReady: boolean;
  pushEnabled: boolean;
  containers: { id: string; memoryMiB: number; cpus: number }[];
  changes: string[];
};
export type ResourceResult = {
  applied: boolean;
  commit: string;
  pushed: boolean;
  warning: string;
};
