<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { orderedCompanyCategories } from '../companyCraftOrder'

const props = withDefaults(defineProps<{
  available: readonly string[]
  modelValue?: readonly string[]
  companyKey?: string
  disabled?: boolean
}>(), { modelValue: () => [], companyKey: '', disabled: false })
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const categories = computed(() => orderedCompanyCategories(props.available, props.modelValue))
const root = ref<HTMLElement | null>(null)
const dragging = ref<string | null>(null)
const dropTarget = ref<string | null>(null)
const announcement = ref('')
let pointerId: number | null = null
let pointerHandle: HTMLElement | null = null

function clearDrag() {
  // Clear first: releasing capture can synchronously fire lostpointercapture.
  const id = pointerId, handle = pointerHandle
  pointerId = null
  pointerHandle = null
  dragging.value = null
  dropTarget.value = null
  if (id !== null && handle?.hasPointerCapture?.(id)) handle.releasePointerCapture(id)
}
function moveTo(category: string, target: number) {
  if (props.disabled) return
  const next = [...categories.value], index = next.indexOf(category)
  if (index < 0 || target < 0 || target >= next.length || index === target) return
  next.splice(index, 1)
  next.splice(target, 0, category)
  // Keep saved identities that are temporarily absent from the selected crafts.
  // They do not render now, but re-selecting a craft must not erase its identity.
  for (const saved of props.modelValue) if (!next.includes(saved)) next.push(saved)
  emit('update:modelValue', next)
  announcement.value = category + '已移至第' + (target + 1) + '位，点击表单底部“保存”后生效。'
}
function move(category: string, delta: number) {
  moveTo(category, categories.value.indexOf(category) + delta)
}
function startDrag(event: DragEvent, category: string) {
  if (props.disabled || categories.value.length < 2 || pointerId !== null) {
    event.preventDefault()
    return
  }
  dragging.value = category
  dropTarget.value = null
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', category)
  }
}
function overCategory(event: DragEvent, category: string) {
  if (props.disabled || dragging.value === null) return
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
  dropTarget.value = category
}
function dropCategory(event: DragEvent, category: string) {
  if (!props.disabled && dragging.value !== null) {
    event.preventDefault()
    moveTo(dragging.value, categories.value.indexOf(category))
  }
  clearDrag()
}
function startPointer(event: PointerEvent, category: string) {
  // Mouse uses native HTML drag/drop; touch/pen uses the same real row targets.
  if (event.pointerType === 'mouse' || event.isPrimary === false || pointerId !== null || props.disabled || categories.value.length < 2 || event.button !== 0) return
  event.preventDefault()
  pointerId = event.pointerId
  pointerHandle = event.currentTarget as HTMLElement
  dragging.value = category
  dropTarget.value = category
  pointerHandle.setPointerCapture?.(event.pointerId)
}
function pointerTarget(event: PointerEvent) {
  const element = root.value?.ownerDocument.elementFromPoint(event.clientX, event.clientY)
  const row = element?.closest<HTMLElement>('[data-company-category]')
  dropTarget.value = row && root.value?.contains(row) ? row.dataset.companyCategory ?? null : null
}
function movePointer(event: PointerEvent) {
  if (event.pointerId !== pointerId) return
  if (props.disabled) { clearDrag(); return }
  event.preventDefault()
  pointerTarget(event)
}
function endPointer(event: PointerEvent) {
  if (event.pointerId !== pointerId) return
  event.preventDefault()
  pointerTarget(event)
  if (!props.disabled && dragging.value !== null && dropTarget.value !== null)
    moveTo(dragging.value, categories.value.indexOf(dropTarget.value))
  clearDrag()
}
function cancelPointer(event: PointerEvent) {
  if (event.pointerId === pointerId) clearDrag()
}
function restoreDefaults() {
  if (props.disabled || !categories.value.length || !props.modelValue.length) return
  if (!confirm('恢复本公司的默认工艺大类顺序？开工、包管、安全防护架、回填、防水涂料、丙纶依次显示，其余已有大类按原配置顺序追加。仅调整展示顺序，点击表单底部“保存”后生效。')) return
  if (props.disabled) return
  clearDrag()
  emit('update:modelValue', [])
  announcement.value = '已恢复默认顺序，点击表单底部“保存”后生效。'
}
watch(() => props.companyKey, () => { clearDrag(); announcement.value = '' })
watch(() => props.disabled, clearDrag)
watch(categories, next => {
  if (dragging.value !== null && !next.includes(dragging.value)) clearDrag()
})
onBeforeUnmount(clearDrag)
</script>

<template>
 <section ref="root" class="company-craft-order" aria-label="公司工艺大类排序" :aria-disabled="disabled || undefined">
  <div class="company-order-heading">
   <h3>工艺大类显示顺序</h3>
   <button type="button" :disabled="disabled || !categories.length || !modelValue.length" @click="restoreDefaults">恢复默认顺序</button>
  </div>
  <p class="company-order-hint">拖动手柄或使用上移、下移；手柄也可用键盘方向键调整。只影响本公司的展示顺序，不改工艺、小项、收费及历史记录内容。修改后点击表单底部“保存”。</p>
  <p v-if="!categories.length" class="company-order-empty">尚未选择固定工艺。请先从下方选择工艺，已有大类会在这里出现。</p>
  <ol v-else class="company-category-list" aria-label="本公司工艺大类顺序">
   <li v-for="(category, index) in categories" :key="category" :data-company-category="category"
       :class="{ 'is-dragging': dragging === category, 'is-drop-target': dropTarget === category && dragging !== category }"
       @dragover="overCategory($event, category)" @drop="dropCategory($event, category)">
    <button type="button" class="company-order-handle" :draggable="!disabled && categories.length > 1" :disabled="disabled || categories.length < 2"
            :aria-label="'拖动' + category + '调整顺序，可用上下方向键'" title="拖动排序；也可用上下方向键"
            @dragstart="startDrag($event, category)" @dragend="clearDrag"
            @pointerdown="startPointer($event, category)" @pointermove="movePointer" @pointerup="endPointer" @pointercancel="cancelPointer" @lostpointercapture="cancelPointer"
            @keydown.up.prevent="move(category, -1)" @keydown.down.prevent="move(category, 1)">
     <span aria-hidden="true">⠿</span>
    </button>
    <span class="company-category-name"><span class="company-category-number" aria-hidden="true">{{ index + 1 }}.</span>{{ category || '未设置大类' }}</span>
    <div class="company-order-actions">
     <button type="button" :disabled="disabled || index === 0" :aria-label="'上移' + category" @click="move(category, -1)">上移</button>
     <button type="button" :disabled="disabled || index === categories.length - 1" :aria-label="'下移' + category" @click="move(category, 1)">下移</button>
    </div>
   </li>
  </ol>
  <p v-if="disabled && categories.length" class="company-order-hint">当前不可修改排序。</p>
  <p class="company-order-status" role="status" aria-live="polite">{{ announcement }}</p>
 </section>
</template>

<style scoped>
.company-craft-order{min-width:0;margin:14px 0;padding:14px;border:1px solid #dce3eb;border-radius:8px;background:#f8fafc}
.company-order-heading{display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap}
.company-order-heading h3{margin:0;font-size:15px}.company-order-heading button{max-width:100%}
.company-order-hint,.company-order-empty,.company-order-status{margin:9px 0 0;line-height:1.6;color:#66768e;overflow-wrap:anywhere}
.company-order-empty{padding:10px 0}.company-order-status{color:#2451ab}.company-order-status:empty{margin:0}
.company-category-list{display:grid;gap:7px;margin:12px 0 0;padding:0;list-style:none}
.company-category-list li{display:grid;grid-template-columns:40px minmax(0,1fr) auto;align-items:center;gap:10px;min-width:0;padding:8px;border:2px solid transparent;border-radius:6px;background:#fff}
.company-category-list li.is-dragging{opacity:.65}.company-category-list li.is-drop-target{border-color:#4b7beb;background:#eaf1ff}
.company-order-handle{width:40px;min-height:40px;padding:4px;font-size:23px;line-height:1;cursor:grab;touch-action:none;user-select:none}
.company-order-handle:active{cursor:grabbing}.company-order-handle:disabled{cursor:not-allowed}
.company-category-name{min-width:0;overflow-wrap:anywhere;font-weight:600}.company-category-number{padding-right:7px;color:#8592a6;font-weight:400}
.company-order-actions{display:flex;gap:5px}.company-order-actions button{padding:7px 10px;white-space:nowrap;min-height:36px}
.company-craft-order button:focus-visible{outline:2px solid #286de0;outline-offset:2px}
@media(max-width:500px){.company-craft-order{padding:10px}.company-category-list li{gap:7px;padding:6px;grid-template-columns:36px minmax(0,1fr) auto}.company-order-handle{width:36px}.company-order-actions{gap:4px}.company-order-actions button{padding:6px 8px;font-size:12px}.company-category-number{padding-right:4px}}
</style>
