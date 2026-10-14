<script setup lang="ts">
import {computed,ref} from 'vue'
import { customerPrice, hasCustomerPrice, CUSTOMER_PRICE_HIDDEN } from '../customerPrices'
import ReferenceSelect from './ReferenceSelect.vue'
import type {Sale} from '../projectWork'
import type {CatalogRow,OrderDraft,References,Balance} from '../workflows/models'
import {saleAmountCents} from '../orderSegments'
import {fromQuantum,formatQuantum,toQuantum,unitFactors} from '../stockQuantity'
const props=defineProps<{draft:OrderDraft;references:References;inventory:Balance[];warehouse:string;disabled:boolean;canWarehouse:boolean;canWages:boolean;canCustomerPrices:boolean}>()
const emit=defineEmits<{add:[];product:[item:CatalogRow|null];source:[]}>()
const warning=ref('')
const saleLocked=(sale:Sale)=>props.disabled||sale.editable===false
const product=(sale:Sale)=>{
 const current=props.references.products.find(item=>item.id===sale.product_id)
 return sale.id?{...current,...sale,unit:sale.unit||current?.unit||'',aux_ratio:sale.conversion_ratio||current?.aux_ratio,aux_unit:sale.input_unit==='aux'?sale.price_unit:current?.aux_unit,base_factor:undefined,aux_factor:undefined}:{...sale,...current,unit:current?.unit||sale.unit||''}
}
function saleQuantum(sale:Sale){
 if(sale.qty_quantum)return sale.qty_quantum
 if(sale.input_qty){const exact=toQuantum(sale.input_qty,sale.input_unit||'base',product(sale));if(exact)return exact}
 const factors=unitFactors(product(sale))
 return factors?String(BigInt(String(sale.qty_micro||0))*factors.base):'0'
}
const quantity=(sale:Sale)=>fromQuantum(saleQuantum(sale),sale.input_unit||'base',product(sale))||sale.input_qty||''
function setQuantity(sale:Sale,value:string){
 if(saleLocked(sale))return
 sale.input_qty=value
 sale.qty_quantum=toQuantum(value,sale.input_unit||'base',product(sale))||''
 const factors=unitFactors(product(sale))
 sale.qty_micro=sale.qty_quantum&&factors?Number(BigInt(sale.qty_quantum)/factors.base):0
 warning.value=''
}
function changeUnit(sale:Sale,event:Event){
 if(saleLocked(sale)||!props.canCustomerPrices)return
 const selected=(event.target as HTMLSelectElement).value,old=sale.input_unit||'base',quantum=saleQuantum(sale)
 const converted=fromQuantum(quantum,selected,product(sale))
 if(converted===null){
  (event.target as HTMLSelectElement).value=old
  warning.value='此数量无法用所选单位精确表示，请保留原单位。'
  return
 }
 sale.input_unit=selected
 sale.input_qty=converted
 sale.qty_quantum=quantum
 warning.value='单位已切换，请核对按新单位填写的销售单价。'
}
const priceVisible=(sale:Sale)=>props.canCustomerPrices&&hasCustomerPrice(sale.price_cents)
const amount=(sale:Sale)=>props.canCustomerPrices&&hasCustomerPrice(sale.price_cents)?saleAmountCents(quantity(sale),sale.price_cents):undefined
const total=computed(()=>props.canCustomerPrices&&(props.draft.sales||[]).every(priceVisible)?(props.draft.sales||[]).reduce((sum,sale)=>sum+amount(sale)!,0):undefined)
const money=customerPrice
const available=(sale:Sale)=>{
 const balance=props.inventory.find(item=>item.product_id===sale.product_id&&item.warehouse_id===(sale.warehouse_id||props.warehouse))
 return balance?.quantity_quantum!=null?formatQuantum(String(balance.quantity_quantum),'base',product(sale)):String(Number(balance?.qty_micro||0)/1e6)
}
const baseQuantity=(sale:Sale)=>formatQuantum(saleQuantum(sale),'base',product(sale))
function setPrice(sale:Sale,event:Event){if(saleLocked(sale)||!priceVisible(sale))return;sale.price_cents=Math.round(Number((event.target as HTMLInputElement).value)*100)}
function removeSale(sale:Sale,index:number){if(!saleLocked(sale))props.draft.sales?.splice(index,1)}
</script>
<template>
  <section class="sales-editor">
    <div class="toolbar"><h3>商品销售</h3><button type="button" :disabled="disabled||(!warehouse&&!canWarehouse)||!canCustomerPrices" @click="canCustomerPrices&&emit('add')">＋ 添加销售材料</button></div>
    <p class="hint">单独销售货款计公司应收；本次带货工资单独填写。</p>
    <p v-if="!canCustomerPrices" class="hint">新增销售材料或调整计价单位需要“查看客户价格”权限；已有售料可修改数量，价格保留。</p>
    <p v-if="warning" class="stock-warning" role="alert">{{warning}}</p>
    <fieldset :disabled="disabled">
      <article v-for="(sale,index) in draft.sales||[]" :key="sale.id||index" class="sale-row">
        <ReferenceSelect v-model="sale.product_id" entity="products" label="销售商品" empty-label="请选择商品" search-placeholder="搜索商品" :selected-name="sale.product_name" active="1" :disabled="saleLocked(sale)||!!sale.id" @change="sale.qty_micro=0;sale.qty_quantum='';sale.input_qty='';sale.input_unit='base';emit('product',$event)" />
        <label>销售数量<input :value="quantity(sale)" type="number" min="0.000001" max="1000000" step="0.000001" :disabled="saleLocked(sale)" @input="setQuantity(sale,($event.target as HTMLInputElement).value)"></label>
        <label>销售单位<select :value="sale.input_unit||'base'" :disabled="saleLocked(sale)||!canCustomerPrices" @change="changeUnit(sale,$event)"><option value="base">{{product(sale).unit||'基本单位'}}</option><option v-if="product(sale).aux_unit" value="aux">{{product(sale).aux_unit}}</option></select></label>
        <ReferenceSelect v-if="canWarehouse" v-model="sale.warehouse_id" entity="warehouses" label="实际发货位置" empty-label="请选择来源" search-placeholder="搜索总仓或工人结余" :selected-name="sale.warehouse_name" active="1" :disabled="saleLocked(sale)" required @change="emit('source')" /><div v-else>发货位置 {{sale.warehouse_name||references.warehouses.find(item=>item.id===(sale.warehouse_id||warehouse))?.name||'未选择'}}</div>
        <label>销售单价（元/{{sale.input_unit==='aux'?product(sale).aux_unit:product(sale).unit||'单位'}}）<span v-if="!priceVisible(sale)">{{CUSTOMER_PRICE_HIDDEN}}</span><input v-else :value="Number(sale.price_cents)/100" type="number" min="0" max="1000000" step="0.01" :disabled="saleLocked(sale)" @input="setPrice(sale,$event)"></label>
        <div class="sale-amount"><span>材料货款</span><strong>{{money(amount(sale))}}{{priceVisible(sale)?' 元':''}}</strong></div>
        <button type="button" :disabled="saleLocked(sale)" @click="removeSale(sale,index)">移除</button>
        <p v-if="sale.editable===false" class="stock-hint">本行已交票，请先作废所属交票批次后再修改；其他未交票销售行仍可编辑。</p>
        <p class="stock-hint">扣减 {{baseQuantity(sale)}} {{product(sale).unit}}；此位置当前可用 {{available(sale)}} {{product(sale).unit}}</p>
      </article>
      <p v-if="!draft.sales?.length" class="empty">本次没有单独销售材料。</p>
      <div class="sales-footer"><strong>本次材料货款：{{money(total)}}{{hasCustomerPrice(total)?' 元':''}}</strong><label v-if="canWages&&draft.sales?.length">本次带货总工资（元）<input :value="Number(draft.delivery_wage_cents||0)/100" type="number" min="0" max="1000000" step="0.01" @input="draft.delivery_wage_cents=Math.round(Number(($event.target as HTMLInputElement).value)*100)"><small>本次合计，可填 20、30 等；不随桶数或袋数倍增。</small></label></div>
    </fieldset>
  </section>
</template>
<style scoped>
.sales-editor{min-width:0;border-top:1px solid #dbe4ef;padding-top:12px}.sale-row{display:grid;grid-template-columns:minmax(180px,1.5fr) minmax(90px,100px) minmax(90px,100px) minmax(150px,1.2fr) minmax(140px,150px) 110px auto;align-items:end;gap:10px;background:#f8fafc;padding:12px;margin-top:8px;border:1px solid #dbe4ef;border-radius:6px}.sale-row label{min-width:0}.sale-row input,.sale-row select{width:100%;min-width:0}.sale-amount{display:grid;gap:6px}.stock-hint{grid-column:1/-1;color:#687c96;font-size:12px;margin:0}.stock-warning{color:#b35214;font-size:13px;margin:6px 0}.sales-footer{display:flex;align-items:center;gap:18px;flex-wrap:wrap;padding:15px 0}.sales-footer small{display:block;color:#687c96}.sales-footer label{min-width:180px}.hint{margin:0 0 10px}@media(max-width:1100px){.sale-row{grid-template-columns:repeat(3,minmax(0,1fr))}.sale-row>:first-child{grid-column:span 2}}@media(max-width:550px){.sale-row{grid-template-columns:repeat(2,minmax(0,1fr))}.sale-row>:first-child{grid-column:1/-1}.sales-footer>label{width:100%}.sales-footer input{max-width:100%}}
</style>
