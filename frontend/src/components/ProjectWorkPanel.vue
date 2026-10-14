<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {api} from '../api'
import {referenceById} from '../workflows/references'
import type {Craft} from '../workflows/models'
import {ticketLabel,ticketPrintOrder,type ProjectTicket,type ProjectWork,type TicketDetail} from '../projectWork'
import {projectError} from '../projects'
import {customerMoney,hasCustomerPrice} from '../customerPrices'
import type {PrintOrder} from '../printOrder'
import ReceiptManagementPanel from './ReceiptManagementPanel.vue'
import OrderPrint from './OrderPrint.vue'
import SavedOrderWork from './SavedOrderWork.vue'
const props=defineProps<{projectId:string;canCustomerPrices:boolean;canFinance?:boolean;canManageFinance?:boolean;canWages?:boolean;canOrder?:boolean;compact?:boolean}>()
const money=(value:unknown)=>customerMoney(value,props.canCustomerPrices)
const financeAllowed=computed(()=>!!props.canFinance&&props.canCustomerPrices)
const financeMoney=(value:unknown)=>customerMoney(value,financeAllowed.value)
const emit=defineEmits<{loaded:[work:ProjectWork];openOrder:[order:{id:string}]}>()
const work=ref<ProjectWork|null>(null),loading=ref(false),busy=ref(false),error=ref(''),notice=ref('')
const printOrder=ref<PrintOrder|null>(null),selected=ref<TicketDetail|null>(null),receiptCompany=ref('')
let generation=0
const submittedTickets=computed(()=>work.value?.tickets||[])
const categoryOrder=ref<string[]>([]),orderingCrafts=ref<Craft[]>([]),sortingWarning=ref('')
async function load(page=1){
  if(!props.projectId)return
  const current=++generation;loading.value=true;error.value=''
  try{
    const result=await api<ProjectWork>('/project-work?'+new URLSearchParams({id:props.projectId,page:String(page),page_size:'25'}))
    if(current!==generation)return
    sortingWarning.value=''
    try{
      const [company,configuration]=await Promise.all([referenceById('companies',result.project.company_id),api<{crafts:Craft[]}>('/reference?'+new URLSearchParams({tables:'crafts',craft_scope:'order',company_id:result.project.company_id,project_id:props.projectId}))])
      if(current!==generation)return
      categoryOrder.value=company?.craft_category_order||[];orderingCrafts.value=configuration.crafts||[]
    }catch(e){
      if(current!==generation)return
      categoryOrder.value=[];orderingCrafts.value=[];sortingWarning.value='公司显示排序读取失败，暂按默认顺序显示；保存明细与金额未改变。请刷新重试。'
    }
    if(current!==generation)return
    work.value=result;emit('loaded',result)
  }catch(e){if(current===generation)error.value=projectError(e)}finally{if(current===generation)loading.value=false}
}
async function preview(ticket?:ProjectTicket,version?:number){
  if(!props.canCustomerPrices){error.value='打印客户金额需要“查看客户价格”权限';return}
  const current=generation
  busy.value=true;error.value=''
  try{const params=new URLSearchParams(ticket?{id:ticket.id}:{project_id:props.projectId});if(version)params.set('version',String(version));const [detail,settings]=await Promise.all([api<TicketDetail>('/project-ticket?'+params),api<{title?:string;company?:string}>('/print-settings')]);if(current===generation)printOrder.value={...ticketPrintOrder(detail),print_settings:settings}}catch(e){if(current===generation)error.value=projectError(e)}finally{if(current===generation)busy.value=false}
}
async function inspect(ticket:ProjectTicket){
  const current=generation
  busy.value=true;error.value='';notice.value=''
  try{const detail=await api<TicketDetail>('/project-ticket?id='+encodeURIComponent(ticket.id));if(current!==generation)return;selected.value=detail}catch(e){if(current===generation)error.value=projectError(e)}finally{if(current===generation)busy.value=false}
}
async function closeReceipts(){receiptCompany.value='';await load(work.value?.page||1)}
watch(()=>[props.projectId,props.canCustomerPrices,props.canFinance,props.canWages],()=>{generation++;busy.value=false;work.value=null;categoryOrder.value=[];orderingCrafts.value=[];sortingWarning.value='';selected.value=null;printOrder.value=null;receiptCompany.value='';notice.value='';void load()},{immediate:true})
onBeforeUnmount(()=>{generation++})
defineExpose({refresh:load,printSummary:()=>preview()})
</script>
<template>
  <section class="project-work" :class="{compact}">
    <div class="toolbar"><h3>{{compact?'工地累计与交票':'工地总表与票据'}}</h3><button type="button" :disabled="loading||busy" @click="load(work?.page||1)">刷新</button><button type="button" :disabled="loading||busy||!work?.total||!canCustomerPrices" @click="preview()">预览 / 打印全工地</button></div>
    <p v-if="!canCustomerPrices" class="hint">客户金额无查看权限；施工详情仍可查看，打印客户金额需要“查看客户价格”权限。</p>
    <p v-if="sortingWarning" role="status" class="notice">{{sortingWarning}}</p>
    <p v-if="loading" role="status" class="hint">正在读取工地累计施工与票据…</p>
    <p v-if="error" class="error" role="alert">{{error}}；已填写的内容仍保留，请核对后重试。</p><p v-if="notice" class="notice" role="status">{{notice}}</p>
    <template v-if="work">
      <p class="hint">本工地正常{{work.project.ticket_mode==='split'?'分两次':'统一'}}交票。不同日期、不同师傅的施工按配置累计；交票和打印不会再次扣料或计工资。</p>
      <section v-if="canCustomerPrices&&work.project.package" class="package-overview"><strong>{{work.project.package.name}} · {{work.project.package.room_count}} 房</strong><span>套餐费 {{money(work.project.package.price_cents)}}，套餐内施工照常计工资。</span><span v-if="work.project.package.coverage.filter(item=>item.mapping==='mapped').length">套餐覆盖：{{work.project.package.coverage.filter(item=>item.mapping==='mapped').map(item=>item.item_name).join('、')}}</span><span v-if="work.project.package.coverage.filter(item=>item.mapping!=='mapped').length">待核实覆盖（仍按实际收费）：{{work.project.package.coverage.filter(item=>item.mapping!=='mapped').map(item=>item.item_name).join('、')}}</span></section>
      <div class="ticket-list"><article v-for="ticket in submittedTickets" :key="ticket.id" class="ticket-card"><div><strong>{{ticketLabel(ticket)}}</strong><span class="ticket-status">{{ticket.status}}</span></div><small>{{ticket.number}} · 第 {{ticket.version}} 版</small><dl><div><dt>票面金额</dt><dd>{{money(ticket.total_cents)}}</dd></div><div v-if="canCustomerPrices&&ticket.package_charge"><dt>房型套餐费</dt><dd>{{ticket.package_charge.name}} · {{money(ticket.package_charge.amount_cents)}}</dd></div><div><dt>已分配收款</dt><dd>{{financeMoney(ticket.received_cents)}}</dd></div><div><dt>尚未核销</dt><dd>{{financeMoney(ticket.outstanding_cents)}}</dd></div></dl><div class="ticket-buttons"><button type="button" :disabled="busy||!canCustomerPrices||!hasCustomerPrice(ticket.total_cents)" @click="preview(ticket)">预览 / 打印</button><button type="button" :disabled="busy" @click="inspect(ticket)">版本 / 记录</button><button v-if="financeAllowed" type="button" :disabled="busy" @click="receiptCompany=work.project.company_id">公司收款 / 欠款</button></div></article></div>
      <p v-if="!submittedTickets.length" class="empty">尚无已填写的施工或售料；保存后按公司配置累计成票。</p>
      <details v-if="work.orders.length" :open="!compact" class="work-records">
        <summary>已保存施工 / 送货记录（共 {{work.total}} 次）</summary>
        <div class="record-grid">
          <article v-for="order in work.orders" :key="order.id" class="saved-order-card" :data-order-id="order.id">
            <strong>{{order.work_date}} · {{order.worker_names||order.worker_name||'—'}}</strong><small>{{order.number}}</small><button v-if="canOrder" type="button" :disabled="loading||busy" @click="emit('openOrder',order)">查看 / 编辑此施工单</button>
            <SavedOrderWork :order="order" :can-customer-prices="canCustomerPrices" :can-wages="canWages" :category-order="categoryOrder" :crafts="orderingCrafts" />
          </article>
        </div>
        <div class="toolbar"><button type="button" :disabled="loading||work.page<=1" @click="load(work.page-1)">上一页记录</button><span>第 {{work.page}} / {{Math.max(1,Math.ceil(work.total/work.page_size))}} 页</span><button type="button" :disabled="loading||work.page*work.page_size>=work.total" @click="load(work.page+1)">下一页记录</button></div>
      </details>
    </template>
    <section v-if="selected" class="ticket-details"><div class="toolbar"><h4>{{ticketLabel(selected.ticket)}} · {{selected.ticket.number}}</h4><button type="button" :disabled="busy" @click="selected=null">关闭票据记录</button></div>
      <p v-if="canCustomerPrices&&selected.ticket.package_charge" class="package-charge-detail">房型套餐收费：{{selected.ticket.package_charge.name}} · {{selected.ticket.package_charge.room_count}} 房 · {{money(selected.ticket.package_charge.amount_cents)}} 元。套餐覆盖施工不再逐项收费，施工工资照常计算。</p>
      <div class="version-list"><strong>已保留版本</strong><button v-for="version in selected.versions||[]" :key="version.version" type="button" :disabled="busy||!canCustomerPrices" @click="preview(selected.ticket,version.version)">第 {{version.version}} 版 · {{version.created_at}} {{version.event}}</button><p v-if="!selected.versions?.length" class="hint">尚无交票版本记录。</p></div>
      <div v-if="financeAllowed" class="receipt-list"><strong>此工地收费的收款分配</strong><p class="hint">这里显示分配到本工地收费的钱；公司实际到账、预收、纠错与减免统一在收款管理操作。</p><article v-for="receipt in selected.payment_receipts||[]" :key="receipt.id"><span>{{receipt.received_at}} · {{financeMoney(receipt.amount_cents)}} · {{receipt.method||'未填写方式'}}</span><span v-if="receipt.voided||receipt.voided_at">历史已解除分配</span></article><button type="button" :disabled="busy" @click="receiptCompany=work?.project.company_id||''">打开公司收款管理</button></div>
    </section>
    <div v-if="receiptCompany&&financeAllowed" class="overlay"><section class="modal wide"><div class="modal-head"><h2>公司收款管理</h2><button type="button" @click="closeReceipts">关闭收款管理</button></div><ReceiptManagementPanel :can-finance="!!canFinance" :can-customer-prices="canCustomerPrices" :can-manage="!!canManageFinance" :initial-company-id="receiptCompany"/></section></div>
    <OrderPrint v-if="printOrder&&canCustomerPrices" :order="printOrder" :company="printOrder.company_name||''" :worker="printOrder.worker_name||''" :can-wages="!!canWages" @close="printOrder=null" />
  </section>
</template>
<style scoped>
.project-work{min-width:0;padding:12px 16px;border-top:1px solid #d9e3ef}.project-work.compact{padding:12px 24px}.ticket-list{display:grid;grid-template-columns:repeat(auto-fit,minmax(245px,1fr));gap:12px}.ticket-card{border:1px solid #d6e1ee;border-radius:6px;padding:12px;background:#f8fafc;min-width:0}.ticket-card>div:first-child{display:flex;justify-content:space-between;gap:8px}.ticket-card small{display:block;margin-top:6px;overflow-wrap:anywhere}.ticket-status{color:#476a93}.ticket-card dl{margin:12px 0}.ticket-card dl>div{display:flex;justify-content:space-between;gap:10px;margin:5px 0}.ticket-card dt{color:#62758d}.ticket-card dd{margin:0;font-weight:600;text-align:right}.ticket-buttons{display:flex;gap:7px;flex-wrap:wrap}.package-overview{display:grid;gap:4px;margin:10px 0;padding:10px 12px;border:1px solid #cbd9e8;border-radius:6px;background:#f5f8fb;color:#40536a;font-size:13px;overflow-wrap:anywhere}.package-overview strong{color:#243a52}.package-charge-detail{padding:10px 12px;border-left:3px solid #3572a5;background:#f2f7fb;overflow-wrap:anywhere}.work-records{margin-top:16px}.work-records summary{cursor:pointer;padding:8px;color:#385d8c}.record-grid{display:grid;grid-template-columns:minmax(0,1fr);gap:10px}.record-grid>.saved-order-card{min-width:0;max-width:100%;padding:12px;border:1px solid #dce4ed;overflow-wrap:anywhere}.saved-order-card>small{display:block;color:#677991;margin:4px 0 10px}.ticket-details{margin-top:14px;padding:12px;border:1px solid #bfd2ed;border-radius:6px}.action-fields{display:flex;align-items:end;flex-wrap:wrap;gap:10px}.action-fields label{min-width:140px;flex:1}.action-fields input{max-width:100%}.version-list,.receipt-list{display:grid;gap:7px;padding:12px 0}.version-list button{text-align:left}.receipt-list article{display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap}.hint{font-size:13px;color:#677991;margin:8px 0 12px;padding:0}@media(max-width:550px){.project-work,.project-work.compact{padding:12px 16px}.ticket-list,.record-grid{grid-template-columns:1fr}.action-fields label{width:100%}}
</style>
