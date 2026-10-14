import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {parse,compileScript} from '@vue/compiler-sfc'
import {createRenderer,createSSRApp} from 'vue'
import {renderToString} from '@vue/server-renderer'
import ts from 'typescript'

registerHooks({resolve(specifier,context,nextResolve){
  if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')){
    const candidate=new URL(specifier,context.parentURL)
    if(existsSync(fileURLToPath(candidate)+'.ts'))return nextResolve(specifier+'.ts',context)
  }
  return nextResolve(specifier,context)
}})
const componentURLs=new Map()
async function componentURL(name,inlineTemplate=true){
  const file=name instanceof URL?name:new URL('./components/'+name+'.vue',import.meta.url)
  const key=file.href+'#inlineTemplate='+inlineTemplate
  if(componentURLs.has(key))return componentURLs.get(key)
  const {descriptor}=parse(await readFile(file,'utf8'),{filename:fileURLToPath(file)})
  const script=compileScript(descriptor,{id:'length-regression-'+file.pathname,inlineTemplate})
  const js=ts.transpileModule(script.content,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText
  const dependencies=new Map()
  // Every Vue dependency in the tested subtree is real, including shared shells.
  for(const match of js.matchAll(/from\s+(['"])([^'"]+)\1/g)){
    const path=match[2]
    dependencies.set(path,path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:
      path.endsWith('.vue')?await componentURL(new URL(path,file)):
      path.startsWith('.')?new URL(path.endsWith('.ts')?path:path+'.ts',file).href:import.meta.resolve(path))
  }
  const source=js.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(dependencies.get(path)))
  const url='data:text/javascript,'+encodeURIComponent(source)
  componentURLs.set(key,url)
  return url
}
const component=async(name,inlineTemplate=true)=>(await import(await componentURL(name,inlineTemplate))).default
const OrderCraftEditor=await component('OrderCraftEditor')
const EntityEditor=await component('EntityEditor')
const OrderCraftIdentity=await component('OrderCraftIdentity')
const line=()=>({id:'',craft_id:'craft',category:'包管',name:'合成长度工艺',spec:'',region:'位置',description:'',mode:'米',price_cents:3000,wage_cents:1000,
  temporary:false,editable:true,entered:false,segments:[{mode:'米',length:2.5,width:0,height:0,count:0,amount_cents:0},{mode:'米',length:1.25,width:0,height:0,count:0,amount_cents:0}]})
function orderProps(item=line()){
  return {draft:{category_workers:{'包管':'worker'},company_id:'company',worker_id:'worker',lines:[item]},references:{crafts:[{id:'craft',company_id:'company',editable:true}],
    workers:[{id:'worker',crafts:['包管'],active:1}]},locked:false,can:()=>true,allowedCrafts:['包管']}
}
const html=(component,props)=>renderToString(createSSRApp(component,props))

test('按米固定和临时工艺实际表单只显示长度，并显示按米单价和多段合计',async()=>{
  const fixed=await html(OrderCraftEditor,orderProps())
  assert.match(fixed,/长度（米）/)
  assert.doesNotMatch(fixed,/宽（米）|高（米）/)
  assert.match(fixed,/max="10000"/)
  assert.match(fixed,/3\.75 米/)
  assert.match(fixed,/112\.50 元/)
  assert.match(fixed,/30\.00 元<span>\/米<\/span>/)
  assert.match(fixed,/＋ 添加一段/)
  const temporary=await html(OrderCraftEditor,orderProps({...line(),temporary:true,craft_id:''}))
  assert.match(temporary,/客户单价（元\/米）/)
  assert.match(temporary,/工资单价（元\/米）/)
  assert.match(temporary,/<label>计量<select[^>]*>[\s\S]*?米[\s\S]*?<\/select>/)
  assert.doesNotMatch(temporary,/宽（米）|高（米）/)
})

test('工艺维护及公司固定工艺的工资和收费单位显示元每米',async()=>{
  const craft={id:'fixed',source_id:'source',source_active:true,name:'合成长度工艺',category:'包管',mode:'米',price_cents:3000,wage_cents:1000,subitems:[]}
  const props={editing:{entity:'crafts',name:'合成长度工艺',mode:'米',wage_cents:1000,fixed_crafts:[],subitems:[]},categories:['包管'],references:{crafts:[]},busy:false,
    craftCategory:'全部',companyCraftOptions:[],companyFixedVisible:[],craftPage:1,craftTotal:0,can:()=>true,hasCompanyCraft:()=>false}
  assert.match(await html(EntityEditor,props),/公共标准工资（元\/米）/)
  const company=await html(EntityEditor,{...props,editing:{...props.editing,entity:'companies',fixed_crafts:[craft],ticket_mode:'single'},companyFixedVisible:[craft]})
  assert.match(company,/收费单价（元\/米）/)
  assert.match(company,/公共标准工资（元\/米）/)
})

test('按家计量在基础资料、公司收费及临时项目中保留整数单位',async()=>{
  const craft={id:'household',source_id:'household-source',source_active:true,name:'按家收费',category:'开工',mode:'家',price_cents:10000,wage_cents:3000,subitems:[]}
  const props={editing:{entity:'crafts',name:'按家收费',mode:'家',wage_cents:3000,fixed_crafts:[],subitems:[]},categories:['开工'],references:{crafts:[]},busy:false,
    craftCategory:'全部',companyCraftOptions:[],companyFixedVisible:[craft],craftPage:1,craftTotal:0,can:()=>true,hasCompanyCraft:()=>false}
  const publicEditor=await html(EntityEditor,props)
  assert.match(publicEditor,/公共标准工资（元\/家）/)
  assert.match(publicEditor,/<select[^>]*>[\s\S]*?<option[^>]*>家<\/option>[\s\S]*?<\/select>/)
  const companyEditor=await html(EntityEditor,{...props,editing:{...props.editing,entity:'companies',fixed_crafts:[craft],ticket_mode:'single'}})
  assert.match(companyEditor,/收费单价（元\/家）/)
  assert.match(companyEditor,/公共标准工资（元\/家）/)
  assert.match(companyEditor,/按家填写整数数量/)
  const temporary=await html(OrderCraftIdentity,{line:{...line(),temporary:true,mode:'家',wage_cents:3000},editable:true,removable:true,canWages:true,allowedCrafts:['开工']})
  assert.match(temporary,/工资单价（元\/家）/)
  assert.match(temporary,/<select[^>]*>[\s\S]*?<option[^>]*>家<\/option>[\s\S]*?<\/select>/)
})

test('公司特殊工资覆盖的空值恢复继承，0仍是明确零工资',async t=>{
  const componentState=await component('EntityEditor',false)
  componentState.render=()=>null
  const renderer=createRenderer({createElement:type=>({type,children:[]}),createText:text=>({text}),createComment:text=>({text}),
    setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text},patchProp(){},
    insert(node,parent){node.parent=parent;parent.children.push(node)},remove(){},parentNode:node=>node.parent,nextSibling:()=>null})
  const craft={wage_override_cents:550}
  const props={editing:{entity:'companies',name:'合成公司',fixed_crafts:[craft],ticket_mode:'single'},categories:['开工'],references:{crafts:[]},busy:false,
    craftCategory:'全部',companyCraftOptions:[],companyFixedVisible:[craft],craftPage:1,craftTotal:0,can:()=>true,hasCompanyCraft:()=>false}
  const app=renderer.createApp(componentState,props)
  const instance=app.mount({children:[]})
  t.after(()=>app.unmount())
  const setOverride=instance.$.setupState.setCompanyWageOverride
  setOverride(craft,{target:{value:'  '}})
  assert.equal(craft.wage_override_cents,null)
  setOverride(craft,{target:{value:'0'}})
  assert.equal(craft.wage_override_cents,0)
  assert.match(await html(EntityEditor,props),/留空恢复工艺小项工资；小项未单独设置时沿用公共工资。填写0为明确的零工资/)
})

test('临时计量切换到米清空旧宽高数量与整项施工状态',async(t)=>{
  const componentState=await component('OrderCraftEditor',false)
  componentState.render=()=>null
  const renderer=createRenderer({createElement:type=>({type,children:[]}),createText:text=>({text}),createComment:text=>({text}),
    setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text},patchProp(){},
    insert(node,parent){node.parent=parent;parent.children.push(node)},remove(){},parentNode:node=>node.parent,nextSibling:()=>null})
  const item={...line(),temporary:true,mode:'米',entered:true,segments:[{mode:'立方',length:2,width:3,height:4,count:5,amount_cents:10000}]}
  const app=renderer.createApp(componentState,orderProps(item))
  const instance=app.mount({children:[]})
  t.after(()=>app.unmount())
  instance.$.setupState.changeMode(item)
  assert.equal(item.entered,false)
  assert.deepEqual(item.segments,[{mode:'米',surface:'地面',length:'',width:'',height:'',count:'',amount_cents:0}])
  assert.equal(item.price_cents,3000)
  assert.equal(item.wage_cents,1000)
})
