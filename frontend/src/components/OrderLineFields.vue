<script setup lang="ts">
import {computed,ref,watch} from 'vue'
import { customerPrice, hasCustomerPrice, CUSTOMER_PRICE_HIDDEN } from '../customerPrices'
import {filled,isCountCraftMode,lineAmount,lineQuantityMicro,quantityLabel,measureUnit} from '../orderSegments'
import ReferenceSelect from './ReferenceSelect.vue'
import type {CatalogRow,OrderLine,OrderSegment} from '../workflows/models'
const props=defineProps<{part:'craft'|'location'|'worker'|'price'|'measure'|'quantity'|'amount'|'note';line:OrderLine;canCustomerPrices:boolean;canWages?:boolean;accountEditable:boolean;editable:boolean;categoryDefaultRow?:boolean;defaultName?:string;defaultWorkerId?:string;selectedWorkerName?:string;workerHint?:string;readonlyLabel?:string;includedLabel?:string;hintError?:boolean;canAddLocation?:boolean;showRemoveLocation?:boolean;canRemoveLocation?:boolean}>()
const emit=defineEmits<{worker:[item:CatalogRow|null];addLocation:[];removeLocation:[]}>()
const money=customerPrice
const pricesVisible=computed(()=>props.canCustomerPrices&&hasCustomerPrice(props.line.price_cents))
const freshSegment=(mode:string):OrderSegment=>({mode,surface:'地面',length:'',width:'',height:'',count:'',amount_cents:0})
const chooseOwnWorker=ref(false)
const explicitWorker=computed(()=>!!props.line.worker_id||!!props.line.worker_override)
const showWorkerChoice=computed(()=>!props.categoryDefaultRow||explicitWorker.value||chooseOwnWorker.value)
// Clearing an old actual_location must not make its editor disappear before saving.
const legacyLocation=ref(!!props.line.actual_location)
const editLegacyLocation=ref(false)
watch(()=>props.line,line=>{legacyLocation.value=!!line.actual_location;editLegacyLocation.value=false;chooseOwnWorker.value=false},{flush:'sync'})
watch(()=>props.line.actual_location,value=>{if(value)legacyLocation.value=true})
// Only exact saved text is redundant. Never guess aliases or use current catalog names.
const repeatedLocation=computed(()=>!!props.line.subitem_id&&!!props.line.subitem_name&&props.line.actual_location===props.line.subitem_name&&!locationTooLong.value)
const showActualLocation=computed(()=>!props.line.subitem_id||(legacyLocation.value&&(!repeatedLocation.value||editLegacyLocation.value)))
function chooseWorker(item:CatalogRow|null){if(props.accountEditable)emit('worker',item)}
function setPrice(event:Event){if(!pricesVisible.value||!props.editable)return;props.line.price_cents=Math.round(Number((event.target as HTMLInputElement).value)*100)}
const effectivePrice=(line:OrderLine)=>line.use_included_price?0:Number(line.price_cents||0)
const locationTooLong=computed(()=>Array.from(props.line.actual_location||'').length>200)
function requestAddLocation(){if(props.canAddLocation)emit('addLocation')}
function requestRemoveLocation(){if(props.canRemoveLocation&&props.showRemoveLocation)emit('removeLocation')}
const actualWage=computed(()=>{
 if(!props.canWages||!filled(props.line))return undefined
 if(props.line.wage_override_cents!=null&&props.line.wage_override_cents!=='')return Number(props.line.wage_override_cents)
 if(props.line.wage_cents===undefined)return props.line.labor_cents===undefined?undefined:Number(props.line.labor_cents)
 return Number((lineQuantityMicro(props.line)*BigInt(String(props.line.wage_cents||0))+500000n)/1000000n)
})
const wageInput=computed(()=>props.line.wage_override_cents==null||props.line.wage_override_cents===''?'':String(Number(props.line.wage_override_cents)/100))
function setWage(event:Event){if(!props.canWages||!props.editable)return;const value=(event.target as HTMLInputElement).value,amount=value===''?null:Math.round(Number(value)*100);if(props.line.wage_override_cents!==amount)props.line.wage_adjustment_reason='';props.line.wage_override_cents=amount}
function setChargeMode(event:Event){if(props.canCustomerPrices&&props.editable)props.line.charge_mode=(event.target as HTMLSelectElement).value as 'normal'|'free'|'redo'}
</script>
<template>
  <template v-if="part==='craft'">
    <strong v-if="!line.temporary" class="line-craft-name">{{line.name||line.category||'未记录工艺'}}</strong>
    <small class="field-label">{{line.mode}} · {{line.subitem_id?'按部位填写':'整项工艺'}}</small>
    <small v-if="line.included_by_craft_id" class="included-note">{{!pricesVisible?'组合工艺，收费按已保存配置':line.use_included_price?'组合施工：本行客户收费为 0，工资按既有规则':'历史独立收费，保留原单价'}}</small>
    <small v-if="line.use_included_price&&pricesVisible" class="included-note">{{includedLabel}}</small>
  </template>
  <div v-else-if="part==='location'" class="location-fields">
    <div v-if="line.subitem_id" class="configured-subitem"><strong>{{line.subitem_name||'配置部位名称未记录'}}</strong></div>
    <div v-if="repeatedLocation&&!showActualLocation" class="location-alias"><small>原实际施工部位同名，已保留</small><button v-if="editable" type="button" class="link" data-action="edit-legacy-location" @click="editLegacyLocation=true">修改原实际部位</button></div>
    <div v-if="showActualLocation" class="actual-location">
      <label>{{line.subitem_id?'原实际施工部位':'实际施工部位'}}<input v-model="line.actual_location" name="actual_location" data-field="actual-location" :disabled="!editable" :aria-invalid="locationTooLong||undefined" placeholder="填写实际施工部位（选填）"></label>
      <small v-if="locationTooLong" class="error">实际施工部位最多填写200字</small>
    </div>
    <div v-if="line.region" class="legacy-region"><small class="field-label">历史兼容文本（原 region）</small><strong>{{line.region}}</strong><small>保留旧原文，不代填配置部位或实际施工部位。</small></div>
    <div class="location-actions"><button type="button" class="link" data-action="add-location" :disabled="!canAddLocation" @click="requestAddLocation">{{line.subitem_id?'＋ 同部位施工明细':'＋ 新增施工部位'}}</button><button v-if="showRemoveLocation" type="button" class="link" data-action="remove-location" :disabled="!canRemoveLocation" @click="requestRemoveLocation">移除此部位</button></div>
  </div>
  <template v-else-if="part==='worker'">
    <template v-if="accountEditable">
      <template v-if="showWorkerChoice"><small v-if="categoryDefaultRow&&explicitWorker" class="worker-choice-hint">本项单独指定，不随默认人变化</small><ReferenceSelect class="line-worker-picker" :model-value="line.worker_id||''" entity="workers" label="本项施工人" :empty-label="defaultWorkerId?'继承大类默认施工人':'请选择本项施工人'" search-placeholder="搜索本项施工人" :selected-name="line.worker_name" :craft="line.category" @change="chooseWorker" /></template>
      <button v-else type="button" class="link choose-worker" @click="chooseOwnWorker=true">单独指定本项</button>
    </template>
    <small class="selected-worker-name">{{selectedWorkerName||line.worker_name||'施工人未记录'}}</small><small v-if="workerHint" :class="{error:hintError}">{{workerHint}}</small><span v-if="readonlyLabel" class="readonly-label">{{readonlyLabel}}</span>
  </template>
  <div v-else-if="part==='price'" class="price-cell">
    <span v-if="!pricesVisible" class="price-hidden">{{CUSTOMER_PRICE_HIDDEN}}</span>
    <label v-else-if="line.temporary" class="unit-price-input">{{line.mode==='金额'?'整项客户收费（元）':'客户单价（元/'+measureUnit(line.mode)+'）'}}<input type="number" min="0" max="1000000" step="0.01" :value="Number(line.price_cents||0)/100" :disabled="!editable" @input="setPrice($event)"></label>
    <div v-else class="fixed-price"><small>{{line.mode==='金额'?'整项客户收费':'客户单价'}}</small><strong>{{money(effectivePrice(line))}} 元<span v-if="line.mode!=='金额'">/{{measureUnit(line.mode)}}</span></strong><small v-if="line.use_included_price">组合内不向客户收费</small></div>
    <label v-if="pricesVisible" class="charge-mode">收费方式<select :value="line.charge_mode||'normal'" :disabled="!editable" @change="setChargeMode"><option value="normal">正常收费</option><option value="free">免费施工</option><option value="redo">补做不收费</option></select></label>
  </div>
  <fieldset v-else-if="part==='measure'" class="measure-input" :disabled="!editable">
    <label v-if="line.mode==='金额'" class="check"><input v-model="line.entered" type="checkbox">已施工（按单价列整项收费）</label>
    <template v-else><div v-for="(segment,segmentIndex) in line.segments" :key="segmentIndex" class="measure-segment">
      <label v-if="isCountCraftMode(line.mode)">数量（{{line.mode}}）<input v-model="segment.count" type="number" min="1" max="1000000" step="1" :placeholder="'填写'+line.mode+'数量'"></label>
      <label v-else-if="line.mode==='米'">长度（米）<input v-model="segment.length" type="number" min="0.000001" max="10000" step="any" placeholder="填写长度"></label>
      <template v-else><label class="surface-label">施工面<select v-model="segment.surface" aria-label="施工面"><option>地面</option><option>墙面</option><option>其他</option></select></label><label>长（米）<input v-model="segment.length" type="number" min="0.000001" max="1000000" step="any"></label><span>×</span><label>宽（米）<input v-model="segment.width" type="number" min="0.000001" max="1000000" step="any"></label><template v-if="line.mode==='立方'"><span>×</span><label>高（米）<input v-model="segment.height" type="number" min="0.000001" max="1000000" step="any"></label></template></template>
      <button v-if="line.segments.length>1" type="button" class="link" @click="line.segments.splice(segmentIndex,1)">删除此段</button>
    </div><button v-if="!isCountCraftMode(line.mode)" type="button" class="link" :disabled="line.mode==='米'&&line.segments.length>=100" @click="line.segments.push(freshSegment(line.mode))">{{line.mode==='米'?'＋ 一段长度':'＋ 一段尺寸'}}</button></template>
  </fieldset>
  <div v-else-if="part==='quantity'" class="summary-cell"><small>{{isCountCraftMode(line.mode)?'数量小计':'方量小计'}}</small><strong>{{filled(line)?quantityLabel(line):'未填写'}}</strong></div>
  <div v-else-if="part==='amount'" class="summary-cell amount-cell"><small>金额小计</small><strong>{{pricesVisible?money(lineAmount(line))+' 元':CUSTOMER_PRICE_HIDDEN}}</strong><details v-if="canWages" class="line-wage"><summary>人工工资{{actualWage===undefined?'':` ${money(actualWage)} 元`}}</summary><p>工资标准：{{line.wage_cents===undefined?'未返回':money(line.wage_cents)+' 元/'+measureUnit(line.mode)}}</p><label>本项实际工资（元）<input :value="wageInput" type="number" min="0" max="1000000" step="0.01" :disabled="!editable" placeholder="留空按标准计算" @input="setWage"></label><label>工资调整原因<input v-model="line.wage_adjustment_reason" :disabled="!editable" maxlength="500" placeholder="调整工资时必填"></label></details></div>
  <label v-else class="note-input">备注<input v-model="line.description" :disabled="!editable" maxlength="1000" placeholder="选填"></label>
</template>
<style scoped>
input,select{width:100%;min-width:0}label{font-size:11px}.line-craft-name{display:block;margin-bottom:3px;font-size:12px;line-height:1.35;overflow-wrap:anywhere}.field-label{display:block;margin-bottom:4px;color:#65768c;font-size:11px}.configured-subitem{display:grid;gap:3px}.configured-subitem strong{font-size:13px;overflow-wrap:anywhere}.legacy-region{display:grid;gap:4px}.location-alias{display:grid;gap:3px}.location-alias button{justify-self:start;white-space:normal;text-align:left}.legacy-region small:last-child,.location-alias small{color:#687c96;font-size:10px;line-height:1.35}.legacy-region strong{font-size:12px;overflow-wrap:anywhere}.location-fields{display:grid;align-content:start;gap:5px;min-width:0}.actual-location,.actual-location label{display:grid;gap:4px;min-width:0}.actual-location input{padding:6px 7px}.location-actions{display:flex;flex-wrap:wrap;gap:4px}.location-actions button{white-space:normal;text-align:left}.included-note{display:block;margin-top:4px;color:#536982;font-size:10px;line-height:1.35}.worker-choice-hint{color:#61758f;font-size:10px;line-height:1.35}.choose-worker{justify-self:start;text-align:left;white-space:normal}.selected-worker-name{display:block;font-size:12px;line-height:1.4;overflow-wrap:anywhere;color:#314361}.error{color:#b42318}.readonly-label{display:inline-block;margin-top:4px;color:#795416;background:#fff4d6;border-radius:4px;padding:2px 5px;font-size:10px;line-height:1.35}.price-hidden{color:#65768c;font-size:12px;overflow-wrap:anywhere}.price-cell,.fixed-price{display:grid;gap:4px}.fixed-price small,.summary-cell small{color:#65768c;font-size:10px}.fixed-price strong,.summary-cell strong{font-size:13px;font-variant-numeric:tabular-nums}.unit-price-input{display:grid;gap:4px}.measure-input{display:grid;gap:3px;border:0;margin:0;padding:0;min-width:0}.measure-segment{display:flex;flex-wrap:wrap;align-items:end;gap:4px;margin-bottom:4px}.measure-segment label{flex:1 1 58px;min-width:0;max-width:105px}.measure-segment>span{padding-bottom:8px;color:#65768c}.measure-segment .surface-label{flex:1 1 78px;max-width:105px}.measure-segment input,.measure-segment select{padding:5px 6px}.check{display:flex;align-items:center;gap:5px}.check input{width:auto}.link{padding:3px 4px;font-size:11px}.summary-cell{display:grid;align-content:center;gap:4px;min-height:42px}.amount-cell strong{color:#1c3552}.note-input{display:grid;gap:4px}.note-input input{padding:6px 7px}
.charge-mode{display:grid;gap:3px}.charge-mode select{font-size:11px;padding:4px}.line-wage{min-width:0;font-size:11px}.line-wage summary{cursor:pointer;color:#3a618f;line-height:1.4}.line-wage p{margin:5px 0}.line-wage label{display:grid;gap:4px;margin-top:5px}
</style>
