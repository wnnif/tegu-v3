import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
import {canAccess,canFillCraft,pageAllowed,SessionChangedError} from './sessionAccess.ts'

const user=(role,permissions=[])=>({id:role,role,permissions})
test('public craft grants are exact across companies and require the order module',()=>{
  const employee={...user('employee',['orders']),crafts:['public-source']}
  assert.equal(canFillCraft(employee,'company-a-fixed'),false)
  assert.equal(canFillCraft(employee,'company-b-fixed'),false)
  for(const company of ['company-a','company-b'])assert.equal(canFillCraft(employee,'public-source'),true,company)
  assert.equal(canFillCraft({...employee,crafts:['开工']},'开工'),false)
  assert.equal(canFillCraft({...employee,permissions:[]},'public-source'),false)
  assert.equal(canAccess(employee,'orders_temporary'),false)
  assert.equal(canFillCraft(user('operator'),'public-source'),true)
})
test('employee grants cannot bypass account, backup, wage or finance page permissions',()=>{
  const employee=user('employee',['orders','rates','users','backups'])
  assert.equal(canAccess(employee,'users'),false)
  assert.equal(canAccess(employee,'backups'),false)
  for(const page of ['users','audit','backups','wages','finance'])assert.equal(pageAllowed(employee,page),false,page)
  for(const page of ['dashboard','account','orders','projects','archives','crafts'])assert.equal(pageAllowed(employee,page),true,page)
})
test('operator retains business wages and accounts but never backup maintenance',()=>{
  const operator=user('operator',['backups'])
  for(const page of ['wages','finance','users','audit','projects'])assert.equal(pageAllowed(operator,page),true,page)
  assert.equal(pageAllowed(operator,'backups'),false)
  assert.equal(pageAllowed(user('admin'),'backups'),true)
})
test('expense access is independent from wage and company-receipt permissions',()=>{
  const employee=user('employee',['expenses'])
  assert.equal(pageAllowed(employee,'expenses'),true)
  assert.equal(pageAllowed(user('employee',['wages']),'expenses'),false)
  assert.equal(pageAllowed(user('employee',['finance']),'expenses'),false)
  assert.equal(pageAllowed(user('employee',['expenses']),'wages'),false)
  assert.equal(pageAllowed(user('employee',['expenses']),'finance'),false)
  assert.equal(pageAllowed(user('operator'),'expenses'),true)
})
test('finance summary requires finance and archive supports each independent business ledger',()=>{
 assert.equal(pageAllowed(user('employee',['finance']),'finance-summary'),true)
 for(const permission of ['wages','expenses','warehouse','customer_prices'])assert.equal(pageAllowed(user('employee',[permission]),'finance-summary'),false)
 for(const permission of ['expenses','warehouse'])assert.equal(pageAllowed(user('employee',[permission]),'archives'),true)
 assert.equal(pageAllowed(null,'finance-summary'),false)
})
test('page guard denies signed-out users and unknown pages, and checks shared entries separately',()=>{
  assert.equal(pageAllowed(null,'dashboard'),false)
  assert.equal(pageAllowed(user('admin'),'unknown'),false)
  assert.equal(pageAllowed(user('admin'),'toString'),false)
  assert.equal(pageAllowed(user('employee',[]),'archives'),false)
  assert.equal(pageAllowed(user('employee',['customers']),'projects'),true)
  assert.equal(pageAllowed(user('employee',['customers']),'orders'),false)
  assert.equal(pageAllowed(user('employee',['wages']),'archives'),true)
  assert.equal(pageAllowed(user('employee',['wages']),'projects'),false)
})

// Execute the production request module; replace only its relative import URL for Node.
const source=(await readFile(new URL('./api.ts',import.meta.url),'utf8')).replaceAll("'./sessionAccess'",JSON.stringify(new URL('./sessionAccess.ts',import.meta.url).href))
const compiled=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText
const {api,apiSession,onSessionExpired}=await import('data:text/javascript,'+encodeURIComponent(compiled))
const json=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}

test('late wage response from a previous login is rejected before returning to its caller',async(t)=>{
  const response=deferred()
  t.mock.method(globalThis,'fetch',()=>response.promise)
  const request=api('/orders?view=wages')
  apiSession.invalidate()
  response.resolve(json({rows:[{wage_cents:12345}]}))
  await assert.rejects(request,SessionChangedError)
})
test('account switch during JSON download also rejects the previous login response',async(t)=>{
  const body=deferred(),started=deferred()
  t.mock.method(globalThis,'fetch',async()=>({ok:true,status:200,headers:new Headers({'Content-Type':'application/json'}),json(){started.resolve();return body.promise}}))
  const request=api('/backup-settings')
  await started.promise
  apiSession.invalidate()
  body.resolve({access_key:'old-session-test-value'})
  await assert.rejects(request,SessionChangedError)
})
test('late unauthorized response cannot expire a newly signed-in account',async(t)=>{
  let expired=0
  onSessionExpired(()=>{expired++;apiSession.invalidate()})
  const response=deferred()
  t.mock.method(globalThis,'fetch',()=>response.promise)
  const request=api('/order?id=old')
  apiSession.invalidate()
  response.resolve(json({error:'登录失效'},401))
  await assert.rejects(request,SessionChangedError)
  assert.equal(expired,0)
})
test('current unauthorized response clears its session while bad login keeps the form error',async(t)=>{
  let expired=0
  onSessionExpired(()=>{expired++;apiSession.invalidate()})
  t.mock.method(globalThis,'fetch',async()=>json({error:'账号或密码错误'},401))
  await assert.rejects(api('/login',{username:'test',password:'test'}),/账号或密码错误/)
  assert.equal(expired,0)
  await assert.rejects(api('/reference'),SessionChangedError)
  assert.equal(expired,1)
})
test('new session request succeeds after an old network failure without publishing its error',async(t)=>{
  const response=deferred()
  const mock=t.mock.method(globalThis,'fetch',()=>response.promise)
  const oldRequest=api('/orders?view=wages')
  apiSession.invalidate()
  response.reject(new Error('old connection'))
  await assert.rejects(oldRequest,SessionChangedError)
  mock.mock.mockImplementation(async()=>json({rows:[]}))
  assert.deepEqual(await api('/orders?view=orders'),{rows:[]})
})
