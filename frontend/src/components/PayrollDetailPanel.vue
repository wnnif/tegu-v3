<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref,watch} from 'vue'
import {api,apiSession,SessionChangedError} from '../api'
import {maxAdvanceAllocation,payrollAdjustmentCanReverse,payrollCents,payrollExportUrl,payrollMoney,payrollSettlementBase,payrollSettlementCanConfirm,payrollSettlementCanReverse,payrollSettlementScopeCompatible,payrollSnapshotText,payrollSourceSelectable,type PayrollActionDraft,type PayrollAdjustmentRow,type PayrollAdvanceRow,type PayrollDeductionRow,type PayrollHistoryPage,type PayrollHistoryRow,type PayrollPage,type PayrollPaymentRow,type PayrollProjectOption,type PayrollProjectPage,type PayrollRow,type PayrollSettlementRow,type PayrollSettlementSelection,type PayrollTab,type PayrollWageRow,type PayrollWorkerSummary} from '../payroll'
import PayrollActionDialog from './PayrollActionDialog.vue'

const props=defineProps<{worker:PayrollWorkerSummary}>()
const emit=defineEmits<{back:[];changed:[]}>()
type Action=PayrollActionDraft['action']
const tabs:{id:PayrollTab;label:string}[]=[{id:'wages',label:'施工工资'},{id:'adjustments',label:'工资调整'},{id:'deductions',label:'扣款'},{id:'advances',label:'工资预支'},{id:'settlements',label:'结算与发放'},{id:'history',label:'操作历史'}]
const tab=ref<PayrollTab>('wages'),from=ref(''),to=ref(''),projectQuery=ref(''),projectId=ref(''),projects=ref<PayrollProjectOption[]>([]),selectedProject=ref<PayrollProjectOption|null>(null),projectsOpen=ref(false),page=ref(1),pageSize=ref(100),rows=ref<PayrollRow[]>([]),historyRows=ref<PayrollHistoryRow[]>([]),total=ref(0),loading=ref(false),projectLoading=ref(false),error=ref(''),notice=ref(''),exportURL=ref(''),selection=ref<Record<PayrollTab,PayrollRow[]>>({wages:[],adjustments:[],deductions:[],advances:[],settlements:[],history:[]}),action=ref<Action|null>(null),target=ref<PayrollRow|null>(null),payment=ref<PayrollPaymentRow|null>(null)
let generation=0,alive=true
const queryParams=computed(()=>{
 const params=new URLSearchParams({worker_id:props.worker.worker_id,tab:tab.value,page:String(page.value)})
 if(from.value)params.set('from',from.value)
 if(to.value)params.set('to',to.value)
 if(projectId.value)params.set('project_id',projectId.value)
 return params
})
const picked=(key:Exclude<PayrollTab,'settlements'|'history'>)=>selection.value[key]
const selectionSummary=computed(()=>({wages:picked('wages') as PayrollWageRow[],adjustments:picked('adjustments') as PayrollAdjustmentRow[],deductions:picked('deductions') as PayrollDeductionRow[],advances:picked('advances') as PayrollAdvanceRow[]} satisfies PayrollSettlementSelection))
const selectedCount=computed(()=>selectionSummary.value.wages.length+selectionSummary.value.adjustments.length+selectionSummary.value.deductions.length+selectionSummary.value.advances.length)
const selectedBase=computed(()=>payrollSettlementBase(selectionSummary.value))
const selectedAdvanceMax=computed(()=>maxAdvanceAllocation(selectionSummary.value))
const canSettle=computed(()=>payrollSettlementCanConfirm(selectionSummary.value))
const pageCount=computed(()=>Math.max(1,Math.ceil(total.value/pageSize.value)))
const actionBusy=computed(()=>!!action.value)
function isSelectable(row:PayrollRow){return payrollSourceSelectable(tab.value,row)}
function isSelected(row:PayrollRow){return selection.value[tab.value].some(item=>item.id===row.id)}
function toggle(row:PayrollRow,event:Event){
 const checked=(event.target as HTMLInputElement).checked,current=selection.value[tab.value].filter(item=>item.id!==row.id)
 if(checked)current.push(row)
 selection.value={...selection.value,[tab.value]:current}
}
function selectionHint():string{
 const total=selectedCount.value
 if(total&&!payrollSettlementScopeCompatible(selectionSummary.value))return `已选 ${total} 笔；结算请按同一工地拆分，未关联工地的记录需单独结算`
 return total?`已选 ${total} 笔，可结算金额 ${payrollMoney(selectedBase.value)}，可抵扣预支最多 ${payrollMoney(selectedAdvanceMax.value)}`:'先在施工工资、调整、已确认扣款或预支标签选择要结算的记录'
}
async function load(clearSelection=false){
 const current=++generation,session=apiSession.capture()
 const params=new URLSearchParams(queryParams.value)
 loading.value=true;error.value='';notice.value=''
 if(clearSelection)selection.value={wages:[],adjustments:[],deductions:[],advances:[],settlements:[],history:[]}
 rows.value=[];historyRows.value=[];total.value=0;exportURL.value=''
 try{
  if(tab.value==='history'){
   const historyParams=new URLSearchParams({worker_id:props.worker.worker_id,page:String(page.value)})
   if(from.value)historyParams.set('from',from.value)
   if(to.value)historyParams.set('to',to.value)
   if(projectId.value)historyParams.set('project_id',projectId.value)
   const result=await api<PayrollHistoryPage>('/payroll-history?'+historyParams)
   if(!alive||current!==generation||!apiSession.current(session))return
   historyRows.value=result.rows;total.value=Number(result.total);page.value=Number(result.page)||page.value;pageSize.value=Number(result.page_size)||100
  }else{
   const result=await api<PayrollPage>('/payroll?'+params)
   if(!alive||current!==generation||!apiSession.current(session))return
   rows.value=result.rows;total.value=Number(result.total);page.value=Number(result.page)||page.value;pageSize.value=Number(result.page_size)||100;exportURL.value=payrollExportUrl('/payroll',params)
  }
 }catch(e){if(alive&&current===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(alive&&current===generation)loading.value=false}
}
function applyFilters(){if(from.value&&to.value&&from.value>to.value){error.value='开始日期不能晚于结束日期';return}page.value=1;void load(true)}
function changeTab(next:PayrollTab){if(next===tab.value||actionBusy.value)return;tab.value=next;page.value=1;void load()}
function turn(next:number){if(loading.value||actionBusy.value||next<1||next>pageCount.value)return;page.value=next;void load()}
function open(actionName:Action,row:PayrollRow|null=null,paymentRow:PayrollPaymentRow|null=null){if(loading.value||actionBusy.value)return;action.value=actionName;target.value=row;payment.value=paymentRow;error.value=''}
function closeAction(){if(loading.value)return;action.value=null;target.value=null;payment.value=null}
async function saved(){action.value=null;target.value=null;payment.value=null;await load(true);emit('changed');notice.value=error.value?'操作已保存，但工资明细刷新失败，请重试':'工资记录已保存'}
async function searchProjects(){
 projectLoading.value=true;error.value=''
 try{const result=await api<PayrollProjectPage>('/payroll-projects?'+new URLSearchParams({q:projectQuery.value.trim(),page:'1'}));projects.value=result.rows;projectsOpen.value=true}
 catch(e){error.value=e instanceof Error?e.message:'读取工地失败'}
 finally{projectLoading.value=false}
}
function chooseProject(value:string){projectId.value=value;selectedProject.value=projects.value.find(item=>item.id===value)||null;projectsOpen.value=false;applyFilters()}
function clearProject(){projectId.value='';selectedProject.value=null;applyFilters()}
function projectLabel(row:PayrollRow){return row.project_name||row.address||'未关联工地'}
function dateOf(row:PayrollRow){if('work_date'in row)return row.work_date;if('effective_date'in row)return row.effective_date;if('deduction_date'in row)return row.deduction_date;if('advance_date'in row)return row.advance_date;return row.settlement_date}
function isWageRow(row:PayrollRow):row is PayrollWageRow{return 'order_id' in row&&'work_date' in row}
function wagePending(row:PayrollRow){return isWageRow(row)&&row.wage_configured===false}
function amountOf(row:PayrollRow){if(isWageRow(row))return payrollCents(row.wage_cents)+payrollCents(row.delivery_wage_cents);return 'amount_cents'in row?payrollCents(row.amount_cents):payrollCents(row.payable_cents)}
function constructionWage(row:PayrollWageRow){return row.wage_cents===undefined?'工价待配置':payrollMoney(row.wage_cents)}
function statusLabel(row:PayrollRow){if(isWageRow(row))return wagePending(row)?'工价待配置':Number(row.settled)?'已结算':'未结算';if('deduction_date'in row)return ({pending:'待确认',confirmed:'已确认',voided:'已撤销'} as Record<string,string>)[row.status]||String(row.status);if('advance_date'in row)return row.status==='voided'?'已撤销':`余额 ${payrollMoney(row.remaining_cents)}`;if('settlement_date'in row)return ({confirmed:'已确认',voided:'已撤销'} as Record<string,string>)[row.status]||'状态未识别';return Boolean(Number(row.settled))?'已结算':'未结算'}
function adjustmentLabel(row:PayrollAdjustmentRow){if(row.reversal_of)return `更正记录 · 原调整 ${row.reversal_of}`;if(row.reversal_id)return `已更正 · 更正记录 ${row.reversal_id}`;return '有效'}
function canEditDeduction(row:PayrollDeductionRow){return ['pending','confirmed'].includes(row.status)&&!Boolean(Number(row.settled))}
function canEditAdvance(row:PayrollAdvanceRow){return row.status!=='voided'&&payrollCents(row.allocated_cents)===0n}
function paymentDate(row:PayrollPaymentRow){return row.paid_date||row.payment_date||'日期未记录'}
function historyEventLabel(row:PayrollHistoryRow){return ({'create-adjustment':'新增工资调整','reverse-adjustment':'更正工资调整','create-deduction':'登记扣款','edit-deduction':'更正扣款','confirm-deduction':'确认扣款','reverse-deduction':'撤销扣款','create-advance':'登记工资预支','edit-advance':'更正工资预支','reverse-advance':'撤销工资预支','create-settlement':'确认工资结算','reverse-settlement':'撤销工资结算','create-payment':'登记实际发放','reverse-payment':'更正付款'} as Record<string,string>)[row.event]||row.event||'工资操作'}
function historyEntityLabel(value:string){return ({adjustment:'工资调整',deduction:'扣款',advance:'工资预支',settlement:'工资结算',payment:'工资发放',wage:'施工工资'} as Record<string,string>)[value]||value||'工资记录'}
watch(()=>props.worker.worker_id,()=>{tab.value='wages';from.value='';to.value='';projectId.value='';selectedProject.value=null;page.value=1;void load(true)})
onMounted(()=>void load(true))
onBeforeUnmount(()=>{alive=false;++generation;rows.value=[];historyRows.value=[];selection.value={wages:[],adjustments:[],deductions:[],advances:[],settlements:[],history:[]}})
defineExpose({refresh:()=>load()})
</script>

<template>
 <section class="payroll-detail">
  <div class="toolbar detail-heading"><button type="button" :disabled="actionBusy" @click="emit('back')">返回人员汇总</button><h2>{{worker.worker_name||'历史施工人'}} · 工资明细</h2><button type="button" :disabled="loading||actionBusy" @click="load(true)">刷新明细</button></div>
  <div class="toolbar filters">
   <label>开始日期<input v-model="from" type="date" :disabled="actionBusy" @change="applyFilters"></label><label>结束日期<input v-model="to" type="date" :disabled="actionBusy" @change="applyFilters"></label>
   <div class="project-filter"><label>关联工地<input v-model="projectQuery" maxlength="300" :disabled="actionBusy" placeholder="输入工地地址或名称" @keydown.enter.prevent="searchProjects"></label><button type="button" :disabled="actionBusy||projectLoading" @click="searchProjects">{{projectLoading?'查询中…':'查找'}}</button><select v-if="projectsOpen" :value="projectId" :disabled="actionBusy" aria-label="按工地筛选" @change="chooseProject(($event.target as HTMLSelectElement).value)"><option value="">请选择工地</option><option v-for="item in projects" :key="item.id" :value="item.id">{{item.name}}{{item.address?' · '+item.address:''}}{{item.status!=='进行中'?'（'+item.status+'）':''}}</option></select><span v-else-if="selectedProject" class="selected-project">{{selectedProject.name}}{{selectedProject.address?' · '+selectedProject.address:''}}</span><button v-if="projectId" type="button" :disabled="actionBusy" @click="clearProject">清除工地</button></div>
   <button type="button" :disabled="actionBusy" @click="applyFilters">查询明细</button>
  </div>
  <div v-if="tab!=='history'" class="toolbar actions"><button type="button" :disabled="loading||actionBusy" @click="open('create-adjustment')">新增工资调整</button><button type="button" :disabled="loading||actionBusy" @click="open('create-deduction')">登记扣款</button><button type="button" :disabled="loading||actionBusy" @click="open('create-advance')">登记工资预支</button><span class="selection-status">{{selectionHint()}}</span><a v-if="exportURL&&!loading&&!error&&total" :href="exportURL">导出全部筛选结果</a><button type="button" class="primary" :disabled="loading||actionBusy||!canSettle" @click="open('create-settlement')">确认工资结算</button></div>
  <div class="tabs payroll-tabs"><button v-for="item in tabs" :key="item.id" type="button" :class="{active:tab===item.id}" :disabled="loading||actionBusy" @click="changeTab(item.id)">{{item.label}}</button></div>
  <p v-if="error" class="error" role="alert">{{error}} <button type="button" :disabled="loading||actionBusy" @click="load()">重新读取</button></p><p v-if="notice" class="notice" role="status">{{notice}}</p>
  <p class="row-count">{{loading?'正在读取…':`共 ${total} 笔 · 第 ${page} / ${pageCount} 页`}}</p>
  <div v-if="tab!=='history'" class="table-wrap"><table class="payroll-table"><thead><tr><th v-if="tab!=='settlements'">结算选择</th><th>日期</th><th>工资项目 / 关联工地</th><th>金额或状态</th><th>说明 / 明细</th><th>操作</th></tr></thead><tbody>
   <template v-for="row in rows" :key="row.id">
    <tr>
     <td v-if="tab!=='settlements'" class="select-cell"><input type="checkbox" :checked="isSelected(row)" :disabled="!isSelectable(row)||loading||actionBusy" :aria-label="`选择${tabs.find(item=>item.id===tab)?.label}记录 ${row.id} 纳入结算`" @change="toggle(row,$event)"><small>{{isSelectable(row)?'可纳入结算':wagePending(row)?'工价待配置，不能结算':'已结算或不可抵扣'}}</small></td>
     <td>{{dateOf(row)}}</td>
     <td class="description-cell">
      <template v-if="tab==='wages'"><strong>施工单 {{(row as PayrollWageRow).number}} · {{wagePending(row)?'工价待配置':'总工资 '+payrollMoney(amountOf(row))}}</strong><small>{{projectLabel(row)}}</small><small v-if="wagePending(row)">本条不能纳入工资结算</small><small v-if="(row as PayrollWageRow).delivery_wage_cents&&payrollCents((row as PayrollWageRow).delivery_wage_cents)>0n">施工工资 {{constructionWage(row as PayrollWageRow)}}{{(row as PayrollWageRow).wage_cents===undefined?'':' 元'}} · 送货工资 {{payrollMoney((row as PayrollWageRow).delivery_wage_cents)}}</small></template>
      <template v-else-if="tab==='adjustments'"><strong>工资调整 · {{adjustmentLabel(row as PayrollAdjustmentRow)}}</strong><small>{{projectLabel(row)}}</small></template>
      <template v-else-if="tab==='deductions'"><strong>工资扣款 · {{statusLabel(row)}}</strong><small>{{projectLabel(row)}}</small><small v-if="(row as PayrollDeductionRow).legal_basis">依据：{{(row as PayrollDeductionRow).legal_basis}}</small></template>
      <template v-else-if="tab==='advances'"><strong>工资预支</strong><small>{{projectLabel(row)}}</small><small>已抵扣 {{payrollMoney((row as PayrollAdvanceRow).allocated_cents)}} · 未抵扣 {{payrollMoney((row as PayrollAdvanceRow).remaining_cents)}}</small></template>
      <template v-else><strong>工资结算 · {{statusLabel(row)}}</strong><small>施工工资 {{payrollMoney((row as PayrollSettlementRow).gross_cents)}} · 调整 {{payrollMoney((row as PayrollSettlementRow).adjustment_cents)}}</small><small>扣款 {{payrollMoney((row as PayrollSettlementRow).deduction_cents)}} · 预支抵扣 {{payrollMoney((row as PayrollSettlementRow).advance_cents)}}</small></template>
     </td>
     <td class="amount-cell"><strong>{{wagePending(row)?'工价待配置':payrollMoney(amountOf(row))}}</strong><small v-if="tab==='settlements'">已实发 {{payrollMoney((row as PayrollSettlementRow).paid_cents)}}, 未发 {{payrollMoney((row as PayrollSettlementRow).remaining_cents)}}</small><small v-else>{{statusLabel(row)}}</small></td>
     <td class="reason-cell"><template v-if="tab==='wages'"><span>{{(row as PayrollWageRow).order_id}}</span><small>{{wagePending(row)?'施工工资待配置':'施工 '+constructionWage(row as PayrollWageRow)+' 元'}}{{payrollCents((row as PayrollWageRow).delivery_wage_cents)>0n?' · 送货 '+payrollMoney((row as PayrollWageRow).delivery_wage_cents):''}}</small></template><template v-else-if="tab==='adjustments'||tab==='deductions'">{{row.reason||'—'}}<small v-if="row.actor">操作人：{{row.actor}}</small></template><template v-else-if="tab==='advances'">{{(row as PayrollAdvanceRow).note||'—'}}</template><template v-else><span>结算确认不代表已付款</span></template></td>
     <td class="row-actions">
      <template v-if="tab==='adjustments'"><button v-if="payrollAdjustmentCanReverse(row as PayrollAdjustmentRow)" type="button" :disabled="loading||actionBusy" @click="open('reverse-adjustment',row)">更正调整</button></template>
      <template v-else-if="tab==='deductions'"><button v-if="canEditDeduction(row as PayrollDeductionRow)" type="button" :disabled="loading||actionBusy" @click="open('edit-deduction',row)">更正扣款</button><button v-if="(row as PayrollDeductionRow).status==='pending'&&!Boolean(Number(row.settled))" type="button" :disabled="loading||actionBusy" @click="open('confirm-deduction',row)">确认依据</button><button v-if="['pending','confirmed'].includes((row as PayrollDeductionRow).status)&&!Boolean(Number(row.settled))" type="button" :disabled="loading||actionBusy" @click="open('reverse-deduction',row)">撤销扣款</button></template>
      <template v-else-if="tab==='advances'"><button v-if="canEditAdvance(row as PayrollAdvanceRow)" type="button" :disabled="loading||actionBusy" @click="open('edit-advance',row)">更正预支</button><button v-if="canEditAdvance(row as PayrollAdvanceRow)" type="button" :disabled="loading||actionBusy" @click="open('reverse-advance',row)">撤销预支</button></template>
       <template v-else-if="tab==='settlements'"><button v-if="payrollCents((row as PayrollSettlementRow).remaining_cents)>0n" type="button" :disabled="loading||actionBusy" @click="open('create-payment',row)">登记实际发放</button><button v-if="payrollSettlementCanReverse(row as PayrollSettlementRow)" type="button" :disabled="loading||actionBusy" @click="open('reverse-settlement',row)">撤销结算</button></template>
      <template v-else><span>来源工资只读</span></template>
     </td>
    </tr>
    <tr v-if="tab==='settlements'&&((row as PayrollSettlementRow).payments||[]).length" class="payments-row"><td colspan="6"><div class="payment-list"><strong>实际付款记录</strong><div v-for="entry in (row as PayrollSettlementRow).payments" :key="entry.id" class="payment-entry"><span>{{paymentDate(entry)}} · {{payrollMoney(entry.amount_cents)}}<small>{{entry.method||'未记录方式'}}{{entry.reversed?' · 已更正':''}} {{entry.note||''}}</small><small v-if="entry.reversal_reason">付款更正原因：{{entry.reversal_reason}}</small></span><button v-if="!entry.reversed" type="button" :disabled="loading||actionBusy" @click="open('reverse-payment',row,entry)">更正付款</button></div></div></td></tr>
   </template>
   <tr v-if="!rows.length"><td :colspan="tab==='settlements'?5:6" class="empty">{{loading?'正在读取工资明细…':error?'读取失败，请检查条件或重试':'当前条件下暂无工资记录'}}</td></tr>
  </tbody></table></div>
  <div v-else class="table-wrap"><table class="payroll-table history-table"><thead><tr><th>操作时间</th><th>操作</th><th>工资记录</th><th>操作人</th><th>原因</th><th>更正前</th><th>更正后</th></tr></thead><tbody>
   <tr v-for="row in historyRows" :key="row.id"><td>{{row.created_at}}</td><td><strong>{{historyEventLabel(row)}}</strong></td><td class="history-id">{{historyEntityLabel(row.entity_type)}} · {{row.entity_id}}<small>操作编号：{{row.operation_id}}</small></td><td>{{row.actor_name||'—'}}</td><td class="history-reason">{{row.reason||'—'}}</td><td><pre>{{payrollSnapshotText(row.before_snapshot)}}</pre></td><td><pre>{{payrollSnapshotText(row.after_snapshot)}}</pre></td></tr>
   <tr v-if="!historyRows.length"><td colspan="7" class="empty">{{loading?'正在读取工资操作历史…':error?'读取失败，请检查条件或重试':'当前条件下暂无操作历史'}}</td></tr>
  </tbody></table></div>
  <div class="toolbar pager"><button type="button" :disabled="loading||actionBusy||page<=1" @click="turn(page-1)">上一页</button><span>第 {{page}} / {{pageCount}} 页 · 每页 {{pageSize}} 笔</span><button type="button" :disabled="loading||actionBusy||page>=pageCount" @click="turn(page+1)">下一页</button></div>
  <PayrollActionDialog v-if="action" :action="action" :worker-id="worker.worker_id" :row="target" :payment="payment" :selection="selectionSummary" @close="closeAction" @saved="saved" />
 </section>
</template>

<style scoped>
.detail-heading h2{font-size:18px;margin:0;flex:1 1 240px}.filters,.actions{align-items:end}.filters label{min-width:125px}.project-filter{display:grid;grid-template-columns:minmax(170px,1fr) auto;gap:7px;align-items:end;min-width:250px;max-width:100%;flex:1 1 290px}.project-filter select{grid-column:1/-1;width:100%;min-width:0}.selected-project{grid-column:1/-1;overflow-wrap:anywhere;color:#53657d}.selection-status{flex:1 1 260px;color:#53657d}.row-count{padding:0 16px 10px;color:#667487}.payroll-table{min-width:880px}.history-table{min-width:1150px}.history-table td{max-width:300px;overflow-wrap:anywhere}.history-table pre{max-width:280px;max-height:220px;margin:0;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace}.history-table .history-id{min-width:170px}.history-id small{overflow-wrap:anywhere}.history-reason{min-width:160px}.payroll-table th,.payroll-table td{vertical-align:top}.payroll-table .description-cell,.payroll-table .reason-cell{white-space:normal;min-width:150px;max-width:300px;overflow-wrap:anywhere}.payroll-table .amount-cell{white-space:nowrap}.select-cell{min-width:110px}.row-actions{min-width:135px;display:flex;flex-wrap:wrap;gap:5px}.row-actions button{white-space:nowrap;padding:6px 9px}.payments-row>td{padding:0 16px 12px;background:#fcfdff}.payment-list{border-left:3px solid #d6e2f4;padding:8px 12px}.payment-entry{display:flex;gap:12px;justify-content:space-between;align-items:center;border-top:1px solid #edf0f5;padding:7px 0}.payment-entry span{overflow-wrap:anywhere}.payment-entry small{margin-top:2px}.pager{justify-content:flex-end}.empty{text-align:center}
@media(max-width:600px){.toolbar{padding:12px}.filters label{min-width:0;flex:1 1 135px}.project-filter{min-width:100%;flex-basis:100%}.detail-heading h2{flex-basis:100%}.payroll-table{min-width:790px}.actions .primary{width:100%}.selection-status{flex-basis:100%}.payment-entry{align-items:flex-start;flex-wrap:wrap}}
</style>
