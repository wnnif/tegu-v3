import {test} from 'node:test'
import assert from 'node:assert/strict'
import {companyCategories,matchingWorkers,craftScope,lineCanEdit} from './orderAccess.ts'

const workers=[
  {id:'start',crafts:['开工'],active:1},
  {id:'fill',crafts:['回填'],active:1},
  {id:'both',crafts:['开工','回填','维修'],active:1},
  {id:'inactive',crafts:['开工'],active:0},
  {id:'unset',crafts:[],active:1},
]
test('company selection reveals login-authorized categories before worker selection',()=>{
  assert.deepEqual(companyCategories('a',['开工','维修']),['开工','维修'])
  assert.deepEqual(companyCategories('', ['维修']),[])
  assert.deepEqual(craftScope(['开工','维修']),['开工','维修'])
})
test('worker options match selected category and login permissions; exclude inactive/unassigned workers',()=>{
  assert.deepEqual(matchingWorkers('a',workers,'开工',['开工']).map(w=>w.id),['start','both'])
  assert.deepEqual(matchingWorkers('a',workers,'回填',['回填']).map(w=>w.id),['fill','both'])
  assert.deepEqual(matchingWorkers('',workers,'开工',['开工']),[])
  assert.deepEqual(matchingWorkers('a',workers,'开工',[]),[])
  assert.deepEqual(matchingWorkers('b',workers,'防水涂料',['防水涂料']),[])
})
test('selected worker narrows scope without dropping temporary-item permissions',()=>{
  assert.deepEqual(craftScope(['开工','维修'],workers[2]),['开工','维修'])
  assert.deepEqual(craftScope(['开工','回填'],workers[0]),['开工'])
  assert.deepEqual(craftScope(['开工'],workers[4]),[])
  assert.deepEqual(matchingWorkers('no-fixed',workers,'回填',['回填']).map(w=>w.id),['fill','both'])
})
test('worker without an active field keeps assigned crafts after selection',()=>{
  const worker={id:'legacy-worker',crafts:['开工','防水涂料']}
  assert.deepEqual(craftScope(['开工','防水涂料','维修'],worker),['开工','防水涂料'])
  assert.deepEqual(craftScope(['开工'],{...worker,active:0}),[])
})
test('工艺具体授权受公司ID、服务端只读和施工人技能共同约束',()=>{
  const crafts=[{id:'company-a-craft',company_id:'a',editable:true},{id:'company-a-readonly',company_id:'a',editable:false},{id:'company-b-craft',company_id:'b',editable:true}]
  const line={craft_id:'company-a-craft',category:'开工',temporary:false}
  assert.equal(lineCanEdit(line,'a',crafts,workers[0],false),true)
  assert.equal(lineCanEdit({...line,craft_id:'company-a-readonly'},'a',crafts,workers[0],false),false)
  assert.equal(lineCanEdit({...line,craft_id:'company-b-craft'},'a',crafts,workers[0],false),false)
  assert.equal(lineCanEdit({...line,editable:false},'a',crafts,workers[0],false),false)
  assert.equal(lineCanEdit(line,'a',crafts,workers[1],false),false)
  assert.equal(lineCanEdit({...line,temporary:true},'a',crafts,workers[0],false),false)
  assert.equal(lineCanEdit({...line,temporary:true},'a',crafts,workers[0],true),true)
  assert.equal(lineCanEdit({...line,id:'saved',craft_id:'company-a-readonly',editable:true},'a',crafts,workers[0],false),true)
  assert.equal(lineCanEdit({...line,id:'saved',craft_id:'company-b-craft',editable:true},'a',crafts,workers[0],false),false)
})
