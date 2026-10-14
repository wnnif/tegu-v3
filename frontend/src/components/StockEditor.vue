<script setup lang="ts">
import {computed,ref} from 'vue'
import ReferenceSelect from './ReferenceSelect.vue'
import {formatQuantum,fromQuantum,moneyCents,moneyFromCents,toQuantum} from '../stockQuantity'
import type {StockDraft,StockLine,CatalogRow} from '../workflows/models'

const props=defineProps<{draft:StockDraft;warehouses:CatalogRow[];busy:boolean}>()
const emit=defineEmits<{save:[];source:[];cancel:[]}>()
const unitError=ref('')
const needsSource=computed(()=>!['进货','报溢'].includes(props.draft.kind))
const needsDestination=computed(()=>!['报损','施工使用','商品销售'].includes(props.draft.kind))
const sourceLabel=computed(()=>props.draft.kind==='领料'?'领出仓库':props.draft.kind==='退料'?'退料人 / 来源位置':props.draft.kind==='转料'?'转出人 / 来源位置':props.draft.kind==='施工使用'?'实际用料位置':props.draft.kind==='商品销售'?'实际发货位置':'调出仓库 / 人员')
const destinationLabel=computed(()=>props.draft.kind==='领料'?'领料人 / 个人结余':props.draft.kind==='退料'?'退回总仓':props.draft.kind==='转料'?'接收人 / 位置':'调入仓库 / 人员')
const sourceWorker=computed((): 'assigned'|'unassigned'|undefined=>props.draft.kind==='退料'||props.draft.kind==='转料'?'assigned':props.draft.kind==='领料'?'unassigned':undefined)
const destinationWorker=computed((): 'assigned'|'unassigned'|undefined=>props.draft.kind==='领料'||props.draft.kind==='转料'?'assigned':props.draft.kind==='退料'?'unassigned':undefined)
const total=computed(()=>props.draft.kind==='进货'?props.draft.lines.reduce((sum,line)=>sum+BigInt(String(line.total_cents||0)),0n):0n)

function inputQuantity(line:StockLine,value:string){
 line.input_qty=value
 line.input_quantity=value
 line.quantity_quantum=value.trim()?toQuantum(value,line.input_unit,line)||'':''
 unitError.value=''
}
function changeUnit(line:StockLine,event:Event){
 const select=event.target as HTMLSelectElement
 const next=select.value
 const quantum=line.quantity_quantum||''
 if(quantum){
  const exact=fromQuantum(quantum,next,line)
  if(exact===null){select.value=line.input_unit;unitError.value=`${line.name}当前数量不能精确表示为所选单位，请保持原单位录入。`;return}
  line.input_qty=exact
  line.input_quantity=exact
 }
 line.input_unit=next
 unitError.value=''
}
function inputAmount(line:StockLine,value:string){
 line.input_amount=value
 line.total_cents=value.trim()?moneyCents(value)||'':''
}
function validLine(line:StockLine){
 if(!line.input_qty.trim())return true
 if(!line.quantity_quantum||BigInt(line.quantity_quantum)<=0n)return false
 return props.draft.kind!=='进货'||(line.input_amount?.trim()!==''&&moneyCents(line.input_amount||'')!==null)
}
const invalid=computed(()=>props.draft.lines.some(line=>!validLine(line)))
const selected=computed(()=>props.draft.lines.some(line=>!!line.quantity_quantum))
const sourceQuantity=(line:StockLine)=>line.available_quantum?formatQuantum(String(line.available_quantum),'base',line):formatQuantum(String(line.available||0),'base',{...line,base_factor:1,aux_factor:1})
</script>

<template>
 <form @submit.prevent="emit('save')" autocomplete="off">
  <fieldset :disabled="busy">
   <div class="form-grid">
    <ReferenceSelect v-if="needsSource" v-model="draft.from" entity="warehouses" :label="sourceLabel" empty-label="请选择" search-placeholder="搜索仓库或人员" active="1" :warehouse-worker="sourceWorker" required :disabled="busy" @change="emit('source')" />
    <ReferenceSelect v-if="needsDestination" v-model="draft.to" entity="warehouses" :label="destinationLabel" empty-label="请选择" search-placeholder="搜索仓库或人员" active="1" :warehouse-worker="destinationWorker" required :disabled="busy" />
    <label>日期<input type="date" v-model="draft.date" required></label>
    <label>经手人<input v-model="draft.handler" maxlength="100" required></label>
    <label>备注<input v-model="draft.note" maxlength="5000"></label>
   </div>
   <p v-if="unitError" class="error" role="alert">{{unitError}}</p>
   <div class="table-wrap"><table class="stock-entry"><thead><tr>
    <th>商品 / 规格</th><th v-if="needsSource">来源可用</th><th>单位</th><th>数量</th><th>折合基本数量</th><th v-if="draft.kind==='进货'">本次进货金额（元）</th><th>明细备注</th>
   </tr></thead><tbody>
    <tr v-for="line in draft.lines" :key="line.product_id">
     <td>{{line.name}}<small>{{line.spec||'—'}}</small></td>
     <td v-if="needsSource">{{sourceQuantity(line)}} {{line.unit}}</td>
     <td><select :value="line.input_unit" :aria-label="line.name+'录入单位'" @change="changeUnit(line,$event)"><option value="base">{{line.unit}}</option><option v-if="line.aux_unit" value="aux">{{line.aux_unit}}</option></select></td>
     <td><input type="text" inputmode="decimal" :value="line.input_qty" :aria-label="line.name+'数量'" placeholder="留空不操作" @input="inputQuantity(line,($event.target as HTMLInputElement).value)"></td>
     <td>{{line.quantity_quantum?formatQuantum(line.quantity_quantum,'base',line):'—'}} {{line.unit}}</td>
     <td v-if="draft.kind==='进货'"><input type="text" inputmode="decimal" :value="line.input_amount" :aria-label="line.name+'本次进货金额'" placeholder="0.00" @input="inputAmount(line,($event.target as HTMLInputElement).value)"></td>
     <td><input v-model="line.note" maxlength="1000" :aria-label="line.name+'备注'"></td>
    </tr>
    <tr v-if="!draft.lines.length"><td :colspan="draft.kind==='进货'?7:needsSource?6:5" class="empty">{{needsSource?'来源位置暂无可用商品':'暂无已启用商品'}}</td></tr>
   </tbody></table></div>
  </fieldset>
  <footer><strong v-if="draft.kind==='进货'">本次合计：¥ {{moneyFromCents(String(total))}}</strong><button type="button" :disabled="busy" @click="emit('cancel')">取消</button><button class="primary" :disabled="busy||invalid||!selected">{{draft.id?'保存修改并调整库存':'保存并更新库存'}}</button></footer>
 </form>
</template>
