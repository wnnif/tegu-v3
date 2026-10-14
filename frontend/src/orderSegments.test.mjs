import {test} from 'node:test'
import assert from 'node:assert/strict'
import {filled,isCountCraftMode,lineAmount,lineQuantity,measureUnit,quantityLabel,segmentFilled} from './orderSegments.ts'

test('按个数量同时作为公司收费和工资计算的工程量',()=>{
  const line={mode:'个',price_cents:10000,segments:[{mode:'个',count:2}]}
  assert.equal(filled(line),true)
  assert.equal(lineQuantity(line),2)
  assert.equal(lineAmount(line),20000)
  assert.equal(lineQuantity(line)*3000,6000)
  assert.equal(lineAmount({...line,price_cents:8000}),16000)
  assert.equal(quantityLabel(line),'2 个')
})
test('家、根、处、孔位、副、张、套均使用整数计数并保留原计价单位',()=>{
  for(const mode of ['家','根','处','孔位','副','张','套']){
    const line={mode,price_cents:125,segments:[{mode,count:2},{mode,count:3}]}
    assert.equal(isCountCraftMode(mode),true)
    assert.equal(filled(line),true)
    assert.equal(lineQuantity(line),5)
    assert.equal(lineAmount(line),625)
    assert.equal(quantityLabel(line),'5 '+mode)
    assert.equal(measureUnit(mode),mode)
    assert.equal(filled({mode,segments:[{mode,count:1.5}]}),false)
  }
  assert.equal(isCountCraftMode('平方'),false)
})
test('按家收费使用整数户数，2家乘100元/家为20000分',()=>{
  const line={mode:'家',price_cents:10000,segments:[{mode:'家',count:2}]}
  assert.equal(filled(line),true)
  assert.equal(isCountCraftMode('家'),true)
  assert.equal(lineQuantity(line),2)
  assert.equal(lineAmount(line),20000)
  assert.equal(quantityLabel(line),'2 家')
  assert.equal(measureUnit('家'),'家')
  for(const count of ['',0,-1,1.5]) assert.equal(segmentFilled({mode:'家',count}),false)
})
test('空数量不计费，金额模式明确整项是否施工',()=>{
  assert.equal(filled({mode:'个',segments:[{mode:'个',count:''}]}),false)
  assert.equal(segmentFilled({mode:'个',count:0}),false)
  assert.equal(lineAmount({mode:'金额',entered:false,price_cents:10000}),0)
  assert.equal(lineAmount({mode:'金额',entered:true,price_cents:10000}),10000)
})
test('同一小项多段尺寸累计，含价只免公司收费而保留工程量',()=>{
  const line={mode:'平方',price_cents:3000,segments:[{mode:'平方',length:2,width:3},{mode:'平方',length:1,width:2}]}
  assert.equal(lineQuantity(line),8)
  assert.equal(lineAmount(line),24000)
  assert.equal(lineAmount({...line,use_included_price:true}),0)
  assert.equal(lineQuantity({...line,use_included_price:true}),8)
  assert.equal(lineQuantity({mode:'立方',segments:[{mode:'立方',length:2,width:3,height:0.5}]}),3)
  assert.equal(lineAmount({mode:'平方',price_cents:100,segments:[{mode:'平方',length:1.005,width:1}]}),101)
})

test('按米多段长度累计计费，不依赖宽高且保持米单位',()=>{
  const line={mode:'米',price_cents:3000,segments:[{mode:'米',length:'2.5',width:0,height:0},{length:'1.25'}]}
  assert.equal(filled(line),true)
  assert.equal(lineQuantity(line),3.75)
  assert.equal(lineAmount(line),11250)
  assert.equal(quantityLabel(line),'3.75 米')
  assert.equal(measureUnit('米'),'米')
  assert.equal(lineAmount({...line,use_included_price:true}),0)
  assert.equal(lineQuantity({...line,use_included_price:true}),3.75)
})

test('按米空白或零长度不计量，多段累加后按六位小数舍入',()=>{
  assert.equal(segmentFilled({mode:'米',length:''}),false)
  assert.equal(segmentFilled({mode:'米',length:0}),false)
  assert.equal(segmentFilled({mode:'米',width:2,height:3}),false)
  assert.equal(filled({mode:'米',segments:[{mode:'米',length:''}]}),false)
  assert.equal(lineAmount({mode:'米',price_cents:3000,segments:[{mode:'米',length:0}]}),0)
  assert.equal(lineQuantity({mode:'米',segments:[{length:'0.3333334'},{length:'0.3333334'}]}),0.666667)
  assert.equal(lineAmount({mode:'米',price_cents:100,segments:[{length:'1.005'}]}),101)
})
