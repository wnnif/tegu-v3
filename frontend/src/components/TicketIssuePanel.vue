<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {api,apiSession,command,SessionChangedError} from '../api'
import {formatCents} from '../printOrder'
import ReferenceSelect from './ReferenceSelect.vue'
import CompanyTicketPrint,{type TicketPrintRow} from './CompanyTicketPrint.vue'

type RowStatus='pending'|'delivered'|'unlinked'
type ReminderStatus='no_work'|'pending'|'delivered'|'mixed'
type BatchStatus='delivered'|'voided'
interface IssueRow extends TicketPrintRow {status:RowStatus;order_version:number;ticket_batch_id?:string}
interface Reminder {project_id:string;project_name:string;address:string;craft_id:string;craft_name:string;subitem_id:string;subitem_name:string;status:ReminderStatus;unmapped?:boolean;total_count:number;pending_count:number;delivered_count:number}
interface TicketBatch {id:string;company_id:string;company_name:string;number:string;ticket_no?:string;delivered_at:string;recipient:string;status:BatchStatus;version:number;total_cents:number;void_reason?:string;voided_by_name?:string;created_by_name?:string}
interface TicketIssueResult {company:{id:string;name:string};rows:IssueRow[];reminders:Reminder[];batches:TicketBatch[];total:number;page:number;page_size:number;batch_total:number;batch_page:number;batch_page_size:number}
interface SnapshotItem extends Partial<IssueRow> {id?:string;name?:string;product_name?:string;qty?:string|number;amount_cents?:number|string;subitem_name?:string}
interface BatchDetail {batch:TicketBatch;items:SnapshotItem[]}

const props=defineProps<{canFinance:boolean;canCustomerPrices:boolean}>()
const emit=defineEmits<{ 'open-order':[id:string] }>()
const allowed=computed(()=>props.canFinance&&props.canCustomerPrices)
const companyId=ref(''),companyName=ref(''),query=ref(''),appliedQuery=ref(''),page=ref(1)
const rows=ref<IssueRow[]>([]),reminders=ref<Reminder[]>([]),batches=ref<TicketBatch[]>([]),total=ref(0),pageSize=ref(100)
const batchPage=ref(1),batchTotal=ref(0),batchPageSize=ref(20)
const selected=ref(new Map<string,IssueRow>()),loading=ref(false),busy=ref(false),error=ref(''),notice=ref('')
const deliveredAt=ref(new Date().toLocaleDateString('sv-SE')),recipient=ref(''),ticketNo=ref('')
const batchDetail=ref<BatchDetail|null>(null),batchLoading=ref(false),batchError=ref(''),voidReason=ref(''),voiding=ref(false)
const printState=ref<{rows:TicketPrintRow[];mode:'company'|'projects';batch?:TicketBatch;companyName:string}|null>(null)
const chosenRows=computed(()=>[...selected.value.values()].filter(row=>row.status==='pending'))
const chosenTotal=computed(()=>chosenRows.value.reduce((sum,row)=>sum+BigInt(row.total_cents||0),0n))
const pages=computed(()=>Math.max(1,Math.ceil(total.value/pageSize.value)))
const batchPages=computed(()=>Math.max(1,Math.ceil(batchTotal.value/batchPageSize.value)))
const selectedKeys=computed(()=>new Set(selected.value.keys()))
const money=(value:number|string|bigint|undefined)=>'¥ '+formatCents(value)
const rowKey=(row:Pick<IssueRow,'item_kind'|'item_id'>)=>row.item_kind+':'+row.item_id
const reminderLabel=(status:ReminderStatus)=>({no_work:'尚无施工记录（当前配置）',pending:'已有施工，尚未交票',delivered:'已交票',mixed:'施工与交票状态混合'}[status])
const statusLabel=(status:RowStatus)=>({pending:'待交票',delivered:'已交票',unlinked:'关联待核对'}[status])
const reminderGroups=computed(()=>{
 const groups=new Map<string,{project_id:string;project_name:string;address:string;rows:Reminder[]}>()
 for(const row of reminders.value){
  const group=groups.get(row.project_id)||{project_id:row.project_id,project_name:row.project_name,address:row.address,rows:[]}
  group.rows.push(row);groups.set(row.project_id,group)
 }
 return [...groups.values()]
})
const params=computed(()=>new URLSearchParams({company_id:companyId.value,page:String(page.value),page_size:'100',q:appliedQuery.value,batch_page:String(batchPage.value),batch_page_size:'20'}))
let submit=command(),generation=0,batchGeneration=0,alive=true

function clearCompany(){++generation;++batchGeneration;loading.value=false;batchLoading.value=false;rows.value=[];reminders.value=[];batches.value=[];total.value=0;page.value=1;batchPage.value=1;batchTotal.value=0;batchPageSize.value=20;query.value='';appliedQuery.value='';selected.value=new Map();companyName.value='';recipient.value='';ticketNo.value='';batchDetail.value=null;printState.value=null;notice.value='';error.value=''}
async function load(){
 if(!allowed.value||!companyId.value)return
 const current=++generation,session=apiSession.capture(),requestedPage=page.value
 loading.value=true;error.value='';notice.value=''
 try{
  const result=await api<TicketIssueResult>('/ticket-issue?'+params.value)
  if(!alive||current!==generation||!apiSession.current(session))return
  companyName.value=result.company?.name||'';rows.value=result.rows;reminders.value=result.reminders;batches.value=result.batches;total.value=result.total;pageSize.value=result.page_size||100;page.value=result.page||requestedPage
  batchTotal.value=result.batch_total??result.batches.length;batchPage.value=result.batch_page||batchPage.value;batchPageSize.value=result.batch_page_size||20
  const next=new Map(selected.value)
  let selectionUpdated=false
  for(const row of result.rows){const key=rowKey(row),previous=next.get(key);if(row.status!=='pending'){if(previous)selectionUpdated=true;next.delete(key)}else if(previous){if(previous.order_version!==row.order_version)selectionUpdated=true;next.set(key,row)}}
  selected.value=next
  if(selectionUpdated){if(printState.value&&!printState.value.batch)printState.value=null;notice.value='已选内容已更新，请重新预览或打印并核对纸票后再确认交票'}
 }catch(e){if(alive&&current===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError)){error.value=(e as Error).message;rows.value=[];reminders.value=[];batches.value=[];total.value=0;batchTotal.value=0}}
 finally{if(alive&&current===generation)loading.value=false}
}
function onCompanyChange(){clearCompany();if(companyId.value)void load()}
function filter(){if(loading.value||busy.value)return;appliedQuery.value=query.value.trim();page.value=1;selected.value=new Map();void load()}
function turn(next:number){if(loading.value||busy.value||next<1||next>pages.value)return;page.value=next;void load()}
function turnBatch(next:number){if(loading.value||busy.value||next<1||next>batchPages.value)return;batchPage.value=next;void load()}
function toggleRow(row:IssueRow,event:Event){if(row.status!=='pending'||busy.value)return;const next=new Map(selected.value),checked=(event.target as HTMLInputElement).checked;if(checked)next.set(rowKey(row),row);else next.delete(rowKey(row));selected.value=next}
function togglePage(event:Event){if(busy.value)return;const next=new Map(selected.value),checked=(event.target as HTMLInputElement).checked;for(const row of rows.value){if(row.status!=='pending')continue;if(checked)next.set(rowKey(row),row);else next.delete(rowKey(row))}selected.value=next}
function isSelected(row:IssueRow){return selectedKeys.value.has(rowKey(row))}
function resetSelection(){selected.value=new Map()}
function preview(mode:'company'|'projects',snapshot?:BatchDetail|null){
 const data=snapshot?.items||chosenRows.value
 const mapped=data.map((raw,index)=>{
  const row=raw as SnapshotItem
  return {item_id:row.item_id||row.id||String(index),item_kind:row.item_kind||'line',order_id:row.order_id||'',work_date:row.work_date||'',order_number:row.order_number||'',project_id:row.project_id||'',project_name:row.project_name||'未命名工地',address:row.address||'',item_name:row.item_name||row.name||row.product_name||'施工项目',subitem_name:row.subitem_name||'',location:row.location||'',quantity:row.quantity??row.qty??'',unit:row.unit||'',total_cents:row.total_cents??row.amount_cents??0,qty_micro:row.qty_micro,price_cents:row.price_cents,category:row.category,region:row.region,segments:row.segments,description:row.description,charge_mode:row.charge_mode,charge_note:row.charge_note,use_included_price:row.use_included_price}
 }) as TicketPrintRow[]
 printState.value={rows:mapped,mode,batch:snapshot?.batch,companyName:snapshot?.batch.company_name||companyName.value}
}
async function confirmDelivered(){
 if(!allowed.value||busy.value||!companyId.value||!chosenRows.value.length)return
 if(!deliveredAt.value||!recipient.value.trim()){error.value='请填写交票日期和收票人';return}
 const session=apiSession.capture(),current=generation;busy.value=true;error.value='';notice.value=''
 try{
  await submit('/ticket-batch',{company_id:companyId.value,items:chosenRows.value.map(row=>({item_id:row.item_id,item_kind:row.item_kind,order_version:row.order_version})),ticket_no:ticketNo.value.trim(),delivered_at:deliveredAt.value,recipient:recipient.value.trim()})
  if(!alive||current!==generation||!apiSession.current(session))return
  notice.value='交票批次已确认，施工与库存记录未重复处理';resetSelection();ticketNo.value='';await load()
 }catch(e){if(alive&&current===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError)){const message=(e as Error).message;error.value=message==='请求失败（409）'?'施工单内容已变化，请刷新后重新预览再确认交票':message}}
 finally{if(alive&&apiSession.current(session))busy.value=false}
}
async function openBatch(batch:TicketBatch){
 const current=++batchGeneration,session=apiSession.capture();batchLoading.value=true;batchError.value='';batchDetail.value=null;voidReason.value='';voiding.value=false
 try{const detail=await api<BatchDetail>('/ticket-batch?id='+encodeURIComponent(batch.id));if(alive&&current===batchGeneration&&apiSession.current(session))batchDetail.value=detail}
 catch(e){if(alive&&current===batchGeneration&&apiSession.current(session)&&!(e instanceof SessionChangedError))batchError.value=(e as Error).message}
 finally{if(alive&&current===batchGeneration)batchLoading.value=false}
}
function closeBatch(){++batchGeneration;batchLoading.value=false;batchDetail.value=null;batchError.value='';voiding.value=false}
async function voidBatch(){
 const detail=batchDetail.value
 if(!allowed.value||!detail||detail.batch.status!=='delivered'||busy.value)return
 if(!voidReason.value.trim()){batchError.value='请填写作废原因';return}
 const session=apiSession.capture(),current=generation;busy.value=true;batchError.value=''
 try{
  await submit('/ticket-batch-action',{id:detail.batch.id,version:detail.batch.version,action:'void',reason:voidReason.value.trim()})
  if(!alive||current!==generation||!apiSession.current(session))return
  batchDetail.value=null;notice.value='交票批次已作废；实际施工、库存、工资和收款记录未被删除';await load()
 }catch(e){if(alive&&current===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError))batchError.value=(e as Error).message}
 finally{if(alive&&apiSession.current(session))busy.value=false}
}
async function refresh(){if(companyId.value)await load()}
watch(allowed,(value)=>{if(!value){++generation;++batchGeneration;clearCompany();busy.value=false;loading.value=false}})
onBeforeUnmount(()=>{alive=false;++generation;++batchGeneration;clearCompany()})
defineExpose({refresh})
</script>

<template>
 <section class="panel ticket-issue-panel">
  <div class="toolbar ticket-issue-toolbar"><ReferenceSelect v-model="companyId" entity="companies" label="公司" empty-label="请选择公司" search-placeholder="搜索公司" :disabled="loading||busy" required @change="onCompanyChange"/><label>明细搜索<input v-model="query" aria-label="明细搜索" placeholder="工地、施工单或项目" :disabled="!companyId||loading||busy" @keyup.enter="filter"></label><button type="button" :disabled="!companyId||loading||busy" @click="filter">查询</button><button type="button" :disabled="!companyId||loading||busy" @click="refresh">刷新</button></div>
  <p v-if="!allowed" class="notice">交票需要“应收款”和“查看客户价格”两项权限。</p>
  <template v-else-if="companyId">
   <div v-if="error" class="error" role="alert">{{error}} <button type="button" :disabled="loading||busy" @click="refresh">重新读取</button></div><p v-if="notice" class="notice" role="status">{{notice}}</p>
   <div class="ticket-issue-summary"><strong>{{companyName||'所选公司'}}</strong><span>本页 {{rows.length}} 条 · 共 {{total}} 条明细（含免费施工）</span><span>已选 {{chosenRows.length}} 条 · {{money(chosenTotal)}}</span></div>
   <section class="ticket-issue-section"><div class="section-heading"><h2>待交票明细</h2><span>按完整施工明细或单独销售商品交票</span></div><div class="table-wrap"><table><thead><tr><th><input type="checkbox" aria-label="选择本页待交票明细" :checked="rows.filter(row=>row.status==='pending').length>0&&rows.filter(row=>row.status==='pending').every(isSelected)" :disabled="loading||busy||!rows.some(row=>row.status==='pending')" @change="togglePage"></th><th>工地 / 日期</th><th>实际内容</th><th>小项 / 实际部位</th><th>数量</th><th>金额</th><th>状态</th></tr></thead><tbody><tr v-for="row in rows" :key="rowKey(row)"><td><input type="checkbox" :checked="isSelected(row)" :disabled="loading||busy||row.status!=='pending'" :aria-label="'选择 '+row.project_name+' '+row.item_name" @change="toggleRow(row,$event)"></td><td>{{row.project_name}}<small>{{row.address}} · {{row.work_date}} · {{row.order_number}}</small></td><td>{{row.item_name}}<small>{{row.item_kind==='sale'?'单独销售商品':'实际施工明细'}}</small></td><td><span>{{row.subitem_name||'—'}}</span><small>实际部位：{{row.location||'—'}}</small></td><td>{{row.quantity}} {{row.unit}}</td><td>{{money(row.total_cents)}}</td><td>{{statusLabel(row.status)}}</td></tr><tr v-if="!rows.length"><td colspan="7" class="empty">{{loading?'正在读取施工明细…':error?'读取失败，请重新读取':'该公司暂无施工或销售明细'}}</td></tr></tbody></table></div><div class="toolbar ticket-issue-pages"><button type="button" :disabled="page<=1||loading||busy" @click="turn(page-1)">上一页</button><span>第 {{page}} / {{pages}} 页</span><button type="button" :disabled="page>=pages||loading||busy" @click="turn(page+1)">下一页</button><button type="button" :disabled="!selected.size||loading||busy" @click="resetSelection">清除已选</button></div></section>
   <section class="ticket-issue-section"><div class="section-heading"><h2>未填写项目提醒</h2><span>提醒不阻止交票</span></div><div v-if="!reminders.length" class="empty ticket-empty">没有需要提醒的公司工艺或部位。</div><div v-for="group in reminderGroups" :key="group.project_id" class="ticket-reminder-project"><h3>{{group.project_name}}<small>{{group.address}}</small></h3><div class="table-wrap"><table><thead><tr><th>公司工艺</th><th>施工部位</th><th>当前状态</th><th>明细数</th></tr></thead><tbody><tr v-for="item in group.rows" :key="item.craft_id+':'+item.subitem_id"><td>{{item.craft_name}}</td><td>{{item.subitem_name||'未设部位'}}</td><td><span class="tag" :class="'reminder-'+item.status">{{reminderLabel(item.status)}}</span><small v-if="item.unmapped" class="reminder-unmapped">配置已变更，已有施工需核对</small><small v-if="item.total_count>0||item.unmapped">待交 {{item.pending_count}} · 已交 {{item.delivered_count}}</small></td><td>{{item.total_count}}</td></tr></tbody></table></div></div></section>
   <section class="ticket-issue-section"><div class="section-heading"><h2>交票批次历史</h2><span>共 {{batchTotal}} 批 · 快照保留当次交票内容</span></div><div class="table-wrap"><table><thead><tr><th>批次 / 票号</th><th>交票日期</th><th>收票人</th><th>状态</th><th>金额</th><th>操作人</th><th></th></tr></thead><tbody><tr v-for="batch in batches" :key="batch.id"><td>{{batch.number}}<small v-if="batch.ticket_no">票号 {{batch.ticket_no}}</small></td><td>{{batch.delivered_at}}</td><td>{{batch.recipient}}</td><td>{{batch.status==='voided'?'已作废':'已交票'}}</td><td>{{money(batch.total_cents)}}</td><td>{{batch.created_by_name||'—'}}<small v-if="batch.voided_by_name">作废：{{batch.voided_by_name}}</small></td><td><button type="button" :disabled="loading||busy" @click="openBatch(batch)">查看快照</button></td></tr><tr v-if="!batches.length"><td colspan="7" class="empty">暂无交票批次</td></tr></tbody></table></div><div class="toolbar ticket-issue-pages"><button type="button" :disabled="batchPage<=1||loading||busy" @click="turnBatch(batchPage-1)">上一页批次</button><span>第 {{batchPage}} / {{batchPages}} 页</span><button type="button" :disabled="batchPage>=batchPages||loading||busy" @click="turnBatch(batchPage+1)">下一页批次</button></div></section>
  <form class="ticket-delivery-form" @submit.prevent="confirmDelivered"><div class="section-heading"><h2>确认纸票已交付</h2><span>预览和打印不会改变交票状态</span></div><div class="form-grid compact"><label>交票日期<input v-model="deliveredAt" type="date" required :disabled="busy"></label><label>收票人<input v-model="recipient" maxlength="100" required :disabled="busy"></label><label>纸票号（可选）<input v-model="ticketNo" maxlength="100" :disabled="busy"></label></div><div class="toolbar ticket-delivery-actions"><span>所选 {{chosenRows.length}} 条 · {{money(chosenTotal)}}</span><button type="button" class="primary" :disabled="!chosenRows.length||loading||busy||!!error" @click="preview('company')">预览公司汇总票</button><button type="button" :disabled="!chosenRows.length||loading||busy||!!error" @click="preview('projects')">预览工地明细</button><button class="primary" :disabled="!allowed||!chosenRows.length||loading||busy||!!error||!deliveredAt||!recipient.trim()">{{busy?'正在确认…':'确认纸票已交付'}}</button></div></form>
  </template>
  <p v-else class="empty ticket-empty">请先选择公司。</p>
 </section>
 <div v-if="batchLoading||batchError||batchDetail" class="overlay ticket-batch-overlay"><section class="modal wide"><div class="modal-head"><h2>交票批次快照</h2><button type="button" :disabled="busy" @click="closeBatch">关闭</button></div><p v-if="batchLoading" class="hint">正在读取批次快照…</p><div v-if="batchError" class="error" role="alert">{{batchError}} <button type="button" @click="batchError=''">关闭提示</button></div><template v-if="batchDetail"><div class="ticket-batch-summary"><strong>{{batchDetail.batch.company_name}}</strong><span>{{batchDetail.batch.number}}<template v-if="batchDetail.batch.ticket_no"> · {{batchDetail.batch.ticket_no}}</template></span><span>{{batchDetail.batch.delivered_at}} · 收票人 {{batchDetail.batch.recipient}}</span><span>{{batchDetail.batch.status==='voided'?'已作废':'已交票'}} · {{money(batchDetail.batch.total_cents)}}</span></div><div class="toolbar"><button type="button" :disabled="!batchDetail.items.length" @click="preview('company',batchDetail)">预览汇总快照</button><button type="button" :disabled="!batchDetail.items.length" @click="preview('projects',batchDetail)">预览工地快照</button><button v-if="batchDetail.batch.status==='delivered'" type="button" :disabled="busy" @click="voiding=!voiding;voidReason='';batchError=''">作废该批次</button></div><div class="table-wrap"><table><thead><tr><th>工地 / 日期</th><th>项目</th><th>小项 / 实际部位</th><th>数量</th><th>金额</th><th>施工单</th></tr></thead><tbody><tr v-for="(item,index) in batchDetail.items" :key="(item.item_kind||'line')+':'+(item.item_id||item.id||index)"><td>{{item.project_name}}<small>{{item.address}} · {{item.work_date}} · {{item.order_number}}</small></td><td>{{item.item_name||item.name||item.product_name}}</td><td><span>{{item.subitem_name||'—'}}</span><small>实际部位：{{item.location||'—'}}</small></td><td>{{item.quantity??item.qty}} {{item.unit}}</td><td>{{money(item.total_cents??item.amount_cents)}}</td><td><button v-if="item.order_id" type="button" :disabled="busy" @click="emit('open-order',item.order_id)">查看施工单</button><span v-else>—</span></td></tr><tr v-if="!batchDetail.items.length"><td colspan="6" class="empty">批次没有快照明细</td></tr></tbody></table></div><form v-if="voiding" class="ticket-void-form" @submit.prevent="voidBatch"><label>作废原因<textarea v-model="voidReason" maxlength="1000" rows="3" required :disabled="busy" placeholder="说明本批次需要作废的原因"></textarea></label><p class="hint">作废只解除本批次的交票关联并保留历史，不会删除施工、库存、工资或已收款记录。修改实际施工量或材料数量后，应按实际差额处理相关记录。</p><div class="toolbar"><button type="button" :disabled="busy" @click="voiding=false;voidReason=''">取消</button><button class="primary" :disabled="busy||!voidReason.trim()">{{busy?'正在作废…':'确认作废批次'}}</button></div></form><p v-if="batchDetail.batch.void_reason" class="notice">作废原因：{{batchDetail.batch.void_reason}} · 操作人：{{batchDetail.batch.voided_by_name||'—'}}</p></template></section></div>
 <CompanyTicketPrint v-if="printState" :company-name="printState.companyName" :rows="printState.rows" :mode="printState.mode" :batch-number="printState.batch?.number" :ticket-no="printState.batch?.ticket_no" :delivered-at="printState.batch?.delivered_at||deliveredAt" :recipient="printState.batch?.recipient||recipient" @close="printState=null" />
</template>

<style scoped>
.ticket-issue-panel{padding-bottom:4px}.ticket-issue-toolbar{align-items:flex-end}.ticket-issue-toolbar .reference-picker{flex:1 1 220px;max-width:380px}.ticket-issue-toolbar>label{flex:1 1 170px;max-width:360px}.ticket-issue-summary{display:flex;flex-wrap:wrap;align-items:center;gap:10px 22px;padding:10px 16px;background:#f7f9fb;border-top:1px solid #edf0f3;border-bottom:1px solid #edf0f3}.ticket-issue-summary strong{font-size:16px;color:#263545}.ticket-issue-summary span{font-size:13px;color:#647386}.ticket-issue-section{padding:14px 16px;border-bottom:1px solid #edf0f3}.section-heading{display:flex;flex-wrap:wrap;align-items:baseline;justify-content:space-between;gap:8px;margin-bottom:10px}.section-heading h2{font-size:16px}.section-heading>span{font-size:12px;color:#758499}.ticket-issue-panel table{min-width:760px}.ticket-issue-panel td small{white-space:normal}.ticket-issue-pages{justify-content:flex-end;padding:10px 0 0}.ticket-issue-pages span{color:#758499}.ticket-reminder-project{margin:10px 0 16px;padding-top:10px;border-top:1px solid #edf0f3}.ticket-reminder-project h3{display:flex;align-items:baseline;gap:8px;margin:0 0 8px}.ticket-reminder-project h3 small{display:inline}.reminder-unmapped{color:#a85d19}.reminder-no_work{background:#fff3df;color:#815313}.reminder-pending{background:#fff3df;color:#815313}.reminder-delivered{background:#eaf4ef;color:#21785a}.reminder-mixed{background:#edf1f5;color:#4d5c6c}.ticket-empty{padding:18px 16px}.ticket-delivery-form{padding:14px 16px}.ticket-delivery-form .form-grid{margin-top:12px}.ticket-delivery-actions{justify-content:flex-end;padding:12px 0 0}.ticket-delivery-actions>span{margin-right:auto;color:#445367;font-weight:600}.ticket-batch-summary{display:flex;flex-wrap:wrap;gap:8px 20px;padding:14px 22px;color:#526174}.ticket-batch-summary strong{color:#25374b}.ticket-void-form{padding:14px 22px}.ticket-void-form label{display:flex;flex-direction:column;gap:6px}.ticket-void-form textarea{width:100%;resize:vertical}.ticket-batch-overlay .modal{max-height:94vh;overflow:auto}.ticket-batch-overlay table{min-width:680px}
@media(max-width:650px){.ticket-issue-section,.ticket-delivery-form{padding:12px}.ticket-issue-summary{gap:7px 12px}.ticket-delivery-actions{justify-content:flex-start}.ticket-delivery-actions>span{width:100%}.ticket-delivery-actions button{flex:1 1 145px}.ticket-batch-summary{flex-direction:column;gap:4px}}
</style>
