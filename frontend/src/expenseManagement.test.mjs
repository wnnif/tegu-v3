import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import {createRenderer,nextTick} from 'vue'
import {registerHooks} from 'node:module'
import ts from 'typescript'
import {expenseClaimConfirmed,expenseClaimPending,expenseClaimStatus,expenseClaimVoided,expenseHistoryCanReverse,expenseHistoryReverseAction,expenseMoney,parseExpenseAmount} from './expenses.ts'

registerHooks({resolve(specifier,context,next){if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')&&existsSync(fileURLToPath(new URL(specifier,context.parentURL))+'.ts'))return next(specifier+'.ts',context);return next(specifier,context)}})
globalThis.Document??=class Document{}
globalThis.ShadowRoot??=class ShadowRoot{}
globalThis.document??={activeElement:null,addEventListener(){},removeEventListener(){}}
const vueURL=new URL('../node_modules/vue/index.mjs',import.meta.url).href
const moduleURL=source=>'data:text/javascript,'+encodeURIComponent(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
const cache=new Map()
async function component(name){
 if(cache.has(name))return cache.get(name)
 const file=new URL(name==='ExpenseManagementPanel.vue'?'./components/ExpenseManagementPanel.vue':'./components/ExpenseActionDialog.vue',import.meta.url)
 const {descriptor}=parse(await readFile(file,'utf8')),script=compileScript(descriptor,{id:'expense-'+name}),imports=new Map()
 for(const match of script.content.matchAll(/from\s+(['"])([^'"]+)\1/g)){
  const path=match[2]
  imports.set(path,path==='vue'?vueURL:path.endsWith('.vue')?await component(path.split('/').at(-1)):new URL(path+'.ts',file).href)
 }
 const source=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(imports.get(path)))
 const template=compileTemplate({id:'expense-'+name,source:descriptor.template.content,filename:name,compilerOptions:{bindingMetadata:script.bindings}})
 assert.deepEqual(template.errors,[])
 const renderURL=moduleURL(template.code.replaceAll('from "vue"','from '+JSON.stringify(vueURL)))
 const url=moduleURL(source.replace(/^export default/m,'const component =')+'\nimport {render} from '+JSON.stringify(renderURL)+';component.render=render;export default component')
 cache.set(name,url)
 return url
}
const {default:Panel}=await import(await component('ExpenseManagementPanel.vue'))
const {default:Dialog}=await import(await component('ExpenseActionDialog.vue'))
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}
function mount(t,Component,props={},override=()=>null){
 const root={type:'root',children:[]},requests=[]
 const insert=(node,parent,anchor=null)=>{if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1);const index=anchor?parent.children.indexOf(anchor):-1;parent.children.splice(index<0?parent.children.length:index,0,node);node.parent=parent}
 const renderer=createRenderer({createElement:type=>({type,tagName:type.toUpperCase(),children:[],props:{},listeners:{},value:'',get options(){return this.children.filter(child=>child.type==='option')},addEventListener(name,fn){this.listeners[name]=fn},getRootNode(){return globalThis.document},setAttribute(){},removeAttribute(){}}),createText:text=>({type:'text',text,children:[]}),createComment:text=>({type:'comment',text,children:[]}),setText:(node,text)=>node.text=text,setElementText:(node,text)=>{node.text=text;node.children=[]},patchProp:(node,key,old,value)=>{node.props[key]=value;node[key]=value},insert,remove:node=>node.parent?.children.splice(node.parent.children.indexOf(node),1),parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,insertStaticContent(content,parent,anchor){const node={type:'static',text:content.replace(/<[^>]+>/g,''),children:[]};insert(node,parent,anchor);return[node,node]}})
 t.mock.method(globalThis,'fetch',async(path,init)=>{requests.push({path,init});const custom=override(path,init);if(custom)return custom;throw new Error('Unexpected expense test request '+path)})
 const app=renderer.createApp(Component,props),state=app.mount(root).$.setupState
 t.after(()=>app.unmount())
 const walk=node=>[node,...(node.children||[]).flatMap(walk)],text=node=>node.type==='comment'?'':(node.text||'')+(node.children||[]).map(text).join('')
 return{app,state,requests,elements:()=>walk(root),text:()=>text(root)}
}

const claim=(overrides={})=>({id:'claim-1',person_name:'张三',expense_date:'2026-10-12',category:'材料采购',amount_cents:20000,note:'购买材料',status:'confirmed',advance_applied_cents:0,paid_cents:0,due_cents:20000,purchase_document_id:'',purchase_number:'',purchase_date:'',purchase_total_cents:0,version:3,...overrides})
const advance=(overrides={})=>({id:'advance-1',person_name:'张三',advance_date:'2026-10-10',amount_cents:100000, purpose:'材料采购',note:'实际现金预支',applied_cents:80000,returned_cents:20000,remaining_cents:0,status:'active',version:2,...overrides})
const page=rows=>({rows,total:rows.length,page:1,page_size:50})

test('expense export uses the loaded filter and disappears after failed read or revoked access',async(t)=>{
 let fail=false
 const h=mount(t,Panel,{allowed:true},path=>path.startsWith('/api/expenses?')?response(fail?{error:'读取失败'}:page([claim()]),fail?500:200):null)
 await settle()
 const link=()=>h.elements().find(node=>node.type==='a'&&node.props.href?.includes('/api/expenses?'))
 assert.ok(link())
 const original=link().props.href
 h.state.query='尚未查询';await settle();assert.equal(link().props.href,original)
 fail=true;await h.state.refresh();await settle();assert.equal(link(),undefined)
 h.app._instance.props.allowed=false;await settle();assert.equal(link(),undefined)
})

test('expense money and status keep confirmation, actual payment and advance coverage distinct',()=>{
 assert.equal(parseExpenseAmount('200'),20000)
 assert.equal(parseExpenseAmount('0.05'),5)
 assert.throws(()=>parseExpenseAmount('1.239'),/最多保留两位小数/)
 assert.throws(()=>parseExpenseAmount('0'),/大于 0/)
 assert.equal(expenseMoney(20000),'¥ 200.00')
 assert.equal(expenseClaimStatus(claim({advance_applied_cents:0,paid_cents:0,due_cents:20000})),'已确认')
 assert.equal(expenseClaimStatus(claim({advance_applied_cents:80000,paid_cents:0,due_cents:20000})),'部分报销')
 assert.equal(expenseClaimStatus(claim({advance_applied_cents:80000,paid_cents:0,due_cents:0})),'预支已核销')
 assert.equal(expenseClaimStatus(claim({advance_applied_cents:0,paid_cents:20000,due_cents:0})),'已付清')
 assert.equal(expenseClaimStatus(claim({status:'pending'})),'待审核')
 assert.equal(expenseClaimStatus(claim({status:'部分报销'})),'部分报销')
 assert.equal(expenseClaimPending(claim({status:'待审核'})),true)
 assert.equal(expenseClaimConfirmed(claim({status:'预支已核销'})),true)
 assert.equal(expenseClaimVoided(claim({status:'已撤销'})),true)
})

test('expense panel requests only the selected paginated ledger and displays separate balances',async t=>{
 const rows=[claim({advance_applied_cents:80000,paid_cents:0,due_cents:12000}),claim({id:'claim-2',status:'pending',amount_cents:5000,due_cents:5000})]
 const h=mount(t,Panel,{allowed:true},path=>path.startsWith('/api/expenses?')?response(page(rows)):null)
 await settle()
 const request=h.requests.find(item=>item.path.startsWith('/api/expenses?'))
 assert.equal(new URL('http://local'+request.path).searchParams.get('tab'),'claims')
 assert.equal(new URL('http://local'+request.path).searchParams.get('page'),'1')
 assert.match(h.text(),/¥ 200\.00/)
 assert.match(h.text(),/¥ 800\.00/)
 assert.match(h.text(),/¥ 120\.00/)
 assert.match(h.text(),/部分报销/)
 assert.match(h.text(),/待审核/)
 assert.doesNotMatch(h.text(),/工资预支|工资发放/)
})

test('expense source navigation applies the exact range on its first request',async t=>{
 const h=mount(t,Panel,{allowed:true,initialFrom:'2026-09-24',initialTo:'2026-10-12'},path=>path.startsWith('/api/expenses?')?response(page([claim()])):null)
 await settle()
 const request=h.requests.find(item=>item.path.startsWith('/api/expenses?'))
 const params=new URL('http://local'+request.path).searchParams
 assert.equal(params.get('from'),'2026-09-24')
 assert.equal(params.get('to'),'2026-10-12')
 assert.equal(params.get('tab'),'claims')
 assert.equal(params.get('page'),'1')
})

test('revoking expense access clears loaded rows and blocks the panel from retaining them',async t=>{
 const h=mount(t,Panel,{allowed:true},path=>path.startsWith('/api/expenses?')?response(page([claim()])):null)
 await settle();assert.equal(h.state.claims.length,1)
 h.app._instance.props.allowed=false;await settle()
 assert.deepEqual(h.state.claims,[]);assert.equal(h.state.dialog,null)
})

test('expense claim write preserves entered fields on error and retries with the same idempotency key',async t=>{
 let failed=true
 const h=mount(t,Dialog,{action:'create-claim',row:null,allowed:true},path=>path==='/api/expense-action'?failed?response({error:'模拟保存失败'},422):response({ok:true,id:'claim-2'}):null)
 h.state.person='张三';h.state.date='2026-10-12';h.state.category='材料采购';h.state.amount='200';h.state.note='购买砂浆'
 await h.state.save();await settle()
 assert.equal(h.state.person,'张三');assert.equal(h.state.amount,'200');assert.equal(h.state.note,'购买砂浆');assert.match(h.text(),/模拟保存失败/)
 failed=false;await h.state.save();await settle()
 const writes=h.requests.filter(item=>item.path==='/api/expense-action'),body=JSON.parse(writes[0].init.body)
 assert.equal(writes.length,2);assert.equal(writes[0].init.headers['Idempotency-Key'],writes[1].init.headers['Idempotency-Key'])
 assert.deepEqual(body,{action:'create-claim',person_name:'张三',expense_date:'2026-10-12',category:'材料采购',amount_cents:20000,note:'购买砂浆'})
})

test('purchase linkage sends only the existing stock document reference and makes no inventory request',async t=>{
 const purchase={id:'purchase-1',work_date:'2026-10-11',total_cents:20000,note:'材料进货',line_items:[],version:1}
 const h=mount(t,Dialog,{action:'create-claim',row:null,allowed:true},path=>path.startsWith('/api/expense-purchases?')?response(page([purchase])):path==='/api/expense-action'?response({ok:true}):null)
 h.state.person='张三';h.state.date='2026-10-12';h.state.category='材料采购';h.state.amount='200';h.state.purchaseQuery='进货';await h.state.loadPurchases();h.state.purchaseId='purchase-1';await h.state.save()
 const body=JSON.parse(h.requests.find(item=>item.path==='/api/expense-action').init.body)
 assert.equal(body.purchase_document_id,'purchase-1')
 assert.equal(h.requests.some(item=>/stock|inventory|purchase-cost/.test(item.path)),false)
})

test('expense advance allocation uses separate expense IDs, versions and amount without calling payroll',async t=>{
 const claimRow=claim({due_cents:80000}),advanceRow=advance({applied_cents:0,returned_cents:0,remaining_cents:100000})
 const h=mount(t,Dialog,{action:'allocate-advance',row:claimRow,allowed:true},path=>path.startsWith('/api/expenses?')?response(page([advanceRow])):path==='/api/expense-action'?response({ok:true}):null)
 await settle();h.state.advanceId='advance-1';h.state.amount='800';h.state.date='2026-10-12';await h.state.save()
 const body=JSON.parse(h.requests.find(item=>item.path==='/api/expense-action').init.body)
 assert.deepEqual(body,{action:'allocate-advance',claim_id:'claim-1',claim_version:3,advance_id:'advance-1',advance_version:2,amount_cents:80000,applied_date:'2026-10-12',note:''})
 assert.equal(h.requests.some(item=>item.path.includes('payroll')),false)
})

test('only active child history events expose reasoned reversals',()=>{
 const row={id:'history-1',entity_type:'payment',entity_id:'payment-1',event:'pay-claim',reason:'',before_snapshot:null,after_snapshot:null,operation_id:'op-1',created_at:'2026-10-12',current_status:'active',current_version:4}
 assert.equal(expenseHistoryCanReverse(row),'payment')
 assert.deepEqual(expenseHistoryReverseAction(row,'重复登记'),{action:'reverse-payment',payment_id:'payment-1',version:4,reason:'重复登记'})
 assert.equal(expenseHistoryCanReverse({...row,current_status:'reversed'}),null)
 assert.throws(()=>expenseHistoryReverseAction(row,' '),/撤销原因/)
})
