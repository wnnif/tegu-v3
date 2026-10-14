import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import {createRenderer,nextTick,reactive,ref} from 'vue'
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
const {useOrders}=await import('./workflows/useOrders.ts')
const {command}=await import('./api.ts')
const {lineAmount,saleAmountCents}=await import('./orderSegments.ts')
const {lineCanEdit}=await import('./orderAccess.ts')
const moduleURL=source=>'data:text/javascript,'+encodeURIComponent(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
const compiled=new Map()
async function component(name){
 if(compiled.has(name))return compiled.get(name)
 const file=new URL('./components/'+name+'.vue',import.meta.url)
 const {descriptor}=parse(await readFile(file,'utf8')),script=compileScript(descriptor,{id:'order-editor-form-'+name}),imports=new Map()
 for(const match of script.content.matchAll(/from\s+(['"])([^'"]+)\1/g)){
  const path=match[2],child=path.split('/').pop().replace('.vue','')
  // Project printing and old settlement details have independent consumer suites.
  imports.set(path,path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:path.endsWith('.vue')?['ProjectWorkPanel','OrderSettlementDetails'].includes(child)?'data:text/javascript,export default {render(){return null}}':await component(child):new URL(path+'.ts',file).href)
 }
 const source=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(imports.get(path)))
 const template=compileTemplate({id:'order-editor-form-'+name,source:descriptor.template.content,filename:name+'.vue',compilerOptions:{bindingMetadata:script.bindings}})
 assert.deepEqual(template.errors,[])
 const render=moduleURL(template.code.replaceAll('from "vue"','from '+JSON.stringify(new URL('../node_modules/vue/index.mjs',import.meta.url).href)))
 const url=moduleURL(source.replace(/^export default/m,'const component =')+'\nimport {render} from '+JSON.stringify(render)+';component.render=render;export default component')
 compiled.set(name,url);return url
}
const {default:OrderEditor}=await import(await component('OrderEditor'))
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done});return {promise,resolve}}
const worker=(id,crafts=['开工','包管','防水涂料','回填'])=>({id,name:'合成人员'+id,version:1,active:1,crafts})
const catalog=(id,mode,category='防水涂料')=>({id,name:'合成'+mode,company_id:'company',version:1,mode,category,editable:true,price_cents:2000,wage_cents:300})
const segment=(mode,values={})=>({mode,surface:'地面',length:'',width:'',height:'',count:'',amount_cents:0,...values})
const line=(id,mode,values={})=>({id:'',craft_id:id,name:'合成'+mode,category:'防水涂料',spec:'',region:id,description:'',mode,price_cents:2000,wage_cents:300,temporary:false,editable:true,entered:false,worker_id:'a',worker_name:'合成人员a',segments:[segment(mode)],...values})
function fixture(){
 const area=line('area','平方',{subitem_id:'bath',region:'主卫',segments:[segment('平方',{length:2,width:3})]})
 return {
  draft:{id:'',version:0,number:'',status:'进行中',company_id:'company',company_name:'合成公司',project_id:'',worker_id:'a',worker_name:'合成人员a',address:'合成地址1栋101',work_date:'2026-10-09',contact:'合成经理',phone:'123',note:'保留备注',materials:[{product_id:'product',warehouse_id:'warehouse',input_unit:'base',input_qty:'2',qty_micro:2000000}],sales:[{id:'',product_id:'product',product_name:'合成商品',warehouse_id:'warehouse',input_qty:'3',input_unit:'base',qty_micro:3000000,price_cents:1500}],delivery_wage_cents:2000,sales_ticket_slot:2,ticket_action:'normal',ticket_id:'',ticket_version:0,
   lines:[area,line('area','平方',{subitem_id:'kitchen',region:'厨房',worker_id:'b',worker_name:'合成人员b',segments:[segment('平方',{length:1,width:2})]}),line('volume','立方',{segments:[segment('立方',{length:2,width:3,height:.5})]}),line('meter','米',{segments:[segment('米',{length:2.5}),segment('米',{length:1.25})]}),line('count','个',{segments:[segment('个',{count:3})]}),line('amount','金额',{entered:true,segments:[]}),line('combo','平方',{included_by_craft_id:'area',use_included_price:true,charge_note:'合成组合含价',segments:[segment('平方',{length:1,width:1})]}),line('','米',{temporary:true,name:'合成临时',region:'临时位置',price_cents:400,wage_cents:100,segments:[segment('米',{length:2})]})]},
  references:{companies:[{id:'company',name:'合成公司',version:1,ticket_mode:'split'}],workers:[worker('a'),worker('b')],warehouses:[{id:'warehouse',name:'合成仓库',version:1,worker_id:'a',active:1}],products:[{id:'product',name:'合成商品',version:1,unit:'桶',aux_unit:'公斤',aux_ratio:10,active:1}],crafts:[catalog('area','平方'),catalog('volume','立方'),catalog('meter','米'),catalog('count','个'),catalog('amount','金额'),catalog('combo','平方')]},
 }
}
function mount(t,options={}){
 const initial=fixture(),draft=reactive(options.draft||initial.draft),references=reactive(options.references||initial.references),busy=ref(false),error=ref(''),dialog=ref('order'),requests=[],events=[]
 const ctx={references:ref(references),inventory:ref([{product_id:'product',warehouse_id:'warehouse',qty_micro:50000000}]),company:ref('company'),categories:ref(['防水涂料']),dialog,error,notice:ref(''),can:options.can||(()=>true),submit:command(),load:async()=>{},perform:async(fn)=>{if(busy.value)return;busy.value=true;error.value='';try{await fn()}catch(e){error.value=e.message}finally{busy.value=false}}}
 const workflow=useOrders(ctx,async()=>{});workflow.orderDraft.value=draft
 t.mock.method(globalThis,'fetch',async(path,init)=>{
  requests.push({path,init});const alternate=options.fetch?.(path,init);if(alternate)return alternate
  const url=new URL(path,'http://synthetic.test')
  if(url.pathname==='/api/reference-options'){
   const entity=url.searchParams.get('entity'),rows=references[entity]||[],id=url.searchParams.get('id')
   return response({rows:id?rows.filter(row=>row.id===id):rows,total:rows.length})
  }
  if(url.pathname==='/api/company-packages')return response({rows:[]})
  if(url.pathname==='/api/projects')return response({rows:[],total:0,page:1})
  if(url.pathname==='/api/order'&&init.method==='POST')return response({id:'saved-synthetic',version:1})
  throw new Error('Unexpected synthetic request '+path)
 })
 const root={type:'root',children:[]}
 function insert(node,parent,anchor=null){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1);const index=anchor?parent.children.indexOf(anchor):-1;parent.children.splice(index<0?parent.children.length:index,0,node);node.parent=parent}
 const renderer=createRenderer({
  createElement:type=>({type,element:type,tagName:type.toUpperCase(),children:[],props:{},value:'',listeners:{},style:{},get options(){return this.children.filter(child=>child.type==='option')},addEventListener(name,fn){this.listeners[name]=fn},getRootNode(){return globalThis.document},setAttribute(){},removeAttribute(){}}),
  createText:text=>({type:'text',text,children:[]}),createComment:text=>({type:'comment',text,children:[]}),
  setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text;node.children=[]},patchProp(node,key,previous,next){node.props[key]=next;node[key]=next},insert,
  remove(node){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1)},parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,
  insertStaticContent(content,parent,anchor){const node={type:'static',text:content.replace(/<[^>]+>/g,''),children:[]};insert(node,parent,anchor);return [node,node]},
 })
 const props=reactive({draft,references,projects:[],projectPage:1,projectTotal:0,workerOptions:references.workers,companyCraftCategories:['防水涂料'],selectedCategory:'',inventory:ctx.inventory.value,busy:false,orderLocked:!!options.locked,orderWarehouse:'warehouse',allowedCrafts:['防水涂料'],can:ctx.can})
 const app=renderer.createApp({render(){return h(OrderEditor,{...props,busy:busy.value,
  onSave:()=>{events.push('save');return workflow.saveOrder()},onLineWorker:(item,person)=>{events.push('lineWorker');workflow.lineWorkerChanged(item,person)},onCategoryWorker:(category,person)=>{events.push('categoryWorker');workflow.categoryWorkerChanged(category,person)},onAddLocation:item=>{events.push('addLocation');return workflow.addLocation(item)},onRemoveLocation:item=>{events.push('removeLocation');return workflow.removeLocation(item)},onTemporary:()=>workflow.addTemporary(),onMaterial:()=>workflow.addMaterial(),onSale:()=>workflow.addSale(),onMaterialProduct:item=>workflow.materialProductSelected(item),onCompany:()=>events.push('company'),onWorker:()=>events.push('worker'),onClose:()=>events.push('close'),onCancelOrder:()=>events.push('cancel'),onPrint:()=>events.push('print')})}})
 app.mount(root);t.after(()=>app.unmount())
 const walk=node=>[node,...node.children.flatMap(walk)],elements=()=>walk(root),text=node=>node.type==='comment'?'':(node.text||'')+node.children.map(text).join('')
 const button=label=>elements().find(node=>node.type==='button'&&text(node)===label)
 const controls=label=>elements().filter(node=>node.type==='label'&&text(node).trim()===label).map(node=>{const owner=node.props.for?node.parent:node;return walk(owner).find(child=>['input','select'].includes(child.element))})
 const setValue=async(node,value)=>{assert.ok(node,'rendered input exists');if(node.props['onUpdate:modelValue'])node.props['onUpdate:modelValue'](value);else node.props.onInput?.({target:{value}});await settle()}
 const select=async(node,value)=>{assert.ok(node,'rendered select exists');node.props.onChange?.({target:{value}});await settle()}
 const submit=async()=>{await elements().find(node=>node.type==='form').props.onSubmit({preventDefault(){}});await settle()}
 return {root,app,draft,references,workflow,props,ctx,busy,error,dialog,requests,events,elements,text:()=>text(root),textOf:text,button,controls,setValue,select,submit,posts:()=>requests.filter(request=>request.init.method==='POST')}
}
const {h}=await import('vue')

test('正常制单使用九列逻辑顺序，配置小项作为部位且不重复空框，整单金额只显示一次',async(t)=>{
 const ui=mount(t);await settle()
 assert.equal(ui.button('正常版'),undefined);assert.equal(ui.button('表格版'),undefined)
 assert.equal(ui.elements().some(node=>node.type==='table'),false)
 const headers=ui.elements().filter(node=>node.props?.role==='columnheader').map(ui.textOf)
 assert.deepEqual(headers,['施工人员','工艺','部位','单价','方量明细','方量小计','金额小计','备注','合计金额'])
 assert.match(ui.text(),/配置部位名称未记录/);assert.match(ui.text(),/实际施工部位/);assert.match(ui.text(),/历史兼容文本（原 region）/);assert.match(ui.text(),/保留旧原文，不代填配置部位或实际施工部位/);assert.equal(ui.controls('实际施工部位').length,ui.draft.lines.filter(row=>!row.subitem_id).length)
 assert.equal(ui.elements().filter(node=>node.props?.class==='order-total-value').length,1)
 assert.equal((ui.text().match(/整张施工单客户收费合计（含售料）/g)||[]).length,1)
 await ui.setValue(ui.controls('施工地址')[0],'合成新地址')
 await ui.setValue(ui.controls('长（米）')[0],'4.5')
 assert.equal(ui.draft.address,'合成新地址');assert.equal(ui.draft.lines[0].segments[0].length,'4.5')
 assert.equal(ui.posts().length,0)
})

test('正常表单保存保留五种计量、多段、小项人员、组合、临时项、售料和耗材',async(t)=>{
 const ui=mount(t);await settle();await ui.submit()
 assert.equal(ui.error.value,'');const payload=JSON.parse(ui.posts()[0].init.body)
 assert.deepEqual(payload.lines.map(item=>item.mode),['平方','平方','立方','米','个','金额','平方','米'])
 assert.deepEqual(payload.lines.slice(0,2).map(item=>item.worker_id),['a','b'])
 assert.deepEqual(payload.lines[3].segments.map(item=>item.length),[2.5,1.25]);assert.deepEqual(payload.lines[5].segments,[])
 assert.equal(payload.lines[6].use_included_price,true);assert.equal(payload.lines[7].temporary,true)
 assert.equal(payload.materials[0].qty_micro,2000000);assert.equal(payload.sales[0].qty_micro,3000000);assert.equal(payload.sales_ticket_slot,2);assert.equal(payload.delivery_wage_cents,2000)
 assert.equal(Object.hasOwn(payload,'entryMode'),false);assert.equal(Object.hasOwn(payload,'layout'),false)
 assert.equal(payload.lines.reduce((sum,item)=>sum+lineAmount(item),0),ui.draft.lines.reduce((sum,item)=>sum+lineAmount(item),0))
})

test('正常表单允许按大类设置默认人并保留小项人员覆盖及全部测量输入',async(t)=>{
 const initial=fixture();initial.draft.category_workers={'防水涂料':'a'};initial.draft.lines[0].worker_id='';initial.draft.lines[0].worker_name='';initial.draft.lines[1].worker_override=true
 const ui=mount(t,initial);await settle()
 await ui.select(ui.controls('防水涂料默认施工人')[0],'b');assert.equal(ui.draft.category_workers['防水涂料'],'b');assert.equal(ui.draft.lines[0].worker_id,'');assert.equal(ui.draft.lines[1].worker_id,'b');assert.equal(ui.draft.lines[1].worker_override,true)
 await ui.select(ui.controls('防水涂料默认施工人')[0],'a');assert.equal(ui.draft.lines[1].worker_id,'b');assert.equal(ui.draft.lines[0].segments[0].length,2)
 await ui.button('单独指定本项').props.onClick();await settle();await ui.select(ui.controls('本项施工人')[0],'b');assert.equal(ui.draft.lines[0].worker_id,'b');assert.equal(ui.draft.lines[0].worker_override,true);assert.equal(ui.draft.lines[1].worker_id,'b')
 assert.equal(ui.draft.worker_id,'a');assert.deepEqual(ui.draft.lines[0].segments[0].length,2)
 await ui.button('＋ 一段尺寸').props.onClick();await settle();assert.equal(ui.draft.lines[0].segments.length,2)
 await ui.setValue(ui.controls('长（米）')[1],'1');await ui.setValue(ui.controls('宽（米）')[1],'2')
 await ui.button('＋ 临时添加项').props.onClick();await settle();assert.equal(ui.draft.lines.length,9)
 await ui.button('＋ 添加销售材料').props.onClick();await ui.button('＋ 添加耗材').props.onClick();await settle()
 assert.equal(ui.draft.sales.length,2);assert.equal(ui.draft.materials.length,2)
 await ui.setValue(ui.controls('销售数量')[0],'4');await ui.setValue(ui.controls('数量')[0],'3')
 assert.equal(ui.draft.lines[0].segments.length,2);assert.equal(ui.draft.sales[0].qty_micro,4000000);assert.equal(ui.draft.materials[0].qty_micro,3000000)
 assert.equal(ui.events.filter(event=>event==='lineWorker').length,1);assert.equal(ui.events.filter(event=>event==='categoryWorker').length,2)
})

test('修改一个工艺大类默认施工人不影响其他大类默认人或小项覆盖',async(t)=>{
 const initial=fixture(),other=line('startup','个',{category:'开工',worker_id:'',worker_name:'',segments:[segment('个',{count:1})]})
 initial.draft.lines[1].worker_override=true
 initial.draft.lines.push(other);initial.draft.category_workers={'防水涂料':'a','开工':'b'}
 initial.references.crafts.push(catalog('startup','个','开工'))
 const ui=mount(t,initial);await settle()
 await ui.select(ui.controls('防水涂料默认施工人')[0],'b')
 assert.equal(ui.draft.category_workers['防水涂料'],'b');assert.equal(ui.draft.category_workers['开工'],'b')
 assert.equal(ui.draft.lines.at(-1).worker_id,'');assert.equal(ui.draft.lines.at(-1).category,'开工')
 assert.equal(ui.draft.lines[1].worker_id,'b');assert.equal(ui.draft.lines[1].worker_override,true)
 assert.equal(ui.draft.lines[0].segments[0].length,2)
})

test('技能不匹配行只读显示或隐藏，不改变草稿、整单金额及提交请求',async(t)=>{
 const initial=fixture();initial.draft.lines=[line('area','平方',{worker_id:'a',worker_name:'合成人员a',description:'保留说明',segments:[segment('平方',{length:2,width:3})]})]
 initial.references.workers[0].crafts=['开工']
 const ui=mount(t,initial);await settle();const before=JSON.stringify(ui.draft.lines),total=ui.draft.lines.reduce((sum,item)=>sum+lineAmount(item),0)+(ui.draft.sales||[]).reduce((sum,item)=>sum+saleAmountCents(item.input_qty||Number(item.sale_qty_micro||item.qty_micro)/1e6,item.price_cents),0)
 const totalText=()=>ui.text().match(/整张施工单客户收费合计（含售料）：¥ ([\d.]+)/)?.[1]
 assert.match(ui.text(),/施工人技能不匹配；内容只读/)
 assert.equal(ui.elements().find(node=>node.type==='fieldset'&&node.props.class==='measure-input').props.disabled,true)
 assert.equal(ui.controls('本项施工人')[0].props.disabled,false,'有工艺填写权时仍可更换不匹配的施工人')
 assert.equal(Number(totalText()),total/100)
 await ui.submit();const validationError=ui.error.value;assert.match(validationError,/施工人未获技能授权/)
 await ui.button('隐藏只读工艺项（1）').props.onClick();await settle()
 assert.equal(ui.elements().filter(node=>node.props?.class==='note-input').length,0,'隐藏时只移除只读行的界面控件');assert.equal(ui.draft.lines[0].description,'保留说明');assert.equal(JSON.stringify(ui.draft.lines),before);assert.equal(Number(totalText()),total/100)
 await ui.submit();assert.equal(ui.error.value,validationError);assert.equal(ui.posts().length,0,'隐藏不能绕过原有技能校验或改变保存请求')
 await ui.button('显示只读工艺项（1）').props.onClick();await settle()
 assert.equal(ui.elements().filter(node=>node.props?.class==='note-input').length,1);assert.equal(ui.draft.lines[0].description,'保留说明');assert.equal(JSON.stringify(ui.draft.lines),before);assert.equal(Number(totalText()),total/100)
})

test('技能只读仍允许选择人员，账号只读与已发记录会锁定人员、尺寸和保存',async(t)=>{
 const initial=fixture();initial.draft.lines=[line('area','平方',{worker_id:'',worker_name:'',segments:[segment('平方')]})];initial.draft.worker_id='';initial.draft.worker_name='';initial.draft.materials=[];initial.draft.sales=[]
 const ui=mount(t,initial);await settle()
 const person=ui.controls('防水涂料默认施工人')[0];assert.equal(person.props.disabled,false)
 const fieldset=ui.elements().find(node=>node.type==='fieldset'&&node.props.class==='measure-input');assert.equal(fieldset.props.disabled,true)
 await ui.select(person,'a');assert.equal(ui.draft.lines[0].worker_id,'');assert.equal(ui.draft.category_workers['防水涂料'],'a');assert.equal(fieldset.props.disabled,false)
 ui.draft.lines[0].editable=false;await settle();assert.equal(ui.controls('本项施工人').length,0);assert.equal(ui.controls('防水涂料默认施工人').length,0);assert.match(ui.text(),/当前账号仅可查看/);assert.match(ui.text(),/合成人员a/)
 ui.draft.id='saved';ui.draft.wage_paid=1;ui.props.orderLocked=true;await settle();assert.match(ui.text(),/已有工资发放，先在工资管理撤销对应发放/);assert.equal(ui.button('保存本次施工 / 送货'),undefined)
 assert.equal(ui.elements().some(node=>node.type==='form'),false,'已锁定施工单只展示保存快照，不提供可提交表单');assert.equal(ui.posts().length,0)
})

test('没有工资权限时正常表单不显示临时工资或带货工资输入',async(t)=>{
 const initial=fixture();for(const item of initial.draft.lines)delete item.wage_cents;delete initial.draft.delivery_wage_cents
 const ui=mount(t,{...initial,can:permission=>permission!=='wages'});await settle()
 assert.doesNotMatch(ui.text(),/工资单价（元|整项工资（元|本次带货总工资/)
 await ui.submit();assert.equal(ui.error.value,'');assert.equal(Object.hasOwn(JSON.parse(ui.posts().at(-1).init.body),'delivery_wage_cents'),false)
})

test('隐藏只读工艺只收起界面行，草稿和总收费不变且保存请求遵守只读权限',async(t)=>{
 const initial=fixture();initial.draft.id='saved';initial.draft.lines[0].id='saved-line';initial.draft.lines[0].editable=false
 const ui=mount(t,initial);await settle();const before=JSON.stringify(ui.draft.lines),total=ui.draft.lines.reduce((sum,item)=>sum+lineAmount(item),0)+(ui.draft.sales||[]).reduce((sum,item)=>sum+saleAmountCents(item.input_qty||Number(item.sale_qty_micro||item.qty_micro)/1e6,item.price_cents),0)
 const totalText=()=>ui.text().match(/整张施工单客户收费合计（含售料）：¥ ([\d.]+)/)?.[1]
 assert.equal(Number(totalText()),total/100)
 assert.match(ui.text(),/隐藏只读工艺项（1）/)
 await ui.submit();assert.equal(ui.error.value,'');const visiblePayload=ui.posts().at(-1).init.body;ui.dialog.value='order'
 await ui.button('隐藏只读工艺项（1）').props.onClick();await settle()
 assert.equal(JSON.stringify(ui.draft.lines),before);assert.equal(Number(totalText()),total/100)
 assert.doesNotMatch(ui.text(),/主卫/)
 await ui.submit();assert.equal(ui.error.value,'')
 const payload=JSON.parse(ui.posts().at(-1).init.body)
 assert.equal(payload.lines.length,initial.draft.lines.length-1);assert.equal(payload.lines.some(item=>item.id==='saved-line'),false);assert.equal(payload.lines.reduce((sum,item)=>sum+lineAmount(item),0)+payload.sales.reduce((sum,item)=>sum+saleAmountCents(item.input_qty||Number(item.sale_qty_micro||item.qty_micro)/1e6,item.price_cents),0),total-lineAmount(initial.draft.lines[0]))
 assert.equal(ui.posts().at(-1).init.body,visiblePayload,'隐藏前后只提交同一份可编辑行，保留后端受保护原行合同')
 await ui.button('显示只读工艺项（1）').props.onClick();await settle();assert.match(ui.text(),/主卫/);assert.equal(JSON.stringify(ui.draft.lines),before);assert.equal(Number(totalText()),total/100)
 // The backend preserves stored protected rows; this UI test verifies unchanged editable requests and draft totals.
})

test('正常表单保存失败保留所有输入并允许用同一幂等编号重试',async(t)=>{
 let attempts=0
 const ui=mount(t,{fetch:(path,init)=>path==='/api/order'&&init.method==='POST'?response(++attempts===1?{error:'合成版本冲突'}:{id:'saved',version:1},attempts===1?409:200):null});await settle()
 const snapshot=JSON.stringify(ui.draft);await ui.submit();assert.match(ui.error.value,/合成版本冲突/);assert.equal(ui.dialog.value,'order');assert.equal(JSON.stringify(ui.draft),snapshot)
 await ui.submit();assert.equal(ui.error.value,'');assert.equal(JSON.stringify(ui.draft),snapshot)
 const [first,second]=ui.posts();assert.equal(first.init.body,second.init.body);assert.equal(first.init.headers['Idempotency-Key'],second.init.headers['Idempotency-Key'])
})

test('正常表单提交中禁止重复保存，失败后恢复保存控件并保留输入',async(t)=>{
 const pending=deferred(),ui=mount(t,{fetch:(path,init)=>path==='/api/order'&&init.method==='POST'?pending.promise:null});await settle()
 await ui.submit();assert.equal(ui.busy.value,true);assert.equal(ui.posts().length,1);assert.equal(ui.button('保存中…').props.disabled,true)
 await ui.submit();assert.equal(ui.posts().length,1)
 pending.resolve(response({error:'合成保存失败'},422));await settle();assert.equal(ui.busy.value,false);assert.equal(ui.button('保存本次施工 / 送货').props.disabled,false);assert.match(ui.error.value,/合成保存失败/);assert.equal(ui.draft.address,'合成地址1栋101')
})

test('无填写权固定工艺保留原内容，关联材料与操作保持只读',async(t)=>{
 const initial=fixture();initial.draft.id='saved';initial.draft.lines[0].id='saved-line';initial.draft.lines[0].editable=false
 const ui=mount(t,initial);await settle();const original=JSON.stringify(ui.draft.lines[0])
 assert.match(ui.text(),/包含只读工艺.*材料及带货工资保持只读/)
 assert.equal(rowElements(locationRows(ui)[0]).some(node=>node.type==='select'),true,'原计量施工面仍保留只读控件')
 assert.equal(rowControl(ui,locationRows(ui)[0],'本项施工人'),undefined,'无填写权人员只显示快照，不暴露选择能力')
 assert.equal(ui.button('＋ 添加销售材料').props.disabled,true);assert.equal(ui.controls('施工使用商品')[0].props.disabled,true)
 assert.equal(lineCanEdit(ui.draft.lines[0],'company',ui.references.crafts,ui.references.workers[0],true),false)
 assert.equal(JSON.stringify(ui.draft.lines[0]),original)
})

test('已交票施工行按行保护，同单未交票行、公共耗材和未交票售料仍可更正',async(t)=>{
 const initial=compactFixture();initial.draft.id='mixed-ticket-order';initial.draft.project_id='project';initial.draft.worker_id='a'
 initial.draft.lines=[line('area','平方',{id:'claimed-line',ticket_claimed:true,editable:false,subitem_id:'bath',subitem_name:'主卫',segments:[segment('平方',{length:2,width:3})]}),line('area','平方',{id:'pending-line',ticket_claimed:false,subitem_id:'kitchen',subitem_name:'厨房',segments:[segment('平方',{length:1,width:2})]})]
 const material=line_id=>({product_id:'product',warehouse_id:'warehouse',line_id,input_unit:'base',input_qty:'1',qty_micro:1000000})
 initial.draft.materials=[material('claimed-line'),material('pending-line'),material('')]
 initial.draft.sales=[{id:'pending-sale',editable:true,product_id:'product',product_name:'合成商品',warehouse_id:'warehouse',input_qty:'3',input_unit:'base',qty_micro:3000000,price_cents:1500}]
 initial.draft.saved_snapshot=structuredClone(initial.draft)
 const ui=mount(t,initial);ui.app._instance.subTree.component.setupState.projectLoaded({project:{id:'project',ticket_mode:'split'},tickets:[]});await settle()
 const [claimed,pending]=ui.draft.lines,claimedBefore=JSON.stringify(claimed),snapshot=JSON.stringify(ui.draft.saved_snapshot),rows=locationRows(ui),claimedRow=rows.find(row=>row.props['data-line-key']===claimed.client_key),pendingRow=rows.find(row=>row.props['data-line-key']===pending.client_key)
 assert.match(ui.textOf(claimedRow),/本行已交票，先作废所属批次再更正/);assert.doesNotMatch(ui.textOf(claimedRow),/无此工艺填写权限/)
 assert.equal(ancestor(rowControl(ui,claimedRow,'长（米）'),node=>node.type==='fieldset').props.disabled,true);assert.equal(ancestor(rowControl(ui,pendingRow,'长（米）'),node=>node.type==='fieldset').props.disabled,false)
 for(const label of ['施工地址','项目经理','联系电话'])assert.equal(ui.controls(label)[0].props.readonly,true)
 const commonNote=ui.elements().find(node=>node.type==='label'&&ui.textOf(node)==='备注'&&node.parent.parent.type==='details')
 assert.equal(rowElements(commonNote).find(node=>node.type==='input').props.readonly,true)
 const slot=ui.elements().find(node=>node.type==='select'&&node.parent.type==='label'&&ui.textOf(node.parent).startsWith('临时项 / 售料归入'))
 assert.equal(slot.props.disabled,true);assert.equal(ui.button('取消本次记录'),undefined)
 const materialField=id=>ui.elements().find(node=>node.props?.['data-material-line-id']===id)
 const quantityIn=id=>rowElements(materialField(id)).find(node=>node.element==='input')
 assert.equal(quantityIn('claimed-line').props.disabled,true);assert.equal(quantityIn('pending-line').props.disabled,false);assert.equal(quantityIn('').props.disabled,false)
 assert.equal(ui.button('＋ 添加销售材料').props.disabled,false);assert.equal(ui.controls('销售数量')[0].props.disabled,false)
 await ui.setValue(rowControl(ui,pendingRow,'长（米）'),'4');await ui.setValue(quantityIn('pending-line'),'2');await ui.setValue(quantityIn(''),'3');await ui.setValue(ui.controls('销售数量')[0],'4')
 assert.equal(JSON.stringify(claimed),claimedBefore);assert.equal(JSON.stringify(ui.draft.saved_snapshot),snapshot)
 await ui.submit();assert.equal(ui.error.value,'');const sent=JSON.parse(ui.posts().at(-1).init.body)
 assert.deepEqual(sent.lines.map(item=>item.id),['pending-line']);assert.equal(sent.lines[0].segments[0].length,4)
 assert.deepEqual(sent.materials.map(item=>[item.line_id,item.qty_micro]),[['claimed-line',1000000],['pending-line',2000000],['',3000000]])
 assert.equal(sent.sales[0].qty_micro,4000000);assert.equal(sent.contact,initial.draft.contact);assert.equal(sent.phone,initial.draft.phone);assert.equal(sent.note,initial.draft.note)
 assert.equal(JSON.stringify(claimed),claimedBefore);assert.equal(JSON.stringify(ui.draft.saved_snapshot),snapshot)
})

test('已交票售料只锁该销售行和共同资料，不锁同单未交票施工与耗材',async(t)=>{
 const initial=compactFixture();initial.draft.id='claimed-sale-order';initial.draft.worker_id='a'
 initial.draft.lines=[line('area','平方',{id:'pending-line',segments:[segment('平方',{length:1,width:2})]})]
 const sale=(id,editable)=>({id,editable,product_id:'product',product_name:'合成商品',warehouse_id:'warehouse',input_qty:'3',input_unit:'base',qty_micro:3000000,price_cents:1500})
 initial.draft.sales=[sale('claimed-sale',false),sale('pending-sale',true)]
 const ui=mount(t,initial);await settle()
 assert.equal(ui.controls('项目经理')[0].props.readonly,true);assert.equal(ui.controls('联系电话')[0].props.readonly,true)
 assert.equal(ui.controls('销售数量')[0].props.disabled,true);assert.equal(ui.controls('销售数量')[1].props.disabled,false)
 assert.equal(ancestor(rowControl(ui,locationRows(ui)[0],'长（米）'),node=>node.type==='fieldset').props.disabled,false);assert.equal(ui.button('＋ 添加耗材').props.disabled,false);assert.equal(ui.button('＋ 添加销售材料').props.disabled,false)
 assert.equal(ui.button('取消本次记录'),undefined);assert.match(ui.text(),/其他未交票明细、耗材和售料仍可更正/)
 await ui.setValue(rowControl(ui,locationRows(ui)[0],'长（米）'),'5');await ui.setValue(ui.controls('销售数量')[1],'4')
 assert.equal(ui.draft.sales[0].input_qty,'3');assert.equal(ui.draft.lines[0].segments[0].length,'5');assert.equal(ui.draft.sales[1].input_qty,'4')
})


const locationRows=ui=>ui.elements().filter(node=>node.props?.['data-line-key'])
const rowElements=node=>[node,...node.children.flatMap(rowElements)]
const rowButton=(ui,row,action)=>rowElements(row).find(node=>node.type==='button'&&node.props['data-action']===action)
const rowControl=(ui,row,label)=>{const owner=rowElements(row).find(node=>node.type==='label'&&ui.textOf(node).trim()===label);return owner&&rowElements(owner.props.for?owner.parent:owner).find(node=>['input','select'].includes(node.element))}
function locationFixture({configured=false}={}){
 const initial=fixture()
 initial.draft.status='进行中'
 initial.draft.category_workers={'防水涂料':'a'}
 initial.draft.lines[0].subitem_id=configured?'bath':'';initial.draft.lines[0].subitem_name=configured?'浴室配置小项':'';initial.draft.lines[0].actual_location='一层主卫'
 initial.references.crafts[0].subitems=configured?[{id:'bath',name:'浴室配置小项'},{id:'kitchen',name:'厨房配置小项'}]:[]
 return initial
}

test('实际Vue无配置小项增删部位贯通OrderEditor与useOrders，DOM key独立且移除不串输入',async(t)=>{
 const ui=mount(t,locationFixture());await settle()
 const original=ui.draft.lines[0],originalRow=locationRows(ui).find(row=>row.props['data-line-key']===original.client_key),before=JSON.stringify(original),total=ui.draft.lines.reduce((sum,line)=>sum+lineAmount(line),0)
 const add=rowButton(ui,originalRow,'add-location');assert.equal(ui.textOf(add),'＋ 新增施工部位');assert.equal(add.props.disabled,false)
 await add.props.onClick();await settle()
 const added=ui.draft.lines.at(-1),sameItem=()=>locationRows(ui).filter(row=>row.props['data-craft-id']==='area'&&row.props['data-subitem-id']==='' )
 assert.equal(ui.events.filter(event=>event==='addLocation').length,1);assert.equal(sameItem().length,2);assert.equal(new Set(sameItem().map(row=>row.props['data-line-key'])).size,2)
 assert.equal(locationRows(ui).find(row=>row.props['data-line-key']===original.client_key),originalRow)
 assert.equal(JSON.stringify(original),before);assert.equal(added.id,'');assert.equal(added.region,'');assert.equal(added.actual_location,'');assert.equal(added.description,'');assert.equal(added.subitem_name,'');assert.equal(added.worker_id,'')
 assert.equal(ui.draft.lines.reduce((sum,line)=>sum+lineAmount(line),0),total,'新增空部位不复制旧工程量或金额')
 const addedRow=sameItem().find(row=>row.props['data-line-key']===added.client_key),location=rowControl(ui,addedRow,'实际施工部位')
 assert.equal(location.props.name,'actual_location');assert.equal(location.props['data-field'],'actual-location');assert.equal(location.props.placeholder,'填写实际施工部位（选填）');assert.equal(location.props.disabled,false)
 await ui.setValue(location,'二层主卫');await ui.setValue(rowControl(ui,addedRow,'长（米）'),'1.5');await ui.setValue(rowControl(ui,addedRow,'宽（米）'),'2');await ui.setValue(rowControl(ui,addedRow,'备注'),'第二部位备注')
 const addSegment=rowElements(addedRow).find(node=>node.type==='button'&&ui.textOf(node)==='＋ 一段尺寸');await addSegment.props.onClick();await settle();assert.equal(added.segments.length,2)
 assert.equal(JSON.stringify(original),before);assert.equal(added.actual_location,'二层主卫');assert.equal(added.description,'第二部位备注')
 const remove=rowButton(ui,originalRow,'remove-location');assert.equal(ui.textOf(remove),'移除此部位');assert.equal(remove.props.disabled,false)
 await remove.props.onClick();await settle()
 assert.equal(ui.events.filter(event=>event==='removeLocation').length,1);assert.equal(sameItem().length,1);assert.equal(sameItem()[0],addedRow);assert.equal(rowButton(ui,addedRow,'remove-location'),undefined)
 assert.equal(rowControl(ui,addedRow,'实际施工部位').value,'二层主卫');assert.equal(added.segments[0].length,'1.5')
 await ui.submit();assert.equal(ui.error.value,'')
 const saved=JSON.parse(ui.posts().at(-1).init.body).lines.filter(line=>line.craft_id==='area'&&!line.subitem_id)
 assert.equal(saved.length,1);assert.equal(saved[0].actual_location,'二层主卫');assert.equal(saved[0].subitem_name,'');assert.equal(saved[0].region,'');assert.equal(saved[0].worker_id,'a');assert.equal(saved[0].description,'第二部位备注')
})

test('实际Vue旧region清楚标为兼容原文，账号只读部位输入禁用且增删事件不能改受保护历史行',async(t)=>{
 const initial=locationFixture({configured:true}),first=initial.draft.lines[0]
 Object.assign(first,{id:'saved-old',editable:false,subitem_name:'',actual_location:'旧实际部位',region:'  原始主卫文本  '})
 initial.draft.lines.push({...JSON.parse(JSON.stringify(first)),id:'saved-old-second',region:'旧厨房文本'})
 const ui=mount(t,initial);await settle();const before=JSON.stringify(ui.draft.lines),rows=locationRows(ui).filter(row=>row.props['data-subitem-id']==='bath')
 assert.match(ui.text(),/配置部位名称未记录/);assert.match(ui.text(),/历史兼容文本（原 region）/);assert.match(ui.text(),/保留旧原文，不代填配置部位或实际施工部位/)
 assert.equal(rows.length,2)
 for(const row of rows){
  const input=rowControl(ui,row,'原实际施工部位'),add=rowButton(ui,row,'add-location'),remove=rowButton(ui,row,'remove-location')
  assert.equal(input.props.disabled,true);assert.equal(input.value,'旧实际部位');assert.equal(add.props.disabled,true);assert.equal(remove.props.disabled,true)
  await remove.props.onClick()
 }
 await settle();assert.equal(JSON.stringify(ui.draft.lines),before);assert.equal(ui.events.includes('addLocation'),false);assert.equal(ui.events.includes('removeLocation'),false);assert.equal(ui.draft.lines[0].region,'  原始主卫文本  ')
})

test('实际Vue保存中独立部位输入和增删按钮锁定，失败后恢复并保留每行内容',async(t)=>{
 const pending=deferred(),ui=mount(t,{...locationFixture(),fetch:(path,init)=>path==='/api/order'&&init.method==='POST'?pending.promise:null});await settle()
 await ui.button('＋ 新增施工部位').props.onClick();await settle()
 const added=ui.draft.lines.at(-1),row=locationRows(ui).find(item=>item.props['data-line-key']===added.client_key)
 await ui.setValue(rowControl(ui,row,'实际施工部位'),'待保存第二部位');await ui.setValue(rowControl(ui,row,'长（米）'),'2');await ui.setValue(rowControl(ui,row,'宽（米）'),'3')
 const before=JSON.stringify(ui.draft),eventCount=ui.events.length
 await ui.submit();assert.equal(ui.busy.value,true);assert.equal(ui.posts().length,1)
 for(const current of locationRows(ui)){
  const input=rowControl(ui,current,'实际施工部位')||rowControl(ui,current,'原实际施工部位')
  if(input)assert.equal(input.props.disabled,true)
  const add=rowButton(ui,current,'add-location'),remove=rowButton(ui,current,'remove-location')
  if(add){assert.equal(add.props.disabled,true);await add.props.onClick()}
  if(remove){assert.equal(remove.props.disabled,true);await remove.props.onClick()}
 }
 await ui.submit();assert.equal(ui.posts().length,1);assert.equal(ui.events.length,eventCount+1);assert.equal(JSON.stringify(ui.draft),before)
 pending.resolve(response({error:'合成保存失败'},422));await settle()
 assert.equal(ui.busy.value,false);assert.match(ui.error.value,/合成保存失败/);assert.equal(JSON.stringify(ui.draft),before);assert.equal(rowControl(ui,row,'实际施工部位').props.disabled,false);assert.equal(rowButton(ui,row,'add-location').props.disabled,false);assert.equal(rowControl(ui,row,'实际施工部位').value,'待保存第二部位')
})

test('实际Vue已发工资记录部位控件只读，强行触发禁用增删按钮也不会发事件',async(t)=>{
 const initial=locationFixture();initial.draft.id='settled';initial.draft.wage_paid=1;initial.draft.lines.push(JSON.parse(JSON.stringify(initial.draft.lines[0])))
 const ui=mount(t,{...initial,locked:true});await settle();const before=JSON.stringify(ui.draft.lines)
 for(const row of locationRows(ui)){
  const input=rowControl(ui,row,'实际施工部位')||rowControl(ui,row,'原实际施工部位')
  if(input)assert.equal(input.props.disabled,true)
  const add=rowButton(ui,row,'add-location'),remove=rowButton(ui,row,'remove-location')
  if(add){assert.equal(add.props.disabled,true);await add.props.onClick()}if(remove){assert.equal(remove.props.disabled,true);await remove.props.onClick()}
 }
 await settle();assert.equal(JSON.stringify(ui.draft.lines),before);assert.equal(ui.events.includes('addLocation'),false);assert.equal(ui.events.includes('removeLocation'),false)
})

test('实际Vue部位输入按Unicode显示200字校验而非UTF16 maxlength，超限拒绝保存且修改后可重试',async(t)=>{
 const ui=mount(t,locationFixture());await settle();const input=ui.controls('实际施工部位')[0]
 assert.equal(input.props.maxlength,undefined)
 await ui.setValue(input,'😀'.repeat(201));assert.equal(input.props['aria-invalid'],true);assert.match(ui.text(),/实际施工部位最多填写200字/)
 await ui.submit();assert.match(ui.error.value,/实际施工部位最多填写200字/);assert.equal(ui.posts().length,0);assert.equal(Array.from(ui.draft.lines[0].actual_location).length,201)
 await ui.setValue(input,'😀'.repeat(200));assert.equal(input.props['aria-invalid'],undefined)
 await ui.submit();assert.equal(ui.error.value,'');assert.equal(Array.from(JSON.parse(ui.posts().at(-1).init.body).lines[0].actual_location).length,200)
})

const hiddenMoneyDraft=value=>{const out=JSON.parse(JSON.stringify(value));const strip=item=>{if(item&&typeof item==='object')for(const key of Object.keys(item)){if(['price_cents','total_cents','amount_cents','received_cents','outstanding_cents'].includes(key))delete item[key];else strip(item[key])}};strip(out);return out}
const noMoneyPayload=value=>{if(value&&typeof value==='object')for(const [key,item]of Object.entries(value)){assert.equal(['price_cents','total_cents','amount_cents','received_cents','outstanding_cents'].includes(key),false);noMoneyPayload(item)}}

test('实际Vue无价保留九列和部位尺寸人员备注，金额mode可选择已施工，旧售料数量可改但单位与新增禁用',async(t)=>{
 const initial=hiddenMoneyDraft(locationFixture());initial.draft.id='saved';initial.draft.lines.at(-1).id='old-temporary';initial.draft.sales[0].id='old-sale'
 const ui=mount(t,{...initial,can:permission=>permission!=='customer_prices'});await settle()
 assert.deepEqual(ui.elements().filter(node=>node.props?.role==='columnheader').map(ui.textOf),['施工人员','工艺','部位','单价','方量明细','方量小计','金额小计','备注','合计金额'])
 assert.equal(ui.elements().filter(node=>node.props?.class==='price-hidden').length,ui.draft.lines.length)
 assert.equal(ui.elements().find(node=>node.props?.class==='order-total-value'),undefined,'没有客户价格权限时不显示整单合计')
 assert.equal(ui.controls('本项施工人')[0].props.disabled,false);assert.equal(ui.controls('实际施工部位')[0].props.disabled,false)
 const row=locationRows(ui).find(node=>node.props['data-line-key']===ui.draft.lines[0].client_key)
 await ui.setValue(rowControl(ui,row,'实际施工部位'),'二层主卫');await ui.setValue(rowControl(ui,row,'长（米）'),'4');await ui.setValue(rowControl(ui,row,'备注'),'隐藏价格不影响备注')
 const check=ui.controls('已施工（按单价列整项收费）')[0];assert.ok(check);assert.equal(check.parent.parent.props.disabled,false);check.props['onUpdate:modelValue'](false);await settle();assert.equal(ui.draft.lines.find(item=>item.mode==='金额').entered,false);check.props['onUpdate:modelValue'](true);await settle()
 const salesUnit=ui.elements().find(node=>node.type==='select'&&ui.textOf(node.parent).startsWith('销售单位'));assert.ok(salesUnit);assert.equal(salesUnit.props.disabled,true);await ui.setValue(ui.controls('销售数量')[0],'5');assert.equal(ui.draft.sales[0].qty_micro,5000000)
 assert.equal(ui.button('＋ 临时添加项').props.disabled,true);assert.equal(ui.button('＋ 添加销售材料').props.disabled,true);assert.equal(ui.button('打印已保存单据').props.disabled,true)
 assert.match(ui.text(),/本次带货总工资（元）/)
 await ui.submit();assert.equal(ui.error.value,'');const body=JSON.parse(ui.posts().at(-1).init.body);noMoneyPayload(body)
 assert.equal(body.lines[0].actual_location,'二层主卫');assert.equal(body.lines[0].segments[0].length,4);assert.equal(body.lines[0].description,'隐藏价格不影响备注');assert.equal(body.sales[0].qty_micro,5000000);assert.equal(body.delivery_wage_cents,2000)
})

test('实际Vue有价格权限但API缺失金额仍占位，不把遗漏单价或整单合计伪装0',async(t)=>{
 const initial=fixture();delete initial.draft.lines[0].price_cents
 const ui=mount(t,initial);await settle()
 assert.equal(ui.elements().filter(node=>node.props?.class==='price-hidden').length,1)
 const row=locationRows(ui).find(node=>node.props['data-craft-id']==='area');assert.match(ui.textOf(row),/无查看权限/)
 assert.equal(ui.textOf(ui.elements().find(node=>node.props?.class==='order-total-value')),'无查看权限')
 assert.equal(ui.button('＋ 临时添加项').props.disabled,false);assert.equal(ui.button('＋ 添加销售材料').props.disabled,false)
 await ui.submit();assert.equal(ui.error.value,'');assert.equal(Object.hasOwn(JSON.parse(ui.posts().at(-1).init.body).lines[0],'price_cents'),false)
})

test('实际Vue撤销价格权限立即遮住仍在草稿里的单价小计总价，再授权恢复正常收费而工资不受影响',async(t)=>{
 const allowed=ref(true),ui=mount(t,{can:permission=>permission!=='customer_prices'||allowed.value});await settle()
 const before=JSON.stringify(ui.draft);assert.equal(ui.elements().filter(node=>node.props?.class==='price-hidden').length,0)
 assert.match(ui.textOf(ui.elements().find(node=>node.props?.class==='order-total-value')),/^¥ /)
 allowed.value=false;await settle()
 assert.equal(ui.elements().filter(node=>node.props?.class==='price-hidden').length,ui.draft.lines.length)
 assert.equal(ui.elements().find(node=>node.props?.class==='order-total-value'),undefined);assert.match(ui.text(),/本次带货总工资（元）/);assert.equal(JSON.stringify(ui.draft),before)
 allowed.value=true;await settle();assert.equal(ui.elements().filter(node=>node.props?.class==='price-hidden').length,0);assert.match(ui.textOf(ui.elements().find(node=>node.props?.class==='order-total-value')),/^¥ /)
})


test('阶段C查看已保存施工单使用原快照，编辑草稿的尺寸人员和收费不污染只读回显',async(t)=>{
 const initial=fixture();initial.draft.id='saved-stage-c';initial.draft.total_cents=12000
 const snapshot=structuredClone(initial.draft);snapshot.company_name='保存所属公司';snapshot.address='保存地址1栋202';snapshot.work_date='2026-10-06';snapshot.number='C-SNAPSHOT';snapshot.worker_name='保存责任人';snapshot.lines=[{...snapshot.lines[0],id:'saved-line-c',subitem_name:'配置小项一',actual_location:'保存厨房',worker_name:'保存实际施工人',qty_micro:6000000,total_cents:12000,chargeable_qty_micro:6000000,waived_qty_micro:0}];snapshot.sales=[];snapshot.materials=[]
 initial.draft.saved_snapshot=snapshot;initial.draft.company_name='尚未保存公司名';initial.draft.address='尚未保存地址';initial.draft.number='尚未保存单号';initial.draft.lines[0].actual_location='尚未保存位置';initial.draft.lines[0].worker_name='尚未保存人员';initial.draft.lines[0].segments[0].length=9;initial.draft.lines[0].price_cents=99999
 const ui=mount(t,initial);await settle();const before=JSON.stringify(ui.draft);await ui.button('查看已保存施工单').props.onClick();await settle()
 assert.deepEqual(ui.elements().filter(node=>node.props?.role==='columnheader').map(ui.textOf),['施工人员','工艺','部位','单价','方量明细','方量小计','金额小计','备注','合计金额'])
 assert.match(ui.text(),/保存所属公司 · 保存地址1栋202 · 2026-10-06 · C-SNAPSHOT/);assert.match(ui.text(),/保存实际施工人/);assert.match(ui.text(),/保存厨房/);assert.doesNotMatch(ui.text(),/尚未保存位置|尚未保存人员|尚未保存公司名|尚未保存地址|尚未保存单号/)
 assert.equal(ui.elements().filter(node=>node.props?.class==='order-total-value').length,1);assert.equal(ui.textOf(ui.elements().find(node=>node.props?.class==='order-total-value')),'¥ 120.00')
 assert.equal(ui.elements().some(node=>['input','select','textarea','form'].includes(node.type)),false);assert.equal(ui.posts().length,0);assert.equal(JSON.stringify(ui.draft),before)
 await ui.button('返回编辑本次施工').props.onClick();await settle();assert.equal(ui.controls('原实际施工部位')[0].value,'尚未保存位置');assert.equal(Number(ui.controls('长（米）')[0].value),9);assert.equal(JSON.stringify(ui.draft),before)
 await ui.submit();assert.equal(ui.error.value,'');assert.equal(Object.hasOwn(JSON.parse(ui.posts().at(-1).init.body),'saved_snapshot'),false,'纯显示快照不进入订单保存API')
})

test('阶段C重新打开先保留实际人员快照，再恢复编辑用大类默认，查看不改变人员归属',async(t)=>{
 const initial=fixture();const saved=structuredClone(initial.draft);saved.id='saved-stage-c-worker';saved.number='C-WORKER';saved.lines=saved.lines.slice(0,2).map((item,index)=>({...item,id:'c-line-'+index,subitem_name:index?'小项乙':'小项甲',actual_location:index?'主卫':'厨房',qty_micro:index?2000000:6000000,total_cents:index?4000:12000,chargeable_qty_micro:index?2000000:6000000,waived_qty_micro:0,entered:true}));saved.sales=[];saved.materials=[];saved.total_cents=16000
 const catalogSnapshot=structuredClone(initial.references);const ui=mount(t,{...initial,fetch:(path,init)=>{const url=new URL(path,'http://synthetic.test');if(url.pathname==='/api/order'&&(!init.method||init.method==='GET'))return response(saved);if(url.pathname==='/api/reference')return response({crafts:[]});if(url.pathname==='/api/reference-options'){const entity=url.searchParams.get('entity'),id=url.searchParams.get('id'),rows=catalogSnapshot[entity]||[];return response({rows:id?rows.filter(row=>row.id===id):rows,total:rows.length})}if(url.pathname==='/api/company-packages')return response({rows:[]});if(url.pathname==='/api/inventory')return response({rows:[],total:0});return null}});await settle();await ui.workflow.openOrder({id:saved.id});await settle();assert.equal(ui.error.value,'')
 const reopened=ui.workflow.orderDraft.value;assert.equal(reopened.lines[0].worker_id,'a','没有保存继承标记时，实际人员保守保留为独立选择');assert.equal(reopened.lines[0].worker_override,true);assert.equal(reopened.saved_snapshot.lines[0].worker_id,'a');assert.equal(reopened.saved_snapshot.lines[1].worker_id,'b');assert.deepEqual(reopened.saved_snapshot,saved)
 ui.props.draft=reopened;await settle();const before=JSON.stringify(reopened);await ui.button('查看已保存施工单').props.onClick();await settle();assert.match(ui.text(),/合成人员a/);assert.match(ui.text(),/合成人员b/);assert.match(ui.text(),/厨房/);assert.match(ui.text(),/主卫/);assert.equal(JSON.stringify(reopened),before);assert.equal(ui.posts().length,0)
})


function compactFixture(){
 const initial=fixture();initial.draft.materials=[];initial.draft.sales=[];initial.draft.delivery_wage_cents=0;initial.draft.worker_id='';initial.draft.worker_name=''
 initial.draft.category_workers={'防水涂料':'a'}
 return initial
}
const cell=(row,label)=>rowElements(row).find(node=>node.props?.['data-label']===label)
const ancestor=(node,predicate)=>{for(let owner=node;owner;owner=owner.parent)if(predicate(owner))return owner}

test('默认施工人只放首个可见账号可填写明细的人员列，技能只读可换人且单一隐藏工具栏不漏存数据',async(t)=>{
 const initial=compactFixture();initial.draft.id='saved-mixed-readonly'
 initial.draft.lines=[line('protected','平方',{id:'original-protected',editable:false,worker_name:'受保护人员快照',subitem_id:'old',subitem_name:'原只读部位',description:'不可丢的旧备注',segments:[segment('平方',{length:3,width:4})]}),line('area','平方',{id:'original-mismatch',worker_id:'wrong',worker_name:'技能不符原人员',worker_override:true,subitem_id:'bath',subitem_name:'主卫',segments:[segment('平方',{length:2,width:3})]}),line('area','平方',{worker_id:'',worker_name:'',subitem_id:'kitchen',subitem_name:'厨房',segments:[segment('平方',{length:1,width:2})]})]
 initial.references.crafts=[{...catalog('protected','平方'),editable:false},{...catalog('area','平方'),subitems:[{id:'bath',name:'主卫'},{id:'kitchen',name:'厨房'}]}]
 initial.references.workers.push(worker('wrong',['开工']))
 initial.draft.saved_snapshot=structuredClone(initial.draft)
 const ui=mount(t,initial);await settle();const [protectedLine,mismatch,editableLine]=ui.draft.lines,before=JSON.stringify(ui.draft),total=ui.textOf(ui.elements().find(node=>node.props?.class==='order-total-value'))
 const selector=ui.controls('防水涂料默认施工人')[0],owner=ancestor(selector,node=>node.props?.['data-line-key'])
 assert.equal(owner.props['data-line-key'],mismatch.client_key,'跳过前置账号只读行，但技能不匹配行仍能选择人员')
 assert.equal(ancestor(selector,node=>node.props?.['data-label']==='施工人员')!==undefined,true)
 assert.equal(!!ancestor(selector,node=>node.type==='header'||node.type==='fieldset'&&node.props?.class==='measure-input'),false,'人员不在禁用技能fieldset或大类头部')
 const protectedRow=locationRows(ui).find(row=>row.props['data-line-key']===protectedLine.client_key),mismatchRow=locationRows(ui).find(row=>row.props['data-line-key']===mismatch.client_key)
 assert.equal(rowElements(cell(protectedRow,'施工人员')).some(node=>node.type==='select'),false);assert.match(ui.textOf(cell(protectedRow,'施工人员')),/受保护人员快照/)
 assert.equal(rowControl(ui,mismatchRow,'本项施工人').props.disabled,false);assert.equal(rowElements(mismatchRow).find(node=>node.type==='fieldset').props.disabled,true)
 assert.equal(ui.elements().filter(node=>node.type==='button'&&/只读工艺项/.test(ui.textOf(node))).length,1)
 await ui.submit();assert.match(ui.error.value,/已保存工艺与施工人当前技能不匹配/);assert.equal(ui.posts().length,0)
 await ui.button('隐藏只读工艺项（2）').props.onClick();await settle()
 assert.deepEqual(locationRows(ui).map(row=>row.props['data-line-key']),[editableLine.client_key]);assert.equal(JSON.stringify(ui.draft),before);assert.equal(ui.textOf(ui.elements().find(node=>node.props?.class==='order-total-value')),total)
 assert.equal(ancestor(ui.controls('防水涂料默认施工人')[0],node=>node.props?.['data-line-key']).props['data-line-key'],editableLine.client_key)
 await ui.submit();assert.match(ui.error.value,/已保存工艺与施工人当前技能不匹配/);assert.equal(ui.posts().length,0,'隐藏技能只读行不能静默删行后保存')
 await ui.button('显示只读工艺项（2）').props.onClick();await settle();assert.equal(JSON.stringify(ui.draft),before);assert.equal(locationRows(ui).length,3)
 await ui.select(rowControl(ui,locationRows(ui).find(row=>row.props['data-line-key']===mismatch.client_key),'本项施工人'),'b')
 const repaired=JSON.stringify(ui.draft),oldSnapshot=JSON.stringify(ui.draft.saved_snapshot)
 await ui.submit();assert.equal(ui.error.value,'');const shownPayload=ui.posts().at(-1).init.body;ui.dialog.value='order'
 await ui.button('隐藏只读工艺项（1）').props.onClick();await settle();assert.equal(JSON.stringify(ui.draft),repaired)
 await ui.submit();assert.equal(ui.error.value,'');assert.equal(ui.posts().at(-1).init.body,shownPayload)
 const sent=JSON.parse(shownPayload);assert.equal(sent.lines.some(item=>item.id==='original-protected'),false,'受保护旧行留给原后端保留合同，不原样回传绕过权限');assert.equal(sent.lines.find(item=>item.id==='original-mismatch').worker_id,'b');assert.equal(sent.lines.length,2);assert.equal(JSON.stringify(ui.draft.saved_snapshot),oldSnapshot)
})

test('整个大类没有账号可填写行时只显示保存的实际人员，不出现默认或本项人员选择能力',async(t)=>{
 const initial=compactFixture();initial.draft.id='only-readonly';initial.draft.lines=[line('area','平方',{id:'readonly-line',editable:false,worker_id:'a',worker_name:'历史完整人员姓名',subitem_id:'bath',subitem_name:'旧主卫',actual_location:'旧主卫',description:'历史备注',segments:[segment('平方',{length:2,width:3})]})]
 initial.references.workers[0].name='资料已经改名'
 const ui=mount(t,initial);await settle();const row=locationRows(ui)[0],before=JSON.stringify(ui.draft)
 assert.equal(ui.controls('防水涂料默认施工人').length,0);assert.equal(ui.controls('本项施工人').length,0);assert.equal(rowElements(cell(row,'施工人员')).some(node=>node.type==='select'||node.type==='button'),false)
 assert.match(ui.textOf(cell(row,'施工人员')),/历史完整人员姓名/);assert.doesNotMatch(ui.textOf(cell(row,'施工人员')),/资料已经改名/)
 assert.equal(rowButton(ui,row,'edit-legacy-location'),undefined,'只读同名原部位不能展开编辑能力')
 await ui.button('隐藏只读工艺项（1）').props.onClick();await settle();assert.equal(locationRows(ui).length,0);assert.equal(JSON.stringify(ui.draft),before)
 await ui.button('显示只读工艺项（1）').props.onClick();await settle();assert.equal(locationRows(ui)[0].props['data-line-key'],row.props['data-line-key']);assert.equal(JSON.stringify(ui.draft),before)
})

test('首行明确选择与大类默认相同的人仍独立，换默认只影响继承行且可明确返回继承',async(t)=>{
 const initial=compactFixture();initial.draft.lines=[line('area','平方',{worker_id:'',worker_name:'',subitem_id:'bath',subitem_name:'主卫',description:'首行尺寸备注',segments:[segment('平方',{length:2,width:3})]}),line('area','平方',{worker_id:'',worker_name:'',subitem_id:'kitchen',subitem_name:'厨房',segments:[segment('平方',{length:1,width:2})]}),line('area','平方',{worker_id:'b',worker_name:'原姓名B',worker_override:true,subitem_id:'other',subitem_name:'其他',segments:[segment('平方',{length:2,width:2})]})]
 const ui=mount(t,initial);await settle();const [first,inherited,override]=ui.draft.lines,dimensions=JSON.stringify(ui.draft.lines.map(item=>[item.segments,item.description]))
 await ui.button('单独指定本项').props.onClick();await settle();await ui.select(rowControl(ui,locationRows(ui)[0],'本项施工人'),'a')
 assert.equal(first.worker_id,'a');assert.equal(first.worker_override,true)
 first.worker_name='姓名快照A';ui.references.workers[0].name='改名后的A';await settle()
 await ui.select(rowControl(ui,locationRows(ui)[0],'本项施工人'),'a');assert.equal(first.worker_name,'姓名快照A','同一人再次选择不能以现姓名覆盖旧快照')
 await ui.select(ui.controls('防水涂料默认施工人')[0],'b')
 assert.deepEqual([first.worker_id,inherited.worker_id,override.worker_id],['a','','b']);assert.equal(first.worker_override,true);assert.equal(first.worker_name,'姓名快照A');assert.equal(override.worker_name,'原姓名B');assert.match(ui.textOf(cell(locationRows(ui)[0],'施工人员')),/本项单独指定，不随默认人变化/)
 await ui.submit();assert.equal(ui.error.value,'');assert.deepEqual(JSON.parse(ui.posts().at(-1).init.body).lines.map(item=>[item.worker_id,item.worker_name]),[['a','姓名快照A'],['b',''],['b','原姓名B']])
 ui.dialog.value='order';await ui.select(rowControl(ui,locationRows(ui)[0],'本项施工人'),'');assert.equal(first.worker_override,false);assert.equal(first.worker_id,'')
 await ui.select(ui.controls('防水涂料默认施工人')[0],'a');assert.equal(inherited.worker_id,'');assert.equal(override.worker_id,'b');assert.equal(override.worker_name,'原姓名B');assert.equal(first.worker_id,'');assert.equal(JSON.stringify(ui.draft.lines.map(item=>[item.segments,item.description])),dimensions)
})


test('新配置小项仅在部位列显示，按公共来源ID顺序投影并可追加同部位独立施工明细',async(t)=>{
 const initial=compactFixture();initial.draft.lines=[line('area','平方',{subitem_id:'bath',subitem_name:'主卫',actual_location:'',region:'',worker_id:'',worker_name:'',segments:[segment('平方',{length:2,width:3})]}),line('area','平方',{subitem_id:'kitchen',subitem_name:'厨房',actual_location:'',region:'',worker_id:'',worker_name:'',segments:[segment('平方',{length:1,width:2})]})]
 initial.references.crafts=[{...catalog('area','平方'),subitems:[{id:'kitchen',name:'厨房'},{id:'bath',name:'主卫'}]}]
 const ui=mount(t,initial);await settle();const before=JSON.stringify(ui.draft),rows=locationRows(ui)
 assert.deepEqual(rows.map(row=>row.props['data-subitem-id']),['kitchen','bath'])
 for(const row of rows){
  const item=ui.draft.lines.find(item=>item.client_key===row.props['data-line-key'])
  assert.match(ui.textOf(cell(row,'部位')),new RegExp(item.subitem_name));assert.doesNotMatch(ui.textOf(cell(row,'工艺')),/厨房|主卫/)
  assert.equal(rowElements(cell(row,'部位')).some(node=>node.props?.['data-field']==='actual-location'),false)
  assert.equal(rowButton(ui,row,'add-location').props.disabled,false)
 }
 assert.equal(JSON.stringify(ui.draft),before,'显示排序与拒绝重复添加都不改草稿')
 await ui.submit();assert.equal(ui.error.value,'');const sent=JSON.parse(ui.posts().at(-1).init.body)
 assert.deepEqual(sent.lines.map(item=>[item.subitem_id,item.subitem_name,item.actual_location,item.region,item.worker_id]),[['bath','主卫','','','a'],['kitchen','厨房','','','a']])
 assert.deepEqual(sent.lines.map(item=>[item.segments[0].length,item.segments[0].width]),[[2,3],[1,2]])
})

test('旧同名实际部位紧凑显示但照原值保存，可明确展开清空且保留重复小项各自尺寸备注人员和region',async(t)=>{
 const initial=compactFixture();initial.draft.id='legacy-locations'
 initial.draft.lines=[line('area','平方',{id:'same-location',sort_order:7,subitem_id:'bath',subitem_name:'原主卫',actual_location:'原主卫',region:'历史混合文字一',worker_name:'历史A',description:'同名部位备注',segments:[segment('平方',{length:2,width:3}),segment('平方',{surface:'墙面',length:1,width:2})]}),line('area','平方',{id:'empty-name-location',sort_order:8,subitem_id:'bath',subitem_name:'',actual_location:'二层原主卫',region:'历史混合文字二',worker_id:'b',worker_name:'历史B',worker_override:true,description:'第二位置独立备注',segments:[segment('平方',{surface:'其他',length:4,width:2})]}),line('area','平方',{id:'removed-subitem-location',sort_order:9,subitem_id:'removed-subitem',subitem_name:'旧退出小项',actual_location:'独立原位置',region:'历史混合文字三',worker_name:'历史C',description:'旧退出小项备注',segments:[segment('平方',{length:1,width:5})]})]
 initial.references.crafts=[{...catalog('area','平方'),subitems:[{id:'bath',name:'目录已改名'}]}]
 initial.draft.saved_snapshot=structuredClone(initial.draft)
 const ui=mount(t,initial);await settle();const [same,emptyName,removed]=ui.draft.lines,keys=ui.draft.lines.map(item=>item.client_key),snapshot=JSON.stringify(ui.draft.saved_snapshot)
 const rows=locationRows(ui),sameRow=rows.find(row=>row.props['data-line-key']===same.client_key),emptyRow=rows.find(row=>row.props['data-line-key']===emptyName.client_key)
 assert.equal(rowControl(ui,sameRow,'原实际施工部位'),undefined);assert.match(ui.textOf(cell(sameRow,'部位')),/原实际施工部位同名，已保留/)
 assert.equal(rowControl(ui,emptyRow,'原实际施工部位').value,'二层原主卫');assert.match(ui.textOf(cell(emptyRow,'部位')),/配置部位名称未记录/)
 assert.doesNotMatch(rows.map(row=>ui.textOf(cell(row,'部位'))).join('|'),/目录已改名/);assert.equal(rows.length,3);assert.equal(new Set(rows.map(row=>row.props['data-line-key'])).size,3)
 assert.ok(rows.every(row=>rowButton(ui,row,'add-location')?.props.disabled===false))
 await ui.submit();assert.equal(ui.error.value,'');const firstPost=JSON.parse(ui.posts().at(-1).init.body)
 assert.deepEqual(firstPost.lines.map(item=>[item.id,item.subitem_name,item.actual_location,item.region,item.description,item.worker_id,item.worker_name,item.sort_order]),initial.draft.lines.map(item=>[item.id,item.subitem_name,item.actual_location,item.region,item.description,item.worker_id,item.worker_name,item.sort_order]))
 assert.deepEqual(firstPost.lines.map(item=>item.segments.map(part=>[part.surface,Number(part.length),Number(part.width)])),[[['地面',2,3],['墙面',1,2]],[['其他',4,2]],[['地面',1,5]]])
 await rowButton(ui,sameRow,'edit-legacy-location').props.onClick();await settle()
 const expanded=rowControl(ui,sameRow,'原实际施工部位');assert.equal(expanded.value,'原主卫');assert.equal(expanded.props.disabled,false)
 await ui.setValue(expanded,'');assert.ok(rowControl(ui,sameRow,'原实际施工部位'),'清空旧值后输入仍保留到保存')
 await ui.submit();assert.equal(ui.error.value,'');const cleared=JSON.parse(ui.posts().at(-1).init.body)
 assert.equal(cleared.lines[0].actual_location,'');assert.equal(cleared.lines[0].subitem_name,'原主卫');assert.equal(cleared.lines[0].region,'历史混合文字一')
 assert.deepEqual(cleared.lines.slice(1),firstPost.lines.slice(1));assert.equal(removed.actual_location,'独立原位置');assert.equal(emptyName.subitem_name,'');assert.deepEqual(ui.draft.lines.map(item=>item.client_key),keys);assert.equal(JSON.stringify(ui.draft.saved_snapshot),snapshot)
})

test('重开编辑按当前公司具体工艺rank与公共小项ID稳定投影，未知和临时追加且草稿顺序sort_order及快照不变',async(t)=>{
 const initial=compactFixture()
 initial.references.crafts=[{...catalog('first','平方'),name:'当前工艺一',subitems:[{id:'a',name:'当前小项甲'},{id:'b',name:'当前小项乙'}]},{...catalog('later','平方'),name:'当前工艺二'},catalog('new-empty','平方')]
 const saved=structuredClone(initial.draft);saved.id='saved-craft-rank';saved.version=4;saved.number='RANK-SNAPSHOT';delete saved.category_workers
 const stored=(craft,id,values={})=>line(craft,'平方',{id,name:'保存工艺'+craft,spec:'保存规格',description:'保存备注'+id,actual_location:'保存实际位置'+id,subitem_name:'',segments:[segment('平方',{length:2,width:3})],...values})
 saved.lines=[stored('later','later-row'),stored('','temporary-one',{temporary:true,name:'保存临时一'}),stored('removed-craft','removed-row'),stored('first','first-retired',{subitem_id:'retired',subitem_name:'保存退出小项'}),stored('','temporary-two',{temporary:true,name:'保存临时二'}),stored('first','first-b-one',{subitem_id:'b',subitem_name:'保存小项乙一'}),stored('first','first-a',{subitem_id:'a',subitem_name:'保存小项甲'}),stored('first','first-b-two',{subitem_id:'b',subitem_name:'保存小项乙二',worker_id:'b',worker_name:'原人员B'})].map((item,index)=>({...item,sort_order:40+index}))
 const currentRefs=structuredClone(initial.references)
 const ui=mount(t,{...initial,fetch:(path,init)=>{const url=new URL(path,'http://synthetic.test');if(url.pathname==='/api/order'&&init.method==='GET')return response(saved);if(url.pathname==='/api/reference')return response({crafts:currentRefs.crafts});if(url.pathname==='/api/reference-options'){const rows=currentRefs[url.searchParams.get('entity')]||[],id=url.searchParams.get('id');return response({rows:id?rows.filter(row=>row.id===id):rows,total:rows.length})}if(url.pathname==='/api/company-packages')return response({rows:[]});if(url.pathname==='/api/inventory')return response({rows:[],total:0});return null}})
 await settle();await ui.workflow.openOrder({id:saved.id});assert.equal(ui.error.value,'');const reopened=ui.workflow.orderDraft.value;ui.props.draft=reopened;await settle()
 const before=JSON.stringify(reopened),snapshot=JSON.stringify(reopened.saved_snapshot),displayIDs=()=>locationRows(ui).map(row=>reopened.lines.find(item=>item.client_key===row.props['data-line-key']).id)
 assert.deepEqual(reopened.lines.slice(0,8).map(item=>[item.id,item.sort_order]),saved.lines.map(item=>[item.id,item.sort_order]))
 assert.deepEqual(displayIDs(),['first-a','first-b-one','first-b-two','first-retired','later-row','','temporary-one','removed-row','temporary-two'])
 assert.doesNotMatch(locationRows(ui).map(row=>ui.textOf(cell(row,'部位'))).join('|'),/当前小项甲|当前小项乙/)
 assert.equal(ancestor(ui.controls('防水涂料默认施工人')[0],node=>node.props?.['data-line-key']).props['data-line-key'],reopened.lines.find(item=>item.id==='first-a').client_key)
 ui.references.crafts=[ui.references.crafts[1],ui.references.crafts[0],ui.references.crafts[2]];await settle()
 assert.deepEqual(displayIDs(),['later-row','first-a','first-b-one','first-b-two','first-retired','','temporary-one','removed-row','temporary-two'])
 assert.equal(JSON.stringify(reopened),before);assert.equal(JSON.stringify(reopened.saved_snapshot),snapshot);assert.deepEqual(reopened.saved_snapshot,saved)
 await ui.submit();assert.equal(ui.error.value,'');const sent=JSON.parse(ui.posts().at(-1).init.body)
 const savedFields=item=>[item.id,item.craft_id,item.name,item.subitem_id||'',item.subitem_name,item.actual_location,item.region,item.description,item.worker_id,item.worker_name,item.sort_order]
 assert.deepEqual(sent.lines.map(savedFields),saved.lines.map(savedFields));assert.equal(JSON.stringify(reopened),before);assert.equal(JSON.stringify(reopened.saved_snapshot),snapshot)
})

test('同一配置部位重复明细分别登记耗材，公共耗材独立且未填写耗材所属行不能保存',async(t)=>{
 const initial=compactFixture();initial.draft.lines=[line('area','平方',{subitem_id:'bath',subitem_name:'主卫',segments:[segment('平方',{length:2,width:3})]})]
 const ui=mount(t,initial);await settle();const first=ui.draft.lines[0],firstRow=locationRows(ui)[0]
 await rowButton(ui,firstRow,'add-location').props.onClick();await settle();const second=ui.draft.lines.at(-1),secondRow=locationRows(ui).find(row=>row.props['data-line-key']===second.client_key)
 assert.equal(second.subitem_id,'bath');assert.notEqual(first.client_key,second.client_key)
 const addMaterialIn=row=>rowElements(row).find(node=>node.type==='button'&&ui.textOf(node)==='＋ 本项耗材')
 await addMaterialIn(firstRow).props.onClick();await addMaterialIn(secondRow).props.onClick();await ui.button('＋ 添加耗材').props.onClick();await settle()
 assert.deepEqual(ui.draft.materials.map(item=>item.line_id),[first.client_key,second.client_key,''])
 for(const material of ui.draft.materials)Object.assign(material,{product_id:'product',warehouse_id:'warehouse',input_unit:'base',input_qty:'1',qty_micro:1000000})
 ui.draft.worker_id='a';await ui.submit();assert.match(ui.error.value,/耗材必须关联已填写的施工明细/);assert.equal(ui.posts().length,0)
 Object.assign(second.segments[0],{length:1,width:2});await settle()
 const firstRemove=rowButton(ui,firstRow,'remove-location');assert.equal(firstRemove.props.disabled,true);assert.equal(ui.workflow.removeLocation(first),false)
 await ui.submit();assert.equal(ui.error.value,'');const sent=JSON.parse(ui.posts().at(-1).init.body)
 assert.deepEqual(sent.materials.map(item=>item.line_id),[sent.lines[0].client_ref,sent.lines[1].client_ref,'']);assert.equal(sent.lines.length,2)
 assert.equal(ui.elements().filter(node=>node.props?.class==='order-total-value').length,1)
})

test('免费施工客户金额为零但实际工资可调整，原因必填且无价格工资权时不显示或提交敏感字段',async(t)=>{
 const initial=compactFixture();initial.draft.lines=[line('area','平方',{charge_mode:'free',wage_override_cents:2500,segments:[segment('平方',{length:2,width:3})]})]
 const ui=mount(t,initial);await settle();assert.equal(lineAmount(ui.draft.lines[0]),0);assert.equal(ui.textOf(ui.elements().find(node=>node.props?.class==='order-total-value')),'¥ 0.00')
 await ui.submit();assert.match(ui.error.value,/填写工资调整原因/);assert.equal(ui.posts().length,0)
 ui.draft.lines[0].wage_adjustment_reason='合成补做加班';await ui.submit();assert.equal(ui.error.value,'');const sent=JSON.parse(ui.posts().at(-1).init.body)
 assert.equal(sent.lines[0].charge_mode,'free');assert.equal(sent.lines[0].wage_override_cents,2500);assert.equal(sent.lines[0].wage_adjustment_reason,'合成补做加班');assert.equal(sent.lines[0].segments[0].length,2)
 const restricted=mount(t,{...initial,can:permission=>!['wages','customer_prices'].includes(permission)});await settle()
 assert.equal(restricted.controls('收费方式').length,0);assert.equal(restricted.controls('本项实际工资（元）').length,0);assert.equal(restricted.controls('工资调整原因').length,0);assert.equal(restricted.elements().some(node=>node.props?.class==='order-total-value'),false)
 await restricted.submit();assert.equal(restricted.error.value,'');const hidden=JSON.parse(restricted.posts().at(-1).init.body).lines[0]
 for(const key of ['price_cents','charge_mode','wage_cents','wage_override_cents','wage_adjustment_reason'])assert.equal(Object.hasOwn(hidden,key),false)
})

test('纯商品销售没有实际施工行也能保存且保持公司工地和实际来源',async(t)=>{
 const initial=fixture();initial.draft.lines=initial.draft.lines.map(item=>({...item,entered:false,segments:[segment(item.mode)]}));initial.draft.materials=[]
 const ui=mount(t,initial);await settle();await ui.submit();assert.equal(ui.error.value,'');const sent=JSON.parse(ui.posts().at(-1).init.body)
 assert.equal(sent.lines.length,0);assert.equal(sent.sales.length,1);assert.equal(sent.sales[0].warehouse_id,'warehouse');assert.equal(sent.company_id,'company');assert.equal(sent.address,'合成地址1栋101');assert.match(ui.text(),/商品销售/)
})

test('清空已保存实际工资明确提交null恢复标准计算，并要求新的调整原因',async(t)=>{
 const initial=compactFixture();initial.draft.id='saved-wage';initial.draft.lines=[line('area','平方',{id:'saved-wage-line',wage_override_cents:2500,wage_adjustment_reason:'旧调整原因',segments:[segment('平方',{length:2,width:3})]})];initial.draft.saved_snapshot=structuredClone(initial.draft)
 const ui=mount(t,initial);await settle();await ui.setValue(ui.controls('本项实际工资（元）')[0],'');assert.equal(ui.draft.lines[0].wage_override_cents,null)
 ui.draft.lines[0].wage_adjustment_reason='';await ui.submit();assert.match(ui.error.value,/填写工资调整原因/);assert.equal(ui.posts().length,0)
 ui.draft.lines[0].wage_adjustment_reason='合成恢复标准工资';await ui.submit();assert.equal(ui.error.value,'');assert.equal(JSON.parse(ui.posts().at(-1).init.body).lines[0].wage_override_cents,null)
})

test('页面仅保留一个新增施工单入口，工地总表只打开已保存施工单',async()=>{
 const app=await readFile(new URL('./App.vue',import.meta.url),'utf8'),sites=await readFile(new URL('./components/ProjectsPanel.vue',import.meta.url),'utf8'),work=await readFile(new URL('./components/ProjectWorkPanel.vue',import.meta.url),'utf8')
 assert.equal((app.match(/>新增施工单<\/button>/g)||[]).length,1);assert.doesNotMatch(app,/@new-order=/);assert.doesNotMatch(sites,/newOrder|继续填写施工 \/ 送货/)
 assert.match(work,/emit\('openOrder',order\)/);assert.match(work,/查看 \/ 编辑此施工单/)
})
