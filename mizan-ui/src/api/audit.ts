import { api } from './http'

export interface AuditRow { id: number; at: string; actor: string; action: string; entity_type: string; entity_id: string; details: unknown }

export const auditApi = {
  trail: (type: string, id: string) => api.get<AuditRow[]>(`/v1/audit/${encodeURIComponent(type)}/${encodeURIComponent(id)}`),
}
