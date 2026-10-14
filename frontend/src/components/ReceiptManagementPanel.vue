<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref,watch} from 'vue'
import {api,apiSession,SessionChangedError} from '../api'
import {hasCustomerPrice,CUSTOMER_PRICE_HIDDEN} from '../customerPrices'
import {receiptMoney,summaryAmounts,type CompanyReceipt,type CustomerDiscount,type ReceiptPage,type ReceiptSummary,type ReceiptTarget} from '../receipts'
import ReferenceSelect from './ReferenceSelect.vue'
import CompanyReceiptDialog from './CompanyReceiptDialog.vue'
import CustomerDiscountDialog from './CustomerDiscountDialog.vue'
const props=defineProps<{canCustomerPrices:boolean;canFinance:boolean;canManage:boolean;initialCompanyId?:string}>()
const allowed=computed(()=>props.canFinance&&props.canCustomerPrices),money=(value:unknown)=>allowed.value&&hasCustomerPrice(value)?receiptMoney(value):CUSTOMER_PRICE_HIDDEN
type SummaryPage=ReceiptPage<ReceiptSummary>&Partial<ReceiptSummary>
const company=ref(props.initialCompanyId||''),query=ref(''),companies=ref<ReceiptSummary[]>([]),summary=ref<SummaryPage|null>(null),companySummary=ref<ReceiptSummary|null>(null)
const summaryPage=ref(1),summarySize=ref(100),tab=ref<'receipts'|'debts'|'discounts'>('receipts'),page=ref(1),total=ref(0),pageSize=ref(100)
const receipts=ref<CompanyReceipt[]>([]),targets=ref<ReceiptTarget[]>([]),discounts=ref<CustomerDiscount[]>([])
const month=ref(''),receiptQuery=ref(''),status=ref('active'),unallocated=ref(false),loading=ref(false),error=ref(''),notice=ref('')
const receiptDialog=ref<{id?:string;companyId:string}|null>(null),discountDialog=ref<{id?:string;companyId:string}|null>(null)
const locked=computed(()=>!!receiptDialog.value||!!discountDialog.value),currentSummary=computed(()=>company.value?companySummary.value:summary.value)
let generation=0,alive=true
function clear(){companies.value=[];summary.value=null;companySummary.value=null;receipts.value=[];targets.value=[];discounts.value=[];total.value=0}
async function refresh(){
 if(!allowed.value){clear();return}
 const gen=++generation,session=apiSession.capture();loading.value=true;error.value='';clear()
 try{
  const summaryResult=await api<SummaryPage>('/finance-companies?'+new URLSearchParams({company_id:company.value,q:company.value?'':query.value.trim(),page:String(summaryPage.value)}))
  if(!alive||gen!==generation||!apiSession.current(session))return
  summary.value=summaryResult;companies.value=summaryResult.rows;summarySize.value=summaryResult.page_size||100;companySummary.value=company.value?summaryResult.rows.find(row=>row.company_id===company.value)||null:null
  if(!company.value)return
  const parameters=new URLSearchParams({company_id:company.value,page:String(page.value)})
  if(tab.value==='receipts'){
   parameters.set('month',month.value);parameters.set('status',status.value);parameters.set('unallocated',unallocated.value?'1':'');parameters.set('q',receiptQuery.value.trim())
   const result=await api<ReceiptPage<CompanyReceipt>>('/company-receipts?'+parameters);if(!alive||gen!==generation||!apiSession.current(session))return;receipts.value=result.rows;total.value=result.total;pageSize.value=result.page_size||100
  }else if(tab.value==='debts'){
   const result=await api<ReceiptPage<ReceiptTarget>>('/receipt-targets?'+parameters);if(!alive||gen!==generation||!apiSession.current(session))return;targets.value=result.rows;total.value=result.total;pageSize.value=result.page_size||100
  }else{
   const result=await api<ReceiptPage<CustomerDiscount>>('/customer-discounts?'+parameters);if(!alive||gen!==generation||!apiSession.current(session))return;discounts.value=result.rows;total.value=result.total;pageSize.value=result.page_size||100
  }
 }catch(e){if(alive&&gen===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError)){clear();error.value=(e as Error).message}}
 finally{if(alive&&gen===generation)loading.value=false}
}
function filter(){if(locked.value)return;page.value=1;summaryPage.value=1;notice.value='';void refresh()}
function openCompany(id:string){if(locked.value||loading.value)return;company.value=id;page.value=1;tab.value='receipts';notice.value='';void refresh()}
function back(){if(locked.value)return;company.value='';page.value=1;notice.value='';void refresh()}
function chooseTab(next:typeof tab.value){if(locked.value)return;tab.value=next;page.value=1;notice.value='';void refresh()}
function turn(next:number){if(loading.value||locked.value)return;if(company.value)page.value=next;else summaryPage.value=next;void refresh()}
function beginReceipt(id?:string){if(!allowed.value||loading.value||locked.value)return;receiptDialog.value={id,companyId:company.value};notice.value=''}
async function changed(){notice.value='已保存；公司实际到账、未分配余额和欠款已重新核对';await refresh()}
watch(()=>[props.canCustomerPrices,props.canFinance],()=>{++generation;clear();receiptDialog.value=null;discountDialog.value=null;month.value='';receiptQuery.value='';query.value='';company.value='';error.value='';notice.value='';loading.value=false;void refresh()})
watch(()=>props.initialCompanyId,id=>{++generation;receiptDialog.value=null;discountDialog.value=null;company.value=id||'';page.value=1;tab.value='receipts';void refresh()})
onMounted(()=>void refresh())
onBeforeUnmount(()=>{alive=false;++generation;clear();receiptDialog.value=null;discountDialog.value=null})
defineExpose({refresh,openCompany})
</script>
<template>
 <section class="panel receipt-management">
  <div class="toolbar"><button v-if="company" type="button" :disabled="locked" @click="back">返回公司汇总</button><h2>{{companySummary?companySummary.company_name+'的收款与欠款':'收款管理'}}</h2><button class="primary" type="button" :disabled="loading||locked||!allowed" @click="beginReceipt()">登记实际收款</button></div>
  <p v-if="!allowed" class="notice">收款管理需要财务与查看客户价格权限；客户金额无查看权限。</p>
  <template v-else>
   <form v-if="!company" class="toolbar filter-toolbar" @submit.prevent="filter"><ReferenceSelect v-model="company" entity="companies" label="付款公司" empty-label="全部公司" :disabled="locked" @change="filter"/><label>公司名称<input v-model="query" :disabled="locked" maxlength="200" placeholder="搜索公司"></label><button :disabled="loading||locked">查询</button></form>
   <p class="hint">公司欠款按全部有效施工及商品收费计算，包含未交票施工。实际到账只登记一次；未分配款也会减少公司真正待收金额。作废交票不会删掉已经收到的钱。</p>
   <div v-if="error" class="error" role="alert">{{error}}<button type="button" :disabled="loading" @click="refresh">重新读取</button></div><p v-if="notice" class="notice" role="status">{{notice}}</p>
   <div v-if="currentSummary" class="summary-grid"><div v-for="[key,label] in summaryAmounts" :key="key" :class="{'actual-debt':key==='outstanding_cents'}"><span>{{label}}</span><strong>{{money(currentSummary[key])}}</strong></div></div>
   <p class="hint">工地尚未核销 = 收费 − 减免 − 已分配收款；公司实际仍需收回 = 收费 − 减免 − 全部实际到账（不足 0 时显示预收余额）。未分配收款不会重复算成公司欠款。</p>
   <template v-if="!company"><p>共 {{summary?.total||0}} 家公司，按公司实际待收优先显示。</p><div class="table-wrap"><table><thead><tr><th>公司</th><th>实际仍需收回</th><th>实际到账</th><th>未分配款</th><th>减免</th><th>工地尚未核销</th><th>操作</th></tr></thead><tbody><tr v-for="row in companies" :key="row.company_id"><td class="wrapping-cell">{{row.company_name}}</td><td>{{money(row.outstanding_cents)}}</td><td>{{money(row.received_cents)}}</td><td>{{money(row.unallocated_cents)}}</td><td>{{money(row.discount_cents)}}</td><td>{{money(row.unsettled_cents)}}</td><td><button :disabled="loading||locked" @click="openCompany(row.company_id)">收款 / 欠款</button></td></tr><tr v-if="!companies.length"><td colspan="7" class="empty">{{loading?'正在读取公司收款…':error?'读取失败，请重试':'暂无公司，可先在客户与工地添加公司'}}</td></tr></tbody></table></div></template>
   <template v-else><div class="tabs"><button v-for="item in [{id:'receipts' as const,name:'收款记录 / 未分配款'},{id:'debts' as const,name:'工地欠款'},{id:'discounts' as const,name:'客户减免'}]" :key="item.id" :class="{active:tab===item.id}" :disabled="locked" @click="chooseTab(item.id)">{{item.name}}</button></div>
    <template v-if="tab==='receipts'"><form class="toolbar filter-toolbar" @submit.prevent="filter"><label>实际到账月份<input v-model="month" type="month" :disabled="locked" @change="filter"></label><button v-if="month" type="button" :disabled="locked" @click="month='';filter()">全部到账月份</button><label>记录状态<select v-model="status" :disabled="locked" @change="filter"><option value="active">有效收款</option><option value="voided">已作废收款</option><option value="">全部记录</option></select></label><label class="check"><input v-model="unallocated" type="checkbox" :disabled="locked" @change="filter">只看有未分配余额</label><label>查找收款<input v-model="receiptQuery" :disabled="locked" placeholder="方式、凭证或备注" maxlength="200"></label><button :disabled="loading||locked">查询收款</button></form><p class="hint">月份与未分配筛选只影响下方到账记录，公司收费与实际待收保持全期口径。</p><div class="table-wrap"><table><thead><tr><th>到账日期 / 方式</th><th>实际到账</th><th>已分配</th><th>未分配</th><th>凭证 / 备注</th><th>状态</th><th>操作</th></tr></thead><tbody><tr v-for="row in receipts" :key="row.id"><td>{{row.received_at}}<small>{{row.method||'未填写方式'}}</small></td><td>{{money(row.amount_cents)}}</td><td>{{money(row.allocated_cents)}}</td><td>{{money(row.unallocated_cents)}}</td><td class="wrapping-cell">{{row.voucher_no||'无凭证号'}}<small>{{row.note}}</small></td><td>{{row.status==='voided'?'已作废':'有效收款'}}</td><td><button :disabled="loading||locked" @click="beginReceipt(row.id)">{{row.status==='voided'?'查看作废历史':'分配 / 更正 / 历史'}}</button></td></tr><tr v-if="!receipts.length"><td colspan="7" class="empty">{{loading?'正在读取收款…':error?'读取失败，请重试':'暂无符合条件的收款，可直接登记提前到账的钱'}}</td></tr></tbody></table></div></template>
    <template v-else-if="tab==='debts'"><p class="hint">这里按工地收费显示已分配、已减免和尚未核销。公司未分配款无法自动猜归属，公司的实际仍需收回金额见上方汇总。</p><div class="table-wrap"><table><thead><tr><th>工地 / 收费来源</th><th>施工及商品收费</th><th>已分配收款</th><th>客户减免</th><th>工地尚未核销</th><th>有效交票批次</th></tr></thead><tbody><tr v-for="row in targets" :key="row.id"><td class="wrapping-cell">{{row.address}}<small>{{row.number}}</small></td><td>{{money(row.total_cents)}}</td><td>{{money(row.allocated_cents)}}</td><td>{{money(row.discount_cents)}}</td><td>{{money(row.outstanding_cents)}}</td><td class="wrapping-cell"><span v-for="batch in row.batches" :key="batch.id">{{batch.number}} · 票内待核销 {{money(batch.available_cents)}}<br></span><span v-if="!row.batches.length">尚未关联有效批次</span></td></tr><tr v-if="!targets.length"><td colspan="6" class="empty">{{loading?'正在读取工地欠款…':error?'读取失败，请重试':'公司暂无施工收费，提前收到的钱仍保留'}}</td></tr></tbody></table></div></template>
    <template v-else><div class="toolbar"><button v-if="canManage" class="primary" type="button" :disabled="loading||locked" @click="discountDialog={companyId:company}">老板确认减免</button><p v-else class="hint">客户减免需由系统管理员或普通管理员确认。</p></div><div class="table-wrap"><table><thead><tr><th>工地 / 交票批次</th><th>减免金额</th><th>确认原因</th><th>人员 / 时间</th><th>状态</th><th>操作</th></tr></thead><tbody><tr v-for="row in discounts" :key="row.id"><td class="wrapping-cell">{{row.address}}<small>{{row.number}} · {{row.batch_number||'未关联批次'}}</small></td><td>{{money(row.amount_cents)}}</td><td class="wrapping-cell">{{row.reason}}<small v-if="row.void_reason">作废原因：{{row.void_reason}}</small></td><td>{{row.created_by_name}}<small>{{row.created_at}}</small></td><td>{{row.voided?'已作废':'有效减免'}}</td><td><button :disabled="loading||locked" @click="discountDialog={id:row.id,companyId:company}">原因 / 历史</button></td></tr><tr v-if="!discounts.length"><td colspan="6" class="empty">{{loading?'正在读取减免…':error?'读取失败，请重试':'暂无客户减免；未同意免收的差额仍然欠款'}}</td></tr></tbody></table></div></template>
   </template>
   <div class="toolbar pagination"><button :disabled="(company?page:summaryPage)<=1||loading||locked" @click="turn((company?page:summaryPage)-1)">上一页</button><span>共 {{company?total:summary?.total||0}} 项 · 第 {{company?page:summaryPage}} / {{Math.max(1,Math.ceil((company?total:summary?.total||0)/(company?pageSize:summarySize)))}} 页</span><button :disabled="(company?page:summaryPage)*(company?pageSize:summarySize)>=(company?total:summary?.total||0)||loading||locked" @click="turn((company?page:summaryPage)+1)">下一页</button></div>
  </template>
 </section>
 <CompanyReceiptDialog v-if="receiptDialog&&allowed" :receipt-id="receiptDialog.id" :company-id="receiptDialog.companyId" :can-manage="canManage" :allowed="allowed" @close="receiptDialog=null" @saved="changed"/>
 <CustomerDiscountDialog v-if="discountDialog&&allowed" :discount-id="discountDialog.id" :company-id="discountDialog.companyId" :can-manage="canManage" :allowed="allowed" @close="discountDialog=null" @saved="changed"/>
</template>
<style scoped>.receipt-management{min-width:0}.receipt-management h2{font-size:18px;margin:0;flex:1}.receipt-management .toolbar{flex-wrap:wrap}.receipt-management input,.receipt-management select{min-width:0;max-width:100%;box-sizing:border-box}.hint{font-size:13px;color:#63758b;line-height:1.6;margin:10px 16px;overflow-wrap:anywhere}.receipt-management>.error,.receipt-management>.notice{margin:12px 16px}.summary-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(155px,1fr));gap:8px;margin:12px 16px}.summary-grid>div{padding:12px;border:1px solid #dce5ef;background:#f7f9fc;border-radius:6px;min-width:0}.summary-grid span{color:#63758b;font-size:13px}.summary-grid strong{display:block;font-size:18px;margin-top:6px;overflow-wrap:anywhere}.summary-grid .actual-debt{background:#edf4ff;border-color:#a9c4e9}.wrapping-cell{white-space:normal;max-width:240px;overflow-wrap:anywhere}.filter-toolbar{align-items:end}.filter-toolbar label{min-width:140px}.filter-toolbar .check{display:flex;align-items:center;gap:5px;white-space:normal}.filter-toolbar .check input{flex:0 0 auto;width:auto}.pagination span{flex:1;text-align:center}@media(max-width:600px){.receipt-management h2{flex:0 0 100%;order:-1}.summary-grid{grid-template-columns:repeat(2,minmax(0,1fr));margin:10px 12px}.summary-grid>div{padding:10px}.summary-grid strong{font-size:16px}.filter-toolbar>*{min-width:0;flex:1}.filter-toolbar label{min-width:125px}.filter-toolbar .check{min-width:100%;flex:0 0 100%;justify-content:flex-start}.pagination span{order:-1;flex-basis:100%}}</style>
