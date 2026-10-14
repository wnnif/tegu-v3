<script setup lang="ts">
import { computed, ref } from 'vue'
import { acceptanceGroups, formatCents, formatQuantity, printableLines, printLineLocation, printWorkerNames, wageRecordKey, type PrintOrder } from '../printOrder'
import { measureUnit } from '../orderSegments'

type OrderPrintModel = PrintOrder & { package_charge?: NonNullable<PrintOrder['package_charge']> & { package_type?: 'room_count' | 'fixed_scope' } }
const props = defineProps<{ order: OrderPrintModel; company: string; worker: string; canWages: boolean }>()
const emit = defineEmits<{ close: [] }>()
const mode = ref<'customer' | 'internal'>('customer')
const internalAvailable = computed(() => props.canWages && props.order.wage_cents != null)
const internal = computed(() => internalAvailable.value && mode.value === 'internal')
const groups = computed(() => acceptanceGroups(props.order))
const lines = computed(() => printableLines(props.order))
const workers = computed(() => printWorkerNames(props.order) || props.worker)
const total = computed(() => props.order.total_cents == null
  ? groups.value.reduce((sum, group) => sum + group.total_cents, 0n) + (props.order.sales || []).reduce((sum, sale) => sum + BigInt(sale.total_cents || 0), 0n)
  : BigInt(props.order.total_cents))
function print() { window.print() }
</script>

<template>
  <Teleport to="body">
    <div class="overlay print-overlay">
      <section class="modal wide">
        <div class="modal-head no-print"><h2>施工单预览</h2><div class="print-actions"><div v-if="internalAvailable" class="print-modes" role="group" aria-label="打印版本"><button type="button" :class="{active:!internal}" :aria-pressed="!internal" @click="mode='customer'">客户版</button><button type="button" :class="{active:internal}" :aria-pressed="internal" @click="mode='internal'">公司内部版</button></div><button @click="print">打印</button><button @click="emit('close')">关闭</button></div></div>
        <article class="acceptance-document">
          <table class="acceptance-table">
            <colgroup><col class="col-worker"><col class="col-craft"><col class="col-region"><col class="col-price"><col class="col-measure"><col class="col-quantity"><col class="col-amount"><col class="col-note"><col class="col-total"></colgroup>
            <thead>
              <tr><th colspan="9" class="document-title">宜宾特固建材施工单<span v-if="internal">（公司内部留档）</span></th></tr>
              <tr><th>客户：</th><td colspan="2">{{ company }}</td><th>工地地址：</th><td colspan="5">{{ order.address }}</td></tr>
              <tr><th>施工人员</th><th>工艺</th><th>部位</th><th>单价</th><th>方量明细</th><th>方量小计</th><th>金额小计</th><th>备注</th><th>合计金额</th></tr>
            </thead>
            <tbody>
              <template v-for="(group, index) in groups" :key="index">
                <tr v-for="(row, rowIndex) in group.rows" :key="rowIndex" class="detail-row">
                  <td>{{ row.worker_name || '—' }}</td>
                  <th v-if="rowIndex === 0" :rowspan="group.rows.length" class="craft-name">{{ group.label }}</th>
                  <td>{{ row.region }}</td>
                  <td>{{ formatCents(group.price_cents) }}<small>{{ group.mode === '金额' ? '整项客户收费' : '元/' + measureUnit(group.mode) }}</small></td>
                  <td><small v-for="(dimension, dimensionIndex) in row.dimensions" :key="dimensionIndex">{{ dimension }}</small><span v-if="!row.dimensions.length">未记录方量明细</span></td>
                  <td>{{ row.quantity }}</td>
                  <td class="group-total">{{ formatCents(row.total_cents) }} 元</td>
                  <td>{{ row.description || '—' }}<small v-if="row.work_date">{{ row.work_date }}</small></td><td>—</td>
                </tr>
              </template>
              <tr v-if="!groups.length"><td colspan="9">暂无已施工项目</td></tr>
              <tr v-if="order.sales?.length" class="group-summary"><th colspan="9">单独销售材料</th></tr>
              <tr v-for="(sale,index) in order.sales || []" :key="sale.id || index" class="detail-row"><td>{{ order.worker_name || worker || '—' }}</td><td>{{ sale.product_name }}</td><td>售料</td><td>{{ formatCents(sale.price_cents) }}<small>元/{{ sale.price_unit || sale.unit }}</small></td><td>{{ formatQuantity(sale.sale_qty_micro || sale.qty_micro) }} {{ sale.price_unit || sale.unit }}</td><td>{{ formatQuantity(sale.sale_qty_micro || sale.qty_micro) }} {{ sale.price_unit || sale.unit }}</td><td>{{ formatCents(sale.total_cents) }} 元</td><td>单独销售材料</td><td>—</td></tr>
              <tr v-if="order.package_charge" class="detail-row"><td>—</td><td>{{order.package_charge.package_type==='fixed_scope'?'墙地固整套套餐':'墙地固房型套餐'}}</td><td>{{order.package_charge.package_type==='fixed_scope'?'整套':`${order.package_charge.room_count} 房`}}</td><td>—</td><td>一次性收费</td><td>1 项</td><td>{{formatCents(order.package_charge.amount_cents)}} 元</td><td>已确认映射的套餐项免重复收费</td><td>—</td></tr>
              <tr class="grand-total"><th colspan="8">合计金额</th><td>{{ formatCents(total) }} 元</td></tr>
            </tbody>
          </table>
          <div class="document-meta"><span>施工时间：{{ order.work_date }}</span><span>项目经理：{{ order.contact || '—' }}</span><span>单号：{{ order.number }}</span></div>
          <p v-if="order.note" class="document-note">备注：{{ order.note }}</p>
          <template v-if="internal">
            <table class="internal-table">
              <thead><tr><th colspan="5">公司内部记录</th></tr><tr><th>施工项目</th><th>施工区域</th><th>工程量</th><th>人工单价 / 整项人工费（元）</th><th>人工费（元）</th></tr></thead>
              <tbody>
                <tr v-for="line in lines" :key="line.id || `${line.category}-${line.name}-${line.region}`"><td>{{ line.category }} / {{ line.name }}<small v-if="line.work_date||line.worker_name||(!line.worker_id||line.worker_id===order.worker_id)&&order.worker_name">{{line.work_date}} {{line.worker_name||(!line.worker_id||line.worker_id===order.worker_id?order.worker_name:'')}}</small></td><td>{{ printLineLocation(line) }}</td><td>{{ line.mode === '金额' ? '1 项' : `${formatQuantity(line.qty_micro || 0)} ${measureUnit(line.mode)}` }}</td><td>{{ formatCents(line.wage_cents) }}</td><td>{{ formatCents(line.labor_cents) }}</td></tr>
                <tr v-if="!lines.length"><td colspan="5">暂无已施工项目</td></tr>
                <tr v-if="Number(order.delivery_wage_cents||0)>0"><th colspan="4">带货工资（各次手填总工资之和）</th><td>{{formatCents(order.delivery_wage_cents)}}</td></tr>
                <tr class="grand-total"><th colspan="4">工资合计</th><td>{{ formatCents(order.wage_cents) }}</td></tr>
              </tbody>
            </table>
            <div class="internal-meta"><span>施工人：{{ workers || '—' }}</span><span>单据状态：{{ order.status }}</span><span v-if="!order.wage_records">付款记录：{{ Number(order.wage_paid) ? '已有付款记录' : '未记录付款' }}</span><span v-if="order.project_name">工地：{{ order.project_name }}</span><span v-if="order.phone">联系电话：{{ order.phone }}</span></div>
            <table v-if="order.wage_records?.length" class="internal-table"><thead><tr><th>施工 / 送货日期</th><th>人员</th><th>带货工资（元）</th><th>此人工资合计（元）</th><th>付款记录</th></tr></thead><tbody><tr v-for="record in order.wage_records" :key="wageRecordKey(record)"><td>{{record.work_date}}</td><td>{{record.worker_name}}</td><td>{{formatCents(record.delivery_wage_cents)}}</td><td>{{formatCents(record.wage_cents)}}</td><td>{{record.wage_paid==null?'—':Number(record.wage_paid)?'已有付款记录':'未记录付款'}}</td></tr></tbody></table>
            <p class="document-note">工资实际已付、扣款、预支及尚未支付金额，以工资管理的独立付款记录为准。</p>
            <table class="internal-table">
              <thead><tr><th colspan="3">材料消耗</th></tr><tr><th>商品</th><th>仓库</th><th>消耗数量</th></tr></thead>
              <tbody><tr v-for="(material, index) in order.materials || []" :key="index"><td>{{ material.product_name }}</td><td>{{ material.warehouse_name }}</td><td>{{ formatQuantity(material.qty_micro) }} {{ material.unit }}</td></tr><tr v-if="!order.materials?.length"><td colspan="3">无材料消耗</td></tr></tbody>
            </table>
          </template>
        </article>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.acceptance-document{padding:20px;color:#111;background:#fff;overflow-x:auto;font-family:'SimSun','Songti SC',serif;font-size:14px}.acceptance-table{width:100%;min-width:690px;table-layout:fixed;border-collapse:collapse;white-space:normal;text-align:center}.acceptance-table td,.acceptance-table th{border:1px solid #111;padding:5px 7px;color:#111;background:#fff;font-weight:400;overflow-wrap:anywhere;line-height:1.4}.acceptance-table small{display:block;color:#444;font-size:11px}.document-title{font-size:21px;font-weight:700!important}.col-worker{width:9%}.col-craft{width:14%}.col-region{width:11%}.col-price{width:9%}.col-measure{width:18%}.col-quantity{width:10%}.col-amount{width:10%}.col-note{width:11%}.col-total{width:8%}.craft-name,.group-total{vertical-align:middle}.group-summary th,.group-summary td,.grand-total th,.grand-total td{font-weight:600}.document-meta{display:flex;flex-wrap:wrap;gap:6px 28px;margin-top:7px}.document-note{margin-top:8px;white-space:pre-wrap;overflow-wrap:anywhere}
.print-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.print-modes{display:flex;border:1px solid #cad4e3;border-radius:6px;overflow:hidden}.print-modes button{border:0;border-radius:0;padding:7px 12px}.print-modes button+button{border-left:1px solid #cad4e3}.print-modes button:not(.active){background:#fff!important;color:#314361!important;box-shadow:none!important}.print-modes button.active{background:#2563eb;color:#fff}.internal-table{width:100%;border-collapse:collapse;table-layout:fixed;margin-top:18px;text-align:left;white-space:normal}.internal-table td,.internal-table th{border:1px solid #111;padding:5px 7px;background:#fff;color:#111;overflow-wrap:anywhere;font-weight:400}.internal-table thead tr:first-child th{font-weight:700}.internal-table .grand-total th,.internal-table .grand-total td{font-weight:700}.internal-meta{display:flex;flex-wrap:wrap;gap:6px 28px;margin-top:8px}
@media(max-width:600px){.print-overlay{padding:8px}.print-overlay .modal{width:100%}.print-overlay .modal-head{align-items:flex-start;gap:8px;flex-wrap:wrap}.print-actions{width:100%}.acceptance-document{padding:10px}}
@media print{.acceptance-document{padding:0;overflow:visible;font-size:11px}.acceptance-table{min-width:0}.acceptance-table td,.acceptance-table th,.internal-table td,.internal-table th{padding:3px 4px}.document-title{font-size:17px}.acceptance-table thead,.internal-table thead{display:table-header-group}.detail-row,.group-summary,.grand-total,.internal-table tr{break-inside:avoid;page-break-inside:avoid}}
</style>
