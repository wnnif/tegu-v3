<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref,watch} from 'vue'
import {api,apiSession,SessionChangedError} from './api'
import {financeDashboardExport,financeDashboardGroups,financeDashboardMoney,financeDashboardMonth,financeDashboardParams,financeDashboardSourceAllowed,financeDashboardSourceNavigation,type FinanceDashboardFilter,type FinanceDashboardResponse,type FinanceDashboardSourceNavigation} from './financeDashboard'

const props=defineProps<{permissions:string[]}>()
const emit=defineEmits<{navigate:[value:FinanceDashboardSourceNavigation]}>()
const initial=financeDashboardMonth(),from=ref(initial.from),to=ref(initial.to),all=ref(false),loading=ref(false),error=ref(''),result=ref<FinanceDashboardResponse|null>(null),exportURL=ref('')
const allowed=computed(()=>props.permissions.includes('finance'))
const groups=computed(()=>financeDashboardGroups(result.value?.groups||[],props.permissions))
let generation=0,alive=true,applied:FinanceDashboardFilter={...initial}

async function load(filter:FinanceDashboardFilter=applied){
 const current=++generation,session=apiSession.capture()
 result.value=null;exportURL.value='';error.value=''
 if(!allowed.value){loading.value=false;return}
 let params:URLSearchParams
 try{params=financeDashboardParams(filter)}catch(e){error.value=(e as Error).message;loading.value=false;return}
 loading.value=true
 try{
  const value=await api<FinanceDashboardResponse>('/finance-summary?'+params)
  if(!alive||current!==generation||!apiSession.current(session)||!allowed.value)return
  result.value=value;applied={from:value.range.from,to:value.range.to,all:value.range.all};exportURL.value=financeDashboardExport(applied)
 }catch(e){if(alive&&current===generation&&apiSession.current(session)&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(alive&&current===generation)loading.value=false}
}
function apply(){void load({from:from.value,to:to.value,all:all.value})}
function thisMonth(){const value=financeDashboardMonth();from.value=value.from;to.value=value.to;all.value=false;void load(value)}
function allTime(){all.value=true;apply()}
function sourceNavigation(metric:{key:string;source:string}){
 return result.value?financeDashboardSourceNavigation(metric,result.value.range):null
}
function open(metric:{key:string;source:string}){
 if(!financeDashboardSourceAllowed(metric.source,props.permissions))return
 const target=sourceNavigation(metric)
 if(target)emit('navigate',target)
}
function refresh(){return load()}
watch(()=>props.permissions.join(','),()=>{generation++;result.value=null;exportURL.value='';if(allowed.value)void load();else loading.value=false})
onMounted(()=>void load())
onBeforeUnmount(()=>{alive=false;generation++})
defineExpose({refresh})
</script>

<template>
 <section class="finance-summary" aria-label="财务汇总">
  <p v-if="!allowed" class="error">无财务汇总权限</p>
  <template v-else>
   <div class="summary-toolbar">
    <label>开始日期<input v-model="from" type="date" :disabled="all||loading"></label>
    <label>结束日期<input v-model="to" type="date" :disabled="all||loading"></label>
    <label class="all-time"><input v-model="all" type="checkbox" :disabled="loading">全部时间</label>
    <button :disabled="loading" @click="apply">查询</button>
    <button :disabled="loading" @click="thisMonth">本月</button>
    <button :disabled="loading" @click="allTime">全部时间</button>
    <a v-if="exportURL&&!loading" :href="exportURL">导出汇总 CSV</a>
   </div>
   <p v-if="loading" role="status">正在读取财务汇总…</p>
   <p v-if="error" class="error" role="alert">{{error}} <button :disabled="loading" @click="refresh">重试</button></p>
   <template v-if="result&&!loading">
    <p class="range-note">{{result.range.all?'全部时间':result.range.from+' 至 '+result.range.to}}；余额截至 {{result.range.to}}；查询时间 {{result.queried_at}}</p>
    <p class="range-note">{{result.balance_note}}</p>
    <p v-if="!groups.length">当前账号没有相应金额查看权限。</p>
    <section v-for="group in groups" :key="group.key" class="summary-group" :aria-label="group.label">
     <h3>{{group.label}}</h3>
     <div class="summary-grid">
      <article v-for="metric in group.metrics" :key="metric.key" class="summary-card">
       <p class="metric-label">{{metric.label}}</p>
       <button v-if="financeDashboardSourceAllowed(metric.source,permissions)&&sourceNavigation(metric)" class="metric-amount" :aria-label="metric.label+'：'+financeDashboardMoney(metric.amount_cents)+(sourceNavigation(metric)?.scope==='snapshot'?'，查看当前库存（非历史快照）':'，按汇总日期查看来源')" @click="open(metric)">{{financeDashboardMoney(metric.amount_cents)}}</button>
       <strong v-else class="metric-amount">{{financeDashboardMoney(metric.amount_cents)}}</strong>
       <p class="metric-basis">{{metric.basis}}</p>
       <p v-if="financeDashboardSourceAllowed(metric.source,permissions)&&!sourceNavigation(metric)" class="source-note">此项的明细页暂不支持相同日期口径，来源未开放跳转。</p>
      </article>
     </div>
    </section>
   </template>
  </template>
 </section>
</template>

<style scoped>
.finance-summary{max-width:1400px}.summary-toolbar{display:flex;align-items:end;gap:10px;flex-wrap:wrap}.summary-toolbar label{display:grid;gap:4px;font-size:13px}.summary-toolbar input[type=date]{min-height:34px;padding:5px}.summary-toolbar .all-time{display:flex;align-items:center;min-height:34px}.summary-toolbar a{padding:8px}.range-note{color:#596579;font-size:13px;line-height:1.6;margin:12px 0}.summary-group{margin:22px 0}.summary-group h3{font-size:16px;margin:0 0 10px}.summary-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(245px,1fr));gap:10px}.summary-card{padding:14px;background:#fff;border:1px solid #dde4ed;border-radius:8px;min-width:0}.metric-label{margin:0 0 8px;font-weight:600}.metric-amount{display:block;font-size:23px;line-height:1.35;color:#185ba2;padding:0;border:0;background:transparent;overflow-wrap:anywhere;text-align:left}.metric-amount:focus-visible{outline:2px solid #185ba2;outline-offset:4px}.metric-basis{color:#667187;font-size:12px;line-height:1.5;margin:10px 0 0}.error{color:#b42318}@media(max-width:600px){.summary-grid{grid-template-columns:1fr}.summary-toolbar label{max-width:100%}.summary-toolbar input[type=date]{width:100%;min-width:0}}
</style>
