import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import {createRenderer,nextTick} from 'vue'
import ts from 'typescript'
registerHooks({resolve(specifier,context,next){if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')&&existsSync(fileURLToPath(new URL(specifier,context.parentURL))+'.ts'))return next(specifier+'.ts',context);return next(specifier,context)}})
globalThis.Document??=class Document{}
globalThis.ShadowRoot??=class ShadowRoot{}
globalThis.document??={activeElement:null,addEventListener(){},removeEventListener(){}}
const moduleURL=source=>'data:text/javascript,'+encodeURIComponent(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
const cache=new Map(),vueURL=new URL('../node_modules/vue/index.mjs',import.meta.url).href
async function component(name){
 if(cache.has(name))return cache.get(name)
 const file=new URL(name==='App'?'./App.vue':'./components/'+name+'.vue',import.meta.url),{descriptor}=parse(await readFile(file,'utf8')),script=compileScript(descriptor,{id:'cash-'+name}),imports=new Map()
 for(const match of script.content.matchAll(/from\s+(['"])([^'"]+)\1/g)){const path=match[2];imports.set(path,path==='vue'?vueURL:path==='../../VERSION?raw'?'data:text/javascript,export default "2.0.1"':path.endsWith('.vue')?['ReceiptManagementPanel','CompanyReceiptDialog','ReceiptAllocationEditor','CustomerDiscountDialog','ReferenceSelect'].some(name=>path.endsWith(name+'.vue'))?await component(path.split('/').at(-1).replace('.vue','')):'data:text/javascript,export default {render(){return null}}':new URL(path+'.ts',file).href)}
 const source=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(imports.get(path))),template=compileTemplate({id:'cash-'+name,source:descriptor.template.content,filename:name+'.vue',compilerOptions:{bindingMetadata:script.bindings}})
 assert.deepEqual(template.errors,[])
 const renderURL=moduleURL(template.code.replaceAll('from "vue"','from '+JSON.stringify(vueURL))),url=moduleURL(source.replace(/^export default/m,'const component =')+'\nimport {render} from '+JSON.stringify(renderURL)+';component.render=render;export default component')
 cache.set(name,url);return url
}
const {default:Management}=await import(await component('ReceiptManagementPanel')),{default:ReceiptDialog}=await import(await component('CompanyReceiptDialog')),{default:Allocation}=await import(await component('ReceiptAllocationEditor')),{default:Discount}=await import(await component('CustomerDiscountDialog')),{default:App}=await import(await component('App'))
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}}),settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()},deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve}}
const summary=()=>({rows:[{company_id:'c-a',company_name:'同名公司',ticket_count:2,total_cents:150000,delivered_cents:100000,undelivered_cents:50000,received_cents:100000,allocated_cents:70000,unallocated_cents:30000,discount_cents:0,unsettled_cents:80000,outstanding_cents:50000,advance_cents:0},{company_id:'c-b',company_name:'同名公司',ticket_count:0,total_cents:0,received_cents:0,allocated_cents:0,unallocated_cents:0,discount_cents:0,unsettled_cents:0,outstanding_cents:0,advance_cents:0}],total:102,page:1,page_size:100,total_cents:150000,delivered_cents:100000,undelivered_cents:50000,received_cents:100000,allocated_cents:70000,unallocated_cents:30000,discount_cents:0,unsettled_cents:80000,outstanding_cents:50000,advance_cents:0})
const receipt=()=>({id:'cash-a',company_id:'c-a',company_name:'同名公司',amount_cents:100000,received_at:'2026-10-12',method:'转账',voucher_no:'合成凭证',note:'合成预收',status:'active',version:2,allocated_cents:70000,unallocated_cents:30000,created_by_name:'合成老板'})
const detail=()=>({receipt:receipt(),allocations:[{id:'a-1',receipt_id:'cash-a',ticket_id:'t-a',batch_id:'b-a',project_id:'p-a',address:'合成甲工地',number:'P-A',batch_number:'B-A',amount_cents:70000}],history:[{version:2,event:'调整收款分配',reason:'两工地预付款',actor_name:'合成老板',created_at:'2026-10-12',snapshot:{}}],history_total:55,history_page:1,history_page_size:50})
const targetRows=()=>({rows:[{id:'t-a',project_id:'p-a',company_id:'c-a',number:'P-A',address:'合成甲工地',total_cents:100000,allocated_cents:70000,discount_cents:0,outstanding_cents:30000,batches:[{id:'b-a',number:'B-A',total_cents:100000,available_cents:30000}]},{id:'t-b',project_id:'p-b',company_id:'c-a',number:'P-B',address:'合成乙工地',total_cents:50000,allocated_cents:0,discount_cents:0,outstanding_cents:50000,batches:[{id:'b-b',number:'B-B',total_cents:50000,available_cents:50000}]}],total:105,page:1,page_size:100})
function mount(t,Component,props={},override=()=>null){
 const root={type:'root',children:[]},requests=[]
 const insert=(node,parent,anchor=null)=>{if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1);const index=anchor?parent.children.indexOf(anchor):-1;parent.children.splice(index<0?parent.children.length:index,0,node);node.parent=parent}
 const renderer=createRenderer({createElement:type=>({type,tagName:type.toUpperCase(),children:[],props:{},listeners:{},value:'',get options(){return this.children.filter(child=>child.type==='option')},addEventListener(name,fn){this.listeners[name]=fn},getRootNode(){return globalThis.document},setAttribute(){},removeAttribute(){}}),createText:text=>({type:'text',text,children:[]}),createComment:text=>({type:'comment',text,children:[]}),setText:(node,text)=>node.text=text,setElementText:(node,text)=>{node.text=text;node.children=[]},patchProp:(node,key,old,value)=>{node.props[key]=value;node[key]=value},insert,remove:node=>node.parent?.children.splice(node.parent.children.indexOf(node),1),parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,insertStaticContent(content,parent,anchor){const node={type:'static',text:content.replace(/<[^>]+>/g,''),children:[]};insert(node,parent,anchor);return[node,node]}})
 t.mock.method(globalThis,'fetch',async(path,init)=>{requests.push({path,init});const custom=override(path,init);if(custom)return custom;if(path==='/api/me')return response({user:{id:'admin',role:'admin',permissions:[],crafts:[]},categories:[]});if(path==='/api/reference')return response({companies:[],workers:[],warehouses:[],products:[],crafts:[]});if(path.startsWith('/api/reference-options?'))return response({rows:[{id:'c-a',name:'同名公司'},{id:'c-b',name:'同名公司'}],total:2,page:1});if(path.startsWith('/api/finance-companies?'))return response(summary());if(path.startsWith('/api/company-receipts?'))return response({rows:[receipt()],total:103,page:1,page_size:100});if(path.startsWith('/api/company-receipt?'))return response(detail());if(path.startsWith('/api/receipt-targets?'))return response(targetRows());if(path.startsWith('/api/customer-discounts?'))return response({rows:[],total:0,page:1,page_size:100});throw new Error('Unexpected synthetic request '+path)})
 const app=renderer.createApp(Component,props),state=app.mount(root).$.setupState; t.after(()=>app.unmount())
 const walk=node=>[node,...node.children.flatMap(walk)],text=node=>node.type==='comment'?'':(node.text||'')+node.children.map(text).join(''),elements=()=>walk(root),button=label=>elements().find(node=>node.type==='button'&&text(node)===label)
 return{app,state,requests,elements,button,text:()=>text(root)}
}
const last=(h,prefix)=>h.requests.filter(row=>row.path.startsWith(prefix)).at(-1),params=path=>new URL('http://synthetic'+path).searchParams

test('company full-period summary distinguishes real 1000, allocated 700, unallocated 300, unsettled 800 and debt 500',async(t)=>{
 const h=mount(t,Management,{canFinance:true,canCustomerPrices:true,canManage:true});await settle()
 assert.match(h.text(),/实际到账¥ 1,000\.00已分配收款¥ 700\.00未分配收款¥ 300\.00.*工地尚未核销¥ 800\.00公司实际仍需收回¥ 500\.00/)
 assert.equal(h.elements().filter(n=>n.type==='button'&&n.text==='收款 / 欠款').length,2)
 h.state.openCompany('c-a');await settle();h.state.month='2026-09';h.state.unallocated=true;h.state.filter();await settle()
 assert.equal(params(last(h,'/api/company-receipts?').path).get('month'),'2026-09');assert.equal(params(last(h,'/api/company-receipts?').path).get('unallocated'),'1')
 assert.equal(params(last(h,'/api/finance-companies?').path).has('month'),false)
 assert.match(h.text(),/公司实际仍需收回¥ 500\.00/);h.state.turn(2);await settle();assert.equal(params(last(h,'/api/company-receipts?').path).get('page'),'2')
})
test('receipt without any worksite is saved once with actual fields; lost response retries same idempotency key and preserves draft',async(t)=>{
 let failed=true
 const h=mount(t,ReceiptDialog,{companyId:'c-b',canManage:true,allowed:true},path=>path==='/api/company-receipt'?failed?response({error:'暂时写入失败'},422):response({...detail(),receipt:{...receipt(),company_id:'c-b',allocated_cents:0,unallocated_cents:100000},allocations:[]}):null);await settle()
 h.state.date='2026-10-11';h.state.amount='1000';h.state.method='微信';h.state.note='提前付款';await h.state.save();await settle()
 assert.equal(h.state.amount,'1000');assert.equal(h.state.note,'提前付款');assert.match(h.text(),/暂时写入失败/)
 failed=false;await h.state.save();await settle();const writes=h.requests.filter(row=>row.path==='/api/company-receipt'),body=JSON.parse(writes[0].init.body)
 assert.equal(writes.length,2);assert.equal(writes[0].init.headers['Idempotency-Key'],writes[1].init.headers['Idempotency-Key']);assert.equal(body.amount_cents,100000);assert.equal(body.company_id,'c-b');assert.equal(body.received_at,'2026-10-11');assert.equal(body.project_id,undefined);assert.equal(body.ticket_id,undefined);assert.equal(h.state.action,'');assert.match(h.text(),/尚未分配.*公司真正待收/)
})
test('allocation full replacement supports two sites/batches and survives candidate pagination; rejects over-allocation before writing',async(t)=>{
 const h=mount(t,Allocation,{detail:detail(),allowed:true},path=>path==='/api/receipt-allocation'?response(detail()):null);await settle()
 h.state.drafts[0].amount='600';h.state.add(h.state.targets[1],'b-b');h.state.drafts[1].amount='300';h.state.reason='分配到两个工地';await h.state.load(2);await settle()
 assert.equal(h.state.drafts.length,2);assert.equal(h.state.drafts[0].amount,'600');assert.match(h.text(),/分配合计 ¥ 900\.00.*剩余未分配 ¥ 100\.00/)
 h.state.drafts[1].amount='500';await h.state.save();assert.equal(h.requests.some(r=>r.path==='/api/receipt-allocation'),false);assert.match(h.text(),/不能超过/)
 h.state.drafts[1].amount='300';await h.state.save();const body=JSON.parse(last(h,'/api/receipt-allocation').init.body)
 assert.deepEqual(body.allocations,[{ticket_id:'t-a',batch_id:'b-a',amount_cents:60000},{ticket_id:'t-b',batch_id:'b-b',amount_cents:30000}]);assert.equal(body.version,2)
})
test('allocation conflict retains entered amounts and exposes refresh path; permission loss discards protected draft',async(t)=>{
 const h=mount(t,Allocation,{detail:detail(),allowed:true},path=>path==='/api/receipt-allocation'?response({error:'收款版本已变化，请刷新'},409):null);await settle()
 h.state.drafts[0].amount='500';h.state.reason='合成调整';await h.state.save();await settle();assert.equal(h.state.drafts[0].amount,'500');assert.equal(h.state.reason,'合成调整');assert.ok(h.button('保留实际收款，返回后刷新核对'))
 h.app._instance.props.allowed=false;await settle();assert.deepEqual(h.state.drafts,[]);assert.equal(h.state.reason,'')
})
test('discount requires explicit amount/reason, separate endpoint; decline does not create cash and employee cannot confirm',async(t)=>{
 const result={discount:{id:'d-a',company_id:'c-a',ticket_id:'t-a',amount_cents:10000,reason:'老板同意少收100',version:1,voided:false,address:'合成甲工地',number:'P-A'},history:[],history_total:0,history_page:1,history_page_size:50}
 const h=mount(t,Discount,{companyId:'c-a',canManage:true,allowed:true},path=>path==='/api/customer-discount'?response(result):null);await settle();h.state.choose(h.state.targets[0]);h.state.amount='100';await h.state.save();assert.equal(h.requests.some(r=>r.path==='/api/customer-discount'),false);assert.match(h.text(),/减免原因/)
 h.state.reason='老板同意少收100';await h.state.save();await settle();const body=JSON.parse(last(h,'/api/customer-discount').init.body);assert.equal(body.amount_cents,10000);assert.equal(body.reason,'老板同意少收100');assert.equal(h.requests.some(r=>r.path==='/api/company-receipt'),false)
 h.app._instance.props.canManage=false;await settle();assert.equal(h.button('作废这笔减免'),undefined)
})
test('receipt correction uses current version, reason and destination company; employee lacks correction/void actions',async(t)=>{
 const h=mount(t,ReceiptDialog,{receiptId:'cash-a',companyId:'c-a',canManage:true,allowed:true},path=>path==='/api/company-receipt-action'?response({...detail(),receipt:{...receipt(),version:3,company_id:'c-b',allocated_cents:0,unallocated_cents:50000,amount_cents:50000},allocations:[]}):null);await settle();h.state.begin('correct');h.state.amount='500';h.state.company='c-b';h.state.reason='公司登记错误';await h.state.save();await settle()
 const body=JSON.parse(last(h,'/api/company-receipt-action').init.body);assert.equal(body.version,2);assert.equal(body.action,'correct');assert.equal(body.company_id,'c-b');assert.equal(body.amount_cents,50000);assert.equal(h.state.detail.allocations.length,0)
 h.app._instance.props.canManage=false;await settle();assert.equal(h.button('更正登记'),undefined);assert.equal(h.button('作废错误收款'),undefined);assert.ok(h.button('分配 / 调整收款'))
})
test('latest company/permission scope wins and missing price rights never fetch amounts',async(t)=>{
 const wait=deferred();let slow=true
 const h=mount(t,Management,{canFinance:true,canCustomerPrices:true,canManage:false},path=>slow&&path.startsWith('/api/finance-companies?')?(slow=false,wait.promise):null);await settle();h.state.openCompany('c-b');await h.state.refresh();wait.resolve(response({...summary(),rows:[{company_id:'old',company_name:'过期公司'}]}));await settle();assert.equal(h.state.companies.some(row=>row.company_id==='old'),false)
 h.app._instance.props.canCustomerPrices=false;await settle();assert.equal(h.state.summary,null);assert.deepEqual(h.state.receipts,[]);assert.match(h.text(),/无查看权限/)
 const denied=mount(t,Management,{canFinance:true,canCustomerPrices:false,canManage:false});await settle();assert.equal(denied.requests.some(r=>r.path.startsWith('/api/finance-companies')),false)
})
test('App finance entry actually renders centralized collection page and reuses existing permissions',async(t)=>{
 const h=mount(t,App);await settle();h.state.openReceipts('c-a');await settle();assert.match(h.text(),/收款管理.*实际到账/);assert.ok(h.button('登记实际收款'));assert.equal(h.requests.some(r=>r.path.startsWith('/api/project-ticket-batch')),false)
 h.state.clearSession();await settle();assert.doesNotMatch(h.text(),/实际到账/)
 h.state.user={id:'employee',role:'employee',permissions:['orders'],crafts:[]};h.state.openReceipts('c-a');await settle();assert.equal(h.state.page,'dashboard')
})
test('large integer cents stay exact; duplicate scope and negative/invalid allocations cannot be submitted',async()=>{
 const {amountInput,allocationPayload,receiptMoney}=await import('./receipts.ts')
 assert.equal(amountInput('9007199254740993'),'90071992547409.93');assert.equal(receiptMoney('9007199254740993'),'¥ 90,071,992,547,409.93')
 assert.throws(()=>allocationPayload([{ticket_id:'a',batch_id:'',amount:'1'},{ticket_id:'a',batch_id:'',amount:'2'}],300),/合为一行/)
 assert.throws(()=>allocationPayload([{ticket_id:'a',batch_id:'',amount:'-1'}],300),/最多保留/)
})
test('void keeps historical amount; busy blocks duplicate submit and paginated history shows former allocation values',async(t)=>{
 const wait=deferred(),voided={...detail(),receipt:{...receipt(),status:'voided',version:3,allocated_cents:0,unallocated_cents:0,void_reason:'合成重复登记'},allocations:[]}
 const h=mount(t,ReceiptDialog,{receiptId:'cash-a',companyId:'c-a',canManage:true,allowed:true},path=>{
  if(path==='/api/company-receipt-action')return wait.promise
  if(path.startsWith('/api/company-receipt?')&&params(path).get('history_page')==='2')return response({...voided,history_page:2,history:[{version:1,event:'登记实际收款',reason:'原预收',actor_name:'合成老板',created_at:'2026-10-11',snapshot:{receipt:receipt(),allocations:detail().allocations}}]})
  return null
 });await settle();h.state.begin('void');h.state.reason='合成重复登记';const pending=h.state.save();await settle();assert.equal(h.button('正在保存…').props.disabled,true);await h.state.save();assert.equal(h.requests.filter(r=>r.path==='/api/company-receipt-action').length,1)
 wait.resolve(response(voided));await pending;await settle();assert.match(h.text(),/已作废.*原金额和操作历史仍保留/);assert.equal(h.state.detail.receipt.amount_cents,100000);assert.equal(h.button('分配 / 调整收款'),undefined)
 await h.button('下一页历史').props.onClick();await settle();assert.equal(params(last(h,'/api/company-receipt?').path).get('history_page'),'2');assert.match(h.text(),/当时登记 ¥ 1,000\.00.*当时分配 ¥ 700\.00.*当时未分配 ¥ 300\.00/)
})
