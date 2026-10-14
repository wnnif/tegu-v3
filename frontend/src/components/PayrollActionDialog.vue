<script setup lang="ts">
import {computed, ref} from 'vue'
import {api, command} from '../api'
import {buildPayrollAction, maxAdvanceAllocation, payrollCents, payrollMoney, payrollSettlementCanConfirm, parsePayrollAmount, type PayrollActionDraft, type PayrollActionPayload, type PayrollAdjustmentRow, type PayrollAdvanceRow, type PayrollDeductionRow, type PayrollPaymentRow, type PayrollProjectPage, type PayrollRow, type PayrollSettlementRow, type PayrollSettlementSelection} from '../payroll'

type Action = PayrollActionDraft['action']
const props=defineProps<{action:Action;workerId:string;row?:PayrollRow|null;payment?:PayrollPaymentRow|null;selection?:PayrollSettlementSelection|null}>()
const emit=defineEmits<{close:[];saved:[]}>()
const submit=command(),busy=ref(false),error=ref(''),amount=ref(''),date=ref(new Date().toLocaleDateString('sv-SE')),reason=ref(''),legalBasis=ref(''),note=ref(''),method=ref(''),projectId=ref(''),projectQuery=ref(''),projects=ref<PayrollProjectPage['rows']>([]),projectLoading=ref(false)
const mode=props.action
const title:Record<Action,string>={
 'create-adjustment':'登记工资补发','reverse-adjustment':'更正工资调整','create-deduction':'登记待确认扣款','edit-deduction':'更正待确认扣款','confirm-deduction':'确认扣款依据','reverse-deduction':'撤销扣款','create-advance':'登记工资预支','edit-advance':'更正工资预支','reverse-advance':'更正工资预支','create-settlement':'确认工资结算','reverse-settlement':'撤销工资结算','create-payment':'登记实际工资发放','reverse-payment':'更正实际工资发放',
}
const heading=computed(()=>mode==='edit-deduction'&&(props.row as PayrollDeductionRow|null)?.status==='confirmed'?'更正已确认扣款':title[mode])
function centsInput(value:unknown){const raw=payrollCents(value),negative=raw<0n,absolute=negative?-raw:raw;return (negative?'-':'')+(absolute/100n).toString()+'.'+String(absolute%100n).padStart(2,'0')}
function parseAmountOrZero(input:string){try{return BigInt(parsePayrollAmount(input,{allowZero:true}))}catch{return 0n}}
function rowAmount(row:PayrollRow|null|undefined){return row&&'amount_cents' in row?row.amount_cents:''}
function rowDate(row:PayrollRow|null|undefined){
 if(!row)return ''
 if('effective_date' in row)return row.effective_date
 if('deduction_date' in row)return row.deduction_date
 if('advance_date' in row)return row.advance_date
 if('settlement_date' in row)return row.settlement_date
 return ''
}
function rowReason(row:PayrollRow|null|undefined){return row?.reason||('note' in (row||{})?String((row as PayrollAdvanceRow).note||''):'')}
if(['edit-deduction','edit-advance'].includes(mode)){amount.value=centsInput(rowAmount(props.row));date.value=rowDate(props.row)||date.value;projectId.value=props.row?.project_id||'';reason.value=rowReason(props.row);note.value=rowReason(props.row);if(mode==='edit-deduction')legalBasis.value=(props.row as PayrollDeductionRow|null)?.legal_basis||''}
if(mode==='create-adjustment'||mode==='create-deduction'||mode==='create-advance')date.value=new Date().toLocaleDateString('sv-SE')
if(mode==='reverse-adjustment'||mode==='reverse-deduction'||mode==='reverse-advance'||mode==='reverse-settlement'||mode==='reverse-payment')reason.value=''
if(mode==='create-payment'&&props.row&&'remaining_cents' in props.row)amount.value=centsInput(props.row.remaining_cents)
const settlement=computed(()=>props.row&&'payable_cents' in props.row?props.row as PayrollSettlementRow:null)
const selection=computed(()=>props.selection||{wages:[],adjustments:[],deductions:[],advances:[]})
const advanceMaximum=computed(()=>maxAdvanceAllocation(selection.value))
const baseAmount=computed(()=>{
 const selected=selection.value
 const wages=selected.wages.reduce((sum,row)=>sum+payrollCents(row.wage_cents)+payrollCents(row.delivery_wage_cents),0n)
 const adjustments=selected.adjustments.reduce((sum,row)=>sum+payrollCents(row.amount_cents),0n)
 const deductions=selected.deductions.reduce((sum,row)=>sum+payrollCents(row.amount_cents),0n)
 return wages+adjustments-deductions
})
const wageTotal=computed(()=>selection.value.wages.reduce((sum,row)=>sum+payrollCents(row.wage_cents)+payrollCents(row.delivery_wage_cents),0n))
const adjustmentTotal=computed(()=>selection.value.adjustments.reduce((sum,row)=>sum+payrollCents(row.amount_cents),0n))
const deductionTotal=computed(()=>selection.value.deductions.reduce((sum,row)=>sum+payrollCents(row.amount_cents),0n))
if(mode==='create-settlement')amount.value=centsInput(advanceMaximum.value)
const requestedAdvance=computed(()=>parseAmountOrZero(amount.value))
const settlementPayable=computed(()=>baseAmount.value-requestedAdvance.value)
const actionLabel=computed(()=>mode==='confirm-deduction'?'确认扣款依据':mode==='create-settlement'?'确认结算（不代表已付款）':mode==='create-payment'?'登记实际发放':mode.startsWith('reverse-')?'保存更正记录':'保存记录')
const originalAmount=()=>props.row&&'remaining_cents' in props.row?props.row.remaining_cents:0
const canSettle=computed(()=>!!props.selection&&payrollSettlementCanConfirm(selection.value))
async function searchProjects(){
 projectLoading.value=true;error.value=''
 try{const result=await api<PayrollProjectPage>('/payroll-projects?'+new URLSearchParams({q:projectQuery.value.trim(),page:'1'}));projects.value=result.rows}
 catch(e){error.value=e instanceof Error?e.message:'读取工地失败'}
 finally{projectLoading.value=false}
}
async function save(){
 if(busy.value)return
 error.value=''
 try{
  let draft:PayrollActionDraft
  const row=props.row
  switch(mode){
   case 'create-adjustment': draft={action:mode,worker_id:props.workerId,amount:amount.value,effective_date:date.value,project_id:projectId.value,reason:reason.value};break
   case 'reverse-adjustment': draft={action:mode,id:(row as PayrollAdjustmentRow|undefined)?.id||'',version:Number(row?.version||0),reason:reason.value};break
   case 'create-deduction': draft={action:mode,worker_id:props.workerId,amount:amount.value,deduction_date:date.value,project_id:projectId.value,reason:reason.value};break
   case 'edit-deduction': {const deduction=row as PayrollDeductionRow|undefined;draft={action:mode,id:deduction?.id||'',version:Number(row?.version||0),confirmed:deduction?.status==='confirmed',amount:amount.value,deduction_date:date.value,project_id:projectId.value,reason:reason.value,legal_basis:legalBasis.value};break}
   case 'confirm-deduction': draft={action:mode,id:(row as PayrollDeductionRow|undefined)?.id||'',version:Number(row?.version||0),legal_basis:legalBasis.value};break
   case 'reverse-deduction': draft={action:mode,id:(row as PayrollDeductionRow|undefined)?.id||'',version:Number(row?.version||0),reason:reason.value};break
   case 'create-advance': draft={action:mode,worker_id:props.workerId,amount:amount.value,advance_date:date.value,project_id:projectId.value,note:note.value};break
   case 'edit-advance': draft={action:mode,id:(row as PayrollAdvanceRow|undefined)?.id||'',version:Number(row?.version||0),amount:amount.value,advance_date:date.value,project_id:projectId.value,note:note.value};break
   case 'reverse-advance': draft={action:mode,id:(row as PayrollAdvanceRow|undefined)?.id||'',version:Number(row?.version||0),reason:reason.value};break
   case 'create-settlement': draft={action:mode,worker_id:props.workerId,settlement_date:date.value,note:note.value,selection:selection.value,advance_allocation:amount.value};break
   case 'reverse-settlement': draft={action:mode,id:(row as PayrollSettlementRow|undefined)?.id||'',version:Number(row?.version||0),reason:reason.value};break
   case 'create-payment': {
    const source=settlement.value
    if(!source)throw new Error('缺少工资结算记录，请刷新后重试')
    const paymentCents=parsePayrollAmount(amount.value)
    if(BigInt(paymentCents)>payrollCents(source.remaining_cents))throw new Error(`本次发放不能超过 ${payrollMoney(source.remaining_cents)}`)
    draft={action:mode,settlement_id:source.id,settlement_version:Number(source.version||0),amount:amount.value,paid_date:date.value,method:method.value,note:note.value};break
   }
   case 'reverse-payment': draft={action:mode,settlement_id:settlement.value?.id||'',id:props.payment?.id||'',version:Number(props.payment?.version||0),reason:reason.value};break
  }
  if(mode==='create-settlement'&&!canSettle.value)throw new Error('请选择至少一笔工资、调整或扣款来源；预支只能作为抵扣项')
  if(mode==='create-payment'&&(!settlement.value||payrollCents(originalAmount())<=0n))throw new Error('该结算没有剩余待发金额')
  const payload:PayrollActionPayload=buildPayrollAction(draft)
  busy.value=true
  await submit('/payroll-action',payload)
  emit('saved')
 }catch(e){error.value=e instanceof Error?e.message:'操作失败，请稍后重试'}
 finally{busy.value=false}
}
</script>

<template>
 <Teleport to="body"><div class="overlay" @keydown.esc="!busy&&emit('close')"><section class="modal wide payroll-dialog" role="dialog" aria-modal="true" aria-labelledby="payroll-dialog-title">
  <div class="modal-head"><h2 id="payroll-dialog-title">{{heading}}</h2><button type="button" :disabled="busy" aria-label="关闭工资操作" @click="emit('close')">×</button></div>
  <form @submit.prevent="save" autocomplete="off">
   <p v-if="mode==='create-deduction'||mode==='edit-deduction'" class="notice">登记和更正本身不影响工资。仅由有工资权限的人员依据适用法律及双方约定核对并确认后，扣款才可纳入结算；系统只记录依据，不判断其法律效力，结算时还须明确勾选。</p>
   <p v-if="mode==='create-adjustment'" class="notice">新增工资调整仅用于正数补发。需要扣减工资时，请登记扣款并依据适用法律及双方约定核对确认后再纳入结算。</p>
   <p v-if="mode==='confirm-deduction'" class="notice">登记本身不影响工资。仅由有工资权限的人员依据适用法律及双方约定核对并确认后，这笔扣款才可纳入结算；保存依据不代表系统判定其法律效力，且仍须在结算时明确勾选。</p>
   <p v-if="mode==='create-advance'" class="notice">这是实际已支付给工人的工资预支，可在尚无施工工资时登记；预支付款不会作为工资付款重复登记。</p>
   <p v-if="mode==='create-settlement'" class="notice">确认只保存工资金额及来源，不代表已经付款。计算为施工工资 {{payrollMoney(wageTotal)}} + 调整 {{payrollMoney(adjustmentTotal)}} - 已确认扣款 {{payrollMoney(deductionTotal)}} - 本次抵扣预支 {{payrollMoney(parseAmountOrZero(amount))}} = {{payrollMoney(settlementPayable)}}。</p>
   <div v-if="error" class="error" role="alert">{{error}}<p>已保留本次填写，可修改后重试。</p></div>
   <fieldset :disabled="busy">
    <div v-if="['create-adjustment','create-deduction','edit-deduction','create-advance','edit-advance'].includes(mode)" class="form-grid">
     <label>{{mode==='create-adjustment'?'补发金额':mode.includes('deduction')?'扣款金额':mode.includes('advance')?'预支金额':'调整金额'}}（元） *<input v-model="amount" type="text" inputmode="decimal" maxlength="18" :placeholder="mode==='create-adjustment'?'例如：50.00':'例如：500.00'" required></label>
     <label>{{mode.includes('deduction')?'扣款日期':mode.includes('advance')?'实际预支日期':'生效日期'}} *<input v-model="date" type="date" required></label>
     <label v-if="mode.includes('deduction')">扣款原因 *<textarea v-model="reason" rows="2" maxlength="1000" required placeholder="说明质量问题、返工或其他事实"></textarea></label>
     <label v-if="mode==='edit-deduction'&&(row as PayrollDeductionRow|null)?.status==='confirmed'">合法或约定依据 *<textarea v-model="legalBasis" rows="3" maxlength="1000" required placeholder="核对后填写依据；系统不判断其法律效力"></textarea></label>
     <label v-else-if="mode==='create-adjustment'">补发原因 *<textarea v-model="reason" rows="2" maxlength="1000" required></textarea></label>
     <label v-else-if="mode==='create-advance'||mode==='edit-advance'">备注<textarea v-model="note" rows="2" maxlength="1000"></textarea></label>
     <div class="project-field"><label>关联工地（可选）<input v-model="projectQuery" maxlength="300" placeholder="输入工地地址或名称" @keydown.enter.prevent="searchProjects"></label><button type="button" :disabled="projectLoading" @click="searchProjects">{{projectLoading?'查询中…':'查找工地'}}</button><select v-model="projectId" aria-label="选择关联工地"><option value="">不关联工地</option><option v-if="projectId&&!projects.some(item=>item.id===projectId)" :value="projectId">{{row?.project_name||row?.address||'当前已关联工地'}}</option><option v-for="item in projects" :key="item.id" :value="item.id">{{item.name}}{{item.address?' · '+item.address:''}}{{item.status!=='进行中'?'（'+item.status+'）':''}}</option></select></div>
    </div>
    <div v-else-if="mode==='confirm-deduction'" class="form-grid"><label>合法或约定依据 *<textarea v-model="legalBasis" rows="4" maxlength="1000" required placeholder="填写依据来源及双方约定情况"></textarea></label></div>
    <div v-else-if="mode==='create-settlement'" class="form-grid">
     <label>结算日期 *<input v-model="date" type="date" required></label>
     <label>本次抵扣预支（元）<input v-model="amount" type="text" inputmode="decimal" maxlength="18" required><small>最多 {{payrollMoney(advanceMaximum)}}；预支余额不足时留在余额中，不形成负工资。</small></label>
     <label>结算备注<textarea v-model="note" rows="2" maxlength="1000"></textarea></label>
     <p class="settlement-sources">本次选择：施工工资 {{selection.wages.length}} 笔 · 调整 {{selection.adjustments.length}} 笔 · 已确认扣款 {{selection.deductions.length}} 笔 · 预支 {{selection.advances.length}} 笔</p>
    </div>
    <div v-else-if="mode==='create-payment'" class="form-grid">
     <p class="notice payment-balance">结算应付 {{payrollMoney(settlement?.payable_cents)}} · 已实发 {{payrollMoney(settlement?.paid_cents)}} · 剩余未发 {{payrollMoney(settlement?.remaining_cents)}}。本窗口登记的是实际支付。</p>
     <label>本次实际发放（元） *<input v-model="amount" type="text" inputmode="decimal" maxlength="18" required></label><label>实际发放日期 *<input v-model="date" type="date" required></label>
     <label>支付方式<input v-model="method" maxlength="40" placeholder="现金、转账等"></label><label>备注<textarea v-model="note" rows="2" maxlength="1000"></textarea></label>
    </div>
    <label v-else-if="mode.startsWith('reverse-')" class="reason-field">{{mode==='reverse-payment'?'付款更正原因':'更正原因'}} *<textarea v-model="reason" rows="3" maxlength="1000" required placeholder="说明更正缘由；原记录会保留"></textarea></label>
   </fieldset>
   <footer><button type="button" :disabled="busy" @click="emit('close')">取消</button><button class="primary" :disabled="busy||projectLoading||mode==='create-payment'&&(!settlement||payrollCents(settlement.remaining_cents)<=0n)||mode==='create-settlement'&&(!canSettle||requestedAdvance>advanceMaximum||settlementPayable<0n)" type="submit">{{busy?'保存中…':actionLabel}}</button></footer>
  </form>
 </section></div></Teleport>
</template>
<style scoped>
.project-field{display:grid;grid-template-columns:minmax(160px,1fr) auto;gap:8px;align-items:end;min-width:0}.project-field select{grid-column:1/-1;width:100%;min-width:0}.settlement-sources{grid-column:1/-1;color:#667487}.reason-field{display:block}.reason-field textarea,.payroll-dialog textarea{width:100%;resize:vertical}.payment-balance{grid-column:1/-1;margin:0}.payroll-dialog label{min-width:0}.payroll-dialog input,.payroll-dialog select{width:100%;min-width:0}footer{flex-wrap:wrap}
@media(max-width:600px){.overlay{padding:10px}.payroll-dialog{width:100%}.modal-head h2{font-size:18px}.form-grid{grid-template-columns:minmax(0,1fr)}}
</style>
