<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref} from 'vue'
import {api,apiSession,SessionChangedError} from '../api'
import {payrollExportUrl,payrollMoney,type PayrollWorkerSummary,type PayrollWorkersPage} from '../payroll'
import PayrollDetailPanel from './PayrollDetailPanel.vue'
import PayrollBonusPanel from './PayrollBonusPanel.vue'

const emit=defineEmits<{archive:[]}>()
const query=ref(''),appliedQuery=ref(''),page=ref(1),pageSize=ref(100),total=ref(0),workers=ref<PayrollWorkerSummary[]>([]),loading=ref(false),error=ref(''),worker=ref<PayrollWorkerSummary|null>(null)
const detailPanel=ref<InstanceType<typeof PayrollDetailPanel>|null>(null),bonusPanel=ref<InstanceType<typeof PayrollBonusPanel>|null>(null),view=ref<'workers'|'wages'|'bonuses'>('workers')
const exportURL=computed(()=>payrollExportUrl('/payroll-workers',new URLSearchParams({q:appliedQuery.value})))
let generation=0,alive=true
const pageCount=()=>Math.max(1,Math.ceil(total.value/pageSize.value))
async function load(queryFilter=appliedQuery.value){
 const current=++generation,session=apiSession.capture(),params=new URLSearchParams({q:queryFilter,page:String(page.value)})
 loading.value=true;error.value='';workers.value=[];total.value=0
 try{
  const result=await api<PayrollWorkersPage>('/payroll-workers?'+params)
  if(!alive||current!==generation||!apiSession.current(session))return
  workers.value=result.rows;total.value=Number(result.total);page.value=Number(result.page)||page.value;pageSize.value=Number(result.page_size)||100;appliedQuery.value=queryFilter
 }catch(e){if(alive&&current===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(alive&&current===generation)loading.value=false}
}
function search(){if(loading.value)return;page.value=1;void load(query.value.trim())}
function open(row:PayrollWorkerSummary,mode:'wages'|'bonuses'='wages'){if(loading.value)return;worker.value=row;view.value=mode}
function back(){worker.value=null;view.value='workers';void load()}
function turn(next:number){if(loading.value||view.value!=='workers'||next<1||next>pageCount())return;page.value=next;void load()}
function money(value:unknown){return payrollMoney(value)}
function refresh(){if(view.value==='wages')void detailPanel.value?.refresh();else if(view.value==='bonuses')void bonusPanel.value?.refresh();else void load()}
onMounted(()=>void load())
onBeforeUnmount(()=>{alive=false;++generation;workers.value=[];worker.value=null})
defineExpose({refresh})
</script>

<template>
 <PayrollDetailPanel v-if="worker&&view==='wages'" ref="detailPanel" :worker="worker" @back="back" @changed="load" />
 <PayrollBonusPanel v-else-if="worker&&view==='bonuses'" ref="bonusPanel" :worker="worker" @back="back" @changed="load" />
 <section v-else class="panel wage-finance">
  <div class="toolbar summary-heading"><h2>工人工资汇总</h2><button type="button" :disabled="loading" @click="emit('archive')">月度归档</button></div>
  <div class="toolbar summary-filters"><label>搜索施工人<input v-model="query" maxlength="100" :disabled="loading" placeholder="输入工人姓名" @keydown.enter.prevent="search"></label><button type="button" :disabled="loading" @click="search">查询</button><button v-if="query" type="button" :disabled="loading" @click="query='';search()">清除</button><button type="button" :disabled="loading" @click="load()">刷新</button><a v-if="!loading&&!error&&total" :href="exportURL">导出全部筛选结果</a></div>
  <p class="hint">工资按已保存的逐人工资记录汇总。预支、扣款、结算确认和实际付款分别记录；交票状态不会改变工资。</p>
  <p v-if="error" class="error" role="alert">{{error}} <button type="button" :disabled="loading" @click="load(query.trim())">重新读取</button></p>
  <p class="list-count">{{loading?'正在读取…':`共 ${total} 位施工人 · 第 ${page} / ${pageCount()} 页`}}</p>
  <div class="worker-list">
   <article v-for="row in workers" :key="row.worker_id" class="worker-row">
    <div class="worker-name"><strong>{{row.worker_name||'历史施工人'}}</strong><small>{{row.order_count}} 张施工记录<span v-if="row.wage_pending_count"> · 工价待配置 {{row.wage_pending_count}} 笔</span></small></div>
    <div><span>{{row.wage_pending_count?'已配置施工工资':'累计施工工资'}}</span><strong>{{money(row.gross_cents)}}</strong></div>
    <div><span>工资调整</span><strong>{{money(row.adjustment_cents)}}</strong></div>
    <div><span>已确认扣款</span><strong>{{money(row.deduction_cents)}}</strong></div>
    <div><span>工资预支</span><strong>{{money(row.advance_issued_cents)}}</strong><small>已抵扣 {{money(row.advance_applied_cents)}} · 余额 {{money(row.advance_balance_cents)}}</small></div>
    <div><span>实际工资付款</span><strong>{{money(row.paid_cents)}}</strong></div>
    <div class="unpaid"><span>{{row.wage_pending_count?'已配置剩余未发工资':'剩余未发工资'}}</span><strong>{{money(row.unpaid_cents)}}</strong></div>
    <div class="bonus-summary"><span>年终奖金累计 / 已发 / 未发</span><strong>{{money(row.bonus_accrued_cents||0)}} / {{money(row.bonus_paid_cents||0)}} / {{money(row.bonus_balance_cents||0)}}</strong></div>
    <div class="worker-actions"><button type="button" :disabled="loading" @click="open(row,'wages')">查看工资明细</button><button type="button" :disabled="loading" @click="open(row,'bonuses')">奖金明细</button></div>
   </article>
   <p v-if="!workers.length" class="empty">{{loading?'正在读取人员工资…':error?'读取失败，请重试':'暂无工资记录'}}</p>
  </div>
  <div class="toolbar pager"><button type="button" :disabled="loading||page<=1" @click="turn(page-1)">上一页</button><span>第 {{page}} / {{pageCount()}} 页 · 每页 {{pageSize}} 人</span><button type="button" :disabled="loading||page>=pageCount()" @click="turn(page+1)">下一页</button></div>
 </section>
</template>

<style scoped>
.summary-heading{justify-content:space-between}.summary-heading h2{font-size:18px;margin:0}.summary-filters{align-items:end}.summary-filters label{min-width:220px;flex:1 1 240px}.worker-list{display:grid;grid-template-columns:minmax(0,1fr);border-top:1px solid #edf0f5}.worker-row{display:grid;grid-template-columns:minmax(130px,1.2fr) repeat(7,minmax(115px,1fr)) minmax(160px,auto);gap:14px;align-items:center;padding:13px 16px;border-bottom:1px solid #edf0f5;min-width:0}.worker-row>div{min-width:0;display:flex;flex-direction:column;gap:3px}.worker-row span{font-size:12px;color:#718097}.worker-row strong{font-size:14px;font-variant-numeric:tabular-nums;overflow-wrap:anywhere}.worker-row small{font-size:11px;overflow-wrap:anywhere}.worker-name strong{font-size:15px}.worker-row .unpaid strong{color:#1f5ebd}.worker-actions{align-items:flex-end}.worker-actions button{white-space:normal}.pager{justify-content:flex-end}.list-count{padding:0 16px 10px;color:#667487}.empty{padding:36px 16px;text-align:center;color:#8793a5}
@media(max-width:1450px){.worker-row{grid-template-columns:repeat(4,minmax(0,1fr))}.worker-name{grid-column:span 2}.worker-actions{align-items:flex-start}}
@media(max-width:600px){.summary-filters label{min-width:0;flex-basis:100%}.worker-row{grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;padding:13px}.worker-name,.bonus-summary{grid-column:1/-1}.worker-name{border-bottom:1px solid #edf0f5;padding-bottom:9px}.worker-actions{grid-column:1/-1;align-items:stretch}.worker-actions button{width:100%}.pager{justify-content:center}}
</style>
