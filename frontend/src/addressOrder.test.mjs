import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {parse,compileScript} from '@vue/compiler-sfc'
import {createRenderer,createSSRApp,reactive,ref} from 'vue'
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
const {command}=await import('./api.ts')

async function component(name,inlineTemplate=true){
  const file=new URL('./components/'+name+'.vue',import.meta.url)
  const {descriptor}=parse(await readFile(file,'utf8'))
  const script=compileScript(descriptor,{id:'address-order-'+name,inlineTemplate})
  const source=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>{
    const url=path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:
      path.endsWith('.vue')?'data:text/javascript,export default {render(){return null}}':new URL(path+'.ts',file).href
    return 'from '+JSON.stringify(url)
  })
  const js=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText
  return (await import('data:text/javascript,'+encodeURIComponent(js))).default
}
const ProjectSelector=await component('ProjectSelector'),OrderEditor=await component('OrderEditor')
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done});return {promise,resolve}}
const settle=()=>new Promise(done=>setImmediate(done))
const project=(id='p',company='c',address='合成小区1栋101号')=>({id,company_id:company,company_name:'合成公司',name:'历史项目名',address,number:'P-'+id,status:'进行中',version:1,order_count:1})
const craft=(company='c',projectId='')=>({id:company+'-fixed',company_id:company,name:'合成固定工艺',category:'开工',mode:'金额',editable:true,price_cents:projectId?2000:1000,wage_cents:300,subitems:[]})
const worker={id:'w',name:'合成师傅',version:1,crafts:['开工'],active:1}
function editorProps(draft,companyMode='split',packageOptions=[]){
  return {draft,references:{companies:[{id:'c',name:'合成公司',ticket_mode:companyMode}],crafts:[],workers:[],warehouses:[],products:[]},
    projects:[],packageOptions,projectPage:1,projectTotal:0,workerOptions:[],companyCraftCategories:[],selectedCategory:'',inventory:[],busy:false,orderLocked:false,orderWarehouse:'',allowedCrafts:['开工'],can:()=>true}
}
const html=(component,props)=>renderToString(createSSRApp(component,props))
function harness(t,override=()=>null,{parallelActions=false}={}){
  const requests=[],loaded=[],refreshed=[]
  t.mock.method(globalThis,'fetch',async(path,init)=>{
    requests.push({path,init})
    const alternative=override(path,init)
    if(alternative)return alternative
    const url=new URL(path,'http://test.local')
    if(url.pathname==='/api/reference')return response({crafts:[craft(url.searchParams.get('company_id'),url.searchParams.get('project_id'))]})
    if(url.pathname==='/api/company-packages')return response({rows:[]})
    if(url.pathname==='/api/projects')return response({rows:[project('p',url.searchParams.get('company_id'))],total:1,page:1})
    if(url.pathname==='/api/reference-options'){
      const entity=url.searchParams.get('entity'),id=url.searchParams.get('id')
      return response({rows:entity==='companies'?[{id,name:'合成公司',version:1,ticket_mode:'split'}]:entity==='workers'?[worker]:entity==='warehouses'?[{id:'wh',name:'合成仓库',worker_id:url.searchParams.get('worker_id'),active:1}]:[],total:1})
    }
    if(url.pathname==='/api/inventory')return response({rows:[],total:0})
    if(url.pathname==='/api/order'&&init.method==='POST')return response({id:'saved',project_id:'created',version:1})
    throw new Error('Unexpected test URL '+path)
  })
  let busy=false
  const ctx={references:ref({companies:[],workers:[],warehouses:[],products:[],crafts:[]}),inventory:ref([]),categories:ref(['开工']),company:ref('c'),
    dialog:ref(''),notice:ref(''),error:ref(''),can:()=>true,submit:command(),load:async()=>{loaded.push(true)},
    perform:async(fn)=>{if(busy&&!parallelActions)return;busy=true;ctx.error.value='';try{await fn()}catch(e){ctx.error.value=e.message}finally{busy=false}}}
  return {ctx,requests,loaded,refreshed,...useOrders(ctx,async(fresh)=>{refreshed.push(fresh)})}
}
async function fresh(t,override){
  const state=harness(t,override)
  await state.openOrder()
  state.orderDraft.value.address='合成小区1栋101号'
  state.orderDraft.value.worker_id='w'
  await state.workerChanged(worker)
  state.orderDraft.value.category_workers={'开工':'w'}
  state.categoryWorkerChanged('开工',worker)
  state.orderDraft.value.lines[0].entered=true
  return state
}
const posts=state=>state.requests.filter(({init})=>init.method==='POST')

test('已有地址查找和分页呈现地址及编号，首次施工无需必选工地',async()=>{
  const draft={id:'',company_id:'c',project_id:'',address:''}
  const rendered=await html(ProjectSelector,{draft,projects:[project()],disabled:false,page:1,total:101})
  assert.match(rendered,/查找已有施工地址/)
  assert.match(rendered,/maxlength="300"/)
  assert.match(rendered,/首次施工：直接填写下方施工地址/)
  assert.match(rendered,/合成小区1栋101号 · P-p/)
  assert.doesNotMatch(rendered,/历史项目名|先在“工地项目”中新建|<select[^>]*required/)
  assert.match(rendered,/下一页地址/)
  const selected=await html(ProjectSelector,{draft:{...draft,project_id:'old',address:'原地址',project_number:'P-old'},projects:[],disabled:false,page:2,total:101})
  assert.match(selected,/原地址 · P-old/)
  const legacy=await html(ProjectSelector,{draft:{...draft,id:'history'},projects:[],disabled:true,page:1,total:0})
  assert.match(legacy,/历史记录未关联工地/)
  assert.doesNotMatch(legacy,/首次施工：/)
})

test('首次制单真实模板只有一个必填300字地址，显示当前公司两票归票选择和保存状态',async()=>{
  const draft={id:'',company_id:'c',project_id:'',address:'',lines:[],materials:[],sales:[{qty_micro:1000000,price_cents:100}]}
  const rendered=await html(OrderEditor,editorProps(draft))
  assert.match(rendered,/施工地址<input[^>]*maxlength="300"[^>]*required/)
  assert.doesNotMatch(rendered,/readonly|项目名称/)
  assert.match(rendered,/第一票|第二票/)
  assert.match(rendered,/首次施工按公司当前配置建档归票/)
  const busy=await html(OrderEditor,{...editorProps(draft),busy:true})
  assert.match(busy,/保存中…/)
  assert.match(busy,/<button class="primary" disabled>/)
  const existing=await html(OrderEditor,editorProps({...draft,project_id:'p',address:'原地址'}))
  assert.match(existing,/施工地址<input[^>]*readonly/)
  const legacy=await html(OrderEditor,editorProps({...draft,id:'history',address:'历史地址'}))
  assert.doesNotMatch(legacy,/临时项 \/ 售料归入|首次施工按公司当前配置建档归票/)
})

test('首次建档可选房型套餐，明确套餐费只收一次且只免已映射项目',async(t)=>{
  const packageOption={id:'package-3',name:'墙地固普通工艺',room_count:3,price_cents:120000,coverage:[
    {item_name:'墙地固·墙',craft_id:'c-fixed',subitem_id:'',mapping:'mapped'},
    {item_name:'门框改色',mapping:'pending'},
  ]}
  const rendered=await html(OrderEditor,editorProps({id:'',company_id:'c',project_id:'',package_id:packageOption.id,address:'',lines:[],materials:[],sales:[]},'single',[packageOption]))
  assert.match(rendered,/工地房型套餐/)
  assert.match(rendered,/墙地固普通工艺/)
  assert.match(rendered,/套餐金额 ¥ 1,200\.00，保存后只计入首张票/)
  assert.match(rendered,/墙地固·墙/)
  assert.match(rendered,/门框改色/)
  assert.match(rendered,/尚未唯一对应系统工艺，不自动免收/)
  assert.doesNotMatch(await html(OrderEditor,editorProps({id:'',company_id:'c',project_id:'p',package_id:'',address:'原址',lines:[],materials:[],sales:[]},'single',[packageOption])),/工地房型套餐（可选）/)
  const state=await fresh(t,(path)=>path.startsWith('/api/company-packages?')?response({rows:[packageOption]}):null)
  assert.equal(state.companyPackages.value[0].id,packageOption.id)
  state.orderDraft.value.package_id=packageOption.id
  await state.saveOrder()
  assert.equal(JSON.parse(posts(state)[0].init.body).package_id,packageOption.id)
})

test('工地A切B瞬间屏蔽A分票和原票，迟到A响应不覆盖B快照',async(t)=>{
  const setup=await component('OrderEditor',false);setup.render=()=>null
  const renderer=createRenderer({createElement:type=>({type,children:[]}),createText:text=>({text}),createComment:text=>({text}),
    setText(){},setElementText(){},patchProp(){},insert(node,parent){parent.children.push(node)},remove(){},parentNode:()=>null,nextSibling:()=>null})
  const draft=reactive({id:'',company_id:'c',project_id:'p',address:'原地址',lines:[],materials:[],sales:[]})
  const app=renderer.createApp(setup,editorProps(draft))
  const state=app.mount({children:[]}).$.setupState;t.after(()=>app.unmount())
  state.projectLoaded({project:{id:'p',ticket_mode:'single'},tickets:[]})
  assert.equal(state.split,false)
  state.projectLoaded({project:{id:'other',ticket_mode:'split'},tickets:[]})
  assert.equal(state.split,false)
  state.projectLoaded({project:{id:'p',ticket_mode:'split'},tickets:[{id:'ticket-a',status:'已交单'}],total:1})
  assert.equal(state.split,true)
  assert.equal(state.correctionTickets[0].id,'ticket-a')
  draft.project_id='b'
  assert.equal(state.currentProjectWork,null)
  assert.equal(state.split,false)
  assert.deepEqual(state.correctionTickets,[])
  state.projectLoaded({project:{id:'p',ticket_mode:'split'},tickets:[{id:'ticket-a',status:'已交单'}],total:2})
  assert.equal(state.currentProjectWork,null)
  assert.deepEqual(state.correctionTickets,[])
  state.projectLoaded({project:{id:'b',ticket_mode:'single'},tickets:[{id:'ticket-b',status:'已交单'}],total:1})
  assert.equal(state.split,false)
  assert.equal(state.correctionTickets[0].id,'ticket-b')
  assert.equal(state.currentProjectWork.project.id,'b')
})

test('首次公司加施工地址直接提交一次order，不前置建project，保留两票售料归票',async(t)=>{
  const state=await fresh(t)
  state.orderDraft.value.address='  合成小区1栋101号  '
  state.orderDraft.value.sales=[{product_id:'product',warehouse_id:'wh',qty_micro:1000000,price_cents:300}]
  state.orderDraft.value.sales_ticket_slot=2
  await state.saveOrder()
  assert.equal(state.ctx.error.value,'')
  assert.equal(posts(state).length,1)
  const request=posts(state)[0],payload=JSON.parse(request.init.body)
  assert.equal(request.path,'/api/order')
  assert.equal(payload.project_id,'')
  assert.equal(payload.address,'合成小区1栋101号')
  assert.equal(payload.sales_ticket_slot,2)
  assert.equal(payload.lines[0].craft_id,'c-fixed')
  assert.equal(state.ctx.dialog.value,'')
  assert.deepEqual(state.refreshed,[true])
})

test('首次地址不能为空或超过300字，300个Unicode字符可提交',async(t)=>{
  const state=await fresh(t)
  state.orderDraft.value.address='  '
  await state.saveOrder()
  assert.match(state.ctx.error.value,/请填写施工地址/)
  assert.equal(posts(state).length,0)
  state.orderDraft.value.address='址'.repeat(301)
  await state.saveOrder()
  assert.match(state.ctx.error.value,/最多填写300字/)
  assert.equal(posts(state).length,0)
  state.orderDraft.value.address='址'.repeat(300)
  await state.saveOrder()
  assert.equal(posts(state).length,1)
})

test('首次纯送货也可按新地址保存，不要求填写施工工艺',async(t)=>{
  const state=await fresh(t)
  state.orderDraft.value.lines.forEach(line=>{line.entered=false})
  state.orderDraft.value.sales=[{product_id:'product',warehouse_id:'wh',qty_micro:2000000,price_cents:1200}]
  state.orderDraft.value.delivery_wage_cents=500
  await state.saveOrder()
  assert.equal(state.ctx.error.value,'')
  const payload=JSON.parse(posts(state)[0].init.body)
  assert.equal(payload.project_id,'');assert.deepEqual(payload.lines,[])
  assert.equal(payload.sales.length,1);assert.equal(payload.delivery_wage_cents,500)
})

test('同公司原地址冲突保留全部草稿，可查找选原地址后重试',async(t)=>{
  let reject=true
  const state=await fresh(t,(path,init)=>path==='/api/order'&&init.method==='POST'&&reject?response({error:'该公司已有此施工地址，请查找并选择原地址'},409):null)
  state.orderDraft.value.note='失败后应保留的备注'
  const before=JSON.stringify(state.orderDraft.value)
  await state.saveOrder()
  assert.match(state.ctx.error.value,/查找并选择原地址/)
  assert.equal(JSON.stringify(state.orderDraft.value),before)
  assert.equal(state.ctx.dialog.value,'order')
  await state.loadProjectOptions('合成小区',1)
  state.orderDraft.value.project_id='p'
  await state.projectChanged()
  assert.equal(state.orderDraft.value.address,'合成小区1栋101号')
  assert.equal(state.orderDraft.value.lines[0].price_cents,2000)
  state.orderDraft.value.category_workers={'开工':'w'}
  state.orderDraft.value.lines[0].entered=true
  reject=false
  await state.saveOrder()
  assert.equal(JSON.parse(posts(state)[1].init.body).project_id,'p')
})

test('丢失响应后草稿保留，内容未变重试复用操作编号',async(t)=>{
  let unavailable=true
  const state=await fresh(t,(path,init)=>path==='/api/order'&&init.method==='POST'&&unavailable?response({error:'响应丢失'},503):null)
  const before=JSON.stringify(state.orderDraft.value)
  await state.saveOrder()
  assert.equal(JSON.stringify(state.orderDraft.value),before)
  assert.equal(state.ctx.dialog.value,'order')
  unavailable=false
  await state.saveOrder()
  assert.equal(posts(state).length,2)
  assert.equal(posts(state)[0].init.headers['Idempotency-Key'],posts(state)[1].init.headers['Idempotency-Key'])
  assert.equal(posts(state)[0].init.body,posts(state)[1].init.body)
})

test('保存中重复调用只发送一次施工记录',async(t)=>{
  const pending=deferred()
  const state=await fresh(t,(path,init)=>path==='/api/order'&&init.method==='POST'?pending.promise:null)
  const first=state.saveOrder()
  await settle()
  await state.saveOrder()
  assert.equal(posts(state).length,1)
  pending.resolve(response({id:'saved',project_id:'p',version:1}))
  await first
})

test('换公司同步清地址归属、工艺材料、施工人及原票状态，并加载新公司',async(t)=>{
  const state=await fresh(t)
  Object.assign(state.orderDraft.value,{company_id:'other',project_id:'p',project_name:'旧项目',project_number:'P-old',ticket_action:'correct',ticket_id:'old-ticket',ticket_version:9,sales_ticket_slot:2,delivery_wage_cents:900})
  state.orderDraft.value.materials=[{product_id:'product',qty_micro:1000000}]
  state.orderDraft.value.sales=[{product_id:'product',qty_micro:1000000}]
  await state.onOrderCompanyChanged()
  const draft=state.orderDraft.value
  assert.equal(draft.address,'');assert.equal(draft.project_id,'');assert.equal(draft.project_name,'');assert.equal(draft.project_number,'')
  assert.equal(draft.ticket_action,'normal');assert.equal(draft.ticket_id,'');assert.equal(draft.ticket_version,0);assert.equal(draft.sales_ticket_slot,1)
  assert.equal(draft.worker_id,'');assert.equal(draft.delivery_wage_cents,0)
  assert.deepEqual(draft.materials,[]);assert.deepEqual(draft.sales,[]);assert.deepEqual(state.ctx.inventory.value,[])
  assert.equal(draft.lines[0].craft_id,'other-fixed')
  assert.equal(state.ctx.references.value.companies.find(company=>company.id==='other').ticket_mode,'split')
})

test('清已有地址选择后可填新址，原票更正/追加和第二票选择全部重置',async(t)=>{
  const state=await fresh(t)
  Object.assign(state.orderDraft.value,{project_id:'p',ticket_action:'append',ticket_id:'old-ticket',ticket_version:5,sales_ticket_slot:2})
  await state.projectChanged()
  assert.equal(state.orderDraft.value.address,'合成小区1栋101号')
  assert.equal(state.orderDraft.value.lines[0].price_cents,2000)
  Object.assign(state.orderDraft.value,{project_id:'',ticket_action:'correct',ticket_id:'old-ticket',ticket_version:7,sales_ticket_slot:2})
  await state.projectChanged()
  assert.equal(state.orderDraft.value.address,'')
  assert.equal(state.orderDraft.value.ticket_action,'normal');assert.equal(state.orderDraft.value.ticket_id,'');assert.equal(state.orderDraft.value.ticket_version,0);assert.equal(state.orderDraft.value.sales_ticket_slot,1)
  assert.equal(state.orderDraft.value.lines[0].price_cents,1000)
})

test('前公司迟到的工艺和地址列表不能覆盖新公司选择',async(t)=>{
  const oldCrafts=deferred(),oldProjects=deferred()
  let delay=false
  const state=harness(t,(path)=>{
    const url=new URL(path,'http://test.local')
    if(!delay||url.searchParams.get('company_id')!=='old')return null
    if(url.pathname==='/api/reference')return oldCrafts.promise
    if(url.pathname==='/api/projects')return oldProjects.promise
    return null
  })
  await state.openOrder()
  delay=true
  state.orderDraft.value.company_id='old'
  const old=state.onOrderCompanyChanged()
  await settle()
  state.orderDraft.value.company_id='new'
  await state.onOrderCompanyChanged()
  oldCrafts.resolve(response({crafts:[craft('old')]}));oldProjects.resolve(response({rows:[project('old','old')],total:99,page:1}))
  await old
  assert.equal(state.ctx.references.value.crafts[0].company_id,'new')
  assert.equal(state.orderDraft.value.lines[0].craft_id,'new-fixed')
  assert.equal(state.projectOptions.value[0].company_id,'new');assert.equal(state.projectTotal.value,1)
})

test('同公司不同地址快照及搜索页的迟到响应不能覆盖最新选择',async(t)=>{
  const oldSnapshot=deferred(),oldSearch=deferred()
  const state=harness(t,(path)=>{
    const url=new URL(path,'http://test.local')
    if(url.pathname==='/api/reference'&&url.searchParams.get('project_id')==='old')return oldSnapshot.promise
    if(url.pathname==='/api/projects'&&url.searchParams.get('q')==='旧搜索')return oldSearch.promise
    return null
  },{parallelActions:true})
  // Direct changes exercise racing responses even though the visible form disables controls while loading.
  await state.openOrder()
  state.projectOptions.value=[project('old'),project('new')]
  state.orderDraft.value.project_id='old'
  const old=state.projectChanged()
  await settle()
  state.orderDraft.value.project_id='new'
  await state.projectChanged()
  oldSnapshot.resolve(response({crafts:[{...craft('c','old'),name:'旧快照'}]}))
  await old
  assert.equal(state.ctx.references.value.crafts[0].name,'合成固定工艺')
  assert.equal(state.orderDraft.value.project_number,'P-new')
  assert.equal(state.orderDraft.value.lines[0].name,'合成固定工艺')
  const search=state.loadProjectOptions('旧搜索',1)
  await state.loadProjectOptions('新搜索',2)
  oldSearch.resolve(response({rows:[project('old')],total:999,page:1}))
  await search
  assert.equal(state.projectQuery.value,'新搜索');assert.equal(state.projectPage.value,2);assert.equal(state.projectTotal.value,1)
})

test('历史未关联记录的编辑仍保留空归属和原地址，不自动按地址关联',async(t)=>{
  const saved={id:'history',version:2,number:'O-old',company_id:'c',project_id:'',address:'  历史原地址  ',work_date:'2026-01-01',worker_id:'w',status:'进行中',
    lines:[{...craft(),id:'old-line',craft_id:'c-fixed',temporary:0,entered:true,editable:true,segments:[]}],materials:[],sales:[]}
  const state=harness(t,(path)=>path==='/api/order?id=history'?response(saved):null)
  await state.openOrder({id:'history'})
  await state.onOrderCompanyChanged();await state.projectChanged()
  assert.equal(state.orderDraft.value.project_id,'')
  assert.equal(state.orderDraft.value.address,'  历史原地址  ')
  await state.saveOrder()
  assert.equal(state.ctx.error.value,'')
  const payload=JSON.parse(posts(state)[0].init.body)
  assert.equal(payload.id,'history');assert.equal(payload.project_id,'');assert.equal(payload.address,'  历史原地址  ')
  assert.deepEqual(state.refreshed,[false])
})
