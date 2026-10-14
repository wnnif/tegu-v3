<script setup lang="ts">
import {onBeforeUnmount,ref,watch} from 'vue'
import {api} from '../api'
import type {WageDetail} from '../wageDetail'
import {formatCents,formatQuantity,savedLineDimensions} from '../printOrder'
import {measureUnit} from '../orderSegments'
const props=defineProps<{wageId:string}>()
const emit=defineEmits<{close:[]}>()
const detail=ref<WageDetail|null>(null),loading=ref(false),error=ref('')
let generation=0
async function load(){
 const current=++generation,id=props.wageId
 detail.value=null;error.value=''
 if(!id){loading.value=false;return}
 loading.value=true
 try{const result=await api<WageDetail>('/wage?id='+encodeURIComponent(id));if(current===generation)detail.value=result}
 catch(e){if(current===generation)error.value=(e as Error).message}
 finally{if(current===generation)loading.value=false}
}
watch(()=>props.wageId,load,{immediate:true})
onBeforeUnmount(()=>{generation++})
</script>
<template>
 <Teleport to="body"><div class="overlay"><section class="modal wide wage-detail">
  <div class="modal-head"><h2>施工人工资明细</h2><button type="button" @click="emit('close')" aria-label="关闭工资明细">×</button></div>
  <div class="wage-content">
   <p v-if="loading" class="hint" role="status">正在读取该施工人工资明细…</p>
   <div v-if="error" class="error" role="alert">{{error}}<button type="button" :disabled="loading" @click="load">重新读取</button></div>
   <template v-if="detail">
    <dl class="wage-heading"><div><dt>施工人</dt><dd>{{detail.worker_name||'—'}}</dd></div><div><dt>公司</dt><dd>{{detail.company_name||'—'}}</dd></div><div><dt>施工地址</dt><dd>{{detail.address||'—'}}</dd></div><div><dt>施工 / 送货日期</dt><dd>{{detail.work_date}}</dd></div><div><dt>单号</dt><dd>{{detail.number}}</dd></div><div><dt>工资状态</dt><dd>{{Number(detail.wage_paid)?'已发放':'待发放'}}</dd></div></dl>
    <p class="hint">仅显示此施工人在本次记录中的施工和工资，明细只读。</p>
    <div class="wage-lines"><article v-for="(line,index) in detail.lines" :key="line.id||index" class="wage-line">
     <div><strong>{{line.category}} / {{line.name}}</strong><p v-if="line.spec">{{line.spec}}</p><p>施工位置：{{line.region||'未填写位置'}}</p><p v-if="line.description">说明：{{line.description}}</p></div>
     <div><span>施工尺寸 / 数量</span><p v-for="(dimension,i) in savedLineDimensions(line)" :key="i">{{dimension}}</p><p>工程量：{{formatQuantity(line.qty_micro)}} {{measureUnit(line.mode)}}</p></div>
     <div><span>{{line.mode==='金额'?'整项工资':'工资标准'}}</span><strong>{{formatCents(line.wage_cents)}} 元/{{measureUnit(line.mode)}}</strong><span>本项工资</span><strong>{{formatCents(line.labor_cents)}} 元</strong></div>
    </article></div>
    <p v-if="!detail.lines.length" class="empty">此人在本次记录中没有施工工艺。</p>
    <p v-if="detail.handles_delivery" class="notice">本人带货工资：{{formatCents(detail.delivery_wage_cents)}} 元</p>
    <p v-if="detail.legacy" class="hint">历史单人记录，工资合计按当时保存的金额显示。</p>
    <div class="wage-total"><strong>此人工资合计：{{formatCents(detail.wage_cents)}} 元</strong></div>
   </template>
   <footer><button type="button" @click="emit('close')">关闭</button></footer>
  </div>
 </section></div></Teleport>
</template>
<style scoped>
.wage-content{padding:16px 24px;min-width:0}.wage-heading{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px 20px;margin:0}.wage-heading div{min-width:0}.wage-heading dt{font-size:12px;color:#728099}.wage-heading dd{margin:0;overflow-wrap:anywhere}.wage-lines{display:grid;gap:10px}.wage-line{display:grid;grid-template-columns:minmax(0,1.4fr) minmax(0,1.4fr) minmax(0,1fr);gap:14px;padding:14px;border:1px solid #dce4ed;border-radius:6px}.wage-line>div{min-width:0;display:grid;align-content:start;gap:5px;overflow-wrap:anywhere}.wage-line span{font-size:12px;color:#728099}.wage-total{text-align:right;margin-top:14px}.hint{padding:10px 0;color:#728099}.error button{margin-left:12px}@media(max-width:650px){.wage-content{padding:16px}.wage-heading{grid-template-columns:1fr}.wage-line{grid-template-columns:1fr;gap:12px}.wage-line>div:last-child{grid-template-columns:1fr 1fr}.wage-total{text-align:left}}
</style>
