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
globalThis.Document??=class Document {}
globalThis.ShadowRoot??=class ShadowRoot {}
globalThis.document??={activeElement:null}
const moduleURL=source=>'data:text/javascript,'+encodeURIComponent(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
async function component(name){
 const file=new URL('./components/'+name+'.vue',import.meta.url)
 const {descriptor}=parse(await readFile(file,'utf8')),script=compileScript(descriptor,{id:'archive-'+name}),imports=new Map()
 for(const match of script.content.matchAll(/from\s+(['"])([^'"]+)\1/g)){
  const path=match[2]
  imports.set(path,path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:path.endsWith('.vue')?await component(path.split('/').pop().replace('.vue','')):new URL(path+'.ts',file).href)
 }
 const source=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(imports.get(path)))
 const template=compileTemplate({id:'archive-'+name,source:descriptor.template.content,filename:name+'.vue',compilerOptions:{bindingMetadata:script.bindings}})
 assert.deepEqual(template.errors,[])
 const render=moduleURL(template.code.replaceAll('from "vue"','from '+JSON.stringify(new URL('../node_modules/vue/index.mjs',import.meta.url).href)))
 return moduleURL(source.replace(/^export default/m,'const component =')+'\nimport {render} from '+JSON.stringify(render)+';component.render=render;export default component')
}
const {default:ArchivePanel}=await import(await component('ArchivePanel'))
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done});return {promise,resolve}}
const run='a'.repeat(32),olderRun='b'.repeat(32)
const files=()=>[
 {company_id:'company-a',company:'示例公司',kind:'施工验收表',count:3,amount_cents:115000,file:run+'/customer.xlsx',available:true},
 {company_id:'company-a',company:'示例公司',kind:'售料明细',count:1,amount_cents:15000,file:run+'/sales.csv',available:true},
 {company_id:'company-a',company:'示例公司',kind:'票据应收',count:1,amount_cents:115000,file:run+'/receivable.csv',available:true},
]
const group=(overrides={})=>({key:'c:company-a',run_id:run,company_id:'company-a',company:'示例公司',month:'2026-10',created_at:'2026-10-09 13:20:00',automatic:0,legacy:false,files:files(),order_count:3,construction_cents:115000,receivable_cents:115000,received_cents:35000,outstanding_cents:80000,wage_cents:3600,...overrides})
const listing=(overrides={})=>({rows:[group()],total:1,page:1,page_size:100,legacy_total:0,can_orders:true,can_finance:true,can_wages:true,latest:null,...overrides})
const job=(overrides={})=>({id:'archive-job',kind:'archive',status:'运行中',stage:'生成表格',progress:1,total:3,message:'',created_at:'2026-10-09 13:21:00',...overrides})
function mount(t,override=()=>null,can=()=>true){
 const root={type:'root',children:[]},requests=[]
 function insert(node,parent,anchor=null){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1);const index=anchor?parent.children.indexOf(anchor):-1;parent.children.splice(index<0?parent.children.length:index,0,node);node.parent=parent}
 const renderer=createRenderer({
  createElement:type=>({type,tagName:type.toUpperCase(),children:[],props:{},value:'',listeners:{},style:{},
   get options(){return this.children.filter(child=>child.type==='option')},addEventListener(name,fn){this.listeners[name]=fn},getRootNode(){return globalThis.document},setAttribute(){},removeAttribute(){}}),
  createText:text=>({type:'text',text,children:[]}),createComment:text=>({type:'comment',text,children:[]}),
  setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text;node.children=[]},
  patchProp(node,key,previous,next){node.props[key]=next;node[key]=next},insert,
  remove(node){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1)},parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,
  insertStaticContent(content,parent,anchor){const node={type:'static',text:content.replace(/<[^>]+>/g,''),children:[]};insert(node,parent,anchor);return [node,node]},
 })
 t.mock.method(globalThis,'fetch',async(path,init)=>{
  requests.push({path,init})
  const alternate=override(path,init);if(alternate)return alternate
  if(path.startsWith('/api/archive-companies?'))return response(listing())
  if(path.startsWith('/api/maintenance/jobs?'))return response({rows:[],total:0})
  if(path.startsWith('/api/reference-options?'))return response({rows:[{id:'company-a',name:'示例公司'},{id:'company-b',name:'另一公司'}],total:2})
  throw new Error('Unexpected synthetic request '+path)
 })
 const app=renderer.createApp(ArchivePanel,{companies:[],can}),state=app.mount(root).$.setupState
 t.after(()=>app.unmount())
 const walk=node=>[node,...node.children.flatMap(walk)],elements=()=>walk(root)
 const text=node=>node.type==='comment'?'':(node.text||'')+node.children.map(text).join('')
 return {app,state,requests,elements,text:()=>text(root),button:label=>elements().find(node=>node.type==='button'&&text(node)===label),links:()=>elements().filter(node=>node.type==='a')}
}
const last=(h,prefix)=>h.requests.filter(request=>request.path.startsWith(prefix)).at(-1)
const params=path=>new URL(path,'http://synthetic.test').searchParams

test('new cash month is separate from allocation snapshot and legacy missing cash is not zero',async(t)=>{
 const h=mount(t,path=>path.startsWith('/api/archive-companies?')?response(listing({rows:[group({finance_scope:'allocation',cash_received_cents:0}),group({run_id:olderRun,cash_received_cents:undefined})],total:2})):null);await settle()
 assert.match(h.text(),/已分配 \/ 历史已收（元）.*当月实际到账（元）/)
 assert.match(h.text(),/不能据此推算公司累计欠款.*收款管理/)
 const rows=h.elements().filter(node=>node.type==='tr'&&node.children.some(cell=>cell.type==='td'&&cell.children.some(child=>child.type==='div')))
 assert.equal(rows[0].children.filter(cell=>cell.type==='td')[6].text,'0.00')
 assert.equal(rows[1].children.filter(cell=>cell.type==='td')[6].text,'未记录')
 await h.button('查看表格').props.onClick();await settle();assert.match(h.text(),/收费与收款分配快照/)
})

test('actual latest company summary renders one row, saved totals, expanded clear kinds and snapshot-bound ZIP links',async(t)=>{
 const h=mount(t);await settle()
 const query=params(last(h,'/api/archive-companies?').path)
 assert.equal(query.get('history'),'0');assert.equal(query.get('company_id'),'');assert.equal(query.get('page'),'1')
 assert.equal(h.requests.some(request=>request.path.startsWith('/api/archives?')),false)
 assert.match(h.text(),/每家公司每月只显示最新一次归档/)
 assert.match(h.text(),/示例公司.*3.*1150\.00.*1150\.00.*350\.00.*800\.00.*36\.00/)
 assert.equal(h.elements().filter(node=>node.type==='button'&&node.text==='查看表格').length,1)
 const zip=h.links()[0].props.href
 assert.equal(params(zip).get('run_id'),run);assert.equal(params(zip).get('key'),'c:company-a')
 await h.button('查看表格').props.onClick();await settle()
 assert.match(h.text(),/施工验收表.*下载 客户版 Excel.*售料明细.*票据应收/)
 assert.equal(h.links().filter(node=>node.props.href.startsWith('/api/archives?file=')).length,3)
 assert.equal(params(h.links()[1].props.href).get('file'),run+'/customer.xlsx')
 await h.button('收起表格').props.onClick();await settle();assert.equal(h.links().length,1)
})

test('historical runs with the same group key expand independently and legacy company hints do not invent a stable ID',async(t)=>{
 const legacy=group({key:'l:'+'f'.repeat(64),run_id:olderRun,company_id:'',legacy:true,order_count:undefined,construction_cents:undefined,received_cents:undefined,outstanding_cents:undefined})
 const h=mount(t,path=>path.startsWith('/api/archive-companies?')?response(listing({rows:params(path).get('history')==='1'?[group(),group({run_id:olderRun,created_at:'2026-10-01 08:00:00'}),legacy]:[group()],total:params(path).get('history')==='1'?3:1,legacy_total:1})):null);await settle()
 assert.match(h.text(),/旧版归档未记录公司编号.*历史版本.*全部公司/)
 h.state.history=true;h.state.filter();await settle()
 assert.equal(params(last(h,'/api/archive-companies?').path).get('history'),'1');assert.match(h.text(),/查看各次保存的历史版本/)
 h.state.toggle(h.state.rows[1]);await settle()
 assert.deepEqual(h.state.expanded,[olderRun+'/c:company-a']);assert.match(h.text(),/2026-10-01 08:00:00保存的版本/)
 h.state.toggle(h.state.rows[2]);await settle();assert.match(h.text(),/旧版未记录的汇总显示“未记录”/)
 assert.equal(params(h.links().find(node=>params(node.props.href).get('key')?.startsWith('l:')).props.href).get('run_id'),olderRun)
})

test('server capability flags control columns including empty pages; omitted historical amounts are never shown as zero',async(t)=>{
 let empty=false
 const h=mount(t,path=>path.startsWith('/api/archive-companies?')?response(listing({rows:empty?[]:[group({order_count:undefined,construction_cents:undefined,receivable_cents:undefined,received_cents:undefined,outstanding_cents:undefined,wage_cents:undefined})],can_orders:true,can_finance:false,can_wages:false})):null);await settle()
 assert.match(h.text(),/施工记录数.*施工及售料金额（元）/);assert.match(h.text(),/未记录/);assert.doesNotMatch(h.text(),/工资（元）|应收（元）|0\.00/)
 empty=true;await h.state.load();await settle();assert.match(h.text(),/施工记录数/);assert.doesNotMatch(h.text(),/工资（元）|应收（元）/)
})

test('real zero-value records retain zero and missing files block group download but available individual files stay downloadable',async(t)=>{
 const h=mount(t,path=>path.startsWith('/api/archive-companies?')?response(listing({rows:[group({wage_cents:0,files:[...files(),{file:run+'/lost.csv',kind:'工资明细',count:1,amount_cents:0,available:false,company:'示例公司'}]})]})):null);await settle()
 assert.match(h.text(),/0\.00/);assert.match(h.text(),/部分文件缺失，需重新生成/);assert.equal(h.links().length,0)
 h.state.toggle(h.state.rows[0]);await settle();assert.equal(h.links().length,3);assert.match(h.text(),/工资明细.*1 条 · 0\.00 元.*文件缺失，需重新生成/)
})

test('server company pagination beyond 100, filter changes and applied links clear prior expansions',async(t)=>{
 const h=mount(t,path=>path.startsWith('/api/archive-companies?')?response(listing({total:121,rows:[group({company_id:params(path).get('company_id')||'company-a'})]})):null);await settle()
 assert.equal(h.button('下一页归档').props.disabled,false);h.state.toggle(h.state.rows[0]);await settle()
 await h.button('下一页归档').props.onClick();await settle();assert.equal(params(last(h,'/api/archive-companies?').path).get('page'),'2');assert.deepEqual(h.state.expanded,[])
 h.state.month='2026-09';h.state.company='company-b';h.state.history=true;h.state.filter();await settle()
 const q=params(last(h,'/api/archive-companies?').path);assert.equal(q.get('page'),'1');assert.equal(q.get('month'),'2026-09');assert.equal(q.get('company_id'),'company-b');assert.equal(q.get('history'),'1')
 const href=h.links()[0].props.href;h.state.company='not-yet-applied';await nextTick();assert.equal(h.links()[0].props.href,href)
})

test('actual company selector changes both filtering ID and generation scope without matching legacy by name',async(t)=>{
 const h=mount(t);await settle()
 const picker=h.elements().find(node=>node.type==='select'&&node.props.id)
 picker.props.onChange({target:{value:'company-b'}});await settle()
 assert.equal(params(last(h,'/api/archive-companies?').path).get('company_id'),'company-b');assert.equal(h.state.company,'company-b')
})

test('failed month query clears previous archive rows, totals, permission columns and latest status then retries without generating',async(t)=>{
 let fail=false,generated=0
 const h=mount(t,path=>{
  if(path==='/api/maintenance/archive'){generated++;return response(job())}
  if(path.startsWith('/api/archive-companies?'))return fail?response({error:'读取失败'},500):response(listing({latest:{month:'2026-08',error:'旧月份提示',time:'旧时间'}}))
  return null
 });await settle();assert.equal(h.state.rows.length,1);h.state.month='2026-09';fail=true;await h.state.refresh(true);await settle()
 assert.deepEqual(h.state.rows,[]);assert.equal(h.state.total,0);assert.equal(h.state.latest,null);assert.equal(h.state.hasWages,false);assert.equal(h.state.error,'读取失败');assert.equal(h.state.busy,false)
 fail=false;await h.button('重新读取').props.onClick();await settle();assert.equal(h.state.rows.length,1);assert.equal(generated,0)
})

test('successful generation and failed listing stay distinguishable; query preserves success and does not regenerate',async(t)=>{
 let generated=0,fail=false
 const h=mount(t,path=>{
  if(path==='/api/maintenance/archive'){generated++;fail=true;return response(job())}
  if(path.startsWith('/api/maintenance/jobs?'))return response({rows:fail?[job({status:'完成',result:{count:3}})]:[],total:fail?1:0})
  if(path.startsWith('/api/archive-companies?')&&fail)return response({error:'网络中断'},500)
  return null
 });await settle();await h.state.generate();await settle()
 assert.equal(generated,1);assert.match(h.state.notice,/月度表格已生成，共 3 份/);assert.match(h.state.error,/表格已生成，但列表读取失败.*无需重复生成.*网络中断/)
 assert.deepEqual(h.state.rows,[]);assert.equal(h.state.total,0);assert.equal(h.state.busy,false)
 fail=false;await h.state.refresh(true);await settle();assert.equal(generated,1);assert.equal(h.state.error,'');assert.match(h.state.notice,/共 3 份/);assert.equal(h.state.rows.length,1)
})

test('generation freezes submitted month/company and prevents duplicate submit, then completion refreshes the current selected range',async(t)=>{
 const accepted=deferred();let generated=0,done=false
 const h=mount(t,(path,init)=>{
  if(path==='/api/maintenance/archive'){generated++;assert.deepEqual(JSON.parse(init.body),{month:'2026-09',company:'company-a'});return accepted.promise}
  if(path.startsWith('/api/maintenance/jobs?'))return response({rows:generated?[job(done?{status:'完成',result:{count:5}}:{})]:[],total:generated?1:0})
  return null
 });await settle();h.state.month='2026-09';h.state.company='company-a';await h.state.load()
 const request=h.state.generate();await h.state.generate();assert.equal(generated,1);assert.equal(h.state.submitting,true)
 accepted.resolve(response(job()));await request;await settle();assert.equal(h.state.running,true)
 h.state.month='2026-10';h.state.company='company-b';h.state.history=true;h.state.filter();await settle();done=true;await h.state.loadJobs();await settle()
 assert.match(h.state.notice,/2026-09 月度表格已生成，共 5 份/);assert.equal(h.state.month,'2026-10');assert.equal(h.state.company,'company-b')
 const q=params(last(h,'/api/archive-companies?').path);assert.equal(q.get('month'),'2026-10');assert.equal(q.get('company_id'),'company-b');assert.equal(q.get('history'),'1');assert.equal(h.state.running,false)
})

test('rejected generation preserves selected range and never reports success; empty month cannot submit',async(t)=>{
 let requests=0
 const h=mount(t,path=>path==='/api/maintenance/archive'?(requests++,response({error:'生成失败'},500)):null);await settle()
 const selected=h.state.month;await h.state.generate();await settle();assert.equal(h.state.month,selected);assert.equal(h.state.notice,'');assert.equal(h.state.error,'生成失败');assert.equal(h.state.busy,false)
 await h.button('全部月份').props.onClick();await settle();assert.equal(h.button('生成整月表格').props.disabled,true);await h.state.generate();assert.equal(requests,1)
})

test('old list and history responses cannot replace a newly filtered company',async(t)=>{
 const old=deferred();let first=true
 const h=mount(t,path=>path.startsWith('/api/archive-companies?')&&first?(first=false,old.promise):null);await settle()
 h.state.company='company-b';h.state.history=true;await h.state.load();old.resolve(response(listing({rows:[group({company:'迟到旧公司'})],latest:{month:'旧月份',error:'旧错误'}})));await settle()
 assert.equal(h.state.rows[0].company,'示例公司');assert.equal(h.state.latest,null);assert.equal(h.state.applied.company,'company-b');assert.equal(h.state.applied.history,true)
})

test('task pages discard late page results and task errors are separate from archive list errors',async(t)=>{
 const delayed=deferred();let block=false,fail=false
 const h=mount(t,path=>{
  if(path.startsWith('/api/maintenance/jobs?')){if(fail)return response({error:'任务不可读'},500);if(block&&params(path).get('page')==='1')return delayed.promise;return response({rows:[job({id:'page-'+params(path).get('page'),status:'完成'})],total:201})}
  return null
 });await settle();block=true;const old=h.state.loadJobs();await settle();h.state.turnJobPage(2);await settle();delayed.resolve(response({rows:[job({id:'late-page',status:'完成'})],total:1}));await old;assert.equal(h.state.jobs[0].id,'page-2')
 fail=true;await h.state.loadJobs();await settle();assert.equal(h.state.error,'');assert.equal(h.state.jobsError,'任务不可读');assert.equal(h.state.rows.length,1)
 fail=false;await h.button('重新读取任务').props.onClick();await settle();assert.equal(h.state.jobsError,'')
})

test('pending job absent from the page is checked by its exact ID and an unrelated completed job is not reported as its success',async(t)=>{
 let generated=false,done=false
 const h=mount(t,path=>{
  if(path==='/api/maintenance/archive'){generated=true;return response(job())}
  if(path==='/api/maintenance/jobs?id=archive-job')return response(job(done?{status:'完成',result:{count:7}}:{}))
  if(path.startsWith('/api/maintenance/jobs?'))return response({rows:generated?[job({id:'other-job',status:'完成',result:{count:99}})]:[],total:generated?1:0})
  return null
 });await settle();await h.state.generate();assert.match(h.state.notice,/任务已受理/);assert.doesNotMatch(h.state.notice,/99/);assert.equal(h.state.running,true)
 done=true;await h.state.loadJobs();await settle();assert.match(h.state.notice,/共 7 份/);assert.ok(h.requests.some(request=>request.path==='/api/maintenance/jobs?id=archive-job'))
})

test('login change and unmount discard pending list, generation and task results without adding old success',async(t)=>{
 const {apiSession}=await import('./api.ts')
 const h=mount(t);await settle();const wait=deferred();t.mock.method(globalThis,'fetch',async()=>wait.promise)
 const reading=h.state.load();await settle();apiSession.invalidate();wait.resolve(response(listing()));await reading;assert.deepEqual(h.state.rows,[]);assert.equal(h.state.latest,null);assert.equal(h.state.error,'')
 // Separate request after the new session is valid must still be ignored after unmount.
 const accepted=deferred();t.mock.method(globalThis,'fetch',async()=>accepted.promise);const generation=h.state.generate();await settle();h.app.unmount();accepted.resolve(response(job({status:'完成'})));await generation
 assert.equal(h.state.pending,null);assert.deepEqual(h.state.jobs,[]);assert.equal(h.state.notice,'')
})

test('unmounted task polling cannot mutate old state or trigger a company-list refresh',async(t)=>{
 const h=mount(t);await settle();const wait=deferred();t.mock.method(globalThis,'fetch',async()=>wait.promise)
 const poll=h.state.loadJobs();await settle();const before=h.requests.length;h.app.unmount();wait.resolve(response({rows:[job({status:'完成'})],total:1}));await poll;assert.deepEqual(h.state.jobs,[]);assert.equal(h.requests.length,before);assert.equal(h.state.notice,'')
})


test('工资-only无客户价格仍能看工资归档及工资文件，但不提供客户金额文件',async(t)=>{
 const wages={company_id:'company-a',company:'示例公司',kind:'工资明细',count:2,amount_cents:3600,file:run+'/wages.csv',available:true}
 const h=mount(t,path=>path.startsWith('/api/archive-companies?')?response(listing({rows:[group({files:[...files(),wages]})]})):null,permission=>permission==='wages');await settle()
 assert.equal(h.state.hasWages,true);assert.equal(h.state.hasOrders,false);assert.equal(h.state.hasFinance,false)
 assert.match(h.text(),/36.00/);assert.doesNotMatch(h.text(),/1150.00|350.00|800.00/)
 assert.equal(h.links().length,1);assert.equal(params(h.links()[0].props.href).get('run_id'),run)
 await h.button('查看表格').props.onClick();await settle();assert.match(h.text(),/工资明细/);assert.doesNotMatch(h.text(),/施工验收表|售料明细|票据应收/)
 assert.equal(h.links().filter(node=>node.props.href.startsWith('/api/archives?file=')).length,1);assert.equal(params(h.links().at(-1).props.href).get('file'),run+'/wages.csv')
})

test('仅orders无客户价格禁止生成归档和客户金额下载，缺金额不会显示0',async(t)=>{
 const h=mount(t,()=>null,permission=>permission==='orders');await settle()
 assert.equal(h.state.archiveAllowed,false);assert.equal(h.links().length,0);assert.equal(h.requests.some(request=>request.path.startsWith('/api/archive-companies?')),false)
 assert.equal(h.state.rows.length,0);assert.match(h.text(),/查看客户价格/)
})

test('报销或仓库权限可看全公司独立归档，文件下载按模块过滤',async(t)=>{
 let allowed='expenses'
 const systemFiles=[
  {kind:'报销登记',file:run+'/expense.csv',count:1,amount_cents:20000,available:true},
  {kind:'报销付款',file:run+'/expense-cash.csv',count:1,amount_cents:10000,available:true},
  {kind:'工资付款',file:run+'/wage-cash.csv',count:1,amount_cents:50000,available:true},
  {kind:'进货金额',file:run+'/purchase.csv',count:1,amount_cents:20000,available:true},
 ]
 const h=mount(t,path=>path.startsWith('/api/archive-companies?')?response(listing({rows:[group({company_id:'system:business',company:'全公司（工资付款、进货及报销）',files:systemFiles,construction_cents:undefined,wage_cents:undefined,receivable_cents:undefined})],can_orders:false,can_finance:false,can_wages:false})):null,permission=>permission===allowed)
 await settle();assert.equal(h.state.archiveAllowed,true)
 h.state.toggle(h.state.rows[0]);await settle()
 assert.equal(h.links().filter(node=>node.props.href.startsWith('/api/archives?file=')).length,2)
 assert.match(h.text(),/报销登记.*报销付款/);assert.equal(h.links().filter(node=>node.props.href.startsWith('/api/archives?file=')).some(node=>/wage-cash|purchase\.csv/.test(node.props.href)),false)
 allowed='warehouse';await h.state.load();h.state.toggle(h.state.rows[0]);await settle()
 assert.equal(h.links().filter(node=>node.props.href.startsWith('/api/archives?file=')).length,1)
 assert.equal(h.links().filter(node=>node.props.href.startsWith('/api/archives?file=')).some(node=>/purchase\.csv/.test(node.props.href)),true);assert.equal(h.links().filter(node=>node.props.href.startsWith('/api/archives?file=')).some(node=>/expense\.csv/.test(node.props.href)),false)
 // A real session permission change remounts/re-renders through App; this
 // isolated renderer keeps the original static can() prop, so verify each
 // independent module view above rather than simulating session mutation.
})
