import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {registerHooks} from 'node:module'
import {fileURLToPath} from 'node:url'
import {parse,compileScript} from '@vue/compiler-sfc'
import {createRenderer,h,nextTick,reactive} from 'vue'
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
globalThis.document??={activeElement:null,addEventListener(){},removeEventListener(){}}
const file=new URL('./components/UserEditor.vue',import.meta.url)
const {descriptor}=parse(await readFile(file,'utf8'))
const script=compileScript(descriptor,{id:'public-grants',inlineTemplate:true})
const source=ts.transpileModule(script.content,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText
 .replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(path==='vue'?new URL('../node_modules/vue/index.mjs',import.meta.url).href:new URL(path+'.ts',file).href))
const UserEditor=(await import('data:text/javascript,'+encodeURIComponent(source))).default
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done});return {promise,resolve}}
const craft=(id,active=1,category='防水涂料')=>({id,name:'公共'+id,company_id:'',version:1,active,category,mode:'平方',spec:'合成规格'})
function mount(t,read,initial={}){
 const requests=[],saves=[],props=reactive({editing:{id:'employee',version:3,name:'合成员工',username:'synthetic',password:'',role:'employee',permissions:['orders'],crafts:[],active:true,...initial},categories:['防水涂料','回填'],permissionNames:{orders:'施工制单',customer_prices:'查看客户价格',wages:'工资管理'},busy:false,canAssignCombinations:true})
 t.mock.method(globalThis,'fetch',async(path,init)=>{requests.push({path,init});assert.equal(init.method,'GET');const params=new URL(path,'http://synthetic.invalid').searchParams;assert.equal(params.get('entity'),'crafts');assert.equal(params.has('company_id'),false);assert.equal(params.has('craft_scope'),false);assert.equal(params.has('active'),false);return read(params,requests.length)})
 function insert(node,parent,anchor=null){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1);const index=anchor?parent.children.indexOf(anchor):-1;parent.children.splice(index<0?parent.children.length:index,0,node);node.parent=parent}
 const renderer=createRenderer({
  createElement:type=>({type,tagName:type.toUpperCase(),children:[],props:{},listeners:{},value:'',style:{},get options(){return this.children.filter(child=>child.type==='option')},addEventListener(name,fn){this.listeners[name]=fn},getRootNode(){return globalThis.document},setAttribute(){},removeAttribute(){}}),
  createText:text=>({type:'text',text,children:[]}),createComment:text=>({type:'comment',text,children:[]}),setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text;node.children=[]},patchProp:(node,key,previous,next)=>{node.props[key]=next;node[key]=next},insert,
  remove(node){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1)},parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,
  insertStaticContent(content,parent,anchor){const node={type:'static',text:content.replace(/<[^>]+>/g,''),children:[]};insert(node,parent,anchor);return [node,node]},
 })
 const root={children:[]},app=renderer.createApp({render:()=>h(UserEditor,{...props,onSave:()=>saves.push(JSON.parse(JSON.stringify(props.editing)))})});app.mount(root);t.after(()=>app.unmount())
 const walk=node=>[node,...node.children.flatMap(walk)],elements=()=>walk(root),text=node=>node.type==='comment'?'':(node.text||'')+node.children.map(text).join('')
 const button=label=>elements().find(node=>node.type==='button'&&text(node)===label)
 const checkbox=id=>elements().find(node=>node.tagName==='INPUT'&&node.props.type==='checkbox'&&node.value===id)
 return {app,props,requests,saves,elements,button,checkbox,text:()=>text(root),submit:()=>elements().find(node=>node.type==='form').props.onSubmit({preventDefault(){}})}
}

test('员工真实授权表单直接选择公共工艺一次，保存公共ID且不自动授予价格工资或资料维护',async t=>{
 const ui=mount(t,()=>response({rows:[craft('water')],total:1,page:1}));await settle()
 assert.match(ui.text(),/公共工艺只需授权一次/);assert.doesNotMatch(ui.text(),/选择公司|先选公司/)
 const grant=ui.checkbox('water');assert.ok(grant);assert.equal(grant.disabled,false)
 grant.checked=true;grant.listeners.change();await settle();assert.deepEqual(ui.props.editing.crafts,['water']);assert.deepEqual(ui.props.editing.permissions,['orders'])
 ui.submit();await settle();assert.equal(ui.saves.length,1);assert.deepEqual(ui.saves[0].crafts,['water']);assert.deepEqual(ui.saves[0].permissions,['orders']);assert.equal(ui.requests.length,1)
})

test('大类搜索与分页保留其他页授权，勾选本页只新增启用公共工艺，清除可撤销停用旧授权',async t=>{
 const ui=mount(t,params=>response({rows:params.get('page')==='2'?[craft('page-two')]:[craft('water'),craft('inactive',0),craft('ungranted-inactive',0)],total:51,page:Number(params.get('page'))}),{crafts:['inactive','other-page']});await settle()
 assert.equal(ui.checkbox('inactive').disabled,false);assert.equal(ui.checkbox('ungranted-inactive').disabled,true)
 ui.button('勾选本页').props.onClick();await settle();assert.deepEqual(ui.props.editing.crafts,['inactive','other-page','water'])
 await ui.button('下一页').props.onClick();await settle();assert.ok(ui.checkbox('page-two'));assert.deepEqual(ui.props.editing.crafts,['inactive','other-page','water'])
 ui.button('勾选本页').props.onClick();await settle();assert.deepEqual(ui.props.editing.crafts,['inactive','other-page','water','page-two'])
 await ui.button('上一页').props.onClick();await settle();ui.button('清除本页').props.onClick();await settle();assert.deepEqual(ui.props.editing.crafts,['other-page','page-two'])
 const category=ui.elements().find(node=>node.type==='select'&&node.options.some(option=>option.text==='回填'))
 category.props['onUpdate:modelValue']('回填');await category.props.onChange();await settle()
 const query=ui.elements().find(node=>node.props?.['aria-label']==='搜索可授权公共工艺');query.props['onUpdate:modelValue']('  防水  ');await ui.button('查询').props.onClick();await settle()
 const params=new URL(ui.requests.at(-1).path,'http://synthetic.invalid').searchParams;assert.equal(params.get('category'),'回填');assert.equal(params.get('q'),'防水');assert.equal(params.get('page'),'1');assert.deepEqual(ui.props.editing.crafts,['other-page','page-two'])
})

test('读取失败可重试并保留授权，保存中真实fieldset和按钮禁用',async t=>{
 let failed=true
 const ui=mount(t,()=>failed?response({error:'公共工艺读取失败'},500):response({rows:[craft('water')],total:1,page:1}),{crafts:['water']});await settle()
 assert.match(ui.text(),/公共工艺读取失败/);assert.deepEqual(ui.props.editing.crafts,['water']);failed=false;await ui.button('重试').props.onClick();await settle();assert.ok(ui.checkbox('water'));assert.equal(ui.checkbox('water').checked,true)
 ui.props.busy=true;await nextTick();assert.equal(ui.elements().find(node=>node.type==='fieldset').disabled,true);assert.equal(ui.button('正在保存…').disabled,true);assert.equal(ui.saves.length,0)
})

test('切换编辑账号与角色时，旧公共工艺响应不能覆盖新表单',async t=>{
 const wait=deferred()
 const ui=mount(t,(_,count)=>count===1?wait.promise:response({rows:[craft('current')],total:1,page:1}));await settle()
 ui.props.editing={...ui.props.editing,id:'employee-two',crafts:['current']};await settle();assert.ok(ui.checkbox('current'))
 wait.resolve(response({rows:[craft('stale')],total:1,page:1}));await settle();assert.ok(ui.checkbox('current'));assert.equal(ui.checkbox('stale'),undefined)
 const last=deferred();t.mock.method(globalThis,'fetch',()=>last.promise);const pending=ui.button('查询').props.onClick();
 ui.props.editing.role='operator';await settle();last.resolve(response({rows:[craft('obsolete')],total:1,page:1}));await pending;await settle();assert.equal(ui.checkbox('obsolete'),undefined);assert.doesNotMatch(ui.text(),/可填写的公共工艺/)
})
