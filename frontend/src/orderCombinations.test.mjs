import {test} from 'node:test'
import assert from 'node:assert/strict'
import {existsSync} from 'node:fs'
import {readFile} from 'node:fs/promises'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {ref,createSSRApp} from 'vue'
import {renderToString} from '@vue/server-renderer'
import {parse,compileScript} from '@vue/compiler-sfc'
import ts from 'typescript'

registerHooks({resolve(specifier,context,nextResolve){
 if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')){
  const candidate=new URL(specifier,context.parentURL)
  if(existsSync(fileURLToPath(candidate)+'.ts'))return nextResolve(specifier+'.ts',context)
 }
 return nextResolve(specifier,context)
}})
const {combinationKind,combinationLineRef,combinationAreaMicro,combinationValidation,combinationWaivers,localCombinationCandidates,serializeCombinations,combinationCharge,quantityDecimal}=await import('./orderCombinations.ts')
const {createOrderLocation}=await import('./orderLocations.ts')
const {lineAmount}=await import('./orderSegments.ts')
const {canAccess}=await import('./sessionAccess.ts')
const {command}=await import('./api.ts')
const {useOrders}=await import('./workflows/useOrders.ts')
const worker=id=>({id,name:'合成工人'+id,version:1,active:1,crafts:['防水涂料','丙纶']})
const fixed=[{id:'fixed-w',company_id:'company',category:'防水涂料',name:'合成防水',mode:'平方',price_cents:5000,wage_cents:2000,editable:true,subitems:[]},
 {id:'fixed-p',company_id:'company',category:'丙纶',name:'合成丙纶',mode:'平方',price_cents:3000,wage_cents:1500,included_by_craft_id:'fixed-w',editable:true,subitems:[]}]
function lines(){return fixed.map((craft,i)=>({...createOrderLocation({...craft,craft_id:craft.id,temporary:false},i),id:i?'p':'w',entered:true,worker_id:i?'b':'a',worker_name:worker(i?'b':'a').name,actual_location:i?'厨房':'主卫',segments:[{mode:'平方',length:i?10:6,width:10}],qty_micro:i?100000000:60000000}))}
const combo=(direction='polypropylene',area='40',extra={})=>({waterproof_line_id:'w',polypropylene_line_id:'p',waterproof_order_version:1,polypropylene_order_version:1,waive_direction:direction,input_area:area,waived_qty_micro:0,...extra})
function candidates(actual=lines(),waivers=new Map()) {return localCombinationCandidates({id:'order',version:1,work_date:'2026-10-10',lines:actual,category_workers:{}},waivers,id=>worker(id).name)}
const refs=new Set(['w','p'])

test('三模式客户金额与后端一致，免收面积不改实际量、人员或工资配置',()=>{
 for(const [direction,area,total] of [['','',600000],['polypropylene','40',480000],['waterproof','20',500000],['polypropylene','100',300000],['waterproof','60',300000]]){
  const actual=lines(),before=structuredClone(actual),available=candidates(actual),selected=direction?[combo(direction,area)]:[]
  const reserved=combinationWaivers([],selected,available)
  assert.equal(available.reduce((sum,c)=>sum+combinationCharge(c,reserved.get(c.id)||0),0),total)
  for(const l of actual)l.waived_qty_micro=reserved.get(l.id)||0
  assert.equal(actual.reduce((sum,l)=>sum+lineAmount(l),0),total)
  assert.deepEqual(actual.map(({waived_qty_micro,...l})=>l),before)
  assert.deepEqual(actual.map(l=>[l.worker_id,l.qty_micro,l.wage_cents]),[['a',60000000,2000],['b',100000000,1500]])
 }
})

test('免收面积只能手动填写正数，6位精度、上限、格式和重复配对验证',()=>{
 for(const value of ['', '-1','0','0.0000001','1e2','NaN','1000000.000001'])assert.equal(combinationAreaMicro(combo('polypropylene',value)),null)
 assert.equal(combinationAreaMicro(combo('polypropylene','0.000001')),1)
 assert.equal(combinationAreaMicro(combo('polypropylene','1000000')),1000000000000)
 assert.equal(quantityDecimal(1001501),'1.001501')
 assert.match(combinationValidation([combo(),combo()],[],candidates(),refs,true),/重复/)
 assert.match(combinationValidation([combo('waterproof','61')],[],candidates(),refs,true),/剩余可用面积/)
 assert.match(combinationValidation([combo('','40')],[],candidates(),refs,true),/明确选择免收防水或丙纶/)
 assert.match(combinationValidation([],[],candidates(),refs,true),/请添加/)
 assert.equal(combinationValidation([],[],candidates(),refs,false),'')
})

test('跨日期余量保持其他关系的保留面积，关闭只释放当前关系，不能超额或引用无关记录',()=>{
 const available=candidates(lines(),new Map([['p',90000000]])),original=[combo('polypropylene','40')]
 assert.equal(combinationWaivers(original,[],available).get('p'),50000000)
 assert.equal(combinationWaivers(original,[combo('polypropylene','30')],available).get('p'),80000000)
 assert.match(combinationValidation([combo('polypropylene','51')],original,available,refs,true),/不能重复免收/)
 assert.match(combinationValidation([combo()],[],available,new Set(),true),/本次施工/)
 available[1].editable=false;available[1].blocked_reason='已交票不能组合'
 assert.equal(combinationValidation([combo()],[],available,refs,true),'已交票不能组合')
})

test('只支持已填写固定平方米防水或丙纶，旧含价、临时、米和未填明细不成为候选',()=>{
 const l=lines()[0]
 assert.equal(combinationKind(l),'waterproof');assert.equal(combinationKind({...l,category:'室外防水'}),'waterproof')
 for(const values of [{mode:'米'},{category:'开工'},{temporary:true},{use_included_price:true},{craft_id:''},{entered:false,segments:[]}])assert.equal(combinationKind({...l,...values}),'')
 const hidden=candidates(lines().map(({price_cents,...line})=>line))
 assert.ok(hidden.every(c=>!Object.hasOwn(c,'price_cents')&&combinationCharge(c,0)===undefined))
})

test('序列化只发送真实明细引用、微面积及版本，不发送编辑器字段，不猜免收量',()=>{
 const c=combo('waterproof','20',{id:'relationship',version:3,ui_key:'local',waterproof_order_id:'order',polypropylene_order_id:'other'})
 assert.deepEqual(serializeCombinations([c]),[{id:'relationship',version:3,waterproof_line_id:'w',polypropylene_line_id:'p',waterproof_order_version:1,polypropylene_order_version:1,waive_direction:'waterproof',waived_qty_micro:20000000}])
 assert.throws(()=>serializeCombinations([combo('polypropylene','')]),/手动填写/)
 const fresh=createOrderLocation({...fixed[0],craft_id:fixed[0].id,temporary:false},0)
 assert.equal(combinationLineRef(fresh),fresh.client_key)
 assert.notEqual(combinationLineRef(fresh),combinationLineRef(createOrderLocation({...fixed[0],craft_id:fixed[0].id,temporary:false},1)))
})

test('独立组合权限默认拒绝普通管理员和员工，不拓宽价格、工资或财务权限',()=>{
 assert.equal(canAccess({role:'operator',permissions:[],crafts:[]},'combo_pricing'),false)
 assert.equal(canAccess({role:'employee',permissions:['orders','customer_prices','wages','finance'],crafts:[]},'combo_pricing'),false)
 const employee={role:'employee',permissions:['orders','combo_pricing'],crafts:[]}
 assert.equal(canAccess(employee,'combo_pricing'),true)
 for(const p of ['customer_prices','wages','finance'])assert.equal(canAccess(employee,p),false)
})

const response=value=>new Response(JSON.stringify(value),{status:200,headers:{'Content-Type':'application/json'}})
async function harness(t,can=()=>true){
 const posts=[],ctx={references:ref({companies:[],workers:[],warehouses:[],products:[],crafts:[]}),inventory:ref([]),categories:ref(['防水涂料','丙纶']),company:ref('company'),dialog:ref(''),error:ref(''),notice:ref(''),can,submit:command(),load:async()=>{},perform:async fn=>{ctx.error.value='';try{await fn()}catch(e){ctx.error.value=e.message}}}
 t.mock.method(globalThis,'fetch',async(path,init)=>{
  const url=new URL(path,'http://synthetic.local')
  if(init.method==='POST'){posts.push(JSON.parse(init.body));return response({id:'saved',version:1})}
  if(url.pathname==='/api/reference')return response({crafts:fixed.map(({price_cents,...craft})=>can('customer_prices')?{...craft,price_cents}:craft)})
  if(url.pathname==='/api/company-packages')return response({rows:[]})
  if(url.pathname==='/api/reference-options')return response({rows:url.searchParams.get('entity')==='companies'?[{id:'company',name:'合成公司'}]:[worker(url.searchParams.get('id'))],total:1})
  if(url.pathname==='/api/projects'||url.pathname==='/api/inventory')return response({rows:[],total:0,page:1})
  throw new Error('Unexpected request '+path)
 })
 const flow=useOrders(ctx,async()=>{})
 await flow.openOrder();assert.equal(ctx.error.value,'')
 ctx.references.value.workers=[worker('a'),worker('b')]
 const d=flow.orderDraft.value;d.address='合成组合工地'
 for(let i=0;i<d.lines.length;i++){const l=d.lines[i];l.entered=true;l.worker_id=i?'b':'a';l.worker_override=true;l.segments=[{mode:'平方',length:i?10:6,width:10}];l.actual_location=i?'厨房':'主卫'}
 return {ctx,flow,d,posts}
}

test('真实正常制单默认不组合，即使旧配置关联也保持正常公司价格与工资',async(t)=>{
 const {d}=await harness(t)
 assert.equal(d.combination_enabled,false);assert.equal(d.combination_dirty,false);assert.deepEqual(d.combinations,[])
 assert.ok(d.lines.every(l=>!l.use_included_price&&!l.included_by_craft_id))
 assert.deepEqual(d.lines.map(l=>l.price_cents),[5000,3000]);assert.deepEqual(d.lines.map(l=>l.wage_cents),[2000,1500])
})

test('真实useOrders同时保存新位置行及明确组合，client_ref映射独立且移除UI状态',async(t)=>{
 const {d,flow,posts,ctx}=await harness(t)
 const w=combinationLineRef(d.lines[0]),p=combinationLineRef(d.lines[1])
 d.combinations=[combo('polypropylene','40',{waterproof_line_id:w,polypropylene_line_id:p,ui_key:'draft-only'})]
 d.combination_dirty=true;d.combination_enabled=true;d.combination_error=''
 await flow.saveOrder();assert.equal(ctx.error.value,'');assert.equal(posts.length,1)
 const sent=posts[0]
 assert.deepEqual(sent.lines.map(l=>l.client_ref),[w,p]);assert.equal(sent.combinations[0].waived_qty_micro,40000000)
 assert.equal(sent.combinations[0].polypropylene_line_id,p)
 for(const field of ['combination_dirty','combination_enabled','combination_error'])assert.equal(Object.hasOwn(sent,field),false)
 for(const field of ['ui_key','input_area'])assert.equal(Object.hasOwn(sent.combinations[0],field),false)
 assert.ok(sent.lines.every(l=>!Object.hasOwn(l,'client_key')))
 assert.equal(d.combinations[0].input_area,'40');assert.equal(d.combination_dirty,true)
})

test('普通保存省略组合清单，撤权不清空原关系；显式改组合才检查操作权限',async(t)=>{
 const {d,flow,posts,ctx}=await harness(t,p=>p!=='combo_pricing')
 d.combinations=[combo()];d.combination_enabled=true;d.combination_dirty=false
 await flow.saveOrder();assert.equal(ctx.error.value,'');assert.equal(posts.length,1);assert.equal(Object.hasOwn(posts[0],'combinations'),false)
 assert.equal(d.combinations.length,1)
 d.combination_dirty=true
 await flow.saveOrder();assert.match(ctx.error.value,/权限已撤销/);assert.equal(posts.length,1)
})

test('无价格权限有组合授权可提交面积但payload不含客户价；前端验证失败保留输入不提交',async(t)=>{
 const {d,flow,posts,ctx}=await harness(t,p=>!['customer_prices','wages','finance'].includes(p))
 d.combinations=[combo('waterproof','20',{waterproof_line_id:combinationLineRef(d.lines[0]),polypropylene_line_id:combinationLineRef(d.lines[1])})]
 d.combination_dirty=true;d.combination_error=''
 await flow.saveOrder();assert.equal(ctx.error.value,'');assert.equal(posts.length,1)
 assert.ok(posts[0].lines.every(l=>!Object.hasOwn(l,'price_cents')&&!Object.hasOwn(l,'total_cents')))
 assert.equal(posts[0].combinations[0].waived_qty_micro,20000000)
 d.combination_error='免收面积超过剩余可用面积'
 await flow.saveOrder();assert.match(ctx.error.value,/剩余可用/);assert.equal(posts.length,1);assert.equal(d.combinations[0].input_area,'20')
})

async function component(name){
 const file=new URL('./components/'+name+'.vue',import.meta.url),{descriptor}=parse(await readFile(file,'utf8'))
 const script=compileScript(descriptor,{id:'combination-spec',inlineTemplate:true})
 const source=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,q,path)=>'from '+JSON.stringify(path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:new URL(path+'.ts',file).href))
 const compiled=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText
 return (await import('data:text/javascript,'+encodeURIComponent(compiled))).default
}

test('真实组合组件展示普通面积但隐藏客户金额，无授权控件禁用，有价回显部分收费',async()=>{
 const Editor=await component('OrderCombinationEditor')
 for(const visible of [true,false]){
  const actual=lines();actual[1].waived_qty_micro=40000000
  if(!visible)for(const l of actual)delete l.price_cents
  const d={id:'order',version:1,project_id:'',company_id:'company',work_date:'2026-10-10',lines:actual,combinations:[combo('polypropylene','40')],combination_dirty:false}
  const html=await renderToString(createSSRApp(Editor,{draft:d,references:{workers:[worker('a'),worker('b')]},can:p=>visible||p==='orders',busy:false,locked:false}))
  assert.match(html,/实际 100㎡ · 免收 40㎡ · 剩余收费 60㎡/)
  if(visible)assert.match(html,/1,800\.00|1800\.00/)
  else {assert.match(html,/无查看权限/);assert.match(html,/disabled[^>]*aria-label="开启防水丙纶组合"/);assert.doesNotMatch(html,/1,800\.00|1800\.00|3,000\.00|3000\.00/)}
 }
})
