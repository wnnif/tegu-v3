import {test} from 'node:test'
import assert from 'node:assert/strict'
import {saleAmountCents,scaledQuantity} from './orderSegments.ts'
test('售料金额按六位数量和整数分计算，五入与服务端一致',()=>{
 assert.equal(saleAmountCents('1.005',100),101)
 assert.equal(saleAmountCents('2',20000),40000)
 assert.equal(saleAmountCents('0.333333',10000),3333)
 assert.equal(scaledQuantity('1.0000005'),1000001n)
 assert.equal(saleAmountCents('',20000),0)
 assert.equal(saleAmountCents('1e3',100),100000)
})
