<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import { customerMoney } from '../customerPrices'
import ProjectEditor from './ProjectEditor.vue'
import ReferenceSelect from './ReferenceSelect.vue'
import ProjectStatusDialog from './ProjectStatusDialog.vue'
import ProjectAssociation from './ProjectAssociation.vue'
import ProjectWorkPanel from './ProjectWorkPanel.vue'
import { projectError, type ProjectAction, type ProjectDetail, type ProjectList, type ProjectOrder, type ProjectRecord } from '../projects'

const props = defineProps<{ canMaintain: boolean; canOrder: boolean; canExport: boolean; canCustomerPrices: boolean; canFinance?: boolean; canManageFinance?: boolean; canWages?: boolean }>()
const money = (value: unknown) => customerMoney(value, props.canCustomerPrices)
const financeMoney = (value: unknown) => customerMoney(value, !!props.canFinance && props.canCustomerPrices)
const emit = defineEmits<{ openOrder: [order: {id:string}]; openReceipts: [companyId:string] }>()
const items = ref<ProjectRecord[]>([]), detail = ref<ProjectRecord | null>(null), orders = ref<ProjectOrder[]>([])
const company = ref(''), search = ref(''), status = ref(''), page = ref(1), total = ref(0), detailPage = ref(1)
const loading = ref(false), detailLoading = ref(false), error = ref(''), detailError = ref(''), notice = ref('')
const editTarget = ref<ProjectRecord | null>(null), action = ref<ProjectAction | null>(null), associating = ref(false)
const workPanel = ref<InstanceType<typeof ProjectWorkPanel> | null>(null)
const exportUrl = (id: string) => '/api/export?' + new URLSearchParams({ view: 'project', project_id: id })
let listGeneration = 0, detailGeneration = 0
async function load(nextPage = page.value) {
  const request = ++listGeneration
  loading.value = true; error.value = ''
  try {
    const result: ProjectList = await api('/projects?' + new URLSearchParams({ company_id: company.value, q: search.value.trim(), status: status.value, page: String(nextPage) }))
    if (request !== listGeneration) return
    items.value = result.rows; total.value = Number(result.total); page.value = Number(result.page)
    if (nextPage > 1 && !result.rows.length && result.total > 0) await load(Math.max(1, Math.ceil(Number(result.total) / 100)))
  } catch (e) { if (request === listGeneration) {items.value=[];total.value=0;error.value = projectError(e)} }
  finally { if (request === listGeneration) loading.value = false }
}
async function open(id: string, nextPage = 1) {
  const request = ++detailGeneration
  detailLoading.value = true; detailError.value = ''; notice.value = ''
  try {
    const result: ProjectDetail = await api('/projects?' + new URLSearchParams({ id, page: String(nextPage) }))
    if (request !== detailGeneration) return
    detail.value = result.project; orders.value = result.orders; detailPage.value = Number(result.page)
    const lastPage = Math.max(1, Math.ceil(Number(result.project.order_count) / 100))
    if (nextPage > lastPage) await open(id, lastPage)
  } catch (e) { if (request === detailGeneration) detailError.value = projectError(e) }
  finally { if (request === detailGeneration) detailLoading.value = false }
}
function back() { ++detailGeneration; detailLoading.value = false; detail.value = null; orders.value = []; detailError.value = ''; notice.value = ''; load() }
function edit(project: ProjectRecord) { editTarget.value = project; notice.value = '' }
async function saved(id: string) {
  editTarget.value = null; action.value = null
  await Promise.all([load(), open(id, detail.value?.id === id ? detailPage.value : 1)])
  notice.value = '工地资料已保存' + (error.value || detailError.value ? '，页面刷新失败，请重试读取最新资料' : '')
}
async function associated() {
  if (!detail.value) return
  await Promise.all([load(), open(detail.value.id, detailPage.value)])
  notice.value = detailError.value ? '旧单关联已保存，工地汇总读取失败，请刷新工地' : '旧单关联已保存，工地汇总已更新'
}
async function refreshCurrent(firstPage = false) {
  const id = detail.value?.id
  await Promise.all([load(), id ? open(id, firstPage ? 1 : detailPage.value) : Promise.resolve()])
  if (id) await workPanel.value?.refresh()
}
defineExpose({ refreshCurrent })
watch([company, status], () => load(1)); onMounted(() => load(1))
watch(() => [props.canCustomerPrices, props.canFinance, props.canWages], () => {
  ++listGeneration; ++detailGeneration; items.value = []; orders.value = []; detail.value = null
  editTarget.value = null; action.value = null; associating.value = false; detailLoading.value = false
  void load(1)
})
onBeforeUnmount(() => { ++listGeneration; ++detailGeneration })
</script>

<template>
  <section class="panel projects-panel">
    <div v-if="notice" class="notice" role="status">{{ notice }}</div>
    <template v-if="detail">
      <div class="toolbar"><button :disabled="detailLoading" @click="back">返回工地列表</button><strong class="project-title">{{ detail.name }}<small>{{ detail.number }}</small></strong><span class="project-status" :class="{ muted: detail.status !== '进行中' }">{{ detail.status }}</span><button :disabled="detailLoading" @click="open(detail.id, detailPage)">刷新工地</button></div>
      <p class="project-address">{{ detail.company_name }} · {{ detail.address }}</p>
      <ProjectWorkPanel ref="workPanel" :project-id="detail.id" :can-finance="canFinance" :can-manage-finance="canManageFinance" :can-wages="canWages" :can-order="canOrder" :can-customer-prices="canCustomerPrices" @open-order="emit('openOrder',$event)" />
      <div class="toolbar project-actions"><button v-if="canFinance&&canCustomerPrices" type="button" :disabled="detailLoading" @click="emit('openReceipts',detail.company_id)">公司收款 / 欠款</button><button v-if="canMaintain" :disabled="detailLoading || !!detailError" @click="edit(detail)">更正工地资料</button><button v-if="canMaintain && detail.status === '进行中'" :disabled="detailLoading || !!detailError" @click="action = 'disable'">停用工地</button><button v-if="canMaintain && detail.status !== '进行中'" :disabled="detailLoading || !!detailError" @click="action = 'enable'">重新启用</button><button v-if="canMaintain && detail.status !== '已结项'" :disabled="detailLoading || !!detailError" @click="action = 'close'">工地结项</button><button v-if="canMaintain" :disabled="detailLoading || !!detailError" @click="associating = true">关联未归属施工单</button><a v-if="canExport&&canCustomerPrices" :href="exportUrl(detail.id)">导出各期</a></div>
      <div v-if="detailError" class="error" role="alert">{{ detailError }}<p>当前保留上次读取结果，请点击“刷新工地”后继续操作。</p></div>
      <p v-if="detailLoading" class="hint" role="status">正在读取工地和各期施工单…</p>
      <p v-if="detail.status !== '进行中'" class="hint">工地{{ detail.status }}，现有各期及收款记录仍保留。重新启用后可以继续开单。</p>
      <div class="project-totals"><div><span>施工 / 送货记录</span><strong>{{ detail.order_count }} 次</strong></div><div><span>施工合计</span><strong>{{ money(detail.total_cents) }}</strong></div><div><span>已分配收款</span><strong>{{ financeMoney(detail.received_cents) }}</strong></div><div><span>尚未交票收费</span><strong>{{ financeMoney(detail.undelivered_cents) }}</strong></div><div><span>已交票尚未核销</span><strong>{{ financeMoney(detail.delivered_outstanding_cents) }}</strong></div><div><span>客户确认减免</span><strong>{{ financeMoney(detail.discount_cents) }}</strong></div><div><span>工地尚未核销</span><strong>{{ financeMoney(detail.outstanding_cents) }}</strong></div></div>
      <p class="hint">工地尚未核销按收费减去客户减免及已分配收款；尚未交票收费单列。公司的未分配收款仍会减少公司真正欠款，请在“公司收款 / 欠款”查看实际待收。已取消施工单不计收费。</p>
      <div class="table-wrap"><table><thead><tr><th>日期 / 单号</th><th>施工地址</th><th>施工人</th><th>施工金额</th><th>已分配收款</th><th>尚未核销</th><th>状态</th><th v-if="canOrder">操作</th></tr></thead><tbody><tr v-for="order in orders" :key="order.id"><td>{{ order.work_date }}<small>{{ order.number }}</small></td><td class="wrapping-cell">{{ order.address }}</td><td>{{ order.worker_names || order.worker_name || '—' }}</td><td>{{ money(order.total_cents) }}</td><td>{{ financeMoney(order.received_cents) }}</td><td>{{ financeMoney(order.outstanding_cents) }}</td><td>{{ order.status }}</td><td v-if="canOrder"><button class="link" :disabled="detailLoading" @click="emit('openOrder', order)">查看 / 更正本次记录</button></td></tr><tr v-if="!orders.length && !detailLoading"><td :colspan="canOrder ? 8 : 7" class="empty">{{ detailError ? '读取失败，请刷新工地' : '本工地暂无施工 / 送货记录' }}</td></tr></tbody></table></div>
      <div class="toolbar pagination"><button :disabled="detailPage <= 1 || detailLoading" @click="open(detail.id, detailPage - 1)">上一页</button><span>共 {{ detail.order_count }} 次 · 第 {{ detailPage }} / {{ Math.max(1, Math.ceil(Number(detail.order_count) / 100)) }} 页</span><button :disabled="detailPage * 100 >= Number(detail.order_count) || detailLoading" @click="open(detail.id, detailPage + 1)">下一页</button></div>
    </template>
    <template v-else>
      <form class="toolbar filter-toolbar" @submit.prevent="load(1)"><ReferenceSelect v-model="company" entity="companies" label="公司" empty-label="全部公司" search-placeholder="搜索公司" :disabled="detailLoading" /><label>工地状态<select v-model="status" :disabled="detailLoading"><option value="">全部状态</option><option>进行中</option><option>已停用</option><option>已结项</option></select></label><label class="search-field">查找工地<input v-model="search" maxlength="300" placeholder="施工地址、名称或编号"></label><button :disabled="loading || detailLoading">查询</button></form>
      <p class="hint">首次施工在“施工制单”填写公司和地址，保存后会出现在这里；后续可查看累计施工、票据和收款。</p>
      <div v-if="error || detailError" class="error" role="alert">{{ error || detailError }}<button type="button" :disabled="loading || detailLoading" @click="detailError = ''; load()">重试读取列表</button></div>
      <p v-if="loading || detailLoading" class="hint" role="status">{{ detailLoading ? '正在打开工地…' : '正在读取工地汇总…' }}</p>
      <div class="table-wrap"><table><thead><tr><th>工地 / 编号</th><th>公司 / 地址</th><th>状态</th><th>单据</th><th>施工合计</th><th>已分配收款</th><th>尚未交票收费</th><th>已交票尚未核销</th><th>工地尚未核销</th><th>操作</th></tr></thead><tbody><tr v-for="project in items" :key="project.id"><td class="wrapping-cell"><strong>{{ project.name }}</strong><small>{{ project.number }}</small></td><td class="wrapping-cell">{{ project.company_name }}<small>{{ project.address }}</small></td><td><span class="project-status" :class="{ muted: project.status !== '进行中' }">{{ project.status }}</span></td><td>{{ project.order_count }}</td><td>{{ money(project.total_cents) }}</td><td>{{ financeMoney(project.received_cents) }}</td><td>{{ financeMoney(project.undelivered_cents) }}</td><td>{{ financeMoney(project.delivered_outstanding_cents) }}</td><td>{{ financeMoney(project.outstanding_cents) }}</td><td><button class="link" :disabled="loading || detailLoading || !!error" @click="open(project.id)">查看汇总 / 交票</button><button v-if="canFinance&&canCustomerPrices" type="button" :disabled="loading||detailLoading" @click="emit('openReceipts',project.company_id)">公司收款 / 欠款</button></td></tr><tr v-if="!items.length && !loading"><td colspan="10" class="empty">{{ error ? '列表读取失败，请重试' : '暂无符合条件的工地' }}</td></tr></tbody></table></div>
      <div class="toolbar pagination"><button :disabled="page <= 1 || loading || detailLoading" @click="load(page - 1)">上一页</button><span>共 {{ total }} 个 · 第 {{ page }} / {{ Math.max(1, Math.ceil(total / 100)) }} 页</span><button :disabled="page * 100 >= total || loading || detailLoading" @click="load(page + 1)">下一页</button></div>
    </template>
    <ProjectEditor v-if="editTarget && canMaintain" :project="editTarget" @close="editTarget = null" @saved="saved" />
    <ProjectStatusDialog :can-customer-prices="canCustomerPrices" :can-finance="canFinance" v-if="action && detail && canMaintain" :project="detail" :action="action" @close="action = null" @saved="saved" />
    <ProjectAssociation :can-customer-prices="canCustomerPrices" v-if="associating && detail && canMaintain" :project="detail" @close="associating = false" @changed="associated" />
  </section>
</template>

<style scoped>
.projects-panel{min-width:0}.projects-panel>.notice,.projects-panel>.error{margin:12px 16px}.project-title{flex:1;min-width:170px;overflow-wrap:anywhere}.project-address{padding:0 16px;overflow-wrap:anywhere}.project-actions{padding-top:12px}.project-status{display:inline-block;background:#eef4ff;color:#356299;border-radius:4px;padding:3px 8px;white-space:nowrap}.project-status.muted{color:#646f7e;background:#eef0f3}.project-totals{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;padding:0 16px}.project-totals>div{border:1px solid #e5ebf3;border-radius:6px;padding:12px;min-width:0}.project-totals span{display:block;color:#6b7c94}.project-totals strong{display:block;font-size:18px;overflow-wrap:anywhere;margin-top:4px}.wrapping-cell{white-space:normal;min-width:170px;max-width:280px;overflow-wrap:anywhere}.filter-toolbar{align-items:end}.search-field{flex:1;min-width:200px!important}.pagination span{flex:1;text-align:center}.error button{margin:8px}.error p{margin-top:6px}
@media(max-width:600px){.project-totals{grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;padding:0 12px}.project-totals strong{font-size:15px}.filter-toolbar>label{flex:1;min-width:120px}.search-field{flex-basis:100%}.pagination span{order:-1;flex-basis:100%}.project-actions{gap:8px}.project-title{flex-basis:65%}}
</style>

