<script setup lang="ts">
import { computed, ref } from 'vue'
import { fromQuantum, formatQuantum, toQuantum, unitFactors } from '../stockQuantity'
import type { Balance, CatalogRow, Material, References } from '../workflows/models'
import ReferenceSelect from './ReferenceSelect.vue'

const props = defineProps<{
  materials: Material[]
  lineId?: string
  references: References
  inventory: Balance[]
  orderWarehouse: string
  canWarehouse: boolean
  disabled: boolean
}>()
const emit = defineEmits<{ product: [item: CatalogRow | null]; source: [] }>()
const warning = ref('')
const lineID = computed(() => props.lineId || '')
const rows = computed(() => (props.materials || []).map((item, index) => ({ item, index }))
  .filter(({ item }) => (item.line_id || '') === lineID.value))
const product = (material: Material) => {
  const current = props.references.products.find(item => item.id === material.product_id)
  return { ...current, ...material, unit: material.unit || current?.unit || '', aux_unit: material.aux_unit || current?.aux_unit, aux_ratio: material.conversion_ratio || current?.aux_ratio, ...(material.conversion_ratio ? { base_factor: undefined, aux_factor: undefined } : {}) }
}
function quantum(material: Material): string {
  if (material.input_qty) {
    const exact = toQuantum(material.input_qty, material.input_unit || 'base', product(material))
    if (exact) return exact
  }
  if (material.qty_quantum) return material.qty_quantum
  const factors = unitFactors(product(material))
  return factors ? String(BigInt(String(material.qty_micro || 0)) * factors.base) : '0'
}
function quantity(material: Material): string {
  return material.input_qty || fromQuantum(quantum(material), material.input_unit || 'base', product(material)) || ''
}
function setQuantity(material: Material, value: string) {
  material.input_qty = value
  material.qty_quantum = toQuantum(value, material.input_unit || 'base', product(material)) || ''
  const factors = unitFactors(product(material))
  material.qty_micro = material.qty_quantum && factors ? Number(BigInt(material.qty_quantum) / factors.base) : 0
  warning.value = ''
}
function changeUnit(material: Material, event: Event) {
  const target = event.target as HTMLSelectElement
  const old = material.input_unit || 'base'
  const oldQuantum = quantum(material)
  const exact = fromQuantum(oldQuantum, target.value, product(material))
  if (exact === null) {
    target.value = old
    warning.value = '此数量无法用所选单位精确表示，请保留原单位。'
    return
  }
  material.input_unit = target.value
  material.input_qty = exact
  material.qty_quantum = oldQuantum
  warning.value = ''
}
function baseQuantity(material: Material) {
  return formatQuantum(quantum(material), 'base', product(material))
}
function available(material: Material) {
  const source = material.warehouse_id || props.orderWarehouse
  const balance = props.inventory.find(item => item.product_id === material.product_id && item.warehouse_id === source)
  return balance?.quantity_quantum != null ? formatQuantum(String(balance.quantity_quantum), 'base', product(material)) : String(Number(balance?.qty_micro || 0) / 1e6)
}
function add() {
  props.materials?.push({ product_id: '', warehouse_id: props.orderWarehouse, line_id: lineID.value, qty_micro: 0, qty_quantum: '', input_qty: '', input_unit: 'base' })
}
function remove(index: number) { props.materials.splice(index, 1) }
</script>

<template>
  <div class="material-fields" :data-material-line-id="lineID">
    <p v-if="warning" class="error" role="alert">{{ warning }}</p>
    <div v-for="row in rows" :key="row.item.id || `${lineID}:${row.index}`" class="material-row">
      <ReferenceSelect v-model="row.item.product_id" entity="products" label="施工使用商品" empty-label="请选择商品" search-placeholder="搜索商品" :selected-name="row.item.product_name || row.item.name" active="1" :disabled="disabled" @change="row.item.qty_micro=0;row.item.qty_quantum='';row.item.input_qty='';row.item.input_unit='base';emit('product',$event)" />
      <label>数量<input :value="quantity(row.item)" type="number" min="0" step="0.000001" :disabled="disabled" @input="setQuantity(row.item,($event.target as HTMLInputElement).value)"></label>
      <label>单位<select :value="row.item.input_unit || 'base'" :disabled="disabled" @change="changeUnit(row.item,$event)"><option value="base">{{ product(row.item).unit || '基本单位' }}</option><option v-if="product(row.item).aux_unit" value="aux">{{ product(row.item).aux_unit }}</option></select></label>
      <ReferenceSelect v-if="canWarehouse" v-model="row.item.warehouse_id" entity="warehouses" label="实际用料位置" empty-label="请选择来源" search-placeholder="搜索总仓或工人结余" :selected-name="row.item.warehouse_name" active="1" required :disabled="disabled" @change="emit('source')" />
      <div v-else>用料位置 {{ row.item.warehouse_name || references.warehouses.find(item => item.id === (row.item.warehouse_id || orderWarehouse))?.name || '未选择' }}</div>
      <div>基本数量 {{ baseQuantity(row.item) }} {{ product(row.item).unit }}<small>可用 {{ available(row.item) }} {{ product(row.item).unit }}</small></div>
      <button type="button" :disabled="disabled" @click="remove(row.index)">移除</button>
    </div>
    <button type="button" :disabled="disabled || (!orderWarehouse && !canWarehouse)" @click="add">{{lineID?'＋ 本项耗材':'＋ 添加耗材'}}</button>
    <p v-if="!rows.length" class="empty">{{ lineID ? '本项没有施工耗材。' : '本次没有公共施工耗材。' }}</p>
  </div>
</template>

<style scoped>
.material-fields{display:grid;gap:8px;min-width:0}.material-row{display:grid;grid-template-columns:minmax(180px,2fr) minmax(90px,1fr) minmax(90px,1fr) minmax(130px,1fr) minmax(120px,1fr) auto;gap:10px;align-items:end;padding:10px;background:#f8fafc;border:1px solid #dce4ed}.material-row label{min-width:0}.material-row input,.material-row select{width:100%;min-width:0}.material-row small{display:block;color:#65768c}.empty{padding:4px;color:#77879b;font-size:13px}.error{margin:0;color:#b42318;font-size:13px}
@media(max-width:800px){.material-row{grid-template-columns:repeat(2,minmax(0,1fr))}.material-row>:first-child{grid-column:1/-1}}
</style>
