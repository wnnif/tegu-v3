<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {api,apiSession,command,SessionChangedError} from '../api'
import {cents} from '../settlements'
import {allocationDrafts,allocationPayload,allocationTotal,amountInput,receiptMoney,type AllocationDraft,type ReceiptDetail,type ReceiptPage,type ReceiptTarget} from '../receipts'
const props=defineProps<{detail:ReceiptDetail;allowed:boolean}>(),emit=defineEmits<{saved:[detail:ReceiptDetail];cancel:[]}>()
const drafts=ref<AllocationDraft[]>([]),targets=ref<ReceiptTarget[]>([]),page=ref(1),total=ref(0),pageSize=ref(100),reason=ref(''),error=ref(''),busy=ref(false),loading=ref(false)
const sum=computed(()=>allocationTotal(drafts.value)),remaining=computed(()=>sum.value===null?null:cents(props.detail.receipt.amount_cents)-sum.value)
let generation=0,alive=true,sequence=0,submit=command()
async function load(next=page.value){
  if(!props.allowed)return
  const gen=++generation,session=apiSession.capture();loading.value=true;error.value='';targets.value=[]
  try{const result=await api<ReceiptPage<ReceiptTarget>>('/receipt-targets?'+new URLSearchParams({company_id:props.detail.receipt.company_id,page:String(next)}));if(!alive||gen!==generation||!apiSession.current(session))return;targets.value=result.rows;total.value=result.total;page.value=result.page;pageSize.value=result.page_size||100}
  catch(e){if(alive&&gen===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
  finally{if(alive&&gen===generation)loading.value=false}
}
function add(target:ReceiptTarget,batchId=''){
  if(busy.value||!props.allowed)return
  if(drafts.value.length>=100){error.value='一笔收款最多 100 个分配位置';return}
  if(drafts.value.some(row=>row.ticket_id===target.id&&row.batch_id===batchId)){error.value='该位置已在分配清单中，请直接修改金额';return}
  const batch=target.batches.find(row=>row.id===batchId)
  drafts.value.push({key:'new-'+(++sequence),ticket_id:target.id,batch_id:batchId,address:target.address,number:target.number,batch_number:batch?.number||'',amount:''});error.value=''
}
function targetAvailable(target:ReceiptTarget,batchId=''){
  const original=props.detail.allocations.filter(row=>row.ticket_id===target.id&&(!batchId||row.batch_id===batchId)).reduce((sum,row)=>sum+cents(row.amount_cents),0n)
  return cents(batchId?target.batches.find(row=>row.id===batchId)?.available_cents:target.outstanding_cents)+original
}
function fill(row:AllocationDraft){
  if(busy.value)return
  const other=drafts.value.filter(item=>item.key!==row.key).reduce((sum,item)=>{try{return sum+BigInt(allocationPayload([item],props.detail.receipt.amount_cents)[0]!.amount_cents)}catch{return sum}},0n)
  const available=cents(props.detail.receipt.amount_cents)-other,target=targets.value.find(item=>item.id===row.ticket_id)
  if(!target){error.value='请先翻到对应工地候选页，或手工填写分配金额';return}
  const limit=targetAvailable(target,row.batch_id)
  const amount=available<limit?available:limit
  if(amount<=0n){error.value='没有可继续分配的余额';return}
  row.amount=amountInput(amount.toString());error.value=''
}
async function save(){
  if(busy.value||!props.allowed)return
  const session=apiSession.capture();busy.value=true;error.value=''
  try{
    if(!reason.value.trim())throw new Error('请填写分配或调整原因')
    const allocations=allocationPayload(drafts.value,props.detail.receipt.amount_cents)
    const result=await submit('/receipt-allocation',{receipt_id:props.detail.receipt.id,version:props.detail.receipt.version,reason:reason.value.trim(),allocations}) as ReceiptDetail
    if(alive&&apiSession.current(session)&&props.allowed)emit('saved',result)
  }catch(e){if(alive&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
  finally{if(alive&&apiSession.current(session))busy.value=false}
}
watch(()=>[props.detail.receipt.id,props.detail.receipt.version,props.allowed],()=>{++generation;drafts.value=props.allowed?allocationDrafts(props.detail):[];targets.value=[];reason.value='';error.value='';busy.value=false;submit=command();void load(1)},{immediate:true})
onBeforeUnmount(()=>{alive=false;++generation;drafts.value=[]})
</script>
<template>
 <form class="allocation-editor" @submit.prevent="save">
  <h3>分配这笔收款</h3><p class="hint">实际到账只记一次。可分配到同公司的多个工地及交票批次，也可保留余额；尚未交票的施工也能分配。移除分配只退回未分配余额。</p>
  <div v-if="error" class="error" role="alert">{{error}}<button v-if="/版本|变化|刷新|冲突/.test(error)" type="button" :disabled="busy" @click="emit('cancel')">保留实际收款，返回后刷新核对</button></div>
  <fieldset :disabled="busy||!allowed"><div class="allocation-rows"><article v-for="row in drafts" :key="row.key"><strong>{{row.address}}<small>{{row.number}} · {{row.batch_number||'不关联交票批次'}}</small></strong><label>分配金额（元）<input v-model="row.amount" inputmode="decimal" maxlength="20" required :aria-label="row.address+'分配金额'"></label><button type="button" @click="fill(row)">填可分配余款</button><button type="button" @click="drafts=drafts.filter(item=>item.key!==row.key)">移除分配</button></article><p v-if="!drafts.length" class="hint">当前不分配到任何工地，全部保留为公司未分配收款。</p></div>
  <p>实际收款 {{receiptMoney(detail.receipt.amount_cents)}} · 分配合计 {{sum===null?'请填写有效金额':receiptMoney(sum)}} · 剩余未分配 {{remaining===null?'待核对':receiptMoney(remaining)}}</p><label>分配 / 调整原因<input v-model="reason" maxlength="1000" required placeholder="例如：确认用于这两个工地"></label></fieldset>
  <div class="toolbar"><button class="primary" :disabled="busy||loading||!allowed">{{busy?'保存中…':'保存分配'}}</button><button type="button" :disabled="busy" @click="emit('cancel')">取消</button></div>
  <h4>选择工地收费</h4><p class="hint">下方余额已加回本笔收款原有分配，服务器保存时会重新核对其他人的操作。</p><button v-if="error&&!targets.length" type="button" :disabled="loading||busy" @click="load()">重新读取工地</button>
  <div class="target-list"><article v-for="target in targets" :key="target.id"><strong>{{target.address}}<small>{{target.number}}</small></strong><span>工地可分配 {{receiptMoney(targetAvailable(target))}}</span><button type="button" :disabled="busy||loading||!allowed" @click="add(target)">分配到此工地</button><div v-for="batch in target.batches" :key="batch.id" class="batch-target"><span>交票 {{batch.number}} · 可分配 {{receiptMoney(targetAvailable(target,batch.id))}}</span><button type="button" :disabled="busy||loading||!allowed" @click="add(target,batch.id)">分配到此批次</button></div></article><p v-if="!targets.length" class="empty">{{loading?'正在读取工地…':error?'读取失败，请重试':'公司暂无施工收费，可先保留未分配收款'}}</p></div>
  <div class="toolbar"><button type="button" :disabled="page<=1||loading||busy" @click="load(page-1)">上一页工地</button><span>共 {{total}} 项 · 第 {{page}} / {{Math.max(1,Math.ceil(total/pageSize))}} 页</span><button type="button" :disabled="page*pageSize>=total||loading||busy" @click="load(page+1)">下一页工地</button></div>
 </form>
</template>
<style scoped>.allocation-editor{min-width:0}.allocation-rows,.target-list{display:grid;gap:10px}.allocation-rows article,.target-list article{display:flex;flex-wrap:wrap;align-items:end;gap:10px;padding:12px;border:1px solid #dce4ee;border-radius:6px;min-width:0}.allocation-rows strong,.target-list strong{flex:1;min-width:160px;overflow-wrap:anywhere}.allocation-rows label{min-width:140px;max-width:200px}.batch-target{display:flex;align-items:center;flex-wrap:wrap;gap:8px;flex-basis:100%;border-top:1px solid #edf0f5;padding-top:8px}.allocation-editor input{min-width:0;max-width:100%;box-sizing:border-box}.allocation-editor .toolbar{padding:12px 0;flex-wrap:wrap}.hint{color:#63758b;font-size:13px;line-height:1.6}@media(max-width:600px){.allocation-rows article,.target-list article{align-items:start}.allocation-rows strong,.allocation-rows label{flex-basis:100%;max-width:none}.toolbar span{flex-basis:100%}}</style>
