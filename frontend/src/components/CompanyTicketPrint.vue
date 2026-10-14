<script setup lang="ts">
import {computed} from 'vue'
import {formatCents,formatQuantity,printChargeDescription,savedLineDimensions,type PrintSegment} from '../printOrder'
import {measureUnit} from '../orderSegments'

export interface TicketPrintRow {
 item_id:string
 item_kind:'line'|'sale'
 order_id:string
 work_date:string
 order_number:string
 project_id:string
 project_name:string
 address:string
 item_name:string
 subitem_name?:string
 location:string
 quantity:string|number
 qty_micro?:string|number
 unit:string
 category?:string
 region?:string
 segments?:string|PrintSegment[]
 description?:string
 charge_mode?:string
 charge_note?:string
 use_included_price?:boolean
 price_cents?:number|string
 total_cents:number|string
}

const props=defineProps<{
 companyName:string
 rows:TicketPrintRow[]
 mode:'company'|'projects'
 batchNumber?:string
 ticketNo?:string
 deliveredAt?:string
 recipient?:string
}>()
const emit=defineEmits<{close:[]}>()
const money=(value:number|string|bigint|undefined)=>'¥ '+formatCents(value)
const total=computed(()=>props.rows.reduce((sum,row)=>sum+BigInt(row.total_cents||0),0n))
const note=(row:TicketPrintRow)=>printChargeDescription(row)
const quantity=(row:TicketPrintRow)=>row.qty_micro == null
 ? `${row.quantity} ${row.unit}`
 : row.item_kind==='line'&&row.unit==='金额'?'1 项':`${formatQuantity(row.qty_micro)} ${row.item_kind==='line'?measureUnit(row.unit):row.unit}`
function dimensions(row:TicketPrintRow):string[] {
 if(row.item_kind==='sale')return []
 let segments:PrintSegment[]=[]
 try { const value=typeof row.segments==='string'?JSON.parse(row.segments):row.segments;if(Array.isArray(value))segments=value }catch{return ['方量明细未记录']}
 return savedLineDimensions({mode:row.unit,segments})
}
const projects=computed(()=>{
 const groups=new Map<string,{id:string;name:string;address:string;rows:TicketPrintRow[];total:bigint}>()
 for(const row of props.rows){
  const key=row.project_id||'unrecorded:'+row.order_id
  const group=groups.get(key)||{id:key,name:row.project_name||'未记录工地',address:row.address||'',rows:[],total:0n}
  group.rows.push(row);group.total+=BigInt(row.total_cents||0);groups.set(key,group)
 }
 return [...groups.values()]
})
function print(){window.print()}
</script>

<template>
 <Teleport to="body">
 <div class="overlay print-overlay ticket-print-overlay">
  <section class="modal wide ticket-print-modal">
   <div class="modal-head no-print"><h2>{{mode==='company'?'公司汇总票':'工地施工明细'}}</h2><div class="toolbar"><button type="button" @click="print">打印</button><button type="button" @click="emit('close')">关闭</button></div></div>
   <article v-if="mode==='company'" class="ticket-document">
    <header class="ticket-document-title"><h1>{{companyName}}施工收费汇总</h1><p v-if="batchNumber">交票批次 {{batchNumber}}<span v-if="ticketNo"> · 票号 {{ticketNo}}</span></p><p v-else>交票预览</p></header>
    <div class="ticket-document-meta"><span>交票日期：{{deliveredAt||'待填写'}}</span><span>收票人：{{recipient||'待填写'}}</span><strong>合计：{{money(total)}}</strong></div>
    <div class="table-wrap"><table><thead><tr><th>工地 / 地址</th><th>施工日期</th><th>项目</th><th>小项 / 实际部位</th><th>数量</th><th>金额</th></tr></thead><tbody>
     <tr v-for="(row,index) in rows" :key="row.item_kind+':'+row.item_id+':'+index"><td>{{row.project_name}}<small>{{row.address}}</small></td><td>{{row.work_date}}</td><td>{{row.item_name}}{{row.item_kind==='sale'?'（销售）':''}}<small v-if="note(row)">{{note(row)}}</small></td><td><span>{{row.subitem_name||'—'}}</span><small>实际部位：{{row.location||'—'}}</small><small v-if="row.region">原部位：{{row.region}}</small></td><td>{{quantity(row)}}<small v-for="(dimension,i) in dimensions(row)" :key="i">{{dimension}}</small><small>{{row.price_cents==null?'原单价未记录':'单价 '+money(row.price_cents)+' / '+(row.item_kind==='line'?measureUnit(row.unit):row.unit)}}</small></td><td>{{money(row.total_cents)}}</td></tr>
     <tr v-if="!rows.length"><td colspan="6" class="empty">没有可打印的明细</td></tr>
    </tbody></table></div>
    <footer class="ticket-document-total"><span>本批次合计</span><strong>{{money(total)}}</strong></footer>
   </article>
   <div v-else class="ticket-project-documents">
    <article v-for="(project,index) in projects" :key="project.id" class="ticket-document" :class="{'ticket-page-break':index>0}">
     <header class="ticket-document-title"><h1>{{companyName}} · {{project.name}}</h1><p>{{project.address}}</p><p v-if="batchNumber">交票批次 {{batchNumber}}<span v-if="ticketNo"> · 票号 {{ticketNo}}</span></p></header>
     <div class="ticket-document-meta"><span>交票日期：{{deliveredAt||'待填写'}}</span><span>收票人：{{recipient||'待填写'}}</span><strong>工地合计：{{money(project.total)}}</strong></div>
     <div class="table-wrap"><table><thead><tr><th>施工日期</th><th>施工单号</th><th>项目</th><th>小项 / 实际部位</th><th>数量</th><th>金额</th></tr></thead><tbody>
      <tr v-for="(row,rowIndex) in project.rows" :key="row.item_kind+':'+row.item_id+':'+rowIndex"><td>{{row.work_date}}</td><td>{{row.order_number}}</td><td>{{row.item_name}}{{row.item_kind==='sale'?'（销售）':''}}<small v-if="note(row)">{{note(row)}}</small></td><td><span>{{row.subitem_name||'—'}}</span><small>实际部位：{{row.location||'—'}}</small><small v-if="row.region">原部位：{{row.region}}</small></td><td>{{quantity(row)}}<small v-for="(dimension,i) in dimensions(row)" :key="i">{{dimension}}</small><small>{{row.price_cents==null?'原单价未记录':'单价 '+money(row.price_cents)+' / '+(row.item_kind==='line'?measureUnit(row.unit):row.unit)}}</small></td><td>{{money(row.total_cents)}}</td></tr>
     </tbody></table></div>
     <footer class="ticket-document-total"><span>工地合计</span><strong>{{money(project.total)}}</strong></footer>
    </article>
    <p v-if="!projects.length" class="empty">没有可打印的明细</p>
   </div>
   <p class="ticket-print-grand-total">公司汇总金额：{{money(total)}}</p>
  </section>
 </div>
 </Teleport>
</template>

<style scoped>
.ticket-print-modal{max-height:94vh;overflow:auto}.ticket-print-modal>.modal-head{gap:12px}.ticket-print-modal>.modal-head .toolbar{padding:0}.ticket-document{padding:22px 26px;color:#202a35}.ticket-document-title{text-align:center;margin-bottom:18px}.ticket-document-title h1{font-size:22px;margin:0 0 6px}.ticket-document-title p{color:#586576;margin-top:4px}.ticket-document-meta{display:flex;flex-wrap:wrap;justify-content:space-between;gap:10px;padding:10px 0;border-top:1px solid #bfc7d0;border-bottom:1px solid #bfc7d0;margin-bottom:12px}.ticket-document .table-wrap{max-width:100%;overflow-x:auto}.ticket-document table{width:100%;min-width:720px;white-space:normal}.ticket-document td,.ticket-document th{padding:8px}.ticket-document td{overflow-wrap:anywhere}.ticket-document-total{display:flex;justify-content:flex-end;gap:24px;padding:14px 8px;border-bottom:1px solid #bfc7d0}.ticket-print-grand-total{text-align:right;font-weight:600;padding:0 26px 22px}.ticket-page-break{border-top:1px dashed #9aa5b2;margin-top:20px;padding-top:28px}
@media print{.ticket-print-overlay{position:static!important;display:block!important;background:white!important;padding:0!important}.ticket-print-modal{width:100%!important;max-width:none!important;max-height:none!important;overflow:visible!important;border:0!important}.ticket-document{padding:0 0 12mm;break-inside:avoid}.ticket-page-break{break-before:page;border:0;margin:0;padding:0}.ticket-print-grand-total{padding:8px 0}.ticket-print-modal .table-wrap,.ticket-document .table-wrap{max-width:none;overflow:visible!important}.ticket-document table{width:100%;min-width:0!important}}
@media(max-width:650px){.ticket-document{padding:16px 12px}.ticket-document-meta{flex-direction:column}.ticket-document-title h1{font-size:18px}}
</style>
