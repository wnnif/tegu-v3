<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {api,apiSession,command,SessionChangedError} from '../api'
import {parseReceiptAmount} from '../settlements'
import {amountInput,financeEvent,financeHistoryAmounts,receiptMoney,type ReceiptDetail} from '../receipts'
import ReferenceSelect from './ReferenceSelect.vue'
import ReceiptAllocationEditor from './ReceiptAllocationEditor.vue'
const props=defineProps<{receiptId?:string;companyId:string;canManage:boolean;allowed:boolean}>(),emit=defineEmits<{close:[];saved:[detail:ReceiptDetail]}>()
const detail=ref<ReceiptDetail|null>(null),id=ref(props.receiptId||''),action=ref<'create'|'correct'|'void'|'allocation'|''>(props.receiptId?'':'create')
const company=ref(props.companyId),date=ref(new Date().toLocaleDateString('sv-SE')),amount=ref(''),method=ref(''),voucher=ref(''),note=ref(''),reason=ref('')
const busy=ref(false),loading=ref(false),error=ref(''),notice=ref(''),historyPage=ref(1)
const locked=computed(()=>busy.value||loading.value),active=computed(()=>detail.value?.receipt.status==='active')
let generation=0,alive=true,submit=command()
function fill(){if(!detail.value)return;const row=detail.value.receipt;company.value=row.company_id;date.value=row.received_at;amount.value=amountInput(row.amount_cents);method.value=row.method;voucher.value=row.voucher_no;note.value=row.note;reason.value=''}
async function load(next=historyPage.value){
 if(!id.value||!props.allowed)return
 const gen=++generation,session=apiSession.capture();loading.value=true;error.value=''
 try{const result=await api<ReceiptDetail>('/company-receipt?'+new URLSearchParams({id:id.value,history_page:String(next)}));if(!alive||gen!==generation||!apiSession.current(session))return;detail.value=result;historyPage.value=result.history_page||next}
 catch(e){if(alive&&gen===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(alive&&gen===generation)loading.value=false}
}
function begin(next:'correct'|'void'|'allocation'){
 if(locked.value||!props.allowed||!active.value||(next!=='allocation'&&!props.canManage))return
 fill();action.value=next;notice.value='';error.value='';submit=command()
}
async function saved(result:ReceiptDetail){detail.value=result;id.value=result.receipt.id;action.value='';reason.value='';historyPage.value=result.history_page||1;notice.value='已保存，实际到账与分配余额已更新';emit('saved',result)}
async function save(){
 if(locked.value||!props.allowed||!['create','correct','void'].includes(action.value)||(action.value!=='create'&&!props.canManage))return
 const gen=generation,session=apiSession.capture();busy.value=true;error.value='';notice.value=''
 try{
  if(action.value!=='create'&&!reason.value.trim())throw new Error('请填写更正或作废原因')
  const body=action.value==='void'?{id:id.value,version:detail.value?.receipt.version,action:'void',reason:reason.value.trim()}:{company_id:company.value,received_at:date.value,amount_cents:parseReceiptAmount(String(amount.value)),method:method.value.trim(),voucher_no:voucher.value.trim(),note:note.value.trim(),...(action.value==='correct'?{id:id.value,version:detail.value?.receipt.version,action:'correct',reason:reason.value.trim()}:{})}
  if(action.value!=='void'&&!company.value)throw new Error('请选择付款公司')
  if(action.value!=='void'&&!date.value)throw new Error('请填写实际到账日期')
  if(action.value!=='void'&&!method.value.trim())throw new Error('请填写收款方式')
  const result=await submit(action.value==='create'?'/company-receipt':'/company-receipt-action',body) as ReceiptDetail
  if(alive&&gen===generation&&apiSession.current(session)&&props.allowed)await saved(result)
 }catch(e){if(alive&&gen===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(alive&&apiSession.current(session))busy.value=false}
}
watch(()=>props.allowed,()=>{++generation;detail.value=null;company.value='';amount.value='';method.value='';voucher.value='';note.value='';reason.value='';action.value='';busy.value=false;loading.value=false})
watch(()=>props.canManage,()=>{if(!props.canManage&&['correct','void'].includes(action.value)){action.value='';reason.value='';amount.value='';company.value=''}})
if(props.receiptId)void load(1)
onBeforeUnmount(()=>{alive=false;++generation;detail.value=null;amount.value='';note.value='';reason.value=''})
</script>
<template>
 <div class="overlay"><section class="modal wide receipt-dialog" role="dialog" aria-modal="true" aria-label="收款详情">
  <div class="modal-head"><h2>{{id?'收款详情 / 分配 / 历史':'登记实际收款'}}</h2><button type="button" :disabled="busy||action==='allocation'" @click="emit('close')">关闭</button></div>
  <div class="dialog-body">
   <p v-if="loading" role="status">正在读取收款…</p><div v-if="error" class="error" role="alert">{{error}}<button v-if="id" type="button" :disabled="locked||action==='allocation'" @click="load()">刷新收款版本</button></div><p v-if="notice" class="notice" role="status">{{notice}}</p>
   <template v-if="detail"><h3>{{detail.receipt.company_name}} · {{detail.receipt.received_at}} <span class="tag">{{active?'有效收款':'已作废'}}</span></h3><div class="receipt-totals"><div>实际到账<strong>{{receiptMoney(detail.receipt.amount_cents)}}</strong></div><div>已分配<strong>{{receiptMoney(detail.receipt.allocated_cents)}}</strong></div><div>未分配<strong>{{receiptMoney(detail.receipt.unallocated_cents)}}</strong></div></div><p class="hint">{{detail.receipt.method||'未填写方式'}} · 凭证 {{detail.receipt.voucher_no||'未填写'}} · 登记人 {{detail.receipt.created_by_name||'未记录'}}<br>{{detail.receipt.note}}</p><p v-if="!active" class="notice">本笔收款已作废，不再计入实际到账。原金额和操作历史仍保留。{{detail.receipt.void_reason}}</p>
   <div v-if="!action" class="toolbar"><button v-if="active" type="button" :disabled="locked||!allowed" @click="begin('allocation')">分配 / 调整收款</button><button v-if="active&&canManage" type="button" :disabled="locked||!allowed" @click="begin('correct')">更正登记</button><button v-if="active&&canManage" type="button" :disabled="locked||!allowed" @click="begin('void')">作废错误收款</button><button type="button" :disabled="locked" @click="load()">刷新收款</button></div>
   </template>
   <form v-if="action==='create'||action==='correct'||action==='void'" @submit.prevent="save">
    <h3>{{action==='create'?'收到钱后先登记':action==='correct'?'更正实际收款':'作废登记错误的收款'}}</h3><fieldset :disabled="locked||!allowed"><div v-if="action!=='void'" class="form-grid"><ReferenceSelect v-model="company" entity="companies" label="付款公司" empty-label="请选择公司" :disabled="locked" :required="true"/><label>实际到账日期<input v-model="date" type="date" required></label><label>到账金额（元）<input v-model="amount" inputmode="decimal" maxlength="20" required></label><label>收款方式<input v-model="method" maxlength="40" placeholder="转账、现金、微信等" required></label><label>凭证号（按需）<input v-model="voucher" maxlength="100"></label><label>备注（按需）<textarea v-model="note" maxlength="1000" rows="2"></textarea></label></div><label v-if="action!=='create'">{{action==='void'?'作废原因':'更正原因'}}<textarea v-model="reason" maxlength="1000" required rows="2"></textarea></label></fieldset>
    <p class="hint">{{action==='create'?'不需要先有工地或交票。保存后全额记为未分配款，可随时分配。':action==='correct'?'减少到账金额会同步缩减分配；更换付款公司会解除原公司全部分配。请核对真实到账凭证。':'仅用于金额、公司登记错误或重复登记。交票作废、施工减价应调整分配，真实收到的钱仍应保留。'}}</p><div class="toolbar"><button class="primary" :disabled="locked||!allowed">{{busy?'正在保存…':action==='void'?'确认作废错误收款':'保存实际收款'}}</button><button type="button" :disabled="busy" @click="action==='create'?emit('close'):action=''">取消</button></div>
   </form>
   <ReceiptAllocationEditor v-if="action==='allocation'&&detail" :detail="detail" :allowed="allowed" @saved="saved" @cancel="action=''"/>
   <template v-if="detail&&!action"><h3>当前有效分配</h3><div class="allocation-list"><article v-for="row in detail.allocations" :key="row.id"><strong>{{row.address}}<small>{{row.number}} · {{row.batch_number||'未关联交票批次'}}</small></strong><span>{{receiptMoney(row.amount_cents)}}</span></article><p v-if="!detail.allocations.length" class="hint">尚未分配，可作为该公司的预收款；公司真正待收仍会扣除这笔实际到账。</p></div><h3>操作历史</h3><p class="hint">原登记、分配、更正及收费变动后的自动调整均保留；作废交票不会删除这笔真实到账。</p><article v-for="row in detail.history" :key="row.version" class="history-row"><strong>第 {{row.version}} 版 · {{financeEvent(row.event)}}</strong><small>{{row.created_at}} · {{row.actor_name||'未记录人员'}}</small><p v-if="financeHistoryAmounts(row.snapshot)" class="history-amounts">{{financeHistoryAmounts(row.snapshot)}}</p><p>{{row.reason||'未填写原因'}}</p></article><p v-if="!detail.history.length" class="hint">暂无历史记录。</p><div class="toolbar"><button type="button" :disabled="historyPage<=1||locked" @click="load(historyPage-1)">上一页历史</button><span>共 {{detail.history_total||detail.history.length}} 条 · 第 {{historyPage}} 页</span><button type="button" :disabled="historyPage*(detail.history_page_size||50)>=(detail.history_total||detail.history.length)||locked" @click="load(historyPage+1)">下一页历史</button></div></template>
  </div>
 </section></div>
</template>
<style scoped>.receipt-dialog{min-width:0}.dialog-body{padding:16px;min-width:0}.dialog-body h3{overflow-wrap:anywhere}.receipt-totals{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px}.receipt-totals>div{padding:12px;background:#f3f7fc;border:1px solid #dfe7f1;border-radius:6px;color:#63758b}.receipt-totals strong{display:block;margin-top:5px;font-size:19px;color:#203d61;overflow-wrap:anywhere}.receipt-dialog .toolbar{padding:12px 0;flex-wrap:wrap}.receipt-dialog input,.receipt-dialog textarea{box-sizing:border-box;min-width:0;max-width:100%}.receipt-dialog .form-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.hint{color:#63758b;font-size:13px;line-height:1.6;overflow-wrap:anywhere}.allocation-list article{display:flex;gap:12px;justify-content:space-between;padding:10px 0;border-bottom:1px solid #e5eaf1}.allocation-list strong{min-width:0;overflow-wrap:anywhere}.history-row{border-top:1px solid #e5eaf1;padding:10px 0;overflow-wrap:anywhere}.history-row p{margin:6px 0}@media(max-width:600px){.dialog-body{padding:12px}.receipt-dialog .form-grid{grid-template-columns:minmax(0,1fr)}.receipt-totals{grid-template-columns:minmax(0,1fr)}.toolbar span{flex-basis:100%}}</style>
