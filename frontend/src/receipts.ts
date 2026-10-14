import {cents,parseReceiptAmount,settlementMoney,type CentsValue} from './settlements'
import type {CompanySummary} from './financeSummary'

export interface ReceiptSummary extends CompanySummary {
  delivered_cents?: CentsValue; undelivered_cents?: CentsValue
  allocated_cents?: CentsValue; unallocated_cents?: CentsValue; discount_cents?: CentsValue
  unsettled_cents?: CentsValue; advance_cents?: CentsValue
}
export interface ReceiptPage<T> {rows:T[];total:number;page:number;page_size:number}
export interface CompanyReceipt {
  id:string;company_id:string;company_name:string;amount_cents:CentsValue;received_at:string
  method:string;voucher_no:string;note:string;status:'active'|'voided';version:number
  created_by_name:string;created_at:string;updated_at:string;void_reason:string
  allocated_cents:CentsValue;unallocated_cents:CentsValue
}
export interface ReceiptAllocation {
  id:string;receipt_id:string;ticket_id:string;batch_id:string;project_id:string
  address:string;number:string;batch_number:string;amount_cents:CentsValue;voided:boolean;created_at:string
}
export interface FinanceHistory {version:number;event:string;reason:string;actor_name:string;created_at:string;snapshot:unknown}
export interface FinanceHistoryPage {history:FinanceHistory[];history_total:number;history_page:number;history_page_size:number}
export interface ReceiptDetail extends FinanceHistoryPage {receipt:CompanyReceipt;allocations:ReceiptAllocation[]}
export interface ReceiptTarget {
  id:string;project_id:string;company_id:string;number:string;address:string
  total_cents:CentsValue;allocated_cents:CentsValue;discount_cents:CentsValue;outstanding_cents:CentsValue
  batches:{id:string;number:string;total_cents:CentsValue;available_cents:CentsValue}[]
}
export interface CustomerDiscount {
  id:string;company_id:string;ticket_id:string;batch_id:string;address:string;number:string;batch_number:string
  amount_cents:CentsValue;reason:string;version:number;voided:boolean|number;created_by_name:string;created_at:string;void_reason:string
}
export interface DiscountDetail extends FinanceHistoryPage {discount:CustomerDiscount}
export interface AllocationDraft {key:string;ticket_id:string;batch_id:string;address:string;number:string;batch_number:string;amount:string}
export const receiptMoney=settlementMoney
export function amountInput(value:CentsValue):string {
  const amount=cents(value),absolute=amount<0n?-amount:amount
  return (amount<0n?'-':'')+String(absolute/100n)+'.'+String(absolute%100n).padStart(2,'0')
}
export function allocationDrafts(detail:ReceiptDetail):AllocationDraft[] {
  return detail.allocations.map((row,index)=>({key:row.id||'saved-'+index,ticket_id:row.ticket_id,batch_id:row.batch_id||'',address:row.address,number:row.number,batch_number:row.batch_number||'',amount:amountInput(row.amount_cents)}))
}
export function allocationPayload(rows:AllocationDraft[],available:CentsValue) {
  if(rows.length>100)throw new Error('一笔收款最多分配到 100 个位置，请减少分配行')
  const seen=new Set<string>();let total=0n
  const allocations=rows.map(row=>{
    if(!row.ticket_id)throw new Error('请选择分配的工地收费')
    const key=row.ticket_id+'\u0000'+row.batch_id
    if(seen.has(key))throw new Error('同一工地收费和交票批次请合为一行')
    seen.add(key)
    const amount=parseReceiptAmount(String(row.amount));total+=BigInt(amount)
    return {ticket_id:row.ticket_id,batch_id:row.batch_id||'',amount_cents:amount}
  })
  if(total>cents(available))throw new Error('分配合计不能超过这笔实际收款金额')
  return allocations
}
export function allocationTotal(rows:AllocationDraft[]):bigint|null {
  try{return rows.reduce((total,row)=>total+BigInt(parseReceiptAmount(String(row.amount))),0n)}catch{return null}
}
export function financeEvent(event:string):string {
  const events:Record<string,string>={create:'登记收款',correct:'更正收款',void:'作废',allocate:'调整收款分配',allocation:'调整收款分配',reconcile:'收费变动后调整',batch_void:'交票作废后解除批次关联',discount:'客户减免',migrate:'历史收款补齐'}
  return events[event]||event
}
export function financeHistoryAmounts(snapshot:unknown):string {
  if(!snapshot||typeof snapshot!=='object')return ''
  const value=snapshot as Record<string,unknown>
  const receipt=value.receipt&&typeof value.receipt==='object'?value.receipt as Record<string,unknown>:null
  if(receipt){
    const allocations=Array.isArray(value.allocations)?value.allocations:[]
    const allocated=allocations.reduce((sum,row)=>sum+cents(row&&typeof row==='object'?(row as Record<string,unknown>).amount_cents:undefined),0n)
    return [String(receipt.company_name||''),'当时登记 '+receiptMoney(receipt.amount_cents),'当时分配 '+receiptMoney(allocated),'当时未分配 '+receiptMoney(cents(receipt.amount_cents)-allocated)].filter(Boolean).join(' · ')
  }
  return value.amount_cents!==undefined?'当时减免 '+receiptMoney(value.amount_cents):''
}
export const summaryAmounts=[['total_cents','施工及商品收费'],['delivered_cents','已交票金额'],['undelivered_cents','尚未交票金额'],['received_cents','实际到账'],['allocated_cents','已分配收款'],['unallocated_cents','未分配收款'],['discount_cents','客户确认减免'],['unsettled_cents','工地尚未核销'],['outstanding_cents','公司实际仍需收回'],['advance_cents','公司预收余额']] as const
