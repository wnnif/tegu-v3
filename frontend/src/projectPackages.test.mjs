import {test} from 'node:test'
import assert from 'node:assert/strict'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'

registerHooks({resolve(specifier,context,nextResolve){
  if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')){
    const candidate=new URL(specifier,context.parentURL)
    if(existsSync(fileURLToPath(candidate)+'.ts'))return nextResolve(specifier+'.ts',context)
  }
  return nextResolve(specifier)
}})

const {packageCoverageGroups,packageDisplayLabel}=await import('./projects.ts')
const {ticketPrintOrder}=await import('./projectWork.ts')

test('fixed-scope package labels omit room count while existing room packages keep it',()=>{
  assert.equal(packageDisplayLabel({id:'fixed',name:'百姓家墙地固整套收费',package_type:'fixed_scope',room_count:0}), '百姓家墙地固整套收费')
  assert.equal(packageDisplayLabel({id:'room-2',name:'墙地固普通工艺',package_type:'room_count',room_count:2}), '墙地固普通工艺 · 2 房')
})

test('included but unmapped package items are informational and stay separate from unresolved coverage',()=>{
  assert.deepEqual(packageCoverageGroups([
    {item_name:'墙固·墙面',mapping:'mapped'},
    {item_name:'门框改色',mapping:'included_unmapped'},
    {item_name:'项目归属不明',mapping:'pending_mapping'},
  ]),{
    mapped:['墙固·墙面'],
    includedUnmapped:['门框改色'],
    pending:['项目归属不明'],
  })
})

test('fixed-scope ticket package keeps its type in print data and remains a single charge row',()=>{
  const print=ticketPrintOrder({
    ticket:{number:'P-fixed',status:'草稿',total_cents:180000,package_charge:{id:'fixed',name:'百姓家墙地固整套收费',package_type:'fixed_scope',room_count:0,amount_cents:180000}},
    orders:[],versions:[],
  })
  assert.deepEqual(print.package_charge,{name:'百姓家墙地固整套收费',package_type:'fixed_scope',room_count:0,amount_cents:180000})
  assert.equal(print.total_cents,180000)
})
