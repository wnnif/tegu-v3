<script setup lang="ts">
import {computed,reactive,ref,watch} from 'vue'
import {api} from '../api'
import { customerPrice, hasCustomerPrice, CUSTOMER_PRICE_HIDDEN } from '../customerPrices'
import ReferenceSelect from './ReferenceSelect.vue'
import type {Sale} from '../projectWork'
import type {CatalogRow,OrderDraft,References,Balance} from '../workflows/models'
import {saleAmountCents} from '../orderSegments'
import {fromQuantum,formatQuantum,toQuantum,unitFactors} from '../stockQuantity'
const props=defineProps<{draft:OrderDraft;references:References;inventory:Balance[];warehouse:string;disabled:boolean;canWarehouse:boolean;canWages:boolean;canCustomerPrices:boolean}>()
const emit=defineEmits<{add:[];product:[item:CatalogRow|null];source:[]}>()
const warning=ref('')
type CompanyPrice={input_unit:'base'|'aux';price_cents:number}
type PriceState={companyId:string;productId:string;loading:boolean;error:string;prices:CompanyPrice[]}
const priceStates=reactive(new Map<string,PriceState>())
const saleKeys=new WeakMap<object,string>()
const requests=new Map<string,number>()
let nextSaleKey=0
function saleKey(sale:Sale){let key=saleKeys.get(sale);if(!key){key='sale-'+(++nextSaleKey);saleKeys.set(sale,key)}return key}
function priceState(sale:Sale){const state=priceStates.get(saleKey(sale));return state?.companyId===props.draft.company_id&&state.productId===sale.product_id?state:undefined}
function configuredPrice(sale:Sale,inputUnit=sale.input_unit||'base'){return priceState(sale)?.prices.find(price=>price.input_unit===inputUnit)}
function hasCompanyPrices(sale:Sale){return !!priceState(sale)?.prices.length}
function priceUnitMissing(sale:Sale){return hasCompanyPrices(sale)&&!configuredPrice(sale)}
function priceFixed(sale:Sale){return !!sale.company_price_fixed||!!configuredPrice(sale)}
function canEditPrice(sale:Sale){const state=priceState(sale);return props.canCustomerPrices&&!saleLocked(sale)&&!priceFixed(sale)&&!priceUnitMissing(sale)&&!state?.loading&&!state?.error}
async function loadCompanyPrices(sale:Sale,productId=sale.product_id){
 const key=saleKey(sale),companyId=props.draft.company_id
 if(!companyId||!productId||!props.canCustomerPrices)return undefined
 const request=(requests.get(key)||0)+1;requests.set(key,request)
 priceStates.set(key,{companyId,productId,loading:true,error:'',prices:[]})
 try{
  const result=await api<{company_id:string;product_id:string;prices:CompanyPrice[]}>('/company-product-prices?'+new URLSearchParams({company_id:companyId,product_id:productId}))
  if(requests.get(key)!==request||sale.product_id!==productId||props.draft.company_id!==companyId)return undefined
  const prices=result.company_id===companyId&&result.product_id===productId?result.prices||[]:[]
  priceStates.set(key,{companyId,productId,loading:false,error:'',prices})
  return prices
 }catch(e){
  if(requests.get(key)!==request||sale.product_id!==productId||props.draft.company_id!==companyId)return undefined
  priceStates.set(key,{companyId,productId,loading:false,error:(e as Error).message,prices:[]})
  return undefined
 }
}
function setConfiguredPrice(sale:Sale,inputUnit=sale.input_unit||'base'){
 const price=configuredPrice(sale,inputUnit)
 if(price){sale.price_cents=price.price_cents;sale.company_price_fixed=true;return true}
 if(hasCompanyPrices(sale)){delete sale.price_cents;sale.company_price_fixed=false;warning.value='该公司商品未配置所选单位的固定价格，请切换到已报价单位。';return false}
 sale.company_price_fixed=false
 return true
}
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
 const state=priceState(sale)
 if(state?.prices.length){
  if(setConfiguredPrice(sale,selected))warning.value='单位已切换，请核对按新单位填写的销售单价。'
 }else if(sale.product_id){
  const productId=sale.product_id
  warning.value='单位已切换，请核对按新单位填写的销售单价。'
  void loadCompanyPrices(sale,productId).then(()=>{if(sale.product_id===productId&&sale.input_unit===selected)setConfiguredPrice(sale,selected)})
 }else warning.value='单位已切换，请核对按新单位填写的销售单价。'
}
function productSelected(sale:Sale,item:CatalogRow|null){
 sale.company_price_fixed=false
 sale.price_cents=0
 warning.value=''
 emit('product',item)
 if(item){void loadCompanyPrices(sale,item.id).then(()=>{if(sale.product_id===item.id)setConfiguredPrice(sale)})}
 else{priceStates.delete(saleKey(sale))}
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
function setPrice(sale:Sale,event:Event){if(!canEditPrice(sale))return;sale.price_cents=Math.round(Number((event.target as HTMLInputElement).value)*100)}
function removeSale(sale:Sale,index:number){if(!saleLocked(sale))props.draft.sales?.splice(index,1)}
watch(()=>props.draft.company_id,()=>{priceStates.clear();requests.clear()})
</script>
<template>
  <section class="sales-editor">
    <div class="toolbar"><h3>商品销售</h3><button type="button" :disabled="disabled||(!warehouse&&!canWarehouse)||!canCustomerPrices" @click="canCustomerPrices&&emit('add')">＋ 添加销售材料</button></div>
    <p class="hint">单独销售货款计公司应收；本次带货工资单独填写。</p>
    <p v-if="!canCustomerPrices" class="hint">新增销售材料或调整计价单位需要“查看客户价格”权限；已有售料可修改数量，价格保留。</p>
    <p v-if="warning" class="stock-warning" role="alert">{{warning}}</p>
    <fieldset :disabled="disabled">
      <article v-for="(sale,index) in draft.sales||[]" :key="sale.id||index" class="sale-row">
        <ReferenceSelect v-model="sale.product_id" entity="products" label="销售商品" empty-label="请选择商品" search-placeholder="搜索商品" :selected-name="sale.product_name" :company-id="draft.company_id" active="1" :disabled="saleLocked(sale)||!!sale.id" @change="sale.qty_micro=0;sale.qty_quantum='';sale.input_qty='';sale.input_unit='base';productSelected(sale,$event)" />
        <label>销售数量<input :value="quantity(sale)" type="number" min="0.000001" max="1000000" step="0.000001" :disabled="saleLocked(sale)" @input="setQuantity(sale,($event.target as HTMLInputElement).value)"></label>
        <label>销售单位<select :value="sale.input_unit||'base'" :disabled="saleLocked(sale)||!canCustomerPrices" @change="changeUnit(sale,$event)"><option value="base">{{product(sale).unit||'基本单位'}}</option><option v-if="product(sale).aux_unit" value="aux">{{product(sale).aux_unit}}</option></select></label>
        <ReferenceSelect v-if="canWarehouse" v-model="sale.warehouse_id" entity="warehouses" label="实际发货位置" empty-label="请选择来源" search-placeholder="搜索总仓或工人结余" :selected-name="sale.warehouse_name" active="1" :disabled="saleLocked(sale)" required @change="emit('source')" /><div v-else>发货位置 {{sale.warehouse_name||references.warehouses.find(item=>item.id===(sale.warehouse_id||warehouse))?.name||'未选择'}}</div>
        <label>{{priceFixed(sale)?'公司固定单价':'销售单价'}}（元/{{sale.input_unit==='aux'?product(sale).aux_unit:product(sale).unit||'单位'}}）<span v-if="!priceVisible(sale)">{{CUSTOMER_PRICE_HIDDEN}}</span><span v-else-if="priceState(sale)?.loading">正在读取公司价格</span><span v-else-if="priceState(sale)?.error">公司价格读取失败</span><span v-else-if="priceUnitMissing(sale)">该单位未配置固定价格</span><input v-else-if="priceVisible(sale)" :value="Number(sale.price_cents)/100" type="number" min="0" max="1000000" step="0.01" :disabled="!canEditPrice(sale)" @input="setPrice(sale,$event)"></label>
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
