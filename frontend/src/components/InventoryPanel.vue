<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {api,SessionChangedError} from '../api'
import HistoryPanel from './HistoryPanel.vue'
import ReferenceSelect from './ReferenceSelect.vue'
import type {CatalogRow,Numeric,PageResult,StockDocument} from '../workflows/models'
import type {InventoryRow} from '../workflows/readModels'
import {formatQuantum,formatSignedQuantum,moneyFromCents} from '../stockQuantity'

const props=defineProps<{companies:CatalogRow[];warehouses:CatalogRow[];warehouse:string;revision:number;initialTab?:'history';initialFrom?:string;initialTo?:string;initialKind?:string}>()
const emit=defineEmits<{edit:[row:StockDocument];changed:[];create:[kind:string,warehouse:string]}>()
type InventoryTab='stock'|'movements'|'history'|'materials'
interface MovementRow {
 id:string
 work_date?:string
 created_at?:string
 product_id:string
 product_name?:string
 warehouse_id:string
 warehouse_name?:string
 delta_quantum:string
 quantity?:string
 unit?:string
 aux_unit?:string
 aux_ratio?:Numeric
 base_factor?:Numeric
 aux_factor?:Numeric
 source_type:string
 source_id:string
 order_number?:string
 project_address?:string
}
interface InventoryFilters {
 tab:InventoryTab
 warehouse:string
 company:string
 query:string
 state:string
 showZero:boolean
 month:string
 productId:string
 fromDate:string
 toDate:string
}
interface WarehouseDetail {
 open:boolean
 rows:InventoryRow[]
 page:number
 total:number
 pageSize:number
 busy:boolean
 error:string
 request:number
}
const tab=ref<InventoryTab>(props.initialTab||'stock'),warehouse=ref(props.warehouse),company=ref(''),query=ref(''),state=ref('all'),showZero=ref(false),month=ref(new Date().toLocaleDateString('sv-SE').slice(0,7))
const productId=ref(''),fromDate=ref(props.initialFrom||''),toDate=ref(props.initialTo||'')
const page=ref(1),pageSize=ref(100),rows=ref<InventoryRow[]>([]),movements=ref<MovementRow[]>([]),total=ref(0),busy=ref(false),error=ref(''),details=ref<Record<string,WarehouseDetail>>({})
let generation=0,detailRequest=0
const captureFilters=():InventoryFilters=>({tab:tab.value,warehouse:warehouse.value,company:company.value,query:query.value.trim(),state:state.value,showZero:showZero.value,month:month.value,productId:productId.value,fromDate:fromDate.value,toDate:toDate.value})
const applied=ref<InventoryFilters>(captureFilters())
const summary=computed(()=>applied.value.tab==='stock'&&!applied.value.warehouse)
function parameters(filters:InventoryFilters,currentPage:number){
 const params=new URLSearchParams({warehouse_id:filters.warehouse,company_id:filters.company,q:filters.query,state:filters.state,show_zero:filters.showZero?'1':'0',month:filters.month,page:String(currentPage)})
 if(filters.tab==='stock')params.set('group_by',filters.warehouse?'warehouse':'product')
 return params
}
const exportURL=computed(()=>'/api/export?'+parameters(applied.value,page.value)+'&view='+(applied.value.tab==='materials'?'materials':'inventory'))
const totalPages=computed(()=>Math.max(1,Math.ceil(total.value/pageSize.value)))

// Keep fixed-decimal quantities as text so large totals are not rounded by Number.
function quantity(value:Numeric|undefined){
 const text=String(value??'0')
 if(!/^-?\d+(?:\.\d+)?$/.test(text))return text
 const [whole,fraction='']=text.split('.')
 const trimmed=fraction.replace(/0+$/,'')
 return trimmed?whole+'.'+trimmed:whole==='-0'?'0':whole
}
function balanceQuantity(row:InventoryRow,unit:'base'|'aux'){
 if(row.quantity_quantum!=null&&/^-?\d+$/.test(String(row.quantity_quantum)))return formatQuantum(String(row.quantity_quantum),unit,row)
 return quantity(unit==='base'?row.quantity:row.auxiliary_quantity)
}
function materialQuantity(row:InventoryRow){
 const input=row.input_qty?.trim()
 if(input){
  const unit=row.input_unit==='aux'||row.input_unit===row.aux_unit?row.aux_unit||'辅助单位':row.unit
  return `${input} ${unit}`
 }
 const quantum=row.quantity_quantum??row.qty_quantum
 if(quantum==null)return '未记录精确数量'
 return `${formatQuantum(String(quantum),'base',{...row,aux_ratio:row.conversion_ratio||row.aux_ratio})} ${row.unit}`
}
function movementQuantity(row:MovementRow){
 if(row.delta_quantum!=null)return formatSignedQuantum(String(row.delta_quantum),'base',{unit:row.unit||'',aux_unit:row.aux_unit,aux_ratio:row.aux_ratio,base_factor:row.base_factor,aux_factor:row.aux_factor})
 const fallback=quantity(row.quantity)
 return fallback.startsWith('-')||fallback==='0'?fallback:'+'+fallback
}
async function load(){
 const current=++generation,filters={...applied.value}
 details.value={};rows.value=[];total.value=0;error.value=''
 movements.value=[]
 if(filters.tab==='history'){busy.value=false;return}
 busy.value=true
 try{
  if(filters.tab==='movements'){
   const params=new URLSearchParams({product_id:filters.productId,warehouse_id:filters.warehouse,from:filters.fromDate,to:filters.toDate,page:String(page.value)})
   const result=await api<PageResult<MovementRow>>('/stock-movements?'+params)
   if(current!==generation)return
   movements.value=result.rows;total.value=result.total;pageSize.value=result.page_size||100
   return
  }
  const result=await api<PageResult<InventoryRow>>((filters.tab==='materials'?'/materials?':'/inventory?')+parameters(filters,page.value))
  if(current!==generation)return
  rows.value=result.rows;total.value=result.total;pageSize.value=result.page_size||100
 }catch(e){
  if(current!==generation||e instanceof SessionChangedError)return
  error.value=(e as Error).message
 }finally{if(current===generation)busy.value=false}
}
function filter(){page.value=1;applied.value=captureFilters();void load()}
function turn(next:number){page.value=next;void load()}
function displayZero(){showZero.value=true;filter()}
const movementSource=(row:MovementRow)=>row.order_number?`施工单 ${row.order_number}`:row.source_id?`${row.source_type} ${row.source_id}`:row.source_type||'—'
function toggleDetail(productId:string){
 const existing=details.value[productId]
 if(existing?.open){existing.open=false;existing.request=++detailRequest;existing.busy=false;return}
 details.value[productId]={open:true,rows:[],page:1,total:0,pageSize:100,busy:false,error:'',request:0}
 void loadDetail(productId,1)
}
async function loadDetail(productId:string,nextPage:number){
 const detail=details.value[productId]
 if(!detail?.open||!summary.value)return
 const current=++detailRequest,listGeneration=generation,filters={...applied.value}
 detail.request=current;detail.busy=true;detail.error='';detail.page=nextPage;detail.rows=[]
 const params=parameters(filters,nextPage)
 params.set('group_by','warehouse');params.set('product_id',productId)
 const isCurrent=()=>listGeneration===generation&&details.value[productId]===detail&&detail.open&&detail.request===current
 try{
  const result=await api<PageResult<InventoryRow>>('/inventory?'+params)
  if(!isCurrent())return
  detail.rows=result.rows;detail.total=result.total;detail.pageSize=result.page_size||100
 }catch(e){if(isCurrent()&&!(e instanceof SessionChangedError))detail.error=(e as Error).message}
 finally{if(isCurrent())detail.busy=false}
}
watch(()=>props.warehouse,value=>{warehouse.value=value;filter()})
watch(()=>props.revision,()=>{void load()})
watch(tab,filter)
onBeforeUnmount(()=>{generation++;details.value={}})
void load()
</script>

<template>
 <section class="panel inventory-panel">
  <div class="tabs"><button v-for="item in [{id:'stock',name:'库存结余'},{id:'movements',name:'库存流水'},{id:'history',name:'库存单据'},{id:'materials',name:'施工消耗'}]" :key="item.id" :class="{active:tab===item.id}" @click="tab=item.id as InventoryTab">{{item.name}}</button></div>
  <div class="toolbar">
   <button v-for="kind in ['进货','领料','退料','转料','调拨','报损','报溢']" :key="kind" @click="emit('create',kind,warehouse)">{{kind}}</button>
   <ReferenceSelect v-model="warehouse" entity="warehouses" label="仓库 / 施工人" :empty-label="tab==='stock'?'全部仓库（商品汇总）':'全部仓库'" search-placeholder="搜索仓库或施工人姓名" @change="filter" />
   <template v-if="tab==='movements'">
    <ReferenceSelect v-model="productId" entity="products" label="商品" empty-label="全部商品" search-placeholder="搜索商品" @change="filter" />
    <label>开始日期<input type="date" v-model="fromDate" @change="filter"></label>
    <label>结束日期<input type="date" v-model="toDate" @change="filter"></label>
    <button :disabled="busy" @click="filter">查询</button>
   </template>
   <template v-else-if="tab!=='history'">
    <ReferenceSelect v-if="tab==='materials'" v-model="company" entity="companies" label="公司" empty-label="全部公司" search-placeholder="搜索公司" @change="filter" />
    <input v-model="query" :placeholder="tab==='stock'?'搜索商品名称、品牌或规格':'搜索商品、单号或工地'" autocomplete="off" @keyup.enter="filter">
    <button :disabled="busy" @click="filter">查询</button>
    <label v-if="tab==='stock'">商品状态<select v-model="state" @change="filter"><option value="all">全部</option><option value="active">启用</option><option value="inactive">停用</option></select></label>
    <label v-else>月份<input type="month" v-model="month" @change="filter"></label>
    <a :href="exportURL">{{summary?'导出商品汇总':'导出筛选结果'}}</a>
    <label v-if="tab==='stock'" class="check toolbar-end"><input type="checkbox" v-model="showZero" @change="filter">显示零库存商品</label>
   </template>
  </div>
  <p v-if="tab==='stock'" class="stock-help">{{summary?'全公司商品结余含总仓和工人手中材料，展开可查看各位置数量。':'已按所选仓库或工人位置显示结余。'}} 参考货值按进货金额的平均参考价估算。</p>
  <div v-if="error" class="error" role="alert">{{error}} <button :disabled="busy" @click="load">重试</button></div>
  <HistoryPanel v-if="tab==='history'" stock :warehouses="warehouses" :warehouse="warehouse" :revision="revision" :initial-from="initialFrom" :initial-to="initialTo" :initial-kind="initialKind" @edit="emit('edit',$event)" @changed="emit('changed')" />
  <template v-else-if="tab==='movements'">
   <div class="table-wrap" :aria-busy="busy"><table class="inventory-table"><thead><tr><th>日期</th><th>商品</th><th>数量变化</th><th>仓库 / 工人</th><th>业务来源</th><th>工地</th></tr></thead><tbody>
    <tr v-for="movement in movements" :key="movement.id"><td>{{movement.work_date||movement.created_at||'—'}}</td><td>{{movement.product_name||movement.product_id}}</td><td>{{movementQuantity(movement)}} {{movement.unit||''}}</td><td>{{movement.warehouse_name||movement.warehouse_id}}</td><td>{{movementSource(movement)}}</td><td>{{movement.project_address||'—'}}</td></tr>
    <tr v-if="!movements.length"><td colspan="6" class="empty">{{busy?'正在读取流水':error?'读取失败，请重试':'暂无符合条件的库存流水'}}</td></tr>
   </tbody></table></div>
   <div class="pager"><span>共 {{total}} 条</span><button :disabled="busy||page<=1" @click="turn(page-1)">上一页</button><span>{{page}} / {{totalPages}}</span><button :disabled="busy||page>=totalPages" @click="turn(page+1)">下一页</button></div>
  </template>
  <template v-else>
   <div class="table-wrap" :aria-busy="busy">
    <table class="inventory-table" :class="{'product-summary':summary}">
     <thead><tr>
      <template v-if="tab==='materials'"><th>单号 / 日期</th><th>公司 / 工地</th></template>
      <th>商品 / 规格</th>
      <th v-if="!summary">仓库</th>
       <th>{{tab==='materials'?'施工使用数量':summary?'基本总库存':'基本数量'}}</th>
      <template v-if="tab==='stock'"><th>{{summary?'辅助总库存':'辅助数量'}}</th><th>参考货值</th><th>商品状态</th></template>
     </tr></thead>
     <tbody>
      <template v-for="row in rows" :key="(row.order_id||row.warehouse_id)+row.product_id+row.name">
       <tr>
        <template v-if="tab==='materials'"><td>{{row.number}}<small>{{row.work_date}}</small></td><td>{{row.company}}<small>{{row.address}}</small></td></template>
        <td class="inventory-product">{{row.name}}<small>{{row.spec}}</small><div v-if="summary" class="distribution-controls"><button class="distribution-toggle" :aria-expanded="!!details[row.product_id]?.open" @click="toggleDetail(row.product_id)">{{details[row.product_id]?.open?'收起各仓':'查看各仓'}}</button><small>{{row.warehouse_count||0}} 个仓库</small></div></td>
        <td v-if="!summary">{{row.warehouse_name||row.warehouse}}</td>
        <td class="inventory-quantity">{{tab==='stock'?balanceQuantity(row,'base')+' '+row.unit:materialQuantity(row)}}</td>
        <template v-if="tab==='stock'">
         <td class="inventory-quantity">{{row.aux_unit?balanceQuantity(row,'aux')+' '+row.aux_unit:'—'}}</td>
         <td class="inventory-quantity">¥ {{moneyFromCents(row.reference_value_cents)}}</td><td>{{Number(row.active)?'启用':'停用'}}</td>
        </template>
       </tr>
       <tr v-if="summary&&details[row.product_id]?.open" class="inventory-details">
        <td colspan="5">
         <div class="distribution-heading">{{row.name}} · 各仓数量</div>
         <div v-if="details[row.product_id].error" class="error" role="alert">{{details[row.product_id].error}} <button :disabled="details[row.product_id].busy" @click="loadDetail(row.product_id,details[row.product_id].page)">重试</button></div>
         <table class="warehouse-table" :aria-busy="details[row.product_id].busy">
          <thead><tr><th>仓库 / 施工人</th><th>基本数量</th><th>辅助数量</th><th>参考货值</th></tr></thead>
          <tbody>
           <tr v-for="detailRow in details[row.product_id].rows" :key="detailRow.warehouse_id"><td>{{detailRow.warehouse_name||detailRow.warehouse}}</td><td>{{balanceQuantity(detailRow,'base')}} {{detailRow.unit}}</td><td>{{detailRow.aux_unit?balanceQuantity(detailRow,'aux')+' '+detailRow.aux_unit:'—'}}</td><td>¥ {{moneyFromCents(detailRow.reference_value_cents)}}</td></tr>
           <tr v-if="!details[row.product_id].rows.length"><td colspan="4" class="empty">{{details[row.product_id].busy?'正在加载各仓数量':details[row.product_id].error?'读取失败，请重试':applied.showZero?'暂无仓库记录':'各仓库存均为零'}}</td></tr>
          </tbody>
         </table>
         <div class="pager distribution-pager">
          <span>共 {{details[row.product_id].total}} 个仓库</span>
          <button :disabled="details[row.product_id].busy||details[row.product_id].page<=1" @click="loadDetail(row.product_id,details[row.product_id].page-1)">上一页</button>
          <span>{{details[row.product_id].page}} / {{Math.max(1,Math.ceil(details[row.product_id].total/details[row.product_id].pageSize))}}</span>
          <button :disabled="details[row.product_id].busy||details[row.product_id].page*details[row.product_id].pageSize>=details[row.product_id].total" @click="loadDetail(row.product_id,details[row.product_id].page+1)">下一页</button>
         </div>
        </td>
       </tr>
      </template>
      <tr v-if="!rows.length"><td :colspan="tab==='stock'?(summary?5:6):5" class="empty">
       <template v-if="busy">正在加载</template>
       <template v-else-if="error">读取失败，请重试</template>
       <template v-else-if="tab==='stock'">
        {{applied.showZero?'暂无符合条件的商品':'暂无符合条件的非零库存商品'}}
        <small>已有商品可进货或从其他仓库调拨；显示零库存可查看商品目录。</small>
        <div class="empty-actions"><button @click="emit('create','进货',warehouse)">进货</button><button @click="emit('create','调拨',warehouse)">调拨</button><button v-if="!applied.showZero" @click="displayZero">显示零库存商品</button></div>
       </template>
       <template v-else>暂无符合条件的记录</template>
      </td></tr>
     </tbody>
    </table>
   </div>
   <div class="pager"><span>共 {{total}} {{summary?'种商品':'项'}}</span><button :disabled="busy||page<=1" @click="turn(page-1)">上一页</button><span>{{page}} / {{totalPages}}</span><button :disabled="busy||page>=totalPages" @click="turn(page+1)">下一页</button></div>
  </template>
 </section>
</template>

<style scoped>
.stock-help{margin:12px 0;color:#667487;font-size:13px}
.inventory-table{min-width:650px}
.inventory-table.product-summary{min-width:580px}
.inventory-product{max-width:260px;overflow-wrap:anywhere}
.inventory-quantity{white-space:nowrap}
.distribution-controls{display:flex;align-items:center;flex-wrap:wrap;gap:8px;margin-top:8px}
.distribution-controls small{margin:0}
.distribution-toggle{white-space:nowrap;padding:7px 10px}
.inventory-details>td{padding:16px;background:#f5f8fc}
.distribution-heading{font-weight:600;margin-bottom:12px;overflow-wrap:anywhere}
.warehouse-table{min-width:420px;background:white}
.warehouse-table th,.warehouse-table td{padding:10px 12px}
.warehouse-table td:first-child{width:45%;overflow-wrap:anywhere}
.distribution-pager{margin-top:12px;margin-bottom:0}
.empty-actions{display:flex;justify-content:center;gap:8px;flex-wrap:wrap;margin-top:12px}
@media(max-width:600px){.inventory-details>td{padding:12px}.stock-help{line-height:1.6}.inventory-product{max-width:220px}}
</style>
