import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import { registerHooks } from 'node:module'
import { fileURLToPath } from 'node:url'
import { createRenderer, createSSRApp, h, nextTick, reactive } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { parse, compileScript } from '@vue/compiler-sfc'
import ts from 'typescript'

registerHooks({ resolve(specifier, context, nextResolve) {
  if (specifier.startsWith('.') && context.parentURL?.startsWith('file:')) {
    const candidate = new URL(specifier, context.parentURL)
    if (existsSync(fileURLToPath(candidate) + '.ts')) return nextResolve(specifier + '.ts', context)
  }
  return nextResolve(specifier, context)
} })

// Use the actual shared shell and its real slot, following the saved-view SSR suite.
const compiledComponents = new Map()
async function componentURL(file) {
  if (compiledComponents.has(file.href)) return compiledComponents.get(file.href)
  const { descriptor } = parse(await readFile(file, 'utf8'))
  const script = compileScript(descriptor, { id: 'stage-c-column-' + compiledComponents.size, inlineTemplate: true })
  let source = ts.transpileModule(script.content, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
  for (const match of [...source.matchAll(/from\s+(['"])([^'"]+)\1/g)]) {
    const specifier = match[2]
    const url = specifier === 'vue' ? new URL('../node_modules/vue/index.mjs', import.meta.url).href
      : specifier.endsWith('.vue') ? await componentURL(new URL(specifier, file))
      : new URL(specifier.endsWith('.ts') ? specifier : specifier + '.ts', file).href
    source = source.replace(match[0], 'from ' + JSON.stringify(url))
  }
  const url = 'data:text/javascript,' + encodeURIComponent(source)
  compiledComponents.set(file.href, url)
  return url
}
let savedComponent
async function render(order, allowed = true) {
  savedComponent ||= (await import(await componentURL(new URL('./components/SavedOrderWork.vue', import.meta.url)))).default
  return renderToString(createSSRApp(savedComponent, { order, canCustomerPrices: allowed }))
}
const modes = ['个', '米', '平方', '立方', '金额']
const line = (mode, index, extra = {}) => ({
  id: 'line-' + index, craft_id: 'craft-' + index, name: '已保存工艺-' + index,
  category: '自定义大类', mode, spec: '保存规格-' + index,
  subitem_id: 'subitem-' + index, subitem_name: '配置小项名-' + index,
  actual_location: '实际位置-' + index, worker_id: 'worker-' + index, worker_name: '已保存人员-' + index,
  qty_micro: 1000000, waived_qty_micro: 0, chargeable_qty_micro: 1000000,
  price_cents: 67131, total_cents: 81317, description: '保存备注-' + index, segments: [], ...extra,
})
const order = lines => ({ id: 'stage-c-columns', work_date: '2026-10-10', lines, sales: [], materials: [], combinations: [], total_cents: 5765432, wage_cents: 9876543 })
const rowFragments = html => [...html.matchAll(/<section\b[^>]*data-saved-line-id="([^"]*)"[^>]*>([\s\S]*?)<\/section>/g)]
function cell(fragment, label) {
  const match = fragment.match(new RegExp('<div\\b[^>]*data-label="' + label + '"[^>]*>([\\s\\S]*?)</div>'))
  assert.ok(match, 'missing cell: ' + label)
  return match[1]
}
function freezeDeep(value) {
  if (value && typeof value === 'object') {
    Object.values(value).forEach(freezeDeep)
    Object.freeze(value)
  }
  return value
}

test('只读五计量配置小项直接归部位列，旧独立位置仍保留，共享SSR slot不漏行', async () => {
  const lines = modes.map((mode, index) => line(mode, index))
  const html = await render(order(lines)), rendered = rowFragments(html)
  assert.equal((html.match(/role="columnheader"/g) || []).length, 9)
  assert.equal(rendered.length, modes.length)
  for (const [index, row] of rendered.entries()) {
    assert.equal(row[1], lines[index].id)
    assert.equal((row[2].match(/role="cell"/g) || []).length, 8)
    assert.ok(cell(row[2], '工艺').includes(lines[index].name))
    assert.ok(!cell(row[2], '工艺').includes(lines[index].subitem_name))
    assert.ok(cell(row[2], '部位').includes(lines[index].subitem_name))
    assert.ok(cell(row[2], '部位').includes('原独立部位：' + lines[index].actual_location))
    assert.doesNotMatch(cell(row[2], '工艺'), /配置小项：/)
  }
})

test('只读五计量无配置小项保留旧region，缺小项名称不按ID猜配置或填造部位', async () => {
  const lines = modes.map((mode, index) => line(mode, index, { subitem_id: '', subitem_name: '', actual_location: '', region: '旧兼容部位不作回填' }))
  lines.push(line('平方', 5, { subitem_name: '', actual_location: '' }))
  const rendered = rowFragments(await render(order(lines)))
  assert.equal(rendered.length, lines.length)
  for (const row of rendered.slice(0, modes.length)) {
    assert.doesNotMatch(cell(row[2], '工艺'), /配置小项|未填写实际部位/)
    assert.match(cell(row[2], '部位'), /旧兼容部位不作回填/)
    assert.doesNotMatch(cell(row[2], '部位'), /未配置小项|未记录小项名称/)
  }
  assert.doesNotMatch(cell(rendered.at(-1)[2], '工艺'), /配置小项：/)
  assert.match(cell(rendered.at(-1)[2], '部位'), />—</)
})

test('阶段C列修正保留组合双方小项描述，无价只读不泄露金额工资、不请求或改写快照', async t => {
  const water = { ...line('平方', 0), category: '防水涂料', order_id: 'stage-c-columns', number: 'C-001', work_date: '2026-10-10' }
  const poly = { ...line('平方', 1), category: '丙纶', order_id: 'earlier-order', number: 'C-002', work_date: '2026-09-29', subitem_name: '早期配置小项', actual_location: '早期实际位置', waived_qty_micro: 500000, chargeable_qty_micro: 500000 }
  const saved = freezeDeep({ ...order([water]), combinations: [{ id: 'relation', waterproof_line_id: water.id, polypropylene_line_id: poly.id, waterproof_order_id: water.order_id, polypropylene_order_id: poly.order_id, waive_direction: 'polypropylene', waived_qty_micro: 500000 }], combination_details: [{ id: 'relation', waterproof: water, polypropylene: poly }] })
  const before = JSON.stringify(saved), requests = [], previousFetch = globalThis.fetch
  globalThis.fetch = (...args) => { requests.push(args); throw new Error('只读渲染不允许HTTP请求') }
  t.after(() => { globalThis.fetch = previousFetch })
  const html = await render(saved, false)
  const combination = html.match(/<section\b[^>]*aria-label="已保存组合计价关系"[^>]*>([\s\S]*?)<\/section>/)?.[1]
  assert.ok(combination)
  assert.ok(combination.includes('配置小项：' + water.subitem_name))
  assert.ok(combination.includes('配置小项：' + poly.subitem_name))
  assert.match(combination, /跨日期关联/)
  assert.doesNotMatch(html, /671\.31|813\.17|57654\.32|98765\.43|order-total-value|<input|<select|<button/)
  assert.match(html, /无查看权限/)
  assert.deepEqual(requests, [])
  assert.equal(JSON.stringify(saved), before)
})
// A minimal host lets the real saved component update in place, not just fresh SSR apps.
function memoryHost() {
  const node = (type, text = '') => ({ type, text, props: {}, children: [], parent: null })
  function remove(child) {
    if (!child.parent) return
    const index = child.parent.children.indexOf(child)
    if (index !== -1) child.parent.children.splice(index, 1)
    child.parent = null
  }
  function insert(child, parent, anchor = null) {
    remove(child)
    const index = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(index < 0 ? parent.children.length : index, 0, child)
    child.parent = parent
  }
  const renderer = createRenderer({
    createElement: type => node(type), createText: text => node('text', text), createComment: text => node('comment', text),
    setText: (target, text) => { target.text = text },
    setElementText: (target, text) => { target.text = text; target.children = [] },
    insert, remove, parentNode: target => target.parent,
    nextSibling: target => target.parent?.children[target.parent.children.indexOf(target) + 1] || null,
    patchProp: (target, key, _previous, value) => { target.props[key] = value },
    insertStaticContent: (content, parent, anchor) => { const target = node('static', content); insert(target, parent, anchor); return [target, target] },
  })
  const root = node('root')
  const elements = (target = root) => [target, ...target.children.flatMap(child => elements(child))]
  const text = (target = root) => target.text + target.children.map(child => text(child)).join('')
  return { renderer, root, elements, text }
}

test('阶段C同一只读组件换保存订单不串人员部位与编号，撤销价格权限立即清空金额DOM', async t => {
  savedComponent ||= (await import(await componentURL(new URL('./components/SavedOrderWork.vue', import.meta.url)))).default
  const first = freezeDeep({ ...order([line('米', 0, { worker_name: '第一单保存工人', actual_location: '第一单保存位置' })]), id: 'first-saved-order' })
  const second = freezeDeep({ ...order([line('米', 0, { worker_name: '第二单保存工人', actual_location: '第二单保存位置' })]), id: 'second-saved-order', total_cents: 7654321 })
  const before = JSON.stringify([first, second]), props = reactive({ order: first, canCustomerPrices: true })
  const host = memoryHost(), app = host.renderer.createApp({ setup: () => () => h(savedComponent, props) })
  const requests = [], previousFetch = globalThis.fetch
  globalThis.fetch = (...args) => { requests.push(args); throw new Error('只读更新不允许HTTP请求') }
  t.after(() => { app.unmount(); globalThis.fetch = previousFetch })
  app.mount(host.root)
  assert.match(host.text(), /第一单保存工人|第一单保存位置/)
  props.order = second
  await nextTick()
  assert.match(host.text(), /第二单保存工人/)
  assert.match(host.text(), /第二单保存位置/)
  assert.doesNotMatch(host.text(), /第一单保存工人|第一单保存位置/)
  const displayed = host.elements().filter(target => target.props['data-saved-order-id'])
  assert.deepEqual(displayed.map(target => target.props['data-saved-order-id']), [second.id])
  assert.deepEqual(host.elements().filter(target => target.props['data-saved-line-id']).map(target => target.props.id), ['saved-order-second-saved-order-line-line-0'])
  assert.equal(host.elements().filter(target => target.props.class === 'order-total-value').length, 1)
  props.canCustomerPrices = false
  await nextTick()
  assert.equal(host.elements().filter(target => target.props.class === 'order-total-value').length, 0)
  assert.doesNotMatch(host.text(), /671\.31|813\.17|76,543\.21|76543\.21|98765\.43/)
  assert.match(host.text(), /无查看权限/)
  assert.deepEqual(requests, [])
  assert.equal(JSON.stringify([first, second]), before)
})

const { groupOrderRowsBySubitem } = await import('./orderRowGroups.ts')
const { savedOrderWorkView } = await import('./orderWorkView.ts')

function subitemLocations(mode, index) {
  const location = (suffix, subitem, actual, sortOrder, extra = {}) => line(mode, index, {
    id: `group-${index}-${suffix}`, client_key: `group-key-${index}-${suffix}`,
    craft_id: `group-craft-${index}`, category: '保存小项顺序大类', name: '同名工艺',
    subitem_id: subitem, subitem_name: '同名配置小项', actual_location: actual,
    sort_order: sortOrder, temporary: false, editable: true, entered: true,
    worker_override: true, ...extra,
  })
  return [
    location('alpha-kitchen', 'sub-z', '厨房', 40),
    location('beta-kitchen', 'sub-a', '厨房', 1),
    location('alpha-bath', 'sub-z', '主卫', 99, { subitem_name: '同ID另一保存名称', worker_id: `override-${index}`, worker_name: `覆盖施工人-${index}` }),
    location('alpha-kitchen-again', 'sub-z', '厨房', 0),
  ]
}
const contiguousIDs = rows => [rows[0], rows[2], rows[3], rows[1]].map(row => row.id)

// Missing IDs remain individual saved rows; neither labels nor positions invent identity.
test('阶段C共享小项分组只投影新数组，首次出现顺序与内部顺序不依赖名称位置或sort_order', () => {
  const source = freezeDeep([
    { id: 'z-1', subitem_id: 'z', subitem_name: '同名', actual_location: '厨房', sort_order: 90 },
    { id: 'legacy-1', subitem_id: '', subitem_name: '同名', actual_location: '厨房', sort_order: 1 },
    { id: 'a-1', subitem_id: 'a', subitem_name: '同名', actual_location: '厨房', sort_order: 0 },
    { id: 'z-2', subitem_id: 'z', subitem_name: '另一保存名称', actual_location: '主卫', sort_order: 20 },
    { id: 'legacy-2', subitem_name: '同名', actual_location: '主卫', sort_order: 2 },
    { id: 'a-2', subitem_id: 'a', subitem_name: '同名', actual_location: '厨房', sort_order: 10 },
  ])
  const before = JSON.stringify(source), grouped = groupOrderRowsBySubitem(source)
  assert.deepEqual(grouped.map(row => row.id), ['z-1', 'z-2', 'legacy-1', 'a-1', 'a-2', 'legacy-2'])
  assert.notEqual(grouped, source)
  for (const row of grouped) assert.equal(row, source.find(original => original.id === row.id))
  assert.equal(JSON.stringify(source), before)
})

test('阶段C五计量保存查看按小项首遇顺序连续显示多部位，不合并同名同位置行或改写快照', async t => {
  const perCraft = modes.map(subitemLocations), source = perCraft.flat()
  const saved = freezeDeep(order(source)), before = JSON.stringify(saved)
  const requests = [], previousFetch = globalThis.fetch
  globalThis.fetch = (...args) => { requests.push(args); throw new Error('展示分组不允许HTTP请求') }
  t.after(() => { globalThis.fetch = previousFetch })
  const view = savedOrderWorkView(saved, true), crafts = view.categories[0].crafts
  assert.equal(crafts.length, modes.length, '同名工艺仍按工艺ID独立')
  assert.deepEqual(crafts.map(craft => craft.rows.map(row => row.id)), perCraft.map(contiguousIDs))
  const html = await render(saved)
  assert.deepEqual(rowFragments(html).map(row => row[1]), perCraft.flatMap(contiguousIDs))
  for (const row of crafts.flatMap(craft => craft.rows)) {
    const original = source.find(line => line.id === row.id)
    for (const field of ['subitem_id', 'subitem_name', 'actual_location', 'worker_id', 'worker_name', 'qty_micro', 'price_cents', 'total_cents']) assert.equal(row[field], original[field], field + ' remains saved')
    assert.equal(row.key, JSON.stringify([saved.id, original.id]))
  }
  assert.equal(JSON.stringify(saved), before)
  assert.deepEqual(requests, [])
})

let craftComponent
async function renderCraftInput(draft, references) {
  craftComponent ||= (await import(await componentURL(new URL('./components/OrderCraftEditor.vue', import.meta.url)))).default
  return renderToString(createSSRApp(craftComponent, { draft, references, locked: false, can: permission => permission !== 'wages', allowedCrafts: ['保存小项顺序大类'] }))
}

test('阶段C五计量真实输入九列按小项连续回显，追加位置不重排草稿保存ID人员尺寸金额或sort_order', async t => {
  const perCraft = modes.map(subitemLocations), lines = perCraft.flat()
  const draft = freezeDeep({ ...order(lines), company_id: 'group-company', worker_id: '', category_workers: {} })
  const workers = [...new Map(lines.map(row => [row.worker_id, { id: row.worker_id, name: row.worker_name, active: 1, crafts: ['保存小项顺序大类'] }])).values()]
  const references = freezeDeep({ crafts: perCraft.map(rows => ({ id: rows[0].craft_id, company_id: draft.company_id, editable: true })), workers, companies: [], products: [], warehouses: [], users: [] })
  const before = JSON.stringify(draft), requests = [], previousFetch = globalThis.fetch
  globalThis.fetch = (...args) => { requests.push(args); throw new Error('SSR输入分组不允许HTTP请求') }
  t.after(() => { globalThis.fetch = previousFetch })
  const html = await renderCraftInput(draft, references)
  const displayed = [...html.matchAll(/<section\b[^>]*data-line-key="([^"]+)"[^>]*>/g)].map(match => match[1])
  const expected = perCraft.flatMap(rows => [rows[0], rows[2], rows[3], rows[1]]).map(row => row.client_key)
  assert.deepEqual(displayed, expected)
  assert.equal(JSON.stringify(draft), before, '显示投影不得改写原草稿或保存数组')
  assert.deepEqual(requests, [])
})
