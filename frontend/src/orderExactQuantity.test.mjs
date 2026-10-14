import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {parse,compileScript} from '@vue/compiler-sfc'
import {createSSRApp} from 'vue'
import {renderToString} from '@vue/server-renderer'
import ts from 'typescript'
import {lineAmount,lineQuantity,lineQuantityMicro,quantityLabel} from './orderSegments.ts'

registerHooks({resolve(specifier,context,nextResolve){
  if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')){
    const candidate=new URL(specifier,context.parentURL)
    if(existsSync(fileURLToPath(candidate)+'.ts'))return nextResolve(specifier+'.ts',context)
  }
  return nextResolve(specifier,context)
}})
async function componentURL(name){
  const file=new URL('./components/'+name+'.vue',import.meta.url)
  const {descriptor}=parse(await readFile(file,'utf8'))
  const script=compileScript(descriptor,{id:'exact-quantity-'+name,inlineTemplate:true})
  const dependencies=new Map()
  for(const match of script.content.matchAll(/from\s+(['"])([^'"]+)\1/g)){const path=match[2];if(path.endsWith('.vue'))dependencies.set(path,await componentURL(path.split('/').pop().replace('.vue','')))}
  const source=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:path.endsWith('.vue')?dependencies.get(path):new URL(path+'.ts',file).href))
  const compiled=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText
  return 'data:text/javascript,'+encodeURIComponent(compiled)
}
const component=async(name,inlineTemplate=true)=>(await import(await componentURL(name,inlineTemplate))).default

// Expected micro quantities use the Go calcLine contract: exact decimal
// dimensions, sum all segments, round once to six places, then round cents.
test('area half-micro quantity matches saved 23.37 rather than binary-float 23.36',()=>{
  const line={mode:'平方',price_cents:2333,segments:[{length:1.001,width:1.0005}]}
  assert.equal(lineQuantityMicro(line),1001501n)
  assert.equal(lineQuantity(line),1.001501)
  assert.equal(lineAmount(line),2337)
  assert.equal(quantityLabel(line),'1.001501 ㎡')
  assert.equal(lineQuantityMicro({...line,segments:[{length:'1.001',width:'1.0005'}]}),1001501n)
})

test('multi-segment area sums exact products before one quantity rounding',()=>{
  const line={mode:'平方',price_cents:100000000,segments:[{length:'0.0000003',width:1},{length:'0.0000003',width:1}]}
  assert.equal(lineQuantityMicro(line),1n)
  assert.equal(lineAmount(line),100)
  assert.equal(lineQuantityMicro({...line,segments:[{length:1.001,width:1.0005},{length:1.001,width:1.0005}]}),2003001n)
})

test('volume multiplication and meter sums share the exact half-up boundary',()=>{
  const volume={mode:'立方',price_cents:2333,segments:[{length:1.001,width:1.0005,height:1}]}
  assert.equal(lineQuantityMicro(volume),1001501n)
  assert.equal(lineAmount(volume),2337)
  assert.equal(quantityLabel(volume),'1.001501 m³')
  const meter={mode:'米',price_cents:100000000,segments:[{length:'3e-7'},{length:'3e-7'}]}
  assert.equal(lineQuantityMicro(meter),1n)
  assert.equal(lineAmount(meter),100)
  assert.equal(quantityLabel(meter),'0.000001 米')
  assert.equal(lineQuantityMicro({...meter,segments:[{length:'1.0000005'}]}),1000001n)
})

test('upper valid quantities remain exact through micro quantity and cents consumers',()=>{
  const line={mode:'平方',price_cents:100000000,segments:[{length:'9999.99999999',width:100}]}
  assert.equal(lineQuantityMicro(line),999999999999n)
  assert.equal(lineQuantity(line),999999.999999)
  assert.equal(quantityLabel(line),'999999.999999 ㎡')
  assert.equal(lineAmount(line),99999999999900)
  assert.equal(lineAmount({mode:'个',price_cents:100000000,segments:[{count:1000000}]}),100000000000000)
})

test('included price retains precise quantity and fixed amounts remain one item',()=>{
  const line={mode:'平方',price_cents:2333,use_included_price:true,segments:[{length:1.001,width:1.0005}]}
  assert.equal(lineQuantityMicro(line),1001501n)
  assert.equal(lineAmount(line),0)
  assert.equal(lineAmount({mode:'金额',entered:true,price_cents:2333}),2333)
  assert.equal(quantityLabel({mode:'金额',entered:true}),'1 项')
  assert.equal(lineAmount({mode:'金额',entered:false,price_cents:2333}),0)
})

test('unfinished or nonfinite dimensions do not throw or show a billed quantity',()=>{
  for(const length of ['',undefined,NaN,Infinity,-1]){
    const line={mode:'平方',price_cents:2333,segments:[{length,width:1}]}
    assert.equal(lineQuantityMicro(line),0n)
    assert.equal(lineAmount(line),0)
  }
  const line={mode:'平方',price_cents:2333,segments:[{length:'1.0010000000000000000000001',width:1.0005}]}
  // The request consumer converts dimensions to Number before posting to Go.
  assert.equal(lineQuantityMicro(line),1001501n)
})

test('actual subitem, craft and company total templates show the same saved cents',async()=>{
  const line={id:'',craft_id:'fixed',category:'防水涂料',name:'合成防水',mode:'平方',price_cents:2333,editable:true,segments:[{length:1.001,width:1.0005,surface:'地面'}]}
  const draft={id:'',company_id:'company',project_id:'',worker_id:'worker',lines:[line],materials:[],sales:[]}
  const references={companies:[{id:'company',ticket_mode:'single'}],crafts:[{id:'fixed',company_id:'company',editable:true}],workers:[{id:'worker',crafts:['防水涂料'],active:1}],warehouses:[],products:[]}
  const common={draft,references,can:()=>true,allowedCrafts:['防水涂料']}
  const craftHTML=await renderToString(createSSRApp(await component('OrderCraftEditor'),{...common,locked:false}))
  assert.match(craftHTML,/1\.001501 ㎡/)
  assert.match(craftHTML,/23\.37 元/);assert.equal((craftHTML.match(/<strong>23\.37 元<\/strong>/g)||[]).length,1,'line amount subtotal is displayed once')
  assert.doesNotMatch(craftHTML,/23\.36/)
  const editorHTML=await renderToString(createSSRApp(await component('OrderEditor'),{...common,projects:[],projectPage:1,projectTotal:0,workerOptions:[],companyCraftCategories:[],selectedCategory:'',inventory:[],busy:false,orderLocked:false,orderWarehouse:''}))
  assert.equal((editorHTML.match(/class="order-total-value"/g)||[]).length,1,'customer order total is displayed once')
  assert.match(editorHTML,/整张施工单客户收费合计（含售料）：<\/span><strong[^>]*>¥ 23\.37<\/strong>/)
  assert.doesNotMatch(editorHTML,/23\.36/)
  assert.doesNotMatch(editorHTML,/23\.36/)
})
