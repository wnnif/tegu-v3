import { test } from 'node:test'
import assert from 'node:assert/strict'
import Module from 'node:module'
import { fileURLToPath } from 'node:url'
import { rolldown } from 'rolldown'
import { ref } from 'vue'
import { moneyCents, toQuantum } from './stockQuantity.ts'

const product = { id: 'p1', name: '防水材料', unit: '桶', aux_unit: '小组', aux_ratio: '6', active: 1, version: 1 }

async function loadWorkflow() {
  const input = fileURLToPath(new URL('./workflows/useStock.ts', import.meta.url))
  const bundle = await rolldown({
    input,
    external: ['vue'],
    plugins: [{
      name: 'stock-api-mock',
      resolveId(id) { return id === '../api' || id === './references' ? `mock:${id}` : null },
      load(id) {
        if (id === 'mock:../api') return 'export const api = (...args) => globalThis.__stockWorkflowApi(...args)'
        if (id === 'mock:./references') return 'export const balances = (...args) => globalThis.__stockWorkflowBalances(...args)'
        return null
      }
    }]
  })
  const result = await bundle.generate({ format: 'cjs' })
  await bundle.close()
  const filename = fileURLToPath(new URL('./workflows/useStock.cjs', import.meta.url))
  const compiled = new Module(filename)
  compiled.filename = filename
  compiled.paths = Module._nodeModulePaths(fileURLToPath(new URL('./', import.meta.url)))
  compiled._compile(result.output[0].code, compiled.filename)
  return compiled.exports.useStock
}

test('purchase saves chosen unit and total, then reopens at the same quantity', async () => {
  const useStock = await loadWorkflow()
  globalThis.__stockWorkflowApi = async path => {
    assert.equal(path, '/reference?tables=products')
    return { products: [product] }
  }
  globalThis.__stockWorkflowBalances = async () => []
  let saved
  const context = {
    perform: async action => action(),
    references: ref({ products: [] }),
    inventory: ref([]),
    user: ref({ name: '测试经手人' }),
    dialog: ref(''),
    submit: async (path, body) => { assert.equal(path, '/stock'); saved = body },
    load: async () => {},
    notice: ref('')
  }
  try {
    const workflow = useStock(context)
    await workflow.openStock('进货', 'main')
    const line = workflow.stockDraft.value.lines[0]
    line.input_unit = 'aux'
    line.input_qty = '6'
    line.input_quantity = '6'
    line.quantity_quantum = toQuantum('6', 'aux', product)
    line.input_amount = '240.00'
    line.total_cents = moneyCents(line.input_amount)
    await workflow.saveStock()
    assert.deepEqual(saved.lines, [{
      product_id: 'p1', input_unit: 'aux', input_quantity: '6',
      quantity_quantum: '6000000', total_cents: 24000, note: ''
    }])

    await workflow.editStock({
      id: 's1', kind: '进货', work_date: '2026-10-11', source_id: '', destination_id: 'main',
      handler: '测试经手人', version: 1, note: '',
      line_items: [{ ...saved.lines[0], qty_micro: 1000000, price_cents: 0, unit: '桶', name: '防水材料' }]
    })
    const reopened = workflow.stockDraft.value.lines[0]
    assert.equal(reopened.input_unit, 'aux')
    assert.equal(reopened.input_qty, '6')
    assert.equal(reopened.quantity_quantum, '6000000')
    assert.equal(reopened.input_amount, '240.00')
    await workflow.saveStock()
    assert.equal(saved.lines[0].quantity_quantum, '6000000')
    assert.equal(saved.lines[0].total_cents, 24000)
  } finally {
    delete globalThis.__stockWorkflowApi
    delete globalThis.__stockWorkflowBalances
  }
})
