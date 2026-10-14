import {test} from 'node:test'
import assert from 'node:assert/strict'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {ref} from 'vue'

registerHooks({resolve(specifier,context,nextResolve){
 if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')){
  const candidate=new URL(specifier,context.parentURL)
  if(existsSync(fileURLToPath(candidate)+'.ts'))return nextResolve(specifier+'.ts',context)
 }
 return nextResolve(specifier,context)
}})
const {createOrderLocation,orderLineKey,canAddOrderLocation,canRemoveOrderLocation}=await import('./orderLocations.ts')
const {useOrders}=await import('./workflows/useOrders.ts')
const {command}=await import('./api.ts')
const {lineWorkerId}=await import('./orderWorkers.ts')
const {filled,lineAmount,lineQuantityMicro}=await import('./orderSegments.ts')
const copy=value=>JSON.parse(JSON.stringify(value))
const person=(id,crafts=['防水涂料'])=>({id,name:'合成工人'+id,version:1,crafts,active:1})
const fixed={id:'fixed',company_id:'company',version:1,category:'防水涂料',name:'合成防水',spec:'配置规格',mode:'平方',price_cents:2000,wage_cents:300,editable:true,subitems:[{id:'floor',name:'地面防水'},{id:'wall',name:'墙面防水'}]}
const unconfigured={...fixed,subitems:[]}
const area=(length=2,width=3,surface='地面')=>({mode:'平方',length,width,height:0,count:0,surface,amount_cents:0})
const identity={id:'',craft_id:fixed.id,category:fixed.category,name:fixed.name,spec:fixed.spec,subitem_id:'floor',subitem_name:'地面防水',mode:'平方',price_cents:fixed.price_cents,wage_cents:fixed.wage_cents,editable:true,temporary:false}
const storedLine=(id,values={})=>({...identity,id,region:'',description:'',worker_id:'a',worker_name:'历史工人a',entered:true,segments:[area()],...values})
const storedOrder=lines=>({id:'saved',version:3,number:'合成单号',company_id:'company',company_name:'合成公司',worker_id:'',project_id:'',address:'合成施工地址',work_date:'2026-10-10',status:'进行中',wage_paid:0,materials:[],sales:[],lines})
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
function harness(t,options={}){
 const requests=[],busy=ref(false)
 t.mock.method(globalThis,'fetch',async(path,init)=>{
  requests.push({path,init})
  const alternative=options.fetch?.(path,init);if(alternative)return alternative
  const url=new URL(path,'http://synthetic.local')
  if(url.pathname==='/api/reference')return response({crafts:options.crafts||[fixed]})
  if(url.pathname==='/api/company-packages')return response({rows:[]})
  if(url.pathname==='/api/projects')return response({rows:[],total:0,page:1})
  if(url.pathname==='/api/inventory')return response({rows:[],total:0})
  if(url.pathname==='/api/reference-options'){
   const entity=url.searchParams.get('entity'),id=url.searchParams.get('id')||''
   const rows=entity==='companies'?[{id:id||'company',name:'合成公司',version:1}]:entity==='workers'?[person(id)]:[]
   return response({rows,total:rows.length})
  }
  if(url.pathname==='/api/order'&&init.method==='GET'&&options.saved)return response(typeof options.saved==='function'?options.saved():options.saved)
  if(url.pathname==='/api/order'&&init.method==='POST')return response({id:'saved',version:1})
  throw new Error('Unexpected synthetic request '+path)
 })
 const ctx={references:ref({companies:[],workers:[],warehouses:[],products:[],crafts:[]}),inventory:ref([]),categories:ref(['防水涂料']),company:ref('company'),dialog:ref(''),error:ref(''),notice:ref(''),can:options.can||(()=>true),submit:command(),load:async()=>{},perform:async(fn)=>{if(busy.value)return;busy.value=true;ctx.error.value='';try{await fn()}catch(e){ctx.error.value=e.message}finally{busy.value=false}}}
 const workflow=useOrders(ctx,async()=>{})
 return {ctx,busy,requests,...workflow,get draft(){return workflow.orderDraft.value},posts:()=>requests.filter(({init})=>init.method==='POST')}
}
async function fresh(t,options){
 const state=harness(t,options);await state.openOrder();assert.equal(state.ctx.error.value,'')
 state.draft.address='合成独立部位施工地址'
 state.draft.category_workers['防水涂料']='a';state.categoryWorkerChanged('防水涂料',person('a'))
 return state
}
const payload=state=>JSON.parse(state.posts().at(-1).init.body)

test('新增部位仅复制工艺和小项身份，各计量模式均不复制保存ID、尺寸、备注、旧金额或显式人员',()=>{
 for(const mode of ['平方','立方','米','个','金额']){
  const source={...identity,id:'old-line',client_key:'old-key',mode,subitem_name:'配置名称快照',region:'旧文本',actual_location:'旧实际部位',description:'旧备注',entered:true,worker_id:'b',worker_name:'历史工人b',worker_override:true,qty_micro:6000000,total_cents:12000,labor_cents:1800,segments:[{...area(),mode,count:7,height:2,amount_cents:1234}]}
  const before=copy(source),line=createOrderLocation(source,9)
  assert.deepEqual([line.craft_id,line.subitem_id,line.subitem_name,line.category,line.name,line.spec,line.mode],['fixed','floor','配置名称快照','防水涂料','合成防水','配置规格',mode])
  assert.deepEqual([line.id,line.region,line.actual_location,line.description,line.worker_id,line.worker_name],['','','','','',''])
  assert.equal(line.worker_override,false);assert.equal(line.entered,false);assert.equal(line.sort_order,9)
  assert.equal(line.price_cents,2000);assert.equal(line.wage_cents,300)
  assert.notEqual(line.client_key,source.client_key);assert.notEqual(line.segments,source.segments)
  assert.deepEqual(line.segments,[{mode,surface:'地面',length:'',width:'',height:'',count:'',amount_cents:0}])
  for(const field of ['qty_micro','total_cents','labor_cents'])assert.equal(Object.hasOwn(line,field),false)
  assert.equal(filled(line),false);assert.equal(lineAmount(line),0);assert.equal(lineQuantityMicro(line),0n)
  line.segments[0].length=7;assert.deepEqual(source,before)
 }
})

test('同一小项各部位使用稳定独立key，删除检查保留最后固定行且拒绝未知或不同小项行',()=>{
 const first=createOrderLocation(identity,0),second=createOrderLocation(identity,1),other=createOrderLocation({...identity,subitem_id:'wall'},2)
 const keys=[first,second,other].map(orderLineKey)
 assert.equal(new Set(keys).size,3);assert.deepEqual([first,second,other].map(orderLineKey),keys)
 assert.equal(canRemoveOrderLocation([first,second,other],first),true)
 assert.equal(canRemoveOrderLocation([first,other],first),false)
 assert.equal(canRemoveOrderLocation([first,second],{...first}),false)
 const temporary=createOrderLocation({...identity,craft_id:'',subitem_id:'',temporary:true},3)
 assert.equal(canRemoveOrderLocation([temporary],temporary),true)
})

test('真实useOrders新固定行和临时行region为空，配置名称仅写入subitem_name且施工面不代填实际部位',async(t)=>{
 const state=await fresh(t)
 assert.deepEqual(state.draft.lines.map(line=>[line.subitem_id,line.subitem_name,line.region,line.actual_location]),[['floor','地面防水','',''],['wall','墙面防水','','']])
 state.addTemporary();const temporary=state.draft.lines.at(-1)
 assert.equal(temporary.temporary,true);assert.equal(temporary.region,'');assert.equal(temporary.actual_location,'');assert.equal(temporary.subitem_name,'')
 assert.equal(temporary.segments[0].surface,'地面');assert.equal(new Set(state.draft.lines.map(orderLineKey)).size,3)
})

test('无配置小项新增部位保留原历史行并跟随大类默认人，显式人员和尺寸互不串行',async(t)=>{
 const state=await fresh(t,{crafts:[unconfigured]}),original=state.draft.lines[0]
 Object.assign(original,{id:'old-line',sort_order:12,subitem_name:'',actual_location:'一层卫浴',region:'  原region  ',description:'原行备注',entered:true,qty_micro:6000000,total_cents:12000,labor_cents:1800,segments:[area()]})
 state.lineWorkerChanged(original,person('b'));const before=copy(original)
 const added=state.addLocation(original)
 assert.ok(added);assert.deepEqual(original,before)
 assert.deepEqual([added.id,added.subitem_id,added.subitem_name,added.region,added.actual_location,added.description],['','','','','',''])
 assert.equal(added.sort_order,13);assert.equal(added.worker_id,'');assert.equal(added.worker_override,false);assert.equal(lineWorkerId(added,state.draft.category_workers[added.category]),'a')
 assert.equal(filled(added),false);assert.equal(lineAmount(added),0)
 state.draft.category_workers['防水涂料']='c';state.categoryWorkerChanged('防水涂料',person('c'))
 assert.equal(lineWorkerId(original,'c'),'b');assert.equal(lineWorkerId(added,'c'),'c')
 added.actual_location='二层卫浴';added.description='二层独立备注';added.segments=[area(1,4)]
 state.lineWorkerChanged(added,person('a'));assert.equal(lineWorkerId(added,'c'),'a')
 assert.deepEqual(original,before);assert.equal(lineQuantityMicro(original),6000000n);assert.equal(lineQuantityMicro(added),4000000n)
 const third=state.addLocation(added);assert.equal(third.actual_location,'');assert.equal(third.description,'');assert.equal(filled(third),false);assert.equal(lineWorkerId(third,'c'),'c')
 assert.equal(new Set([original,added,third].map(orderLineKey)).size,3)
})

test('同小项多部位POST和GET重开不去重，所有尺寸段、备注、顺序及人员独立恢复后可继续编辑',async(t)=>{
 let saved=storedOrder([
  storedLine('saved-line-0',{sort_order:0,actual_location:'一层东卫',description:'首行备注',segments:[area()]}),
  storedLine('saved-line-1',{sort_order:2,actual_location:'二层东卫',description:'次行备注',worker_id:'b',worker_name:'历史工人b',segments:[area(1,4,'墙面'),area(1.5,2,'其他')]})
 ])
 const state=harness(t,{saved:()=>saved});await state.openOrder({id:'saved'});assert.equal(state.ctx.error.value,'')
 const [first,second]=state.draft.lines;const added=state.addLocation(first);assert.ok(added,'同一配置部位可以继续添加独立空明细');assert.equal(added.subitem_id,first.subitem_id);assert.equal(added.entered,false)
 await state.saveOrder();assert.equal(state.ctx.error.value,'')
 const sent=payload(state)
 assert.equal(sent.lines.length,2);assert.deepEqual(sent.lines.map(line=>[line.subitem_id,line.subitem_name,line.actual_location,line.region,line.worker_id]),[['floor','地面防水','一层东卫','','a'],['floor','地面防水','二层东卫','','b']])
 assert.deepEqual(sent.lines.map(line=>line.sort_order),[0,2]);assert.equal(sent.lines[1].segments.length,2)
 assert.ok(sent.lines.every(line=>!Object.hasOwn(line,'client_key')&&!Object.hasOwn(line,'worker_override')))
 saved={...sent,id:'saved',version:1,lines:sent.lines.map((line,index)=>({...line,id:'saved-line-'+index,worker_name:'历史工人'+line.worker_id}))}
 await state.openOrder({id:'saved'});assert.equal(state.ctx.error.value,'')
 const restored=state.draft.lines.filter(line=>line.subitem_id==='floor')
 assert.equal(restored.length,2);assert.deepEqual(restored.map(line=>[line.id,line.actual_location,line.description,line.region]),[['saved-line-0','一层东卫','首行备注',''],['saved-line-1','二层东卫','次行备注','']])
 assert.deepEqual(restored.map(line=>line.segments),sent.lines.map(line=>line.segments));assert.deepEqual(restored.map(line=>lineWorkerId(line,state.draft.category_workers[line.category])),['a','b'])
 assert.equal(new Set(restored.map(orderLineKey)).size,2);assert.equal(state.draft.lines.filter(line=>line.subitem_id==='wall').length,1)
 const firstSnapshot=copy(restored[0]);restored[1].actual_location='二层东卫更改';restored[1].segments[1].length=2
 await state.saveOrder();assert.equal(state.ctx.error.value,'')
 assert.deepEqual(restored[0],firstSnapshot);assert.deepEqual(payload(state).lines.map(line=>[line.id,line.actual_location,line.segments.length]),[['saved-line-0','一层东卫',1],['saved-line-1','二层东卫更改',2]])
})

test('同小项即使实际部位都留空仍是独立行，不按部位文本或施工面合并',async(t)=>{
 const state=harness(t,{saved:storedOrder([
  storedLine('first',{actual_location:'',segments:[area(2,3,'地面')]}),
  storedLine('second',{actual_location:'',segments:[area(1,4,'墙面')]})
 ])});await state.openOrder({id:'saved'});assert.equal(state.ctx.error.value,'')
 await state.saveOrder();assert.equal(state.ctx.error.value,'')
 assert.deepEqual(payload(state).lines.map(line=>[line.subitem_id,line.actual_location,line.region,line.segments[0].surface]),[['floor','','','地面'],['floor','','','墙面']])
})

test('重开缺失或空新字段的历史同小项行不猜值，region逐字保留且不能通过新增覆盖旧名称',async(t)=>{
 const legacy=storedLine('legacy',{region:'  原文 / 主卫  ',description:'旧备注',sort_order:0}),empty=storedLine('empty',{region:'厨房·旧兼容文本',subitem_name:'',actual_location:'',worker_id:'b',sort_order:1,segments:[area(1,4,'墙面')]})
 delete legacy.subitem_name;delete legacy.actual_location
 const state=harness(t,{saved:storedOrder([legacy,empty]),crafts:[{...fixed,subitems:[{id:'floor',name:'后来改名的新配置'},{id:'wall',name:'墙面防水'}]}]})
 await state.openOrder({id:'saved'});assert.equal(state.ctx.error.value,'')
 const oldRows=state.draft.lines.filter(line=>line.subitem_id==='floor')
 assert.equal(oldRows.length,2);assert.deepEqual(oldRows.map(line=>[line.region,line.subitem_name,line.actual_location]),[['  原文 / 主卫  ','',''],['厨房·旧兼容文本','','']])
 assert.deepEqual(oldRows.map(line=>line.segments),[legacy.segments,empty.segments])
 await state.saveOrder();assert.equal(state.ctx.error.value,'')
 assert.deepEqual(payload(state).lines.map(line=>[line.id,line.region,line.subitem_name,line.actual_location]),[['legacy','  原文 / 主卫  ','',''],['empty','厨房·旧兼容文本','','']])
 const before=copy(oldRows[0]);const added=state.addLocation(oldRows[0]);assert.ok(added);assert.equal(added.subitem_name,'');assert.deepEqual(oldRows[0],before)
 assert.equal(state.draft.lines.find(line=>line.subitem_id==='wall').subitem_name,'墙面防水','新配置缺行按来源生成，历史行名称不追填')
})

test('真实移除只删除指定部位，保留最后固定行；陈旧对象不会splice(-1)误删，临时行可完整移除',async(t)=>{
 const state=harness(t,{saved:storedOrder([
  storedLine('first',{sort_order:0}),storedLine('wall',{subitem_id:'wall',subitem_name:'墙面防水',sort_order:1}),
  storedLine('second',{sort_order:2}),storedLine('third',{sort_order:3})
 ])});await state.openOrder({id:'saved'});assert.equal(state.ctx.error.value,'')
 const [first,wall,second,third]=state.draft.lines
 const firstKey=orderLineKey(first),thirdKey=orderLineKey(third)
 assert.equal(state.removeLocation(second),true);assert.equal(state.removeLocation(second),false);assert.ok(state.draft.lines.includes(third))
 assert.equal(state.removeLocation({...third}),false);assert.equal(state.removeLocation(first),true)
 assert.equal(orderLineKey(third),thirdKey);assert.notEqual(thirdKey,firstKey);assert.equal(state.removeLocation(third),false);assert.equal(state.removeLocation(wall),false)
 await state.workerChanged();assert.equal(state.ctx.error.value,'');assert.equal(state.draft.lines.filter(line=>line.subitem_id==='floor').length,1)
 state.addTemporary();const temporary=state.draft.lines.at(-1);assert.equal(state.removeLocation(temporary),true);assert.ok(state.draft.lines.includes(third))
})

test('真实增删遵守账号只读、订单锁定、已发工资、工人技能与停用保护',async(t)=>{
 let allowOrders=true
 const state=await fresh(t,{crafts:[unconfigured],can:permission=>permission!=='orders'||allowOrders}),first=state.draft.lines[0],second=state.addLocation(first)
 function blocked(){const before=copy(state.draft.lines);assert.equal(state.addLocation(first),undefined);assert.equal(state.removeLocation(second),false);assert.deepEqual(state.draft.lines,before)}
 first.editable=false;second.editable=false;blocked();first.editable=true;second.editable=true
 state.draft.editable=false;blocked();state.draft.editable=true
 state.draft.status='已取消';blocked();state.draft.status='进行中'
 state.draft.wage_paid=1;blocked();state.draft.wage_paid=0
 allowOrders=false;blocked();allowOrders=true
 state.draft.category_workers['防水涂料']='bad';state.categoryWorkerChanged('防水涂料',person('bad',['开工']));blocked()
 state.draft.category_workers['防水涂料']='inactive';state.categoryWorkerChanged('防水涂料',{...person('inactive'),active:0});blocked()
 state.draft.category_workers['防水涂料']='a';state.ctx.references.value.crafts[0].editable=false;blocked()
})

test('历史可编辑行没有当前新行授权时不允许复制，临时项许可关闭后不能增删临时部位',async(t)=>{
 let allowTemporary=true
 const state=await fresh(t,{crafts:[unconfigured],can:permission=>permission!=='orders_temporary'||allowTemporary}),first=state.draft.lines[0]
 first.id='saved-row';first.editable=true;state.ctx.references.value.crafts=[]
 assert.equal(state.addLocation(first),undefined)
 state.addTemporary();const temporary=state.draft.lines.at(-1)
 assert.equal(temporary.temporary,true);allowTemporary=false
 assert.equal(state.addLocation(temporary),undefined);assert.equal(state.removeLocation(temporary),false)
})

test('实际部位按Unicode字符校验，200个补充字符可保存，201个拒绝且完整保留草稿',async(t)=>{
 const state=await fresh(t),line=state.draft.lines[0]
 line.segments=[area()];line.actual_location='😀'.repeat(200)
 await state.saveOrder();assert.equal(state.ctx.error.value,'');assert.equal(payload(state).lines[0].actual_location,line.actual_location);assert.equal(Array.from(payload(state).lines[0].actual_location).length,200)
 state.ctx.dialog.value='order';line.actual_location+='😀';const before=copy(state.draft),count=state.posts().length
 await state.saveOrder();assert.match(state.ctx.error.value,/实际施工部位最多填写200字/);assert.equal(state.posts().length,count);assert.equal(state.ctx.dialog.value,'order');assert.deepEqual(state.draft,before)
})

test('独立部位保存失败不改草稿或key，同内容重试保留既有幂等编号',async(t)=>{
 let attempts=0
 const state=await fresh(t,{crafts:[unconfigured],fetch:(path,init)=>path==='/api/order'&&init.method==='POST'?response(++attempts===1?{error:'合成版本冲突'}:{id:'saved',version:1},attempts===1?409:200):null}),first=state.draft.lines[0],second=state.addLocation(first)
 assert.ok(second,'无配置小项仍能新增独立部位以验证失败和重试')
 first.actual_location='一楼';first.segments=[area()];second.actual_location='二楼';second.segments=[area(1,4)];second.description='失败后保留'
 const before=copy(state.draft)
 await state.saveOrder();assert.match(state.ctx.error.value,/合成版本冲突/);assert.equal(state.ctx.dialog.value,'order');assert.deepEqual(state.draft,before);assert.equal(state.busy.value,false)
 await state.saveOrder();assert.equal(state.ctx.error.value,'');assert.deepEqual(state.draft,before)
 const [failed,retry]=state.posts();assert.equal(failed.init.body,retry.init.body);assert.equal(failed.init.headers['Idempotency-Key'],retry.init.headers['Idempotency-Key'])
})

const noPrices=permission=>permission!=='customer_prices'
const hidePrices=value=>{const out=copy(value);const strip=item=>{if(item&&typeof item==='object')for(const key of Object.keys(item)){if(['price_cents','total_cents','amount_cents','received_cents','outstanding_cents'].includes(key))delete item[key];else strip(item[key])}};strip(out);return out}
const assertNoPrices=value=>{if(value&&typeof value==='object')for(const [key,item]of Object.entries(value)){assert.equal(['price_cents','total_cents','amount_cents','received_cents','outstanding_cents'].includes(key),false,'hidden payload must omit '+key);assertNoPrices(item)}}

test('无客户价格权限可填写固定工艺与独立部位，保存普通信息但不补0或提交隐藏金额',async(t)=>{
 const state=await fresh(t,{can:noPrices,crafts:[hidePrices(unconfigured)]}),first=state.draft.lines[0]
 assert.equal(Object.hasOwn(first,'price_cents'),false)
 first.segments=[hidePrices(area())];first.actual_location='一楼主卫';first.description='无价仍可编辑备注'
 const second=state.addLocation(first);assert.ok(second);assert.equal(Object.hasOwn(second,'price_cents'),false)
 second.segments=[hidePrices(area(1,4))];second.actual_location='二楼主卫'
 state.lineWorkerChanged(second,person('b'));assert.equal(second.worker_id,'b')
 await state.saveOrder();assert.equal(state.ctx.error.value,'')
 const body=payload(state);assertNoPrices(body)
 assert.deepEqual(body.lines.map(item=>[item.worker_id,item.actual_location]),[['a','一楼主卫'],['b','二楼主卫']])
 assert.equal(body.lines[0].description,'无价仍可编辑备注');assert.equal(body.lines[1].segments[0].width,4)
 assert.equal(body.lines[0].wage_cents,300)
})

test('无价旧固定金额行用已施工/数量恢复，隐藏价格不影响重开或普通保存',async(t)=>{
 const amountCraft=hidePrices({...fixed,id:'amount-fixed',mode:'金额',subitems:[]}),old=hidePrices(storedLine('old-amount',{craft_id:'amount-fixed',subitem_id:'',subitem_name:'',mode:'金额',qty_micro:1000000,segments:[],entered:undefined}))
 const state=harness(t,{can:noPrices,crafts:[amountCraft],saved:hidePrices(storedOrder([old]))})
 await state.openOrder({id:'saved'});assert.equal(state.ctx.error.value,'')
 const line=state.draft.lines[0];assert.equal(line.entered,true);assert.equal(filled(line),true);assert.equal(Object.hasOwn(line,'price_cents'),false)
 line.description='更新整项备注';line.actual_location='入户门口'
 await state.saveOrder();assert.equal(state.ctx.error.value,'');const body=payload(state);assertNoPrices(body)
 assert.equal(body.lines[0].id,'old-amount');assert.equal(body.lines[0].qty_micro,1000000);assert.equal(body.lines[0].entered,true);assert.deepEqual(body.lines[0].segments,[])
})

test('无价旧临时项和售料保留ID/数量普通编辑，新自由定价入口及绕过保存被拒绝',async(t)=>{
 const old=hidePrices(storedOrder([storedLine('temporary-old',{craft_id:'',temporary:true,name:'历史临时项',subitem_id:'',segments:[area()]})]))
 old.worker_id='a';old.sales=[{id:'sale-old',product_id:'product',product_name:'历史商品',warehouse_id:'warehouse',qty_micro:2000000,input_qty:'2',input_unit:'base',price_unit:'桶'}]
 const state=harness(t,{can:noPrices,crafts:[hidePrices(fixed)],saved:old});await state.openOrder({id:'saved'});assert.equal(state.ctx.error.value,'')
 const temporary=state.draft.lines.find(item=>item.id==='temporary-old');temporary.description='保留单价只改备注';state.draft.sales[0].qty_micro=3000000;state.draft.sales[0].input_qty='3'
 const before=copy(state.draft);state.addTemporary();assert.match(state.ctx.error.value,/需要查看客户价格权限/);state.addSale();assert.match(state.ctx.error.value,/需要查看客户价格权限/);assert.deepEqual(state.draft,before)
 await state.saveOrder();assert.equal(state.ctx.error.value,'');const body=payload(state);assertNoPrices(body)
 assert.equal(body.lines[0].id,'temporary-old');assert.equal(body.lines[0].description,'保留单价只改备注');assert.equal(body.sales[0].id,'sale-old');assert.equal(body.sales[0].qty_micro,3000000);assert.equal(body.sales[0].input_unit,'base')
 state.draft.sales.push({...state.draft.sales[0],id:''});const count=state.posts().length;await state.saveOrder();assert.match(state.ctx.error.value,/新增临时收费项或销售材料需要查看客户价格权限/);assert.equal(state.posts().length,count)
})


test('配置部位初始只建一次，明确添加同部位施工明细保持独立且参考刷新不重复生成',async(t)=>{
 const state=await fresh(t),before=copy(state.draft.lines)
 const added=state.addLocation(state.draft.lines[0]);assert.equal(canAddOrderLocation(state.draft.lines[0]),true);assert.ok(added);assert.equal(added.subitem_id,state.draft.lines[0].subitem_id)
 assert.deepEqual(state.draft.lines.slice(0,before.length),before);assert.notEqual(added.client_key,state.draft.lines[0].client_key)
 const all=copy(state.draft.lines);await state.workerChanged();assert.deepEqual(state.draft.lines,all,'刷新参考及工人不重复生成配置小项')
 assert.equal(canAddOrderLocation({subitem_id:''}),true)
})
