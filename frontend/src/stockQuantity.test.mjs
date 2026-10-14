import {test} from 'node:test'
import assert from 'node:assert/strict'
import {decimalScaled, fromQuantum, formatQuantum, formatSignedQuantum, moneyCents, moneyFromCents, restoreStockInput, toQuantum, unitFactors} from './stockQuantity.ts'

const product = {unit: '桶', aux_unit: '小组', aux_ratio: '6'}

test('one bucket stays six groups through repeated unit switches', () => {
  const quantity = toQuantum('1', '桶', product)
  assert.equal(quantity, '6000000')
  assert.equal(fromQuantum(quantity, '小组', product), '6')
  for (let index = 0; index < 20; index++) {
    const unit = index % 2 ? '桶' : '小组'
    assert.equal(toQuantum(fromQuantum(quantity, unit, product), unit, product), quantity)
  }
  assert.equal(String(BigInt(quantity) - 6n * BigInt(toQuantum('1', '小组', product))), '0')
})

test('a single group cannot be silently rounded to six decimal buckets', () => {
  const quantity = toQuantum('1', '小组', product)
  assert.equal(fromQuantum(quantity, '桶', product), null)
  assert.equal(formatQuantum(quantity, '桶', product), '1/6')
})

test('decimal ratios and money remain exact', () => {
  const fractional = {unit: '包', aux_unit: '公斤', aux_ratio: '2.5'}
  assert.deepEqual(unitFactors(fractional), {base: 5n, aux: 2n})
  assert.equal(toQuantum('1', '包', fractional), toQuantum('2.5', '公斤', fractional))
  assert.equal(decimalScaled('0.0000001'), null)
  assert.equal(moneyCents('240.00'), '24000')
  assert.equal(moneyCents('240.001'), null)
  assert.equal(moneyFromCents('24000'), '240.00')
})

test('saved auxiliary quantity reopens without another conversion', () => {
  const quantum = toQuantum('1', '小组', product)
  assert.deepEqual(restoreStockInput(quantum, 'aux', '1', product), {unit:'aux', quantity:'1'})
  assert.deepEqual(restoreStockInput(quantum, 'base', '0.166667', product), {unit:'aux', quantity:'1'})
  assert.equal(toQuantum(restoreStockInput(quantum, 'base', '0.166667', product).quantity, 'aux', product), quantum)
})

test('inventory and movement labels retain exact fractions and direction', () => {
  const oneGroup = toQuantum('1', 'aux', product)
  assert.equal(formatQuantum(oneGroup, 'base', product), '1/6')
  assert.equal(formatSignedQuantum('-'+oneGroup, 'base', product), '-1/6')
  assert.equal(formatSignedQuantum(oneGroup, 'aux', product), '+1')
})
