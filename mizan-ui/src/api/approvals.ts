import { api } from './http'

export interface Approval {
  id: string
  kind: 'year_close' | 'appropriation'
  subject: string
  status: 'pending' | 'approved' | 'rejected'
  requested_by: string
  payload: unknown
  created_at: string
  decided_by?: string
  decided_at?: string
  reason?: string
  result?: unknown
}

export const approvals = {
  list: (status?: string) => api.get<{ items: Approval[] }>('/v1/approvals', status ? { status } : undefined),
}
