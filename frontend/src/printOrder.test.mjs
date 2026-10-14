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
  return nextResolve(specifier,context)
}})
const {acceptanceGroups,formatCents,formatQuantity,printableLines,printableRows}=await import('./printOrder.ts')

test('long saved order prints one row per filled segment and omits unbuilt work', () => {
  const lines = printableLines({lines: [
    {name: '开工', category: '开工', mode: '平方', qty_micro: 3000000, price_cents: 1200, total_cents: 3600,
      segments: Array.from({length: 80}, (_, index) => ({length: 1, width: 1, surface: `区域${index + 1}`}))},
    {name: '未施工', category: '维修', mode: '平方', qty_micro: 0, price_cents: 1000, total_cents: 0,
      segments: [{length: 0, width: 0}]},
    {name: '整项', category: '维修', mode: '金额', qty_micro: 1000000, price_cents: 5000, total_cents: 5000},
  ]})
  const rows = printableRows(lines)
  assert.equal(lines.length, 2)
  assert.equal(rows.length, 81)
  assert.equal(rows[0].first, true)
  assert.equal(rows[1].first, false)
  assert.equal(rows[79].segment.surface, '区域80')
  assert.equal(rows[80].line.name, '整项')
})

test('acceptance layout groups saved work and preserves exact order amounts', () => {
  const groups = acceptanceGroups({lines: [
    {category: '防水涂料', name: '蓝防水', region: '厨房', mode: '平方', qty_micro: 1000000, price_cents: 2500, total_cents: 2500,
      segments: [{surface: '墙面', length: 2, width: 0.3}, {surface: '地面', length: 1, width: 0.4}]},
    {category: '防水涂料', name: '蓝防水', region: '卫生间', mode: '平方', qty_micro: 2000000, price_cents: 2500, total_cents: 5000,
      segments: [{surface: '墙面', length: 2, width: 1}]},
    {category: '包管', name: '包管', mode: '金额', qty_micro: 1000000, price_cents: 66600, total_cents: 66600, description: '水管口施工'},
    {category: '包管', name: '未施工', mode: '平方', qty_micro: 0, price_cents: 1000, total_cents: 0},
  ]})
  assert.equal(groups.length, 2)
  assert.equal(groups[0].rows.length, 2)
  assert.equal(groups[0].rows[0].region, '厨房')
  assert.equal(groups[0].rows[0].wall, '2 × 0.3')
  assert.equal(groups[0].rows[0].floor, '1 × 0.4')
  assert.equal(groups[0].total_cents, 7500n)
  assert.equal(groups[1].rows[0].floor, '水管口施工')
  assert.equal(formatCents(groups.reduce((sum, group) => sum + group.total_cents, 0n)), '741.00')
})
test('按个打印保留数量单位单价和金额，含价实际施工仍打印',()=>{
  const groups=acceptanceGroups({lines:[
    {category:'维修',name:'按个收费',region:'位置甲',mode:'个',qty_micro:2000000,price_cents:10000,total_cents:20000,segments:[{mode:'个',count:2}]},
    {category:'丙纶',name:'组合丙纶',region:'位置乙',mode:'平方',qty_micro:6000000,price_cents:1800,total_cents:0,use_included_price:true,charge_note:'费用已含在防水中',segments:[{mode:'平方',length:2,width:3}]},
  ]})
  assert.equal(groups.length,2)
  assert.equal(groups[0].rows[0].quantity,'2 个')
  assert.equal(groups[0].price_cents,10000)
  assert.equal(groups[0].total_cents,20000n)
  assert.equal(groups[1].rows[0].quantity,'6 ㎡')
  assert.equal(groups[1].rows[0].description,'费用已含在防水中')
  assert.equal(groups[1].total_cents,0n)
})
test('根、处、孔位、副、张、套在交付打印中保留各自整数单位',()=>{
  const modes=['根','处','孔位','副','张','套']
  const groups=acceptanceGroups({lines:modes.map((mode,index)=>({id:'count-'+index,category:'维修',name:'计数工艺-'+mode,mode,qty_micro:2000000,price_cents:100,total_cents:200,segments:[{mode,count:2}]}))})
  assert.deepEqual(groups.map(group=>group.rows[0].quantity),modes.map(mode=>'2 '+mode))
  assert.deepEqual(groups.map(group=>group.rows[0].floor),modes.map(mode=>'2 '+mode))
})

test('按米打印单段长度和已保存合计，包含项目保留工程量且不重复收费',()=>{
  const line={category:'包管',name:'合成长度工艺',region:'位置甲',mode:'米',qty_micro:3750000,price_cents:3000,total_cents:11250,
    segments:[{mode:'米',length:2.5,width:0,height:0},{mode:'米',length:1.25,width:0,height:0}]}
  const groups=acceptanceGroups({lines:[line,{...line,region:'位置乙',use_included_price:true,total_cents:0}]})
  assert.equal(groups.length,2)
  assert.equal(groups[0].rows[0].quantity,'3.75 米')
  assert.equal(groups[0].rows[0].floor,'2.5 米 + 1.25 米')
  assert.equal(groups[0].price_cents,3000)
  assert.equal(groups[0].total_cents,11250n)
  assert.equal(groups[1].rows[0].quantity,'3.75 米')
  assert.equal(groups[1].total_cents,0n)
  assert.equal(printableRows([{...line,segments:[...line.segments,{mode:'米',length:0,width:5}]}]).length,2)
})

test('打印按保存工艺身份和小项部位投影，免费补做保留实际方量与收费备注',()=>{
 const base={category:'维修',name:'同名工艺',mode:'平方',qty_micro:'1234567',price_cents:2000,total_cents:2469,subitem_name:'保存主卫',actual_location:'北墙',region:'历史文字',segments:[{surface:'墙面',length:1,width:1.234567}]}
 const source={lines:[{...base,id:'a',craft_id:'craft-a'},{...base,id:'b',craft_id:'craft-b'}, {...base,id:'free',craft_id:'craft-a',charge_mode:'free',total_cents:0,description:'本次免费'}, {...base,id:'redo',craft_id:'craft-a',charge_mode:'redo',total_cents:0}]}
 const before=JSON.stringify(source),groups=acceptanceGroups(source)
 assert.equal(groups.length,4,'同名不同工艺身份及不同收费模式不会合并')
 assert.equal(groups[0].rows[0].region,'保存主卫；实际部位：北墙；原部位：历史文字')
 assert.equal(groups[0].rows[0].quantity,'1.234567 ㎡')
 assert.deepEqual(groups[0].rows[0].dimensions,['墙面：1 × 1.234567'])
 assert.match(groups[2].rows[0].description,/本次免费；免费施工/)
 assert.equal(groups[3].rows[0].description,'补做不收费')
 assert.equal(groups.reduce((sum,group)=>sum+group.total_cents,0n),4938n)
 assert.equal(JSON.stringify(source),before)
})

test('保存整数分和方量打印不会经过浮点数而丢失末位',()=>{
 const groups=acceptanceGroups({lines:[{category:'维修',name:'保存工艺',mode:'平方',qty_micro:'9007199254740993',price_cents:1,total_cents:'9007199254740993'}]})
 assert.equal(formatCents(groups[0].total_cents),'90071992547409.93')
 assert.equal(groups[0].rows[0].quantity,'9007199254.740993 ㎡')
 assert.equal(formatQuantity('-1250000'),'-1.25')
})
