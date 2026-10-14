<script setup lang="ts">
import {onBeforeUnmount,ref,watch} from 'vue'
import {api,apiSession,command,SessionChangedError} from '../api'
import {cents,parseReceiptAmount} from '../settlements'
import {financeEvent,financeHistoryAmounts,receiptMoney,type DiscountDetail,type ReceiptPage,type ReceiptTarget} from '../receipts'
const props=defineProps<{companyId:string;discountId?:string;canManage:boolean;allowed:boolean}>(),emit=defineEmits<{close:[];saved:[]}>()
const id=ref(props.discountId||''),detail=ref<DiscountDetail|null>(null),targets=ref<ReceiptTarget[]>([]),target=ref<ReceiptTarget|null>(null),batch=ref(''),amount=ref(''),reason=ref(''),voidReason=ref(''),voiding=ref(false)
const busy=ref(false),loading=ref(false),error=ref(''),notice=ref(''),page=ref(1),total=ref(0),pageSize=ref(100),historyPage=ref(1)
let generation=0,alive=true,submit=command()
async function load(next=1){
 if(!props.allowed)return
 const gen=++generation,session=apiSession.capture();loading.value=true;error.value=''
 try{
  if(id.value){const result=await api<DiscountDetail>('/customer-discount?'+new URLSearchParams({id:id.value,history_page:String(next)}));if(!alive||gen!==generation||!apiSession.current(session))return;detail.value=result;historyPage.value=result.history_page||next}
  else{targets.value=[];const result=await api<ReceiptPage<ReceiptTarget>>('/receipt-targets?'+new URLSearchParams({company_id:props.companyId,page:String(next)}));if(!alive||gen!==generation||!apiSession.current(session))return;targets.value=result.rows;page.value=result.page;total.value=result.total;pageSize.value=result.page_size||100}
 }catch(e){if(alive&&gen===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(alive&&gen===generation)loading.value=false}
}
function choose(row:ReceiptTarget){if(busy.value)return;target.value=row;batch.value='';amount.value='';error.value=''}
async function save(){
 if(busy.value||!props.allowed||!props.canManage)return
 const session=apiSession.capture();busy.value=true;error.value=''
 try{
  let body:unknown,path:string
  if(id.value){if(!voidReason.value.trim())throw new Error('请填写作废减免原因');path='/customer-discount-action';body={id:id.value,version:detail.value?.discount.version,action:'void',reason:voidReason.value.trim()}}
  else{
   if(!target.value)throw new Error('请选择减免对应的工地收费')
   if(!reason.value.trim())throw new Error('请填写老板确认的减免原因')
   const value=parseReceiptAmount(String(amount.value))
   if(BigInt(value)>cents(target.value.outstanding_cents))throw new Error('减免不能超过该工地尚未核销金额，不能重复减免已经免收的收费')
   path='/customer-discount';body={company_id:props.companyId,ticket_id:target.value.id,batch_id:batch.value,amount_cents:value,reason:reason.value.trim()}
  }
  const result=await submit(path,body) as DiscountDetail
  if(!alive||!apiSession.current(session)||!props.allowed)return
  detail.value=result;id.value=result.discount.id;voiding.value=false;voidReason.value='';notice.value='减免记录已保存；实际到账金额没有增加';emit('saved')
 }catch(e){if(alive&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(alive&&apiSession.current(session))busy.value=false}
}
watch(()=>[props.allowed,props.canManage],()=>{++generation;target.value=null;targets.value=[];amount.value='';reason.value='';voidReason.value='';voiding.value=false;busy.value=false;if(!props.allowed)detail.value=null})
void load()
onBeforeUnmount(()=>{alive=false;++generation;targets.value=[];detail.value=null;reason.value='';amount.value=''})
</script>
<template>
 <div class="overlay"><section class="modal wide discount-dialog" role="dialog" aria-modal="true" aria-label="客户减免">
  <div class="modal-head"><h2>{{id?'客户减免 / 操作历史':'老板确认客户减免'}}</h2><button type="button" :disabled="busy" @click="emit('close')">关闭</button></div><div class="dialog-body">
   <p class="hint">减免单独记账并保留原因，不是收款。施工组合中已经免收的金额不进入可减免收费。未确认减免的差额继续欠款。</p><div v-if="error" class="error" role="alert">{{error}}<button type="button" :disabled="busy||loading" @click="load(id?historyPage:page)">重新核对</button></div><p v-if="notice" class="notice" role="status">{{notice}}</p>
   <template v-if="detail"><h3>{{detail.discount.address}}</h3><p>{{detail.discount.number}} · {{detail.discount.batch_number||'未关联交票批次'}} · {{detail.discount.voided?'已作废':'有效减免'}}</p><strong>减免 {{receiptMoney(detail.discount.amount_cents)}}</strong><p>确认原因：{{detail.discount.reason}}</p><p v-if="detail.discount.void_reason">作废原因：{{detail.discount.void_reason}}</p><div v-if="canManage&&!detail.discount.voided&&!voiding" class="toolbar"><button type="button" :disabled="busy||loading" @click="voiding=true;submit=command()">作废这笔减免</button></div><form v-if="voiding" @submit.prevent="save"><label>作废减免原因<textarea v-model="voidReason" :disabled="busy" maxlength="1000" required rows="2"></textarea></label><div class="toolbar"><button class="primary" :disabled="busy||!canManage">确认作废减免</button><button type="button" :disabled="busy" @click="voiding=false">取消</button></div></form><h3>操作历史</h3><article v-for="row in detail.history" :key="row.version" class="history-row"><strong>第 {{row.version}} 版 · {{financeEvent(row.event)}}</strong><small>{{row.created_at}} · {{row.actor_name}}</small><p v-if="financeHistoryAmounts(row.snapshot)" class="history-amounts">{{financeHistoryAmounts(row.snapshot)}}</p><p>{{row.reason}}</p></article><div class="toolbar"><button type="button" :disabled="historyPage<=1||loading||busy" @click="load(historyPage-1)">上一页历史</button><span>共 {{detail.history_total||detail.history.length}} 条 · 第 {{historyPage}} 页</span><button type="button" :disabled="historyPage*(detail.history_page_size||50)>=(detail.history_total||detail.history.length)||loading||busy" @click="load(historyPage+1)">下一页历史</button></div></template>
   <form v-else-if="canManage" @submit.prevent="save"><fieldset :disabled="busy||!allowed"><template v-if="target"><h3>减免对应：{{target.address}}</h3><p>{{target.number}} · 原收费 {{receiptMoney(target.total_cents)}} · 已确认减免 {{receiptMoney(target.discount_cents)}} · 尚未核销 {{receiptMoney(target.outstanding_cents)}}</p><div class="form-grid"><label>关联交票批次（按需）<select v-model="batch"><option value="">不关联批次</option><option v-for="row in target.batches" :key="row.id" :value="row.id">{{row.number}}</option></select></label><label>老板同意减免（元）<input v-model="amount" inputmode="decimal" maxlength="20" required></label><label>确认减免原因<textarea v-model="reason" maxlength="1000" required rows="2"></textarea></label></div><div class="toolbar"><button class="primary" :disabled="busy||loading">{{busy?'正在保存…':'确认减免'}}</button><button type="button" :disabled="busy" @click="target=null">重新选择工地</button></div></template><h3>选择工地收费</h3><div class="targets"><article v-for="row in targets" :key="row.id"><strong>{{row.address}}<small>{{row.number}}</small></strong><span>尚未核销 {{receiptMoney(row.outstanding_cents)}}</span><button type="button" :disabled="busy||loading||cents(row.outstanding_cents)<=0n" @click="choose(row)">选择此工地减免</button></article><p v-if="!targets.length" class="empty">{{loading?'正在读取工地…':error?'读取失败，请重试':'公司暂无可减免的工地收费'}}</p></div><div class="toolbar"><button type="button" :disabled="page<=1||busy||loading" @click="load(page-1)">上一页工地</button><span>共 {{total}} 项 · 第 {{page}} 页</span><button type="button" :disabled="page*pageSize>=total||busy||loading" @click="load(page+1)">下一页工地</button></div></fieldset></form>
  </div>
 </section></div>
</template>
<style scoped>.dialog-body{padding:16px;min-width:0}.discount-dialog input,.discount-dialog select,.discount-dialog textarea{box-sizing:border-box;min-width:0;max-width:100%}.discount-dialog .toolbar{padding:12px 0;flex-wrap:wrap}.hint{font-size:13px;color:#63758b;line-height:1.6}.targets{display:grid;gap:10px}.targets article{display:flex;flex-wrap:wrap;align-items:center;gap:10px;border:1px solid #dce4ee;border-radius:6px;padding:12px;min-width:0}.targets strong{flex:1;min-width:160px;overflow-wrap:anywhere}.history-row{padding:12px 0;border-top:1px solid #e5eaf1;overflow-wrap:anywhere}.history-row p{margin:6px 0}@media(max-width:600px){.dialog-body{padding:12px}.form-grid{grid-template-columns:minmax(0,1fr)}.toolbar span{flex-basis:100%}}</style>
