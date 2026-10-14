export interface WorkerSummary {
  worker_id: string; worker_name: string; order_count: number
  wage_cents: number; paid_cents: number; unpaid_cents: number
}
export interface WageSummaryPage {
  rows: WorkerSummary[]; total: number; page: number; page_size: number
  wage_cents: number; paid_cents: number; unpaid_cents: number
}
export interface CompanySummary {
  company_id: string; company_name: string; ticket_count: number
  total_cents?: number|string; received_cents?: number|string; outstanding_cents?: number|string
}
export interface CompanySummaryPage {
  rows: CompanySummary[]; total: number; page: number; page_size: number
  total_cents?: number|string; received_cents?: number|string; outstanding_cents?: number|string
}
