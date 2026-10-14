<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch, watchEffect } from 'vue'
import { api, SessionChangedError } from '../api'
import { customerMoney } from '../customerPrices'
import { combinationAreaMicro, combinationCharge, combinationLineRef, combinationTarget, combinationValidation, combinationWaivers, localCombinationCandidates, quantityDecimal } from '../orderCombinations'
import type { CombinationCandidate, CombinationKind, CombinationPage, ProjectCombination } from '../orderCombinations'
import type { OrderDraft, References } from '../workflows/models'

const props = defineProps<{ draft: OrderDraft; references: References; can: (permission: string) => boolean; busy: boolean; locked: boolean }>()
const original = ref<ProjectCombination[]>([]), savedWaivers = ref(new Map<string, number>())
const cache = ref(new Map<string, CombinationCandidate>()), currentPage = ref<CombinationCandidate[]>([])
const page = ref(1), total = ref(0), loading = ref(false), loadError = ref(''), attempted = ref(false), serverVersion = ref<number | null>(null)
let generation = 0, request = 0, keySequence = 0
const rows = computed(() => props.draft.combinations || [])
const enabled = computed(() => props.draft.combination_enabled ?? rows.value.length > 0)
const local = computed(() => localCombinationCandidates(props.draft, savedWaivers.value, id => props.references.workers.find(worker => worker.id === id)?.name || '未选择施工人'))
const localRefs = computed(() => new Set(local.value.map(candidate => candidate.id)))
const candidates = computed(() => {
  const all = new Map(cache.value)
  for (const candidate of local.value) all.set(candidate.id, candidate)
  return [...all.values()]
})
const byId = computed(() => new Map(candidates.value.map(candidate => [candidate.id, candidate])))
const preview = computed(() => combinationWaivers(original.value, rows.value, candidates.value))
const linkedBlocked = computed(() => original.value.some(combo => [combo.waterproof_line_id, combo.polypropylene_line_id].some(id => !byId.value.get(id)?.editable)))
const mayEdit = computed(() => props.can('combo_pricing') && !props.busy && !props.locked && !linkedBlocked.value && !loading.value)
const validation = computed(() => {
  if (props.draft.combination_dirty && serverVersion.value !== null && serverVersion.value !== (props.draft.combination_version || 0)) return '工地组合已被其他人修改，请重新打开核对；当前草稿尚未保存'
  return combinationValidation(rows.value, original.value, candidates.value, localRefs.value, enabled.value)
})
const displayError = computed(() => loadError.value || ((props.draft.combination_dirty || attempted.value) ? validation.value : ''))
const selectedCandidates = computed(() => [...new Set(rows.value.flatMap(combo => [combo.waterproof_line_id, combo.polypropylene_line_id]))].flatMap(id => byId.value.has(id) ? [byId.value.get(id)!] : []))
function options(kind: CombinationKind): CombinationCandidate[] {
  const ids = new Set([...local.value.map(candidate => candidate.id), ...currentPage.value.map(candidate => candidate.id), ...rows.value.flatMap(combo => [combo.waterproof_line_id, combo.polypropylene_line_id])])
  return candidates.value.filter(candidate => candidate.kind === kind && ids.has(candidate.id))
}
function candidateLabel(candidate: CombinationCandidate): string {
  return `${localRefs.value.has(candidate.id) ? '本次' : candidate.work_date + ' · ' + candidate.number} · ${candidate.name} · ${candidate.subitem_name || candidate.region || '无小项'} · ${candidate.actual_location || '未填部位'} · ${candidate.worker_name || '未选施工人'} · ${quantityDecimal(candidate.qty_micro)}㎡`
}
function markChanged() {
  props.draft.combination_dirty = true
  for (const combo of rows.value) combo.waived_qty_micro = combinationAreaMicro(combo) || 0
}
function addRelation() {
  if (!mayEdit.value || rows.value.length >= 200) return
  props.draft.combination_enabled = true
  ;(props.draft.combinations ||= []).push({ ui_key: 'combo-draft-' + ++keySequence, waterproof_line_id: '', polypropylene_line_id: '', waive_direction: '', input_area: '', waived_qty_micro: 0, waterproof_order_version: props.draft.version, polypropylene_order_version: props.draft.version })
  markChanged()
}
function toggle(checked: boolean) {
  if (!mayEdit.value) return
  props.draft.combination_enabled = checked
  if (!checked) props.draft.combinations = []
  else if (!rows.value.length) addRelation()
  markChanged()
}
function selectLine(combo: ProjectCombination, kind: CombinationKind, id: string) {
  const candidate = byId.value.get(id)
  if (kind === 'waterproof') {
    combo.waterproof_line_id = id
    combo.waterproof_order_version = candidate?.order_version || 0
  } else {
    combo.polypropylene_line_id = id
    combo.polypropylene_order_version = candidate?.order_version || 0
  }
  markChanged()
}
function removeRelation(index: number) {
  if (!mayEdit.value) return
  props.draft.combinations!.splice(index, 1)
  markChanged()
}
function availableFor(combo: ProjectCombination): number {
  const target = combinationTarget(combo), candidate = byId.value.get(target)
  return candidate ? Math.max(0, candidate.qty_micro - (preview.value.get(target) || 0) + (combinationAreaMicro(combo) || 0)) : 0
}
function money(candidate: CombinationCandidate): string {
  return customerMoney(combinationCharge(candidate, preview.value.get(candidate.id) || 0), props.can('customer_prices'))
}
function accept(result: CombinationPage, selected: OrderDraft, token: number): boolean {
  if (token !== generation || props.draft !== selected || selected.project_id !== result.project_id) return false
  for (const candidate of result.rows) cache.value.set(candidate.id, candidate)
  serverVersion.value = result.combination_version
  // A new form starts at the current project version, but never advance a dirty
  // or existing form past a concurrent change without the user's re-open.
  if (!selected.id && !selected.combination_dirty) selected.combination_version = result.combination_version
  return true
}
async function loadPage(next = 1) {
  const selected = props.draft, token = generation, active = ++request
  if (!selected.project_id) return
  loading.value = true; loadError.value = ''
  try {
    const result = await api<CombinationPage>('/project-combinations?' + new URLSearchParams({ project_id: selected.project_id, page: String(next) }))
    if (active !== request || !accept(result, selected, token)) return
    currentPage.value = result.rows.filter(candidate => candidate.order_id !== selected.id)
    total.value = result.total; page.value = next
    // Pin old linked dates, even when they are outside the current candidate page.
    const missing = [...new Set(original.value.flatMap(combo => [combo.waterproof_line_id, combo.polypropylene_line_id]))].filter(id => !byId.value.has(id))
    for (let start = 0; start < missing.length; start += 50) {
      const related = await api<CombinationPage>('/project-combinations?' + new URLSearchParams({ project_id: selected.project_id, line_ids: missing.slice(start, start + 50).join(',') }))
      if (active !== request || !accept(related, selected, token)) return
    }
  } catch (error) {
    if (token === generation && active === request && !(error instanceof SessionChangedError)) loadError.value = (error as Error).message
  } finally {
    if (token === generation && active === request) loading.value = false
  }
}
watch(() => [props.draft, props.draft.project_id, props.draft.company_id], () => {
  generation++; request++; cache.value = new Map(); currentPage.value = []; loading.value = false; loadError.value = ''; attempted.value = false; serverVersion.value = null; page.value = 1; total.value = 0
  original.value = (props.draft.combinations || []).map(combo => ({ ...combo }))
  savedWaivers.value = new Map(props.draft.lines.map(line => [combinationLineRef(line), Number(line.waived_qty_micro || 0)]))
  props.draft.combinations ||= []
  props.draft.combination_enabled ??= original.value.length > 0
  props.draft.combination_version ??= 0
  if (props.draft.project_id && (props.can('combo_pricing') || original.value.length)) void loadPage()
}, { immediate: true })
watchEffect(() => {
  // Projection is for display only. Go independently computes every charge.
  for (const line of props.draft.lines) {
    const waived = preview.value.get(combinationLineRef(line)) || 0
    if (Number(line.waived_qty_micro || 0) !== waived) line.waived_qty_micro = waived
  }
  props.draft.combination_error = validation.value
})
onBeforeUnmount(() => { generation++; request++ })
function validate(): boolean {
  if (!props.draft.combination_dirty) return true
  attempted.value = true
  return !validation.value && !loadError.value && !loading.value
}
defineExpose({ validate })
</script>
<template>
  <section class="combination-editor" aria-label="防水与丙纶组合计价">
    <div class="combination-heading"><h3>防水与丙纶组合计价</h3><label class="check"><input type="checkbox" :checked="enabled" :disabled="!mayEdit" aria-label="开启防水丙纶组合" @change="toggle(($event.target as HTMLInputElement).checked)">开启组合</label><strong>{{enabled ? '按明确选择的面积免收' : '不组合 · 各项正常收费'}}</strong></div>
    <p class="hint">默认不组合；必须明确选择实际明细、免收方向并手动填写面积。工资始终按两项完整实际施工量、各自施工人和工资配置计算，不扣免收量。</p>
    <p v-if="!can('combo_pricing')" class="hint">没有组合免收权限，不能更改收费；须由系统管理员明确授权。普通施工信息仍按原工艺权限填写。</p>
    <p v-else-if="linkedBlocked" class="notice">现有关联明细不可直接修改（可能已交票、收款、结算或没有双方工艺填写权），组合保持原样。</p>
    <template v-if="enabled">
      <div v-if="draft.project_id" class="candidate-pager"><span>{{loading ? '正在读取同工地实际施工明细…' : `同工地候选 ${total} 条 · 第 ${page} 页`}}</span><button type="button" :disabled="busy||loading||page<=1" @click="loadPage(page-1)">前一页候选</button><button type="button" :disabled="busy||loading||page*50>=total" @click="loadPage(page+1)">后一页候选</button><button type="button" :disabled="busy||loading" @click="loadPage(page)">刷新候选</button></div>
      <p v-else class="hint">首次地址可以选择本次已填写的两项；关联其他日期时先选择同公司的原工地。</p>
      <article v-for="(combo,index) in rows" :key="combo.id||combo.ui_key||index" class="combination-row" :data-combination="index">
        <div class="combination-inputs">
          <label>防水实际施工明细<select :value="combo.waterproof_line_id" :disabled="!mayEdit" :aria-label="`第${index+1}组防水明细`" @change="selectLine(combo,'waterproof',($event.target as HTMLSelectElement).value)"><option value="">请明确选择防水明细</option><option v-for="candidate in options('waterproof')" :key="candidate.id" :value="candidate.id" :disabled="!candidate.editable">{{candidateLabel(candidate)}}{{candidate.blocked_reason ? ' · '+candidate.blocked_reason : ''}}</option></select></label>
          <label>丙纶实际施工明细<select :value="combo.polypropylene_line_id" :disabled="!mayEdit" :aria-label="`第${index+1}组丙纶明细`" @change="selectLine(combo,'polypropylene',($event.target as HTMLSelectElement).value)"><option value="">请明确选择丙纶明细</option><option v-for="candidate in options('polypropylene')" :key="candidate.id" :value="candidate.id" :disabled="!candidate.editable">{{candidateLabel(candidate)}}{{candidate.blocked_reason ? ' · '+candidate.blocked_reason : ''}}</option></select></label>
          <label>免收方向<select v-model="combo.waive_direction" :disabled="!mayEdit" :aria-label="`第${index+1}组免收方向`" @change="markChanged"><option value="">请明确选择</option><option value="polypropylene">丙纶免收，防水正常收费</option><option value="waterproof">防水免收，丙纶正常收费</option></select></label>
          <label>手动免收面积（㎡）<input :value="combo.input_area ?? quantityDecimal(combo.waived_qty_micro)" type="number" min="0.000001" step="0.000001" :max="quantityDecimal(availableFor(combo))" :disabled="!mayEdit" :aria-label="`第${index+1}组免收面积`" @input="combo.input_area=($event.target as HTMLInputElement).value;markChanged()"><small>本组剩余可分配 {{quantityDecimal(availableFor(combo))}}㎡</small></label>
        </div>
        <button type="button" :disabled="!mayEdit" @click="removeRelation(index)">移除此组合</button>
      </article>
      <button type="button" :disabled="!mayEdit||rows.length>=200" @click="addRelation">＋ 添加明确关联</button>
      <div class="combination-summaries">
        <article v-for="candidate in selectedCandidates" :key="candidate.id" class="combination-summary" :data-combination-line="candidate.id"><strong>{{candidate.name}} · {{candidate.actual_location||candidate.subitem_name||candidate.region||'未填部位'}}</strong><small>{{candidate.work_date}} · {{candidate.worker_name||'未选施工人'}}{{localRefs.has(candidate.id)?' · 本次施工':''}}</small><div>实际 {{quantityDecimal(candidate.qty_micro)}}㎡ · 免收 {{quantityDecimal(preview.get(candidate.id)||0)}}㎡ · 剩余收费 {{quantityDecimal(Math.max(0,candidate.qty_micro-(preview.get(candidate.id)||0)))}}㎡</div><div>客户收费金额：<strong>{{money(candidate)}}</strong></div><small v-if="candidate.blocked_reason">{{candidate.blocked_reason}}</small></article>
      </div>
    </template>
    <p v-if="displayError" class="error" role="alert">{{displayError}} <button v-if="loadError&&draft.project_id" type="button" :disabled="busy||loading" @click="loadPage(page)">重试读取</button></p>
    <p class="hint">保存本次施工时一并保存组合。其他日期记录保留真实日期、部位、施工量与人员；已交票、有效收款或工资结算的记录不能直接追溯免收。</p>
  </section>
</template>
<style scoped>
.combination-editor{min-width:0;border:1px solid #cddceb;border-radius:8px;padding:14px;background:#f7faff}.combination-heading,.candidate-pager{display:flex;align-items:center;flex-wrap:wrap;gap:12px}.combination-heading h3{margin:0}.combination-heading strong{font-size:13px;color:#355879}.hint{font-size:13px;color:#61758f;line-height:1.6;margin:8px 0}.candidate-pager{margin:10px 0;font-size:13px}.combination-row{border:1px solid #d6e2ee;background:#fff;border-radius:6px;padding:12px;margin:10px 0}.combination-inputs{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.combination-inputs label{min-width:0;display:flex;flex-direction:column;gap:5px}.combination-inputs input,.combination-inputs select{width:100%;min-width:0}.combination-inputs small,.combination-summary small{color:#60758c}.combination-row>button{margin-top:10px}.combination-summaries{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;margin-top:12px}.combination-summary{min-width:0;background:#fff;border:1px solid #d6e2ee;border-radius:6px;padding:10px;display:flex;flex-direction:column;gap:6px;overflow-wrap:anywhere;font-size:13px}.combination-summary strong{color:#244768}.error{margin:10px 0}.notice{font-size:13px;margin:8px 0}@media(max-width:800px){.combination-editor{padding:10px}.combination-inputs,.combination-summaries{grid-template-columns:minmax(0,1fr)}.candidate-pager{gap:8px}.combination-heading{gap:8px}.combination-heading strong{width:100%}}
</style>
