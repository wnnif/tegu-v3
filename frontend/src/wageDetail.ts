import type {Numeric,Flag} from './workflows/models'
import type {PrintSegment} from './printOrder'

export interface WageDetailLine {
  id:string; category:string; name:string; spec:string; region:string; description:string
  mode:string; subitem_id:string; sort_order:number; worker_id:string;worker_name:string;qty_micro:Numeric
  wage_cents?:Numeric; labor_cents?:Numeric; wage_configured:boolean; wage_override_cents?:Numeric; segments:PrintSegment[]
}
export interface WageDetail {
  id:string; order_id:string; number:string; company_id:string; company_name:string
  project_id:string; project_name:string; address:string; work_date:string
  worker_id:string; worker_name:string; status:string; wage_cents?:Numeric; wage_configured:boolean; wage_paid:Flag
  version:number; delivery_wage_cents:Numeric; legacy:boolean; handles_delivery:boolean
  lines:WageDetailLine[]
}
