<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref,useId,watch} from 'vue'
import {api,SessionChangedError} from '../api'

import type {CatalogRow as Option,PageResult} from '../workflows/models'
const props=withDefaults(defineProps<{
 entity:'companies'|'workers'|'warehouses'|'products'|'crafts'
 modelValue:string
 label:string
 emptyLabel:string
 selectedName?:string
 searchPlaceholder?:string
 disabled?:boolean
 required?:boolean
 active?:string
 category?:string
 companyId?:string
 workerId?:string
 craft?:string
 warehouseWorker?:'assigned'|'unassigned'
}>(),{selectedName:'',searchPlaceholder:'搜索名称',disabled:false,required:false,active:'',category:'',companyId:'',workerId:'',craft:'',warehouseWorker:undefined})
const emit=defineEmits<{ 'update:modelValue':[id:string];change:[item:Option|null] }>()
const fieldId=useId(),query=ref(''),options=ref<Option[]>([]),selected=ref<Option|null>(null),page=ref(1),total=ref(0),loading=ref(false),error=ref(''),missing=ref(false),expanded=ref(false)
const initialSelection={id:props.modelValue,name:props.selectedName}
const selectedLabel=computed(()=>{const item=selected.value?.id===props.modelValue?selected.value:null;const name=item?.name||(initialSelection.id===props.modelValue?initialSelection.name:'')||'已选资料';return name+(item?.active!==undefined&&!Number(item.active)?'（已停用）':'')})
let generation=0,timer:ReturnType<typeof setTimeout>|undefined
const parameters=()=>new URLSearchParams({entity:props.entity,q:query.value.trim(),page:String(page.value),active:props.active,category:props.category,company_id:props.companyId,worker_id:props.workerId,craft:props.craft})
async function load(){
 const current=++generation
 loading.value=true;error.value=''
 try{
  const result=await api<PageResult<Option>>('/reference-options?'+parameters())
  if(current!==generation)return
  options.value=result.rows;total.value=result.total
  if(options.value.some(item=>item.id===props.modelValue))missing.value=false
  if(props.modelValue&&!options.value.some(item=>item.id===props.modelValue)&&selected.value?.id!==props.modelValue){
   const found=await api<PageResult<Option>>('/reference-options?'+new URLSearchParams({entity:props.entity,id:props.modelValue}))
   if(current===generation){selected.value=found.rows[0]||null;missing.value=!selected.value}
  }
 }catch(e){if(current!==generation||e instanceof SessionChangedError)return;options.value=[];total.value=0;error.value=(e as Error).message}
 finally{if(current===generation)loading.value=false}
}
function schedule(){page.value=1;if(timer)clearTimeout(timer);timer=setTimeout(()=>{void load()},250)}
function choose(value:string){const item=options.value.find(option=>option.id===value)||selected.value?.id===value&&selected.value||null;selected.value=item||null;missing.value=false;emit('update:modelValue',value);emit('change',item||null)}
const unavailable=(item:Option)=>props.entity==='workers'&&item.active!==undefined&&!Number(item.active)||props.entity==='warehouses'&&!!props.warehouseWorker&&(props.warehouseWorker==='assigned'?!item.worker_id:!!item.worker_id)
function turn(next:number){page.value=next;void load()}
watch(()=>[props.entity,props.active,props.category,props.companyId,props.workerId,props.craft],()=>{query.value='';page.value=1;options.value=[];selected.value=null;missing.value=false;void load()})
watch(()=>props.modelValue,()=>{if(selected.value?.id!==props.modelValue)selected.value=null;missing.value=false;if(props.modelValue&&!options.value.some(item=>item.id===props.modelValue))void load()})
onMounted(()=>{void load()})
onBeforeUnmount(()=>{generation++;if(timer)clearTimeout(timer)})
</script>

<template>
 <div class="reference-picker">
  <label :for="fieldId">{{label}}</label>
  <div class="reference-control"><select :id="fieldId" :value="modelValue" :disabled="disabled||loading" :required="required" @change="choose(($event.target as HTMLSelectElement).value)"><option value="">{{emptyLabel}}</option><option v-if="modelValue&&!options.some(item=>item.id===modelValue)" :value="modelValue">{{selectedLabel}}{{missing?'（历史资料）':''}}</option><option v-for="item in options" :key="item.id" :value="item.id" :disabled="unavailable(item)">{{item.name}}{{item.unit?'（'+item.unit+'）':''}}{{item.active!==undefined&&!Number(item.active)?'（已停用）':''}}</option></select><button type="button" class="reference-toggle" :disabled="disabled" :aria-label="(expanded?'收起':'搜索或翻页')+label" :aria-expanded="expanded" :aria-controls="fieldId+'-search'" @click="expanded=!expanded">{{expanded?'收起':'搜索'}}</button></div>
  <div v-if="expanded" :id="fieldId+'-search'" class="reference-search"><input v-model="query" type="search" :aria-label="searchPlaceholder" :placeholder="searchPlaceholder" :disabled="disabled" @input="schedule" @keydown.enter.prevent="page=1;load()"><button type="button" :disabled="disabled||loading||page<=1" aria-label="上一页选项" @click="turn(page-1)">‹</button><button type="button" :disabled="disabled||loading||page*50>=total" aria-label="下一页选项" @click="turn(page+1)">›</button></div>
  <small v-if="error" class="error" role="alert">{{error}}<button type="button" :disabled="disabled||loading" @click="load">重试</button></small>
  <small v-else-if="missing" class="result-count">已选资料不在现有目录中，请核对后再保存</small>
  <small v-else-if="expanded&&!loading" class="result-count">{{total ? '共 '+total+' 项 · 第 '+page+' 页' : '暂无匹配资料'}}</small>
 </div>
</template>

<style scoped>
.reference-picker{min-width:0;max-width:100%;display:flex;flex-direction:column;gap:5px;align-self:start}.reference-picker>label{flex:none;width:auto;min-width:0}.reference-picker select,.reference-picker input{width:100%;min-width:0}.reference-control,.reference-search{display:flex;gap:4px;align-items:center;min-width:0}.reference-control select{flex:1;min-width:0}.reference-toggle{flex:0 0 auto;white-space:nowrap;padding:8px 10px}.reference-search input{flex:1}.reference-search button{flex:0 0 32px;width:32px;height:40px;padding:0}.reference-picker small{font-size:12px}.result-count{color:#667487}
</style>
