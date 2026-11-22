import { api, money } from './http'
import { listQuery, type ListParams, type Page } from './page'

export interface Employee { id: string; code: string; name: string; national_id?: string; insurance_no?: string; active: boolean }
export interface Allowance { code: string; name: string; amount: number; taxable: boolean; insurable: boolean; wage_base?: boolean }
export interface Contract { id: string; employee_id: string; base_salary: number; allowances: Allowance[]; insured: boolean; start: string; start_jalali: string; end?: string; end_jalali?: string }
export interface Breakdown { days: number; month_days: number; lines: { code: string; name: string; amount: number }[]; gross: number; insurable: number; employee_insurance: number; employer_insurance: number; unemployment: number; taxable_income: number; tax: number; deduction_total: number; net: number }
export interface Payslip extends Partial<Breakdown> { id: string; employee_id: string; year: number; month: number; status: 'issued' | 'paid' | 'void' | string; rule_version_id: string; run_id?: string; void_reason?: string; gross: number; tax: number; net: number }
export interface Bracket { up_to: number; rate_bp: number }
export interface RuleVersion { id: string; name: string; effective_from: string; effective_from_jalali: string; employee_bp: number; employer_bp: number; unemployment_bp: number; ceiling_daily: number; floor_daily: number; deduct_employee_insurance: boolean; brackets: Bracket[]; terms: Record<string, unknown>; source?: string }
export interface Loan { id: string; employee_id: string; principal: number; installment: number; remaining: number; start_year: number; start_month: number; status: string; note?: string; written_off: number }
export interface Remittance { id: string; kind: 'tax' | 'insurance'; amount: number; period_year?: number; period_month?: number; pay_from: string; reference?: string; created_at: string }
export interface Holiday { jalali_year: number; jalali_month: number; jalali_day: number; name: string; kind?: string; verified: boolean; source: string }

export interface PayslipRequest { employee_id: string; year: number; month: number; days?: number; leave_days?: number; severance?: boolean; bonus_days?: number }

export const payroll = {
  employees: (p?: ListParams) => api.get<Page<Employee> | Employee[]>('/v1/payroll/employees', listQuery(p)),
  addEmployee: (b: { code: string; name: string; national_id?: string; insurance_no?: string }) => api.post<Employee>('/v1/payroll/employees', b),
  contracts: (id: string) => api.get<Contract[] | { items: Contract[] }>(`/v1/payroll/employees/${id}/contracts`),
  addContract: (id: string, b: { base_salary: number; allowances: Allowance[]; insured: boolean; start: string; end?: string }) => api.post<Contract>(`/v1/payroll/employees/${id}/contracts`, { ...b, base_salary: money(b.base_salary), allowances: b.allowances.map((a) => ({ ...a, amount: money(a.amount) })) }),
  endContract: (id: string, end: string) => api.post<Contract>(`/v1/payroll/contracts/${id}/end`, { end }),
  profile: (id: string) => api.get<Record<string, string>>(`/v1/payroll/employees/${id}/profile`),
  putProfile: (id: string, p: Record<string, string>) => api.put<Record<string, string>>(`/v1/payroll/employees/${id}/profile`, p),
  rules: () => api.get<RuleVersion[] | { items: RuleVersion[] }>('/v1/payroll/rule-versions'),
  addRule: (b: Omit<RuleVersion, 'id' | 'effective_from_jalali'> & { currency?: string }) => api.post<RuleVersion>('/v1/payroll/rule-versions', b),
  deleteRule: (id: string) => api.del<void>(`/v1/payroll/rule-versions/${id}`),
  payslips: (p?: ListParams, signal?: AbortSignal) => api.get<Page<Payslip>>('/v1/payroll/payslips', listQuery(p), signal),
  payslip: (id: string) => api.get<Payslip>(`/v1/payroll/payslips/${id}`),
  preview: (b: PayslipRequest) => api.post<Breakdown>('/v1/payroll/payslips/preview', b),
  issue: (b: PayslipRequest) => api.post<Payslip>('/v1/payroll/payslips', b),
  void: (id: string, reason: string) => api.post<Payslip>(`/v1/payroll/payslips/${id}/void`, { reason }),
  run: (b: { year: number; month: number; payslip_ids?: string[]; bank_account_id?: string; date?: string }, key: string) => api.post<unknown>('/v1/payroll/runs', b, key),
  loans: (id: string) => api.get<Loan[] | { items: Loan[] }>(`/v1/payroll/employees/${id}/loans`),
  grantLoan: (id: string, b: { principal: number; installment: number; start_year: number; start_month: number; bank_account_id?: string; note?: string }) => api.post<Loan>(`/v1/payroll/employees/${id}/loans`, { ...b, principal: money(b.principal), installment: money(b.installment) }),
  writeOff: (id: string, reason: string) => api.post<Loan>(`/v1/payroll/loans/${id}/write-off`, { reason }),
  holidays: (year: number) => api.get<Holiday[] | { items: Holiday[] }>('/v1/payroll/holidays', { jalali_year: year }),
  addHoliday: (h: Holiday) => api.post<Holiday>('/v1/payroll/holidays', h),
  remittances: () => api.get<Remittance[] | { items: Remittance[] }>('/v1/payroll/remittances'),
  remit: (b: { kind: 'tax' | 'insurance'; amount: number; period_year: number; period_month: number; bank_account_id?: string; reference?: string; date?: string }, key: string) => api.post<Remittance>('/v1/payroll/remittances', { ...b, amount: money(b.amount) }, key),
  ssaFiles: (year: number, month: number) => api.get<Record<string, string>>('/v1/payroll/ssa-files', { year, month }),
  workshop: () => api.get<Record<string, unknown>>('/v1/payroll/workshop'),
  putWorkshop: (w: Record<string, unknown>) => api.put<Record<string, unknown>>('/v1/payroll/workshop', w),
}
