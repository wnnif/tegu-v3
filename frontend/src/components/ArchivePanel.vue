<script setup lang="ts">
import {computed,ref,onMounted,onBeforeUnmount,watch} from 'vue'
import {api,apiSession,SessionChangedError} from '../api'
import ReferenceSelect from './ReferenceSelect.vue'
import {CUSTOMER_PRICE_HIDDEN,hasCustomerPrice} from '../customerPrices'
import type {CatalogRow,Numeric,PageResult} from '../workflows/models'
import type {ArchiveCompany,ArchiveCompanyPage,ArchiveFile,ArchiveStatus,MaintenanceJob} from '../workflows/readModels'

const props=defineProps<{companies:CatalogRow[];can:(permission:string)=>boolean}>()
const pricesAllowed=computed(()=>props.can('customer_prices'))
const archiveAllowed=computed(()=>props.can('wages')||props.can('expenses')||props.can('warehouse')||pricesAllowed.value&&(props.can('orders')||props.can('finance')))
const archivePermissions:Record<string,string>={'施工明细':'orders','售料明细':'orders','施工验收表':'orders','工资明细':'wages','施工内部留档表':'wages','财务应收':'finance','旧单据应收':'finance','票据应收':'finance','收款流水':'finance','工资付款':'wages','工资调整':'wages','工资扣款':'wages','工资预支':'wages','进货金额':'warehouse','报销登记':'expenses','报销付款':'expenses','报账预支':'expenses','报账核销':'expenses','报账退回':'expenses'}
const fileAllowed=(file:ArchiveFile)=>{const permission=archivePermissions[file.kind];return !!permission&&props.can(permission)&&(file.kind!=='施工内部留档表'&&['wages','expenses','warehouse'].includes(permission)||pricesAllowed.value)}
const visibleFiles=(row:ArchiveCompany)=>row.files.filter(fileAllowed)
const month=ref(new Date().toLocaleDateString('sv-SE').slice(0,7)),company=ref(''),history=ref(false),page=ref(1)
const rows=ref<ArchiveCompany[]>([]),total=ref(0),pageSize=ref(100),legacyTotal=ref(0),latest=ref<ArchiveStatus|null>(null)
const expanded=ref<string[]>([]),loading=ref(false),submitting=ref(false),error=ref(''),notice=ref('')
const serverOrders=ref(false),serverFinance=ref(false),serverWages=ref(false)
const hasOrders=computed(()=>serverOrders.value&&props.can('orders')&&pricesAllowed.value),hasFinance=computed(()=>serverFinance.value&&props.can('finance')&&pricesAllowed.value),hasWages=computed(()=>serverWages.value&&props.can('wages'))
const jobs=ref<MaintenanceJob[]>([]),jobPage=ref(1),jobTotal=ref(0),jobsLoading=ref(false),jobsError=ref('')
type Scope={month:string;company:string;history:boolean;page:number}
const applied=ref<Scope|null>(null)
type Pending={id:string;month:string;company:string}
const pending=ref<Pending|null>(null)
const running=computed(()=>!!pending.value||jobs.value.some(job=>job.status==='运行中'))
const busy=computed(()=>loading.value||submitting.value)
const columns=computed(()=>4+(hasOrders.value?2:0)+(hasFinance.value?4:0)+(hasWages.value?1:0))
let listGeneration=0,jobsGeneration=0,submitGeneration=0,alive=true
let timer:ReturnType<typeof setInterval>|undefined
const knownRunning=new Set<string>(),handled=new Set<string>()
const currentScope=():Scope=>({month:month.value,company:company.value,history:history.value,page:page.value})
const identity=(row:ArchiveCompany)=>row.run_id+'/'+row.key
const amount=(value:Numeric|undefined,allowed=true)=>!allowed?CUSTOMER_PRICE_HIDDEN:hasCustomerPrice(value)?(Number(value)/100).toFixed(2):'未记录'
const groupLink=(row:ArchiveCompany)=>'/api/archive-company-download?'+new URLSearchParams({run_id:row.run_id,key:row.key})
const fileLink=(file:ArchiveFile)=>'/api/archives?file='+encodeURIComponent(file.file)
const format=(file:ArchiveFile)=>file.kind==='施工验收表'?'客户版 Excel':file.kind==='施工内部留档表'?'内部版 Excel':file.file.endsWith('.xlsx')?'Excel':'CSV'
function clearList(){rows.value=[];total.value=0;latest.value=null;legacyTotal.value=0;expanded.value=[];serverOrders.value=false;serverFinance.value=false;serverWages.value=false}
function toggle(row:ArchiveCompany){const key=identity(row);expanded.value=expanded.value.includes(key)?expanded.value.filter(value=>value!==key):[...expanded.value,key]}
async function load(){
 const generation=++listGeneration,session=apiSession.capture(),scope=currentScope()
 applied.value=scope;loading.value=true;error.value='';clearList()
 if(!archiveAllowed.value){loading.value=false;return null}
 try{
  const result=await api<ArchiveCompanyPage>('/archive-companies?'+new URLSearchParams({month:scope.month,company_id:scope.company,history:scope.history?'1':'0',page:String(scope.page)}))
  if(!alive||generation!==listGeneration||!apiSession.current(session))return null
  rows.value=result.rows;total.value=result.total;pageSize.value=result.page_size||100;legacyTotal.value=result.legacy_total;latest.value=result.latest
  serverOrders.value=result.can_orders;serverFinance.value=result.can_finance;serverWages.value=result.can_wages
  return null
 }catch(e){if(alive&&generation===listGeneration&&apiSession.current(session)&&!(e instanceof SessionChangedError)){error.value=(e as Error).message;return error.value}return null}
 finally{if(alive&&generation===listGeneration)loading.value=false}
}
async function finish(job:MaintenanceJob,own:Pending|null){
 if(handled.has(job.id))return
 handled.add(job.id);knownRunning.delete(job.id)
 if(own&&pending.value?.id===own.id)pending.value=null
 if(job.status!=='完成'){error.value=job.message||'生成未完成，请核对任务记录';return}
 notice.value=(own?own.month+' ':'')+'月度表格已生成，共 '+(job.result?.count??0)+' 份'
 // Refresh the current view, never reset a later selection to the old job's scope.
 page.value=1;const reading=load(),generation=listGeneration,session=apiSession.capture(),readingError=await reading
 if(alive&&generation===listGeneration&&apiSession.current(session)&&readingError)error.value='表格已生成，但列表读取失败，请点击“查询”重新加载，无需重复生成：'+readingError
}
async function loadJobs(force=false){
 if(!archiveAllowed.value){jobs.value=[];jobTotal.value=0;return}
 if(jobsLoading.value&&!force)return
 const generation=++jobsGeneration,session=apiSession.capture(),requestedPage=jobPage.value
 jobsLoading.value=true;jobsError.value=''
 try{
  const result=await api<PageResult<MaintenanceJob>>('/maintenance/jobs?kind=archive&page='+requestedPage)
  if(!alive||generation!==jobsGeneration||!apiSession.current(session))return
  jobs.value=result.rows;jobTotal.value=result.total
  for(const job of result.rows)if(job.status==='运行中')knownRunning.add(job.id)
  const own=pending.value
  const ownJob=own?(result.rows.find(job=>job.id===own.id)||await api<MaintenanceJob>('/maintenance/jobs?id='+encodeURIComponent(own.id))):null
  if(!alive||generation!==jobsGeneration||!apiSession.current(session)||own&&pending.value?.id!==own.id)return
  if(ownJob&&ownJob.status!=='运行中')await finish(ownJob,own)
  else if(!own){const completed=result.rows.find(job=>knownRunning.has(job.id)&&job.status!=='运行中'&&!handled.has(job.id));if(completed)await finish(completed,null)}
 }catch(e){if(alive&&generation===jobsGeneration&&apiSession.current(session)&&!(e instanceof SessionChangedError))jobsError.value=(e as Error).message}
 finally{if(alive&&generation===jobsGeneration)jobsLoading.value=false}
}
async function refresh(reset=false){
 if(submitting.value)return
 if(reset)page.value=1
 await Promise.all([load(),loadJobs()])
}
function filter(){if(submitting.value)return;page.value=1;void load()}
function turn(next:number){if(busy.value)return;page.value=next;void load()}
async function generate(){
 if(!archiveAllowed.value){error.value='客户金额归档需要对应模块及“查看客户价格”权限；工资明细仍按工资权限开放';return}
 if(busy.value||running.value||!month.value)return
 const generation=++submitGeneration,session=apiSession.capture(),scope={month:month.value,company:company.value}
 submitting.value=true;error.value='';notice.value=''
 try{
  const job=await api<MaintenanceJob>('/maintenance/archive',{month:scope.month,company:scope.company})
  if(!alive||generation!==submitGeneration||!apiSession.current(session))return
  pending.value={id:job.id,...scope};knownRunning.add(job.id);jobPage.value=1
  notice.value=scope.month+'归档任务已受理，可在下方查看结果'
  await loadJobs(true)
 }catch(e){if(alive&&generation===submitGeneration&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(alive&&generation===submitGeneration)submitting.value=false}
}
function turnJobPage(next:number){jobPage.value=next;jobs.value=[];void loadJobs(true)}
watch(()=>[props.can('customer_prices'),props.can('orders'),props.can('finance'),props.can('wages'),props.can('expenses'),props.can('warehouse')],()=>{++listGeneration;++jobsGeneration;++submitGeneration;clearList();jobs.value=[];pending.value=null;knownRunning.clear();handled.clear();submitting.value=false;jobsLoading.value=false;notice.value='';void refresh()})
onMounted(()=>{void refresh();timer=setInterval(()=>{void loadJobs()},5000)})
onBeforeUnmount(()=>{alive=false;++listGeneration;++jobsGeneration;++submitGeneration;if(timer)clearInterval(timer);clearList();jobs.value=[];pending.value=null})
</script>

<template>
 <section class="panel archive-panel">
  <form class="toolbar filter-toolbar" @submit.prevent="refresh(true)">
   <label>归档月份<input type="month" v-model="month" :disabled="submitting" @change="filter"></label>
   <button v-if="month" type="button" :disabled="submitting" @click="month='';filter()">全部月份</button>
   <ReferenceSelect v-model="company" entity="companies" label="筛选及生成公司" empty-label="全部公司" search-placeholder="搜索公司" :disabled="submitting" @change="filter"/>
   <label>显示版本<select v-model="history" :disabled="submitting" @change="filter"><option :value="false">最新归档</option><option :value="true">历史版本</option></select></label>
   <button :disabled="busy">查询</button><button type="button" class="primary" :disabled="busy||running||!month||!archiveAllowed" @click="generate">生成整月表格</button>
  </form>
  <p v-if="!pricesAllowed" class="hint">客户金额表格无查看权限；下载施工、客户金额文件或内部 Excel 需要对应模块及“查看客户价格”权限，纯工资明细仍按工资权限开放。</p>
   <p class="hint">{{applied?.history?'查看各次保存的历史版本':'每家公司每月只显示最新一次归档'}}。选定月份后，随时可生成截至目前的资料；重新生成会保留旧版本。</p>
  <p class="hint">施工与施工工资按施工日期，收款按到账日期，工资付款按实际发薪日期，报销按费用日期分别归档。选择“全部公司”时，另含全公司工资付款、进货及报销表；预支、核销、退回分表，不重复合计付款。</p>
  <p v-if="hasFinance" class="hint">收费、已分配和尚未核销是归档施工范围的核销快照；当月实际到账按到账月份统计，不能据此推算公司累计欠款。公司真正待收与预收余额请查看收款管理。旧归档未记录的到账金额显示“未记录”。</p>
  <p v-if="!month" class="hint">当前查看全部月份；生成表格需先选择具体月份。</p>
  <p v-if="legacyTotal" class="notice">有 {{legacyTotal}} 组旧版归档未记录公司编号，选择“历史版本”和“全部公司”可查看及下载。重新生成后会进入最新公司汇总。</p>
  <div v-if="error" class="error" role="alert">{{error}} <button :disabled="busy" @click="refresh()">重新读取</button></div>
  <p v-if="latest?.error" class="error">{{latest.month}}：{{latest.error}}（{{latest.time}}）</p>
  <p v-if="notice" class="notice" role="status">{{notice}}</p>
  <div class="table-wrap"><table class="company-archives"><thead><tr><th>公司 / 月份</th><template v-if="hasOrders"><th>施工记录数</th><th>施工及售料金额（元）</th></template><template v-if="hasFinance"><th>收费（元）</th><th>已分配 / 历史已收（元）</th><th>尚未核销（元）</th><th>当月实际到账（元）</th></template><th v-if="hasWages">工资（元）</th><th>文件数</th><th>生成时间</th><th>方式</th></tr></thead>
   <tbody><template v-for="row in rows" :key="identity(row)">
    <tr><td class="archive-company"><strong>{{row.company||'原公司未记录'}}</strong><small>{{row.month}}<span v-if="row.legacy"> · 旧版，按原批次存档名显示</span></small><button type="button" :aria-expanded="expanded.includes(identity(row))" @click="toggle(row)">{{expanded.includes(identity(row))?'收起表格':'查看表格'}}</button><div class="archive-group-download"><a v-if="visibleFiles(row).length&&visibleFiles(row).every(file=>file.available)" :href="groupLink(row)">下载整月资料 ZIP</a><span v-else-if="visibleFiles(row).length" class="missing-file">部分文件缺失，需重新生成</span><span v-else>无可下载文件</span></div></td>
     <template v-if="hasOrders"><td>{{row.order_count===undefined?'未记录':row.order_count}}</td><td>{{amount(row.construction_cents)}}</td></template><template v-if="hasFinance"><td>{{amount(row.receivable_cents)}}</td><td>{{amount(row.received_cents)}}</td><td>{{amount(row.outstanding_cents)}}</td><td>{{amount(row.cash_received_cents)}}</td></template><td v-if="hasWages">{{amount(row.wage_cents)}}</td><td>{{visibleFiles(row).length}}</td><td>{{row.created_at}}</td><td>{{Number(row.automatic)?'自动':'手动'}}</td>
    </tr>
    <tr v-if="expanded.includes(identity(row))"><td :colspan="columns" class="archive-details"><div class="archive-detail-head"><strong>{{row.company}} · {{row.month}} 表格</strong><span v-if="hasFinance">{{row.finance_scope==='allocation'?'收费与收款分配快照':'历史票据核销快照'}}</span><span>{{row.created_at}}保存的版本</span></div><p v-if="row.legacy" class="hint">旧版未记录的汇总显示“未记录”，原文件保持不变；同名公司未跨批次合并。</p><div class="archive-file-list"><div v-for="file in visibleFiles(row)" :key="file.file" class="archive-file"><div><strong>{{file.kind}}</strong><small>{{file.count}} 条 · {{file.amount_cents===undefined?'金额未记录':amount(file.amount_cents)+' 元'}}</small></div><a v-if="file.available" :href="fileLink(file)">下载 {{format(file)}}</a><span v-else class="missing-file">文件缺失，需重新生成</span></div><p v-if="!visibleFiles(row).length" class="empty">本次无可下载明细</p></div></td></tr>
   </template><tr v-if="!rows.length"><td :colspan="columns" class="empty">{{loading?'正在读取公司归档…':error?'归档列表读取失败，请重试查询':applied?.history?'暂无符合条件的历史归档':'暂无符合条件的最新公司归档'}}</td></tr></tbody>
  </table></div>
  <div class="toolbar"><span>共 {{total}} 组{{applied?.history?'历史版本':'公司月度归档'}} · 第 {{page}} / {{Math.max(1,Math.ceil(total/pageSize))}} 页</span><button :disabled="busy||page<=1" @click="turn(page-1)">上一页归档</button><button :disabled="busy||page*pageSize>=total" @click="turn(page+1)">下一页归档</button></div>
  <h3 class="task-heading">归档任务记录</h3><div v-if="jobsError" class="error" role="alert">任务记录读取失败：{{jobsError}} <button :disabled="jobsLoading" @click="loadJobs(true)">重新读取任务</button></div>
  <div class="table-wrap"><table><thead><tr><th>任务编号</th><th>开始时间</th><th>状态</th><th>进度</th><th>结果</th></tr></thead><tbody><tr v-for="job in jobs" :key="job.id"><td class="wrap-cell">{{job.id}}</td><td>{{job.created_at}}</td><td>{{job.status}}</td><td>{{job.stage}}<small v-if="job.total">{{job.progress}} / {{job.total}} 阶段</small></td><td>{{job.status==='完成'?'生成 '+(job.result?.count??0)+' 份':job.message||'—'}}</td></tr><tr v-if="!jobs.length"><td colspan="5" class="empty">{{jobsLoading?'正在读取任务…':jobsError?'任务记录读取失败':'暂无归档任务'}}</td></tr></tbody></table></div>
  <div class="toolbar"><span>共 {{jobTotal}} 条任务 · 第 {{jobPage}} 页</span><button :disabled="jobsLoading||jobPage<=1" @click="turnJobPage(jobPage-1)">上一页任务</button><button :disabled="jobsLoading||jobPage*100>=jobTotal" @click="turnJobPage(jobPage+1)">下一页任务</button></div>
 </section>
</template>

<style scoped>
.archive-group-download{margin-top:10px;line-height:1.5}
.archive-panel .archive-detail-head{justify-content:flex-start}.archive-panel .archive-file{justify-content:flex-start}.archive-panel .archive-file>div{flex:0 1 320px}@media(max-width:650px){.archive-panel .archive-file>div{flex:0 1 auto}}
.archive-company{min-width:180px;max-width:300px;white-space:normal;overflow-wrap:anywhere}.archive-company button{margin-top:8px}.archive-details{padding:16px;background:#f8faff;white-space:normal}.archive-detail-head{display:flex;gap:12px;flex-wrap:wrap;justify-content:space-between;overflow-wrap:anywhere}.archive-detail-head span{color:#758499}.archive-file-list{display:grid;gap:8px;margin-top:12px}.archive-file{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 12px;background:#fff;border:1px solid #e1e6ee;border-radius:6px;min-width:0}.archive-file>div{min-width:0;overflow-wrap:anywhere}.archive-file a{flex-shrink:0}.missing-file{color:#b83d32;white-space:normal;overflow-wrap:anywhere}.task-heading{padding:0 16px}.archive-panel>.hint{padding-top:0}.archive-file .missing-file{max-width:220px}@media(max-width:650px){.archive-company{min-width:160px}.archive-file{align-items:flex-start;flex-direction:column}.archive-details{padding:12px}.archive-detail-head{flex-direction:column;gap:4px}}
</style>
