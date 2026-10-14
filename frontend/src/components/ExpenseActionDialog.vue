<script setup lang="ts">
import {computed, onBeforeUnmount, ref, watch} from 'vue'
import {api, apiSession, command, SessionChangedError} from '../api'
import {expenseHistoryReverseAction, expenseMoney, parseExpenseAmount, type ExpenseAction, type ExpenseAdvance, type ExpenseClaim, type ExpenseHistory, type ExpensePage, type ExpensePurchase} from '../expenses'

type ActionName=ExpenseAction['action']
type ExpenseRow=ExpenseClaim|ExpenseAdvance|ExpenseHistory|null
const props=defineProps<{action:ActionName;row:ExpenseRow;allowed:boolean}>()
const emit=defineEmits<{close:[];saved:[]}>()
const submit=command()
const today=new Date().toLocaleDateString('sv-SE')
const person=ref(''),date=ref(today),category=ref<'材料采购'|'油费'|'停车费'|'餐费'|'其他'>('材料采购'),amount=ref(''),purpose=ref(''),note=ref(''),reason=ref(''),purchaseId=ref(''),purchaseQuery=ref(''),purchaseRows=ref<ExpensePurchase[]>([]),purchasePage=ref(1),purchasePageSize=ref(50),purchaseTotal=ref(0),purchaseLoading=ref(false),advanceId=ref(''),advanceRows=ref<ExpenseAdvance[]>([]),advancePage=ref(1),advancePageSize=ref(50),advanceTotal=ref(0),selectedAdvance=ref<ExpenseAdvance|null>(null),advanceLoading=ref(false)
const busy=ref(false),error=ref('')
const locked=computed(()=>busy.value||purchaseLoading.value||advanceLoading.value)
const purchasePageCount=computed(()=>Math.max(1,Math.ceil(purchaseTotal.value/purchasePageSize.value)))
const advancePageCount=computed(()=>Math.max(1,Math.ceil(advanceTotal.value/advancePageSize.value)))
const titleMap:Record<ActionName,string>={
 'create-claim':'新增报销','update-claim':'更正报销','confirm-claim':'确认报销金额','void-claim':'撤销报销','pay-claim':'登记实际付款','reverse-payment':'更正实际付款',
 'create-advance':'登记费用预支','correct-advance':'更正预支信息','reverse-advance':'撤销费用预支','allocate-advance':'报账核销预支','reverse-allocation':'撤销预支核销','return-advance':'登记员工退回','reverse-return':'更正员工退回',
}
const title=computed(()=>titleMap[props.action])
const claim=()=>props.row&&'expense_date' in props.row?props.row as ExpenseClaim:null
const advance=()=>props.row&&'advance_date' in props.row?props.row as ExpenseAdvance:null
const history=()=>props.row&&'entity_type' in props.row?props.row as ExpenseHistory:null
const currentAdvance=computed(()=>advanceRows.value.find(row=>row.id===advanceId.value)||selectedAdvance.value)
watch(advanceId,id=>{selectedAdvance.value=advanceRows.value.find(row=>row.id===id)||null})

if(props.action==='update-claim'&&claim()){
 const row=claim()!
 person.value=row.person_name;date.value=row.expense_date;category.value=row.category;amount.value=amountText(row.amount_cents);note.value=row.note||'';purchaseId.value=row.purchase_document_id||''
}
if(props.action==='pay-claim'&&claim())amount.value=amountText(claim()!.due_cents)
if(props.action==='create-advance')date.value=today
if(props.action==='correct-advance'&&advance()){
 const row=advance()!
 person.value=row.person_name;date.value=row.advance_date;purpose.value=row.purpose;note.value=row.note||''
}
if(props.action==='return-advance')date.value=today
if(props.action==='allocate-advance'){
 date.value=today
 person.value=claim()?.person_name||''
 void loadAdvances()
}

function amountText(value:unknown){
 const cents=BigInt(String(value??0))
 return `${cents/100n}.${String(cents%100n).padStart(2,'0')}`
}
function purchaseLabel(row:ExpensePurchase){
 const items=row.line_items.map(item=>{
  if(!item||typeof item!=='object')return ''
  const record=item as Record<string,unknown>
  return String(record.product_name||record.product||record.name||record.item_name||'')
 }).filter(Boolean).slice(0,3).join('、')
 return `${row.work_date} · ${expenseMoney(row.total_cents)}${items?' · '+items:''}${row.note?' · '+row.note:''}`
}
async function loadPurchases(next=purchasePage.value){
 if(!props.allowed)return
 const session=apiSession.capture();purchaseLoading.value=true;error.value=''
 try{
  const result=await api<ExpensePage<ExpensePurchase>>('/expense-purchases?'+new URLSearchParams({q:purchaseQuery.value.trim(),page:String(next)}))
  if(!apiSession.current(session)||!props.allowed)return
  purchaseRows.value=result.rows;purchasePage.value=Number(result.page)||next;purchasePageSize.value=Number(result.page_size)||50;purchaseTotal.value=Number(result.total)||0
 }catch(e){if(apiSession.current(session)&&props.allowed&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(apiSession.current(session))purchaseLoading.value=false}
}
function searchPurchases(){purchasePage.value=1;void loadPurchases(1)}
async function loadAdvances(next=advancePage.value){
 if(!props.allowed||!person.value.trim())return
 const session=apiSession.capture();advanceLoading.value=true;error.value=''
 try{
  const result=await api<ExpensePage<ExpenseAdvance>>('/expenses?'+new URLSearchParams({tab:'advances',q:person.value.trim(),page:String(next)}))
  if(!apiSession.current(session)||!props.allowed)return
  advanceRows.value=result.rows.filter(row=>row.remaining_cents>0&&!['voided','已撤销'].includes(row.status)&&row.person_name.trim()===person.value.trim())
  advancePage.value=Number(result.page)||next;advancePageSize.value=Number(result.page_size)||50;advanceTotal.value=Number(result.total)||0
  if(advanceId.value)selectedAdvance.value=advanceRows.value.find(row=>row.id===advanceId.value)||selectedAdvance.value
 }catch(e){if(apiSession.current(session)&&props.allowed&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(apiSession.current(session))advanceLoading.value=false}
}

function buildAction():ExpenseAction{
 const row=claim(),advanceRow=advance(),entry=history()
 const amountCents=()=>parseExpenseAmount(amount.value)
 const why=reason.value.trim()
 switch(props.action){
  case 'create-claim':
   if(!person.value.trim())throw new Error('请填写报销人')
   if(!date.value)throw new Error('请填写费用日期')
   return {action:props.action,person_name:person.value.trim(),expense_date:date.value,category:category.value,amount_cents:amountCents(),note:note.value.trim(),...(category.value==='材料采购'&&purchaseId.value?{purchase_document_id:purchaseId.value}:{})}
  case 'update-claim':
   if(!row)throw new Error('报销记录已失效，请刷新后重试')
   if(!why)throw new Error('请填写更正原因')
   if(!person.value.trim()||!date.value)throw new Error('请填写报销人和费用日期')
   return {action:props.action,id:row.id,version:row.version,person_name:person.value.trim(),expense_date:date.value,category:category.value,amount_cents:amountCents(),note:note.value.trim(),...(category.value==='材料采购'&&purchaseId.value?{purchase_document_id:purchaseId.value}:{}),reason:why}
  case 'confirm-claim': if(!row)throw new Error('缺少报销记录');return {action:props.action,id:row.id,version:row.version}
  case 'void-claim': if(!row)throw new Error('缺少报销记录');if(!why)throw new Error('请填写撤销原因');return {action:props.action,id:row.id,version:row.version,reason:why}
  case 'pay-claim':
   if(!row)throw new Error('缺少报销记录')
   if(!date.value)throw new Error('请填写实际付款日期')
   if(amountCents()>BigInt(row.due_cents))throw new Error(`本次付款不能超过剩余应付 ${expenseMoney(row.due_cents)}`)
   return {action:props.action,id:row.id,version:row.version,amount_cents:Number(amountCents()),paid_date:date.value,note:note.value.trim()}
  case 'reverse-payment': if(!entry)throw new Error('缺少付款历史');return expenseHistoryReverseAction(entry,why)
  case 'create-advance':
   if(!person.value.trim()||!date.value||!purpose.value.trim())throw new Error('请填写预支人、实际付款日期和用途')
   return {action:props.action,person_name:person.value.trim(),advance_date:date.value,amount_cents:amountCents(),purpose:purpose.value.trim(),note:note.value.trim()}
  case 'correct-advance':
   if(!advanceRow)throw new Error('缺少预支记录')
   if(!why)throw new Error('请填写更正原因')
   if(!date.value||!purpose.value.trim())throw new Error('请填写日期和用途')
   return {action:props.action,id:advanceRow.id,version:advanceRow.version,advance_date:date.value,purpose:purpose.value.trim(),note:note.value.trim(),reason:why}
  case 'reverse-advance': if(!advanceRow)throw new Error('缺少预支记录');if(!why)throw new Error('请填写撤销原因');return {action:props.action,id:advanceRow.id,version:advanceRow.version,reason:why}
  case 'allocate-advance':
   if(!row||!currentAdvance.value)throw new Error('请选择有余额的对应人员费用预支')
   if(!date.value)throw new Error('请填写核销日期')
   if(amountCents()>BigInt(row.due_cents))throw new Error(`核销不能超过报销剩余应付 ${expenseMoney(row.due_cents)}`)
   if(amountCents()>BigInt(currentAdvance.value.remaining_cents))throw new Error(`核销不能超过预支余额 ${expenseMoney(currentAdvance.value.remaining_cents)}`)
   return {action:props.action,claim_id:row.id,claim_version:row.version,advance_id:currentAdvance.value.id,advance_version:currentAdvance.value.version,amount_cents:Number(amountCents()),applied_date:date.value,note:note.value.trim()}
  case 'reverse-allocation': if(!entry)throw new Error('缺少核销历史');return expenseHistoryReverseAction(entry,why)
  case 'return-advance':
   if(!advanceRow)throw new Error('缺少预支记录')
   if(!date.value)throw new Error('请填写实际退回日期')
   if(amountCents()>BigInt(advanceRow.remaining_cents))throw new Error(`退回金额不能超过未核销余额 ${expenseMoney(advanceRow.remaining_cents)}`)
   return {action:props.action,advance_id:advanceRow.id,advance_version:advanceRow.version,amount_cents:Number(amountCents()),returned_date:date.value,note:note.value.trim()}
  case 'reverse-return': if(!entry)throw new Error('缺少退回历史');return expenseHistoryReverseAction(entry,why)
 }
}
async function save(){
 if(busy.value||!props.allowed)return
 error.value=''
 try{
  const payload=buildAction(),session=apiSession.capture();busy.value=true
  await submit('/expense-action',payload)
  if(apiSession.current(session)&&props.allowed)emit('saved')
 }catch(e){if(!(e instanceof SessionChangedError)&&props.allowed)error.value=(e as Error).message}
 finally{busy.value=false}
}
watch(()=>props.allowed,allowed=>{
 if(allowed)return
  person.value='';amount.value='';note.value='';reason.value='';purpose.value='';purchaseRows.value=[];advanceRows.value=[];purchaseId.value='';advanceId.value='';selectedAdvance.value=null;error.value=''
})
onBeforeUnmount(()=>{person.value='';amount.value='';note.value='';reason.value='';purchaseRows.value=[];advanceRows.value=[]})
</script>

<template>
 <div class="overlay expense-overlay" @keydown.esc="!locked&&emit('close')">
  <section class="modal wide expense-dialog" role="dialog" aria-modal="true" :aria-label="title">
   <div class="modal-head"><h2>{{title}}</h2><button type="button" :disabled="busy" aria-label="关闭操作窗口" @click="emit('close')">×</button></div>
   <form @submit.prevent="save" autocomplete="off">
    <p v-if="action==='confirm-claim'" class="notice">确认金额只表示报销事实已审核，不代表老板已经付款。实际付款请使用“登记实际付款”。</p>
    <p v-if="action==='create-advance'" class="notice">请只登记已经实际交给员工的费用预支；工资预支在工资管理中单独处理。</p>
    <p v-if="action==='allocate-advance'&&claim()" class="notice">本次核销把预支余额用于这笔已确认费用，不会产生新的付款，也不会增加商品库存。报销待付 {{expenseMoney(claim()!.due_cents)}}。</p>
    <p v-if="action==='pay-claim'&&claim()" class="notice">本窗口仅登记已经实际付给员工的钱。已确认 {{expenseMoney(claim()!.amount_cents)}} · 预支核销 {{expenseMoney(claim()!.advance_applied_cents)}} · 已实际支付 {{expenseMoney(claim()!.paid_cents)}} · 尚欠 {{expenseMoney(claim()!.due_cents)}}。</p>
    <p v-if="error" class="error" role="alert">{{error}}<small>已保留本次填写，可以修改后重试。</small></p>
    <fieldset :disabled="locked||!allowed">
     <div v-if="['create-claim','update-claim'].includes(action)" class="form-grid">
      <label>报销人员 *<input v-model="person" maxlength="120" required></label>
      <label>费用日期 *<input v-model="date" type="date" required></label>
      <label>费用类型 *<select v-model="category"><option v-for="item in ['材料采购','油费','停车费','餐费','其他']" :key="item" :value="item">{{item}}</option></select></label>
      <label>费用金额（元） *<input v-model="amount" inputmode="decimal" maxlength="20" placeholder="例如：200.00" required></label>
      <label class="expense-wide">备注<textarea v-model="note" maxlength="1000" rows="2"></textarea></label>
      <div v-if="category==='材料采购'" class="expense-wide purchase-lookup">
       <label>关联已有进货单（可选）<input v-model="purchaseQuery" maxlength="120" placeholder="按单号备注或日期搜索" @keydown.enter.prevent="searchPurchases"></label>
       <button type="button" :disabled="locked" @click="searchPurchases">{{purchaseLoading?'正在查询…':'查询进货单'}}</button>
       <select v-model="purchaseId" aria-label="关联已有进货单"><option value="">不关联进货单</option><option v-if="purchaseId&&!purchaseRows.some(row=>row.id===purchaseId)" :value="purchaseId">已关联进货单 {{claim()?.purchase_number||purchaseId}}</option><option v-for="row in purchaseRows" :key="row.id" :value="row.id">{{purchaseLabel(row)}} · 可关联 {{expenseMoney(row.available_cents??row.total_cents)}}</option></select>
       <div class="lookup-pages"><button type="button" :disabled="locked||purchasePage<=1" @click="loadPurchases(purchasePage-1)">上一页</button><span>{{purchaseTotal?`第 ${purchasePage} / ${purchasePageCount} 页 · ${purchaseTotal} 笔进货`:'请搜索后选择进货单'}}</span><button type="button" :disabled="locked||purchasePage>=purchasePageCount" @click="loadPurchases(purchasePage+1)">下一页</button></div>
       <small>关联仅核对已登记进货金额，不会再次入库或重复计采购金额。</small>
      </div>
      <label v-if="action==='update-claim'" class="expense-wide">更正原因 *<textarea v-model="reason" maxlength="1000" rows="2" required></textarea></label>
     </div>
     <div v-else-if="action==='create-advance'" class="form-grid">
      <label>预支人员 *<input v-model="person" maxlength="120" required></label><label>实际付款日期 *<input v-model="date" type="date" required></label>
      <label>实际预支金额（元） *<input v-model="amount" inputmode="decimal" maxlength="20" required></label><label>用途 *<input v-model="purpose" maxlength="300" required></label>
      <label class="expense-wide">备注<textarea v-model="note" maxlength="1000" rows="2"></textarea></label>
     </div>
     <div v-else-if="action==='correct-advance'" class="form-grid">
      <label>实际付款日期 *<input v-model="date" type="date" required></label><label>用途 *<input v-model="purpose" maxlength="300" required></label>
      <label class="expense-wide">备注<textarea v-model="note" maxlength="1000" rows="2"></textarea></label><label class="expense-wide">更正原因 *<textarea v-model="reason" maxlength="1000" rows="2" required></textarea></label>
      <p class="expense-wide hint">原实际付款金额 {{expenseMoney(advance()?.amount_cents)}} 保持不变。金额登记错误且尚未使用时，请撤销原预支后重新登记。</p>
     </div>
     <div v-else-if="action==='pay-claim'||action==='return-advance'||action==='allocate-advance'" class="form-grid">
      <label v-if="action==='allocate-advance'" class="expense-wide">选择预支记录 *<select v-model="advanceId" required><option value="">请选择该人员尚有余额的预支</option><option v-if="advanceId&&selectedAdvance&&!advanceRows.some(row=>row.id===advanceId)" :value="advanceId">{{selectedAdvance.advance_date}} · {{selectedAdvance.person_name}} · 剩余 {{expenseMoney(selectedAdvance.remaining_cents)}} · {{selectedAdvance.purpose}}</option><option v-for="row in advanceRows" :key="row.id" :value="row.id">{{row.advance_date}} · {{row.person_name}} · 剩余 {{expenseMoney(row.remaining_cents)}} · {{row.purpose}}</option></select></label>
      <div v-if="action==='allocate-advance'" class="expense-wide lookup-pages"><button type="button" :disabled="locked||advancePage<=1" @click="loadAdvances(advancePage-1)">上一页预支</button><span>{{advanceTotal?`第 ${advancePage} / ${advancePageCount} 页 · ${advanceTotal} 笔有余额预支`:'该人员没有可核销预支'}}</span><button type="button" :disabled="locked||advancePage>=advancePageCount" @click="loadAdvances(advancePage+1)">下一页预支</button></div>
      <p v-if="action==='allocate-advance'&&!advanceLoading&&!advanceTotal" class="expense-wide hint">{{person?'该人员目前没有可核销的费用预支余额。':'请先填写报销人员。'}}</p>
      <label>金额（元） *<input v-model="amount" inputmode="decimal" maxlength="20" required></label><label>{{action==='pay-claim'?'实际付款日期':action==='return-advance'?'实际退回日期':'实际核销日期'}} *<input v-model="date" type="date" required></label>
      <label v-if="action!=='pay-claim'" class="expense-wide">备注<textarea v-model="note" maxlength="1000" rows="2"></textarea></label>
     </div>
    </fieldset>
    <div v-if="action==='confirm-claim'" class="expense-confirm"><p>报销人员：{{claim()?.person_name||'—'}} · {{claim()?.expense_date||''}} · {{claim()?.category||''}}</p><strong>确认费用 {{expenseMoney(claim()?.amount_cents)}}</strong><p>确认后不会记录实际付款；如需核销已有费用预支，请在确认后单独操作。</p></div>
    <div v-if="history()&&['reverse-payment','reverse-allocation','reverse-return'].includes(action)" class="expense-confirm"><p>{{history()!.entity_type}} · {{history()!.entity_id}} · {{history()!.created_at}}</p><strong>原事件金额 {{expenseMoney(history()!.amount_cents)}}</strong><p>本操作只新增反向记录，历史事实仍会保留。</p></div>
    <label v-if="['void-claim','reverse-payment','reverse-allocation','reverse-return','reverse-advance'].includes(action)" class="reason-field">{{action==='void-claim'||action==='reverse-advance'?'撤销原因':'更正原因'}} *<textarea v-model="reason" maxlength="1000" rows="3" required placeholder="说明更正缘由；原记录会保留"></textarea></label>
    <footer><button type="button" :disabled="busy" @click="emit('close')">取消</button><button class="primary" type="submit" :disabled="locked||!allowed||action==='allocate-advance'&&!advanceRows.length">{{busy?'正在保存…':action==='pay-claim'?'登记实际付款':action==='return-advance'?'登记实际退回':action==='allocate-advance'?'确认预支核销':action==='confirm-claim'?'确认报销金额':title}}</button></footer>
   </form>
  </section>
 </div>
</template>

<style scoped>
.expense-overlay{overflow:auto}.expense-dialog{min-width:0}.expense-dialog fieldset{min-width:0}.expense-dialog label{min-width:0}.expense-dialog input,.expense-dialog select,.expense-dialog textarea{width:100%;min-width:0}.expense-wide{grid-column:1/-1}.purchase-lookup{display:grid;grid-template-columns:minmax(180px,1fr) auto;gap:8px;align-items:end;min-width:0}.purchase-lookup select,.purchase-lookup small,.purchase-lookup .lookup-pages{grid-column:1/-1}.lookup-pages{display:flex;align-items:center;justify-content:space-between;gap:8px;min-width:0}.lookup-pages span{flex:1;text-align:center;color:#63758b}.reason-field{display:block;margin-top:12px}.reason-field textarea{width:100%;resize:vertical}.expense-confirm{margin-top:14px;padding:12px;border-left:3px solid #4a78bc;background:#f5f8fc;display:grid;gap:6px}.expense-confirm strong{font-size:18px}.expense-confirm p:last-child{color:#63758b}.expense-dialog footer{flex-wrap:wrap}
@media(max-width:600px){.expense-dialog{width:100%}.expense-dialog .form-grid{grid-template-columns:minmax(0,1fr)}.expense-wide{grid-column:auto}.purchase-lookup{grid-template-columns:minmax(0,1fr)}.purchase-lookup select,.purchase-lookup small,.purchase-lookup .lookup-pages{grid-column:auto}.lookup-pages{flex-wrap:wrap}.lookup-pages span{order:-1;flex-basis:100%}.expense-dialog footer button{flex:1}}
</style>
