<script setup lang="ts">
import { computed } from 'vue'
import OrderNineColumns from './OrderNineColumns.vue'
import OrderCustomerTotal from './OrderCustomerTotal.vue'
import { savedOrderWorkView } from '../orderWorkView'
import type { SavedOrderRecord } from '../workflows/readModels'
import type { Craft } from '../workflows/models'

const props = defineProps<{ order: SavedOrderRecord; canCustomerPrices: boolean; canWages?: boolean; categoryOrder?: readonly string[]; crafts?: readonly Craft[] }>()
const view = computed(() => savedOrderWorkView(props.order, props.canCustomerPrices, { categoryOrder: props.categoryOrder, crafts: props.crafts, canWages: props.canWages }))
</script>

<template>
  <div class="saved-order-work" :data-saved-order-id="view.id">
    <p class="saved-view-hint">已保存记录 · 人员、方量与收费使用当时快照</p>
    <OrderNineColumns label="已保存施工记录九列查看">
      <section v-for="category in view.categories" :key="category.key" class="category-group">
        <header class="category-header"><strong class="category-title">{{category.category || '未记录大类'}}</strong></header>
        <article v-for="craft in category.crafts" :key="craft.key" class="craft-group" :data-saved-craft-id="craft.craft_id">
          <div class="craft-heading"><strong>{{craft.name || '未记录工艺名称'}}</strong><span class="saved-secondary"></span></div>
          <div class="craft-lines">
            <section v-for="line in craft.rows" :id="line.anchor" :key="line.key" class="order-line-row saved-line-row" role="row" :data-saved-line-id="line.id" :data-craft-id="line.craft_id" :data-subitem-id="line.subitem_id">
              <div class="order-cell" data-label="施工人员" role="cell"><strong class="saved-text">{{line.worker_name || '未记录施工人'}}</strong></div>
              <div class="order-cell" data-label="工艺" role="cell"><strong class="saved-text">{{line.name || '未记录工艺名称'}}</strong><small class="saved-secondary">{{line.mode || '未记录计量方式'}}{{line.temporary ? ' · 临时项' : ''}}</small><small v-if="line.spec" class="saved-secondary">{{line.spec}}</small></div>
              <div class="order-cell" data-label="部位" role="cell"><strong class="saved-text">{{line.subitem_name || line.actual_location || line.region || '—'}}</strong><small v-if="line.subitem_name && line.actual_location && line.actual_location !== line.subitem_name" class="saved-secondary legacy-location">原独立部位：{{line.actual_location}}</small><small v-if="line.region && line.region !== line.subitem_name && line.region !== line.actual_location" class="saved-secondary legacy-region">原部位信息：{{line.region}}</small></div>
              <div class="order-cell" data-label="单价" role="cell"><strong class="saved-number">{{line.price_text}}</strong><small v-if="line.price_cents !== undefined" class="saved-secondary">{{line.mode === '金额' ? '整项客户收费' : '元/' + line.unit}}</small><small v-if="line.charge_mode==='free'||line.charge_mode==='redo'" class="saved-secondary">{{line.charge_mode==='free'?'免费施工':'补做不收费'}}</small></div>
              <div class="order-cell measure-cell" data-label="方量明细" role="cell"><span v-for="dimension in line.dimensions" :key="dimension.key" class="saved-measure">{{dimension.text}}</span></div>
              <div class="order-cell" data-label="方量小计" role="cell"><strong class="saved-number">{{line.quantity_text}}</strong><small v-if="line.has_waiver" class="saved-secondary">免收 {{line.waived_quantity_text}} · 剩余收费 {{line.chargeable_quantity_text}}</small></div>
              <div class="order-cell" data-label="金额小计" role="cell"><strong class="saved-number">{{line.amount_text}}</strong><small v-if="canWages&&(line.labor_cents!==undefined||line.wage_override_cents!==undefined)" class="saved-secondary">实际工资：{{Number(line.labor_cents??line.wage_override_cents)/100}} 元</small><details v-if="canWages&&line.wage_adjustments?.length" class="wage-adjustments"><summary>工资调整记录（{{line.wage_adjustments.length}}）</summary><p v-for="adjustment in line.wage_adjustments" :key="adjustment.id||adjustment.created_at"><span>{{adjustment.before_cents===undefined?'未记录':Number(adjustment.before_cents)/100}} → {{adjustment.after_cents===undefined?'未记录':Number(adjustment.after_cents)/100}} 元</span> · {{adjustment.reason||'未记录原因'}} · {{adjustment.actor_name||'未记录人员'}} · {{adjustment.created_at||'未记录时间'}}</p></details></div>
              <div class="order-cell note-cell" data-label="备注" role="cell"><span class="saved-text saved-note">{{line.description || '—'}}</span><small v-if="line.charge_note" class="saved-secondary saved-note">{{line.charge_note}}</small></div>
              <div v-if="view.materials.some(material=>material.line_id===line.id)" class="saved-line-materials"><strong>本项施工耗材</strong><p v-for="material in view.materials.filter(material=>material.line_id===line.id)" :key="material.key">{{material.product_name||'未记录商品名称'}} · {{material.quantity_text}} · {{material.warehouse_name||'未记录仓库'}}</p></div>
            </section>
          </div>
        </article>
      </section>
      <p v-if="!view.categories.length" class="saved-empty">没有已保存的施工工艺明细。</p>
    </OrderNineColumns>

    <section class="saved-combinations" aria-label="已保存组合计价关系">
      <h4>组合计价（保存记录）</h4>
      <p v-if="!view.combination_enabled" class="saved-secondary">未使用组合计价。</p>
      <template v-else>
        <p class="saved-secondary">已启用组合计价。免收只影响客户收费；实际施工量和工资不在此处调整。</p>
        <article v-for="relation in view.combinations" :key="relation.key" class="saved-combination" :data-combination-id="relation.id">
          <p class="saved-combination-title"><strong>{{relation.direction_text}}</strong> · 本组合免收 {{relation.waived_quantity_text}}<span v-if="relation.cross_date" class="saved-cross-date"> · 跨日期关联</span></p>
          <div v-for="side in [relation.waterproof, relation.polypropylene]" :key="side.kind" class="saved-combination-side" :data-related-line-id="side.id" :data-related-order-id="side.order_id">
            <strong>{{side.kind === 'waterproof' ? '防水明细' : '丙纶明细'}}：{{side.name || '未记录工艺名称'}}</strong>
            <p class="saved-relation-reference"><a v-if="side.anchor" :href="'#' + side.anchor">施工记录 {{side.number || side.order_id || '未记录编号'}} · 明细 {{side.id}}</a><span v-else>关联施工记录 {{side.number || side.order_id || '未记录编号'}} · 明细 {{side.id}}</span></p>
            <p v-if="side.has_details" class="saved-secondary">施工日期：{{side.work_date || '未记录'}} · 施工人：{{side.worker_name || '未记录'}} · 实际部位：{{side.actual_location || '未填写'}} · 配置小项：{{side.subitem_name || '未记录'}}</p>
            <p v-else class="saved-secondary">仅保存关联编号；日期、部位、施工人等快照未返回，不推测关联记录。</p>
            <p>实际 {{side.quantity_text}} · 累计免收 {{side.waived_quantity_text}} · 剩余收费 {{side.chargeable_quantity_text}}</p>
            <p class="saved-secondary">保存后金额：<strong class="saved-number">{{side.amount_text}}</strong></p>
          </div>
        </article>
      </template>
    </section>

    <section v-if="view.sales.length" class="saved-sales" aria-label="已保存售料记录">
      <h4>售料记录（已计入保存的整单合计）</h4>
      <article v-for="sale in view.sales" :key="sale.key" class="saved-sale">
        <strong>{{sale.product_name || '未记录商品名称'}}</strong>
        <p>实际数量：{{sale.quantity_text}}<span v-if="sale.price_unit"> · 计价数量：{{sale.sale_quantity_text}}</span></p>
        <p class="saved-secondary">客户单价：{{sale.price_text}}<span v-if="sale.price_cents !== undefined && sale.price_unit"> /{{sale.price_unit}}</span> · 金额小计：<strong class="saved-number">{{sale.amount_text}}</strong></p>
      </article>
    </section>
    <section v-if="view.materials.some(material=>!material.line_id)" class="saved-materials" aria-label="已保存施工耗材">
      <h4>公共施工耗材</h4>
      <p v-for="material in view.materials.filter(material=>!material.line_id)" :key="material.key" class="saved-text">{{material.product_name || '未记录商品名称'}} · {{material.quantity_text}} · {{material.warehouse_name || '未记录仓库'}}</p>
    </section>
    <p v-if="view.sales.length || view.materials.length" class="saved-secondary">材料/送货责任人：{{view.material_worker_name || '未记录责任人'}}</p>
    <section v-if="canWages&&view.wage_records?.length" class="saved-wages" aria-label="已保存工资记录"><h4>施工工资记录</h4><p v-for="record in view.wage_records" :key="record.id||record.worker_id">{{record.worker_name||'未记录施工人'}} · {{Number(record.wage_cents||0)/100}} 元 · {{Number(record.wage_paid)?'已发放':'待发放'}}</p></section>
    <details v-if="canWages&&view.wage_adjustments?.length" class="saved-wages wage-adjustment-history"><summary>工资调整历史（{{view.wage_adjustments.length}}）</summary><p v-for="adjustment in view.wage_adjustments" :key="adjustment.id||adjustment.created_at"><span>{{adjustment.line_deleted?'已删除明细 · ':''}}{{adjustment.worker_name||'未记录施工人'}}</span> · {{adjustment.before_cents===undefined?'未记录':Number(adjustment.before_cents)/100}} → {{adjustment.after_cents===undefined?'未记录':Number(adjustment.after_cents)/100}} 元 · {{adjustment.reason||'未记录原因'}} · {{adjustment.actor_name||'未记录人员'}} · {{adjustment.created_at||'未记录时间'}}</p></details>
    <p v-if="view.note" class="saved-order-note saved-note">整单备注：{{view.note}}</p>
    <OrderCustomerTotal v-if="canCustomerPrices" :amount="view.total_cents" :can-customer-prices="canCustomerPrices" />
  </div>
</template>

<style scoped>
.saved-order-work{min-width:0;max-width:100%}
.saved-view-hint,.saved-secondary{color:#65768c;font-size:12px;line-height:1.5;overflow-wrap:anywhere}
.saved-view-hint{margin:0 0 8px}
.saved-text,.saved-number,.saved-measure{display:block;font-size:13px;line-height:1.5;overflow-wrap:anywhere}
.saved-number{font-variant-numeric:tabular-nums}
small.saved-secondary{display:block;margin-top:4px}
.saved-measure + .saved-measure{margin-top:5px}
.saved-note{white-space:pre-wrap;overflow-wrap:anywhere}
.saved-empty{grid-column:1/-1;margin:0;padding:12px;color:#65768c;font-size:13px}
.saved-combinations,.saved-sales,.saved-materials{margin-top:12px;min-width:0;max-width:100%;font-size:13px}
.saved-combinations h4,.saved-sales h4,.saved-materials h4{margin:0 0 6px;font-size:13px}
.saved-combinations p,.saved-sale p{margin:5px 0;line-height:1.5;overflow-wrap:anywhere}
.saved-combination,.saved-sale{border:1px solid #dce5f1;border-radius:6px;margin-top:7px;padding:8px 10px;min-width:0}
.saved-combination-title{color:#314562}
.saved-cross-date{color:#315f92;font-size:12px}
.saved-combination-side{margin-top:8px;padding-top:7px;border-top:1px solid #e7edf5;min-width:0;overflow-wrap:anywhere}
.saved-relation-reference{font-size:12px;overflow-wrap:anywhere}
.saved-relation-reference a{color:#245f9c;text-decoration:underline}
.saved-order-note{margin:10px 0;color:#435670;font-size:13px}
.saved-line-materials{grid-column:1/-1;min-width:0;border-top:1px dashed #dce4ed;padding:6px 8px;font-size:12px;color:#49627e}.saved-line-materials p{margin:5px 0;overflow-wrap:anywhere}.saved-wages{margin-top:12px;font-size:13px;min-width:0}.saved-wages h4{margin:0 0 6px}.saved-wages p,.wage-adjustments p{overflow-wrap:anywhere;line-height:1.5;margin:5px 0}.wage-adjustment-history summary,.wage-adjustments summary{cursor:pointer;color:#3a618f}.wage-adjustments{font-size:11px;margin-top:5px}
</style>
