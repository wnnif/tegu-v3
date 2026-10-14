import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import {createRenderer,nextTick} from 'vue'
import ts from 'typescript'
import {pageAllowed} from './sessionAccess.ts'

registerHooks({resolve(specifier,context,nextResolve){
 if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')){
  const candidate=new URL(specifier,context.parentURL)
  if(existsSync(fileURLToPath(candidate)+'.ts'))return nextResolve(specifier+'.ts',context)
 }
 return nextResolve(specifier,context)
}})
globalThis.Document??=class Document{}
globalThis.ShadowRoot??=class ShadowRoot{}
globalThis.document??={activeElement:null,addEventListener(){},removeEventListener(){}}
const moduleURL=source=>'data:text/javascript,'+encodeURIComponent(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
const cache=new Map()
async function component(name){
 if(cache.has(name))return cache.get(name)
 const file=new URL('./components/'+name+'.vue',import.meta.url)
 const {descriptor}=parse(await readFile(file,'utf8')),script=compileScript(descriptor,{id:'ticket-issue-'+name}),imports=new Map()
 for(const match of script.content.matchAll(/from\s+(['"])([^'"]+)\1/g)){
  const path=match[2]
  imports.set(path,path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:path.endsWith('.vue')?await component(path.split('/').pop().replace('.vue','')):new URL(path+'.ts',file).href)
 }
 const source=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(imports.get(path)))
 const template=compileTemplate({id:'ticket-issue-'+name,source:descriptor.template.content,filename:name+'.vue',compilerOptions:{bindingMetadata:script.bindings}})
 assert.deepEqual(template.errors,[])
 const render=moduleURL(template.code.replaceAll('from "vue"','from '+JSON.stringify(new URL('../node_modules/vue/index.mjs',import.meta.url).href)))
 const url=moduleURL(source.replace(/^export default/m,'const component =')+'\nimport {render} from '+JSON.stringify(render)+';component.render=render;export default component')
 cache.set(name,url);return url
}
const {default:TicketIssuePanel}=await import(await component('TicketIssuePanel'))
const {default:OrderSalesEditor}=await import(await component('OrderSalesEditor'))
const {default:CompanyTicketPrint}=await import(await component('CompanyTicketPrint'))
const {default:OrderPrint}=await import(await component('OrderPrint'))
const projectPanelSource=await readFile(new URL('./components/ProjectWorkPanel.vue',import.meta.url),'utf8')
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}
const row=(id,project,status='pending',total=1234,kind='line')=>({item_id:id,item_kind:kind,order_id:'order-'+id,order_version:3,work_date:'2026-10-12',order_number:'施工-'+id,project_id:project,project_name:project==='p1'?'工地甲':'工地乙',address:project==='p1'?'甲地址':'乙地址',item_name:kind==='sale'?'销售砂浆':'防水施工',subitem_name:'配置厨房',location:'厨房东墙',quantity:'2',unit:'平方',total_cents:total,status})
const reminders=[
 {project_id:'p1',project_name:'工地甲',address:'甲地址',craft_id:'c1',craft_name:'防水',subitem_id:'s1',subitem_name:'主卫',status:'no_work',total_count:0,pending_count:0,delivered_count:0},
 {project_id:'p1',project_name:'工地甲',address:'甲地址',craft_id:'c1',craft_name:'防水',subitem_id:'s2',subitem_name:'厨房',status:'pending',total_count:2,pending_count:2,delivered_count:0},
 {project_id:'p2',project_name:'工地乙',address:'乙地址',craft_id:'c2',craft_name:'维修',subitem_id:'s3',subitem_name:'阳台',status:'delivered',total_count:1,pending_count:0,delivered_count:1},
 {project_id:'p2',project_name:'工地乙',address:'乙地址',craft_id:'c2',craft_name:'维修',subitem_id:'s4',subitem_name:'客厅',status:'mixed',total_count:3,pending_count:1,delivered_count:2},
 {project_id:'p2',project_name:'工地乙',address:'乙地址',craft_id:'c3',craft_name:'原工艺',subitem_id:'s5',subitem_name:'原部位',status:'no_work',unmapped:true,total_count:2,pending_count:1,delivered_count:1},
]
function mount(t,{allowed=true,override=()=>null,view=TicketIssuePanel,props={canFinance:allowed,canCustomerPrices:allowed}}={}){
 const root={type:'root',children:[]},portal={type:'body',children:[]},requests=[];let created=false,voided=false
 function insert(node,parent,anchor=null){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1);const index=anchor?parent.children.indexOf(anchor):-1;parent.children.splice(index<0?parent.children.length:index,0,node);node.parent=parent}
 const renderer=createRenderer({
  createElement:type=>({type,tagName:type.toUpperCase(),children:[],props:{},value:'',listeners:{},style:{},get options(){return this.children.filter(child=>child.type==='option')},addEventListener(name,fn){this.listeners[name]=fn},getRootNode(){return globalThis.document},setAttribute(){},removeAttribute(){}}),
  createText:text=>({type:'text',text,children:[]}),createComment:text=>({type:'comment',text,children:[]}),
  setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text;node.children=[]},
  patchProp(node,key,previous,next){node.props[key]=next;node[key]=next},insert,
  remove(node){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1)},parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,
  insertStaticContent(content,parent,anchor){const node={type:'static',text:content.replace(/<[^>]+>/g,''),children:[]};insert(node,parent,anchor);return [node,node]},querySelector:selector=>selector==='body'?portal:null,
 })
 t.mock.method(globalThis,'fetch',async(path,init)=>{
  requests.push({path,init})
  const alternate=override(path,init);if(alternate)return alternate
  if(path.startsWith('/api/reference-options?'))return response({rows:[{id:'company-a',name:'示例公司'}],total:1})
  if(path.startsWith('/api/ticket-issue?')){
   const rows=created&&!voided?[row('zero','p1','delivered',0),row('sale-b','p2','delivered',2400,'sale'),row('already','p2','delivered',500)]:[row('zero','p1','pending',0),row('sale-b','p2','pending',2400,'sale'),row('already','p2','delivered',500)]
   rows.push(row('unlinked','p1','unlinked',800))
   return response({company:{id:'company-a',name:'示例公司'},rows,reminders,batches:created?[{id:'batch-a',company_id:'company-a',company_name:'示例公司',number:'B-1',ticket_no:'A001',delivered_at:'2026-10-12',recipient:'收票人甲',status:voided?'voided':'delivered',version:voided?2:1,total_cents:2400,created_by_name:'操作员甲',...(voided?{void_reason:'票面信息错误',voided_by_name:'管理员甲'}:{})}]:[],total:4,page:1,page_size:100,batch_total:created?1:0,batch_page:1,batch_page_size:20})
  }
  if(path==='/api/ticket-batch'&&init?.method==='POST'){created=true;return response({batch:{id:'batch-a'}})}
  if(path==='/api/ticket-batch?id=batch-a')return response({batch:{id:'batch-a',company_id:'company-a',company_name:'示例公司',number:'B-1',ticket_no:'A001',delivered_at:'2026-10-12',recipient:'收票人甲',status:'delivered',version:1,total_cents:2400},items:[row('sale-b','p2','delivered',2400,'sale')]})
  if(path==='/api/ticket-batch-action'&&init?.method==='POST'){voided=true;return response({batch:{id:'batch-a',status:'voided'}})}
  throw new Error('Unexpected synthetic request '+path)
 })
 const app=renderer.createApp(view,props),state=app.mount(root).$.setupState
 t.after(()=>app.unmount())
 const walk=node=>[node,...node.children.flatMap(walk)],elements=()=>[...walk(root),...walk(portal)],text=node=>node.type==='comment'?'':(node.text||'')+node.children.map(text).join('')
 return {app,state,requests,elements,text:()=>text(root)+text(portal),textNode:text,nodes:selector=>elements().filter(selector)}
}
const params=path=>new URL(path,'http://test.local').searchParams

test('公司与独立工地打印使用同一保存金额且免费补做售料和历史缺省可核对',async(t)=>{
 const rows=[
  {...row('normal','p1','pending','9007199254740991'),qty_micro:'1234567',price_cents:1200,segments:'[{"surface":"墙面","length":1,"width":1.234567}]',description:'人工特殊收费'},
  {...row('free','p1','pending',0),qty_micro:'2000000',price_cents:1200,charge_mode:'free'},
  {...row('redo','p2','pending',0),qty_micro:'1000000',price_cents:1200,charge_mode:'redo'},
  {...row('sale','p2','pending',2,'sale'),qty_micro:'2000000',price_cents:1,unit:'袋'},
 ]
 const before=JSON.stringify(rows),h=mount(t,{view:CompanyTicketPrint,props:{companyName:'保存公司',rows,mode:'company'}});await settle()
 assert.equal(h.state.total,9007199254740993n)
 assert.match(h.text(),/90071992547409\.93/)
 assert.match(h.text(),/免费施工/);assert.match(h.text(),/补做不收费/);assert.match(h.text(),/人工特殊收费/)
 assert.match(h.text(),/1\.234567 ㎡.*墙面：1 × 1\.234567.*单价 ¥ 12\.00/)
 assert.equal(h.state.projects[0].total+h.state.projects[1].total,h.state.total)
 const projects=mount(t,{view:CompanyTicketPrint,props:{companyName:'保存公司',rows,mode:'projects'}});await settle()
 assert.match(projects.text(),/工地合计：¥ 90071992547409\.91/);assert.match(projects.text(),/工地合计：¥ 0\.02/)
 assert.doesNotMatch(h.text()+projects.text(),/工资|罚款|库存货值|预支/)
 assert.equal(h.requests.length+projects.requests.length,0)
 assert.equal(JSON.stringify(rows),before)
 const old=mount(t,{view:CompanyTicketPrint,props:{companyName:'保存公司',rows:[row('old','p1')],mode:'projects'}});await settle()
 assert.match(old.text(),/原单价未记录/)
})

test('公司与工地打印表格窄屏可横向滚动且纸版恢复页面宽度',async()=>{
 const source=await readFile(new URL('./components/CompanyTicketPrint.vue',import.meta.url),'utf8')
 const css=source.match(/<style scoped>([\s\S]*?)<\/style>/)?.[1]||''
 assert.match(css,/\.ticket-document \.table-wrap\{[^}]*overflow-x:auto/)
 assert.match(css,/\.ticket-document table\{width:100%;min-width:720px/)
 assert.match(css,/@media print\{[\s\S]*?\.ticket-print-modal \.table-wrap,\.ticket-document \.table-wrap\{max-width:none;overflow:visible!important\}/)
 assert.match(css,/@media print\{[\s\S]*?\.ticket-document table\{width:100%;min-width:0!important\}/)
})

test('无工资权限时强制内部打印选择仍不能渲染工资或耗材记录',async(t)=>{
 const order={id:'saved',company_id:'co',worker_id:'w',number:'S',address:'保存地址',work_date:'2026-10-12',status:'进行中',total_cents:2469,wage_cents:9876,lines:[{id:'line',craft_id:'c',category:'维修',name:'已保存工艺',subitem_name:'保存厨房',actual_location:'北墙',mode:'平方',qty_micro:1234567,price_cents:2000,total_cents:2469,labor_cents:9876}],materials:[{product_name:'内部耗材',warehouse_name:'内部仓',qty_micro:1000000,unit:'桶'}]}
 const before=JSON.stringify(order),h=mount(t,{view:OrderPrint,props:{order,company:'保存公司',worker:'施工人',canWages:false}});h.state.mode='internal';await settle()
 assert.equal(h.state.internal,false)
 assert.match(h.text(),/保存厨房；实际部位：北墙/);assert.match(h.text(),/24\.69 元/)
 assert.doesNotMatch(h.text(),/工资合计|人工费|内部耗材|内部仓|98\.76/)
 assert.equal(h.requests.length,0);assert.equal(JSON.stringify(order),before)
})

test('实际交票预览传递保存收费与方量字段，不能落回旧缺省或转成新的金额',async(t)=>{
 const saved={...row('snapshot','p1','pending',2469),qty_micro:'1234567',price_cents:2000,segments:'[{"surface":"墙面","length":1,"width":1.234567}]',charge_mode:'free',charge_note:'原保存收费说明',description:'保存特殊说明',region:'保存旧部位'}
 const h=mount(t,{override:path=>path.startsWith('/api/ticket-issue?')?response({company:{id:'company-a',name:'保存公司'},rows:[saved],reminders:[],batches:[],total:1,page:1,page_size:100,batch_total:0}):null});await settle()
 h.state.companyId='company-a';await h.state.load();h.state.toggleRow(h.state.rows[0],{target:{checked:true}});h.state.preview('company');await settle()
 const projected=h.state.printState.rows[0]
 for(const key of ['qty_micro','price_cents','segments','charge_mode','charge_note','description','region'])assert.equal(projected[key],saved[key],key)
 assert.match(h.text(),/免费施工.*原保存收费说明/);assert.match(h.text(),/1\.234567 ㎡/)
 assert.equal(h.state.chosenTotal,2469n);assert.equal(h.requests.some(item=>item.init?.method==='POST'),false)
})

test('ticket issue renders reminder states, selects cross-site rows including zero price, and print does not POST',async(t)=>{
 let printCount=0;globalThis.window??={};globalThis.window.print??=()=>{};t.mock.method(globalThis.window,'print',()=>{printCount++})
 const h=mount(t);await settle()
 const companySelect=h.nodes(node=>node.type==='select')[0];companySelect.value='company-a';companySelect.props.onChange({target:{value:'company-a'}});await settle()
 assert.equal(params(h.requests.find(item=>item.path.startsWith('/api/ticket-issue?')).path).get('company_id'),'company-a')
 assert.match(h.text(),/工地甲.*工地乙/);for(const label of ['尚无施工记录（当前配置）','已有施工，尚未交票','已交票','施工与交票状态混合','关联待核对','配置已变更，已有施工需核对','待交 1 · 已交 1'])assert.ok(h.text().includes(label),label)
 assert.equal(h.state.rows.length,4)
 for(const item of h.state.rows)h.state.toggleRow(item,{target:{checked:true}});await settle()
 assert.equal(h.state.chosenRows.length,2);assert.equal(h.state.chosenTotal,2400n);assert.equal(h.state.chosenRows.some(item=>item.total_cents===0),true)
 assert.equal(h.state.chosenRows.some(item=>item.item_kind==='sale'),true)
 h.state.preview('company');await settle();assert.match(h.text(),/示例公司施工收费汇总/);assert.equal(h.state.printState.rows[0].subitem_name,'配置厨房');assert.equal(h.state.printState.rows[0].location,'厨房东墙');assert.match(h.text(),/配置厨房.*实际部位：厨房东墙/);assert.equal(h.requests.some(item=>item.init?.method==='POST'),false)
 const printButton=h.nodes(node=>node.type==='button'&&h.textNode(node)==='打印')[0]
 printButton.props.onClick();assert.equal(printCount,1);assert.equal(h.requests.some(item=>item.init?.method==='POST'),false)
 assert.equal(pageAllowed({id:'employee',role:'employee',permissions:['finance']},'ticket-issue'),false)
 assert.equal(pageAllowed({id:'employee',role:'employee',permissions:['finance','customer_prices']},'ticket-issue'),true)
})

test('delivery posts selected IDs and order versions; snapshot previews locally; void records its reason without affecting another ticket',async(t)=>{
 const h=mount(t);await settle();h.state.companyId='company-a';await h.state.load();await settle()
 h.state.selected=new Map([['sale:sale-b',row('sale-b','p2','pending',2400,'sale')]])
 h.state.deliveredAt='2026-10-12';h.state.recipient='收票人甲';h.state.ticketNo='A001';await h.state.confirmDelivered();await settle()
 const create=h.requests.find(item=>item.path==='/api/ticket-batch'&&item.init?.method==='POST')
 assert.ok(create);assert.deepEqual(JSON.parse(create.init.body),{company_id:'company-a',items:[{item_id:'sale-b',item_kind:'sale',order_version:3}],ticket_no:'A001',delivered_at:'2026-10-12',recipient:'收票人甲'})
 await h.state.openBatch({id:'batch-a'});await settle();assert.equal(h.state.batchDetail.items.length,1)
 assert.match(h.text(),/配置厨房.*实际部位：厨房东墙/)
 h.state.preview('projects',h.state.batchDetail);await settle();assert.match(h.text(),/示例公司 · 工地乙/)
 assert.equal(h.state.printState.rows[0].subitem_name,'配置厨房');assert.equal(h.state.printState.rows[0].location,'厨房东墙')
 h.state.printState=null;h.state.batchDetail=null;await h.state.openBatch({id:'batch-a'});await settle();h.state.voidReason='票面信息错误';await h.state.voidBatch();await settle()
 const action=h.requests.find(item=>item.path==='/api/ticket-batch-action'&&item.init?.method==='POST')
 assert.deepEqual(JSON.parse(action.init.body),{id:'batch-a',version:1,action:'void',reason:'票面信息错误'})
 assert.equal(h.state.batches[0].status,'voided');assert.equal(h.state.rows.find(item=>item.item_id==='already').status,'delivered')
})

test('batch history pages independently of candidate pages and retains selected pending rows',async(t)=>{
 const h=mount(t,{override:path=>{
  if(!path.startsWith('/api/ticket-issue?'))return null
  const query=params(path),page=Number(query.get('page')),batchPage=Number(query.get('batch_page'))
  return response({company:{id:'company-a',name:'示例公司'},rows:[row('page-'+page,'p1')],reminders:[],batches:[],total:201,page,page_size:100,batch_total:21,batch_page:batchPage,batch_page_size:20})
 }})
 await settle();h.state.companyId='company-a';await h.state.load();h.state.toggleRow(h.state.rows[0],{target:{checked:true}})
 h.state.turn(2);await settle();assert.equal(h.state.page,2)
 h.state.turnBatch(2);await settle()
 const request=h.requests.filter(item=>item.path.startsWith('/api/ticket-issue?')).at(-1)
 assert.equal(params(request.path).get('page'),'2');assert.equal(params(request.path).get('batch_page'),'2');assert.equal(params(request.path).get('batch_page_size'),'20')
 assert.equal(h.state.page,2);assert.equal(h.state.batchPage,2);assert.equal(h.state.chosenRows.length,1);assert.equal(h.state.chosenRows[0].item_id,'page-1')
 assert.equal(h.state.chosenRows[0].order_version,3)
})

test('version conflict preserves selected rows and inputs; refresh updates versions and requires a fresh candidate preview',async(t)=>{
 let serverVersion=3,confirmed=false
 const h=mount(t,{override:(path,init)=>{
  if(path.startsWith('/api/ticket-issue?'))return response({company:{id:'company-a',name:'示例公司'},rows:[{...row('changed','p1',confirmed?'delivered':'pending',serverVersion===3?1000:2000),order_version:serverVersion}],reminders:[],batches:[],total:1,page:1,page_size:100,batch_total:0,batch_page:1,batch_page_size:20})
  if(path==='/api/ticket-batch'&&init?.method==='POST'){
   if(JSON.parse(init.body).items[0].order_version!==serverVersion)return response({error:'施工单内容已变化，请刷新后重新预览再确认交票'},409)
   confirmed=true;return response({batch:{id:'correct-version'}})
  }
  return null
 }})
 await settle();h.state.companyId='company-a';await h.state.load();h.state.toggleRow(h.state.rows[0],{target:{checked:true}});h.state.recipient='收票人甲';h.state.ticketNo='纸票A'
 h.state.preview('company');await settle();assert.equal(h.state.printState.rows[0].total_cents,1000)
 h.state.printState=null;serverVersion=4;await h.state.confirmDelivered();await settle()
 assert.equal(confirmed,false);assert.equal(h.state.busy,false);assert.equal(h.state.chosenRows.length,1);assert.equal(h.state.chosenRows[0].order_version,3);assert.equal(h.state.recipient,'收票人甲');assert.equal(h.state.ticketNo,'纸票A')
 assert.match(h.text(),/施工单内容已变化，请刷新后重新预览再确认交票/)
 assert.equal(JSON.parse(h.requests.find(item=>item.path==='/api/ticket-batch').init.body).items[0].order_version,3)
 h.state.preview('company');await h.state.refresh();await settle()
 assert.equal(h.state.error,'');assert.equal(h.state.chosenRows.length,1);assert.equal(h.state.chosenRows[0].order_version,4);assert.equal(h.state.chosenTotal,2000n);assert.equal(h.state.printState,null);assert.match(h.text(),/已选内容已更新，请重新预览或打印并核对纸票/)
 h.state.preview('company');await settle();assert.equal(h.state.printState.rows[0].total_cents,2000);h.state.printState=null
 await h.state.confirmDelivered();await settle();assert.equal(confirmed,true)
 assert.equal(JSON.parse(h.requests.filter(item=>item.path==='/api/ticket-batch').at(-1).init.body).items[0].order_version,4)
})

test('a delivered sale row is protected while another pending sale remains editable',async(t)=>{
 const sale=(id,editable)=>({id,editable,product_id:'product-a',product_name:'砂浆',unit:'袋',input_unit:'base',input_qty:'2',qty_micro:2000000,price_cents:100,warehouse_id:'warehouse-a'})
 const locked=sale('claimed',false),pending=sale('pending',true),draft={sales:[locked,pending],delivery_wage_cents:0}
 const h=mount(t,{view:OrderSalesEditor,props:{draft,references:{products:[{id:'product-a',name:'砂浆',unit:'袋'}],warehouses:[],companies:[],workers:[],crafts:[]},inventory:[],warehouse:'warehouse-a',disabled:false,canWarehouse:true,canWages:true,canCustomerPrices:true}})
 await settle()
 assert.match(h.text(),/本行已交票，请先作废所属交票批次后再修改/)
 const walk=node=>[node,...node.children.flatMap(walk)],articles=h.nodes(node=>node.type==='article')
 const numericControls=article=>walk(article).filter(node=>node.tagName==='INPUT'&&node.props.type==='number')
 assert.equal(numericControls(articles[0]).length,2);assert.equal(numericControls(articles[1]).length,2)
 for(const control of numericControls(articles[0]))assert.equal(control.props.disabled,true)
 for(const control of numericControls(articles[1]))assert.equal(control.props.disabled,false)
 const removeButtons=h.nodes(node=>node.type==='button'&&h.textNode(node)==='移除')
 assert.equal(removeButtons[0].props.disabled,true);assert.equal(removeButtons[1].props.disabled,false)
 h.state.setQuantity(locked,'9');h.state.setPrice(locked,{target:{value:'9'}});h.state.changeUnit(locked,{target:{value:'aux'}});removeButtons[0].props.onClick()
 assert.equal(locked.input_qty,'2');assert.equal(locked.price_cents,100);assert.equal(locked.input_unit,'base');assert.equal(draft.sales.length,2)
 h.state.setQuantity(pending,'3');h.state.setPrice(pending,{target:{value:'2'}})
 assert.equal(pending.input_qty,'3');assert.equal(pending.price_cents,200)
 removeButtons[1].props.onClick();assert.deepEqual(draft.sales,[locked])
})

test('dual permission UI guard makes no ticket list request for an unauthorized user',async(t)=>{
 const h=mount(t,{allowed:false});await settle()
 assert.match(h.text(),/交票需要“应收款”和“查看客户价格”两项权限/)
 assert.equal(h.requests.some(item=>item.path.startsWith('/api/ticket-issue?')),false)
})

test('existing project ticket details route receipt handling to the centralized page and have no old financial write action',()=>{
 assert.doesNotMatch(projectPanelSource,/inspect\(ticket,\s*['"]deliver['"]\)/)
 assert.doesNotMatch(projectPanelSource,/action:\s*['"]deliver['"]|action===['"]deliver['"]|action==='deliver'/)
 assert.doesNotMatch(projectPanelSource,/project-ticket-action|voidReceipt|saveAction|action:'receive'/)
 assert.match(projectPanelSource,/<ReceiptManagementPanel/)
 assert.match(projectPanelSource,/company-id="receiptCompany"/)
 assert.match(projectPanelSource,/公司收款 \/ 欠款/)
})
