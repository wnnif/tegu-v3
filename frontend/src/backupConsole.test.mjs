import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import {createRenderer,nextTick} from 'vue'
import ts from 'typescript'

const vueURL=new URL('../node_modules/vue/index.mjs',import.meta.url).href
// The actual v-model directive inspects these browser constructors; this
// renderer supplies a synthetic unfocused root and never contacts a browser.
globalThis.Document??=class Document {}
globalThis.ShadowRoot??=class ShadowRoot {}
const moduleURL=source=>'data:text/javascript,'+encodeURIComponent(ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
const apiSource=(await readFile(new URL('./api.ts',import.meta.url),'utf8')).replaceAll("'./sessionAccess'",JSON.stringify(new URL('./sessionAccess.ts',import.meta.url).href))
const apiURL=moduleURL(apiSource)
const {descriptor}=parse(await readFile(new URL('./components/BackupConsole.vue',import.meta.url),'utf8'))
const script=compileScript(descriptor,{id:'backup-task-binding'})
const rewrite=source=>source.replace(/from\s+(['"])([^'"]+)\1/g,(_,quote,path)=>'from '+JSON.stringify(path==='vue'?vueURL:apiURL))
const Component=(await import(moduleURL(rewrite(script.content)))).default
const template=compileTemplate({id:'backup-task-binding',source:descriptor.template.content,filename:'BackupConsole.vue',compilerOptions:{bindingMetadata:script.bindings}})
assert.deepEqual(template.errors,[])
Component.render=(await import(moduleURL(rewrite(template.code)))).render
const response=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done});return {promise,resolve}}
const settle=async()=>{await new Promise(resolve=>setImmediate(resolve));await nextTick()}
const backup=file=>({id:file,file,created_at:'合成日期',status:'完成',available:true,message:''})
const message='数据已恢复到独立数据库，当前服务未切换。'

function mount(t,override=()=>null){
  const requests=[],currentJobs=[],restores=[]
  const portal={type:'body',children:[]},root={type:'root',children:[]}
  function insert(node,parent,anchor=null){
    if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1)
    const index=anchor?parent.children.indexOf(anchor):-1
    parent.children.splice(index<0?parent.children.length:index,0,node);node.parent=parent
  }
  const renderer=createRenderer({
    createElement:type=>({type,tagName:type.toUpperCase(),props:{},children:[],listeners:{},value:'',
      addEventListener(name,handler){this.listeners[name]=handler},getRootNode(){return {activeElement:null}}}),
    createText:text=>({type:'text',text,children:[]}),createComment:text=>({type:'comment',text,children:[]}),
    setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text;node.children=[]},patchProp:(node,key,previous,next)=>{node.props[key]=next},
    insert,remove(node){if(node.parent)node.parent.children.splice(node.parent.children.indexOf(node),1)},parentNode:node=>node.parent,
    nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,querySelector:selector=>selector==='body'?portal:null,
    insertStaticContent(content,parent,anchor){const node={type:'static',text:content.replace(/<[^>]+>/g,''),children:[]};insert(node,parent,anchor);return [node,node]},
  })
  t.mock.method(globalThis,'fetch',async(path,init)=>{
    requests.push({path,init})
    const alternative=override(path,init,currentJobs)
    if(alternative)return alternative
    if(path.startsWith('/api/backups?'))return response({rows:[backup('backup-a.json'),backup('backup-b.json')],total:2,used_bytes:0,free_bytes:10000000000})
    if(path.startsWith('/api/maintenance/jobs?kind=backup'))return response({rows:[],total:0})
    if(path.startsWith('/api/maintenance/jobs?kind=all'))return response({rows:currentJobs,total:currentJobs.length})
    if(path.startsWith('/api/maintenance/jobs?id='))return response(currentJobs.find(job=>path.endsWith(job.id)))
    if(path==='/api/maintenance/verify')return response({counts:{orders:1}})
    if(path==='/api/maintenance/restore'){
      const payload=JSON.parse(init.body);restores.push(payload)
      const job={id:'job-'+restores.length,kind:'restore',status:'运行中',stage:'正在恢复',progress:1,total:3,message:'',created_at:'合成日期'}
      currentJobs.unshift(job);return response(job)
    }
    throw new Error('Unexpected synthetic endpoint '+path)
  })
  const app=renderer.createApp(Component,{revision:1}),state=app.mount(root).$.setupState
  t.after(()=>app.unmount())
  const nodes=node=>[node,...node.children.flatMap(nodes)]
  const elements=()=>[...nodes(root),...nodes(portal)]
  const text=node=>node.type==='comment'?'':(node.text||'')+node.children.map(text).join('')
  const dialog=()=>elements().find(node=>node.type==='section'&&node.props.class==='modal')
  const dialogText=()=>dialog()?text(dialog()):''
  const button=label=>elements().find(node=>node.type==='button'&&text(node)===label)
  const form=()=>elements().find(node=>node.type==='form')
  const fillTarget=value=>{state.target=value;state.confirm=value}
  const open=async(index)=>{const buttons=elements().filter(node=>node.type==='button'&&text(node)==='校验 / 恢复');await buttons[index].props.onClick();await settle()}
  const submit=async()=>{await form().props.onSubmit({preventDefault(){}});await settle()}
  const complete=(index=0,status='完成',target)=>{const job=currentJobs[index];job.status=status;job.result=status==='完成'?{target:target||restores.find((_,i)=>'job-'+(i+1)===job.id).target,message}:undefined;job.message=status==='完成'?'':'合成恢复失败：请准备新空库'}
  return {state,requests,currentJobs,restores,dialogText,button,form,fillTarget,open,submit,complete}
}

test('A completion never becomes B success; B renders blocking task and sends its own request after A completes',async(t)=>{
  const h=mount(t);await settle();await h.open(0)
  h.fillTarget('tegu_restore_a');await settle();await h.submit()
  assert.equal(h.restores.length,1)
  assert.match(h.dialogText(),/恢复文件：backup-a.json.*目标库：tegu_restore_a.*任务编号：job-1/)
  await h.button('×').props.onClick();await settle();await h.open(1)
  h.fillTarget('tegu_restore_b');await settle()
  assert.equal(h.button('恢复到该空库').props.disabled,true)
  assert.match(h.dialogText(),/另一恢复任务正在执行.*backup-a.json.*tegu_restore_a/)
  await h.state.restore();await settle()
  assert.equal(h.restores.length,1)
  assert.match(h.dialogText(),/已有恢复任务正在执行/)
  h.complete();await h.state.loadJobs();await settle()
  assert.equal(h.state.chosen.file,'backup-b.json')
  assert.equal(h.state.chosenTask,undefined)
  assert.doesNotMatch(h.dialogText(),/数据已恢复到独立数据库/)
  assert.ok(h.form(),'B keeps its actual restore form')
  assert.equal(h.button('恢复到该空库').props.disabled,false)
  await h.submit()
  assert.deepEqual(h.restores,[{file:'backup-a.json',target:'tegu_restore_a',confirm:'tegu_restore_a'},{file:'backup-b.json',target:'tegu_restore_b',confirm:'tegu_restore_b'}])
})

test('reopening same backup preserves running job and shows the actual server result target',async(t)=>{
  const h=mount(t);await settle();await h.open(0);h.fillTarget('tegu_restore_a');await h.submit()
  await h.button('×').props.onClick();await settle();await h.open(0)
  assert.equal(h.form(),undefined)
  assert.match(h.dialogText(),/目标库：tegu_restore_a/)
  assert.doesNotMatch(h.dialogText(),/准备新的恢复/)
  h.complete(0,'完成','tegu_restore_actual');await h.state.loadJobs();await settle()
  assert.match(h.dialogText(),/目标库：tegu_restore_actual.*任务编号：job-1.*数据已恢复/)
  await h.button('准备新的恢复').props.onClick();await settle()
  assert.ok(h.form());assert.equal(h.state.target,'');assert.equal(h.state.confirm,'')
  assert.doesNotMatch(h.dialogText(),/数据已恢复/)
  h.fillTarget('tegu_restore_new');await h.submit()
  assert.equal(h.restores.length,2);assert.equal(h.state.chosenTask.job.id,'job-2')
})

test('failed recovery stays bound to A, cannot mark B failed and explicitly prepares a new empty target',async(t)=>{
  const h=mount(t);await settle();await h.open(0);h.fillTarget('tegu_restore_a');await h.submit()
  await h.button('×').props.onClick();await settle();await h.open(1)
  h.complete(0,'失败');await h.state.loadJobs();await settle()
  assert.doesNotMatch(h.dialogText(),/合成恢复失败/)
  await h.button('×').props.onClick();await settle();await h.open(0)
  assert.match(h.dialogText(),/目标库：tegu_restore_a.*合成恢复失败/)
  assert.ok(h.button('准备新的恢复'))
  await h.button('准备新的恢复').props.onClick();await settle()
  assert.ok(h.form());assert.equal(h.state.target,'');assert.equal(h.state.confirm,'')
})

test('restore request failure preserves typed target and allows a real retry',async(t)=>{
  let fail=true,attempts=0
  const h=mount(t,path=>{if(path==='/api/maintenance/restore'){attempts++;if(fail)return response({error:'恢复请求失败'},500)}return null})
  await settle();await h.open(0);h.fillTarget('tegu_restore_a');await h.submit()
  assert.match(h.dialogText(),/恢复请求失败/);assert.equal(h.state.target,'tegu_restore_a');assert.ok(h.form())
  fail=false;await h.submit()
  assert.equal(attempts,2);assert.equal(h.restores.length,1);assert.equal(h.state.chosenTask.job.id,'job-1')
})

test('a late poll cannot replace a new same-file task or roll a completed task back to running',async(t)=>{
  let intercept=false,pending
  const h=mount(t,(path,init,jobs)=>{
    if(intercept&&path.startsWith('/api/maintenance/jobs?kind=all')){intercept=false;pending=deferred();return pending.promise}
    return null
  })
  await settle();await h.open(0);h.fillTarget('tegu_restore_a');await h.submit()
  intercept=true;const oldPoll=h.state.loadJobs()
  h.complete();await h.state.loadJobs();await settle()
  await h.button('准备新的恢复').props.onClick();await settle();h.fillTarget('tegu_restore_new');await h.submit()
  pending.resolve(response({rows:[{id:'job-1',kind:'restore',status:'运行中'}],total:1}));await oldPoll;await settle()
  assert.equal(h.state.chosenTask.job.id,'job-2')
  assert.equal(h.state.chosenTask.target,'tegu_restore_new')
  assert.match(h.dialogText(),/目标库：tegu_restore_new.*任务编号：job-2/)
})

test('an already running restore after reload disables a second restore with visible job context',async(t)=>{
  const h=mount(t)
  h.currentJobs.push({id:'external-job',kind:'restore',status:'运行中',message:'',stage:'恢复中'})
  await settle();await h.state.loadJobs();await h.open(1);h.fillTarget('tegu_restore_b');await settle()
  assert.equal(h.button('恢复到该空库').props.disabled,true)
  assert.match(h.dialogText(),/另一恢复任务正在执行（编号 external-job）/)
  assert.doesNotMatch(h.dialogText(),/目标库：tegu_restore_b/)
})
