<script setup lang="ts">
import {computed, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import {api, apiSession, SessionChangedError} from '../api'
import {expenseClaimConfirmed, expenseClaimPending, expenseClaimStatus, expenseClaimVoided, expenseCents, expenseHistoryCanReverse, expenseHistorySnapshot, expenseMoney, type ExpenseAction, type ExpenseAdvance, type ExpenseClaim, type ExpenseHistory, type ExpensePage, type ExpenseTab} from '../expenses'
import ExpenseActionDialog from './ExpenseActionDialog.vue'

const props=defineProps<{allowed:boolean;initialFrom?:string;initialTo?:string}>()
type ActionName=ExpenseAction['action']
type ExpenseRow=ExpenseClaim|ExpenseAdvance|ExpenseHistory
const tabs:Array<{id:ExpenseTab;label:string}>=[{id:'claims',label:'报销记录'},{id:'advances',label:'费用预支'},{id:'history',label:'操作历史'}]
const tab=ref<ExpenseTab>('claims'),from=ref(props.initialFrom||''),to=ref(props.initialTo||''),status=ref(''),query=ref(''),page=ref(1),pageSize=ref(50)
const claims=ref<ExpenseClaim[]>([]),advances=ref<ExpenseAdvance[]>([]),history=ref<ExpenseHistory[]>([]),total=ref(0)
const loading=ref(false),error=ref(''),notice=ref(''),dialog=ref<{action:ActionName;row:ExpenseRow|null}|null>(null)
const appliedExport=ref('')
const pageCount=computed(()=>Math.max(1,Math.ceil(total.value/pageSize.value)))
const params=computed(()=>new URLSearchParams({tab:tab.value,from:from.value,to:to.value,status:status.value,q:query.value.trim(),page:String(page.value)}))
let generation=0,alive=true

function clearRows(){claims.value=[];advances.value=[];history.value=[];total.value=0;appliedExport.value=''}
async function refresh(){
 if(!props.allowed){clearRows();dialog.value=null;return}
 if(loading.value)return
 const current=++generation,session=apiSession.capture(),requestedTab=tab.value
 const requestedParams=params.value.toString()
 loading.value=true;error.value='';clearRows()
 try{
  const result=await api<ExpensePage<ExpenseRow>>('/expenses?'+requestedParams)
  if(!alive||current!==generation||!apiSession.current(session)||!props.allowed||requestedTab!==tab.value)return
  total.value=Number(result.total)||0;page.value=Number(result.page)||page.value;pageSize.value=Number(result.page_size)||50
  appliedExport.value='/api/expenses?'+requestedParams+'&export=1'
  if(requestedTab==='claims')claims.value=result.rows as ExpenseClaim[]
  else if(requestedTab==='advances')advances.value=result.rows as ExpenseAdvance[]
  else history.value=result.rows as ExpenseHistory[]
 }catch(e){if(alive&&current===generation&&apiSession.current(session)&&props.allowed&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(alive&&current===generation)loading.value=false}
}
function applyFilters(){if(from.value&&to.value&&from.value>to.value){error.value='开始日期不能晚于结束日期';return}page.value=1;notice.value='';void refresh()}
function changeTab(next:ExpenseTab){if(next===tab.value||loading.value||dialog.value)return;tab.value=next;status.value='';page.value=1;void refresh()}
function turn(next:number){if(loading.value||dialog.value||next<1||next>pageCount.value)return;page.value=next;void refresh()}
function open(action:ActionName,row:ExpenseRow|null=null){if(loading.value||dialog.value||!props.allowed)return;dialog.value={action,row};error.value='';notice.value=''}
function close(){dialog.value=null}
async function saved(){dialog.value=null;await refresh();notice.value=error.value?'操作已保存，但列表刷新失败，请重试':'报销记录已保存，余额已更新'}
function claimCanCorrect(row:ExpenseClaim){return expenseClaimConfirmed(row)&&expenseCents(row.paid_cents)===0n&&expenseCents(row.advance_applied_cents)===0n}
function claimCanVoid(row:ExpenseClaim){return !expenseClaimVoided(row)&&expenseCents(row.paid_cents)===0n&&expenseCents(row.advance_applied_cents)===0n}
function advanceVoided(row:ExpenseAdvance){return row.status==='voided'||row.status==='已撤销'}
function advanceUnused(row:ExpenseAdvance){return !advanceVoided(row)&&expenseCents(row.applied_cents)===0n&&expenseCents(row.returned_cents)===0n}
function statusOptions(){
 if(tab.value==='claims')return [['','全部状态'],['待审核','待审核'],['已确认','已确认'],['部分报销','部分报销'],['预支已核销','预支已核销'],['已付清','已付清'],['已撤销','已撤销']]
 if(tab.value==='advances')return [['','全部状态'],['未核销','有余额'],['已结清','已结清'],['已撤销','已撤销']]
 return [['','全部记录'],['create-claim','新增报销'],['confirm-claim','确认报销'],['pay-claim','实际付款'],['create-advance','费用预支'],['allocate-advance','预支核销'],['return-advance','员工退回']]
}
function historyAction(row:ExpenseHistory):ActionName|null{const kind=expenseHistoryCanReverse(row);return kind==='payment'?'reverse-payment':kind==='allocation'?'reverse-allocation':kind==='return'?'reverse-return':null}
function historyLabel(row:ExpenseHistory){return ({'create-claim':'新增报销','update-claim':'更正报销','confirm-claim':'确认报销','void-claim':'撤销报销','pay-claim':'实际付款','reverse-payment':'更正实际付款','create-advance':'登记费用预支','correct-advance':'更正预支信息','reverse-advance':'撤销费用预支','allocate-advance':'预支核销','reverse-allocation':'撤销预支核销','return-advance':'员工退回','reverse-return':'更正员工退回'} as Record<string,string>)[row.event]||row.event||'费用操作'}
function historyEntity(row:ExpenseHistory){return ({claim:'报销记录',expense_claim:'报销记录',advance:'费用预支',expense_advance:'费用预支',payment:'实际付款',expense_payment:'实际付款',allocation:'预支核销',expense_allocation:'预支核销',return:'员工退回',expense_return:'员工退回'} as Record<string,string>)[row.entity_type]||row.entity_type||'费用记录'}
watch(()=>props.allowed,allowed=>{if(allowed){void refresh();return}++generation;clearRows();dialog.value=null;error.value='';notice.value='';loading.value=false})
onMounted(()=>void refresh())
onBeforeUnmount(()=>{alive=false;++generation;clearRows();dialog.value=null})
defineExpose({refresh:()=>refresh()})
</script>

<template>
 <section class="panel expense-management" aria-label="报销与费用预支">
  <div class="expense-toolbar toolbar"><button class="primary" type="button" :disabled="loading||!!dialog||!allowed" @click="open('create-claim')">＋ 新增报销</button><button type="button" :disabled="loading||!!dialog||!allowed" @click="open('create-advance')">登记费用预支</button><a v-if="appliedExport&&allowed&&!loading" :href="appliedExport">导出当前筛选</a><span class="expense-count">共 {{total}} 项</span></div>
  <div class="tabs expense-tabs" role="tablist" aria-label="费用账目"><button v-for="item in tabs" :key="item.id" type="button" role="tab" :aria-selected="tab===item.id" :class="{active:tab===item.id}" :disabled="loading||!!dialog" @click="changeTab(item.id)">{{item.label}}</button></div>
  <div class="toolbar expense-filters"><label>开始日期<input v-model="from" type="date" :disabled="loading||!!dialog" @change="applyFilters"></label><label>结束日期<input v-model="to" type="date" :disabled="loading||!!dialog" @change="applyFilters"></label><label>状态<select v-model="status" :disabled="loading||!!dialog" @change="applyFilters"><option v-for="item in statusOptions()" :key="item[0]" :value="item[0]">{{item[1]}}</option></select></label><label class="expense-search">搜索<input v-model="query" maxlength="120" placeholder="人员、用途、备注或单据" :disabled="loading||!!dialog" @keydown.enter.prevent="applyFilters"></label><button type="button" :disabled="loading||!!dialog" @click="applyFilters">查询</button></div>
  <p v-if="error" class="error expense-message" role="alert">{{error}} <button type="button" :disabled="loading||!!dialog" @click="refresh">重新读取</button></p><p v-if="notice" class="notice expense-message" role="status">{{notice}}</p>
  <p class="expense-page-status">{{loading?'正在读取…':`共 ${total} 项 · 第 ${page} / ${pageCount} 页 · 每页 ${pageSize} 项`}}</p>
  <div v-if="tab==='claims'" class="table-wrap"><table class="expense-table"><thead><tr><th>日期</th><th>报销人 / 类型</th><th>费用及关联</th><th>报销状态</th><th>申报 / 确认金额</th><th>预支核销</th><th>实际已付</th><th>尚欠</th><th>操作</th></tr></thead><tbody>
   <tr v-for="row in claims" :key="row.id"><td>{{row.expense_date}}</td><td><strong>{{row.person_name}}</strong><small>{{row.category}}</small></td><td class="expense-description">{{row.note||'—'}}<small v-if="row.purchase_document_id">关联进货 {{row.purchase_number||row.purchase_document_id}} · {{row.purchase_date}} · 原进货 {{expenseMoney(row.purchase_total_cents)}}</small></td><td><span class="expense-status" :class="'state-'+row.status">{{expenseClaimStatus(row)}}</span></td><td>{{expenseMoney(row.amount_cents)}}</td><td>{{expenseMoney(row.advance_applied_cents)}}</td><td>{{expenseMoney(row.paid_cents)}}</td><td><strong>{{expenseMoney(row.due_cents)}}</strong></td><td class="expense-actions">
    <button v-if="expenseClaimPending(row)" type="button" :disabled="loading||!!dialog" @click="open('update-claim',row)">编辑</button><button v-if="expenseClaimPending(row)" type="button" :disabled="loading||!!dialog" @click="open('confirm-claim',row)">确认</button>
    <button v-if="claimCanVoid(row)" type="button" :disabled="loading||!!dialog" @click="open('void-claim',row)">撤销</button><button v-if="claimCanCorrect(row)" type="button" :disabled="loading||!!dialog" @click="open('update-claim',row)">更正</button>
    <button v-if="expenseClaimConfirmed(row)&&expenseCents(row.due_cents)>0n" type="button" :disabled="loading||!!dialog" @click="open('allocate-advance',row)">核销预支</button><button v-if="expenseClaimConfirmed(row)&&expenseCents(row.due_cents)>0n" type="button" :disabled="loading||!!dialog" @click="open('pay-claim',row)">登记实际付款</button>
    <span v-if="expenseClaimVoided(row)">已撤销</span><span v-else-if="expenseClaimConfirmed(row)&&expenseCents(row.due_cents)<=0n">{{expenseClaimStatus(row)}}</span>
   </td></tr><tr v-if="!claims.length"><td colspan="9" class="empty">{{loading?'正在读取报销记录…':error?'读取失败，请检查条件或重试':'当前条件下暂无报销记录'}}</td></tr>
  </tbody></table></div>
  <div v-else-if="tab==='advances'" class="table-wrap"><table class="expense-table advance-table"><thead><tr><th>实际付款日期</th><th>预支人 / 用途</th><th>备注</th><th>实际已付</th><th>已核销</th><th>员工已退</th><th>未核销余额</th><th>操作</th></tr></thead><tbody>
   <tr v-for="row in advances" :key="row.id"><td>{{row.advance_date}}</td><td><strong>{{row.person_name}}</strong><small>{{row.purpose}}</small></td><td class="expense-description">{{row.note||'—'}}</td><td>{{expenseMoney(row.amount_cents)}}</td><td>{{expenseMoney(row.applied_cents)}}</td><td>{{expenseMoney(row.returned_cents)}}</td><td><strong>{{expenseMoney(row.remaining_cents)}}</strong></td><td class="expense-actions"><button v-if="advanceUnused(row)" type="button" :disabled="loading||!!dialog" @click="open('correct-advance',row)">更正说明</button><button v-if="advanceUnused(row)" type="button" :disabled="loading||!!dialog" @click="open('reverse-advance',row)">撤销预支</button><button v-if="!advanceVoided(row)&&expenseCents(row.remaining_cents)>0n" type="button" :disabled="loading||!!dialog" @click="open('return-advance',row)">登记员工退回</button><span v-if="advanceVoided(row)">已撤销</span></td></tr>
   <tr v-if="!advances.length"><td colspan="8" class="empty">{{loading?'正在读取费用预支…':error?'读取失败，请检查条件或重试':'当前条件下暂无费用预支'}}</td></tr>
  </tbody></table></div>
  <div v-else class="table-wrap"><table class="expense-table history-table"><thead><tr><th>操作时间</th><th>操作</th><th>费用记录</th><th>操作人</th><th>原因 / 说明</th><th>更正前后快照</th><th>历史处理</th></tr></thead><tbody>
   <tr v-for="row in history" :key="row.id"><td>{{row.created_at}}</td><td>{{historyLabel(row)}}<small>{{row.current_status==='reversed'?'已撤销 / 更正':''}}</small></td><td class="expense-description">{{historyEntity(row)}} · {{row.entity_id}}<small>编号 {{row.operation_id}}</small><small v-if="row.amount_cents!==undefined">金额 {{expenseMoney(row.amount_cents)}}</small></td><td>{{row.actor_name||'—'}}</td><td class="expense-description">{{row.reason||'—'}}</td><td class="snapshot-cell"><details><summary>查看快照</summary><pre>更正前：{{expenseHistorySnapshot(row.before_snapshot)}}&#10;更正后：{{expenseHistorySnapshot(row.after_snapshot)}}</pre></details></td><td><button v-if="historyAction(row)&&row.current_status==='active'" type="button" :disabled="loading||!!dialog" @click="open(historyAction(row)!,row)">{{historyAction(row)==='reverse-payment'?'更正付款':historyAction(row)==='reverse-allocation'?'撤销核销':'更正退回'}}</button><span v-else>{{row.current_status==='reversed'?'已更正':'—'}}</span></td></tr>
   <tr v-if="!history.length"><td colspan="7" class="empty">{{loading?'正在读取操作历史…':error?'读取失败，请检查条件或重试':'当前条件下暂无操作历史'}}</td></tr>
  </tbody></table></div>
  <div class="toolbar expense-pager"><button type="button" :disabled="loading||!!dialog||page<=1" @click="turn(page-1)">上一页</button><span>第 {{page}} / {{pageCount}} 页</span><button type="button" :disabled="loading||!!dialog||page>=pageCount" @click="turn(page+1)">下一页</button></div>
  <ExpenseActionDialog v-if="dialog&&allowed" :key="dialog.action+':'+(dialog.row?.id||'new')" :action="dialog.action" :row="dialog.row" :allowed="allowed" @close="close" @saved="saved" />
 </section>
</template>

<style scoped>
.expense-management{min-width:0}.expense-toolbar{padding-bottom:9px}.expense-count{margin-left:auto;color:#64748b}.expense-tabs{padding-top:0}.expense-filters{align-items:end}.expense-filters label{min-width:125px}.expense-search{flex:1 1 180px}.expense-filters input,.expense-filters select{width:100%;min-width:0}.expense-message{margin:10px 15px}.expense-page-status{padding:2px 15px 10px;color:#67778b}.expense-table{min-width:1080px}.advance-table{min-width:950px}.history-table{min-width:1050px}.expense-table th,.expense-table td{vertical-align:top}.expense-table td{white-space:nowrap}.expense-table .expense-description{white-space:normal;max-width:300px;min-width:140px;overflow-wrap:anywhere}.expense-table small{margin-top:3px}.expense-status{display:inline-block;padding:3px 8px;border-radius:4px;background:#edf2f7;color:#46566a;white-space:nowrap}.state-pending{background:#fff5df;color:#8a5b00}.state-confirmed{background:#eef4ff;color:#365c98}.state-voided{background:#f1f2f4;color:#6d7480}.expense-actions{display:flex;flex-wrap:wrap;gap:4px;min-width:185px;white-space:normal!important}.expense-actions button,.history-table button{padding:5px 8px;white-space:nowrap}.snapshot-cell{min-width:180px;max-width:320px;white-space:normal!important}.snapshot-cell pre{max-height:240px;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace}.expense-pager{justify-content:flex-end}.expense-pager span{min-width:130px;text-align:center}
@media(max-width:650px){.expense-filters label{min-width:0;flex:1 1 135px}.expense-search{flex-basis:100%}.expense-toolbar button{flex:1 1 145px}.expense-count{flex:1 1 100%;margin:0}.expense-pager{justify-content:space-between}.expense-pager button{flex:1}.expense-pager span{order:-1;flex-basis:100%}}
</style>
