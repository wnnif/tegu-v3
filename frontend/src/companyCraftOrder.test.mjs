import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import { registerHooks } from 'node:module'
import { fileURLToPath } from 'node:url'
import { createRenderer, createSSRApp, h, markRaw, nextTick, reactive, ref } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { parse, compileScript } from '@vue/compiler-sfc'
import ts from 'typescript'
import { DEFAULT_COMPANY_CATEGORY_ORDER, orderedCompanyCategories } from './companyCraftOrder.ts'
import { withoutCustomerPrices } from './customerPrices.ts'
import { effectiveCatalogWage } from './craftWages.ts'

registerHooks({ resolve(specifier, context, nextResolve) {
  if (specifier.startsWith('.') && context.parentURL?.startsWith('file:')) {
    const file = new URL(specifier, context.parentURL)
    if (existsSync(fileURLToPath(file) + '.ts')) return nextResolve(specifier + '.ts', context)
  }
  return nextResolve(specifier, context)
} })
const { useCatalog } = await import('./workflows/useCatalog.ts')
const jsonCopy = value => JSON.parse(JSON.stringify(value))

const compiled = new Map()
async function componentURL(file, inlineTemplate = true) {
  const key = file.href + '#' + inlineTemplate
  if (compiled.has(key)) return compiled.get(key)
  const { descriptor } = parse(await readFile(file, 'utf8'), { filename: fileURLToPath(file) })
  const script = compileScript(descriptor, { id: 'company-order-' + file.pathname + inlineTemplate, inlineTemplate })
  let source = ts.transpileModule(script.content, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
  for (const match of [...source.matchAll(/from\s+(['"])([^'"]+)\1/g)]) {
    const specifier = match[2]
    const url = specifier === 'vue' ? new URL('../node_modules/vue/index.mjs', import.meta.url).href
      : specifier.endsWith('.vue') ? await componentURL(new URL(specifier, file))
      : specifier.startsWith('.') ? new URL(specifier.endsWith('.ts') ? specifier : specifier + '.ts', file).href
      : import.meta.resolve(specifier)
    source = source.replace(match[0], 'from ' + JSON.stringify(url))
  }
  const url = 'data:text/javascript,' + encodeURIComponent(source)
  compiled.set(key, url)
  return url
}
const loadComponent = async (name, inlineTemplate = true) => (await import(await componentURL(new URL('./components/' + name + '.vue', import.meta.url), inlineTemplate))).default
const CompanyCraftOrder = await loadComponent('CompanyCraftOrder')
const EntityEditor = await loadComponent('EntityEditor')
const html = (component, props) => renderToString(createSSRApp(component, props))
const renderedCategories = content => [...content.matchAll(/data-company-category="([^"]*)"/g)].map(match => match[1])
globalThis.Document ??= class Document {}
globalThis.ShadowRoot ??= class ShadowRoot {}
globalThis.document ??= { activeElement: null, addEventListener() {}, removeEventListener() {} }

function memoryHost() {
  let hitTarget = null
  const document = { elementFromPoint: () => hitTarget }
  const node = (type, text = '') => markRaw({
    type, text, props: {}, children: [], parent: null, ownerDocument: document, captured: new Set(),
    tagName: type.toUpperCase(), value: '', listeners: {}, style: {},
    get options() { return this.children.filter(child => child.type === 'option') },
    addEventListener(name, fn) { this.listeners[name] = fn }, getRootNode() { return globalThis.document }, setAttribute() {}, removeAttribute() {},
    setPointerCapture(id) { this.captured.add(id) },
    hasPointerCapture(id) { return this.captured.has(id) },
    releasePointerCapture(id) { this.captured.delete(id) },
    contains(child) { for (let current = child; current; current = current.parent) if (current === this) return true; return false },
    closest() { for (let current = this; current; current = current.parent) if ('data-company-category' in current.props) return current; return null },
    get dataset() { return { companyCategory: this.props['data-company-category'] } },
  })
  function remove(child) {
    if (!child.parent) return
    const index = child.parent.children.indexOf(child)
    if (index >= 0) child.parent.children.splice(index, 1)
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
    setText: (target, text) => { target.text = text }, setElementText: (target, text) => { target.text = text; target.children = [] },
    insert, remove, parentNode: target => target.parent, nextSibling: target => target.parent?.children[target.parent.children.indexOf(target) + 1] || null,
    patchProp: (target, key, previous, value) => { target.props[key] = value; target[key] = value },
    insertStaticContent: (content, parent, anchor) => { const target = node('static', content); insert(target, parent, anchor); return [target, target] },
  })
  const root = node('root')
  const elements = (target = root) => [target, ...target.children.flatMap(child => elements(child))]
  const text = (target = root) => target.text + target.children.map(child => text(child)).join('')
  return { renderer, root, elements, text, hit: target => { hitTarget = target } }
}
function mountOrder(t, initial = {}) {
  const updates = []
  const props = reactive({ available: ['回填', '包管', '开工'], modelValue: [], companyKey: 'company-a', disabled: false, ...initial })
  props['onUpdate:modelValue'] = value => { updates.push([...value]); props.modelValue = value }
  const host = memoryHost(), app = host.renderer.createApp({ setup: () => () => h(CompanyCraftOrder, props) })
  app.mount(host.root)
  let mounted = true
  const unmount = () => { if (mounted) { mounted = false; app.unmount() } }
  t.after(unmount)
  const button = label => host.elements().find(node => node.type === 'button' && node.props['aria-label'] === label)
  const handle = category => button('拖动' + category + '调整顺序，可用上下方向键')
  const row = category => host.elements().find(node => node.props['data-company-category'] === category)
  const reset = () => host.elements().find(node => node.type === 'button' && host.text(node) === '恢复默认顺序')
  const displayed = () => host.elements().filter(node => 'data-company-category' in node.props).map(node => node.props['data-company-category'])
  return { props, updates, host, app, unmount, button, handle, row, reset, displayed }
}
function event(extra = {}) {
  return { prevented: false, preventDefault() { this.prevented = true }, ...extra }
}
function dispatch(handler, value) {
  for (const listener of Array.isArray(handler) ? handler : [handler]) listener(value)
}
function dragEvent() {
  const values = []
  return event({ dataTransfer: { effectAllowed: '', dropEffect: '', setData: (...args) => values.push(args) }, values })
}
function craft(id, category, companyId = 'company-a', extra = {}) {
  return { id, name: '同名防水丙纶工艺', source_id: 'source-' + id, company_id: companyId, active: 1, category, mode: '平方', price_cents: 3200, wage_cents: 700,
    subitems: [{ id: id + '-part', name: '独立配置小项' }], ticket_slot: 1, included_by_craft_id: '', ...extra }
}
function entityProps(permissions, extra = {}) {
  const crafts = [craft('water', '防水涂料'), craft('poly', '丙纶'), craft('outdoor', '室外防水')]
  return {
    editing: { entity: 'companies', id: 'company-a', version: 9, name: '合成公司A', crafts: [], fixed_crafts: crafts, craft_category_order: ['丙纶', '室外防水', '防水涂料'], ticket_mode: 'single', active: true },
    categories: ['开工', '包管', '安全防护架', '回填', '防水涂料', '室外防水', '丙纶', '维修'], references: { companies: [], workers: [], warehouses: [], products: [], crafts },
    busy: false, locked: false, craftCategory: '全部', companyCraftOptions: [], companyFixedVisible: crafts, craftPage: 1, craftTotal: 0,
    can: permission => permissions.includes(permission), hasCompanyCraft: () => true, ...extra,
  }
}
function workflow(permissions = ['customers']) {
  let pending = Promise.resolve()
  const submitted = [], ctx = {
    page: ref('companies'), categories: ref(['开工', '包管', '安全防护架', '回填', '防水涂料', '室外防水', '丙纶', '维修']), category: ref('全部'),
    references: ref({ companies: [], workers: [], warehouses: [], products: [], crafts: [] }), dialog: ref(''), notice: ref(''), error: ref(''),
    can: permission => permissions.includes(permission), perform: fn => { pending = Promise.resolve().then(fn); return pending },
    submit: async (path, payload) => { submitted.push({ path, payload: jsonCopy(payload) }); return {} }, load: async () => {},
  }
  const catalog = useCatalog(ctx, { search: ref(''), catalogStatus: ref('1'), pageNo: ref(1), reload: () => {} })
  return { catalog, ctx, submitted, done: () => pending }
}
function mockCatalogReads(t, companyCrafts, publicCrafts = []) {
  const previous = globalThis.fetch, requests = []
  globalThis.fetch = async input => {
    const url = new URL(input, 'http://synthetic.invalid')
    requests.push(url.pathname + url.search)
    assert.equal(url.pathname.startsWith('/api/reference'), true, 'Only mocked catalog GETs are allowed')
    const payload = url.pathname === '/api/reference' ? { crafts: companyCrafts[url.searchParams.get('company_id')] || [] }
      : { rows: publicCrafts, total: publicCrafts.length, page: 1 }
    return new Response(JSON.stringify(payload), { headers: { 'content-type': 'application/json' } })
  }
  t.after(() => { globalThis.fetch = previous })
  return requests
}

// Pure contract: exact identities, stable order and no source/snapshot mutations.
test('default six exact identities precede other available categories in original configuration order', () => {
  const available = Object.freeze(['维修', '室外防水', '丙纶', '防水涂料', '回填', '安全防护架', '包管', '开工'])
  assert.deepEqual(DEFAULT_COMPANY_CATEGORY_ORDER, ['开工', '包管', '安全防护架', '回填', '防水涂料', '丙纶'])
  assert.deepEqual(orderedCompanyCategories(available), [...DEFAULT_COMPANY_CATEGORY_ORDER, '维修', '室外防水'])
  assert.deepEqual(available, ['维修', '室外防水', '丙纶', '防水涂料', '回填', '安全防护架', '包管', '开工'])
})
test('manual polypropylene-before-waterproof wins; duplicate identities dedup stably without losing available categories', () => {
  const available = Object.freeze(['防水涂料', '室外防水', '丙纶', '丙纶', '维修', '防水涂料'])
  const configured = Object.freeze(['丙纶', '丙纶', '不存在', '防水涂料', '防水涂料'])
  assert.deepEqual(orderedCompanyCategories(available, configured), ['丙纶', '防水涂料', '室外防水', '维修'])
  assert.deepEqual(configured, ['丙纶', '丙纶', '不存在', '防水涂料', '防水涂料'])
})
test('new available categories append after saved manual list, without canonical re-ranking or changing company B', () => {
  const available = ['开工', '室外防水', '回填', '防水涂料', '丙纶', '维修']
  const companyA = Object.freeze(['丙纶', '防水涂料', '回填'])
  const companyB = Object.freeze(['回填', '室外防水', '防水涂料'])
  assert.deepEqual(orderedCompanyCategories(available, companyA), ['丙纶', '防水涂料', '回填', '开工', '室外防水', '维修'])
  assert.deepEqual(orderedCompanyCategories(available, companyB), ['回填', '室外防水', '防水涂料', '开工', '丙纶', '维修'])
  assert.deepEqual(orderedCompanyCategories(['开工', ...available], companyA), orderedCompanyCategories(available, companyA))
})
test('no name/substring inference, trimming or outdoor-waterproof merging; empty and removed categories are projections only', () => {
  const available = ['防水涂料延伸', '工艺名有丙纶', '室外防水', '防水涂料 ', '安全防护架', '丙纶', '回填2', '']
  assert.deepEqual(orderedCompanyCategories(available), ['安全防护架', '丙纶', ...available.filter(item => !['安全防护架', '丙纶'].includes(item))])
  assert.deepEqual(orderedCompanyCategories([], ['丙纶']), [])
  assert.deepEqual(orderedCompanyCategories(['室外防水', '维修'], ['丙纶', '室外防水']), ['室外防水', '维修'])
})

test('company sorter SSR contains real drag handles, meaningful up/down labels, all selected categories and empty guidance', async () => {
  const content = await html(CompanyCraftOrder, { available: ['室外防水', '防水涂料', '丙纶'], modelValue: ['丙纶', '防水涂料'] })
  assert.deepEqual(renderedCategories(content), ['丙纶', '防水涂料', '室外防水'])
  assert.equal((content.match(/draggable="true"/g) || []).length, 3)
  assert.match(content, /aria-label="上移防水涂料"/)
  assert.match(content, /aria-label="下移室外防水"/)
  assert.match(content, /点击表单底部“保存”/)
  assert.match(await html(CompanyCraftOrder, { available: [] }), /尚未选择固定工艺/)
})
test('company maintenance without customer-price permission can sort, prices remain hidden; price permission cannot grant company sorting', async () => {
  const noPrice = await html(EntityEditor, entityProps(['customers']))
  assert.deepEqual(renderedCategories(noPrice), ['丙纶', '室外防水', '防水涂料'])
  assert.equal((noPrice.match(/draggable="true"/g) || []).length, 3)
  assert.match(noPrice, /无查看权限/)
  assert.doesNotMatch(noPrice, /aria-label="同名防水丙纶工艺收费单价"/)
  const priceOnly = await html(EntityEditor, entityProps(['customer_prices']))
  assert.equal((priceOnly.match(/draggable="true"/g) || []).length, 0)
  assert.match(priceOnly, /aria-label="同名防水丙纶工艺收费单价"/)
  for (const extra of [{ busy: true }, { locked: true }]) {
    const disabled = await html(EntityEditor, entityProps(['customers', 'customer_prices'], extra))
    assert.equal((disabled.match(/draggable="true"/g) || []).length, 0)
    assert.match(disabled, /当前不可修改排序/)
  }
})
test('company wage override is separate from customer price and only visible to wage/rate readers', async () => {
  const overridden = craft('poly', '丙纶', 'company-a', { wage_cents: 500, wage_override_cents: 550 })
  const rateEditor = await html(EntityEditor, entityProps(['customers', 'rates'], {
    editing: { ...entityProps(['customers']).editing, fixed_crafts: [overridden] }, companyFixedVisible: [overridden],
  }))
  assert.match(rateEditor, /公共标准工资（元\/平方米）/)
  assert.match(rateEditor, /公司特殊工资覆盖（元\/平方米）/)
  assert.match(rateEditor, /value="5\.5"/)
  assert.match(rateEditor, /仅影响之后新建的施工记录/)

  const customerEditor = await html(EntityEditor, entityProps(['customers', 'customer_prices'], {
    editing: { ...entityProps(['customers']).editing, fixed_crafts: [overridden] }, companyFixedVisible: [overridden],
  }))
  assert.match(customerEditor, /同名防水丙纶工艺收费单价/)
  assert.doesNotMatch(customerEditor, /公司特殊工资覆盖/)

  const retained = withoutCustomerPrices({ fixed_crafts: [{ price_cents: 3000, wage_override_cents: 550 }] })
  assert.deepEqual(retained, { fixed_crafts: [{ wage_override_cents: 550 }] }, 'price redaction must retain the independent wage field')
})
test('new order wage preview prioritizes company override, selected subitem wage, then public default', () => {
  assert.equal(effectiveCatalogWage({ wage_cents: 300 }, { wage_cents: 450 }), 450)
  assert.equal(effectiveCatalogWage({ wage_cents: 300 }, {}), 300)
  assert.equal(effectiveCatalogWage({ wage_cents: 300, wage_override_cents: 550 }, { wage_cents: 450 }), 550)
  assert.equal(effectiveCatalogWage({ wage_cents: 300, wage_override_cents: 0 }, { wage_cents: 450 }), 0)
  assert.equal(effectiveCatalogWage({}), 0)
})
test('actual rendered up/down and keyboard controls reorder only emitted draft configuration, with stable identities', async t => {
  const source = Object.freeze(['回填', '包管', '开工']), initial = Object.freeze(['包管', '开工', '回填'])
  const mounted = mountOrder(t, { available: source, modelValue: initial })
  mounted.button('下移包管').props.onClick()
  await nextTick()
  assert.deepEqual(mounted.displayed(), ['开工', '包管', '回填'])
  dispatch(mounted.handle('回填').props.onKeydown, event({ key: 'ArrowUp' }))
  await nextTick()
  assert.deepEqual(mounted.displayed(), ['开工', '回填', '包管'])
  mounted.button('上移开工').props.onClick()
  assert.equal(mounted.updates.length, 2, 'Boundary controls cannot create spurious mutations')
  assert.deepEqual(source, ['回填', '包管', '开工'])
  assert.deepEqual(initial, ['包管', '开工', '回填'])
  assert.match(mounted.host.text(), /尚未|点击表单底部“保存”后生效/)
})
test('newly selected categories append after the manual order and duplicate craft categories render once', async t => {
  const mounted = mountOrder(t, { available: ['防水涂料', '丙纶', '丙纶'], modelValue: ['丙纶', '防水涂料'] })
  assert.deepEqual(mounted.displayed(), ['丙纶', '防水涂料'])
  mounted.props.available = ['开工', '室外防水', '防水涂料', '丙纶', '丙纶']
  await nextTick()
  assert.deepEqual(mounted.displayed(), ['丙纶', '防水涂料', '开工', '室外防水'])
  assert.deepEqual(mounted.updates, [], 'Selecting a category changes only the projection until an explicit reorder')
  mounted.button('上移室外防水').props.onClick()
  await nextTick()
  assert.deepEqual(mounted.displayed(), ['丙纶', '防水涂料', '室外防水', '开工'])
})
test('native HTML drag/drop commits a real reorder on drop, not on hover; unrelated external drags are ignored', async t => {
  const mounted = mountOrder(t)
  const drag = dragEvent()
  mounted.handle('开工').props.onDragstart(drag)
  assert.deepEqual(drag.values, [['text/plain', '开工']])
  assert.equal(drag.dataTransfer.effectAllowed, 'move')
  const hover = dragEvent()
  mounted.row('回填').props.onDragover(hover)
  await nextTick()
  assert.equal(hover.prevented, true)
  assert.deepEqual(mounted.displayed(), ['开工', '包管', '回填'])
  assert.equal(mounted.updates.length, 0)
  mounted.row('回填').props.onDrop(dragEvent())
  await nextTick()
  assert.deepEqual(mounted.displayed(), ['包管', '回填', '开工'])
  mounted.row('包管').props.onDrop(dragEvent())
  assert.equal(mounted.updates.length, 1)
})
test('touch/pen pointer capture targets actual rows, releases capture and cancels without mutations', async t => {
  const mounted = mountOrder(t), handle = mounted.handle('开工')
  const pointer = extra => event({ pointerType: 'touch', pointerId: 71, button: 0, currentTarget: handle, clientX: 10, clientY: 10, ...extra })
  handle.props.onPointerdown(pointer())
  assert.equal(handle.hasPointerCapture(71), true)
  mounted.host.hit(mounted.row('回填'))
  handle.props.onPointermove(pointer())
  handle.props.onPointerup(pointer())
  await nextTick()
  assert.deepEqual(mounted.displayed(), ['包管', '回填', '开工'])
  assert.equal(handle.hasPointerCapture(71), false)
  const second = mounted.handle('包管')
  second.props.onPointerdown(pointer({ pointerType: 'pen', pointerId: 72, currentTarget: second }))
  second.props.onPointercancel(pointer({ pointerId: 72, currentTarget: second }))
  second.props.onPointerup(pointer({ pointerId: 72, currentTarget: second }))
  assert.equal(mounted.updates.length, 1)
  assert.equal(second.hasPointerCapture(72), false)
})
test('only one primary pointer can own the drag; disabling or unmounting releases capture without a reorder', async t => {
  const mounted = mountOrder(t), first = mounted.handle('开工'), second = mounted.handle('包管')
  const pointer = (id, target, extra = {}) => event({ pointerType: 'touch', pointerId: id, button: 0, currentTarget: target, ...extra })
  first.props.onPointerdown(pointer(1, first))
  second.props.onPointerdown(pointer(2, second, { isPrimary: false }))
  second.props.onPointerdown(pointer(3, second))
  assert.equal(first.hasPointerCapture(1), true)
  assert.equal(second.captured.size, 0)
  mounted.props.disabled = true
  await nextTick()
  assert.equal(first.hasPointerCapture(1), false)
  first.props.onPointerup(pointer(1, first))
  assert.deepEqual(mounted.updates, [])
  mounted.props.disabled = false
  await nextTick()
  first.props.onPointerdown(pointer(4, first))
  mounted.unmount()
  assert.equal(first.hasPointerCapture(4), false)
  assert.deepEqual(mounted.updates, [])
})
test('restore requires explicit confirmation; cancel keeps draft, confirm emits [] and default does not save by itself', async t => {
  const previous = globalThis.confirm
  let accepted = false, confirmations = 0
  globalThis.confirm = message => { confirmations++; assert.match(message, /本公司的默认工艺大类顺序/); return accepted }
  t.after(() => { if (previous === undefined) delete globalThis.confirm; else globalThis.confirm = previous })
  const mounted = mountOrder(t, { modelValue: ['回填', '包管', '开工'] })
  mounted.reset().props.onClick()
  assert.equal(confirmations, 1)
  assert.deepEqual(mounted.props.modelValue, ['回填', '包管', '开工'])
  assert.equal(mounted.updates.length, 0)
  accepted = true
  mounted.reset().props.onClick()
  await nextTick()
  assert.deepEqual(mounted.props.modelValue, [])
  assert.deepEqual(mounted.displayed(), ['开工', '包管', '回填'])
  assert.deepEqual(mounted.updates, [[]])
})
test('disabled/locked guard rejects forged events and permission revocation during a drag', async t => {
  const mounted = mountOrder(t, { disabled: true, modelValue: ['回填', '包管', '开工'] })
  mounted.button('上移包管').props.onClick()
  const blocked = dragEvent()
  mounted.handle('回填').props.onDragstart(blocked)
  mounted.row('开工').props.onDrop(dragEvent())
  mounted.reset().props.onClick()
  assert.equal(blocked.prevented, true)
  assert.deepEqual(mounted.updates, [])
  mounted.props.disabled = false
  await nextTick()
  mounted.handle('回填').props.onDragstart(dragEvent())
  mounted.props.disabled = true
  await nextTick()
  mounted.row('开工').props.onDrop(dragEvent())
  assert.deepEqual(mounted.updates, [])
})
test('same sorter component switches companies without carrying an in-progress drag or mutating company A', async t => {
  const first = Object.freeze(['回填', '包管', '开工'])
  const mounted = mountOrder(t, { modelValue: first })
  mounted.handle('回填').props.onDragstart(dragEvent())
  mounted.props.companyKey = 'company-b'
  mounted.props.modelValue = []
  await nextTick()
  mounted.row('开工').props.onDrop(dragEvent())
  assert.deepEqual(mounted.displayed(), ['开工', '包管', '回填'])
  assert.deepEqual(mounted.updates, [])
  assert.deepEqual(first, ['回填', '包管', '开工'])
})
test('form write guard checks company-maintenance permission independently of prices, busy and locked states', async t => {
  const EntityScript = await loadComponent('EntityEditor', false)
  // Script harness checks the actual parent mutation guard; SSR above checks its real template and child.
  EntityScript.render = () => h('div')
  let permissions = ['customers']
  const props = reactive(entityProps(permissions))
  props.can = permission => permissions.includes(permission)
  const before = JSON.stringify(props.editing.fixed_crafts)
  const host = memoryHost(), app = host.renderer.createApp({ setup: () => () => h(EntityScript, props) }), instance = app.mount(host.root)
  t.after(() => app.unmount())
  const apply = order => instance.$.subTree.component.setupState.changeCompanyCategoryOrder(order)
  apply(['防水涂料', '丙纶', '室外防水'])
  assert.deepEqual(props.editing.craft_category_order, ['防水涂料', '丙纶', '室外防水'])
  permissions = ['customer_prices']
  apply(['丙纶'])
  assert.deepEqual(props.editing.craft_category_order, ['防水涂料', '丙纶', '室外防水'])
  permissions = ['customers']
  props.busy = true
  await nextTick()
  apply([])
  props.busy = false
  props.locked = true
  await nextTick()
  apply([])
  assert.deepEqual(props.editing.craft_category_order, ['防水涂料', '丙纶', '室外防水'])
  props.locked = false
  await nextTick()
  apply([])
  assert.deepEqual(props.editing.craft_category_order, [])
  assert.equal(JSON.stringify(props.editing.fixed_crafts), before)
})
test('catalog projection groups exact categories contiguously and preserves craft/source/subitem order and values', () => {
  const { catalog } = workflow()
  const crafts = [craft('water-a', '防水涂料'), craft('poly-a', '丙纶'), craft('water-b', '防水涂料'), craft('outside', '室外防水')]
  catalog.catalogDraft.value.fixed_crafts = crafts
  catalog.catalogDraft.value.craft_category_order = ['丙纶', '防水涂料']
  const before = JSON.stringify(catalog.catalogDraft.value.fixed_crafts)
  assert.deepEqual(catalog.companyFixedVisible.value.map(item => item.source_id), ['source-poly-a', 'source-water-a', 'source-water-b', 'source-outside'])
  catalog.craftCategory.value = '防水涂料'
  assert.deepEqual(catalog.companyFixedVisible.value.map(item => item.id), ['water-a', 'water-b'])
  assert.equal(JSON.stringify(catalog.catalogDraft.value.fixed_crafts), before)
})
test('company A/B/new drafts clone their own saved order and save version/config through the existing form without hidden prices', async t => {
  const a = { id: 'company-a', name: '合成A', version: 11, craft_category_order: ['丙纶', '防水涂料'] }
  const b = { id: 'company-b', name: '合成B', version: 7 }
  const rows = { 'company-a': [craft('a-water', '防水涂料'), craft('a-poly', '丙纶')], 'company-b': [craft('b-poly', '丙纶', b.id), craft('b-water', '防水涂料', b.id)] }
  mockCatalogReads(t, rows)
  const { catalog, submitted, done } = workflow(['customers'])
  catalog.editEntity(a, 'companies')
  await done()
  assert.deepEqual(catalog.companyFixedVisible.value.map(item => item.category), ['丙纶', '防水涂料'])
  catalog.catalogDraft.value.craft_category_order.reverse()
  await catalog.saveEntity()
  assert.deepEqual(a.craft_category_order, ['丙纶', '防水涂料'])
  assert.equal(submitted[0].path, '/entity')
  assert.equal(submitted[0].payload.version, 11)
  assert.deepEqual(submitted[0].payload.craft_category_order, ['防水涂料', '丙纶'])
  assert.equal('price_cents' in submitted[0].payload.fixed_crafts[0], false)
  assert.equal(submitted[0].payload.fixed_crafts[0].wage_cents, 700)
  catalog.editEntity(b, 'companies')
  await done()
  assert.deepEqual(catalog.catalogDraft.value.craft_category_order, [])
  assert.deepEqual(catalog.companyFixedVisible.value.map(item => item.category), ['防水涂料', '丙纶'])
  await catalog.saveEntity()
  assert.deepEqual(submitted[1].payload.craft_category_order, [])
  assert.equal(submitted[1].payload.id, 'company-b')
  catalog.editEntity(undefined, 'companies')
  await done()
  assert.equal(catalog.catalogDraft.value.id, '')
  assert.deepEqual(catalog.catalogDraft.value.craft_category_order, [])
  assert.deepEqual(catalog.catalogDraft.value.fixed_crafts, [])
  catalog.resetCatalog()
  assert.deepEqual(catalog.catalogDraft.value.craft_category_order, [])
})
test('failed/conflicting form save retains unsaved sorting and fixed craft details', async t => {
  mockCatalogReads(t, { 'company-a': [craft('water', '防水涂料'), craft('poly', '丙纶')] })
  const { catalog, ctx, done } = workflow(['customers'])
  catalog.editEntity({ id: 'company-a', name: '合成A', version: 4 }, 'companies')
  await done()
  catalog.catalogDraft.value.craft_category_order = ['丙纶', '防水涂料']
  const before = JSON.stringify(catalog.catalogDraft.value)
  ctx.submit = async () => { throw new Error('版本冲突') }
  // useCatalog captures submit at creation, so use an explicit fresh failing workflow instead.
  const failingContext = { ...ctx, perform: async fn => fn() }
  const failingCatalog = useCatalog(failingContext, { search: ref(''), catalogStatus: ref('1'), pageNo: ref(1), reload: () => {} })
  failingCatalog.catalogDraft.value = jsonCopy(catalog.catalogDraft.value)
  await assert.rejects(failingCatalog.saveEntity(), /版本冲突/)
  assert.equal(JSON.stringify(failingCatalog.catalogDraft.value), before)
  assert.equal(ctx.dialog.value, 'entity')
})

function mountFoundationCompany(t, result, extra = {}) {
  const { catalog } = result, props = reactive({ ...entityProps(['customers', 'customer_prices', 'rates']), ...extra })
  const host = memoryHost(), app = host.renderer.createApp({ render: () => h(EntityEditor, {
    ...props, editing: catalog.catalogDraft.value, companyFixedVisible: catalog.companyFixedVisible.value,
    companyCraftOptions: catalog.companyCraftOptions.value, hasCompanyCraft: catalog.hasCompanyCraft,
    onRemoveCraft: catalog.removeCompanyCraft, onToggleCraft: catalog.toggleCompanyCraft, onSave: catalog.saveEntity,
  }) })
  app.mount(host.root); t.after(() => app.unmount())
  const button = label => host.elements().find(node => node.type === 'button' && node.props['aria-label'] === label)
  return { props, host, button }
}

test('V3 公司真实同类上下移保存完整数组顺序，保留收费工资部位与其他公司配置', async t => {
  const first = craft('water-a', '防水涂料', 'company-a', { name: '防水A', price_cents: 2000, wage_cents: 500 }),
    second = craft('water-b', '防水涂料', 'company-a', { name: '防水B', price_cents: 2500, wage_cents: 500 }),
    pipe = craft('pipe', '包管', 'company-a', { name: '包管' })
  const rows = { 'company-a': [first, pipe, second], 'company-b': [craft('b', '防水涂料', 'company-b')] }, original = jsonCopy(rows)
  mockCatalogReads(t, rows)
  const result = workflow(['customers', 'customer_prices', 'rates']); result.catalog.editEntity({ id: 'company-a', version: 3, name: '合成甲公司' }, 'companies'); await result.done()
  const ui = mountFoundationCompany(t, result)
  assert.equal(ui.button('上移工艺防水A').props.disabled, true)
  ui.button('上移工艺防水B').props.onClick(); await nextTick()
  assert.deepEqual(result.catalog.catalogDraft.value.fixed_crafts.map(item => item.id), ['water-b', 'pipe', 'water-a'])
  assert.deepEqual(result.catalog.companyFixedVisible.value.filter(item => item.category === '防水涂料').map(item => item.id), ['water-b', 'water-a'])
  await result.catalog.saveEntity()
  assert.deepEqual(result.submitted[0].payload.fixed_crafts.map(item => item.id), ['water-b', 'pipe', 'water-a'])
  for (const saved of result.submitted[0].payload.fixed_crafts) {
    const source = rows['company-a'].find(item => item.id === saved.id)
    for (const key of ['price_cents', 'wage_cents', 'source_id', 'subitems']) assert.deepEqual(saved[key], source[key], key)
  }
  assert.deepEqual(rows, original)
})

test('V3 类内排序需要公司维护权限，忙碌或锁定时真实按钮与处理器均拒绝修改', async t => {
  mockCatalogReads(t, { 'company-a': [craft('a', '防水涂料', 'company-a', { name: 'A' }), craft('b', '防水涂料', 'company-a', { name: 'B' })] })
  const result = workflow(); result.catalog.editEntity({ id: 'company-a', version: 1, name: '合成公司' }, 'companies'); await result.done()
  const ui = mountFoundationCompany(t, result, { can: permission => permission === 'customer_prices' }), before = JSON.stringify(result.catalog.catalogDraft.value.fixed_crafts)
  assert.equal(ui.button('上移工艺B').props.disabled, true); ui.button('上移工艺B').props.onClick(); assert.equal(JSON.stringify(result.catalog.catalogDraft.value.fixed_crafts), before)
  ui.props.can = permission => permission === 'customers'
  for (const state of ['busy', 'locked']) {
    ui.props[state] = true; await nextTick(); assert.equal(ui.button('上移工艺B').props.disabled, true)
    ui.button('上移工艺B').props.onClick(); assert.equal(JSON.stringify(result.catalog.catalogDraft.value.fixed_crafts), before)
    ui.props[state] = false
  }
  await nextTick(); ui.button('上移工艺B').props.onClick(); await nextTick()
  assert.deepEqual(result.catalog.catalogDraft.value.fixed_crafts.map(item => item.id), ['b', 'a'])
})

test('V3 公司移除后重选保留固定ID版本已填价与原位置，停用项重选也复用原身份', async t => {
  const source = craft('source-water', '防水涂料', '', { name: '公共防水', source_id: '', wage_cents: 500 }),
    existing = craft('company-water', '防水涂料', 'company-a', { name: '公共防水', source_id: source.id, price_cents: 2000, version: 9 }),
    inactiveSource = craft('source-old', '防水涂料', '', { name: '公共旧工艺', source_id: '' }),
    inactive = craft('company-old', '防水涂料', 'company-a', { source_id: inactiveSource.id, active: 0, price_cents: 3000, version: 5 })
  const company = { id: 'company-a', name: '甲公司', version: 1 }
  mockCatalogReads(t, { 'company-a': [existing, craft('pipe', '包管'), inactive] }, [source, inactiveSource])
  const result = workflow(['customers', 'customer_prices', 'rates']); result.catalog.editEntity(company, 'companies'); await result.done()
  const ui = mountFoundationCompany(t, result), item = result.catalog.catalogDraft.value.fixed_crafts[0]; item.price_cents = 2300
  const article = ui.host.elements().find(node => node.type === 'article' && ui.host.text(node).includes('公共防水'))
  const remove = ui.host.elements(article).find(node => node.type === 'button' && ui.host.text(node) === '移除'); remove.props.onClick(); await nextTick()
  assert.equal(result.catalog.hasCompanyCraft(source), false)
  const option = ui.host.elements().find(node => node.type === 'label' && String(node.props.class).includes('company-craft-option') && ui.host.text(node).includes('公共防水'))
  ui.host.elements(option).find(node => node.tagName === 'INPUT').props.onChange({ target: { checked: true } }); await nextTick()
  const restored = result.catalog.catalogDraft.value.fixed_crafts[0]
  assert.equal(restored.id, existing.id); assert.equal(restored.version, 9); assert.equal(restored.price_cents, 2300); assert.equal(restored.source_id, source.id); assert.equal(restored.wage_cents, 500)
  result.catalog.toggleCompanyCraft(inactiveSource, true)
  const reactivated = result.catalog.catalogDraft.value.fixed_crafts.find(item => item.source_id === inactiveSource.id)
  assert.equal(reactivated.id, inactive.id); assert.equal(reactivated.version, 5); assert.equal(reactivated.price_cents, 3000)
  result.catalog.toggleCompanyCraft(source, true)
  assert.equal(result.catalog.catalogDraft.value.fixed_crafts.filter(item => item.source_id === source.id).length, 1)
  await result.catalog.saveEntity(); assert.equal(result.submitted[0].payload.fixed_crafts[0].id, existing.id)
})

test('V3 多项按不同次序取消重选不打乱公司原工艺顺序，切公司不带入前公司缓存', async t => {
  const sources = ['a', 'b', 'c'].map(id => craft('source-' + id, '防水涂料', '', { source_id: '' }))
  const fixed = sources.map((source, index) => craft('fixed-' + index, '防水涂料', 'company-a', { source_id: source.id }))
  mockCatalogReads(t, { 'company-a': fixed, 'company-b': [] }, sources)
  const result = workflow(['customers', 'customer_prices'])
  for (const removeOrder of [[0, 1], [1, 0]]) for (const selectOrder of [[0, 1], [1, 0]]) {
    result.catalog.editEntity({ id: 'company-a', name: '甲公司', version: 1 }, 'companies'); await result.done()
    for (const index of removeOrder) result.catalog.toggleCompanyCraft(sources[index], false)
    for (const index of selectOrder) result.catalog.toggleCompanyCraft(sources[index], true)
    assert.deepEqual(result.catalog.catalogDraft.value.fixed_crafts.map(item => item.id), fixed.map(item => item.id))
  }
  result.catalog.toggleCompanyCraft(sources[0], false)
  result.catalog.editEntity({ id: 'company-b', name: '乙公司', version: 1 }, 'companies'); await result.done()
  result.catalog.toggleCompanyCraft(sources[0], true)
  assert.equal(result.catalog.catalogDraft.value.fixed_crafts[0].id, '')
  assert.equal(result.catalog.catalogDraft.value.fixed_crafts[0].company_id, 'company-b')
})
