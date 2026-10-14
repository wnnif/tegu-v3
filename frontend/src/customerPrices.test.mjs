import {test} from 'node:test'
import assert from 'node:assert/strict'
import {CUSTOMER_PRICE_HIDDEN,customerMoney,customerPrice,hasCustomerPrice,withoutCustomerPrices} from './customerPrices.ts'
import {canAccess,canFillCraft,pageAllowed} from './sessionAccess.ts'

test('客户缺价不是0：合法零金额显示0，省略或无权均明确隐藏',()=>{
 for(const value of [undefined,null,'', ' ',NaN,Infinity,false]){
  assert.equal(hasCustomerPrice(value),false)
  assert.equal(customerMoney(value),CUSTOMER_PRICE_HIDDEN)
  assert.equal(customerPrice(value),CUSTOMER_PRICE_HIDDEN)
 }
 for(const value of [0,'0']){
  assert.equal(hasCustomerPrice(value),true)
  assert.equal(customerPrice(value),'0.00')
  assert.equal(customerMoney(value),'¥ 0.00')
 }
 assert.equal(customerPrice(1250),'12.50')
 assert.equal(customerMoney(1250,false),CUSTOMER_PRICE_HIDDEN)
})

test('隐藏金额payload复制递归去客户money，不改草稿或工资与普通施工字段',()=>{
 const input={price_cents:100,total_cents:200,received_cents:50,outstanding_cents:150,wage_cents:30,delivery_wage_cents:20,
  lines:[{id:'old',craft_id:'fixed',worker_id:'worker',actual_location:'一层主卫',description:'保留备注',qty_micro:6000000,price_cents:10,total_cents:60,labor_cents:30,wage_cents:5,segments:[{mode:'平方',length:2,width:3,surface:'地面',amount_cents:60}]}],
  sales:[{id:'sale',product_id:'product',qty_micro:2000000,price_cents:100,total_cents:200,unit:'桶'}],fixed_crafts:[{id:'fixed',price_cents:100,included_by_craft_id:'parent',wage_cents:30}]}
 const before=structuredClone(input),output=withoutCustomerPrices(input)
 const check=value=>{if(value&&typeof value==='object')for(const [key,item]of Object.entries(value)){assert.equal(['price_cents','total_cents','amount_cents','received_cents','outstanding_cents'].includes(key),false);check(item)}}
 check(output)
 assert.deepEqual(input,before);assert.notEqual(output,input)
 assert.equal(output.wage_cents,30);assert.equal(output.delivery_wage_cents,20)
 assert.equal(output.lines[0].labor_cents,30);assert.equal(output.lines[0].worker_id,'worker');assert.equal(output.lines[0].actual_location,'一层主卫')
 assert.equal(output.lines[0].segments[0].length,2);assert.equal(output.sales[0].qty_micro,2000000)
 assert.equal(output.fixed_crafts[0].included_by_craft_id,'parent')
})

test('customer_prices独立键：员工需明确授权，不授模块、工艺、工资或财务',()=>{
 const base={id:'employee',role:'employee',permissions:[],crafts:[]}
 assert.equal(canAccess(base,'customer_prices'),false)
 const granted={...base,permissions:['customer_prices']}
 assert.equal(canAccess(granted,'customer_prices'),true)
 for(const key of ['orders','orders_temporary','customers','rates','warehouse','wages','finance'])assert.equal(canAccess(granted,key),false)
 for(const page of ['orders','companies','projects','crafts','wages','finance','archives'])assert.equal(pageAllowed(granted,page),false)
 assert.equal(canFillCraft(granted,'fixed'),false)
 assert.equal(canAccess({...base,role:'admin'},'customer_prices'),true)
 assert.equal(canAccess({...base,role:'operator'},'customer_prices'),true)
})
