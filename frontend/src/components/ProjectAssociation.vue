<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, command } from '../api'
import { projectError, projectMoney, type ProjectRecord, type UnassignedOrder, type UnassignedOrderList } from '../projects'
const props = defineProps<{ project: ProjectRecord; canCustomerPrices: boolean }>()
const money = (value: unknown) => projectMoney(value, props.canCustomerPrices)
const emit = defineEmits<{ close: []; changed: [] }>()
const rows = ref<UnassignedOrder[]>([]), selected = ref<UnassignedOrder | null>(null)
const query = ref(''), page = ref(1), total = ref(0), loading = ref(false), saving = ref(false), error = ref(''), notice = ref('')
const submit = command()
let generation = 0
async function load(nextPage = page.value) {
  if (saving.value) return
  const request = ++generation
  loading.value = true; error.value = ''; selected.value = null
  try {
    const result: UnassignedOrderList = await api('/projects?' + new URLSearchParams({ unassigned: '1', company_id: props.project.company_id, q: query.value.trim(), page: String(nextPage) }))
    if (request !== generation) return
    rows.value = result.rows; total.value = Number(result.total); page.value = Number(result.page)
    if (nextPage > 1 && !result.rows.length && result.total > 0) await load(Math.max(1, Math.ceil(Number(result.total) / 100)))
  } catch (e) { if (request === generation) error.value = projectError(e) }
  finally { if (request === generation) loading.value = false }
}
function select(order: UnassignedOrder) { selected.value = order; error.value = ''; notice.value = '' }
async function associate() {
  if (!selected.value || saving.value || loading.value) return
  saving.value = true; error.value = ''; notice.value = ''
  const order = selected.value
  let succeeded = false
  try {
    await submit('/project-associate', { order_id: order.id, project_id: props.project.id, version: Number(order.version) })
    notice.value = '已将施工单 ' + order.number + ' 关联到本工地'; selected.value = null; succeeded = true; emit('changed')
  } catch (e) { error.value = projectError(e) }
  finally { saving.value = false }
  if (succeeded) await load()
}
onMounted(() => load(1))
</script>
<template>
  <div class="overlay" @keydown.esc="!saving && emit('close')">
    <section class="modal association-dialog" role="dialog" aria-modal="true" aria-labelledby="project-association-title">
      <div class="modal-head"><h2 id="project-association-title">关联未归属施工单</h2><button type="button" :disabled="saving" aria-label="关闭施工单关联" @click="emit('close')">×</button></div>
      <div class="association-body">
        <p><strong>{{ project.name }}</strong> · {{ project.company_name }}</p>
        <p class="notice">仅显示该公司尚未关联工地的施工单。请逐张核对地址和单号；关联后会计入本工地各期汇总，原单据金额、收款记录和地址会保留。</p>
        <div v-if="notice" class="notice" role="status">{{ notice }}</div>
        <div v-if="error" class="error" role="alert">{{ error }}<p v-if="selected">当前选择已保留。若单据已被他人修改或关联，请刷新候选列表后重新选择。</p><button type="button" :disabled="saving || loading" @click="load()">刷新候选列表</button></div>
        <form class="association-search" @submit.prevent="load(1)"><label>搜索施工单<input v-model="query" maxlength="200" :disabled="saving || loading" placeholder="施工地址或单号"></label><button :disabled="saving || loading">查询</button></form>
        <p v-if="loading" class="hint" role="status">正在读取未关联的施工单…</p>
        <template v-if="selected">
          <section class="association-confirm" aria-label="确认关联施工单">
            <h3>请核对本次关联</h3><p>施工单：<strong>{{ selected.number }}</strong> · {{ selected.work_date }}</p><p>原地址：{{ selected.address }}</p><p>施工金额：{{ money(selected.total_cents) }} · {{ selected.status }}</p><p>归入工地：<strong>{{ project.name }}</strong>（{{ project.number }}）</p>
            <p class="hint">保存后普通编辑不能改回归属，请确认这张旧单确属本工地。</p>
            <div class="confirm-actions"><button type="button" :disabled="saving" @click="selected = null; error = ''">返回重新选择</button><button class="primary" type="button" :disabled="saving || loading" @click="associate">{{ saving ? '关联中…' : '确认关联这张单' }}</button></div>
          </section>
        </template>
        <template v-else>
          <div class="table-wrap"><table><thead><tr><th>日期 / 单号</th><th>施工地址</th><th>状态</th><th>施工金额</th><th>操作</th></tr></thead><tbody><tr v-for="order in rows" :key="order.id"><td>{{ order.work_date }}<small>{{ order.number }}</small></td><td class="address">{{ order.address }}</td><td>{{ order.status }}</td><td>{{ money(order.total_cents) }}</td><td><button type="button" class="link" :disabled="saving || loading || !!error" @click="select(order)">选择关联</button></td></tr><tr v-if="!rows.length && !loading"><td colspan="5" class="empty">{{ error ? '列表读取失败，请重试' : '没有符合条件的未关联施工单' }}</td></tr></tbody></table></div>
          <div class="association-pages"><button type="button" :disabled="page <= 1 || saving || loading" @click="load(page - 1)">上一页</button><span>共 {{ total }} 张 · 第 {{ page }} / {{ Math.max(1, Math.ceil(total / 100)) }} 页</span><button type="button" :disabled="page * 100 >= total || saving || loading" @click="load(page + 1)">下一页</button></div>
        </template>
        <footer><button type="button" :disabled="saving" @click="emit('close')">完成并关闭</button></footer>
      </div>
    </section>
  </div>
</template>
<style scoped>
.association-dialog{width:980px}.association-body{padding:18px 24px}.association-search{display:flex;gap:10px;align-items:end;padding:0!important;flex-wrap:wrap}.association-search label{flex:1;min-width:180px}.association-pages,.confirm-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin-top:14px}.association-pages span{flex:1}.address{white-space:normal;min-width:170px;max-width:360px;overflow-wrap:anywhere}.association-confirm{padding:14px;border:1px solid #d8e3f2;border-radius:6px;margin-top:14px}.association-confirm h3{margin-top:0}.association-confirm p{margin-top:8px;overflow-wrap:anywhere}.association-confirm .hint{padding:0}.error p{margin:8px 0}.error button{margin-top:8px}
@media(max-width:600px){.overlay{padding:10px}.association-body{padding:14px}.modal-head h2{font-size:18px}.association-pages span{order:-1;flex-basis:100%}.confirm-actions button{flex:1}}
</style>
