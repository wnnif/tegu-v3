import { test } from 'node:test'
import assert from 'node:assert/strict'
import { cents, financeOrder, parseReceiptAmount, settlementMoney } from './settlements.ts'

test('receipt amount uses exact cents for values prone to floating point rounding', () => {
  assert.equal(parseReceiptAmount('1.01'), 101)
  assert.equal(parseReceiptAmount('0.29'), 29)
  assert.equal(parseReceiptAmount('1234.5'), 123450)
  assert.equal(parseReceiptAmount('1000000000000.00'), 100000000000000)
})
test('receipt amount rejects invalid precision, zero, scientific notation and over-limit values', () => {
  for (const input of ['', '0', '-1', '1.001', '1e3', '1,000.00', '1000000000000.01']) assert.throws(() => parseReceiptAmount(input))
})
test('remaining collection excludes prior receipts and cancelled legacy orders', () => {
  assert.equal(financeOrder({total_cents:10000, received_cents:2500, status:'部分收款'}).outstanding_cents, '7500')
  assert.equal(financeOrder({total_cents:10000, status:'已收款'}).outstanding_cents, '0')
  assert.equal(financeOrder({total_cents:10000, status:'已取消'}).outstanding_cents, '0')
})
test('batch totals preserve cents above the safe integer aggregate range', () => {
  const sum = Array.from({length:100}, () => cents('99999999999999')).reduce((total, amount) => total + amount, 0n)
  assert.equal(settlementMoney(sum), '¥ 99,999,999,999,999.00')
})
