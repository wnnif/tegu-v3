import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import { registerHooks } from 'node:module'
import { fileURLToPath } from 'node:url'
import { createSSRApp } from 'vue'
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
const { savedOrderWorkView, savedQuantityText, savedRowAnchor } = await import('./orderWorkView.ts')
const { CUSTOMER_PRICE_HIDDEN, customerMoney } = await import('./customerPrices.ts')

function line(id, mode, extra = {}) {
  return {
    id, craft_id: 'craft-' + id, category: '自定义未来大类', name: '保存工艺-' + id,
    mode, spec: '保存规格', subitem_id: 'subitem-' + id, subitem_name: '保存小项-' + id,
    actual_location: '保存部位-' + id, worker_id: 'worker-' + id, worker_name: '保存施工人-' + id,
    description: '保存备注-' + id, sort_order: 0, qty_micro: 1000000,
    waived_qty_micro: 0, chargeable_qty_micro: 1000000, price_cents: 1234, total_cents: 1122,
    segments: [], temporary: false, ...extra,
  }
}
function order(extra = {}) {
  return {
    id: 'order-snapshot', number: 'C-只读-001', work_date: '2026-10-10',
    worker_id: 'header-worker', worker_name: '单头保存责任人，不代入施工行',
    category_workers: { '自定义未来大类': 'category-default' },
    total_cents: 456789, wage_cents: 987654321, delivery_wage_cents: 876543210,
    lines: [], sales: [], materials: [], combinations: [], ...extra,
  }
}
function allModes() {
  return [
    line('count', '个', { qty_micro: 3000000, chargeable_qty_micro: 3000000, total_cents: 4011, segments: [{ mode: '个', count: 99, length: 888, width: 777 }] }),
    line('length', '米', { qty_micro: '3456789', chargeable_qty_micro: '3456789', total_cents: 5222, segments: [{ mode: '米', length: '99.75', width: 666, height: 555 }, { mode: '米', length: '0.5' }] }),
    line('area', '平方', { qty_micro: 5500000, chargeable_qty_micro: 5500000, total_cents: 6333, segments: [{ mode: '平方', surface: '墙面', length: 99, width: 88, height: 777 }] }),
    line('volume', '立方', { qty_micro: 7125000, chargeable_qty_micro: 7125000, total_cents: 7444, segments: [{ mode: '立方', surface: '其他', length: 9, width: 8, height: 7 }] }),
    line('amount', '金额', { qty_micro: 1000000, chargeable_qty_micro: 1000000, total_cents: 8555, price_cents: 9999, segments: [{ mode: '金额', amount_cents: 98765 }] }),
  ]
}
const rows = view => view.categories.flatMap(category => category.crafts.flatMap(craft => craft.rows))
function freezeDeep(value) {
  if (value && typeof value === 'object') {
    for (const item of Object.values(value)) freezeDeep(item)
    Object.freeze(value)
  }
  return value
}
function combinationFixture(direction = 'polypropylene', waived = 40000000) {
  const water = {
    ...line('actual-water', '平方', { craft_id: 'fixed-water', category: '防水涂料', name: '保存防水', qty_micro: 60000000, price_cents: 5000, total_cents: 300000, chargeable_qty_micro: 60000000 }),
    order_id: 'order-snapshot', number: 'C-只读-001', work_date: '2026-10-10',
  }
  const poly = {
    ...line('actual-poly', '平方', { craft_id: 'fixed-poly', category: '丙纶', name: '保存丙纶', qty_micro: 100000000, price_cents: 3000, total_cents: 180000, waived_qty_micro: 40000000, chargeable_qty_micro: 60000000 }),
    order_id: 'earlier-order', number: 'C-早期-099', work_date: '2026-09-20',
    subitem_name: '早期保存小项', actual_location: '早期厨房', worker_name: '早期保存施工人',
  }
  const target = direction === 'waterproof' ? water : poly
  target.waived_qty_micro = waived
  target.chargeable_qty_micro = Number(target.qty_micro) - waived
  target.total_cents = waived === Number(target.qty_micro) ? 0 : direction === 'waterproof' ? 199991 : 179993
  return order({
    total_cents: 478888, lines: [water],
    combinations: [{ id: 'saved-relationship', waterproof_line_id: water.id, polypropylene_line_id: poly.id, waterproof_order_id: water.order_id, polypropylene_order_id: poly.order_id, waterproof_order_version: 3, polypropylene_order_version: 6, waive_direction: direction, waived_qty_micro: waived }],
    combination_details: [{ id: 'saved-relationship', waterproof: water, polypropylene: poly }],
  })
}

// Compile real Vue components using the repository's existing Node/SSR harness
// convention. Child .vue imports are compiled too, not replaced by fake shells.
const componentURLs = new Map()
async function componentURL(file) {
  if (componentURLs.has(file.href)) return componentURLs.get(file.href)
  const { descriptor } = parse(await readFile(file, 'utf8'))
  const script = compileScript(descriptor, { id: 'saved-order-spec-' + componentURLs.size, inlineTemplate: true })
  let compiled = ts.transpileModule(script.content, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
  const imports = [...compiled.matchAll(/from\s+(['"])([^'"]+)\1/g)]
  for (const match of imports) {
    const specifier = match[2]
    const url = specifier === 'vue' ? new URL('../node_modules/vue/index.mjs', import.meta.url).href
      : specifier.endsWith('.vue') ? await componentURL(new URL(specifier, file))
      : new URL(specifier.endsWith('.ts') ? specifier : specifier + '.ts', file).href
    compiled = compiled.replace(match[0], 'from ' + JSON.stringify(url))
  }
  const url = 'data:text/javascript,' + encodeURIComponent(compiled)
  componentURLs.set(file.href, url)
  return url
}
let savedComponent
async function render(saved, canCustomerPrices = true, canWages = false) {
  savedComponent ||= (await import(await componentURL(new URL('./components/SavedOrderWork.vue', import.meta.url)))).default
  return renderToString(createSSRApp(savedComponent, { order: saved, canCustomerPrices, canWages }))
}

test('五种计量只格式化保存数量，尺寸与单价不重新计算保存小计或整单总額', () => {
  const saved = order({ lines: allModes(), total_cents: '4099988' })
  const view = savedOrderWorkView(saved, true), actual = rows(view)
  assert.deepEqual(actual.map(row => row.quantity_text), ['3 个', '3.456789 米', '5.5 ㎡', '7.125 m³', '1 项'])
  assert.deepEqual(actual.map(row => row.qty_micro), saved.lines.map(row => row.qty_micro))
  assert.deepEqual(actual.map(row => row.total_cents), [4011, 5222, 6333, 7444, 8555])
  assert.equal(view.total_cents, '4099988')
  assert.match(actual[0].dimensions[0].text, /99 个/)
  assert.doesNotMatch(actual[0].dimensions[0].text, /888|777/)
  assert.match(actual[1].dimensions[0].text, /99\.75 米/)
  assert.doesNotMatch(actual[1].dimensions[0].text, /666|555/)
  assert.equal(actual[1].dimensions.length, 2)
  assert.match(actual[2].dimensions[0].text, /墙面：长 99 米 × 宽 88 米/)
  assert.doesNotMatch(actual[2].dimensions[0].text, /777/)
  assert.match(actual[3].dimensions[0].text, /高 7 米/)
  assert.equal(actual[4].dimensions[0].text, '整项金额：' + customerMoney(98765))
  assert.equal(actual[4].amount_text, customerMoney(8555))
})

test('大类与工艺保持API保存顺序，同名不同ID不合并，同一小项多部位各有稳定明细身份', () => {
  const saved = order({ lines: [
    line('a-first', '平方', { category: '后出现名称', craft_id: 'same-craft', name: '同名工艺', subitem_id: 'same-sub', subitem_name: '旧保存小项', actual_location: '厨房', sort_order: 40 }),
    line('b', '平方', { category: '后出现名称', craft_id: 'other-craft', name: '同名工艺', sort_order: 2 }),
    line('a-second', '平方', { category: '后出现名称', craft_id: 'same-craft', name: '同名工艺', subitem_id: 'same-sub', subitem_name: '后来保存小项', actual_location: '主卫', sort_order: 41 }),
    line('c', '米', { category: '先出现名称', craft_id: 'same-craft', name: '同名工艺' }),
    line('temporary-1', '金额', { category: '后出现名称', craft_id: '', name: '同名临时项', temporary: true }),
    line('temporary-2', '金额', { category: '后出现名称', craft_id: '', name: '同名临时项', temporary: true }),
  ] })
  const view = savedOrderWorkView(saved, true)
  assert.deepEqual(view.categories.map(category => category.category), ['后出现名称', '先出现名称'])
  const crafts = view.categories[0].crafts
  assert.deepEqual(crafts.map(craft => craft.rows.map(row => row.id)), [['a-first', 'a-second'], ['b'], ['temporary-1'], ['temporary-2']])
  assert.notEqual(crafts[0].key, crafts[1].key)
  const locations = crafts[0].rows
  assert.deepEqual(locations.map(row => [row.subitem_name, row.actual_location]), [['旧保存小项', '厨房'], ['后来保存小项', '主卫']])
  assert.notEqual(locations[0].key, locations[1].key)
  assert.notEqual(locations[0].anchor, locations[1].anchor)
  const reversed = rows(savedOrderWorkView(order({ lines: [...saved.lines].reverse() }), true))
  assert.deepEqual(Object.fromEntries(reversed.map(row => [row.id, row.key])), Object.fromEntries(rows(view).map(row => [row.id, row.key])))
})

test('施工行人员和A2字段只读自身保存快照，不借单头、大类默认、当前配置或旧region补值', () => {
  const saved = order({ lines: [
    line('explicit', '米', { worker_id: 'worker-explicit', worker_name: '保存显式施工人' }),
    line('no-name', '米', { worker_id: 'header-worker', worker_name: '', subitem_name: '', actual_location: '', region: '不得猜为实际部位' }),
  ], category_workers: { '自定义未来大类': '新大类默认' }, references: { workers: [{ id: 'header-worker', name: '当前改名人员' }], crafts: [{ id: 'craft-no-name', name: '当前新名称', subitems: [{ id: 'subitem-no-name', name: '当前小项' }] }] } })
  const actual = rows(savedOrderWorkView(saved, true))
  assert.deepEqual(actual.map(row => row.worker_name), ['保存显式施工人', ''])
  assert.equal(actual[1].subitem_name, '')
  assert.equal(actual[1].actual_location, '')
  assert.equal(actual[1].name, '保存工艺-no-name')
  assert.equal(actual[1].region, '不得猜为实际部位')
  assert.doesNotMatch(JSON.stringify(actual), /当前改名人员|当前小项|新大类默认|单头保存责任人/)
})

test('适配器读取冻结快照不修改源数据，不保留草稿或原尺寸数组引用，查看不产生写入入口', async () => {
  const saved = combinationFixture(), before = structuredClone(saved)
  saved.lines.push(...allModes())
  before.lines.push(...allModes())
  freezeDeep(saved)
  const a = savedOrderWorkView(saved, true), b = savedOrderWorkView(saved, false)
  assert.deepEqual(saved, before)
  assert.notEqual(rows(a), saved.lines)
  assert.notEqual(rows(a)[0].dimensions, saved.lines[0].segments)
  assert.equal(a.combinations[0].cross_date, true)
  assert.equal(b.combinations[0].cross_date, true)
  assert.deepEqual(saved, before)
  const adapter = await readFile(new URL('./orderWorkView.ts', import.meta.url), 'utf8')
  const component = await readFile(new URL('./components/SavedOrderWork.vue', import.meta.url), 'utf8')
  assert.doesNotMatch(adapter, /from ['"](?:\.\/api|\.\/workflows\/useOrders)['"]|lineQuantityMicro\s*\(|lineAmount\s*\(|saleAmountCents\s*\(/)
  assert.doesNotMatch(component, /v-model|<input\b|<select\b|<button\b|defineEmits|\b(?:api|command|submit|watch|onMounted)\s*[(<]/)
})

test('无客户价格权限即使收到完整缓存也不向视图泄露任何收费字段、金额段或整单总额', () => {
  const saved = combinationFixture()
  saved.lines.push(line('amount-secret', '金额', { price_cents: 73456987, total_cents: 65349876, segments: [{ mode: '金额', amount_cents: 54328765 }] }))
  saved.sales.push({ id: 'sale-secret', product_id: 'p', warehouse_id: 'w', product_name: '保存售料', unit: '桶', qty_micro: 2000000, sale_qty_micro: 3000000, price_unit: '公斤', price_cents: 43217654, total_cents: 32106543 })
  saved.total_cents = 21095432
  const view = savedOrderWorkView(saved, false), serialized = JSON.stringify(view)
  assert.equal(Object.hasOwn(view, 'total_cents'), false)
  assert.ok(rows(view).every(row => !Object.hasOwn(row, 'price_cents') && !Object.hasOwn(row, 'total_cents') && row.price_text === CUSTOMER_PRICE_HIDDEN && row.amount_text === CUSTOMER_PRICE_HIDDEN))
  assert.equal(view.sales[0].amount_text, CUSTOMER_PRICE_HIDDEN)
  assert.equal(view.combinations[0].polypropylene.amount_text, CUSTOMER_PRICE_HIDDEN)
  assert.doesNotMatch(serialized, /price_cents|total_cents|amount_cents|¥|73456987|65349876|54328765|43217654|32106543|21095432/)
  assert.equal(rows(view).at(-1).dimensions[0].text, '整项金额：' + CUSTOMER_PRICE_HIDDEN)
  assert.equal(view.combinations[0].polypropylene.quantity_text, '100 ㎡')
})

test('真实0元明确显示，客户金额缺省不补0，缺数量也不猜为0或整项1', () => {
  const view = savedOrderWorkView(order({ total_cents: 0, lines: [
    line('zero', '金额', { price_cents: 0, total_cents: 0, qty_micro: 0, segments: [{ mode: '金额', amount_cents: 0 }] }),
    line('missing', '金额', { price_cents: undefined, total_cents: undefined, qty_micro: undefined, chargeable_qty_micro: undefined }),
  ] }), true)
  assert.equal(view.total_cents, 0)
  assert.equal(rows(view)[0].price_text, customerMoney(0))
  assert.equal(rows(view)[0].amount_text, customerMoney(0))
  assert.equal(rows(view)[0].quantity_text, '0 项')
  assert.equal(rows(view)[1].price_text, CUSTOMER_PRICE_HIDDEN)
  assert.equal(rows(view)[1].amount_text, CUSTOMER_PRICE_HIDDEN)
  assert.equal(rows(view)[1].quantity_text, '未记录')
  assert.equal(Object.hasOwn(rows(view)[1], 'price_cents'), false)
  assert.equal(Object.hasOwn(savedOrderWorkView(order({ total_cents: undefined }), true), 'total_cents'), false)
})

test('组合原关系保留两个方向、部分和全部免收，显示已保存实际/累计免收/剩余/金额而非重新分配', () => {
  for (const [direction, waived] of [['polypropylene', 40000000], ['polypropylene', 100000000], ['waterproof', 20000000], ['waterproof', 60000000]]) {
    const saved = combinationFixture(direction, waived), view = savedOrderWorkView(saved, true), relation = view.combinations[0]
    const snapshot = direction === 'waterproof' ? saved.combination_details[0].waterproof : saved.combination_details[0].polypropylene
    const side = relation[direction]
    assert.equal(view.combination_enabled, true)
    assert.equal(relation.direction, direction)
    assert.equal(relation.waived_quantity_text, savedQuantityText(waived, '㎡'))
    assert.equal(side.quantity_text, savedQuantityText(snapshot.qty_micro, '㎡'))
    assert.equal(side.waived_quantity_text, savedQuantityText(snapshot.waived_qty_micro, '㎡'))
    assert.equal(side.chargeable_quantity_text, savedQuantityText(snapshot.chargeable_qty_micro, '㎡'))
    assert.equal(side.total_cents, snapshot.total_cents)
    assert.equal(view.total_cents, 478888)
  }
  const saved = combinationFixture()
  saved.combination_details[0].polypropylene.waived_qty_micro = 70000000
  saved.combination_details[0].polypropylene.chargeable_qty_micro = 30000000
  const relation = savedOrderWorkView(saved, true).combinations[0]
  assert.equal(relation.waived_quantity_text, '40 ㎡')
  assert.equal(relation.polypropylene.waived_quantity_text, '70 ㎡')
  assert.equal(relation.polypropylene.chargeable_quantity_text, '30 ㎡')
  assert.equal(relation.polypropylene.total_cents, 179993)
  const noRelation = savedOrderWorkView(order({ combination_enabled: true, combinations: [], included_by_craft_id: 'fixed-water' }), true)
  assert.equal(noRelation.combination_enabled, false)
})

test('组合按真实关联ID读取跨日期部位/施工人，元数据缺失或不匹配只展示显式编号不猜值', () => {
  const saved = combinationFixture(), view = savedOrderWorkView(saved, true), relation = view.combinations[0]
  assert.equal(relation.cross_date, true)
  assert.equal(relation.polypropylene.work_date, '2026-09-20')
  assert.equal(relation.polypropylene.number, 'C-早期-099')
  assert.equal(relation.polypropylene.actual_location, '早期厨房')
  assert.equal(relation.polypropylene.subitem_name, '早期保存小项')
  assert.equal(relation.polypropylene.worker_name, '早期保存施工人')
  assert.equal(relation.waterproof.anchor, savedRowAnchor(saved.id, 'actual-water'))
  assert.equal(relation.polypropylene.anchor, undefined)
  delete saved.combination_details
  const missing = savedOrderWorkView(saved, true).combinations[0]
  assert.equal(missing.cross_date, false)
  assert.deepEqual([missing.waterproof.order_id, missing.waterproof.id], ['order-snapshot', 'actual-water'])
  assert.deepEqual([missing.polypropylene.order_id, missing.polypropylene.id], ['earlier-order', 'actual-poly'])
  for (const side of [missing.waterproof, missing.polypropylene]) {
    assert.equal(side.has_details, false)
    assert.deepEqual([side.work_date, side.actual_location, side.worker_name, side.number], ['', '', '', ''])
    assert.equal(side.quantity_text, '未记录')
    assert.equal(side.amount_text, CUSTOMER_PRICE_HIDDEN)
  }
  const wrong = combinationFixture()
  wrong.combination_details[0].polypropylene.id = 'unrelated-line'
  wrong.combination_details[0].waterproof.order_id = 'unrelated-order'
  assert.ok(Object.values(savedOrderWorkView(wrong, true).combinations[0]).filter(value => value && typeof value === 'object').every(side => side.has_details === false))
})

test('空施工和纯售料合法，整单金额只用保存总额，材料和送货工资不相加，售料换算不重算', () => {
  const saved = order({
    total_cents: 40999, note: '整单保存备注',
    sales: [{ id: 'actual-sale', product_id: 'saved-product', warehouse_id: 'saved-warehouse', product_name: '保存售料名称', unit: '桶', qty_micro: 2000000, sale_qty_micro: 5500000, price_unit: '公斤', conversion_ratio: '当前故意不一致的99', price_cents: 76543, total_cents: 31007 }],
    materials: [{ product_id: 'saved-material', warehouse_id: 'saved-warehouse', product_name: '保存耗材', warehouse_name: '保存仓库', unit: '袋', qty_micro: 1250000 }],
  })
  const view = savedOrderWorkView(saved, true)
  assert.deepEqual(view.categories, [])
  assert.equal(view.total_cents, 40999)
  assert.equal(view.sales[0].total_cents, 31007)
  assert.equal(view.sales[0].quantity_text, '2 桶')
  assert.equal(view.sales[0].sale_quantity_text, '5.5 公斤')
  assert.equal(view.materials[0].quantity_text, '1.25 袋')
  assert.equal(view.material_worker_name, saved.worker_name)
  assert.equal(view.note, saved.note)
  assert.doesNotMatch(JSON.stringify(view), /wage_cents|987654321|876543210|conversion_ratio/)
  assert.deepEqual(savedOrderWorkView(order({ total_cents: 0 }), true).categories, [])
})

test('售料实际数量使用已存精确量和换算比例，DTO缺辅助名称仍显示1桶或1/6桶', async () => {
  const saved = order({ sales: [
    { id: 'base-sale', product_id: 'p', warehouse_id: 'w', product_name: '整桶售料', unit: '桶', conversion_ratio: '6', qty_quantum: '6000000', qty_micro: 1000000, sale_qty_micro: 1000000, price_unit: '桶', price_cents: 1000, total_cents: 1000 },
    { id: 'aux-sale', product_id: 'p', warehouse_id: 'w', product_name: '小组售料', unit: '桶', conversion_ratio: '6', qty_quantum: '1000000', qty_micro: 166667, sale_qty_micro: 1000000, price_unit: '小组', price_cents: 200, total_cents: 200 },
    { id: 'decimal-sale', product_id: 'p2', warehouse_id: 'w', product_name: '小数比例售料', unit: '包', conversion_ratio: '2.5', qty_quantum: '2000000', qty_micro: 400000, sale_qty_micro: 1000000, price_unit: '公斤', price_cents: 300, total_cents: 300 },
  ] })
  const before = structuredClone(saved), view = savedOrderWorkView(saved, true)
  assert.deepEqual(view.sales.map(sale => sale.quantity_text), ['1 桶', '1/6 桶', '0.4 包'])
  assert.deepEqual(view.sales.map(sale => sale.sale_quantity_text), ['1 桶', '1 小组', '1 公斤'])
  const html = await render(saved)
  assert.match(html, /实际数量：1 桶/)
  assert.match(html, /实际数量：1\/6 桶/)
  assert.doesNotMatch(html, /实际数量：6 桶|实际数量：0\.166667 桶/)
  assert.deepEqual(saved, before)
})

test('保存微量格式保留6位精度和大整数，非法或缺省不显示成0，未知模式不猜平方米', () => {
  assert.equal(savedQuantityText(1, '米'), '0.000001 米')
  assert.equal(savedQuantityText('9007199254740993123', '㎡'), '9007199254740.993123 ㎡')
  for (const value of [undefined, '', 'bad', NaN, Infinity, 1.5]) assert.equal(savedQuantityText(value, '米'), '未记录')
  const row = rows(savedOrderWorkView(order({ lines: [line('unknown', '新计量单位')] }), true))[0]
  assert.equal(row.quantity_text, '1 新计量单位')
  assert.match(row.dimensions[0].text, /未记录方量明细/)
})

test('保存查看保留家、根、处、孔位、副、张、套单位及计数段原文', () => {
  const modes=['家','根','处','孔位','副','张','套']
  const saved=order({lines:modes.map((mode,index)=>line('count-'+index,mode,{qty_micro:2000000,segments:[{mode,count:2}]}))})
  const actual=rows(savedOrderWorkView(saved,true))
  assert.deepEqual(actual.map(row=>row.quantity_text),modes.map(mode=>'2 '+mode))
  assert.deepEqual(actual.map(row=>row.dimensions[0].text),modes.map(mode=>'数量：2 '+mode))
})

test('保存明细与公共耗材正确分组，1桶6小组精确显示且工资和已删调整记录按权限隔离',async()=>{
 const adjustment={id:'wage-history',line_id:'removed-line',line_deleted:true,worker_name:'历史人员',before_cents:3100,after_cents:4700,reason:'合成加班原因',actor_name:'合成管理员',created_at:'2026-10-11 10:00:00'}
 const saved=order({lines:[line('with-material','平方',{labor_cents:1800,wage_override_cents:1800,wage_adjustments:[{...adjustment,line_id:'with-material',line_deleted:false}]}),line('other','平方')],
  materials:[{id:'line-material',line_id:'with-material',product_id:'p',warehouse_id:'w',product_name:'本项专用耗材',warehouse_name:'实际工人库存',unit:'桶',aux_unit:'小组',conversion_ratio:'6',input_unit:'aux',input_qty:'1',qty_quantum:'1000000',qty_micro:166666},{id:'public-material',line_id:'',product_id:'p2',warehouse_id:'w2',product_name:'公共耗材商品',warehouse_name:'实际总仓',unit:'袋',qty_micro:2000000}],
  wage_records:[{id:'wage-account',worker_name:'历史人员',worker_id:'worker',wage_cents:1800,wage_paid:0}],wage_adjustments:[adjustment]})
 const allowed=savedOrderWorkView(saved,true,{canWages:true});assert.equal(allowed.materials[0].quantity_text,'1/6 桶');assert.equal(allowed.materials[0].line_id,'with-material')
 assert.equal(allowed.wage_adjustments[0].line_deleted,true)
 const allowedHTML=await render(saved,true,true);assert.match(allowedHTML,/本项施工耗材|本项专用耗材|公共施工耗材|公共耗材商品|1\/6 桶|实际工资：18 元|已删除明细|合成加班原因|合成管理员/)
 assert.equal((allowedHTML.match(/本项专用耗材/g)||[]).length,1);assert.equal((allowedHTML.match(/公共耗材商品/g)||[]).length,1);assert.equal((allowedHTML.match(/class="order-total-value"/g)||[]).length,1)
 const hidden=savedOrderWorkView(saved,false);assert.doesNotMatch(JSON.stringify(hidden),/labor_cents|wage_|合成加班原因|合成管理员|4700|3100/)
 assert.doesNotMatch(await render(saved,false,false),/实际工资|工资调整|合成加班原因|合成管理员|已删除明细|class="order-total-value"/)
})

test('真实Vue只读九列每行8格，唯一整单总額来自保存快照，保留部位与人员且没有编辑控件', async () => {
  const saved = order({ lines: allModes(), total_cents: 478888 })
  const html = await render(saved)
  assert.equal((html.match(/role="columnheader"/g) || []).length, 9)
  const renderedRows = html.match(/<section[^>]*class="order-line-row saved-line-row"[^>]*>[\s\S]*?<\/section>/g) || []
  assert.equal(renderedRows.length, 5)
  for (const row of renderedRows) assert.deepEqual([...row.matchAll(/data-label="([^"]+)"/g)].map(match => match[1]), ['施工人员', '工艺', '部位', '单价', '方量明细', '方量小计', '金额小计', '备注'])
  assert.equal((html.match(/class="order-total-value"/g) || []).length, 1)
  assert.ok(html.includes(customerMoney(478888)))
  for (const row of saved.lines) {
    assert.ok(html.includes(row.worker_name))
    assert.ok(html.includes(row.actual_location))
    assert.ok(html.includes(row.subitem_name))
  }
  assert.match(html, /3\.456789 米|3\.456789/)
  assert.doesNotMatch(html, /<input\b|<select\b|<button\b|987654321|876543210/)
})

test('真实Vue无价格权限时金额段/售料/组合均隐藏，整单合计元素完全不进入DOM', async () => {
  const saved = combinationFixture()
  saved.lines.push(line('hidden-amount', '金额', { segments: [{ mode: '金额', amount_cents: 54328765 }], price_cents: 73456987, total_cents: 65349876 }))
  saved.sales.push({ id: 'hidden-sale', product_id: 'p', warehouse_id: 'w', product_name: '保存售料', unit: '桶', qty_micro: 2000000, price_unit: '桶', sale_qty_micro: 2000000, price_cents: 43217654, total_cents: 32106543 })
  saved.total_cents = 21095432
  const before = structuredClone(saved), html = await render(saved, false)
  assert.match(html, /无查看权限/)
  assert.match(html, /跨日期关联|2026-09-20/)
  assert.match(html, /早期厨房|早期保存施工人/)
  assert.doesNotMatch(html, /class="order-total-(?:value|label|row|scroll)"|¥|734,569\.87|653,498\.76|543,287\.65|432,176\.54|321,065\.43|210,954\.32/)
  assert.deepEqual(saved, before)
})

test('真实Vue组合跨日期快照与本地锚点可查看，无元数据提示明确，空表与纯售料只留一个保存总额', async () => {
  const saved = combinationFixture(), comboHTML = await render(saved)
  assert.match(comboHTML, /免收丙纶|本组合免收 40 ㎡/)
  assert.match(comboHTML, /实际 100 ㎡ · 累计免收 40 ㎡ · 剩余收费 60 ㎡/)
  assert.match(comboHTML, /2026-09-20|早期厨房|早期保存施工人/)
  assert.ok(comboHTML.includes('href="#' + savedRowAnchor(saved.id, 'actual-water') + '"'))
  assert.ok(comboHTML.includes('id="' + savedRowAnchor(saved.id, 'actual-water') + '"'))
  delete saved.combination_details
  assert.match(await render(saved), /日期、部位、施工人等快照未返回/)
  const salesOnly = order({ total_cents: 40999, sales: [{ id: 'only-sale', product_id: 'p', warehouse_id: 'w', product_name: '纯售料保存商品', unit: '桶', qty_micro: 2000000, price_unit: '桶', sale_qty_micro: 2000000, price_cents: 76543, total_cents: 31007 }] })
  const html = await render(salesOnly)
  assert.match(html, /没有已保存的施工工艺明细/)
  assert.match(html, /纯售料保存商品/)
  assert.equal((html.match(/class="order-total-value"/g) || []).length, 1)
  assert.ok(html.includes(customerMoney(40999)))
  assert.ok(html.indexOf('aria-label="已保存售料记录"') < html.indexOf('class="order-total-value"'))
  const empty = await render(order({ total_cents: 0 }))
  assert.match(empty, /未使用组合计价/)
  assert.ok(empty.includes(customerMoney(0)))
  assert.doesNotMatch(await render(order({ total_cents: 0 }), false), /class="order-total-value"/)
})


test('公司默认与人工排序只重排保存视图，大类连续且快照量价人员和旧部位不变', () => {
  const saved=order({ lines:[
    line('poly-a','平方',{category:'丙纶',craft_id:'poly',subitem_id:'kitchen',subitem_name:'保存厨房',actual_location:'旧厨房东侧',region:'旧原文',total_cents:9876}),
    line('water-a','平方',{category:'防水涂料',craft_id:'water'}),
    line('start-a','个',{category:'开工',craft_id:'start'}),
    line('poly-b','平方',{category:'丙纶',craft_id:'poly',subitem_id:'bath',subitem_name:'保存主卫'}),
    line('other','米',{category:'室外防水',craft_id:'other'}),
    line('poly-c','平方',{category:'丙纶',craft_id:'poly',subitem_id:'kitchen',subitem_name:'旧厨房另一明细',actual_location:'厨房西侧'}),
  ]})
  const original=structuredClone(saved)
  const defaults=savedOrderWorkView(saved,true)
  assert.deepEqual(defaults.categories.map(c=>c.category),['开工','防水涂料','丙纶','室外防水'])
  const manual=savedOrderWorkView(saved,true,{categoryOrder:['丙纶','防水涂料','开工'],crafts:[{id:'poly',subitems:[{id:'bath'},{id:'kitchen'}]}]})
  assert.deepEqual(manual.categories.map(c=>c.category),['丙纶','防水涂料','开工','室外防水'])
  assert.deepEqual(manual.categories[0].crafts[0].rows.map(r=>r.id),['poly-b','poly-a','poly-c'])
  const projection=rows(manual).find(row=>row.id==='poly-a')
  assert.deepEqual([projection.subitem_name,projection.actual_location,projection.region,projection.worker_name,projection.total_cents],['保存厨房','旧厨房东侧','旧原文','保存施工人-poly-a',9876])
  assert.deepEqual(saved,original)
  assert.equal(manual.total_cents,saved.total_cents)
  const hidden=savedOrderWorkView(saved,false,{categoryOrder:['丙纶','防水涂料','开工']})
  assert.ok(rows(hidden).every(row=>!('total_cents' in row)&&!('price_cents' in row)))
  assert.ok(!('total_cents' in hidden))
})

test('当前小项配置顺序只投影稳定ID，未知旧小项及每条无配置旧行独立保留', async () => {
  const {groupOrderRowsBySubitem}=await import('./orderRowGroups.ts')
  const input=[{id:'old-1',subitem_id:''},{id:'k-1',subitem_id:'k'},{id:'b-1',subitem_id:'b'},{id:'k-2',subitem_id:'k'},{id:'retired',subitem_id:'retired'},{id:'old-2',subitem_id:''}]
  const previous=structuredClone(input)
  const result=groupOrderRowsBySubitem(input,[{id:'b'},{id:'k'},{id:'b'}])
  assert.deepEqual(result.map(row=>row.id),['b-1','k-1','k-2','old-1','retired','old-2'])
  assert.deepEqual(input,previous)
  for(const row of input)assert.ok(result.includes(row))
})

test('配置小项直接出现在保存部位列，独立部位和原region作为兼容原文保留', async () => {
  const html=await render(order({lines:[line('legacy-location','平方',{subitem_name:'主卫',actual_location:'主卫淋浴区',region:'原纸单备注部位'})]}))
  const location=html.match(/data-label="部位"[\s\S]*?<\/div>/)?.[0]||''
  assert.match(location,/主卫<\/strong>/)
  assert.match(location,/原独立部位：主卫淋浴区/)
  assert.match(location,/原部位信息：原纸单备注部位/)
  assert.doesNotMatch(html,/配置小项：|未填写实际部位|<input\b|新增施工部位/)
})


test('保存查看的大类内部遵守当前固定工艺顺序，旧停用或未知工艺稳定追加且快照不改', () => {
  const saved=order({lines:[
    line('old-first','平方',{category:'安全防护架',craft_id:'old'}),
    line('second-first','平方',{category:'安全防护架',craft_id:'second'}),
    line('first-first','平方',{category:'安全防护架',craft_id:'first'}),
    line('old-second','平方',{category:'安全防护架',craft_id:'old'}),
    line('other-category','米',{category:'回填',craft_id:'other'}),
  ]})
  const original=structuredClone(saved)
  const view=savedOrderWorkView(saved,true,{crafts:[{id:'first'},{id:'second'}]})
  assert.deepEqual(view.categories[0].crafts.map(c=>c.craft_id),['first','second','old'])
  assert.deepEqual(view.categories[0].crafts[2].rows.map(r=>r.id),['old-first','old-second'])
  assert.deepEqual(view.categories.map(c=>c.category),['安全防护架','回填'])
  assert.deepEqual(saved,original)
})
