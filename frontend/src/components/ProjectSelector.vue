<script setup lang="ts">
import {computed,ref,watch} from 'vue'
import type {ProjectRecord} from '../projects'
import type {OrderDraft} from '../workflows/models'
const props=defineProps<{draft:OrderDraft;projects:ProjectRecord[];disabled:boolean;page:number;total:number}>()
const emit=defineEmits<{change:[];search:[query:string];page:[page:number]}>()
const query=ref('')
const options=computed(()=>props.projects.filter(project=>!!props.draft.id||project.status==='进行中'))
watch(()=>props.draft.company_id,()=>{query.value=''})
</script>
<template>
 <div v-if="draft.company_id" class="project-selector">
  <div v-if="!draft.id" class="project-search"><label>查找已有施工地址<input v-model="query" maxlength="300" :disabled="disabled" placeholder="输入施工地址或工地编号" @keydown.enter.prevent="emit('search',query.trim())"></label><button type="button" :disabled="disabled" @click="emit('search',query.trim())">查找</button></div>
  <label>{{draft.id?'所属施工地址':'已有施工地址（首次施工可留空）'}}<select v-model="draft.project_id" :disabled="disabled" @change="emit('change')"><option value="">{{draft.id?'历史记录未关联工地':'首次施工：直接填写下方施工地址'}}</option><option v-for="project in options" :key="project.id" :value="project.id">{{project.address}} · {{project.number}}{{project.status!=='进行中'?'（'+project.status+'）':''}}</option><option v-if="draft.project_id&&!options.some(project=>project.id===draft.project_id)" :value="draft.project_id">{{draft.address||'当前已选施工地址'}}{{draft.project_number?' · '+draft.project_number:''}}</option></select></label>
  <div v-if="!draft.id" class="project-pager"><button type="button" :disabled="disabled||page<=1" @click="emit('page',page-1)">上一页地址</button><span>共 {{total}} 个地址 · 第 {{page}} / {{Math.max(1,Math.ceil(total/100))}} 页</span><button type="button" :disabled="disabled||page*100>=total" @click="emit('page',page+1)">下一页地址</button></div>
  <p v-if="!draft.id" class="selector-hint">首次施工填地址，保存时自动建档；后续查找并选择原地址继续填写，施工、票据和收款归到同一工地。</p><p v-else class="selector-hint">已保存记录保留原工地归属；历史未关联记录可从“工地汇总”逐张关联。</p>
 </div>
</template>
<style scoped>.project-pager{display:flex;align-items:center;gap:8px;flex-wrap:wrap;width:100%;font-size:12px}.project-selector{display:flex;gap:10px;flex-wrap:wrap;padding:2px 0 10px;min-width:0;width:100%;overflow:hidden}.project-selector label{min-width:0;flex:1 1 220px}.project-selector input,.project-selector select{width:100%;min-width:0}.project-search{display:flex;gap:8px;align-items:end;flex:1 1 280px;min-width:0}.project-search label{flex:1}.selector-hint{flex-basis:100%;color:#758499;font-size:12px}</style>
