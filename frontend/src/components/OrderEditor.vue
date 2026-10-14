<script setup lang="ts">
import {computed,ref,toRef,watch} from 'vue'
import { customerMoney, hasCustomerPrice } from '../customerPrices'
import {filled,lineAmount,saleAmountCents} from '../orderSegments'
import ProjectSelector from './ProjectSelector.vue'
import ProjectWorkPanel from './ProjectWorkPanel.vue'
import OrderCraftEditor from './OrderCraftEditor.vue'
import OrderMaterialsEditor from './OrderMaterialsEditor.vue'
import SavedOrderWork from './SavedOrderWork.vue'
import OrderCustomerTotal from './OrderCustomerTotal.vue'
import OrderCombinationEditor from './OrderCombinationEditor.vue'
import OrderSalesEditor from './OrderSalesEditor.vue'
import OrderSettlementDetails from './OrderSettlementDetails.vue'
import ReferenceSelect from './ReferenceSelect.vue'
import type {ProjectRecord} from '../projects'
import type {ProjectWork} from '../projectWork'
import {ticketLabel} from '../projectWork'
import {needsMaterialWorker} from '../orderWorkers'
import type {CompanyPackageOption,ProjectPackage} from '../projects'
import type {OrderDraft,OrderLine,References,CatalogRow,Balance,Entity} from '../workflows/models'
const props=withDefaults(defineProps<{draft:OrderDraft;references:References;projects:ProjectRecord[];packageOptions?:CompanyPackageOption[];projectPage:number;projectTotal:number;workerOptions:CatalogRow[];companyCraftCategories:string[];selectedCategory:string;inventory:Balance[];busy:boolean;orderLocked:boolean;orderWarehouse:string;allowedCrafts:string[];can:(p:string)=>boolean}>(),{packageOptions:()=>[]})
const emit=defineEmits<{save:[];company:[];project:[];projectSearch:[query:string];projectPage:[page:number];category:[category:string];worker:[item:CatalogRow|null];lineWorker:[line:OrderLine,item:CatalogRow|null];categoryWorker:[category:string,item:CatalogRow|null];addLocation:[line:OrderLine];removeLocation:[line:OrderLine];materialProduct:[item:CatalogRow|null];materialSource:[];print:[];cancelOrder:[];temporary:[];material:[];sale:[];close:[]}>()
const combinationEditor=ref<InstanceType<typeof OrderCombinationEditor>|null>(null)
const showSaved=ref(false)
watch(()=>props.draft,()=>{showSaved.value=false})
const editing=toRef(props,'draft'),projectWork=ref<ProjectWork|null>(null),workPanel=ref<InstanceType<typeof ProjectWorkPanel>|null>(null)
const savedRecord=computed(()=>editing.value.saved_snapshot||editing.value)
const companyCategoryOrder=computed(()=>props.references.companies.find(company=>company.id===editing.value.company_id)?.craft_category_order||[])
const companyCrafts=computed(()=>props.references.crafts.filter(craft=>craft.company_id===editing.value.company_id))
const money=(value:unknown)=>customerMoney(value,props.can('customer_prices'))
const financeMoney=(value:unknown)=>customerMoney(value,props.can('finance')&&props.can('customer_prices'))
const activePackage=computed<ProjectPackage|undefined>(()=>editing.value.project_id?currentProjectWork.value?.project.package:props.packageOptions.find(item=>item.id===editing.value.package_id))
const packageCoverage=computed(()=>activePackage.value?.coverage||[])
const coveredLine=(line:OrderLine)=>packageCoverage.value.some(item=>item.mapping==='mapped'&&!!item.craft_id&&item.craft_id===line.craft_id&&(item.subitem_id||'')===(line.subitem_id||''))
const includedPackageItems=computed(()=>packageCoverage.value.filter(item=>item.mapping==='mapped').map(item=>item.item_name))
const pendingPackageItems=computed(()=>packageCoverage.value.filter(item=>item.mapping!=='mapped').map(item=>item.item_name))
const label=(type:Entity,id:string)=>props.references[type]?.find((x:CatalogRow)=>x.id===id)?.name||'—'
const companyTotal=computed(()=>{
 const lines=editing.value.lines.filter(filled),sales=editing.value.sales||[]
 const chargedLines=lines.filter(line=>!coveredLine(line))
 if(!props.can('customer_prices')||chargedLines.some(line=>!hasCustomerPrice(line.price_cents))||sales.some(sale=>!hasCustomerPrice(sale.price_cents)))return undefined
 let total=chargedLines.reduce((sum,line)=>sum+lineAmount(line),0)+sales.reduce((sum,sale)=>sum+saleAmountCents(sale.input_qty||Number(sale.sale_qty_micro||sale.qty_micro)/1e6,sale.price_cents!),0)
 if(!editing.value.project_id&&editing.value.package_id){if(!activePackage.value||!hasCustomerPrice(activePackage.value.price_cents))return undefined;total+=Number(activePackage.value.price_cents)}
 return total
})
const currentProjectWork=computed(()=>projectWork.value?.project.id===editing.value.project_id?projectWork.value:null)
const split=computed(()=>editing.value.project_id?currentProjectWork.value?.project.ticket_mode==='split':!editing.value.id&&props.references.companies.find(company=>company.id===editing.value.company_id)?.ticket_mode==='split')
const protectedRecord=computed(()=>editing.value.lines.some(line=>!!line.id&&line.editable===false&&!line.ticket_claimed))
const hasTicketClaims=computed(()=>editing.value.lines.some(line=>line.ticket_claimed)||(editing.value.sales||[]).some(sale=>sale.editable===false))
const commonLocked=computed(()=>protectedRecord.value||hasTicketClaims.value)
const wageIssued=computed(()=>!!Number(editing.value.wage_paid)||!!editing.value.wage_records?.some(record=>!!Number(record.wage_paid)))
const correctionTickets=computed(()=>(currentProjectWork.value?.tickets||[]).filter(ticket=>ticket.status!=='草稿'))
function requestSave(){if(!props.busy&&!props.orderLocked&&combinationEditor.value?.validate()!==false)emit('save')}
function companyChanged(){projectWork.value=null;emit('company')}
function printSaved(){if(!props.can('customer_prices'))return;if(editing.value.project_id)workPanel.value?.printSummary();else emit('print')}
function correctionChanged(){editing.value.ticket_version=correctionTickets.value.find(ticket=>ticket.id===editing.value.ticket_id)?.version||0}
function projectLoaded(work:ProjectWork){if(work.project.id===editing.value.project_id)projectWork.value=work}
</script>
<template>
  <p v-if="editing.id&&orderLocked" class="notice">{{wageIssued?'已有工资发放，先在工资管理撤销对应发放后再更正本次记录。':'本次记录只读。'}}工地后续施工请通过“新增施工单”选择原工地。</p>
  <OrderSettlementDetails v-if="editing.id&&!editing.project_id&&can('finance')&&can('customer_prices')" :order="editing" />
  <div v-if="editing.id&&!orderLocked" class="toolbar saved-view-toggle"><button type="button" :disabled="busy" :aria-pressed="showSaved" @click="showSaved=!showSaved">{{showSaved?'返回编辑本次施工':'查看已保存施工单'}}</button><small>查看保存结果不会修改正在填写的内容。</small></div>
  <section v-if="editing.id&&(orderLocked||showSaved)" class="saved-order-inspection">
    <p class="hint">{{savedRecord.company_name||'所属公司'}} · {{savedRecord.address}} · {{savedRecord.work_date}} · {{savedRecord.number}}</p>
    <SavedOrderWork :order="savedRecord" :can-customer-prices="can('customer_prices')" :can-wages="can('wages')" :category-order="companyCategoryOrder" :crafts="companyCrafts" />
    <footer><button type="button" @click="printSaved" :disabled="busy||!can('customer_prices')">{{editing.project_id?'打印工地累计已保存内容':'打印已保存单据'}}</button><button v-if="!orderLocked" type="button" :disabled="busy" @click="showSaved=false">返回编辑</button><button type="button" :disabled="busy" @click="emit('close')">关闭</button></footer>
  </section>
  <form v-else @submit.prevent="requestSave" autocomplete="off" class="order-entry-form">
    <fieldset :disabled="orderLocked||busy" class="order-fields">
      <div class="form-grid compact">
        <ReferenceSelect v-model="editing.company_id" entity="companies" label="公司" empty-label="请选择公司" search-placeholder="搜索公司" :selected-name="editing.company_name" :disabled="orderLocked||busy||!!editing.id" required @change="companyChanged" />
      </div>
      <ProjectSelector :draft="editing" :projects="projects" :page="projectPage" :total="projectTotal" :disabled="orderLocked||busy||!!editing.id" @change="projectWork=null;emit('project')" @search="emit('projectSearch',$event)" @page="emit('projectPage',$event)" />
      <section v-if="!editing.id&&!editing.project_id&&packageOptions.length&&can('customer_prices')" class="package-selection">
        <label>工地房型套餐（可选）<select v-model="editing.package_id"><option value="">不选择套餐</option><option v-for="option in packageOptions" :key="option.id" :value="option.id">{{option.name}} · {{option.room_count}} 房 · {{money(option.price_cents)}}</option></select></label>
      </section>
      <section v-if="activePackage&&can('customer_prices')" class="package-summary">
        <strong>房型套餐：{{activePackage.name}} · {{activePackage.room_count}} 房</strong>
        <span>{{editing.project_id?'套餐已绑定此工地，金额只计入首张票':'套餐金额 '+money(activePackage.price_cents)+'，保存后只计入首张票'}}</span>
        <span v-if="includedPackageItems.length">套餐内免重复收费：{{includedPackageItems.join('、')}}；施工工资照常计算。</span>
        <span v-if="pendingPackageItems.length">以下项目尚未唯一对应系统工艺，不自动免收：{{pendingPackageItems.join('、')}}。</span>
      </section>
      <div class="form-grid compact order-basics">
        <label>施工地址<input v-model="editing.address" maxlength="300" required :readonly="!!editing.project_id||commonLocked" :placeholder="editing.project_id?'已选原施工地址':'首次施工直接填写完整地址'"></label>
        <label>本次施工 / 送货日期<input type="date" v-model="editing.work_date" required :disabled="!!editing.id"></label>
        <ReferenceSelect v-model="editing.worker_id" entity="workers" label="材料 / 送货责任人" :empty-label="editing.company_id?'仅耗材、售料或带货工资需要选择':'请先选择公司'" search-placeholder="搜索材料 / 送货责任人" :selected-name="editing.worker_name" :disabled="orderLocked||busy||!editing.company_id||!!editing.id" :required="needsMaterialWorker(editing)" @change="emit('worker',$event)" />
      </div>
      <details class="extra-fields"><summary>项目经理 / 联系电话 / 备注</summary><div class="form-grid compact"><label>项目经理<input v-model="editing.contact" :readonly="commonLocked"></label><label>联系电话<input v-model="editing.phone" maxlength="60" :readonly="commonLocked"></label><label>备注<input v-model="editing.note" maxlength="5000" :readonly="commonLocked"></label></div></details>
      <p v-if="editing.worker_id&&!orderWarehouse&&!can('warehouse')" class="error">此材料 / 送货责任人未关联材料结余位置，不能填写耗材或销售材料。</p><p v-else-if="orderWarehouse" class="hint">默认材料位置：{{label('warehouses',orderWarehouse)}}。有仓库权限可为每项选择实际来源。</p>
      <p v-if="editing.company_id" class="notice">首条明细选人，同类部位自动继承；不同人员在本行指定。材料/送货责任人只用于仓库和带货工资。</p>
      <OrderCraftEditor :draft="editing" :references="references" :locked="orderLocked||busy" :can="can" :allowed-crafts="allowedCrafts" :inventory="inventory" :order-warehouse="orderWarehouse" :materials-locked="protectedRecord" @temporary="emit('temporary')" @line-worker="(line,item)=>emit('lineWorker',line,item)" @category-worker="(category,item)=>emit('categoryWorker',category,item)" @add-location="emit('addLocation',$event)" @remove-location="emit('removeLocation',$event)" @material-product="emit('materialProduct',$event)" @material-source="emit('materialSource')" />
      <OrderCombinationEditor ref="combinationEditor" :draft="editing" :references="references" :can="can" :busy="busy" :locked="orderLocked" />
      <p v-if="protectedRecord&&!orderLocked" class="notice">本次记录包含只读工艺，共同的工地资料、材料及带货工资保持只读。新的材料或送货请另增一次记录。</p>
      <p v-else-if="hasTicketClaims&&!orderLocked" class="notice">本次记录有已交票明细，共同的工地资料及归票分组保持只读；其他未交票明细、耗材和售料仍可更正。已交票行请先作废所属批次。</p>
      <section v-if="split&&(editing.lines.some(line=>line.temporary)||editing.sales?.length)" class="ticket-assignment"><label>临时项 / 售料归入<select v-model="editing.sales_ticket_slot" :disabled="orderLocked||busy||commonLocked"><option :value="1">第一票</option><option :value="2">第二票</option></select></label><p class="hint">正常分两票时，本次所有临时项和售料共用此选择；{{editing.project_id?'公司固定工艺按工地已保存的配置归票':'首次施工按公司当前配置建档归票'}}。更正原票或追加票时，以所选归票方式为准。</p></section>
      <section class="materials"><div class="toolbar"><h3>公共施工耗材</h3></div><p class="hint">整单共用的施工耗材，库存与各项耗材合并校验。</p><OrderMaterialsEditor :materials="editing.materials" :references="references" :inventory="inventory" :order-warehouse="orderWarehouse" :can-warehouse="can('warehouse')" :disabled="orderLocked||busy||protectedRecord" @product="emit('materialProduct',$event)" @source="emit('materialSource')" /></section>
      <OrderSalesEditor :draft="editing" :references="references" :inventory="inventory" :warehouse="orderWarehouse" :disabled="orderLocked||busy||protectedRecord" :can-warehouse="can('warehouse')" :can-wages="can('wages')" :can-customer-prices="can('customer_prices')" @add="emit('sale')" @product="emit('materialProduct',$event)" @source="emit('materialSource')" />
      <section v-if="editing.project_id&&!editing.id" class="ticket-assignment"><h3>本次新增内容如何交票</h3><label>归票方式<select v-model="editing.ticket_action"><option value="normal">按工地原定分组，累计到未交票</option><option value="correct">更正已交原票，保留已收款</option><option value="append">单独开追加票，仅含本次新增</option></select></label><label v-if="editing.ticket_action==='correct'">需要更正的原票<select v-model="editing.ticket_id" required @change="correctionChanged"><option value="">请选择原票</option><option v-for="ticket in correctionTickets" :key="ticket.id" :value="ticket.id">{{ticketLabel(ticket)}} · {{ticket.number}} · 已收 {{financeMoney(ticket.received_cents)}}</option></select></label><p class="hint">普通一票 / 两票由公司设置。原票已交后新增，可更正原票或单独追加；更正不抹掉原收款记录。</p></section>
      <OrderCustomerTotal :amount="companyTotal" :can-customer-prices="can('customer_prices')" />
    </fieldset>
    <footer><button type="button" @click="printSaved" :disabled="busy||!can('customer_prices')||(!editing.id&&!currentProjectWork?.total)">{{editing.project_id?'打印工地累计已保存内容':'打印已保存单据'}}</button><button type="button" v-if="editing.id&&!orderLocked&&!commonLocked&&can('order_delete')" @click="emit('cancelOrder')" :disabled="busy">取消本次记录</button><button type="button" :disabled="busy" @click="emit('close')">关闭</button><button v-if="!orderLocked" class="primary" :disabled="busy">{{busy?'保存中…':'保存本次施工 / 送货'}}</button></footer>
  </form>
  <ProjectWorkPanel v-if="editing.project_id" ref="workPanel" :project-id="editing.project_id" :can-finance="can('finance')&&can('customer_prices')" :can-manage-finance="can('users')" :can-wages="can('wages')" :can-customer-prices="can('customer_prices')" compact @loaded="projectLoaded" />
</template>
<style scoped>
.order-fields{display:grid;gap:12px;min-width:0}.order-fields>*{min-width:0}.extra-fields summary{padding:3px 0}.extra-fields[open]>div{padding-top:8px}.order-entry-form{min-width:0;max-width:100%}.order-basics{grid-template-columns:minmax(180px,1.5fr) minmax(150px,1fr) minmax(220px,1.3fr)}
.package-selection,.package-summary{display:grid;gap:6px;min-width:0;padding:10px 12px;border:1px solid #cbd9e8;border-radius:6px;background:#f5f8fb}.package-selection label{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.package-selection select{min-width:min(320px,100%);max-width:100%}.package-summary{font-size:13px;color:#40536a}.package-summary strong{color:#243a52}
.materials{padding-top:12px;border-top:1px solid #dce4ed}.material-row{display:grid;grid-template-columns:minmax(180px,2fr) minmax(90px,1fr) minmax(90px,1fr) minmax(130px,1fr) auto;gap:10px;align-items:end;padding:12px;background:#f8fafc;border:1px solid #dce4ed;margin-bottom:8px}.material-row label{min-width:0}.material-row input,.material-row select{width:100%;min-width:0}.material-row small{display:block;color:#65768c}.ticket-assignment{border:1px solid #d6e2f3;background:#f4f8fe;padding:12px;border-radius:6px;display:flex;gap:12px;flex-wrap:wrap}.ticket-assignment h3,.ticket-assignment p{width:100%}.ticket-assignment label{flex:1;min-width:220px}.hint{font-size:13px;color:#61758f;margin:4px 0;padding:0}.empty{padding:12px;color:#77879b;font-size:13px}
@media(max-width:800px){.order-basics{grid-template-columns:repeat(2,minmax(0,1fr))}.order-basics>:first-child{grid-column:1/-1}.material-row{grid-template-columns:repeat(2,minmax(0,1fr))}.material-row>:first-child{grid-column:1/-1}.ticket-assignment label{min-width:0;flex-basis:100%}footer{display:flex;flex-wrap:wrap;gap:8px}}
.saved-view-toggle{margin:8px 0}.saved-view-toggle small{color:#61758f;font-size:12px}.saved-order-inspection{min-width:0;max-width:100%}
</style>
