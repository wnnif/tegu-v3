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
const emit=defineEmits<{temporary:[];lineWorker:[line:OrderLine,item:CatalogRow|null];categoryWorker:[category:string,item:CatalogRow|null];addLocation:[line:OrderLine];removeLocation:[line:OrderLine];materialProduct:[item:CatalogRow|null];materialSource:[]}>()
props.draft.category_workers ||= {}
const showReadonly=ref(true)
watch(()=>props.draft,(draft)=>{draft.category_workers ||= {};showReadonly.value=true},{flush:'sync'})
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
function changeMode(line:OrderLine){if(!editable(line))return;line.segments=[freshSegment(line.mode)];line.entered=false}
function clearLine(line:OrderLine){if(!editable(line))return;line.segments=[freshSegment(line.mode)];line.entered=false}
function removeTemporary(line:OrderLine){const index=props.draft.lines.indexOf(line);if(!line.temporary||!removable(line)||hasMaterials(line)||index<0)return;props.draft.lines.splice(index,1)}
const includedLabel=(line:OrderLine)=>line.charge_note||`费用已含在${props.references.crafts.find(c=>c.id===line.included_by_craft_id)?.name||'组合主项'}中`
</script>
<template>
  <section class="craft-input">
    <div class="toolbar craft-toolbar"><h3>本次施工填写</h3><button v-if="readonlyCount" type="button" :aria-pressed="showReadonly" title="包括无工艺填写权限或人员技能不匹配的项目；隐藏只改变显示，不修改草稿、金额或保存内容" @click="showReadonly=!showReadonly">{{showReadonly?'隐藏':'显示'}}只读工艺项（{{readonlyCount}}）</button><button v-if="can('orders_temporary')" type="button" :disabled="locked||!allowedCrafts.length||!can('customer_prices')" @click="can('customer_prices')&&emit('temporary')">＋ 临时添加项</button><small v-if="readonlyCount">无填写权或技能不匹配项可隐藏，仅改变显示。</small></div>
    <p v-if="!can('customer_prices')" class="hint">客户收费单价、小计及合计无查看权限；已授权固定工艺仍可填写。新增临时收费项需要“查看客户价格”权限。</p>
    <OrderNineColumns label="本次施工填写九列">
        <section v-for="category in categoryGroups" :key="category.name" class="category-group">
          <header class="category-header"><div class="category-title"><strong>{{category.name}}</strong></div></header>
          <article v-for="group in category.groups" :key="group.key" class="craft-group">
            <div class="craft-heading"><OrderCraftIdentity :line="group.identity" :editable="editable(group.identity)" :removable="removable(group.identity)&&!hasMaterials(group.identity)" :can-wages="can('wages')" :allowed-crafts="allowedCrafts" @mode="changeMode(group.identity)" @remove="removeTemporary(group.identity)" /></div>
            <div class="craft-lines">
              <section v-for="line in group.visibleLines" :key="orderLineKey(line)" :data-line-key="orderLineKey(line)" :data-craft-id="line.craft_id" :data-subitem-id="line.subitem_id" class="order-line-row" :class="{readonly:readonlyLine(line), 'needs-worker':accountEditable(line)&&!lineWorkerId(line,defaultId(line.category))}">
                <div class="order-cell personnel-cell" data-label="施工人员"><div v-if="line===category.defaultLine" class="category-default-choice"><small aria-hidden="true">大类默认</small><ReferenceSelect class="category-default-picker" :model-value="draft.category_workers?.[category.name]||''" entity="workers" :label="category.name+'默认施工人'" empty-label="请选择本大类默认施工人" search-placeholder="搜索施工人" :selected-name="defaultName(category.name)" :craft="category.name" @change="categoryWorkerChanged(category.name,$event)" /></div><OrderLineFields :category-default-row="line===category.defaultLine" :can-customer-prices="can('customer_prices')" part="worker" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" :default-name="defaultName(line.category)" :default-worker-id="defaultId(line.category)" :selected-worker-name="selectedWorkerName(line)" :worker-hint="workerHint(line)" :readonly-label="readonlyLabel(line)" :hint-error="accountEditable(line)&&!!lineWorkerId(line,defaultId(line.category))&&!editable(line)" @worker="emit('lineWorker',line,$event)" /></div>
                <div class="order-cell" data-label="工艺"><OrderLineFields :can-customer-prices="can('customer_prices')" part="craft" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" :included-label="includedLabel(line)" /></div>
                <div class="order-cell" data-label="部位"><OrderLineFields :can-customer-prices="can('customer_prices')" part="location" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" :can-add-location="addable(line)" :show-remove-location="canRemoveOrderLocation(draft.lines,line)" :can-remove-location="removable(line)&&!hasMaterials(line)" @add-location="requestAddLocation(line)" @remove-location="requestRemoveLocation(line)" /><small v-if="hasMaterials(line)" class="material-remove-hint">先移除本项耗材后再移除此明细。</small></div>
                <div class="order-cell" data-label="单价"><OrderLineFields :can-customer-prices="can('customer_prices')" part="price" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" /></div>
                <div class="order-cell measure-cell" data-label="方量明细"><OrderLineFields :can-customer-prices="can('customer_prices')" part="measure" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" /></div>
                <div class="order-cell" data-label="方量小计"><OrderLineFields :can-customer-prices="can('customer_prices')" part="quantity" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" /></div>
                <div class="order-cell" data-label="金额小计"><OrderLineFields :can-customer-prices="can('customer_prices')" :can-wages="can('wages')" part="amount" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" /></div>
                <div class="order-cell note-cell" data-label="备注"><OrderLineFields :can-customer-prices="can('customer_prices')" part="note" :line="line" :account-editable="accountEditable(line)" :editable="editable(line)" /></div>
                <details class="line-materials" :data-consumables-for="combinationLineRef(line)"><summary>本项耗材{{hasMaterials(line)?'（已登记）':''}}</summary><OrderMaterialsEditor :materials="draft.materials" :line-id="combinationLineRef(line)" :references="references" :inventory="inventory||[]" :order-warehouse="orderWarehouse||''" :can-warehouse="can('warehouse')" :disabled="!editable(line)||!!materialsLocked" @product="emit('materialProduct',$event)" @source="emit('materialSource')" /></details>
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
.craft-input{display:grid;gap:6px;min-width:0}.craft-toolbar{gap:6px;margin:0;min-width:0;flex-wrap:wrap}.craft-toolbar h3{font-size:14px;margin:0 auto 0 0}.craft-toolbar button{font-size:12px;padding:5px 8px;white-space:normal}.craft-toolbar small{color:#61758f;font-size:11px}.personnel-cell{display:grid;align-content:start;gap:5px}.category-default-choice{display:grid;gap:3px;min-width:0}.category-default-choice>small{font-size:10px;color:#61758f}.personnel-cell :deep(.line-worker-picker>label),.personnel-cell :deep(.category-default-picker>label){position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);clip-path:inset(50%);white-space:nowrap;border:0}.personnel-cell :deep(.reference-picker){width:100%;min-width:0;gap:3px}.personnel-cell :deep(.reference-picker>label){font-size:11px}.personnel-cell :deep(.reference-control select){padding:5px 4px}.personnel-cell :deep(.reference-toggle){padding:5px 4px;font-size:11px}.empty{padding:8px;color:#77879b;font-size:12px;margin:0}.craft-group :deep(input),.craft-group :deep(select){max-width:100%}
.line-materials{grid-column:1/-1;padding:4px 8px;border-top:1px dashed #dce4ed;min-width:0}.line-materials summary{cursor:pointer;font-size:12px;color:#3a618f;padding:3px 0}.line-materials[open]{padding-bottom:8px}.material-remove-hint{display:block;color:#795416;font-size:10px;margin-top:4px}
@media(max-width:800px){.order-cell :deep(input),.order-cell :deep(select){width:100%;min-width:0}.craft-group :deep(.measure-segment label){max-width:none}.craft-toolbar small{flex-basis:100%}}
</style>
