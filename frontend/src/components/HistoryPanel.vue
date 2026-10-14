<script setup lang="ts">
import {ref,onMounted,watch} from 'vue'
import {api,command} from '../api'
import type {StockDocument,StockLine} from '../workflows/models'
import {formatQuantum,moneyFromCents,scaledDecimal} from '../stockQuantity'
interface HistoryRow {id:string;name?:string;kind?:string;work_date?:string;source_id?:string;destination_id?:string;source_name?:string;destination_name?:string;total_cents?:number|string;voided?:boolean|number;created_at?:string;actor_name?:string;action?:string;target_id?:string;detail?:unknown;handler?:string;note?:string;line_items?:unknown;void_reason?:string;version?:number|string}
type Row=HistoryRow
const props=defineProps<{stock?:boolean;warehouses?:Row[];warehouse?:string;revision?:number;initialFrom?:string;initialTo?:string;initialKind?:string}>()
const emit=defineEmits<{edit:[row:StockDocument];changed:[]}>()
const query=ref(''),actor=ref(''),action=ref(props.initialKind||''),from=ref(props.initialFrom||''),to=ref(props.initialTo||''),state=ref('active'),page=ref(1),total=ref(0),rows=ref<Row[]>([]),detail=ref<HistoryRow|null>(null),reason=ref(''),error=ref(''),busy=ref(false)
const submit=command()
let generation=0
const money=(v:unknown)=>moneyFromCents(String(v??'0'))
const warehouseName=(id:string)=>props.warehouses?.find(w=>w.id===id)?.name||id||'—'
function parsed(v:unknown){if(typeof v!=='string')return v;try{return JSON.parse(v)}catch{return v}}
function stockLines(value:unknown):StockLine[]{const candidate=parsed(value);return Array.isArray(candidate)?candidate.filter((line):line is StockLine=>!!line&&typeof line==='object'&&typeof line.product_id==='string'):[]}
function stockQuantity(line:StockLine){
 if(line.input_quantity||line.input_qty)return `${line.input_quantity||line.input_qty} ${line.input_unit==='aux'?line.aux_unit||'辅助单位':line.input_unit==='base'?line.unit:line.input_unit}`
 if(line.quantity_quantum)return `${formatQuantum(line.quantity_quantum,'base',line)} ${line.unit}`
 return /^\d+$/.test(String(line.qty_micro))?`${scaledDecimal(BigInt(String(line.qty_micro)))} ${line.unit}`:'—'
}
const fieldNames:Record<string,string>={before:'修改前',after:'修改后',id:'编号',version:'版本',name:'名称',username:'账号',role:'角色',permissions:'功能权限',crafts:'工艺权限',active:'启用状态',entity:'资料类型',kind:'单据类型',date:'日期',work_date:'施工日期',from:'调出仓库',to:'调入仓库',source_id:'调出仓库',destination_id:'调入仓库',warehouse_id:'仓库',product_id:'商品',worker_id:'施工人',company_id:'公司',address:'工地地址',contact:'项目经理',phone:'电话',note:'备注',reason:'原因',qty_micro:'数量（基本单位）',price_cents:'单价（元）',total_cents:'金额（元）',amount_cents:'金额（元）',wage_cents:'人工单价（元）',labor_cents:'人工金额（元）',lines:'明细',line_items:'明细',unit:'单位',color:'颜色',brand:'品牌',spec:'规格',aux_unit:'辅助单位',aux_ratio:'换算比例',weight_kg:'重量（kg）',category:'分类',mode:'计费方式',segments:'尺寸',length:'长',width:'宽',height:'高',surface:'区域',temporary:'临时项',status:'状态',voided:'作废状态',void_reason:'作废原因',created_at:'创建时间',created_by:'创建人',source_id_snapshot:'原记录',materials:'材料消耗',orders:'单据',warehouse_type:'仓库类型'}
function auditFields(value:unknown,path=''):Array<{field:string;value:string}>{
 value=parsed(value)
 if(value===null||typeof value!=='object')return [{field:path||'内容',value:value==null?'—':String(value)}]
 return Object.entries(value).flatMap(([key,v])=>{if(/password|secret|token|access_key/i.test(key))return [{field:path+' / 凭据',value:'已隐藏'}];const label=Array.isArray(value)?'第 '+(Number(key)+1)+' 项':fieldNames[key]||key;const name=path?path+' / '+label:label;if(key.endsWith('_cents'))return [{field:name,value:(Number(v||0)/100).toFixed(2)}];if(key==='qty_micro')return [{field:name,value:String(Number(v||0)/1e6)}];return auditFields(v,name)})
}
async function load(reset=false){if(reset)page.value=1;const g=++generation;busy.value=true;error.value='';try{const p=new URLSearchParams({q:query.value,actor:actor.value,action:action.value,kind:props.stock?action.value:'',from:from.value,to:to.value,state:state.value,warehouse:props.warehouse||'',page:String(page.value)});const r=await api<{rows:HistoryRow[];total:number}>((props.stock?'/stock-documents':'/audit')+'?'+p);if(g===generation){rows.value=r.rows;total.value=r.total}}catch(e){if(g===generation){rows.value=[];total.value=0;error.value=(e as Error).message}}finally{if(g===generation)busy.value=false}}
async function open(row:Row){if(busy.value)return;error.value='';busy.value=true;try{detail.value=props.stock?await api<StockDocument>('/stock-document?id='+encodeURIComponent(row.id)):row;reason.value=''}catch(e){error.value=(e as Error).message}finally{busy.value=false}}
async function changeState(){if(busy.value||!detail.value)return;const restoring=!!Number(detail.value.voided);if(!confirm(restoring?'恢复此单并重新计算库存？':'作废此单并反向调整库存？'))return;busy.value=true;error.value='';try{await submit('/stock-state',{id:detail.value.id,version:Number(detail.value.version),action:restoring?'restore':'void',reason:reason.value});detail.value=null;await load();emit('changed')}catch(e){error.value=(e as Error).message}finally{busy.value=false}}
function edit(){const row=detail.value;if(!row)return;detail.value=null;emit('edit',{...row,line_items:stockLines(row.line_items)} as StockDocument)}
function print(){window.print()}
watch(()=>[props.warehouse,props.revision],()=>load(true));onMounted(()=>load())
</script>
<template>
 <section class="history-panel">
  <form class="toolbar" @submit.prevent="load(true)" autocomplete="off">
   <label v-if="stock">显示范围<select v-model="state"><option value="active">有效单据</option><option value="void">回收站 / 已作废</option><option value="all">全部</option></select></label>
   <label v-else>操作人<input v-model="actor" placeholder="姓名"></label>
   <label>类型<select v-if="stock" v-model="action"><option value="">全部</option><option>进货</option><option>领料</option><option>退料</option><option>转料</option><option>调拨</option><option>报损</option><option>报溢</option></select><input v-else v-model="action" placeholder="操作名称"></label>
   <label>开始日期<input type="date" v-model="from"></label><label>结束日期<input type="date" v-model="to"></label>
   <input v-model="query" :placeholder="stock?'单据编号或备注':'记录编号或变更内容'" aria-label="搜索记录"><button :disabled="busy">查询</button>
  </form>
  <p v-if="error" class="error" role="alert">{{error}}</p>
  <div class="table-wrap"><table><thead><tr><template v-if="stock"><th>类型 / 编号</th><th>日期</th><th>来源位置</th><th>去向位置</th><th>进货金额</th><th>状态</th></template><template v-else><th>时间</th><th>操作人</th><th>操作</th><th>记录编号</th></template><th>操作</th></tr></thead><tbody>
   <tr v-for="r in rows" :key="r.id"><template v-if="stock"><td>{{r.kind}}<small>{{r.id}}</small></td><td>{{r.work_date}}</td><td>{{r.source_name||warehouseName(r.source_id||'')}}</td><td>{{r.destination_name||warehouseName(r.destination_id||'')}}</td><td>{{r.kind==='进货'?(r.total_cents==null?'未记录':'¥ '+money(r.total_cents)):'—'}}</td><td>{{Number(r.voided)?'已作废':'有效'}}</td></template><template v-else><td>{{r.created_at}}</td><td>{{r.actor_name}}</td><td>{{r.action}}</td><td>{{r.target_id}}</td></template><td><button class="link" :disabled="busy" @click="open(r)">详情{{stock?' / 纠错':''}}</button></td></tr>
   <tr v-if="!rows.length"><td :colspan="stock?7:5" class="empty">{{busy?'加载中':error?'读取失败，请重试':'暂无符合条件的记录'}}</td></tr>
  </tbody></table></div>
  <div class="toolbar"><span>共 {{total}} 条 · 第 {{page}} / {{Math.max(1,Math.ceil(total/100))}} 页</span><button :disabled="busy||page<=1" @click="page--;load()">上一页</button><button :disabled="busy||page*100>=total" @click="page++;load()">下一页</button></div>
  <Teleport to="body"><div v-if="detail" class="overlay history-overlay"><section class="modal wide"><div class="modal-head"><h2>{{stock?detail.kind+'单据':'操作详情'}}</h2><button :disabled="busy" @click="detail=null" aria-label="关闭">×</button></div><div class="history-body">
   <p v-if="error" class="error">{{error}}</p>
   <template v-if="stock"><p>编号：{{detail.id}} · 日期：{{detail.work_date}} · {{Number(detail.voided)?'已作废':'有效'}}</p><p>来源：{{detail.source_name||warehouseName(detail.source_id||'')}}　去向：{{detail.destination_name||warehouseName(detail.destination_id||'')}}</p><p>经手人：{{detail.handler||'—'}}</p><p>备注：{{detail.note||'—'}}</p>
    <div class="table-wrap"><table><thead><tr><th>商品</th><th>录入数量</th><th>基本数量</th><th>明细备注</th><template v-if="detail.kind==='进货'"><th>本次进货金额</th></template></tr></thead><tbody><tr v-for="l in stockLines(detail.line_items)" :key="l.product_id"><td>{{l.name||l.product_id}}</td><td>{{stockQuantity(l)}}</td><td>{{l.quantity_quantum?formatQuantum(l.quantity_quantum,'base',l):/^\d+$/.test(String(l.qty_micro))?scaledDecimal(BigInt(String(l.qty_micro))):'—'}} {{l.unit||'—'}}</td><td>{{l.note||'—'}}</td><template v-if="detail.kind==='进货'"><td>¥ {{money(l.total_cents)}}</td></template></tr></tbody></table></div>
    <p v-if="!detail.line_items" class="error">此旧单没有保存商品明细，不能编辑或自动恢复。</p><p v-if="detail.kind==='进货'">合计：{{detail.total_cents==null?'未记录':'¥ '+money(detail.total_cents)}}</p><p v-if="detail.void_reason">作废原因：{{detail.void_reason}}</p>
    <label v-if="!Number(detail.voided)" class="no-print">作废原因<textarea v-model="reason" maxlength="1000"></textarea></label>
    <footer><button @click="print">打印</button><button v-if="!Number(detail.voided)" :disabled="busy||!detail.line_items" @click="edit">修改单据</button><button :disabled="busy||(!Number(detail.voided)&&!reason.trim())||(!!Number(detail.voided)&&!detail.line_items)" @click="changeState">{{Number(detail.voided)?'恢复单据':'作废单据'}}</button></footer>
   </template>
   <template v-else><p>{{detail.created_at}} · {{detail.actor_name}} · {{detail.action}}</p><p>对象：{{detail.target_id}}</p><div class="table-wrap"><table class="audit-detail"><thead><tr><th>变更字段</th><th>内容</th></tr></thead><tbody><tr v-for="(f,i) in auditFields(detail.detail)" :key="i"><td>{{f.field}}</td><td>{{f.value}}</td></tr></tbody></table></div></template>
  </div></section></div></Teleport>
 </section>
</template>
<style scoped>
.history-body{padding:20px}.history-body p{margin:10px 0;overflow-wrap:anywhere}.audit-detail{white-space:pre-wrap;overflow-wrap:anywhere;background:#f7f8fa;padding:16px;font-size:13px}.history-body textarea{min-height:72px}
@media print{.history-overlay .modal{width:100%}.history-body{padding:0}}
</style>
