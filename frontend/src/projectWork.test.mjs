import {test} from 'node:test'
import assert from 'node:assert/strict'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
registerHooks({resolve(specifier,context,nextResolve){
  if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')){
    const candidate=new URL(specifier,context.parentURL)
    if(existsSync(fileURLToPath(candidate)+'.ts'))return nextResolve(specifier+'.ts',context)
  }
  return nextResolve(specifier,context)
}})
const {ticketPrintOrder,ticketLabel}=await import('./projectWork.ts')
const {wageRecordKey,printWorkerNames}=await import('./printOrder.ts')

const makeOrder=(number,date,worker)=>({number,work_date:date,worker_name:worker,company_id:'c',company_name:'合成公司',worker_id:worker,address:'合成地址',status:'进行中',lines:[],materials:[],sales:[]})
test('工地票汇总跨日期人员及售料，带货工资不随数量倍增',()=>{
  const first={...makeOrder('A','2026-01-01','甲'),wage_cents:3000,delivery_wage_cents:3000,sales:[{product_name:'合成材料',qty_micro:2000000,sale_qty_micro:2000000,unit:'桶',price_cents:20000,total_cents:40000}]}
  const second={...makeOrder('B','2026-03-01','乙'),wage_cents:6000,delivery_wage_cents:0,lines:[{id:'l',category:'维修',name:'合成按个项',mode:'个',qty_micro:2000000,price_cents:10000,total_cents:20000,wage_cents:3000,labor_cents:6000}]}
  const result=ticketPrintOrder({ticket:{number:'P-1',status:'草稿',total_cents:60000},orders:[first,second],versions:[]})
  assert.equal(result.number,'P-1')
  assert.equal(result.total_cents,60000)
  assert.equal(result.sales.length,1)
  assert.equal(result.delivery_wage_cents,3000)
  assert.equal(result.wage_cents,9000)
  assert.equal(result.worker_name,'甲、乙')
  assert.equal(result.lines[0].work_date,'2026-03-01')
})
test('没有工资权限的票据聚合不会生成工资字段',()=>{
  const result=ticketPrintOrder({ticket:{number:'P-1',status:'草稿',total_cents:0},orders:[makeOrder('A','2026-01-01','甲')],versions:[]})
  assert.equal(Object.hasOwn(result,'wage_cents'),false)
  assert.equal(Object.hasOwn(result,'delivery_wage_cents'),false)
  assert.equal(Object.hasOwn(result,'wage_records'),false)
})
test('票面公司和工地使用票据快照，后续施工资料不能替换原票抬头',()=>{
  const result=ticketPrintOrder({ticket:{number:'P-1',status:'草稿',total_cents:0,company_id:'original-company',company_name:'原票公司',project_name:'原票工地',address:'原票地址'},orders:[{...makeOrder('A','2026-01-01','甲'),company_id:'new-company',company_name:'后续公司',project_name:'后续工地',address:'后续地址'}],versions:[]})
  assert.equal(result.company_id,'original-company')
  assert.equal(result.company_name,'原票公司')
  assert.equal(result.project_name,'原票工地')
  assert.equal(result.address,'原票地址')
})
test('房型套餐作为票据独立收费明细进入打印数据且不改套餐外施工明细',()=>{
  const result=ticketPrintOrder({ticket:{number:'P-2',status:'草稿',total_cents:168000,package_charge:{id:'pkg',name:'墙地固普通工艺',room_count:3,amount_cents:120000}},orders:[{...makeOrder('A','2026-01-01','甲'),total_cents:48000,lines:[{id:'l',category:'开工',name:'套餐外施工',mode:'平方',qty_micro:1000000,price_cents:48000,total_cents:48000}]}],versions:[]})
  assert.deepEqual(result.package_charge,{name:'墙地固普通工艺',room_count:3,amount_cents:120000})
  assert.equal(result.total_cents,168000)
  assert.equal(result.lines[0].name,'套餐外施工')
})
test('同单两个工人保留小项姓名快照和各自工资，带货工资只归送货人一笔',()=>{
  const order={...makeOrder('A','2026-10-04','材料人'),id:'order',worker_id:'delivery',wage_cents:3900,delivery_wage_cents:500,
    lines:[{id:'water',category:'防水涂料',name:'防水',worker_id:'a',worker_name:'原名甲',region:'主卫',mode:'平方',qty_micro:6000000,price_cents:2000,total_cents:12000,labor_cents:1800},
      {id:'fiber',category:'丙纶',name:'丙纶',worker_id:'b',worker_name:'原名乙',region:'厨房',mode:'平方',qty_micro:8000000,price_cents:0,total_cents:0,labor_cents:1600}],
    wage_records:[{id:'wa',order_id:'order',worker_id:'a',worker_name:'原名甲',number:'A',work_date:'2026-10-04',wage_cents:1800,delivery_wage_cents:0,wage_paid:1},
      {id:'wb',order_id:'order',worker_id:'b',worker_name:'原名乙',number:'A',work_date:'2026-10-04',wage_cents:1600,delivery_wage_cents:0,wage_paid:0},
      {id:'wd',order_id:'order',worker_id:'delivery',worker_name:'材料人',number:'A',work_date:'2026-10-04',wage_cents:500,delivery_wage_cents:500,wage_paid:0}]}
  const result=ticketPrintOrder({ticket:{number:'P',status:'草稿',total_cents:12000},orders:[order],versions:[]})
  assert.deepEqual(result.lines.map(line=>line.worker_name),['原名甲','原名乙'])
  assert.deepEqual(result.wage_records.map(wageRecordKey),['wa','wb','wd'])
  assert.equal(result.wage_cents,3900);assert.equal(result.delivery_wage_cents,500)
  assert.equal(printWorkerNames(result),'原名甲、原名乙、材料人')
  assert.equal(result.lines[1].total_cents,0)
})
test('历史打印缺行工人回退保存的单头名字，明确另一工人不误显示单头',()=>{
  const order={...makeOrder('A','2026-01-01','历史师傅'),worker_id:'old',lines:[{id:'legacy',category:'开工',name:'旧工艺',mode:'金额',qty_micro:1000000,total_cents:1000,price_cents:1000},
    {id:'other',worker_id:'another',category:'维修',name:'另一个人',mode:'金额',qty_micro:1000000,total_cents:1000,price_cents:1000}]}
  const result=ticketPrintOrder({ticket:{number:'P',status:'草稿',total_cents:2000},orders:[order],versions:[]})
  assert.equal(result.lines[0].worker_name,'历史师傅')
  assert.equal(result.lines[1].worker_name,'')
})

test('票名只按工地交票快照，统一、分票、追加和累计互不混淆',()=>{
   assert.equal(ticketLabel({kind:'normal',slot:1,ticket_mode_snapshot:'single'}),'统一票')
   assert.equal(ticketLabel({kind:'normal',slot:1,ticket_mode_snapshot:'split'}),'第一票')
   assert.equal(ticketLabel({kind:'normal',slot:2,ticket_mode_snapshot:'split'}),'第二票')
   assert.equal(ticketLabel({kind:'append',ticket_mode_snapshot:'single'}),'追加票')
   assert.equal(ticketLabel({kind:'summary',ticket_mode_snapshot:'split'}),'全工地累计')
   assert.equal(ticketLabel({kind:'normal',slot:2}),'正常票')
})
