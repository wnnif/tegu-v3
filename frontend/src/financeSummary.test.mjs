import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import {createRenderer,nextTick,reactive} from 'vue'
import ts from 'typescript'

registerHooks({resolve(specifier,context,nextResolve){
 if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')){
  const candidate=new URL(specifier,context.parentURL)
  if(existsSync(fileURLToPath(candidate)+'.ts'))return nextResolve(specifier+'.ts',context)
 }
 return nextResolve(specifier,context)
}})
const {acceptanceGroups}=await import('./printOrder.ts')
globalThis.Document??=class Document {}
globalThis.ShadowRoot??=class ShadowRoot {}
globalThis.document??={activeElement:null,addEventListener(){},removeEventListener(){}}
const moduleURL=source=>'data:text/javascript,'+encodeURIComponent(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
const cache=new Map()
async function component(name){
 if(cache.has(name))return cache.get(name)
 const file=new URL(name==='App'?'./App.vue':'./components/'+name+'.vue',import.meta.url)
 const {descriptor}=parse(await readFile(file,'utf8')),script=compileScript(descriptor,{id:'wage-detail-'+name})
 const imports=new Map()
 for(const match of script.content.matchAll(/from\s+(['"])([^'"]+)\1/g)){
  const path=match[2]
  imports.set(path,path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:path==='../../VERSION?raw'?'data:text/javascript,export default "1.05"':['WageFinancePanel','PayrollDetailPanel','FinanceBatch','TicketFinancePanel'].some(name=>path.endsWith(name+'.vue'))?await component(path.split('/').pop().replace('.vue','')):path.endsWith('.vue')?'data:text/javascript,export default {render(){return null}}':new URL(path+'.ts',file).href)
 }
 const source=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(imports.get(path)))
 const template=compileTemplate({id:'wage-detail-'+name,source:descriptor.template.content,filename:name+'.vue',compilerOptions:{bindingMetadata:script.bindings}})
 assert.deepEqual(template.errors,[])
 const renderURL=moduleURL(template.code.replaceAll('from "vue"','from '+JSON.stringify(new URL('../node_modules/vue/index.mjs',import.meta.url).href)))
 const url=moduleURL(source.replace(/^export default/m,'const component =')+'\nimport {render} from '+JSON.stringify(renderURL)+';component.render=render;export default component')
 cache.set(name,url);return url
}
const {default:App}=await import(await component('App'))
const {default:TicketFinancePanel}=await import(await component('TicketFinancePanel'))
const {default:WageFinancePanel}=await import(await component('WageFinancePanel'))
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done});return {promise,resolve}}
function mount(t,Component,props={},override=()=>null){
 if(Component===TicketFinancePanel)props={canCustomerPrices:true,...props}
 const root={type:'root',children:[]},portal={type:'body',children:[]},requests=[]
 function insert(node,parent,anchor=null){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1);const index=anchor?parent.children.indexOf(anchor):-1;parent.children.splice(index<0?parent.children.length:index,0,node);node.parent=parent}
 const renderer=createRenderer({
  createElement:type=>({type,tagName:type.toUpperCase(),children:[],props:{},value:'',listeners:{},style:{},
   get options(){return this.children.filter(child=>child.type==='option')},addEventListener(name,fn){this.listeners[name]=fn},getRootNode(){return globalThis.document},setAttribute(){},removeAttribute(){}}),
  createText:text=>({type:'text',text,children:[]}),createComment:text=>({type:'comment',text,children:[]}),
  setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text;node.children=[]},
  patchProp(node,key,previous,next){node.props[key]=next;node[key]=next},insert,
  remove(node){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1)},parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,
  querySelector:selector=>selector==='body'?portal:null,
  insertStaticContent(content,parent,anchor){const node={type:'static',text:content.replace(/<[^>]+>/g,''),children:[]};insert(node,parent,anchor);return [node,node]},
 })
 t.mock.method(globalThis,'fetch',async(path,init)=>{
  requests.push({path,init})
  const alternate=override(path,init);if(alternate)return alternate
  if(path==='/api/me')return response({user:{id:'admin',role:'admin',permissions:[],crafts:['防水涂料']},categories:['防水涂料']})
  if(path==='/api/reference')return response({companies:[],workers:[],products:[],warehouses:[],crafts:[]})
  if(path.startsWith('/api/payroll-workers?'))return response(payrollSummary())
  if(path.startsWith('/api/finance-companies?'))return response(companySummary())
  if(path.startsWith('/api/payroll-history?'))return response(payrollHistory())
  if(path.startsWith('/api/payroll?'))return response(payrollRows())
  if(path.startsWith('/api/orders?'))return response({rows:[{id:'order-a',address:'合成地址',number:'O-A',company_id:'company-a',company_name:'合成公司',work_date:'2026-10-08',worker_name:'同名师傅',total_cents:3000,status:'进行中'}],total:1,total_cents:3000,page:1,page_size:100})
  if(path.startsWith('/api/project-tickets?'))return response(ticketRows())
  throw new Error('Unexpected synthetic request '+path)
 })
 const app=renderer.createApp(Component,props),state=app.mount(root).$.setupState
 t.after(()=>app.unmount())
 const walk=node=>[node,...node.children.flatMap(walk)],elements=()=>[...walk(root),...walk(portal)]
 const text=node=>node.type==='comment'?'':(node.text||'')+node.children.map(text).join('')
 const content=()=>text(portal),button=label=>elements().find(node=>node.type==='button'&&text(node)===label)
 return {app,state,requests,content,button,elements,text:()=>text(root)+text(portal),child:()=>app._instance.subTree.children?.find(v=>v.component?.type?.__name==='WageFinancePanel')?.component?.setupState}
}

const payrollSummary=()=>({rows:[{worker_id:'worker-a',worker_name:'同名师傅',order_count:2,gross_cents:3000,adjustment_cents:250,deduction_cents:100,advance_issued_cents:500,advance_applied_cents:200,advance_balance_cents:300,paid_cents:500,unpaid_cents:2450},{worker_id:'worker-b',worker_name:'同名师傅',order_count:1,gross_cents:0,adjustment_cents:0,deduction_cents:0,advance_issued_cents:0,advance_applied_cents:0,advance_balance_cents:0,paid_cents:0,unpaid_cents:0}],total:103,page:1,page_size:100})
const companySummary=()=>({rows:[{company_id:'company-a',company_name:'同名公司',ticket_count:2,total_cents:10000,received_cents:2000,outstanding_cents:8000},{company_id:'company-b',company_name:'同名公司',ticket_count:1,total_cents:0,received_cents:0,outstanding_cents:0}],total:102,page:1,page_size:100,total_cents:88800,received_cents:4000,outstanding_cents:84800})
const payrollRows=()=>({rows:[{id:'wage-a',order_id:'order-multi',number:'O-A',work_date:'2026-10-08',wage_cents:2000,delivery_wage_cents:500,settled:false,project_name:'合成工地',address:'合成地址'}],total:1,page:1,page_size:100})
const payrollHistory=()=>({rows:[],total:0,page:1,page_size:100})
const ticketRows=()=>({rows:[{id:'ticket-a',project_id:'project-a',project_name:'合成工地',address:'合成旧月地址',number:'P-A',kind:'normal',ticket_mode_snapshot:'single',version:3,status:'部分收款',total_cents:10000,received_cents:2000,outstanding_cents:8000}],total:101,page:1,total_cents:100000,received_cents:10000,outstanding_cents:90000})
const last=(h,prefix)=>h.requests.filter(({path})=>path.startsWith(prefix)).at(-1)
const params=path=>new URL('http://synthetic'+path).searchParams

test('payroll worker summary renders ledger totals and opens that worker payroll detail',async(t)=>{
 const h=mount(t,WageFinancePanel);await settle()
 const summaryRequest=last(h,'/api/payroll-workers?')
 assert.equal(params(summaryRequest.path).get('q'),'');assert.equal(params(summaryRequest.path).get('page'),'1')
 assert.match(h.text(),/工人工资汇总.*共 103 位施工人/)
 assert.match(h.text(),/累计施工工资.*工资调整.*已确认扣款.*工资预支.*实际工资付款.*剩余未发工资/)
 assert.match(h.text(),/同名师傅.*¥ 30\.00.*¥ 2\.50.*¥ 1\.00.*¥ 5\.00.*¥ 2\.00.*¥ 3\.00.*¥ 5\.00.*¥ 24\.50/)
 assert.equal(h.requests.some(r=>r.path.startsWith('/api/orders?')),false)
 await h.button('查看工资明细').props.onClick();await settle()
 const q=params(last(h,'/api/payroll?').path)
 assert.equal(q.get('worker_id'),'worker-a');assert.equal(q.get('tab'),'wages');assert.equal(q.get('page'),'1')
 assert.match(h.text(),/同名师傅 · 工资明细.*施工单 O-A/)
})

test('payroll summary worker search and paging retain the API filters and export all matches',async(t)=>{
 const h=mount(t,WageFinancePanel);await settle()
 h.state.query='张三';h.button('查询').props.onClick();await settle()
 let q=params(last(h,'/api/payroll-workers?').path)
 assert.equal(q.get('q'),'张三');assert.equal(q.get('page'),'1')
 assert.equal(params(h.elements().find(n=>n.type==='a').props.href).get('export'),'1')
 await h.button('下一页').props.onClick();await settle()
 q=params(last(h,'/api/payroll-workers?').path)
 assert.equal(q.get('q'),'张三');assert.equal(q.get('page'),'2')
})

test('stale payroll summary responses cannot replace a newer search or survive unmount',async(t)=>{
 const wait=deferred();let first=true
 const h=mount(t,WageFinancePanel,{},path=>{if(first&&path.startsWith('/api/payroll-workers?')){first=false;return wait.promise}return null})
 await settle();h.state.query='新筛选';h.state.load('新筛选');await settle();wait.resolve(response({...payrollSummary(),rows:[{worker_id:'old',worker_name:'过期人员'}]}));await settle();assert.equal(h.state.workers.some(r=>r.worker_id==='old'),false)
 const second=deferred();t.mock.method(globalThis,'fetch',async()=>second.promise);const pending=h.state.refresh();await settle();h.app.unmount();second.resolve(response(payrollSummary()));await pending;assert.deepEqual(h.state.workers,[])
})

test('App payroll entry renders the ledger and blocks workers without wages permission',async(t)=>{
 const h=mount(t,App);await settle();h.state.go('wages');await settle();assert.match(h.text(),/工人工资汇总/)
 assert.equal(h.requests.filter(r=>r.path.startsWith('/api/payroll-workers?')).length,1)
 assert.equal(h.requests.some(r=>r.path.startsWith('/api/wage-workers?')||r.path.startsWith('/api/orders?')),false)
 await h.button('查看工资明细').props.onClick();await settle()
 assert.ok(h.requests.some(r=>params(r.path).get('worker_id')==='worker-a'&&r.path.startsWith('/api/payroll?')))
 h.state.clearSession();await settle();assert.doesNotMatch(h.text(),/同名师傅|工人工资汇总/)
 h.state.user={id:'employee',role:'employee',permissions:['orders']};await settle();h.state.go('wages');await settle()
 assert.equal(h.state.page,'dashboard');assert.equal(h.state.can('wages'),false)
 assert.equal(h.requests.filter(r=>r.path.startsWith('/api/payroll-workers?')).length,1)
 h.state.user={id:'admin',role:'admin',permissions:[]};h.state.go('wages');await settle();await h.button('月度归档').props.onClick();await settle();assert.equal(h.state.page,'archives')
 h.state.go('orders');await settle();assert.ok(h.button('查看 / 编辑'));assert.equal(h.button('登记实际发放'),undefined)
})

test('payroll worker summary pagination and export keep the active query',async(t)=>{
 const h=mount(t,WageFinancePanel);await settle()
 await h.button('下一页').props.onClick();await settle()
 assert.equal(params(last(h,'/api/payroll-workers?').path).get('page'),'2')
 assert.equal(params(h.elements().find(n=>n.type==='a').props.href).get('export'),'1')
 assert.equal(params(h.elements().find(n=>n.type==='a').props.href).get('page'),null)
})

test('实际账号表单按独立customer_prices checkbox授撤，不联动模块工艺工资财务',async(t)=>{
 const {default:UserEditor}=await import(await component('UserEditor'))
 const editing=reactive({id:'employee',version:2,name:'合成员工',username:'synthetic',role:'employee',password:'',active:true,permissions:['orders'],crafts:['public-water']});let saves=0
 const h=mount(t,UserEditor,{editing,categories:['防水涂料'],permissionNames:{orders:'施工制单',customer_prices:'查看客户价格',wages:'工资管理',finance:'应收应付'},busy:false,canAssignCombinations:true,onSave:()=>{saves++}},path=>path.startsWith('/api/reference-options?')?response({rows:[],total:0,page:1}):null);await settle()
 const checkbox=h.elements().find(node=>node.tagName==='INPUT'&&node.props['data-permission']==='customer_prices');assert.ok(checkbox,'实际渲染customer_prices权限框');assert.equal(checkbox.props['aria-label'],'查看客户价格');assert.equal(checkbox.checked,false)
 checkbox.checked=true;checkbox.listeners.change();await settle();assert.deepEqual(editing.permissions,['orders','customer_prices']);assert.deepEqual(editing.crafts,['public-water'])
 checkbox.checked=false;checkbox.listeners.change();await settle();assert.deepEqual(editing.permissions,['orders']);assert.deepEqual(editing.crafts,['public-water'])
 const form=h.elements().find(node=>node.type==='form');form.props.onSubmit({preventDefault(){}});await settle();assert.equal(saves,1);assert.ok(h.button('保存账号'));assert.equal(h.requests.length,1)
 const grantQuery=params(h.requests[0].path);assert.equal(grantQuery.get('entity'),'crafts');assert.equal(grantQuery.has('company_id'),false);assert.equal(grantQuery.has('craft_scope'),false);assert.equal(h.requests[0].init.method,'GET')
})

// Phase 4B centralized receipts and customer-price boundaries: receiptManagement.test.mjs.
