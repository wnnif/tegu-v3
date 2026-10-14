import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {registerHooks} from 'node:module'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import {createRenderer,nextTick} from 'vue'
import ts from 'typescript'

registerHooks({resolve(specifier,context,next){if(specifier.startsWith('.')&&context.parentURL?.startsWith('file:')&&existsSync(fileURLToPath(new URL(specifier,context.parentURL))+'.ts'))return next(specifier+'.ts',context);return next(specifier,context)}})
globalThis.Document??=class Document{}
globalThis.ShadowRoot??=class ShadowRoot{}
const teleportTarget={type:'body',children:[]}
globalThis.document??={activeElement:null,addEventListener(){},removeEventListener(){},querySelector(selector){return selector==='body'?teleportTarget:null}}
globalThis.document.querySelector??=(selector)=>selector==='body'?teleportTarget:null
const vueURL=new URL('../node_modules/vue/index.mjs',import.meta.url).href
const moduleURL=source=>'data:text/javascript,'+encodeURIComponent(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
const file=new URL('./components/HistoryPanel.vue',import.meta.url)
const {descriptor}=parse(await readFile(file,'utf8')),script=compileScript(descriptor,{id:'history-panel-date-source'})
const scriptSource=script.content.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(path==='vue'?vueURL:new URL(path+'.ts',file).href))
const template=compileTemplate({id:'history-panel-date-source',source:descriptor.template.content,filename:'HistoryPanel.vue',compilerOptions:{bindingMetadata:script.bindings}})
assert.deepEqual(template.errors,[])
const renderURL=moduleURL(template.code.replaceAll('from "vue"','from '+JSON.stringify(vueURL)))
const {default:Panel}=await import(moduleURL(scriptSource.replace(/^export default/m,'const component =')+'\nimport {render} from '+JSON.stringify(renderURL)+';component.render=render;export default component'))
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}

test('finance purchase source opens effective purchase documents with the exact date range on first request',async t=>{
 const requests=[],root={type:'root',children:[]}
 const insert=(node,parent,anchor=null)=>{if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1);const index=anchor?parent.children.indexOf(anchor):-1;parent.children.splice(index<0?parent.children.length:index,0,node);node.parent=parent}
 const renderer=createRenderer({createElement:type=>({type,tagName:type.toUpperCase(),children:[],props:{},get options(){return this.children.filter(child=>child.type==='option')},addEventListener(){},removeEventListener(){},setAttribute(){},removeAttribute(){}}),createText:text=>({type:'text',text}),createComment:text=>({type:'comment',text}),setText:(node,text)=>node.text=text,setElementText:(node,text)=>{node.text=text;node.children=[]},patchProp:(node,key,old,value)=>{node.props[key]=value;node[key]=value},insert,remove:node=>node.parent?.children.splice(node.parent.children.indexOf(node),1),parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,insertStaticContent(content,parent,anchor){const node={type:'static',text:content.replace(/<[^>]+>/g,''),children:[]};insert(node,parent,anchor);return[node,node]}})
 t.mock.method(globalThis,'fetch',async(path,init)=>{requests.push({path,init});return response({rows:[],total:0,page:1,page_size:100})})
 const app=renderer.createApp({...Panel,render:()=>null},{stock:true,warehouses:[],warehouse:'',revision:1,initialFrom:'2026-09-24',initialTo:'2026-10-12',initialKind:'进货'})
 app.mount(root);t.after(()=>app.unmount());await settle()
 const request=requests.find(item=>item.path.startsWith('/api/stock-documents?'))
 assert.ok(request)
 const params=new URL('http://local'+request.path).searchParams
 assert.equal(params.get('from'),'2026-09-24')
 assert.equal(params.get('to'),'2026-10-12')
 assert.equal(params.get('kind'),'进货')
 assert.equal(params.get('state'),'active')
 assert.equal(params.get('page'),'1')
})
