<script setup lang="ts">
import {measureUnit} from '../orderSegments'
import type {OrderLine} from '../workflows/models'
defineProps<{line:OrderLine;editable:boolean;removable:boolean;canWages:boolean;allowedCrafts:string[]}>()
const emit=defineEmits<{mode:[];remove:[]}>()
</script>
<template>
  <div v-if="!line.temporary" class="fixed-identity"><strong>{{line.name}}</strong><small>固定工艺{{line.spec?' · '+line.spec:''}}</small></div>
  <fieldset v-else class="temporary-identity" :disabled="!editable">
    <label>临时类别<select v-model="line.category"><option v-for="category in allowedCrafts" :key="category">{{category}}</option></select></label>
    <label>临时项目<input v-model="line.name" required maxlength="200"></label>
    <label>计量<select v-model="line.mode" @change="emit('mode')"><option>平方</option><option>立方</option><option>米</option><option>个</option><option>家</option><option>根</option><option>处</option><option>孔位</option><option>副</option><option>张</option><option>套</option><option>金额</option></select></label>
    <label v-if="canWages">{{line.mode==='金额'?'整项工资（元）':'工资单价（元/'+measureUnit(line.mode)+'）'}}<input type="number" min="0" max="1000000" step="0.01" :value="Number(line.wage_cents||0)/100" @input="line.wage_cents=Math.round(Number(($event.target as HTMLInputElement).value)*100)"></label>
    <button type="button" :disabled="!removable" @click="emit('remove')">移除临时项</button>
  </fieldset>
</template>
<style scoped>
.fixed-identity{display:flex;align-items:baseline;gap:9px;flex-wrap:wrap}.fixed-identity strong{font-size:14px;color:#24364b}.fixed-identity small{color:#65768c;font-size:11px}.temporary-identity{display:grid;grid-template-columns:minmax(115px,.8fr) minmax(150px,1.2fr) minmax(95px,.65fr) minmax(130px,1fr) auto;gap:7px;align-items:end;border:0;margin:0;padding:0;min-width:0}.temporary-identity label{min-width:0;font-size:11px}.temporary-identity input,.temporary-identity select{width:100%;min-width:0}.temporary-identity button{white-space:nowrap}@media(max-width:800px){.temporary-identity{grid-template-columns:repeat(2,minmax(0,1fr))}.temporary-identity button{justify-self:start}}
</style>
