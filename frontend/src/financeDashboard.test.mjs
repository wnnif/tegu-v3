import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {registerHooks} from 'node:module'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import {createRenderer,nextTick} from 'vue'
import ts from 'typescript'
import {financeDashboardGroups,financeDashboardMoney,financeDashboardMonth,financeDashboardParams,financeDashboardExport,financeDashboardSourceNavigation} from './financeDashboard.ts'

registerHooks({resolve(specifier,context,next){if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')&&existsSync(fileURLToPath(new URL(specifier,context.parentURL))+'.ts'))return next(specifier+'.ts',context);return next(specifier,context)}})
globalThis.Document??=class Document{}
globalThis.ShadowRoot??=class ShadowRoot{}
globalThis.document??={activeElement:null,addEventListener(){},removeEventListener(){}}
const vueURL=new URL('../node_modules/vue/index.mjs',import.meta.url).href
const moduleURL=source=>'data:text/javascript,'+encodeURIComponent(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
const file=new URL('./FinanceSummaryPanel.vue',import.meta.url)
const {descriptor}=parse(await readFile(file,'utf8')),script=compileScript(descriptor,{id:'finance-dashboard'})
const scriptSource=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(path==='vue'?vueURL:new URL(path+'.ts',file).href))
const template=compileTemplate({id:'finance-dashboard',source:descriptor.template.content,filename:'FinanceSummaryPanel.vue',compilerOptions:{bindingMetadata:script.bindings}})
assert.deepEqual(template.errors,[])
const renderURL=moduleURL(template.code.replaceAll('from "vue"','from '+JSON.stringify(vueURL)))
const {default:Panel}=await import(moduleURL(scriptSource.replace(/^export default/m,'const component =')+'\nimport {render} from '+JSON.stringify(renderURL)+';component.render=render;export default component'))

const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}
function mount(t,props,request){
 const root={type:'root',children:[]},requests=[],navigated=[]
 const insert=(node,parent,anchor=null)=>{if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1);const index=anchor?parent.children.indexOf(anchor):-1;parent.children.splice(index<0?parent.children.length:index,0,node);node.parent=parent}
 const renderer=createRenderer({createElement:type=>({type,tagName:type.toUpperCase(),children:[],props:{},listeners:{},value:'',get options(){return this.children.filter(child=>child.type==='option')},addEventListener(name,fn){this.listeners[name]=fn},getRootNode(){return globalThis.document},setAttribute(){},removeAttribute(){}}),createText:text=>({type:'text',text,children:[]}),createComment:text=>({type:'comment',text,children:[]}),setText:(node,text)=>node.text=text,setElementText:(node,text)=>{node.text=text;node.children=[]},patchProp:(node,key,old,value)=>{node.props[key]=value;node[key]=value},insert,remove:node=>node.parent?.children.splice(node.parent.children.indexOf(node),1),parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,insertStaticContent(content,parent,anchor){const node={type:'static',text:content.replace(/<[^>]+>/g,''),children:[]};insert(node,parent,anchor);return[node,node]}})
 t.mock.method(globalThis,'fetch',async(path,init)=>{requests.push({path,init});return request(path,init)})
 const app=renderer.createApp(Panel,{...props,onNavigate:value=>navigated.push(value)}),state=app.mount(root).$.setupState
 t.after(()=>app.unmount())
 const walk=node=>[node,...(node.children||[]).flatMap(walk)],text=node=>node.type==='comment'?'':(node.text||'')+(node.children||[]).map(text).join('')
 return{app,state,requests,navigated,elements:()=>walk(root),text:()=>text(root)}
}
const metric=(key,label,cents,source)=>({key,label,amount_cents:cents,basis:'截至 2026-10-12',source})
const sample={range:{from:'2026-10-01',to:'2026-10-12',all:false},queried_at:'2026-10-12 12:00:00',balance_note:'库存为查询时当前参考货值',groups:[{key:'customer',label:'客户业务',metrics:[metric('construction','施工收费','100000','orders')]},{key:'payroll',label:'工人工资',metrics:[metric('wage','累计施工工资','999999','wages')]},{key:'inventory',label:'库存货值',metrics:[metric('purchase','商品进货金额','30000','inventory'),metric('inventory_value','当前库存货值','20000','inventory')]},{key:'expenses',label:'报销',metrics:[metric('expense_unpaid','尚未支付报销','30000','expenses')]}]}

test('finance money remains exact for totals larger than JavaScript safe integers',()=>{
 assert.equal(financeDashboardMoney('10000000000000001'),'¥ 100,000,000,000,000.01')
 assert.equal(financeDashboardMoney('-12550'),'-¥ 125.50')
 assert.equal(financeDashboardMoney(undefined),'—')
 assert.equal(financeDashboardMoney(100),'—')
})

test('default month uses Shanghai date and export preserves applied exact dates',()=>{
 assert.deepEqual(financeDashboardMonth(new Date('2026-09-30T18:00:00Z')),{from:'2026-10-01',to:'2026-10-01',all:false})
 assert.throws(()=>financeDashboardParams({from:'2026-02-30',to:'2026-03-12',all:false}),/有效/)
 assert.throws(()=>financeDashboardParams({from:'2026-10-12',to:'2026-10-01',all:false}),/晚于/)
 const url=new URL(financeDashboardExport({from:'2026-10-01',to:'2026-10-12',all:false}),'http://localhost')
 assert.equal(url.searchParams.get('from'),'2026-10-01')
 assert.equal(url.searchParams.get('to'),'2026-10-12')
 assert.equal(url.searchParams.get('export'),'1')
 assert.equal(financeDashboardParams({from:'',to:'',all:true}).toString(),'all=1')
})

test('only sources with matching date semantics navigate, while current stock is labeled as a snapshot',()=>{
 const range={from:'1000-01-01',to:'2026-10-13',all:true}
 assert.deepEqual(financeDashboardSourceNavigation({key:'construction',source:'orders'},range),{view:'orders',scope:'period',filters:{from:range.from,to:range.to}})
 assert.deepEqual(financeDashboardSourceNavigation({key:'expense_registered',source:'expenses'},range),{view:'expenses',scope:'period',filters:{from:range.from,to:range.to}})
 assert.deepEqual(financeDashboardSourceNavigation({key:'purchase',source:'inventory'},range),{view:'inventory',scope:'period',filters:{from:range.from,to:range.to,kind:'进货',inventoryTab:'history'}})
 assert.deepEqual(financeDashboardSourceNavigation({key:'inventory_value',source:'inventory'},range),{view:'inventory',scope:'snapshot'})
 for(const metric of [{key:'wage_paid',source:'wages'},{key:'expense_paid',source:'expenses'},{key:'received',source:'finance'},{key:'delivered',source:'ticket-issue'},{key:'purchase_balance',source:'inventory'}])
  assert.equal(financeDashboardSourceNavigation(metric,range),null)
})

test('finance page independently hides wages, warehouse and expense groups',()=>{
 assert.deepEqual(financeDashboardGroups(sample.groups,['finance','customer_prices']).map(g=>g.key),['customer'])
 assert.deepEqual(financeDashboardGroups(sample.groups,['finance','wages']).map(g=>g.key),['payroll'])
 assert.deepEqual(financeDashboardGroups(sample.groups,['customer_prices','wages','warehouse','expenses']),[])
})

test('real finance panel renders permitted saved cents and exports only the loaded range',async(t)=>{
 const h=mount(t,{permissions:['finance','customer_prices','orders']},()=>response(sample))
 await settle()
 assert.match(h.text(),/施工收费/)
 assert.match(h.text(),/¥ 1,000.00/)
 assert.doesNotMatch(h.text(),/999999|累计施工工资|库存货值|尚未支付报销/)
 const exportLink=()=>h.elements().find(node=>node.type==='a'&&node.props.href?.startsWith('/api/finance-summary?'))
 const before=exportLink().props.href
 h.state.from='2026-09-01';await settle()
 assert.equal(exportLink().props.href,before)
 assert.ok(h.requests.every(request=>request.init.method==='GET'))
 h.state.open({key:'construction',source:'orders'})
 assert.deepEqual(h.navigated[0],{view:'orders',scope:'period',filters:{from:sample.range.from,to:sample.range.to}})
})

test('all-time drilldown uses the canonical returned range instead of dropping dates',async(t)=>{
 const allTime={...sample,range:{from:'1000-01-01',to:'2026-10-13',all:true}}
 const h=mount(t,{permissions:['finance','customer_prices','orders']},()=>response(allTime))
 await settle()
 h.state.open({key:'construction',source:'orders'})
 assert.deepEqual(h.navigated[0],{view:'orders',scope:'period',filters:{from:'1000-01-01',to:'2026-10-13'}})
})

test('finance panel exposes only matching source links and labels the live inventory snapshot',async(t)=>{
 const h=mount(t,{permissions:['finance','customer_prices','orders','warehouse','expenses']},()=>response(sample))
 await settle()
 const metricButtons=()=>h.elements().filter(node=>node.type==='button'&&node.props.class==='metric-amount')
 assert.equal(metricButtons().length,3)
 assert.ok(metricButtons().some(node=>node.props['aria-label'].includes('查看当前库存（非历史快照）')))
 assert.match(h.text(),/来源未开放跳转/)
 h.state.open({key:'expense_unpaid',source:'expenses'})
 assert.equal(h.navigated.length,0)
 h.state.open({key:'inventory_value',source:'inventory'})
 assert.deepEqual(h.navigated[0],{view:'inventory',scope:'snapshot'})
 h.state.open({key:'purchase',source:'inventory'})
 assert.deepEqual(h.navigated[1],{view:'inventory',scope:'period',filters:{from:sample.range.from,to:sample.range.to,kind:'进货',inventoryTab:'history'}})
})

test('failed reload or revoked finance clears values and export links',async(t)=>{
 let fail=false
 const h=mount(t,{permissions:['finance','customer_prices']},()=>response(fail?{error:'读取失败'}:sample,fail?500:200))
 await settle()
 assert.match(h.text(),/¥ 1,000.00/)
 fail=true;await h.state.refresh();await settle()
 assert.match(h.text(),/读取失败/)
 assert.doesNotMatch(h.text(),/¥ 1,000.00/)
 assert.equal(h.elements().some(node=>node.type==='a'),false)
 h.app._instance.props.permissions=[];await settle()
 assert.match(h.text(),/无财务汇总权限/)
})

test('late query results cannot repopulate a revoked finance session',async(t)=>{
 let resolve
 const pending=new Promise(done=>resolve=done)
 const h=mount(t,{permissions:['finance','customer_prices']},()=>pending)
 h.app._instance.props.permissions=[];await settle()
 resolve(response(sample));await settle()
 assert.match(h.text(),/无财务汇总权限/)
 assert.doesNotMatch(h.text(),/¥ 1,000.00/)
})
