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

const transpile=source=>ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText
const moduleUrl=source=>'data:text/javascript,'+encodeURIComponent(transpile(source))
const apiSource=(await readFile(new URL('./api.ts',import.meta.url),'utf8')).replaceAll("'./sessionAccess'",JSON.stringify(new URL('./sessionAccess.ts',import.meta.url).href))
const apiUrl=moduleUrl(apiSource)
const {descriptor}=parse(await readFile(new URL('./App.vue',import.meta.url),'utf8'))
const script=compileScript(descriptor,{id:'app-session-regression'})
const stub='data:text/javascript,export default {render(){return null}}'
const resolved=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>{
  const url=path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:path.endsWith('.vue')?stub:path==='../../VERSION?raw'?'data:text/javascript,export default "1.04"':path==='./api'?apiUrl:new URL(path+'.ts',import.meta.url).href
  return 'from '+JSON.stringify(url)
})
const {default:App}=await import(moduleUrl(resolved))
// Run the real setup/lifecycle without a browser; browser rendering is checked separately.
App.render=()=>null
const template=compileTemplate({source:descriptor.template.content,id:'app-session-regression',compilerOptions:{bindingMetadata:script.bindings}})
assert.deepEqual(template.errors,[])
const renderSource=template.code.replaceAll('from "vue"','from '+JSON.stringify(new URL('../node_modules/vue/index.mjs',import.meta.url).href))
const {render:actualRender}=await import(moduleUrl(renderSource))
const renderer=createRenderer({
  createElement:type=>({type,tagName:type.toUpperCase(),children:[],style:{},options:[],addEventListener(){},removeEventListener(){},setAttribute(){},removeAttribute(){}}),createText:text=>({text}),createComment:text=>({text}),
  setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text},patchProp(node,key,oldValue,newValue){node[key]=newValue},
  insert(node,parent,anchor){if(node.parent)node.parent.children=node.parent.children.filter(child=>child!==node);node.parent=parent;const index=anchor?parent.children.indexOf(anchor):-1;if(index<0)parent.children.push(node);else parent.children.splice(index,0,node)},
  remove(node){if(node.parent)node.parent.children=node.parent.children.filter(item=>item!==node)},
  parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,
})
const users={
  admin:{id:'a',username:'admin',name:'测试管理员',role:'admin',permissions:[],crafts:['开工']},
  employee:{id:'e',username:'employee',name:'测试员工',role:'employee',permissions:['orders','rates'],crafts:['开工']},
  operator:{id:'o',username:'operator',name:'测试普通管理员',role:'operator',permissions:[],crafts:['开工']},
}
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const waitRender=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}
function mount(t,options={}){
  let current=users.admin
  t.mock.method(globalThis,'fetch',async(path,init)=>{
    if(options.handle){const override=options.handle(path,init,current);if(override)return override}
    if(path==='/api/login'){current=users[JSON.parse(init.body).username];return response({ok:true})}
    if(path==='/api/logout'){current=null;return response({ok:true})}
    if(path==='/api/me')return response({user:current,categories:['开工']})
    if(path==='/api/reference')return response({companies:[],workers:[],products:[],warehouses:[],crafts:[]})
    if(path.startsWith('/api/orders?'))return current.role==='employee'&&path.includes('view=wages')?response({error:'无工资权限'},403):response({rows:[{id:'secret-order',wage_cents:54321}],total:1,wage_cents:54321,total_cents:100000})
    if(path==='/api/backup-settings')return response({enabled:true,access_key:'old-account-value',secret:'',retain:168,interval:60})
    throw new Error('Unexpected test URL '+path)
  })
  const app=renderer.createApp(options.render?{...App,render:actualRender}:App)
  const instance=app.mount(options.root||{children:[]})
  t.after(()=>app.unmount())
  return instance.$.setupState
}

test('same-tab admin to employee to operator clears sensitive root state and returns to permitted home',async(t)=>{
  const state=mount(t)
  await waitRender()
  state.go('orders');await waitRender()
  assert.equal(state.orderRows[0].wage_cents,54321)
  state.dialog='order';state.orderDraft.lines=[{wage_cents:54321}];state.printData={wage_cents:54321};state.selected=['secret-order'];state.projectOptions=[{id:'old-project'}]
  state.backupConfig={access_key:'old-account-value',secret:'test-only'}
  await state.signOut()
  assert.equal(state.user,null)
  assert.deepEqual(state.orderRows,[])
  assert.deepEqual(state.orderDraft.lines,[])
  assert.deepEqual(state.projectOptions,[])
  assert.equal(state.dialog,'');assert.equal(state.printData,null)
  assert.equal(state.backupConfig.access_key,'');assert.equal(state.backupConfig.secret,'')
  state.login.username='employee';state.login.password='test'
  await state.signIn()
  assert.equal(state.page,'dashboard')
  assert.deepEqual(state.orderRows,[])
  assert.equal(state.can('wages'),false)
  state.go('wages');assert.equal(state.page,'dashboard')
  await state.signOut()
  state.login.username='operator';state.login.password='test'
  await state.signIn()
  assert.equal(state.page,'dashboard')
  assert.equal(state.can('wages'),true)
  state.go('backups');assert.equal(state.page,'dashboard')
})
test('a denied list refresh clears old amounts and an unauthorized page is redirected',async(t)=>{
  let deny=false
  const state=mount(t,{handle:path=>deny&&path.startsWith('/api/orders?')?response({error:'无工资权限'},403):null})
  await waitRender()
  state.go('orders');await waitRender()
  assert.equal(state.totalAmount,100000)
  deny=true;await state.load()
  assert.deepEqual(state.orderRows,[]);assert.equal(state.totalAmount,undefined);assert.equal(state.total,0)
  state.user=users.employee;state.page='wages';state.orderRows=[{wage_cents:98765}]
  await state.load()
  assert.equal(state.page,'dashboard');assert.deepEqual(state.orderRows,[])
})
test('finance source navigation sends the chosen date range in the first orders request',async(t)=>{
 const requests=[]
 const state=mount(t,{handle:(path,init)=>{
  if(path.startsWith('/api/orders?')){requests.push({path,init});return response({rows:[],total:0,total_cents:0,wage_cents:0})}
  return null
 }})
 await waitRender()
 state.navigateFinanceSource({view:'orders',scope:'period',filters:{from:'2026-09-24',to:'2026-10-12'}})
 await waitRender()
 const query=new URL('http://local'+requests[0].path).searchParams
 assert.equal(query.get('from'),'2026-09-24')
 assert.equal(query.get('to'),'2026-10-12')
 assert.equal(query.has('month'),false)
 assert.equal(query.get('view'),'orders')
})
test('old in-flight App list cannot replace the newly logged-in employee state',async(t)=>{
  let resolveOld,requestStarted
  const started=new Promise(resolve=>{requestStarted=resolve})
  let delayed=true
  const state=mount(t,{handle:path=>{
    if(delayed&&path.startsWith('/api/orders?')){requestStarted();return new Promise(resolve=>{resolveOld=resolve})}
    return null
  }})
  await waitRender()
  state.go('orders');await started
  await state.signOut()
  state.login.username='employee';state.login.password='test'
  await state.signIn()
  delayed=false;resolveOld(response({rows:[{wage_cents:98765}],total:1,wage_cents:98765}))
  await waitRender()
  assert.equal(state.user.id,'e');assert.equal(state.page,'dashboard');assert.deepEqual(state.orderRows,[]);assert.equal(state.totalAmount,undefined)
  assert.equal(state.error,'')
})
test('server editability locks an order without exposing its wage status',async(t)=>{
  const state=mount(t)
  await waitRender()
  state.user=users.employee
  state.orderDraft={id:'saved-order',status:'进行中',editable:false}
  assert.equal(state.orderLocked,true)
  assert.equal(Object.hasOwn(state.orderDraft,'wage_paid'),false)
  state.orderDraft={id:'saved-order',status:'进行中',editable:true}
  assert.equal(state.orderLocked,false)
  state.orderDraft={id:'',status:'进行中'}
  assert.equal(state.orderLocked,false)
})

test('finance and wage root refresh delegate to their panels without old detail requests',async(t)=>{
  const calls=[]
  const state=mount(t,{handle:path=>{calls.push(path);return null}})
  await waitRender()
  state.go('finance');await waitRender()
  assert.equal(calls.some(path=>path.startsWith('/api/orders?')),false)
  let financeRefresh=0,wageRefresh=0
  state.ticketFinancePanel={refresh:async()=>{financeRefresh++}}
  await state.load();assert.equal(financeRefresh,1)
  state.go('wages');await waitRender()
  state.wageFinancePanel={refresh:async()=>{wageRefresh++}}
  await state.load();assert.equal(wageRefresh,1)
  assert.equal(calls.some(path=>path.includes('view=wages')||path.includes('view=finance')),false)
  state.go('orders');await waitRender()
  assert.equal(calls.some(path=>path.includes('view=orders')),true)
})


test('同一个员工撤销customer_prices清除旧价缓存草稿及打印，旧在途列表不得复活金额',async(t)=>{
 let granted=true,hold=false,startedResolve,lateResolve
 const started=new Promise(resolve=>{startedResolve=resolve}),late=new Promise(resolve=>{lateResolve=resolve})
 const employee={...users.employee,permissions:['orders','customer_prices']}
 const state=mount(t,{handle:(path,init)=>{
  if(path==='/api/me')return response({user:{...employee,permissions:granted?['orders','customer_prices']:['orders']},categories:['开工']})
  if(path.startsWith('/api/orders?')){if(hold){hold=false;startedResolve();return late}return response({rows:[granted?{id:'priced',total_cents:246800,price_cents:9876}:{id:'hidden'}],total:1,...(granted?{total_cents:246800}:{})})}
  return null
 }})
 await waitRender();state.go('orders');await waitRender();assert.equal(state.can('customer_prices'),true);assert.equal(state.orderRows[0].total_cents,246800)
 state.dialog='order';state.orderDraft.lines=[{id:'draft',price_cents:9876,total_cents:246800}];state.printData={total_cents:246800};state.references.crafts=[{id:'cached',price_cents:9876}];state.selected=['priced']
 hold=true;let oldLoadError;const oldLoad=state.load().catch(error=>{oldLoadError=error});await started
 granted=false;await state.refreshSessionAccess();await waitRender()
 assert.equal(state.user.id,employee.id);assert.equal(state.can('orders'),true);assert.equal(state.can('customer_prices'),false);assert.equal(state.dialog,'');assert.deepEqual(state.orderDraft.lines,[]);assert.equal(state.printData,null);assert.deepEqual(state.references.crafts,[]);assert.deepEqual(state.selected,[]);assert.equal(state.totalAmount,undefined)
 assert.match(state.notice,/账号权限已更新/);assert.equal(state.orderRows[0].id,'hidden');assert.equal(Object.hasOwn(state.orderRows[0],'total_cents'),false)
 lateResolve(response({rows:[{id:'late-secret',total_cents:246800}],total:1,total_cents:246800}));await oldLoad;await waitRender();assert.equal(oldLoadError?.name,'SessionChangedError')
 assert.equal(state.orderRows[0].id,'hidden');assert.equal(state.totalAmount,undefined);assert.equal(state.error,'')
})
