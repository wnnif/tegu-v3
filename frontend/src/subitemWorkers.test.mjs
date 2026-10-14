import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {parse,compileScript} from '@vue/compiler-sfc'
import {createRenderer,createSSRApp,ref} from 'vue'
import {renderToString} from '@vue/server-renderer'
import ts from 'typescript'

registerHooks({resolve(specifier,context,nextResolve){
  if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')){
    const candidate=new URL(specifier,context.parentURL)
    if(existsSync(fileURLToPath(candidate)+'.ts'))return nextResolve(specifier+'.ts',context)
  }
  return nextResolve(specifier,context)
}})
const {useOrders}=await import('./workflows/useOrders.ts')
const {useFinance}=await import('./workflows/useFinance.ts')
const {command}=await import('./api.ts')
const {assignLineWorker,lineWorkerId}=await import('./orderWorkers.ts')
const {lineAccountCanEdit,lineCanEdit}=await import('./orderAccess.ts')
const {financeOrder,sourceOrderId,wageSettlementPayload}=await import('./settlements.ts')
const worker=(id,crafts=['防水涂料'])=>({id,name:'合成人员'+id,version:1,crafts,active:1})
const fixed={id:'fixed',company_id:'company',name:'合成防水',category:'防水涂料',mode:'平方',price_cents:2000,wage_cents:300,editable:true,subitems:[{id:'bath',name:'主卫'},{id:'kitchen',name:'厨房'}]}
const measure=()=>({mode:'平方',length:2,width:3,height:0,count:0,surface:'地面',amount_cents:0})
const savedLine=(id,person)=>({id,craft_id:'fixed',category:'防水涂料',name:'合成防水',spec:'',region:id,description:'',mode:'平方',price_cents:2000,wage_cents:300,temporary:false,editable:true,worker_id:person,worker_name:'历史'+person,segments:[measure()],qty_micro:6000000,total_cents:12000,labor_cents:1800})
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done});return {promise,resolve}}
const settle=()=>new Promise(done=>setImmediate(done))
function harness(t,override=()=>null){
  const requests=[]
  t.mock.method(globalThis,'fetch',async(path,init)=>{
    requests.push({path,init})
    const alternative=override(path,init);if(alternative)return alternative
    const url=new URL(path,'http://test.local')
    if(url.pathname==='/api/reference')return response({crafts:[fixed]})
    if(url.pathname==='/api/company-packages')return response({rows:[]})
    if(url.pathname==='/api/projects')return response({rows:[],total:0,page:1})
    if(url.pathname==='/api/reference-options'){
      const entity=url.searchParams.get('entity'),id=url.searchParams.get('id')
      return response({rows:entity==='companies'?[{id,name:'合成公司',version:1}]:entity==='workers'?[worker(id)]:entity==='warehouses'?[{id:'warehouse',name:'合成仓库',worker_id:url.searchParams.get('worker_id'),active:1}]:[],total:1})
    }
    if(url.pathname==='/api/inventory')return response({rows:[],total:0})
    if(url.pathname==='/api/order'&&init.method==='POST')return response({id:'saved',version:1})
    throw new Error('Unexpected '+path)
  })
  const ctx={references:ref({companies:[],workers:[],warehouses:[],products:[],crafts:[]}),inventory:ref([]),categories:ref(['防水涂料','丙纶']),company:ref('company'),dialog:ref(''),error:ref(''),notice:ref(''),can:()=>true,submit:command(),load:async()=>{},perform:async(fn)=>{ctx.error.value='';try{await fn()}catch(e){ctx.error.value=e.message}}}
  return {ctx,requests,...useOrders(ctx,async()=>{})}
}
const posts=state=>state.requests.filter(({init})=>init.method==='POST')
async function fresh(t,override){const state=harness(t,override);await state.openOrder();state.orderDraft.value.address='合成独立测试地址';return state}

const componentURLs=new Map()
async function componentURL(name,inlineTemplate=true){
  const file=name instanceof URL?name:new URL('./components/'+name+'.vue',import.meta.url)
  const key=file.href+'#inlineTemplate='+inlineTemplate
  if(componentURLs.has(key))return componentURLs.get(key)
  const {descriptor}=parse(await readFile(file,'utf8'),{filename:fileURLToPath(file)})
  const script=compileScript(descriptor,{id:'subitem-'+file.pathname,inlineTemplate})
  const js=ts.transpileModule(script.content,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText
  const dependencies=new Map()
  // Every Vue dependency in the tested subtree is real, including shared shells.
  for(const match of js.matchAll(/from\s+(['"])([^'"]+)\1/g)){
    const path=match[2]
    dependencies.set(path,path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:
      path.endsWith('.vue')?await componentURL(new URL(path,file)):
      path.startsWith('.')?new URL(path.endsWith('.ts')?path:path+'.ts',file).href:import.meta.resolve(path))
  }
  const source=js.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(dependencies.get(path)))
  const url='data:text/javascript,'+encodeURIComponent(source)
  componentURLs.set(key,url)
  return url
}
const OrderCraftEditor=(await import(await componentURL('OrderCraftEditor'))).default

test('同工艺主卫A厨房B分别保存，无默认施工人也能保存纯施工',async(t)=>{
  const state=await fresh(t),[bath,kitchen]=state.orderDraft.value.lines
  bath.segments=[measure()];kitchen.segments=[{...measure(),length:1,width:2}]
  state.lineWorkerChanged(bath,worker('a'));state.lineWorkerChanged(kitchen,worker('b'))
  await state.saveOrder()
  assert.equal(state.ctx.error.value,'')
  const body=JSON.parse(posts(state)[0].init.body)
  assert.equal(body.worker_id,'')
  assert.deepEqual(body.lines.map(line=>[line.subitem_id,line.worker_id,line.segments[0].length]),[['bath','a',2],['kitchen','b',1]])
  assert.equal(body.lines[0].price_cents,2000)
})
test('行工人换人仅更新该行身份，默认人变化保留明确人员与已填尺寸',async(t)=>{
  const state=await fresh(t),[bath,kitchen]=state.orderDraft.value.lines
  bath.segments=[measure()];kitchen.segments=[measure()]
  state.orderDraft.value.worker_id='a';await state.workerChanged(worker('a'))
  state.lineWorkerChanged(kitchen,worker('b'))
  const old=JSON.stringify(kitchen.segments)
  state.orderDraft.value.worker_id='c';await state.workerChanged(worker('c'))
  assert.equal(lineWorkerId(bath,state.orderDraft.value.worker_id),'c')
  assert.equal(lineWorkerId(kitchen,state.orderDraft.value.worker_id),'b')
  assert.equal(JSON.stringify(kitchen.segments),old)
  state.lineWorkerChanged(kitchen,worker('a'))
  assert.equal(kitchen.worker_id,'a');assert.equal(JSON.stringify(kitchen.segments),old)
  assert.equal(state.orderWarehouse.value,'warehouse')
})
test('已填小项没有工人或无该工艺技能时保存拒绝且不删输入',async(t)=>{
  const state=await fresh(t),line=state.orderDraft.value.lines[0];line.segments=[measure()]
  await state.saveOrder()
  assert.match(state.ctx.error.value,/请为已填写的小项选择施工人/)
  assert.equal(posts(state).length,0);assert.equal(line.segments[0].width,3)
  state.lineWorkerChanged(line,worker('wrong',['丙纶']));await state.saveOrder()
  assert.match(state.ctx.error.value,/技能授权/)
  assert.equal(posts(state).length,0);assert.equal(line.segments[0].width,3)
})
test('选择行工人或切工艺大类不会改变材料仓库和默认人员',async(t)=>{
  const state=await fresh(t);state.orderDraft.value.worker_id='material';await state.workerChanged(worker('material',['丙纶']))
  state.orderDraft.value.materials=[{product_id:'p',warehouse_id:'warehouse',qty_micro:1000000}]
  state.lineWorkerChanged(state.orderDraft.value.lines[0],worker('bath'))
  const before=state.requests.length;await state.orderCategoryChanged('防水涂料')
  assert.equal(state.requests.length,before)
  assert.equal(state.orderDraft.value.worker_id,'material')
  assert.equal(state.orderDraft.value.materials[0].warehouse_id,'warehouse')
  assert.deepEqual(state.allowedCrafts.value,['防水涂料','丙纶'])
})
test('首个可填写人员列可选大类默认，未选人计量禁用，账号只读不提供选人能力',async()=>{
  const line={...savedLine('',''),id:'',worker_name:'',segments:[]}
  const draft={company_id:'company',worker_id:'',lines:[line]}
  const props={draft,references:{crafts:[fixed],workers:[]},locked:false,can:()=>true,allowedCrafts:['防水涂料']}
  const rendered=await renderToString(createSSRApp(OrderCraftEditor,props))
  assert.match(rendered,/施工人[^<]*<\/label>[\s\S]*<select[^>]*>/)
  const select=rendered.match(/>防水涂料默认施工人<\/label>[\s\S]*?(<select[^>]*>)/)[1]
  assert.doesNotMatch(select,/disabled/)
  assert.match(rendered,/<fieldset class="measure-input" disabled>/)
  assert.doesNotMatch(rendered,/<fieldset[^>]*class="subitem-row"/)
  const readonly=await renderToString(createSSRApp(OrderCraftEditor,{...props,draft:{...draft,lines:[{...line,editable:false}]}}))
  assert.doesNotMatch(readonly,/>本项施工人<\/label>|>防水涂料默认施工人<\/label>/);assert.match(readonly,/施工人未指定/)
})
test('真实SSR固定行工艺列显示保存名称及缺省，不借工艺标题通过断言或移动临时编辑',async()=>{
  for(const [name,category,expected] of [['历史保存防水工艺','防水涂料','历史保存防水工艺'],['','防水涂料','防水涂料'],['','','未记录工艺']]){
    const line={...savedLine('line','a'),client_key:'line-key',name,category}
    const draft={company_id:'company',worker_id:'a',category_workers:{},lines:[line]}
    const props={draft,references:{crafts:[{...fixed,name:'当前目录改名不替换快照'}],workers:[worker('a')]},locked:false,can:()=>true,allowedCrafts:['防水涂料']}
    const before=JSON.stringify(draft)
    const html=await renderToString(createSSRApp(OrderCraftEditor,props))
    const cells=[...html.matchAll(/<div[^>]*data-label="工艺"[^>]*>([\s\S]*?)<\/div>/g)]
    assert.equal(cells.length,1,'读取真实九列工艺单元格，而不是全宽工艺标题')
    assert.match(cells[0][1],new RegExp('<strong class="line-craft-name">'+expected+'</strong>'))
    assert.match(cells[0][1],/平方 · 整项工艺/)
    assert.doesNotMatch(cells[0][1],/当前目录改名不替换快照/)
    assert.equal(JSON.stringify(draft),before,'名称回显不回写草稿或保存快照')
  }
  const line={...savedLine('temporary','a'),craft_id:'',temporary:true,name:'合成临时工艺'}
  const draft={company_id:'company',worker_id:'a',category_workers:{},lines:[line]}
  const common={draft,references:{crafts:[fixed],workers:[worker('a')]},locked:false,allowedCrafts:['防水涂料']}
  const html=await renderToString(createSSRApp(OrderCraftEditor,{...common,can:()=>true}))
  const cell=html.match(/<div[^>]*data-label="工艺"[^>]*>([\s\S]*?)<\/div>/)[1]
  assert.doesNotMatch(cell,/line-craft-name|临时项目|工资单价|整项工资/)
  assert.match(html,/临时项目<input[^>]*value="合成临时工艺"/)
  assert.match(html,/工资单价（元\/㎡）/)
  const noWages=await renderToString(createSSRApp(OrderCraftEditor,{...common,can:permission=>permission!=='wages'}))
  assert.doesNotMatch(noWages,/工资单价|整项工资/)
})
test('真实SSR人员标签视觉紧凑但保留可访问关联，大类默认有短可见标签',async()=>{
  const draft={company_id:'company',worker_id:'a',category_workers:{'防水涂料':'a'},lines:[savedLine('one','a'),savedLine('two','b')]}
  const props={draft,references:{crafts:[fixed],workers:[worker('a'),worker('b')]},locked:false,can:()=>true,allowedCrafts:['防水涂料']}
  const html=await renderToString(createSSRApp(OrderCraftEditor,props))
  const choices=[...html.matchAll(/<div[^>]*class="reference-picker (category-default-picker|line-worker-picker)"[^>]*>\s*(<label[^>]*for="([^"]+)"[^>]*>)([^<]+)<\/label>\s*<div[^>]*class="reference-control"[^>]*>\s*<select[^>]*id="([^"]+)"[^>]*>/g)]
  assert.equal(choices.length,3,'一份大类默认和两份独立本项选择仍在真实人员列内')
  assert.deepEqual(choices.map(match=>match[4]),['防水涂料默认施工人','本项施工人','本项施工人'])
  for(const match of choices){
    assert.equal(match[3],match[5],'label for仍关联其下拉框id')
    assert.doesNotMatch(match[2],/aria-hidden|\bhidden\b/,'完整可访问标签不能用隐藏属性移除')
  }
  assert.equal([...html.matchAll(/<small aria-hidden="true">大类默认<\/small>/g)].length,1)
})
test('真实SSR施工面有独立可访问名，地面墙面其他选项不会混入控件名称',async()=>{
  const line={...savedLine('one','a'),subitem_id:'bath',subitem_name:'主卫',actual_location:''}
  const draft={company_id:'company',worker_id:'a',category_workers:{'防水涂料':'a'},lines:[line]}
  const html=await renderToString(createSSRApp(OrderCraftEditor,{draft,references:{crafts:[fixed],workers:[worker('a')]},locked:false,can:()=>true,allowedCrafts:['防水涂料']}))
  assert.equal([...html.matchAll(/<select[^>]*aria-label="施工面"[^>]*>/g)].length,1)
  assert.match(html,/<option[^>]*>地面<\/option>/)
  assert.match(html,/<option[^>]*>墙面<\/option>/)
  assert.match(html,/<option[^>]*>其他<\/option>/)
})
test('账号可选人员与工人技能分别校验，选技能工人不能扩大固定工艺权限',()=>{
  const line=savedLine('','a')
  assert.equal(lineAccountCanEdit(line,'company',[fixed],false),true)
  assert.equal(lineCanEdit(line,'company',[fixed],undefined,false),false)
  assert.equal(lineCanEdit(line,'company',[fixed],worker('b'),false),true)
  assert.equal(lineCanEdit({...line,editable:false},'company',[fixed],worker('b'),false),false)
  assert.equal(lineCanEdit(line,'company',[{...fixed,editable:false}],worker('b'),false),false)
  assert.equal(lineCanEdit(line,'company',[fixed],{...worker('b'),active:0},false),false)
})
test('已锁记录不冒充账号缺权，工资已发时提示撤销对应发放',async()=>{
  const draft={id:'saved',company_id:'company',worker_id:'a',project_id:'',status:'进行中',lines:[savedLine('one','a')],materials:[],wage_paid:true}
  const common={draft,references:{crafts:[fixed],workers:[worker('a')],companies:[],products:[],warehouses:[]},can:()=>true,allowedCrafts:['防水涂料']}
  const table=await renderToString(createSSRApp(OrderCraftEditor,{...common,locked:true}))
  assert.match(table,/本次记录只读/);assert.doesNotMatch(table,/当前账号仅可查看/)
  const Editor=(await import(await componentURL('OrderEditor'))).default
  const html=await renderToString(createSSRApp(Editor,{...common,projects:[],projectPage:1,projectTotal:0,workerOptions:[],companyCraftCategories:[],selectedCategory:'',inventory:[],busy:false,orderLocked:true,orderWarehouse:''}))
  assert.match(html,/已有工资发放，先在工资管理撤销对应发放后再更正本次记录/)
  const readonly=await renderToString(createSSRApp(OrderCraftEditor,{...common,locked:false,draft:{...draft,lines:[{...draft.lines[0],editable:false}]}}))
  assert.match(readonly,/当前账号仅可查看/)
})
test('小项选中姓名独立完整显示，历史和默认人名字不会被窄下拉隐藏',async()=>{
  const name='合成长名字验收施工人员',line={...savedLine('one','a'),worker_name:name}
  const draft={company_id:'company',worker_id:'default',worker_name:'合成默认施工人',category_workers:{'防水涂料':'default'},lines:[line]}
  const props={draft,references:{crafts:[fixed],workers:[worker('a'),{...worker('a'),id:'default',name:'合成默认施工人'}]},locked:false,can:()=>true,allowedCrafts:['防水涂料']}
  const html=await renderToString(createSSRApp(OrderCraftEditor,props))
  assert.match(html,new RegExp('<small class="selected-worker-name">'+name+'<'))
  const inherited=await renderToString(createSSRApp(OrderCraftEditor,{...props,draft:{...draft,lines:[{...line,worker_id:'',worker_name:''}]}}))
  assert.match(inherited,/<small class="selected-worker-name">默认：施工人姓名未记录</,'保存缺姓名不以现资料回填')
  const newLine=await renderToString(createSSRApp(OrderCraftEditor,{...props,draft:{...draft,lines:[{...line,id:'',worker_id:'',worker_name:''}]}}))
  assert.match(newLine,/<small class="selected-worker-name">默认：合成默认施工人</)
})
test('尚未选工人的临时项可移除，只读或已锁记录仍不能移除',async(t)=>{
  const Table=(await import(await componentURL('OrderCraftEditor',false))).default;Table.render=()=>null
  const temporary={...savedLine('',''),id:'',craft_id:'',temporary:true,worker_name:'',segments:[]}
  const draft={company_id:'company',worker_id:'',lines:[temporary]}
  const renderer=createRenderer({createElement:type=>({type,children:[]}),createText:text=>({text}),createComment:text=>({text}),setText(){},setElementText(){},patchProp(){},insert(node,parent){parent.children.push(node)},remove(){},parentNode:()=>null,nextSibling:()=>null})
  const app=renderer.createApp(Table,{draft,references:{crafts:[],workers:[]},locked:false,can:()=>true,allowedCrafts:['防水涂料']})
  const state=app.mount({children:[]}).$.setupState;t.after(()=>app.unmount())
  state.removeTemporary({...temporary})
  assert.equal(draft.lines.length,1,'不在草稿中的临时项不能误删最后一项')
  temporary.editable=false;state.removeTemporary(temporary)
  assert.equal(draft.lines.length,1,'账号只读的临时项不能删除')
  temporary.editable=true;state.removeTemporary(temporary)
  assert.equal(draft.lines.length,0,'选人之前也能撤掉临时项')
  const lockedDraft={...draft,lines:[temporary]}
  const lockedApp=renderer.createApp(Table,{draft:lockedDraft,references:{crafts:[],workers:[]},locked:true,can:()=>true,allowedCrafts:['防水涂料']})
  const lockedState=lockedApp.mount({children:[]}).$.setupState;t.after(()=>lockedApp.unmount())
  lockedState.removeTemporary(temporary)
  assert.equal(lockedDraft.lines.length,1)
})
test('打开旧单并行补读所有不同工人且去重，保留已保存姓名快照',async(t)=>{
  const a=deferred(),b=deferred(),pending=[]
  const saved={id:'order',version:1,number:'T',company_id:'company',worker_id:'a',worker_name:'历史a',project_id:'',address:'地址',work_date:'2026-10-04',status:'进行中',lines:[savedLine('one','a'),savedLine('two','b'),savedLine('three','b')],materials:[]}
  const state=harness(t,(path)=>{
    const url=new URL(path,'http://test.local')
    if(url.pathname==='/api/order')return response(saved)
    if(url.pathname==='/api/reference-options'&&url.searchParams.get('entity')==='workers'){
      const id=url.searchParams.get('id');pending.push(id);return (id==='a'?a:b).promise
    }
  })
  const opening=state.openOrder({id:'order'});await settle()
  assert.deepEqual(pending,['a','b'])
  a.resolve(response({rows:[worker('a')],total:1}));b.resolve(response({rows:[worker('b')],total:1}));await opening
  assert.deepEqual(state.ctx.references.value.workers.map(w=>w.id),['a','b'])
  assert.equal(state.orderDraft.value.lines[1].worker_name,'历史b')
})
test('打开旧单时只读工艺固定施工人和历史姓名，不跟随大类默认变化',async(t)=>{
  const savedLineReadonly={...savedLine('readonly','a'),editable:false}
  const saved={id:'order',version:1,number:'T',company_id:'company',worker_id:'a',worker_name:'历史a',project_id:'',address:'地址',work_date:'2026-10-04',status:'进行中',category_workers:{'防水涂料':'a'},lines:[savedLineReadonly],materials:[]}
  const state=harness(t,path=>{
    const url=new URL(path,'http://test.local')
    if(url.pathname==='/api/order')return response(saved)
  })
  await state.openOrder({id:'order'})
  const line=state.orderDraft.value.lines[0]
  assert.equal(line.worker_id,'a')
  assert.equal(line.worker_name,'历史a')
  assert.equal(line.worker_override,true)
  state.orderDraft.value.category_workers['防水涂料']='b'
  assert.equal(lineWorkerId(line,state.orderDraft.value.category_workers['防水涂料']),'a')
  assert.equal(line.worker_name,'历史a')
})
test('旧单人员补读迟到不能覆盖后来打开的草稿',async(t)=>{
  const late=deferred()
  const state=harness(t,path=>{
    const url=new URL(path,'http://test.local')
    if(url.pathname==='/api/order')return response({id:'old',version:1,number:'T',company_id:'company',worker_id:'old-worker',project_id:'',address:'旧地址',work_date:'2026-10-04',status:'进行中',lines:[savedLine('old-line','old-worker')],materials:[]})
    if(url.pathname==='/api/reference-options'&&url.searchParams.get('id')==='old-worker')return late.promise
  })
  const old=state.openOrder({id:'old'});await settle();await state.openOrder()
  late.resolve(response({rows:[worker('old-worker')],total:1}));await old
  assert.equal(state.orderDraft.value.id,'')
  assert.equal(state.ctx.references.value.workers.length,0)
})
test('同单两个工人的工资身份各自独立，选择A不连带B，查看回原单',()=>{
  const rows=ref([financeOrder({id:'wa',order_id:'order',worker_id:'a',worker_name:'甲',version:2,wage_cents:1800}),financeOrder({id:'wb',order_id:'order',worker_id:'b',worker_name:'乙',version:3,wage_cents:600})])
  const selection=ref(['wa']),finance=useFinance(selection,rows,ref(''))
  assert.deepEqual(finance.settlementOrders.value.map(row=>row.worker_name),['甲'])
  assert.equal(sourceOrderId(rows.value[0]),'order')
  assert.deepEqual(wageSettlementPayload('pay',finance.settlementOrders.value),{action:'pay',orders:[{id:'wa',version:2}],note:''})
  selection.value=['wa','wb']
  assert.deepEqual(wageSettlementPayload('undo-pay',finance.settlementOrders.value).orders,[{id:'wa',version:2},{id:'wb',version:3}])
})
test('工资确认组件实际提交独立工资行，不向整单财务接口发放',async(t)=>{
  const Batch=(await import(await componentURL('FinanceBatch',false))).default;Batch.render=()=>null
  const calls=[];t.mock.method(globalThis,'fetch',async(path,init)=>{calls.push({path,body:JSON.parse(init.body)});return response({ok:true})})
  const renderer=createRenderer({createElement:type=>({type,children:[]}),createText:text=>({text}),createComment:text=>({text}),setText(){},setElementText(){},patchProp(){},insert(node,parent){parent.children.push(node)},remove(){},parentNode:()=>null,nextSibling:()=>null})
  const app=renderer.createApp(Batch,{orders:[financeOrder({id:'wa',order_id:'order',worker_id:'a',worker_name:'甲',version:2,wage_cents:1800})],company:'',action:'pay',wages:true})
  const state=app.mount({children:[]}).$.setupState;t.after(()=>app.unmount())
  await state.save()
  assert.equal(state.error,'')
  assert.equal(calls[0].path,'/api/wage-settlement')
  assert.deepEqual(calls[0].body.orders,[{id:'wa',version:2}])
})

test('内部打印逐小项和工资行显示各人工资，客户版不渲染工资或发放状态',async(t)=>{
  const Print=(await import(await componentURL('OrderPrint'))).default
  const order={id:'order',company_id:'company',worker_id:'a',worker_name:'材料人',number:'T',work_date:'2026-10-04',address:'合成地址',status:'进行中',total_cents:24000,wage_cents:3600,delivery_wage_cents:0,
    lines:[savedLine('主卫','a'),savedLine('厨房','b')],
    wage_records:[{id:'wa',order_id:'order',worker_id:'a',worker_name:'历史a',number:'T',work_date:'2026-10-04',wage_cents:1800,delivery_wage_cents:0,wage_paid:1},
      {id:'wb',order_id:'order',worker_id:'b',worker_name:'历史b',number:'T',work_date:'2026-10-04',wage_cents:1800,delivery_wage_cents:0,wage_paid:0}]}
  const context={},customer=await renderToString(createSSRApp(Print,{order,company:'合成公司',worker:'材料人',canWages:true}),context)
  const customerHtml=customer+(context.teleports?.body||'')
  assert.doesNotMatch(customerHtml,/人工费（元）|工资合计|已发放|待发放/)
  assert.match(customerHtml,/宜宾特固建材施工单/)
  assert.match(customerHtml,/<th>施工人员<\/th><th>工艺<\/th><th>部位<\/th><th>单价<\/th><th>方量明细<\/th><th>方量小计<\/th><th>金额小计<\/th><th>备注<\/th><th>合计金额<\/th>/)
  assert.match(customerHtml,/<th colspan="8">合计金额<\/th>/)
  assert.match(customerHtml,/<td>历史a<\/td>[\s\S]*?<td>主卫<\/td>/);assert.match(customerHtml,/<td>历史b<\/td>[\s\S]*?<td>厨房<\/td>/)
  const Setup=(await import(await componentURL('OrderPrint',false))).default;Setup.render=()=>null
  const renderer=createRenderer({createElement:type=>({type,children:[]}),createText:text=>({text}),createComment:text=>({text}),setText(){},setElementText(){},patchProp(){},insert(node,parent){parent.children.push(node)},remove(){},parentNode:()=>null,nextSibling:()=>null})
  const app=renderer.createApp(Setup,{order,company:'合成公司',worker:'材料人',canWages:true}),state=app.mount({children:[]}).$.setupState;t.after(()=>app.unmount())
  state.mode='internal'
  assert.equal(state.internal,true)
  assert.equal(state.lines[0].worker_name,'历史a');assert.equal(state.lines[1].worker_name,'历史b')
  assert.equal(state.workers,'历史a、历史b')
})


test('明确选与默认相同的本项人员仍是独立覆盖，重新选同一人员保留历史姓名，只有空选项回到继承',()=>{
 const item={...savedLine('first','a'),worker_name:'历史姓名快照',worker_override:false},dimensions=JSON.stringify(item.segments)
 assignLineWorker(item,worker('a'),'a')
 assert.equal(item.worker_override,true);assert.equal(item.worker_id,'a');assert.equal(item.worker_name,'历史姓名快照')
 assert.equal(lineWorkerId(item,'b'),'a','后续换大类默认不会带走首行明确选人')
 assignLineWorker(item,null,'a');assert.equal(item.worker_override,false);assert.equal(item.worker_id,'');assert.equal(item.worker_name,'历史姓名快照')
 assignLineWorker(item,worker('b'),'a');assert.equal(item.worker_id,'b');assert.equal(item.worker_name,'合成人员b')
 assignLineWorker(item,null,'a');assert.equal(item.worker_id,'');assert.equal(item.worker_name,'')
 assert.equal(JSON.stringify(item.segments),dimensions)
})

test('useOrders只允许有账号填写权的未锁行换人，技能不匹配仍能换人修复且维持尺寸备注',async(t)=>{
 const state=await fresh(t),item=state.orderDraft.value.lines[0]
 item.segments=[measure()];item.description='必须保留'
 state.lineWorkerChanged(item,worker('mismatch',['开工']));const dimensions=JSON.stringify(item.segments)
 assert.equal(lineCanEdit(item,'company',[fixed],worker('mismatch',['开工']),true),false)
 state.lineWorkerChanged(item,worker('a'));assert.equal(item.worker_id,'a');assert.equal(item.description,'必须保留');assert.equal(JSON.stringify(item.segments),dimensions)
 item.editable=false;const before=JSON.stringify(item);state.lineWorkerChanged(item,worker('b'));assert.equal(JSON.stringify(item),before)
 item.editable=true;state.orderDraft.value.wage_paid=1;const settled=JSON.stringify(item);state.lineWorkerChanged(item,worker('b'));assert.equal(JSON.stringify(item),settled)
 state.orderDraft.value.wage_paid=0;const removed={...item};state.lineWorkerChanged(removed,worker('b'));assert.equal(JSON.stringify(item),settled);assert.equal(removed.worker_id,'a')
})


test('重开没有继承标记的旧单保守固定实际施工人，同默认ID也不随默认改变且姓名快照不变',async(t)=>{
 const saved={id:'order-pinned',version:3,number:'原单',company_id:'company',worker_id:'',project_id:'',address:'原地址',work_date:'2026-10-04',status:'进行中',wage_paid:0,lines:[savedLine('actual-one','a'),savedLine('actual-two','a')],materials:[],sales:[]}
 const state=harness(t,(path,init)=>new URL(path,'http://test.local').pathname==='/api/order'&&init.method==='GET'?response(saved):null)
 await state.openOrder({id:saved.id});assert.equal(state.ctx.error.value,'')
 const draft=state.orderDraft.value,[first,second]=draft.lines,dimensions=JSON.stringify(draft.lines.map(item=>item.segments))
 assert.equal(draft.category_workers['防水涂料'],'a')
 assert.deepEqual([first.worker_id,first.worker_override,second.worker_id,second.worker_override],['a',true,'a',true])
 assert.deepEqual(draft.saved_snapshot,saved)
 draft.category_workers['防水涂料']='b';state.categoryWorkerChanged('防水涂料',worker('b'))
 assert.deepEqual([lineWorkerId(first,'b'),lineWorkerId(second,'b')],['a','a'])
 assert.deepEqual([first.worker_name,second.worker_name],['历史a','历史a'])
 assert.equal(lineWorkerId(draft.lines.find(item=>!item.id),'b'),'b','新补齐且未覆盖的配置行仍继承默认人')
 await state.saveOrder();assert.equal(state.ctx.error.value,'')
 const body=JSON.parse(posts(state).at(-1).init.body)
 assert.deepEqual(body.lines.map(item=>[item.id,item.worker_id,item.worker_name]),[['actual-one','a','历史a'],['actual-two','a','历史a']])
 assert.ok(body.lines.every(item=>!Object.hasOwn(item,'worker_override')));assert.equal(Object.hasOwn(body,'category_workers'),false);assert.equal(Object.hasOwn(body,'saved_snapshot'),false)
 state.lineWorkerChanged(second,null);assert.equal(second.worker_override,false);assert.equal(second.worker_id,'');assert.equal(lineWorkerId(second,'b'),'b');assert.equal(second.worker_name,'')
 draft.category_workers['防水涂料']='a';assert.equal(lineWorkerId(second,'a'),'a');assert.equal(first.worker_name,'历史a')
 assert.equal(JSON.stringify(draft.lines.map(item=>item.segments)),dimensions);assert.deepEqual(draft.saved_snapshot,saved)
})
