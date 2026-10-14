import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import {createRenderer,nextTick} from 'vue'
import ts from 'typescript'

registerHooks({resolve(specifier,context,nextResolve){
 if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')){
  const candidate=new URL(specifier,context.parentURL)
  if(existsSync(fileURLToPath(candidate)+'.ts'))return nextResolve(specifier+'.ts',context)
 }
 return nextResolve(specifier,context)
}})
const [{acceptanceGroups},{buildPayrollAction}]=await Promise.all([import('./printOrder.ts'),import('./payroll.ts')])
globalThis.Document??=class Document {}
globalThis.ShadowRoot??=class ShadowRoot {}
globalThis.document??={activeElement:null,addEventListener(){},removeEventListener(){}}
const moduleURL=source=>'data:text/javascript,'+encodeURIComponent(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
const cache=new Map()
async function component(name){
 const file=name instanceof URL?name:new URL(name==='App'?'./App.vue':'./components/'+name+'.vue',import.meta.url)
 if(cache.has(file.href))return cache.get(file.href)
 const {descriptor}=parse(await readFile(file,'utf8'),{filename:fileURLToPath(file)})
 const script=compileScript(descriptor,{id:'wage-detail-'+file.pathname})
 const js=ts.transpileModule(script.content,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText
 const imports=new Map()
 for(const match of js.matchAll(/from\s+(['"])([^'"]+)\1/g)){
  const path=match[2]
  // Only unrelated App pages remain isolated. Wage, project-work and all their
  // Vue descendants (SavedOrderWork, nine-column shell and total) are real.
  const unrelatedAppPage=name==='App'&&path.endsWith('.vue')&&!path.endsWith('WageFinancePanel.vue')
  imports.set(path,path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:
   path==='../../VERSION?raw'?'data:text/javascript,export default "1.05"':
   unrelatedAppPage?'data:text/javascript,export default {render(){return null}}':
   path.endsWith('.vue')?await component(new URL(path,file)):
   path.startsWith('.')?new URL(path.endsWith('.ts')?path:path+'.ts',file).href:import.meta.resolve(path))
 }
 const source=js.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(imports.get(path)))
 const template=compileTemplate({id:'wage-detail-'+file.pathname,source:descriptor.template.content,filename:fileURLToPath(file),compilerOptions:{bindingMetadata:script.bindings}})
 assert.deepEqual(template.errors,[])
 const renderURL=moduleURL(template.code.replaceAll('from "vue"','from '+JSON.stringify(new URL('../node_modules/vue/index.mjs',import.meta.url).href)))
 const url=moduleURL(source.replace(/^export default/m,'const component =')+'\nimport {render} from '+JSON.stringify(renderURL)+';component.render=render;export default component')
 cache.set(file.href,url);return url
}
const {default:PayrollDetailPanel}=await import(await component('PayrollDetailPanel'))
const {default:PayrollActionDialog}=await import(await component('PayrollActionDialog'))
const {default:ProjectWorkPanel}=await import(await component('ProjectWorkPanel'))
const {default:OrderPrint}=await import(await component('OrderPrint'))
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}
const line=(id,region,wage=300,labor=1800)=>({id,craft_id:'saved-waterproof',category:'防水涂料',name:'合成防水',spec:'保存材料规格',region,subitem_id:'saved-subitem-'+id,subitem_name:'配置-'+region,actual_location:region,description:'保存说明',mode:'平方',qty_micro:6000000,wage_cents:wage,labor_cents:labor,segments:[{mode:'平方',surface:'地面',length:2,width:3}]})
function mount(t,Component,props={},override=()=>null){
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
  if(path.startsWith('/api/payroll?'))return response({rows:[{id:'wa',order_id:'source-order',number:'O-1',work_date:'2026-10-08',worker_id:'a',worker_name:'历史甲',project_id:'p',project_name:'合成工地',wage_cents:1800,delivery_wage_cents:0,settled:false}],total:1,page:1,page_size:100})
  if(path.startsWith('/api/payroll-history?'))return response({rows:[{id:'history-a',entity_type:'deduction',entity_id:'deduction-a',event:'edit-deduction',reason:'更正扣款金额',before_snapshot:{amount_cents:5000},after_snapshot:{amount_cents:3000},actor_name:'管理员甲',operation_id:'op-a',created_at:'2026-10-12 10:00:00'}],total:1,page:1,page_size:100})
  throw new Error('Unexpected synthetic request '+path)
 })
 const app=renderer.createApp(Component,props),state=app.mount(root).$.setupState
 t.after(()=>app.unmount())
 const walk=node=>node?[node,...(node.children||[]).flatMap(walk)]:[],elements=()=>[...walk(root),...walk(portal)]
 const text=node=>!node||node.type==='comment'?'':(node.text||'')+(node.children||[]).map(text).join('')
 const content=()=>text(portal),button=label=>elements().find(node=>node.type==='button'&&text(node)===label)
 return {app,state,requests,content,button,elements,text:node=>node===undefined?text(root)+text(portal):text(node),root,portal}
}

test('payroll detail scopes wages to the selected worker and forwards date and project filters',async(t)=>{
 const worker={worker_id:'a',worker_name:'历史甲',order_count:1,gross_cents:1800,adjustment_cents:0,deduction_cents:0,advance_issued_cents:0,advance_applied_cents:0,advance_balance_cents:0,paid_cents:0,unpaid_cents:1800}
 const h=mount(t,PayrollDetailPanel,{worker});await settle()
 let request=h.requests.find(item=>item.path.startsWith('/api/payroll?'))
 let q=new URL('http://synthetic'+request.path).searchParams
 assert.equal(q.get('worker_id'),'a');assert.equal(q.get('tab'),'wages');assert.equal(q.get('page'),'1')
 assert.match(h.text(),/历史甲 · 工资明细.*施工单 O-1.*合成工地/)
 h.state.from='2026-10-01';h.state.to='2026-10-31';h.state.projectId='p';await h.state.applyFilters();await settle()
 request=h.requests.filter(item=>item.path.startsWith('/api/payroll?')).at(-1);q=new URL('http://synthetic'+request.path).searchParams
 assert.equal(q.get('worker_id'),'a');assert.equal(q.get('tab'),'wages');assert.equal(q.get('from'),'2026-10-01');assert.equal(q.get('to'),'2026-10-31');assert.equal(q.get('project_id'),'p');assert.equal(q.get('page'),'1')
})

test('payroll operation history uses the same worker, date and project filters and displays snapshots as text',async(t)=>{
 const worker={worker_id:'a',worker_name:'历史甲',order_count:1,gross_cents:1800,adjustment_cents:0,deduction_cents:0,advance_issued_cents:0,advance_applied_cents:0,advance_balance_cents:0,paid_cents:0,unpaid_cents:1800}
 const h=mount(t,PayrollDetailPanel,{worker});await settle()
 h.state.from='2026-10-01';h.state.to='2026-10-31';h.state.projectId='p';await h.button('操作历史').props.onClick();await settle()
 const request=h.requests.filter(item=>item.path.startsWith('/api/payroll-history?')).at(-1)
 const q=new URL('http://synthetic'+request.path).searchParams
 assert.equal(q.get('worker_id'),'a');assert.equal(q.get('from'),'2026-10-01');assert.equal(q.get('to'),'2026-10-31');assert.equal(q.get('project_id'),'p');assert.equal(q.get('page'),'1')
 assert.match(h.text(),/更正扣款.*管理员甲.*更正扣款金额.*amount_cents.*5000.*3000/s)
 assert.equal(h.button('登记扣款'),undefined);assert.equal(h.button('确认工资结算'),undefined)
 assert.equal(h.requests.every(item=>item.init?.method==='GET'||item.init?.method===undefined),true)
})

test('new payroll adjustments are positive supplements while reversal remains a separate action',async(t)=>{
 const base={worker_id:'a',amount:'12.34',effective_date:'2026-10-12',project_id:'',reason:'补发漏计工资'}
 assert.throws(()=>buildPayrollAction({action:'create-adjustment',...base,amount:'-12.34'}),/正数金额/)
 assert.equal(buildPayrollAction({action:'create-adjustment',...base}).amount_cents,1234)
 assert.deepEqual(buildPayrollAction({action:'reverse-adjustment',id:'adjustment-a',version:2,reason:'撤销重复补发'}),{action:'reverse-adjustment',id:'adjustment-a',version:2,reason:'撤销重复补发'})
 const h=mount(t,PayrollActionDialog,{action:'create-adjustment',workerId:'a'},path=>path==='/api/payroll-action'?response({ok:true}):null)
 assert.match(h.text(),/新增工资调整仅用于正数补发.*需要扣减工资时.*登记扣款/)
 assert.match(h.text(),/补发金额（元）/)
 h.state.amount='-12.34';h.state.reason='补发漏计工资'
 await h.elements().find(node=>node.type==='form').props.onSubmit({preventDefault(){}});await settle()
 assert.match(h.text(),/请输入正数金额/)
 assert.equal(h.requests.some(item=>item.path==='/api/payroll-action'),false)
 h.state.amount='12.34';await h.elements().find(node=>node.type==='form').props.onSubmit({preventDefault(){}});await settle()
 const request=h.requests.find(item=>item.path==='/api/payroll-action')
 assert.equal(request.init.method,'POST');assert.equal(JSON.parse(request.init.body).amount_cents,1234)
})

const workOrder=()=>({id:'order',company_id:'c',company_name:'合成公司',worker_id:'delivery',worker_name:'历史送货人',worker_names:'历史甲、历史乙',number:'O-1',work_date:'2026-10-08',address:'合成地址',status:'进行中',total_cents:20000,wage_cents:4098,delivery_wage_cents:0,materials:[],sales:[],
 lines:[{...line('a','主卫',233,1397),worker_id:'a',worker_name:'历史甲',qty_micro:6000000,waived_qty_micro:0,chargeable_qty_micro:6000000,price_cents:2000,total_cents:12000},
  {...line('b','厨房',450,2701),worker_id:'b',worker_name:'历史乙',qty_micro:4000000,waived_qty_micro:0,chargeable_qty_micro:4000000,price_cents:2000,total_cents:8000}],
 wage_records:[{id:'wa',order_id:'order',number:'O-1',work_date:'2026-10-08',worker_id:'a',worker_name:'历史甲',wage_cents:1397,delivery_wage_cents:0,wage_paid:1},
  {id:'wb',order_id:'order',number:'O-1',work_date:'2026-10-08',worker_id:'b',worker_name:'历史乙',wage_cents:2701,delivery_wage_cents:0,wage_paid:0}]})
test('finance work nine-column rows show saved workers, locations, dimensions, quantities and amounts; summary and selected ticket use distinct endpoints',async(t)=>{
 const order=workOrder(),ticket={id:'ticket',number:'P-1',kind:'normal',ticket_mode_snapshot:'single',status:'草稿',total_cents:20000,received_cents:0,outstanding_cents:20000,version:1}
 const h=mount(t,ProjectWorkPanel,{projectId:'p',canFinance:true,canWages:false,canCustomerPrices:true},path=>{
  if(path.startsWith('/api/project-work?'))return response({project:{id:'p',company_id:'c',ticket_mode:'single'},orders:[order],tickets:[ticket],total:1,page:1,page_size:25})
  if(path.startsWith('/api/project-ticket?'))return response({ticket,orders:[order],versions:[]})
  if(path.startsWith('/api/reference-options?'))return response({rows:[{id:'c',name:'合成公司',craft_category_order:[]}],total:1})
  if(path.startsWith('/api/reference?'))return response({crafts:[]})
  if(path==='/api/print-settings')return response({title:'合成验收单'})
  return null
 });await settle()
 const savedRows=h.elements().filter(node=>node.props?.class?.split(/\s+/).includes('saved-line-row'))
 assert.deepEqual(savedRows.map(node=>node.props['data-saved-line-id']),['a','b'])
 const categoryTitles=h.elements().filter(node=>node.props?.class==='category-title')
 assert.deepEqual(categoryTitles.map(h.text),['防水涂料'])
 assert.equal(h.state.sortingWarning,'')
 assert.ok(h.requests.some(({path})=>path==='/api/reference-options?entity=companies&id=c'))
 assert.ok(h.requests.some(({path})=>path.startsWith('/api/reference?tables=crafts&craft_scope=order&company_id=c&project_id=p')))
 const columns=['施工人员','工艺','部位','单价','方量明细','方量小计','金额小计','备注']
 for(const [index,row] of savedRows.entries()){
  const cells=row.children.filter(node=>node.props?.['data-label'])
  assert.deepEqual(cells.map(node=>node.props['data-label']),columns)
  assert.equal(h.text(cells[0]),order.lines[index].worker_name)
  assert.equal(h.text(cells[1]),order.lines[index].name+order.lines[index].mode+order.lines[index].spec)
  assert.equal(h.text(cells[2]),order.lines[index].subitem_name+'原独立部位：'+order.lines[index].actual_location)
  assert.match(h.text(cells[3]),/¥ 20\.00/)
  assert.equal(h.text(cells[4]),'地面：长 2 米 × 宽 3 米')
  // The second saved quantity is deliberately not rederived from the 2×3 dimensions.
  assert.equal(h.text(cells[5]),index===0?'6 ㎡':'4 ㎡')
  assert.equal(h.text(cells[6]),index===0?'¥ 120.00':'¥ 80.00')
  assert.equal(h.text(cells[7]),'保存说明')
  assert.doesNotMatch(h.text(row),/历史送货人|人工费|工资合计|13\.97|27\.01/)
 }
 assert.equal(h.elements().filter(node=>node.props?.role==='columnheader').length,9)
 const totals=h.elements().filter(node=>node.props?.class==='order-total-value')
 assert.equal(totals.length,1)
 assert.equal(h.text(totals[0]),'¥ 200.00')
 await h.button('预览 / 打印全工地').props.onClick();await settle()
 assert.ok(h.requests.some(({path})=>path==='/api/project-ticket?project_id=p'))
 assert.deepEqual(h.state.printOrder.lines.map(line=>[line.region,line.worker_name]),[['主卫','历史甲'],['厨房','历史乙']])
 await h.button('预览 / 打印').props.onClick();await settle()
 assert.ok(h.requests.some(({path})=>path==='/api/project-ticket?id=ticket'))
})

test('same craft and position by different workers remain separate printed rows with saved names and dates',()=>{
 const order=workOrder();order.lines[1].region='主卫';order.lines[0].work_date='2026-10-07';order.lines[1].work_date='2026-10-08'
 const groups=acceptanceGroups(order)
 assert.equal(groups.length,1);assert.equal(groups[0].rows.length,2)
 assert.deepEqual(groups[0].rows.map(row=>[row.region,row.worker_name,row.work_date]),[['配置-主卫；实际部位：主卫','历史甲','2026-10-07'],['配置-厨房；实际部位：厨房；原部位：主卫','历史乙','2026-10-08']])
 assert.equal(groups[0].total_cents,20000n)
})

test('customer summary prints workers at each location but excludes wages; internal summary adds saved wage amounts',async(t)=>{
 const order=workOrder(),h=mount(t,OrderPrint,{order,company:'合成公司',worker:'历史送货人',canWages:true})
 await settle()
 assert.match(h.content(),/历史甲.*主卫.*历史乙.*厨房/)
 assert.doesNotMatch(h.content(),/工资合计|人工费（元）|13\.97|27\.01|已发放|待发放/)
 await h.button('公司内部版').props.onClick();await settle()
 assert.match(h.content(),/人工费（元）.*13\.97.*27\.01.*工资合计.*40\.98/)
 assert.match(h.content(),/历史甲.*已有付款记录.*历史乙.*未记录付款/)
 await h.button('客户版').props.onClick();await settle()
 assert.doesNotMatch(h.content(),/工资合计|13\.97|27\.01/)
})
