<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref,watch} from 'vue'
import {api,apiSession,command,SessionChangedError} from '../api'
import {payrollMoney,parsePayrollAmount,bonusPayrollActionName,bonusEntryKindLabel,bonusEntryCanReverse} from '../payroll'
import type {PayrollWorkerSummary} from '../payroll'

type Tab='ledger'|'payments'
type BonusRow={id:string;entry_kind:string;rule_key:string;order_id:string;order_number:string;line_id:string;category:string;line_name:string;worker_id:string;worker_name:string;project_id:string;project_name:string;address:string;work_date:string;bonus_year:number;qty_micro:string|number;unit:string;rate_cents:string|number;amount_cents:string|number;reason:string;actor_name:string;version:number;reversal_of?:string;reversal_id?:string}
type BonusPayment={id:string;amount_cents:string|number;paid_date:string;method:string;note:string;version:number;reversed:boolean|number;reversal_reason:string}
type BonusSettlement={id:string;worker_id:string;worker_name:string;bonus_year:number;settlement_date:string;amount_cents:string|number;paid_cents:string|number;remaining_cents:string|number;status:string;version:number;note:string;payments:BonusPayment[]}
type BonusPage={rows:(BonusRow|BonusSettlement)[];total:number;page:number;page_size:number;summary:{bonus_year:number;bonus_accrued_cents:string|number;bonus_adjustment_cents:string|number;bonus_paid_cents:string|number;bonus_balance_cents:string|number;bonus_settled_cents:string|number;bonus_available_cents:string|number}}
type Mode='create-adjustment'|'reverse-adjustment'|'create-settlement'|'reverse-settlement'|'create-payment'|'reverse-payment'
const props=defineProps<{worker:PayrollWorkerSummary}>()
const emit=defineEmits<{back:[];changed:[]}>()
const submit=command(),tab=ref<Tab>('ledger'),year=ref(new Date().getFullYear()),page=ref(1),pageSize=ref(100),total=ref(0),rows=ref<BonusPage['rows']>([]),summary=ref<BonusPage['summary']>({bonus_year:year.value,bonus_accrued_cents:0,bonus_adjustment_cents:0,bonus_paid_cents:0,bonus_balance_cents:0,bonus_settled_cents:0,bonus_available_cents:0}),loading=ref(false),busy=ref(false),error=ref(''),notice=ref(''),mode=ref<Mode|null>(null),target=ref<BonusRow|BonusSettlement|null>(null),payment=ref<BonusPayment|null>(null),amount=ref(''),date=ref(new Date().toLocaleDateString('sv-SE')),reason=ref(''),note=ref(''),method=ref('')
let generation=0,alive=true
const pageCount=computed(()=>Math.max(1,Math.ceil(total.value/pageSize.value)))
const available=computed(()=>BigInt(String(summary.value.bonus_available_cents||0)))
const title=computed(()=>({
 'create-adjustment':'登记奖金调整','reverse-adjustment':'更正奖金调整','create-settlement':'确认奖金结算','reverse-settlement':'撤销奖金结算','create-payment':'登记实际奖金发放','reverse-payment':'更正奖金付款',
} as Record<Mode,string>)[mode.value||'create-adjustment'])
const label=(value:unknown)=>payrollMoney(value)
const isRow=(row:BonusPage['rows'][number]):row is BonusRow=>'entry_kind'in row
const selectedSettlement=computed(()=>target.value&&!isRow(target.value)?target.value as BonusSettlement:null)
const selectedEntry=computed(()=>target.value&&isRow(target.value)?target.value as BonusRow:null)
const exportURL=computed(()=>'/api/payroll-bonus?'+new URLSearchParams({worker_id:props.worker.worker_id,year:String(year.value),tab:tab.value,export:'1'}))
async function load(){
 const current=++generation,session=apiSession.capture(),params=new URLSearchParams({worker_id:props.worker.worker_id,year:String(year.value),tab:tab.value,page:String(page.value)})
 loading.value=true;error.value='';notice.value='';rows.value=[];total.value=0
 try{const result=await api<BonusPage>('/payroll-bonus?'+params);if(!alive||current!==generation||!apiSession.current(session))return;rows.value=result.rows||[];total.value=Number(result.total);page.value=Number(result.page)||page.value;pageSize.value=Number(result.page_size)||100;summary.value=result.summary}
 catch(e){if(alive&&current===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=e instanceof Error?e.message:'读取奖金失败'}
 finally{if(alive&&current===generation)loading.value=false}
}
function changeTab(value:Tab){if(tab.value===value||busy.value||loading.value)return;tab.value=value;page.value=1;void load()}
function refresh(){void load()}
function open(value:Mode,row:BonusRow|BonusSettlement|null=null,paymentRow:BonusPayment|null=null){if(loading.value||busy.value)return;mode.value=value;target.value=row;payment.value=paymentRow;amount.value='';date.value=new Date().toLocaleDateString('sv-SE');reason.value='';note.value='';method.value='';error.value='';if(value==='create-settlement')amount.value=available.value>0n?(Number(available.value)/100).toFixed(2):'';if(value==='create-payment'&&row&&!isRow(row))amount.value=(Number(BigInt(String(row.remaining_cents)))/100).toFixed(2)}
function close(){if(busy.value)return;mode.value=null;target.value=null;payment.value=null;error.value=''}
function request(action:Mode,payload:Record<string,unknown>){
 return {action:bonusPayrollActionName(action),...payload}
}
async function save(){
 if(!mode.value||busy.value)return
 error.value=''
 try{
  let payload:Record<string,unknown>
  const row=target.value
  switch(mode.value){
   case 'create-adjustment': payload=request(mode.value,{worker_id:props.worker.worker_id,bonus_year:year.value,effective_date:date.value,amount_cents:parsePayrollAmount(amount.value,{signed:true}),reason:reason.value.trim()});break
   case 'reverse-adjustment': payload=request(mode.value,{id:selectedEntry.value?.id||'',version:Number(selectedEntry.value?.version||0),reason:reason.value.trim()});break
   case 'create-settlement': {const cents=parsePayrollAmount(amount.value);if(BigInt(cents)>available.value)throw new Error(`本次奖金结算不能超过 ${label(available.value)}`);payload=request(mode.value,{worker_id:props.worker.worker_id,bonus_year:year.value,amount_cents:cents,settlement_date:date.value,note:note.value.trim()});break}
   case 'reverse-settlement': payload=request(mode.value,{id:selectedSettlement.value?.id||'',version:Number(selectedSettlement.value?.version||0),reason:reason.value.trim()});break
   case 'create-payment': {const source=selectedSettlement.value;if(!source)throw new Error('缺少奖金结算记录，请刷新后重试');const cents=parsePayrollAmount(amount.value);if(BigInt(cents)>BigInt(String(source.remaining_cents)))throw new Error(`本次奖金发放不能超过 ${label(source.remaining_cents)}`);payload=request(mode.value,{settlement_id:source.id,settlement_version:Number(source.version||0),amount_cents:cents,paid_date:date.value,method:method.value.trim(),note:note.value.trim()});break}
   case 'reverse-payment': if(!selectedSettlement.value||!payment.value)throw new Error('缺少奖金付款记录，请刷新后重试');payload=request(mode.value,{settlement_id:selectedSettlement.value.id,id:payment.value.id,version:Number(payment.value.version||0),reason:reason.value.trim()});break
  }
  if(mode.value.startsWith('reverse-')&&!reason.value.trim())throw new Error('请填写更正原因')
  if(mode.value==='create-adjustment'&&!reason.value.trim())throw new Error('请填写奖金调整原因')
  if(!/^\d{4}-\d{2}-\d{2}$/.test(date.value))throw new Error('请选择有效日期')
  busy.value=true;await submit('/payroll-action',payload);mode.value=null;target.value=null;payment.value=null;await load();emit('changed');notice.value=error.value?'操作已保存，但奖金明细刷新失败，请重试':'奖金记录已保存'
 }catch(e){error.value=e instanceof Error?e.message:'奖金操作失败，请重试'}
 finally{busy.value=false}
}
watch(()=>props.worker.worker_id,()=>{tab.value='ledger';year.value=new Date().getFullYear();page.value=1;void load()})
onMounted(()=>void load())
onBeforeUnmount(()=>{alive=false;++generation;rows.value=[]})
defineExpose({refresh})
</script>

<template>
 <section class="panel bonus-panel">
  <div class="toolbar bonus-heading"><button type="button" :disabled="busy" @click="emit('back')">返回人员汇总</button><h2>{{worker.worker_name||'历史施工人'}} · 年终奖金</h2><button type="button" :disabled="loading||busy" @click="load">刷新</button></div>
  <div class="bonus-summary"><div><span>本年施工计提</span><strong>{{label(summary.bonus_accrued_cents)}}</strong></div><div><span>手动调整</span><strong>{{label(summary.bonus_adjustment_cents)}}</strong></div><div><span>已实际发放</span><strong>{{label(summary.bonus_paid_cents)}}</strong></div><div><span>未发余额</span><strong>{{label(summary.bonus_balance_cents)}}</strong></div></div>
  <div class="toolbar bonus-filters"><label>奖金年度<input v-model.number="year" type="number" min="2000" max="2200" :disabled="loading||busy" @change="page=1;load()"></label><button type="button" :class="{active:tab==='ledger'}" :disabled="loading||busy" @click="changeTab('ledger')">奖金累计</button><button type="button" :class="{active:tab==='payments'}" :disabled="loading||busy" @click="changeTab('payments')">奖金发放</button><button v-if="tab==='ledger'" type="button" :disabled="loading||busy" @click="open('create-adjustment')">登记奖金调整</button><button v-if="tab==='payments'" type="button" class="primary" :disabled="loading||busy||available<=0n" @click="open('create-settlement')">结算奖金</button><a v-if="!loading&&!error&&total" :href="exportURL">导出全部结果</a></div>
  <p class="hint">施工计提与老板手动登记的奖金分开列明。丙纶奖金只支持手动登记；未明确的精开工项目不会自动计提。</p>
  <p v-if="error" class="error" role="alert">{{error}} <button type="button" :disabled="loading||busy" @click="load">重新读取</button></p><p v-if="notice" class="notice" role="status">{{notice}}</p>
  <p class="row-count">{{loading?'正在读取…':`共 ${total} 笔 · 第 ${page} / ${pageCount} 页`}}</p>
  <div class="table-wrap"><table class="bonus-table"><thead><tr><th>日期</th><th>来源 / 工地</th><th>奖金项目</th><th>金额</th><th>说明</th><th>操作</th></tr></thead><tbody>
   <template v-for="row in rows" :key="row.id">
    <tr v-if="tab==='ledger'&&isRow(row)"><td>{{row.work_date||'—'}}</td><td>{{row.project_name||row.address||'未关联工地'}}<small v-if="row.order_number">施工单 {{row.order_number}}</small></td><td>{{bonusEntryKindLabel(row.entry_kind)}}<small>{{row.category}} · {{row.line_name}}</small><small>{{row.rule_key}}</small></td><td class="amount">{{label(row.amount_cents)}}</td><td>{{row.reason||`工程量 ${row.qty_micro} ${row.unit} × ${label(row.rate_cents)}`}}<small v-if="row.reversal_of">更正原记录 {{row.reversal_of}}</small></td><td><button v-if="bonusEntryCanReverse(row.entry_kind,row.reversal_id)" type="button" :disabled="loading||busy" @click="open('reverse-adjustment',row)">更正</button></td></tr>
    <tr v-else-if="tab==='payments'&&!isRow(row)"><td>{{row.settlement_date}}</td><td>{{row.bonus_year}} 年奖金</td><td>奖金结算<small>{{row.note||'—'}}</small></td><td class="amount">{{label(row.amount_cents)}}</td><td>已发 {{label(row.paid_cents)}} · 未发 {{label(row.remaining_cents)}}</td><td><button v-if="row.status==='confirmed'&&Number(row.remaining_cents)>0" type="button" :disabled="loading||busy" @click="open('create-payment',row)">登记部分发放</button><button v-if="row.status==='confirmed'&&Number(row.paid_cents)===0" type="button" :disabled="loading||busy" @click="open('reverse-settlement',row)">撤销结算</button><details v-if="row.payments?.length"><summary>付款记录 {{row.payments.length}}</summary><div v-for="item in row.payments" :key="item.id" class="payment-row"><span>{{item.paid_date||'—'}} · {{label(item.amount_cents)}} · {{item.method||'未注明'}}</span><button v-if="!Boolean(Number(item.reversed))" type="button" :disabled="loading||busy" @click="open('reverse-payment',row,item)">更正付款</button><small v-if="item.reversal_reason">{{item.reversal_reason}}</small></div></details></td></tr>
   </template>
   <tr v-if="!rows.length"><td colspan="6" class="empty">{{loading?'正在读取奖金…':error?'读取失败，请重试':tab==='ledger'?'暂无奖金累计或调整':'暂无奖金结算记录'}}</td></tr>
  </tbody></table></div>
  <div class="toolbar pager"><button type="button" :disabled="loading||page<=1" @click="page--;load()">上一页</button><span>第 {{page}} / {{pageCount}} 页</span><button type="button" :disabled="loading||page>=pageCount" @click="page++;load()">下一页</button></div>
  <Teleport to="body"><div v-if="mode" class="overlay" @keydown.esc="!busy&&close()"><section class="modal bonus-dialog" role="dialog" aria-modal="true" aria-labelledby="bonus-dialog-title"><div class="modal-head"><h2 id="bonus-dialog-title">{{title}}</h2><button type="button" :disabled="busy" aria-label="关闭奖金操作" @click="close">×</button></div><form @submit.prevent="save"><p v-if="mode==='create-adjustment'" class="hint">正数增加奖金，负数冲减奖金。已付款记录保留原流水，调整必须填写原因。</p><p v-if="mode==='create-settlement'" class="hint">本年可结算余额 {{label(summary.bonus_available_cents)}}。结算不会立即记作已付款。</p><p v-if="mode==='create-payment'" class="hint">结算金额 {{label(selectedSettlement?.amount_cents)}} · 已发 {{label(selectedSettlement?.paid_cents)}} · 未发 {{label(selectedSettlement?.remaining_cents)}}。</p><p v-if="error" class="error" role="alert">{{error}}<br>已保留填写内容，可修改后重试。</p><fieldset :disabled="busy"><label v-if="mode==='create-adjustment'">奖金调整金额（元，可正可负） *<input v-model="amount" type="text" inputmode="decimal" maxlength="18" required placeholder="例如 50.00 或 -20.00"></label><label v-if="mode==='create-settlement'">本次结算奖金（元） *<input v-model="amount" type="text" inputmode="decimal" maxlength="18" required></label><label v-if="mode==='create-payment'">本次实际发放（元） *<input v-model="amount" type="text" inputmode="decimal" maxlength="18" required></label><label v-if="mode==='create-adjustment'||mode==='create-settlement'||mode==='create-payment'">{{mode==='create-payment'?'实际发放日期':'日期'}} *<input v-model="date" type="date" required></label><label v-if="mode==='create-payment'">支付方式<input v-model="method" maxlength="40" placeholder="现金、转账等"></label><label v-if="mode==='create-settlement'||mode==='create-payment'">备注<textarea v-model="note" rows="2" maxlength="1000"></textarea></label><label v-if="mode==='create-adjustment'||mode.startsWith('reverse-')">{{mode==='create-adjustment'?'奖金调整原因':'更正原因'}} *<textarea v-model="reason" rows="3" maxlength="1000" required></textarea></label></fieldset><footer><button type="button" :disabled="busy" @click="close">取消</button><button class="primary" type="submit" :disabled="busy">{{busy?'保存中…':'保存奖金记录'}}</button></footer></form></section></div></Teleport>
 </section>
</template>

<style scoped>
.bonus-heading{justify-content:space-between}.bonus-heading h2{font-size:18px;margin:0}.bonus-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;padding:12px 16px;border-block:1px solid #edf0f5}.bonus-summary div{display:flex;flex-direction:column;gap:4px;min-width:0}.bonus-summary span,.bonus-table small{font-size:12px;color:#718097}.bonus-summary strong{font-size:15px;overflow-wrap:anywhere;font-variant-numeric:tabular-nums}.bonus-filters{align-items:end}.bonus-filters label{min-width:150px}.bonus-filters input{display:block;min-width:0;width:100%}.bonus-filters button.active{background:#e8eef8}.hint{color:#667487}.row-count{padding:0 16px 8px;color:#667487}.bonus-table{width:100%;border-collapse:collapse}.bonus-table th,.bonus-table td{padding:9px 10px;border-bottom:1px solid #edf0f5;text-align:left;vertical-align:top}.bonus-table small{display:block;margin-top:3px;overflow-wrap:anywhere}.bonus-table .amount{white-space:nowrap;font-variant-numeric:tabular-nums;font-weight:600}.bonus-table details{margin-top:6px}.bonus-table summary{cursor:pointer}.payment-row{display:flex;gap:8px;align-items:center;flex-wrap:wrap;padding:5px 0}.empty{text-align:center;padding:28px;color:#8793a5}.pager{justify-content:flex-end}.bonus-dialog{width:min(520px,100%);max-height:calc(100dvh - 32px);overflow:auto}.bonus-dialog form{display:grid;gap:14px}.bonus-dialog fieldset{display:grid;gap:12px;border:0;padding:0;margin:0}.bonus-dialog label{display:grid;gap:5px;min-width:0}.bonus-dialog input,.bonus-dialog textarea{width:100%;min-width:0}.bonus-dialog textarea{resize:vertical}.bonus-dialog footer{display:flex;justify-content:flex-end;gap:8px;flex-wrap:wrap}
@media(max-width:700px){.bonus-summary{grid-template-columns:repeat(2,minmax(0,1fr));padding-inline:10px}.bonus-table th,.bonus-table td{padding:7px 6px;font-size:12px}.bonus-table th:nth-child(2),.bonus-table td:nth-child(2){min-width:125px}.bonus-filters label{flex:1 1 100%}.bonus-heading h2{font-size:16px}}
</style>
