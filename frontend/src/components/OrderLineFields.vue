<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import { customerPrice, hasCustomerPrice, CUSTOMER_PRICE_HIDDEN } from '../customerPrices'
import {filled,isCountCraftMode,lineAmount,lineQuantityMicro,quantityLabel,measureUnit} from '../orderSegments'
import ReferenceSelect from './ReferenceSelect.vue'
import type {CatalogRow,OrderLine,OrderSegment} from '../workflows/models'
const props=defineProps<{part:'craft'|'location'|'worker'|'price'|'measure'|'quantity'|'amount'|'note';line:OrderLine;canCustomerPrices:boolean;canWages?:boolean;accountEditable:boolean;editable:boolean;categoryDefaultRow?:boolean;defaultName?:string;defaultWorkerId?:string;selectedWorkerName?:string;workerHint?:string;readonlyLabel?:string;includedLabel?:string;hintError?:boolean;canAddLocation?:boolean;showRemoveLocation?:boolean;canRemoveLocation?:boolean}>()
const emit=defineEmits<{worker:[item:CatalogRow|null];addLocation:[];removeLocation:[];measurementIncomplete:[incomplete:boolean]}>()
const money=customerPrice
const pricesVisible=computed(()=>props.canCustomerPrices&&hasCustomerPrice(props.line.price_cents))
const freshSegment=(mode:string):OrderSegment=>({mode,surface:'地面',length:'',width:'',height:'',count:'',amount_cents:0})
const measurementDrafts=new WeakMap<OrderSegment,string>()
const measurementParts=(value:string,mode:string):string[]|null=>{
 const count=mode==='米'?1:mode==='立方'?3:2,parts=value.trim().split(/[×xX*]/)
 if(parts.length!==count)return null
 const limits=mode==='米'?[10000]:Array(count).fill(1000000)
 if(parts.some((part,index)=>!/^\d+(?:\.\d+)?$/.test(part.trim())||!Number.isFinite(Number(part))||Number(part)<=0||Number(part)>limits[index]!))return null
 return parts.map(part=>part.trim())
}
function measurementText(segment:OrderSegment,mode:string){
 if(measurementDrafts.has(segment))return measurementDrafts.get(segment)!
 if(mode==='米')return segment.length===''||segment.length==null?'':String(segment.length)
 const parts=[segment.length,segment.width,...(mode==='立方'?[segment.height]:[])].map(value=>value==null?'':String(value))
 return parts.every(value=>!value)?'':parts.join('×')
}
function setMeasurement(event:Event,segment:OrderSegment){
 const value=(event.target as HTMLInputElement).value
 measurementDrafts.set(segment,value)
 const parts=measurementParts(value,props.line.mode)
 emit('measurementIncomplete',value.trim()!==''&&!parts)
 if(!parts){segment.length='';segment.width='';segment.height='';return}
 segment.length=parts[0]!;segment.width=parts[1]||'';segment.height=parts[2]||''
}
function setDimension(event:Event,segment:OrderSegment,field:'length'|'width'|'height'){
 measurementDrafts.delete(segment)
 emit('measurementIncomplete',false)
 segment[field]=(event.target as HTMLInputElement).value
}
function addSegment(){if(!isCountCraftMode(props.line.mode))props.line.segments.push(freshSegment(props.line.mode))}
const chooseOwnWorker=ref(false)
const explicitWorker=computed(()=>!!props.line.worker_id||!!props.line.worker_override)
const showWorkerChoice=computed(()=>chooseOwnWorker.value)
const workerActionLabel=computed(()=>explicitWorker.value?'改人':'指定')
// Clearing an old actual_location must not make its editor disappear before saving.
const legacyLocation=ref(!!props.line.actual_location)
const editLegacyLocation=ref(false)
watch(()=>props.line,line=>{legacyLocation.value=!!line.actual_location;editLegacyLocation.value=false;chooseOwnWorker.value=false},{flush:'sync'})
watch(()=>props.line.actual_location,value=>{if(value)legacyLocation.value=true})
// Only exact saved text is redundant. Never guess aliases or use current catalog names.
const repeatedLocation=computed(()=>!!props.line.subitem_id&&!!props.line.subitem_name&&props.line.actual_location===props.line.subitem_name&&!locationTooLong.value)
const showActualLocation=computed(()=>!props.line.subitem_id||(legacyLocation.value&&(!repeatedLocation.value||editLegacyLocation.value)))
function chooseWorker(item:CatalogRow|null){if(props.accountEditable){emit('worker',item);chooseOwnWorker.value=false}}
function setPrice(event:Event){if(!pricesVisible.value||!props.editable)return;props.line.price_cents=Math.round(Number((event.target as HTMLInputElement).value)*100)}
const effectivePrice=(line:OrderLine)=>line.use_included_price?0:Number(line.price_cents||0)
const locationTooLong=computed(()=>Array.from(props.line.actual_location||'').length>200)
function requestAddLocation(){if(props.canAddLocation)emit('addLocation')}
function requestRemoveLocation(){if(props.canRemoveLocation&&props.showRemoveLocation)emit('removeLocation')}
watch(()=>props.line.segments,()=>{if(props.part==='measure')emit('measurementIncomplete',false)},{flush:'sync'})
onBeforeUnmount(()=>{if(props.part==='measure')emit('measurementIncomplete',false)})
const actualWage=computed(()=>{
 if(!props.canWages||!filled(props.line))return undefined
 if(props.line.wage_override_cents!=null&&props.line.wage_override_cents!=='')return Number(props.line.wage_override_cents)
 if(props.line.wage_configured===false)return undefined
 if(props.line.wage_cents===undefined)return props.line.labor_cents===undefined?undefined:Number(props.line.labor_cents)
 return Number((lineQuantityMicro(props.line)*BigInt(String(props.line.wage_cents||0))+500000n)/1000000n)
})
const wageInput=computed(()=>props.line.wage_override_cents==null||props.line.wage_override_cents===''?'':String(Number(props.line.wage_override_cents)/100))
function setWage(event:Event){if(!props.canWages||!props.editable)return;const value=(event.target as HTMLInputElement).value,amount=value===''?null:Math.round(Number(value)*100);if(props.line.wage_override_cents!==amount)props.line.wage_adjustment_reason='';props.line.wage_override_cents=amount}
function setChargeMode(event:Event){if(props.canCustomerPrices&&props.editable)props.line.charge_mode=(event.target as HTMLSelectElement).value as 'normal'|'free'|'redo'}
</script>
<template>
  <template v-if="part==='craft'">
    <strong class="line-craft-name" :title="line.mode+' · '+(line.subitem_id?'按部位填写':'整项工艺')">{{line.category||'未分类'}} · {{line.name||'未记录工艺'}}<small class="craft-mode">{{line.mode}}</small></strong>
    <small v-if="line.included_by_craft_id" class="included-note" :title="!pricesVisible?'组合工艺，收费按已保存配置':line.use_included_price?'组合施工：本行客户收费为 0，工资按既有规则':'历史独立收费，保留原单价'">{{line.use_included_price?'含价':'组合'}}</small>
    <small v-else-if="line.use_included_price&&pricesVisible" class="included-note" :title="includedLabel">含价</small>
  </template>
  <div v-else-if="part==='location'" class="location-fields">
    <strong v-if="line.subitem_id" class="configured-subitem">{{line.subitem_name||'配置部位名称未记录'}}</strong>
    <button v-if="repeatedLocation&&!showActualLocation&&editable" type="button" class="link location-legacy-edit" data-action="edit-legacy-location" title="原实际施工部位与配置部位同名，已保留" @click="editLegacyLocation=true">改原部位</button>
    <label v-if="showActualLocation" class="actual-location"><span class="visually-hidden">{{line.subitem_id?'原实际施工部位':'实际施工部位'}}</span><input v-model="line.actual_location" name="actual_location" data-field="actual-location" :aria-label="line.subitem_id?'原实际施工部位':'实际施工部位'" :disabled="!editable" :aria-invalid="locationTooLong||undefined" placeholder="实际施工部位（选填）"><small v-if="locationTooLong" class="error">最多填写200字</small></label>
    <details v-if="line.region" class="legacy-region"><summary title="查看保留的历史原始部位文字">历史</summary><div><small class="field-label">历史兼容文本（原 region）</small><strong>{{line.region}}</strong><small>保留旧原文，不代填配置部位或实际施工部位。</small></div></details>
    <div class="location-actions"><button type="button" class="link" data-action="add-location" :aria-label="line.subitem_id?'新增同部位施工明细':'新增施工部位'" :title="line.subitem_id?'新增同部位施工明细':'新增施工部位'" :disabled="!canAddLocation" @click="requestAddLocation">＋部位</button><button v-if="showRemoveLocation" type="button" class="link" data-action="remove-location" aria-label="移除此部位" title="移除此部位" :disabled="!canRemoveLocation" @click="requestRemoveLocation">移除</button></div>
  </div>
  <template v-else-if="part==='worker'">
    <template v-if="accountEditable">
      <div v-if="showWorkerChoice" class="worker-edit-control"><ReferenceSelect class="line-worker-picker" :model-value="line.worker_id||''" entity="workers" label="本项施工人" :empty-label="defaultWorkerId?'继承大类默认施工人':'请选择本项施工人'" search-placeholder="搜索本项施工人" :selected-name="line.worker_name" :craft="line.category" @change="chooseWorker" /><button type="button" class="link" @click="chooseOwnWorker=false">收起</button></div>
      <div v-else-if="!categoryDefaultRow||explicitWorker" class="worker-row-control"><small class="selected-worker-name">{{selectedWorkerName||line.worker_name||'施工人未记录'}}</small><small v-if="categoryDefaultRow&&explicitWorker" class="visually-hidden">本项单独指定，不随默认人变化</small><button type="button" class="link choose-worker" :aria-label="workerActionLabel+'本项施工人'" @click="chooseOwnWorker=true">{{workerActionLabel}}</button></div>
      <button v-else type="button" class="link choose-worker default-worker-override" aria-label="单独指定本项施工人" @click="chooseOwnWorker=true">指定</button>
    </template>
    <small v-else-if="!categoryDefaultRow||explicitWorker||defaultWorkerId" class="selected-worker-name">{{selectedWorkerName||line.worker_name||'施工人未记录'}}</small>
    <small v-if="workerHint&&hintError" class="worker-hint-error">{{workerHint}}</small><span v-if="readonlyLabel" class="readonly-label">{{readonlyLabel}}</span>
  </template>
  <div v-else-if="part==='price'" class="price-cell">
    <span v-if="!pricesVisible" class="price-hidden">{{CUSTOMER_PRICE_HIDDEN}}</span>
    <label v-else-if="line.temporary" class="unit-price-input">{{line.mode==='金额'?'整项客户收费（元）':'客户单价（元/'+measureUnit(line.mode)+'）'}}<input type="number" min="0" max="1000000" step="0.01" :value="Number(line.price_cents||0)/100" :disabled="!editable" @input="setPrice($event)"></label>
    <div v-else class="fixed-price"><strong>{{money(effectivePrice(line))}} 元<span v-if="line.mode!=='金额'">/{{measureUnit(line.mode)}}</span></strong><small v-if="line.use_included_price" title="组合内不向客户收费">含价</small></div>
    <details v-if="pricesVisible" class="charge-mode"><summary :title="'调整收费方式：正常收费、免费施工或补做不收费'">{{line.charge_mode==='free'?'免费':line.charge_mode==='redo'?'补做':'调整'}}</summary><label>收费方式<select :value="line.charge_mode||'normal'" :disabled="!editable" @change="setChargeMode"><option value="normal">正常收费</option><option value="free">免费施工</option><option value="redo">补做不收费</option></select></label></details>
  </div>
  <fieldset v-else-if="part==='measure'" class="measure-input" :disabled="!editable">
    <label v-if="line.mode==='金额'" class="check"><input v-model="line.entered" type="checkbox">已施工（按单价列整项收费）</label>
    <div v-else class="measure-inline">
      <template v-if="line.segments.length">
        <input v-if="isCountCraftMode(line.mode)" v-model="line.segments[0]!.count" data-field="measure-count" type="number" min="1" max="1000000" step="1" :aria-label="'数量（'+line.mode+'）'" :placeholder="line.mode">
        <input v-else data-field="measure-dimensions" type="text" inputmode="decimal" autocomplete="off" spellcheck="false" :aria-label="line.mode==='米'?'长度（米）':line.mode==='立方'?'长×宽×高（米）':'长×宽（米）'" :placeholder="line.mode==='米'?'长度':line.mode==='立方'?'长×宽×高':'长×宽'" :value="measurementText(line.segments[0]!,line.mode)" @input="setMeasurement($event,line.segments[0]!)">
        <span v-if="!isCountCraftMode(line.mode)&&line.mode!=='米'" class="measure-surface-indicator">{{line.segments[0]!.surface||'地面'}}</span>
      </template>
      <button v-else type="button" class="link measure-first-add" data-action="add-first-segment" @click="addSegment">填写尺寸</button>
      <details v-if="!isCountCraftMode(line.mode)||line.segments.length>1" class="measure-advanced">
        <summary data-action="edit-measurements" :title="'编辑施工面'+(line.segments.length>1?'、多段尺寸':'')">{{line.segments.length>1?'尺寸 '+line.segments.length+'段':'更多'}}</summary>
        <div class="measure-editor">
          <div v-for="(segment,segmentIndex) in line.segments" :key="segmentIndex" class="measure-editor-segment">
            <span class="segment-index">{{segmentIndex+1}}</span>
            <select v-if="!isCountCraftMode(line.mode)&&line.mode!=='米'" v-model="segment.surface" aria-label="施工面"><option>地面</option><option>墙面</option><option>其他</option></select>
            <label v-if="isCountCraftMode(line.mode)">数量<input v-model="segment.count" type="number" min="1" max="1000000" step="1"></label>
            <label v-else-if="line.mode==='米'">长度（米）<input :value="segment.length" type="number" min="0.000001" max="10000" step="any" @input="setDimension($event,segment,'length')"></label>
            <template v-else><label>长（米）<input :value="segment.length" type="number" min="0.000001" max="1000000" step="any" @input="setDimension($event,segment,'length')"></label><label>宽（米）<input :value="segment.width" type="number" min="0.000001" max="1000000" step="any" @input="setDimension($event,segment,'width')"></label><label v-if="line.mode==='立方'">高（米）<input :value="segment.height" type="number" min="0.000001" max="1000000" step="any" @input="setDimension($event,segment,'height')"></label></template>
            <button v-if="line.segments.length>1" type="button" class="link" :aria-label="'删除第'+(segmentIndex+1)+'段'" @click="line.segments.splice(segmentIndex,1)">删除</button>
          </div>
          <button v-if="!isCountCraftMode(line.mode)" type="button" class="link" data-action="add-measure-segment" :disabled="line.mode==='米'&&line.segments.length>=100" @click="addSegment">＋ 添加一段</button>
        </div>
      </details>
    </div>
  </fieldset>
  <div v-else-if="part==='quantity'" class="summary-cell"><small>{{isCountCraftMode(line.mode)?'数量小计':'方量小计'}}</small><strong>{{filled(line)?quantityLabel(line):'未填写'}}</strong></div>
  <div v-else-if="part==='amount'" class="summary-cell amount-cell"><small>金额小计</small><strong>{{pricesVisible?money(lineAmount(line))+' 元':CUSTOMER_PRICE_HIDDEN}}</strong><details v-if="canWages" class="line-wage"><summary>人工工资{{line.wage_configured===false&&line.wage_override_cents==null?' · 工价待配置':actualWage===undefined?'':` ${money(actualWage)} 元`}}</summary><p>工资标准：{{line.wage_configured===false?'待配置':line.wage_cents===undefined?'未返回':money(line.wage_cents)+' 元/'+measureUnit(line.mode)}}</p><p v-if="line.wage_configured===false&&line.wage_override_cents==null" class="pending-wage">公共工价未配置。填写本项实际工资并说明原因后，才可纳入工资结算。</p><label>本项实际工资（元）<input :value="wageInput" type="number" min="0" max="1000000" step="0.01" :disabled="!editable" placeholder="留空按标准计算" @input="setWage"></label><label>工资调整原因<input v-model="line.wage_adjustment_reason" :disabled="!editable" maxlength="500" placeholder="调整工资时必填"></label></details></div>
  <label v-else class="note-input">备注<input v-model="line.description" :disabled="!editable" maxlength="1000" placeholder="选填"></label>
</template>
<style scoped>
input,select{width:100%;min-width:0}label{font-size:11px}.line-craft-name{display:block;margin-bottom:0;font-size:12px;line-height:1.3;overflow-wrap:anywhere}.line-craft-name .craft-mode{display:inline;margin-left:5px;font-size:11px;white-space:nowrap}.field-label{display:block;margin-bottom:0;color:#65768c;font-size:10px}.configured-subitem{display:grid;gap:3px}.configured-subitem strong{font-size:13px;overflow-wrap:anywhere}.legacy-region{display:grid;gap:4px}.location-alias{display:grid;gap:3px}.location-alias button{justify-self:start;white-space:normal;text-align:left}.legacy-region small:last-child,.location-alias small{color:#687c96;font-size:10px;line-height:1.35}.legacy-region strong{font-size:12px;overflow-wrap:anywhere}.location-fields{display:flex;align-items:center;gap:4px;min-width:0}.actual-location,.actual-location label{display:grid;gap:4px;min-width:0}.actual-location input{padding:6px 7px}.location-actions{display:flex;align-items:center;gap:2px;flex:0 0 auto}.location-actions button{white-space:nowrap;padding:2px 4px}.included-note{display:block;margin-top:4px;color:#536982;font-size:10px;line-height:1.35}.choose-worker{justify-self:start;text-align:left;white-space:nowrap}.selected-worker-name{display:block;min-width:0;font-size:12px;line-height:1.25;overflow-wrap:anywhere;color:#314361}.worker-row-control{display:flex;align-items:center;gap:4px;min-width:0}.worker-row-control .choose-worker{padding:2px 3px;font-size:11px}.default-worker-override{padding:2px 3px;font-size:11px}.worker-hint-error{display:block;color:#b42318;font-size:10px;line-height:1.2}.error{color:#b42318}.readonly-label{display:inline-block;margin-top:4px;color:#795416;background:#fff4d6;border-radius:4px;padding:2px 5px;font-size:10px;line-height:1.35}.price-hidden{color:#65768c;font-size:12px;overflow-wrap:anywhere}.price-cell{position:relative;display:flex;align-items:center;gap:5px;min-width:0}.fixed-price{display:flex;align-items:baseline;gap:4px;min-width:0;white-space:nowrap}.fixed-price strong,.summary-cell strong{font-size:13px;font-variant-numeric:tabular-nums}.fixed-price small,.summary-cell small{color:#65768c;font-size:10px}.unit-price-input{display:grid;gap:4px}.charge-mode{position:relative;flex:0 0 auto}.charge-mode summary{display:block;cursor:pointer;list-style:none;border:1px solid #c8d4e1;border-radius:4px;padding:4px 6px;color:#365a82;background:#fff;font-size:11px;white-space:nowrap}.charge-mode summary::-webkit-details-marker{display:none}.charge-mode label{position:absolute;z-index:30;top:calc(100% + 5px);right:0;display:grid;gap:4px;width:150px;padding:9px;background:#fff;border:1px solid #bccada;border-radius:5px;box-shadow:0 8px 22px #10233b2b}.charge-mode select{font-size:11px;padding:4px}.measure-input{display:block;border:0;margin:0;padding:0;min-width:0}.measure-inline{display:flex;align-items:center;gap:5px;min-height:30px;min-width:0}.measure-inline>[data-field]{flex:1 1 auto;width:auto;min-width:54px;padding:5px 7px;font-size:13px}.measure-inline>[data-field="measure-count"]{max-width:95px}.measure-surface-indicator{flex:0 0 auto;color:#536982;font-size:11px;white-space:nowrap}.measure-advanced{position:relative;flex:0 0 auto}.measure-advanced summary{display:block;cursor:pointer;list-style:none;border:1px solid #c8d4e1;border-radius:4px;padding:4px 6px;color:#365a82;background:#fff;font-size:11px;white-space:nowrap}.measure-advanced summary::-webkit-details-marker{display:none}.measure-editor{position:absolute;z-index:30;top:calc(100% + 5px);right:0;display:grid;gap:7px;min-width:360px;max-width:min(620px,80vw);padding:10px;background:#fff;border:1px solid #bccada;border-radius:5px;box-shadow:0 8px 22px #10233b2b}.measure-editor-segment{display:flex;align-items:end;gap:7px;min-width:0}.measure-editor-segment label{display:grid;gap:3px;flex:1 1 90px;min-width:68px}.measure-editor-segment select{width:90px;min-width:75px;padding:5px 6px}.measure-editor-segment input{padding:5px 6px}.segment-index{flex:0 0 20px;color:#65768c;font-size:11px;text-align:center}.measure-first-add{white-space:nowrap}.check{display:flex;align-items:center;gap:5px}.check input{width:auto}.link{padding:3px 4px;font-size:11px}.summary-cell{display:grid;align-content:center;gap:4px;min-height:42px}.amount-cell strong{color:#1c3552}.note-input{display:grid;gap:4px}.note-input input{padding:6px 7px}
.line-wage{min-width:0;font-size:11px}.line-wage summary{cursor:pointer;color:#3a618f;line-height:1.4}.line-wage p{margin:5px 0}.line-wage label{display:grid;gap:4px;margin-top:5px}.line-wage .pending-wage{color:#915300}
</style>
