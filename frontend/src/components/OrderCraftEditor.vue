<script setup lang="ts">
import {computed,ref,watch} from 'vue'
import {lineAccountCanEdit,lineCanEdit} from '../orderAccess'
import {lineWorkerId} from '../orderWorkers'
import {canAddOrderLocation,canRemoveOrderLocation,orderLineKey} from '../orderLocations'
import {groupOrderRowsBySubitem} from '../orderRowGroups'
import {orderedCompanyCategories} from '../companyCraftOrder'
import {combinationLineRef} from '../orderCombinations'
import OrderCraftIdentity from './OrderCraftIdentity.vue'
import OrderMaterialsEditor from './OrderMaterialsEditor.vue'
import OrderNineColumns from './OrderNineColumns.vue'
import OrderLineFields from './OrderLineFields.vue'
import ReferenceSelect from './ReferenceSelect.vue'
import type {Balance,CatalogRow,OrderDraft,OrderLine,OrderSegment,References} from '../workflows/models'

const props=defineProps<{draft:OrderDraft;references:References;locked:boolean;can:(permission:string)=>boolean;allowedCrafts:string[];inventory?:Balance[];orderWarehouse?:string;materialsLocked?:boolean}>()
const emit=defineEmits<{temporary:[];lineWorker:[line:OrderLine,item:CatalogRow|null];categoryWorker:[category:string,item:CatalogRow|null];addLocation:[line:OrderLine];removeLocation:[line:OrderLine];materialProduct:[item:CatalogRow|null];materialSource:[];measurementIncomplete:[incomplete:boolean]}>()
props.draft.category_workers ||= {}
const showReadonly=ref(true)
const incompleteMeasurements=ref(new Set<string>())
watch(()=>props.draft,(draft)=>{draft.category_workers ||= {};showReadonly.value=true},{flush:'sync'})
function measurementIncomplete(line:OrderLine,incomplete:boolean){const key=orderLineKey(line);if(incomplete)incompleteMeasurements.value.add(key);else incompleteMeasurements.value.delete(key);emit('measurementIncomplete',incompleteMeasurements.value.size>0)}
const defaultId=(category:string)=>props.draft.category_workers?.[category]||''
const worker=(line:OrderLine)=>props.references.workers.find(item=>item.id===lineWorkerId(line,defaultId(line.category)))
const accountEditable=(line:OrderLine)=>!props.locked&&lineAccountCanEdit(line,props.draft.company_id,props.references.crafts,props.can('orders_temporary'))
const editable=(line:OrderLine)=>!props.locked&&lineCanEdit(line,props.draft.company_id,props.references.crafts,worker(line),props.can('orders_temporary'))
const defaultName=(category:string)=>props.references.workers.find(item=>item.id===defaultId(category))?.name||''
const selectedWorkerName=(line:OrderLine)=>{
 const id=lineWorkerId(line,defaultId(line.category))
 if(!id)return '施工人未指定'
 const name=line.worker_name||(line.id?'施工人姓名未记录':worker(line)?.name||'已选施工人')
 return (line.worker_id?'':'默认：')+name
}
const workerHint=(line:OrderLine)=>props.locked?'本次记录只读':line.ticket_claimed?'本行已交票，先作废所属批次再更正':!accountEditable(line)?'当前账号仅可查看':!lineWorkerId(line,defaultId(line.category))?'先选本大类施工人，再填写本项':!editable(line)?'所选人员已停用或没有此类工艺技能；内容只读，可更换人员':''
const skillMismatch=(line:OrderLine)=>{
 const id=lineWorkerId(line,defaultId(line.category))
 if(!id)return false
 const person=props.references.workers.find(item=>item.id===id)
 return !person||(person.active!==undefined&&!Number(person.active))||!(person.crafts||[]).includes(line.category)
}
const readonlyLine=(line:OrderLine)=>!accountEditable(line)||skillMismatch(line)
const removable=(line:OrderLine)=>!props.locked&&accountEditable(line)&&!skillMismatch(line)
const hasMaterials=(line:OrderLine)=>(props.draft.materials||[]).some(material=>material.line_id===combinationLineRef(line))
const addable=(line:OrderLine)=>canAddOrderLocation(line)&&removable(line)&&(!line.temporary||props.can('customer_prices'))&&lineAccountCanEdit({...line,id:''},props.draft.company_id,props.references.crafts,props.can('orders_temporary'))
function requestAddLocation(line:OrderLine){if(props.draft.lines.includes(line)&&addable(line))emit('addLocation',line)}
function requestRemoveLocation(line:OrderLine){if(removable(line)&&!hasMaterials(line)&&canRemoveOrderLocation(props.draft.lines,line))emit('removeLocation',line)}
const readonlyLabel=(line:OrderLine)=>props.locked?'本次记录只读':line.ticket_claimed?'本行已交票，先作废所属批次再更正':!accountEditable(line)?'无此工艺填写权限，只读':skillMismatch(line)?'施工人技能不匹配；内容只读，可更换人员':''
const groups=computed(()=>{
 const result:{key:string;category:string;lines:OrderLine[]}[]=[]
 props.draft.lines.forEach(line=>{
  const key=line.temporary?orderLineKey(line):line.craft_id
  let group=result.find(item=>item.key===key)
  if(!group){group={key,category:line.category,lines:[]};result.push(group)}
  group.lines.push(line)
 })
 return result
})
const readonlyCount=computed(()=>props.draft.lines.filter(readonlyLine).length)
const categoryGroups=computed(()=>{
 const result:{name:string;groups:{key:string;lines:OrderLine[];visibleLines:OrderLine[];identity:OrderLine}[]}[]=[]
 const companyCrafts=props.references.crafts.filter(craft=>craft.company_id===props.draft.company_id)
 const byCraftId=new Map(companyCrafts.map(craft=>[craft.id,craft]))
 const craftRank=new Map(companyCrafts.map((craft,index)=>[craft.id,index]))
 for(const group of groups.value){
  // The reference craft's subitems come from its public source. Use IDs only for projection;
  // saved names, removed subitems and legacy duplicate position rows remain untouched.
  const configured=group.lines[0]?.temporary?undefined:byCraftId.get(group.lines[0]?.craft_id||'')?.subitems
  const visibleLines=groupOrderRowsBySubitem(group.lines,configured).filter(line=>showReadonly.value||!readonlyLine(line))
  if(!visibleLines.length)continue
  let category=result.find(item=>item.name===group.category)
  if(!category){category={name:group.category,groups:[]};result.push(category)}
  category.groups.push({...group,visibleLines,identity:group.lines.find(line=>!readonlyLine(line))||group.lines[0]!})
 }
 const companyOrder=props.references.companies?.find(company=>company.id===props.draft.company_id)?.craft_category_order||[]
 return orderedCompanyCategories(result.map(category=>category.name),companyOrder).map(name=>{
  const category=result.find(item=>item.name===name)!
  // Sort only the view. Stable ties append missing crafts/temporary rows in draft order.
  const orderedGroups=[...category.groups].sort((left,right)=>
   (left.identity.temporary?Number.MAX_SAFE_INTEGER:craftRank.get(left.identity.craft_id)??Number.MAX_SAFE_INTEGER)-
   (right.identity.temporary?Number.MAX_SAFE_INTEGER:craftRank.get(right.identity.craft_id)??Number.MAX_SAFE_INTEGER))
  return {...category,groups:orderedGroups,defaultLine:orderedGroups.flatMap(group=>group.visibleLines).find(accountEditable)}
 })
})
function categoryCanChooseWorker(category:string){return props.draft.lines.some(line=>line.category===category&&accountEditable(line))}
function setCategoryDefaultValue(category:string,value:string){props.draft.category_workers ||= {};props.draft.category_workers[category]=value}
function categoryWorkerChanged(category:string,item:CatalogRow|null){
 if(!props.draft.company_id||!categoryCanChooseWorker(category))return
 const previous=defaultId(category),next=item?.id||''
 setCategoryDefaultValue(category,next)
 for(const line of props.draft.lines){
  // Nonempty row IDs are independent choices; never reset them, including the first row.
  if(line.category!==category||line.worker_override||line.worker_id||!accountEditable(line))continue
  if(previous!==next)line.worker_name=''
 }
 emit('categoryWorker',category,item)
}
const freshSegment=(mode:string):OrderSegment=>({mode,surface:'地面',length:'',width:'',height:'',count:'',amount_cents:0})
function changeMode(line:OrderLine){if(!editable(line))return;measurementIncomplete(line,false);line.segments=[freshSegment(line.mode)];line.entered=false}
function clearLine(line:OrderLine){if(!editable(line))return;line.segments=[freshSegment(line.mode)];line.entered=false}
function removeTemporary(line:OrderLine){const index=props.draft.lines.indexOf(line);if(!line.temporary||!removable(line)||hasMaterials(line)||index<0)return;props.draft.lines.splice(index,1)}
const includedLabel=(line:OrderLine)=>line.charge_note||`费用已含在${props.references.crafts.find(c=>c.id===line.included_by_craft_id)?.name||'组合主项'}中`
</script>
<template>
  <section class="craft-input">
    <div class="toolbar craft-toolbar"><h3>本次施工填写</h3><button v-if="readonlyCount" type="button" :aria-pressed="showReadonly" title="包括无工艺填写权限或人员技能不匹配的项目；隐藏只改变显示，不修改草稿、金额或保存内容" @click="showReadonly=!showReadonly">{{showReadonly?'隐藏':'显示'}}只读工艺项（{{readonlyCount}}）</button><button v-if="can('orders_temporary')" type="button" :disabled="locked||!allowedCrafts.length||!can('customer_prices')" @click="can('customer_prices')&&emit('temporary')">＋ 临时添加项</button><small v-if="readonlyCount">无填写权或技能不匹配项可隐藏，仅改变显示。</small></div>
    <OrderNineColumns class="order-entry-grid" label="本次施工填写九列">
        <section v-for="category in categoryGroups" :key="category.name" class="category-group">
          <article v-for="group in category.groups" :key="group.key" class="craft-group">
            <div class="craft-lines">
              <section v-for="line in group.visibleLines" :key="orderLineKey(line)" :data-line-key="orderLineKey(line)" :data-craft-id="line.craft_id" :data-subitem-id="line.subitem_id" class="order-line-row" :class="{readonly:readonlyLine(line), 'needs-worker':accountEditable(line)&&!lineWorkerId(line,defaultId(line.category))}">
                <div class="order-cell personnel-cell" data-label="施工人员"><ReferenceSelect v-if="line===category.defaultLine" class="category-default-picker" :model-value="draft.category_workers?.[category.name]||''" entity="workers" :label="category.name+'默认施工人'" empty-label="选择本大类默认施工人" search-placeholder="搜索施工人" :selected-name="defaultName(category.name)" :craft="category.name" @change="categoryWorkerChanged(category.name,$event)" /><OrderLineFields :category-default-row="line===category.defaultLine" :can-customer-prices="can('customer_prices')" part="worker" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" :default-name="defaultName(line.category)" :default-worker-id="defaultId(line.category)" :selected-worker-name="selectedWorkerName(line)" :worker-hint="workerHint(line)" :readonly-label="readonlyLabel(line)" :hint-error="accountEditable(line)&&!!lineWorkerId(line,defaultId(line.category))&&!editable(line)" @worker="emit('lineWorker',line,$event)" /></div>
                <div class="order-cell craft-cell" data-label="工艺"><OrderLineFields :can-customer-prices="can('customer_prices')" part="craft" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" :included-label="includedLabel(line)" /><details v-if="line.temporary" class="temp-line-settings"><summary>项目设置</summary><OrderCraftIdentity :line="line" :editable="editable(line)" :removable="removable(line)&&!hasMaterials(line)" :can-wages="can('wages')" :allowed-crafts="allowedCrafts" @mode="changeMode(line)" @remove="removeTemporary(line)" /></details></div>
                <div class="order-cell" data-label="部位"><OrderLineFields :can-customer-prices="can('customer_prices')" part="location" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" :can-add-location="addable(line)" :show-remove-location="canRemoveOrderLocation(draft.lines,line)" :can-remove-location="removable(line)&&!hasMaterials(line)" @add-location="requestAddLocation(line)" @remove-location="requestRemoveLocation(line)" /><small v-if="hasMaterials(line)" class="material-remove-hint">先移除本项耗材后再移除此明细。</small></div>
                <div class="order-cell" data-label="单价"><OrderLineFields :can-customer-prices="can('customer_prices')" part="price" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" /></div>
                <div class="order-cell measure-cell" data-label="方量明细"><OrderLineFields :can-customer-prices="can('customer_prices')" part="measure" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" @measurement-incomplete="measurementIncomplete(line,$event)" /><small v-if="incompleteMeasurements.has(orderLineKey(line))" class="measurement-incomplete">尺寸未填完整</small></div>
                <div class="order-cell" data-label="方量小计"><OrderLineFields :can-customer-prices="can('customer_prices')" part="quantity" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" /></div>
                <div class="order-cell line-options-cell" data-label="金额小计"><OrderLineFields :can-customer-prices="can('customer_prices')" :can-wages="can('wages')" part="amount" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" /><details class="line-materials" :data-consumables-for="combinationLineRef(line)"><summary>耗材{{hasMaterials(line)?'已登记':''}}</summary><OrderMaterialsEditor :materials="draft.materials" :line-id="combinationLineRef(line)" :references="references" :inventory="inventory||[]" :order-warehouse="orderWarehouse||''" :can-warehouse="can('warehouse')" :disabled="!editable(line)||!!materialsLocked" @product="emit('materialProduct',$event)" @source="emit('materialSource')" /></details></div>
                <div class="order-cell note-cell" data-label="备注"><OrderLineFields :can-customer-prices="can('customer_prices')" part="note" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" /></div>
              </section>
            </div>
          </article>
        </section>
    </OrderNineColumns>
    <p v-if="!categoryGroups.length&&readonlyCount&&!showReadonly" class="empty">只读工艺项已隐藏。恢复显示不会更改草稿或金额。</p>
    <p v-else-if="!categoryGroups.length" class="empty">选择公司后显示其固定工艺；公司尚未配置时，请先在公司资料中设置。</p>
  </section>
</template>
<style scoped>
.craft-input{display:grid;gap:4px;min-width:0}.craft-toolbar{gap:6px;margin:0;min-width:0;flex-wrap:wrap;padding:0}.craft-toolbar h3{font-size:14px;margin:0 auto 0 0}.craft-toolbar button{font-size:12px;padding:4px 7px;white-space:normal}.craft-toolbar small{color:#61758f;font-size:11px}.personnel-cell{display:flex;align-items:center;gap:4px;min-width:0}.personnel-cell :deep(.line-worker-picker>label),.personnel-cell :deep(.category-default-picker>label){position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);clip-path:inset(50%);white-space:nowrap;border:0}.personnel-cell :deep(.reference-picker){width:100%;min-width:0;gap:0}.personnel-cell :deep(.reference-picker>label){font-size:11px}.personnel-cell :deep(.reference-control select){height:32px;padding:4px 22px 4px 6px;font-size:12px}.personnel-cell :deep(.reference-toggle){padding:4px 5px;font-size:10px}.craft-cell{display:flex;align-items:center;gap:5px}.temp-line-settings{flex:0 0 auto}.temp-line-settings>summary,.line-materials>summary{list-style:none;cursor:pointer;padding:2px 4px;color:#426a9e;font-size:11px;white-space:nowrap}.temp-line-settings>summary::-webkit-details-marker,.line-materials>summary::-webkit-details-marker{display:none}.temp-line-settings[open],.line-materials[open]{position:relative;z-index:2}.temp-line-settings[open] :deep(.temporary-identity){position:absolute;left:0;top:100%;z-index:4;width:min(600px,75vw);padding:10px;background:#fff;border:1px solid #d9e2ef;box-shadow:0 6px 22px #172e5e22;border-radius:5px}.line-materials{min-width:0}.line-materials[open] :deep(.material-fields){position:absolute;right:0;top:100%;z-index:4;width:min(660px,75vw);padding:10px;background:#fff;border:1px solid #d9e2ef;box-shadow:0 6px 22px #172e5e22;border-radius:5px}.line-options-cell{position:relative;display:flex;align-items:center;gap:5px}.line-options-cell :deep(.summary-cell){min-height:0;gap:1px}.line-options-cell :deep(.summary-cell small){display:none}.line-options-cell :deep(.line-wage){position:static}.line-options-cell :deep(.line-wage summary){white-space:nowrap}.line-options-cell :deep(.line-wage[open]){position:absolute;right:0;top:100%;z-index:4;width:min(360px,75vw);padding:10px;background:#fff;border:1px solid #d9e2ef;box-shadow:0 6px 22px #172e5e22;border-radius:5px}.line-options-cell :deep(.line-wage[open] summary){white-space:normal}.empty{padding:8px;color:#77879b;font-size:12px;margin:0}.craft-group :deep(input),.craft-group :deep(select){max-width:100%}.material-remove-hint{display:block;color:#795416;font-size:10px;margin-top:4px}
@media(max-width:800px){.order-cell :deep(input),.order-cell :deep(select){width:100%;min-width:0}.craft-group :deep(.measure-segment label){max-width:none}.craft-toolbar small{flex-basis:100%}}
</style>
<style scoped>
.line-options-cell{display:grid;grid-template-columns:minmax(0,1fr);align-content:center;gap:1px}
.line-options-cell :deep(.summary-cell){min-width:0;min-height:0;gap:1px}
.personnel-cell :deep(.category-default-picker .reference-toggle){padding:4px 1px}
.line-options-cell :deep(.line-wage){min-width:0}
.line-options-cell :deep(.line-wage summary){padding:0;line-height:1.2;white-space:nowrap}
.line-options-cell :deep(.line-materials>summary){padding:0;line-height:1.2}
.note-cell :deep(.note-input){gap:0}
.note-cell :deep(.note-input>small){display:none}
.measurement-incomplete{display:block;color:#b42318;font-size:10px;line-height:1.2}
</style>
