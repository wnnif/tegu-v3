<script setup lang="ts">
import ReferenceSelect from './ReferenceSelect.vue'
import CompanyCraftOrder from './CompanyCraftOrder.vue'
import {orderedCompanyCategories} from '../companyCraftOrder'
import {computed,ref} from 'vue'
import {CUSTOMER_PRICE_HIDDEN,hasCustomerPrice} from '../customerPrices'
import type {CatalogDraft,CatalogRow,References,Craft} from '../workflows/models'
const props=defineProps<{editing:CatalogDraft;categories:string[];references:References;busy:boolean;locked?:boolean;craftCategory:string;companyCraftOptions:Craft[];companyFixedVisible:Craft[];craftPage:number;craftTotal:number;can:(p:string)=>boolean;hasCompanyCraft:(c:Craft)=>boolean}>()
const emit=defineEmits<{save:[];selectCategory:[];category:[value:string];searchCraft:[query:string];craftPage:[page:number];toggleCraft:[item:Craft,selected:boolean];removeCraft:[item:Craft]}>()
const craftSearch=ref('')
const companyCategoryTabs=computed(()=>orderedCompanyCategories(props.categories,props.editing.craft_category_order))
const companyOrderAvailable=computed(()=>props.editing.fixed_crafts.map(c=>c.category))
function changeCompanyCategoryOrder(order:string[]){if(props.editing.entity!=='companies'||props.busy||props.locked||!props.can('customers'))return;props.editing.craft_category_order=[...order]}
const unlinkedIds=new Set(props.editing.fixed_crafts.filter(c=>c.source_missing||!c.source_id).map(c=>c.id))
const unit=(mode:string)=>({'平方':'平方米','立方':'立方米','米':'米','个':'个','家':'家','根':'根','处':'处','孔位':'孔位','副':'副','张':'张','套':'套','金额':'项'}[mode]||'项')
const craftKey=(craft:Craft)=>craft.id||'source:'+craft.source_id
let subitemSequence=0
function addSubitem(){(props.editing.subitems ||= []).push({id:'subitem-'+Date.now().toString(36)+'-'+(++subitemSequence),name:''})}
function moveSubitem(index:number,delta:number){const list=props.editing.subitems||[];const target=index+delta;if(target<0||target>=list.length)return;const [item]=list.splice(index,1);if(item)list.splice(target,0,item)}
const mayRemoveCraft=(craft:Craft)=>props.can('customer_prices')||!props.editing.fixed_crafts.some(item=>item.included_by_craft_id===craftKey(craft))
function removeCraft(craft:Craft){if(mayRemoveCraft(craft))emit('removeCraft',craft)}
function craftNeighbor(craft:Craft,delta:number){const peers=props.editing.fixed_crafts.filter(item=>item.category===craft.category);return peers[peers.indexOf(craft)+delta]}
function moveCraft(craft:Craft,delta:number){
 if(props.editing.entity!=='companies'||props.busy||props.locked||!props.can('customers'))return
 const neighbor=craftNeighbor(craft,delta);if(!neighbor)return
 const next=[...props.editing.fixed_crafts],index=next.indexOf(craft),target=next.indexOf(neighbor)
 next[index]=neighbor;next[target]=craft;props.editing.fixed_crafts=next
}
function changeTicketMode(){if(props.editing.ticket_mode==='single')for(const item of props.editing.fixed_crafts)item.ticket_slot=1}
function changeTicketSlot(craft:Craft){for(const item of props.editing.fixed_crafts)if(item.included_by_craft_id===craftKey(craft))item.ticket_slot=craft.ticket_slot}
function assignSource(craft:Craft,source:CatalogRow|null){if(!source)return;craft.source_id=source.id;craft.name=source.name;craft.category=source.category||craft.category;craft.mode=source.mode||craft.mode;craft.spec=source.spec;craft.wage_cents=source.wage_cents;craft.subitems=source.subitems||[];craft.source_missing=false;craft.source_active=source.source_active}
function setCompanyWageOverride(craft:Craft,event:Event){if(!props.can('rates')&&!props.can('wages'))return;const raw=(event.target as HTMLInputElement).value.trim();craft.wage_override_cents=raw===''?null:Math.round(Number(raw)*100)}
function setSubitemWage(item:NonNullable<CatalogDraft['subitems']>[number],event:Event){if(!props.can('rates')&&!props.can('wages'))return;const raw=(event.target as HTMLInputElement).value.trim();if(raw==='')delete item.wage_cents;else item.wage_cents=Math.round(Number(raw)*100)}
</script>
<template>
 <form @submit.prevent="emit('save')" autocomplete="off">
  <p v-if="editing.entity==='workers'&&!editing.id" class="hint">保存后自动创建与施工人同名的仓库。制单选择该施工人时自动使用该仓库。</p>
  <fieldset :disabled="busy||locked">
   <div class="form-grid">
    <label>名称<input v-model="editing.name" :maxlength="['warehouses','workers'].includes(editing.entity)?100:200" required></label>
    <template v-if="['companies','workers'].includes(editing.entity)"><label>联系电话<input v-model="editing.phone" maxlength="60"></label><label v-if="editing.entity==='companies'">联系人<input v-model="editing.contact" maxlength="100"></label><label v-if="editing.entity==='companies'">客户类型<input v-model="editing.customer_type" maxlength="100"></label></template>
    <ReferenceSelect v-if="editing.entity==='warehouses'" v-model="editing.worker_id" entity="workers" label="对应施工人" empty-label="未关联" search-placeholder="搜索施工人" :selected-name="editing.worker_name" :disabled="busy" />
    <template v-if="editing.entity==='products'"><label>品牌<input v-model="editing.brand" maxlength="100"></label><label>规格<input v-model="editing.spec" maxlength="200"></label><label>颜色<input v-model="editing.color" maxlength="100"></label><label>重量（kg/基本单位）<input v-model="editing.weight_kg" type="number" min="0" max="1000000" step="0.000001"></label><label>基本单位<input v-model="editing.unit" maxlength="30" required></label><label>辅助单位<input v-model="editing.aux_unit" maxlength="30"></label><label>1基本单位含多少辅助单位<input v-model="editing.aux_ratio" type="text" inputmode="decimal" pattern="(?:0\.(?=[0-9]{0,5}[1-9])[0-9]{1,6}|[1-9][0-9]{0,5}(?:\.[0-9]{1,6})?|1000000(?:\.0{1,6})?)" maxlength="20" required></label></template>
    <template v-if="editing.entity==='warehouses'"><label>仓库分类<input v-model="editing.category" maxlength="100" required></label><label>仓库类型<select v-model="editing.warehouse_type"><option>普通仓库</option><option>工地仓库</option><option>临时仓库</option></select></label></template>
    <label v-if="['products','warehouses','companies'].includes(editing.entity)">备注<input v-model="editing.note" maxlength="1000"></label>
    <template v-if="editing.entity==='crafts'">
     <label>规格 / 颜色 / 工艺<input v-model="editing.spec" maxlength="200"></label>
     <label>施工大类<select v-model="editing.category"><option v-for="c in categories" :key="c">{{c}}</option></select></label>
     <label>计量方式<select v-model="editing.mode"><option>平方</option><option>立方</option><option>米</option><option>金额</option><option>个</option><option>家</option><option>根</option><option>处</option><option>孔位</option><option>副</option><option>张</option><option>套</option></select></label>
     <label>公共标准工资（元/{{unit(editing.mode)}}）<input :value="editing.wage_cents/100" @input="editing.wage_cents=Math.round(Number(($event.target as HTMLInputElement).value)*100)" type="number" min="0" max="1000000" step="0.01" required></label>
    </template>
    <label v-if="['crafts','products','warehouses'].includes(editing.entity)" class="check"><input type="checkbox" v-model="editing.active">启用</label>
   </div>
   <section v-if="editing.entity==='crafts'">
    <p class="hint">各公司收费在公司固定工艺中单独设置；此处的人工单价对所有公司统一生效，新施工按新标准计算，历史施工保留原标准。金额模式按每个已填写小项记一项工资。</p>
    <div class="toolbar"><h3>施工部位 / 小项</h3><button type="button" :disabled="(editing.subitems||[]).length>=100" @click="addSubitem">＋ 添加部位</button></div>
    <p class="hint">按以下顺序在制单中展开。小项名称由你维护，每个小项可以填写多段尺寸；按米只填长度并合计。不设小项时显示一行待填写位置。</p>
    <ol class="subitems"><li v-for="(item,index) in editing.subitems" :key="item.id"><input v-model="item.name" :aria-label="'第'+(index+1)+'个小项名称'" placeholder="小项 / 施工位置名称" maxlength="100" required><label v-if="can('rates')||can('wages')" class="subitem-wage">小项工资（元/{{unit(editing.mode)}}）<input :aria-label="item.name+'小项工资'" :value="item.wage_cents==null?'':Number(item.wage_cents)/100" type="number" min="0" max="1000000" step="0.01" placeholder="沿用公共工资" @input="setSubitemWage(item,$event)"></label><button type="button" :disabled="index===0" @click="moveSubitem(index,-1)">上移</button><button type="button" :disabled="index===(editing.subitems||[]).length-1" @click="moveSubitem(index,1)">下移</button><button type="button" @click="editing.subitems?.splice(index,1)">移除</button></li></ol>
    <p v-if="!editing.subitems?.length" class="hint">尚未设置小项。</p>
   </section>
   <section v-if="editing.entity==='workers'"><h3>可施工的大类</h3><p class="hint">施工人技能与员工账号填写权限分开设置。不勾选表示不能施工，修改不改变历史单据。</p><div class="craft-options"><label v-for="c in categories" :key="c" class="check"><input type="checkbox" v-model="editing.crafts" :value="c">{{c}}</label></div></section>
   <section v-if="editing.entity==='companies'" class="company-crafts">
    <div class="toolbar"><h3>公司固定工艺</h3><span>已选 {{editing.fixed_crafts.length}} 项</span></div>
    <label class="ticket-choice">正常交票方式<select v-model="editing.ticket_mode" @change="changeTicketMode"><option value="single">统一一张票</option><option value="split">分两次交票</option></select></label>
    <p class="hint">首次制单按施工地址建档时使用此设置。分两次时逐项指定第一票或第二票；工地后续新增仍可更正原票或另开追加票。</p>
    <CompanyCraftOrder :available="companyOrderAvailable" :model-value="editing.craft_category_order" :company-key="editing.id" :disabled="busy||locked||!can('customers')" @update:model-value="changeCompanyCategoryOrder" />
    <div class="tabs"><button v-for="c in ['全部',...companyCategoryTabs]" :key="c" type="button" :class="{active:craftCategory===c}" @click="emit('category',c)">{{c}}</button></div>
    <div class="toolbar"><input v-model="craftSearch" placeholder="搜索工艺名称或规格" aria-label="搜索公共工艺" @keyup.enter.prevent="emit('searchCraft',craftSearch)"><button type="button" @click="emit('searchCraft',craftSearch)">查询</button><button type="button" @click="emit('selectCategory')" :disabled="!companyCraftOptions.length||!can('customer_prices')">选择当前结果</button></div>
    <div class="craft-options company-craft-options"><label v-for="c in companyCraftOptions" :key="c.id" class="check company-craft-option"><input type="checkbox" :checked="hasCompanyCraft(c)" :disabled="!can('customer_prices')&&(!hasCompanyCraft(c)||editing.fixed_crafts.some(item=>item.source_id===c.id&&!mayRemoveCraft(item)))" @change="emit('toggleCraft',c,($event.target as HTMLInputElement).checked)"><span class="company-craft-copy"><strong class="company-craft-name">{{c.name}}</strong><small>{{c.spec?c.spec+' · ':''}}{{c.mode}}</small></span></label><p v-if="!companyCraftOptions.length" class="hint">暂无工艺分项，请先在工艺管理中设置。</p></div>
    <div class="toolbar"><span>公共工艺 {{craftTotal}} 项 · 第 {{craftPage}} / {{Math.max(1,Math.ceil(craftTotal/50))}} 页</span><button type="button" :disabled="busy||craftPage<=1" @click="emit('craftPage',craftPage-1)">上一页</button><button type="button" :disabled="busy||craftPage*50>=craftTotal" @click="emit('craftPage',craftPage+1)">下一页</button></div>
    <p v-if="!can('customer_prices')" class="hint">客户收费无查看权限；普通公司资料和已有工艺可维护，新增收费工艺需要“查看客户价格”权限，保存保留原价格。</p>
    <p class="hint combination-help">新施工默认各项正常收费，免收须在制单中明确选择实际明细、方向和面积；两项工资始终正常计算。历史含价关联仅保留兼容，不再自动免收。</p>
    <div class="fixed-craft-list">
     <p v-if="companyFixedVisible.length" class="hint">同一大类内可上移、下移工艺；本公司的收费和顺序独立保存，公共标准工资在工艺管理中统一维护。调整后点击表单底部“保存”。</p>
     <article v-for="c in companyFixedVisible" :key="craftKey(c)" class="fixed-craft">
      <div class="toolbar"><strong>{{c.category}} · {{c.name}}</strong><span>{{c.spec}}</span><button type="button" :aria-label="'上移工艺'+c.name" :disabled="busy||locked||!can('customers')||!craftNeighbor(c,-1)" @click="moveCraft(c,-1)">上移</button><button type="button" :aria-label="'下移工艺'+c.name" :disabled="busy||locked||!can('customers')||!craftNeighbor(c,1)" @click="moveCraft(c,1)">下移</button><button type="button" class="link" :disabled="!mayRemoveCraft(c)" @click="removeCraft(c)">移除</button></div>
      <template v-if="unlinkedIds.has(c.id)||c.source_missing||!c.source_id"><p v-if="c.source_missing||!c.source_id" class="error">此历史固定工艺没有公共来源，请明确关联后保存。</p><ReferenceSelect :model-value="c.source_id||''" entity="crafts" label="公共工艺来源" empty-label="请选择公共工艺" active="1" :disabled="busy" @change="assignSource(c,$event)" /></template>
      <p v-else-if="c.source_active===false" class="error">公共工艺已停用，历史记录保留，不能新增施工。</p>
      <div class="fixed-fields">
       <label v-if="can('customer_prices')&&hasCustomerPrice(c.price_cents)">收费单价（元/{{unit(c.mode)}}）<input :aria-label="c.name+'收费单价'" :value="Number(c.price_cents)/100" @input="c.price_cents=Math.round(Number(($event.target as HTMLInputElement).value)*100)" type="number" min="0" max="1000000" step="0.01" required><small v-if="c.customer_price_test_only">模拟测试价</small></label>
       <label v-else>收费单价<output>{{CUSTOMER_PRICE_HIDDEN}}</output></label>
       <label v-if="can('wages')||can('rates')">公共标准工资（元/{{unit(c.mode)}}）<output>{{(c.public_wage_cents??c.wage_cents)===undefined?'—':(Number(c.public_wage_cents??c.wage_cents)/100).toFixed(2)}}</output><small v-if="c.wage_test_only">模拟测试价</small></label>
       <label v-if="can('wages')||can('rates')">公司特殊工资覆盖（元/{{unit(c.mode)}}）<input :aria-label="c.name+'公司特殊工资覆盖'" :value="c.wage_override_cents==null?'':Number(c.wage_override_cents)/100" type="number" min="0" max="1000000" step="0.01" placeholder="沿用工艺小项或公共工资" @input="setCompanyWageOverride(c,$event)"><small v-if="c.wage_test_only">模拟测试价</small><small>留空恢复工艺小项工资；小项未单独设置时沿用公共工资。填写0为明确的零工资。仅影响之后新建的施工记录。</small></label>
       <label v-if="editing.ticket_mode==='split'">交票分组<select v-model.number="c.ticket_slot" :disabled="!!c.included_by_craft_id" @change="changeTicketSlot(c)"><option :value="1">第一张票</option><option :value="2">第二张票</option></select></label>
       <label v-if="c.included_by_craft_id">历史含价关联（只读保留）<output>{{editing.fixed_crafts.find(item=>craftKey(item)===c.included_by_craft_id)?.name||c.included_by_craft_id}}</output></label>
      </div>
      <p v-if="c.included_by_craft_id&&can('customer_prices')" class="hint">历史关联和旧单快照保持不变。新施工使用这里明确配置的收费单价；若旧配置仍为0，需人工确认并维护正常收费价，系统不会猜价或批量改历史数据。</p>
      <p class="hint">{{['个','家','根','处','孔位','副','张','套'].includes(c.mode)?'按'+unit(c.mode)+'填写整数数量，例如2'+unit(c.mode)+' × 公司单价。':c.mode==='金额'?'金额模式按整项收费金额填写。':'制单按'+unit(c.mode)+'计算数量。'}} 小项：{{c.subitems?.map(item=>item.name).join('、')||'未设置'}}</p>
      <ul v-if="(can('rates')||can('wages'))&&c.subitems?.length" class="subitem-wage-summary"><li v-for="item in c.subitems" :key="item.id">{{item.name}}：{{item.wage_cents==null?'沿用公共工资':(Number(item.wage_cents)/100).toFixed(2)+' 元/'+unit(c.mode)}}</li></ul>
     </article>
    </div>
    <details v-if="editing.id&&references.crafts.some((c:Craft)=>c.company_id===editing.id&&!Number(c.active))"><summary>已停用的历史固定工艺</summary><ul><li v-for="c in references.crafts.filter((item:Craft)=>item.company_id===editing.id&&!Number(item.active))" :key="c.id">{{c.category}} · {{c.name}} {{c.spec}}（已停用）</li></ul></details>
   </section>
   <footer><button class="primary" :disabled="busy">{{busy?'正在保存…':'保存'}}</button></footer>
  </fieldset>
 </form>
</template>
<style scoped>
.company-craft-options{grid-template-columns:repeat(auto-fit,minmax(240px,1fr));align-items:start}.company-craft-option{display:grid;grid-template-columns:16px minmax(0,1fr);gap:8px;align-items:start;min-width:0}.company-craft-option>input{margin:3px 0 0}.company-craft-copy{display:block;min-width:0}.company-craft-name{display:block;line-height:1.55;overflow-wrap:anywhere;font-weight:500;color:#314361}.company-craft-copy small{margin-top:4px;line-height:1.5;overflow-wrap:anywhere}.combination-help{padding:0 0 12px}
.subitems{padding-left:24px}.subitems li{display:flex;gap:6px;align-items:center;margin:8px 0}.subitems input{flex:1;min-width:100px}.subitems button{white-space:nowrap;padding:8px}.ticket-choice{display:flex;gap:12px;align-items:center;max-width:420px}.fixed-craft-list{display:grid;gap:12px}.fixed-craft{padding:14px;border:1px solid #dce3eb;border-radius:8px}.fixed-craft>.toolbar{margin-bottom:10px}.fixed-fields{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px}.fixed-fields label{display:flex;flex-direction:column;gap:6px;min-width:0}.fixed-fields input,.fixed-fields select{width:100%;min-width:0}.fixed-fields output{display:block;padding:9px;background:#f1f5f9;border-radius:4px;color:#4c5b6e}.fixed-craft .hint{margin:10px 0 0}@media(max-width:600px){.subitems li{flex-wrap:wrap}.subitems input{flex-basis:100%}.fixed-fields{grid-template-columns:1fr}.ticket-choice{align-items:stretch;flex-direction:column}.fixed-craft{padding:10px}}
@media(max-width:600px){.company-craft-options{grid-template-columns:1fr}}
.subitems li{flex-wrap:wrap}.subitem-wage{min-width:180px}.subitem-wage input{width:100%;margin-top:4px}.subitem-wage-summary{margin:8px 0;padding-left:22px;color:#526175;font-size:13px}
</style>
