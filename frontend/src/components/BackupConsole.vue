<script setup lang="ts">
import {computed,ref,watch,onUnmounted} from 'vue'
import {api} from '../api'
import type {PageResult} from '../workflows/models'
import type {BackupRun,BackupReport,MaintenanceJob} from '../workflows/readModels'
const props=defineProps<{revision:number}>()
const data=ref<BackupRun[]>([]),jobs=ref<MaintenanceJob[]>([]),jobPage=ref(1),jobTotal=ref(0),page=ref(1),total=ref(0),bytes=ref(0),free=ref<number|null>(null),lowSpace=ref(false),loaded=ref(false),jobsLoaded=ref(false),busy=ref(false),error=ref(''),listError=ref(''),jobsError=ref(''),chosen=ref<BackupRun|null>(null),report=ref<BackupReport|null>(null),target=ref(''),confirm=ref('')
interface RestoreTask {file:string;target:string;job:MaintenanceJob}
const restoreTasks=ref<Record<string,RestoreTask>>({})
const chosenTask=computed(()=>chosen.value?restoreTasks.value[chosen.value.file]:undefined)
const runningTask=computed(()=>Object.values(restoreTasks.value).find(task=>task.job.status==='运行中'))
const runningRestore=computed(()=>runningTask.value?.job||jobs.value.find(job=>job.kind==='restore'&&job.status==='运行中'))
const otherRunning=computed(()=>runningRestore.value&&runningRestore.value.id!==chosenTask.value?.job.id)
const actualTarget=(task:RestoreTask)=>task.job.result?.target||task.target
const taskMessage=(job:MaintenanceJob)=>job.status==='运行中'?'恢复任务已受理；可以关闭窗口，之后在任务记录中查看结果':job.status==='完成'?(job.result?.message||'恢复已完成'):(job.message||'恢复任务未完成')
const latestBackupFailure=computed(()=>{const latest=data.value[0];return latest&&['失败','云上传失败'].includes(latest.status)?'最近一次备份'+latest.status+'：'+(latest.message||'请查看服务日志'):''})
let lastBackupDone='',jobsRequest=0
const names:Record<string,string>={users:'账号及权限',companies:'公司',projects:'工地',workers:'施工人',warehouses:'仓库',products:'商品',product_unit_definitions:'商品历史换算',product_purchase_costs:'进货参考成本',crafts:'工艺',orders:'施工单',order_lines:'施工明细',order_line_wage_adjustments:'明细工资调整',order_worker_wages:'逐人工资',wage_settlements:'旧工资付款历史',order_materials:'施工耗材',order_sales:'销售商品',project_craft_slots:'工地交票配置',project_combinations:'施工组合计价',project_tickets:'工地票据',project_ticket_items:'票据施工明细',project_ticket_versions:'票据历史版本',project_ticket_receipts:'收款分配',company_receipts:'公司真实收款',company_receipt_versions:'收款历史版本',customer_discounts:'客户减免',customer_discount_versions:'减免历史版本',ticket_batches:'公司汇总交票',ticket_batch_items:'汇总交票内容',ticket_batch_versions:'汇总交票历史',stock_balances:'库存余额',stock_documents:'库存单据',stock_movements:'库存流水',settlements:'旧结算记录',payment_receipts:'旧收款流水',attachment_files:'票据与收款附件',requests:'防重记录',audit:'操作日志',archive_runs:'表格归档记录',backup_runs:'备份记录',maintenance_jobs:'维护任务',settings:'系统设置',payroll_adjustments:'工资调整',payroll_deductions:'罚款扣款',payroll_advances:'工资预支',payroll_settlements:'工资结算',payroll_settlement_sources:'结算来源快照',payroll_payments:'实际工资付款',payroll_history:'工资操作历史',expense_claims:'员工报销',expense_advances:'报账预支',expense_advance_allocations:'报账核销',expense_payments:'实际报销付款',expense_advance_returns:'报账预支退回',expense_history:'报销操作历史'}
async function load(){try{const r=await api<PageResult<BackupRun>&{used_bytes:number;free_bytes:number|null;low_space:boolean}>('/backups?page='+page.value);data.value=r.rows;total.value=r.total;bytes.value=r.used_bytes;free.value=r.free_bytes??null;lowSpace.value=!!r.low_space;loaded.value=true;listError.value=''}catch(e){data.value=[];total.value=0;bytes.value=0;free.value=null;lowSpace.value=false;loaded.value=false;listError.value=(e as Error).message}}
async function verify(row:BackupRun){if(busy.value)return;chosen.value=row;report.value=null;target.value='';confirm.value='';busy.value=true;error.value='';try{report.value=await api<BackupReport>('/maintenance/verify',{file:row.file})}catch(e){error.value=(e as Error).message}finally{busy.value=false}}
async function loadJobs(){
 const request=++jobsRequest
 const pending=Object.values(restoreTasks.value).filter(task=>task.job.status==='运行中')
 try{
  const [listing,backups]=await Promise.all([api<PageResult<MaintenanceJob>>('/maintenance/jobs?kind=all&page='+jobPage.value),api<PageResult<MaintenanceJob>>('/maintenance/jobs?kind=backup&page=1')])
  if(request!==jobsRequest)return
  jobs.value=listing.rows;jobTotal.value=listing.total;jobsLoaded.value=true;jobsError.value=''
  let reload=false
  const latestBackup=backups.rows.find((j:MaintenanceJob)=>j.status!=='运行中')
  if(latestBackup&&latestBackup.id!==lastBackupDone){lastBackupDone=latestBackup.id;reload=true}
  for(const task of pending){
   const current=listing.rows.find(job=>job.id===task.job.id)||await api<MaintenanceJob>('/maintenance/jobs?id='+task.job.id)
   if(request!==jobsRequest)return
   const stored=restoreTasks.value[task.file]
   if(stored?.job.id===current.id&&stored.job.status==='运行中'){
    restoreTasks.value[task.file]={...stored,job:current}
    if(current.status!=='运行中')reload=true
   }
  }
  if(reload)await load()
 }catch(e){if(request!==jobsRequest)return;jobs.value=[];jobTotal.value=0;jobsLoaded.value=false;jobsError.value=(e as Error).message}
}
async function restore(){
 if(busy.value||!chosen.value)return
 if(runningRestore.value){error.value='已有恢复任务正在执行，请先在任务记录中核对结果';return}
 const file=chosen.value.file,restoreTarget=target.value
 busy.value=true;error.value=''
 try{
  const job=await api<MaintenanceJob>('/maintenance/restore',{file,target:restoreTarget,confirm:confirm.value})
  restoreTasks.value[file]={file,target:restoreTarget,job}
  await loadJobs()
 }catch(e){error.value=(e as Error).message}finally{busy.value=false}
}
function prepareRestore(){
 if(!chosen.value||chosenTask.value?.job.status==='运行中')return
 delete restoreTasks.value[chosen.value.file]
 target.value='';confirm.value='';error.value=''
}
function refreshJobs(){void loadJobs()}
watch(()=>props.revision,()=>{void load();refreshJobs()});void load();refreshJobs();const timer=setInterval(refreshJobs,5000);onUnmounted(()=>clearInterval(timer))
</script>
<template><section><p v-if="loaded">备份目录已用 {{(bytes/1024/1024).toFixed(1)}} MB</p><p v-if="free!==null">备份盘可用 {{(free/1024**3).toFixed(1)}} GB</p><p v-if="loaded&&free===null" class="error" role="alert">无法读取备份盘剩余空间，请检查备份目录挂载与权限。</p><p v-if="lowSpace" class="error" role="alert">磁盘空间不足5 GB，请扩容或转移已核验备份。</p><p v-if="page===1&&latestBackupFailure" class="error" role="alert">{{latestBackupFailure}}</p><p v-if="listError" class="error" role="alert">备份列表读取失败：{{listError}}</p><p v-if="jobsError" class="error" role="alert">任务记录读取失败：{{jobsError}}</p><p v-if="error" class="error">{{error}}</p><div class="table-wrap"><table><thead><tr><th>时间</th><th>状态</th><th>文件 / 大小</th><th>说明</th><th>操作</th></tr></thead><tbody><tr v-for="r in data" :key="r.id"><td>{{r.created_at}}</td><td>{{r.status}}</td><td class="wrap-cell">{{r.file||'—'}}<small v-if="r.bytes">{{(r.bytes/1024/1024).toFixed(2)}} MB</small></td><td class="wrap-cell">{{r.message}}</td><td><template v-if="r.available"><a :href="'/api/backup-file?file='+encodeURIComponent(r.file)">下载快照</a> <a :href="'/api/backup-file?checksum=1&file='+encodeURIComponent(r.file)">校验文件</a><button class="link" :disabled="busy" @click="verify(r)">校验 / 恢复</button></template><span v-else>无可下载文件</span></td></tr><tr v-if="!data.length"><td colspan="5" class="empty">{{listError?'备份列表读取失败，请稍后刷新':loaded?'暂无备份记录':'正在读取备份记录'}}</td></tr></tbody></table></div><div class="pager"><span>共 {{total}} 条</span><button :disabled="page<=1||busy" @click="page--;load()">上一页</button><span>{{page}}</span><button :disabled="page*100>=total||busy" @click="page++;load()">下一页</button></div>
 <h3>维护任务记录</h3><div class="table-wrap"><table><thead><tr><th>任务编号</th><th>类型</th><th>开始时间</th><th>状态</th><th>进度</th><th>结果</th></tr></thead><tbody><tr v-for="j in jobs" :key="j.id"><td class="wrap-cell">{{j.id}}</td><td>{{j.kind==='backup'?'备份':'恢复'}}</td><td>{{j.created_at}}</td><td>{{j.status}}</td><td>{{j.stage}}<small v-if="j.total">{{j.progress}} / {{j.total}} 阶段</small></td><td class="wrap-cell">{{j.status==='完成'?(j.result?.message||j.result?.file||'已完成'):j.message||'—'}}<small v-if="j.result?.target">目标库：{{j.result.target}}</small></td></tr><tr v-if="!jobs.length"><td colspan="6" class="empty">{{jobsError?'任务记录读取失败，请稍后刷新':jobsLoaded?'暂无维护任务':'正在读取任务记录'}}</td></tr></tbody></table></div><div class="pager"><span>共 {{jobTotal}} 条</span><button :disabled="busy||jobPage<=1" @click="jobPage--;refreshJobs()">上一页</button><span>{{jobPage}}</span><button :disabled="busy||jobPage*100>=jobTotal" @click="jobPage++;refreshJobs()">下一页</button></div>
 <Teleport to="body"><div v-if="chosen" class="overlay"><section class="modal"><div class="modal-head"><h2>备份校验与独立恢复</h2><button :disabled="busy" @click="chosen=null" aria-label="关闭">×</button></div><div class="restore-content">
  <p class="wrap-cell">{{chosen.file}}</p><p v-if="busy">正在处理，请勿关闭此窗口</p><p v-if="error" class="error" role="alert">{{error}}</p>
  <p v-if="otherRunning" class="notice" role="status">另一恢复任务正在执行（编号 {{runningRestore?.id}}）。<template v-if="runningTask">文件：{{runningTask.file}}；目标库：{{actualTarget(runningTask)}}。</template>完成并核对任务结果后，才能开始本次恢复。</p>
  <template v-if="report"><p>SHA256及文件结构校验通过</p><div class="table-wrap"><table><tbody><tr v-for="(count,name) in report.counts"><th>{{names[name as string]||name}}</th><td>{{count}}</td></tr></tbody></table></div></template>
  <div v-if="chosenTask" :class="chosenTask.job.status==='完成'||chosenTask.job.status==='运行中'?'notice':'error'" role="status">
   <p class="wrap-cell">恢复文件：{{chosenTask.file}}</p><p class="wrap-cell">目标库：{{actualTarget(chosenTask)}}</p><p class="wrap-cell">任务编号：{{chosenTask.job.id}}</p><p>{{taskMessage(chosenTask.job)}}</p>
   <button v-if="chosenTask.job.status!=='运行中'" type="button" :disabled="busy" @click="prepareRestore">准备新的恢复</button>
  </div>
  <form v-if="report&&!chosenTask" @submit.prevent="restore" autocomplete="off"><p>仅恢复到同一数据库服务器上预先建立并授权的独立空库。当前业务库不会覆盖。恢复后还需核对数据并单独切换服务配置。</p><label>独立空库名称<input v-model="target" pattern="tegu_restore_[a-z0-9_]{1,40}" placeholder="tegu_restore_202609" required></label><label>再次输入目标库名<input v-model="confirm" required></label><footer><button class="primary" :disabled="busy||!!runningRestore||!target||target!==confirm">恢复到该空库</button></footer></form>
 </div></section></div></Teleport>
 <p class="hint">完整恢复还需保存同版本程序、运行配置、数据库连接配置，以及备份目录的 NDJSON / SHA256、monthly 月表文件和 DATA_DIR 中的 backup.key（使用加密云存储凭据时）。附件内容随数据库快照保存；数据库恢复不自动复制月表或密钥。以上文件含私密资料，应保留在本人可读取的安全位置。</p>
</section></template>
<style scoped>
.restore-content{padding:16px 24px;min-width:0}
.restore-content form{padding:16px 0}
.restore-content>.wrap-cell{max-width:none}
.restore-content label{margin-top:12px}
@media(max-width:650px){.restore-content{padding:16px}}
</style>
