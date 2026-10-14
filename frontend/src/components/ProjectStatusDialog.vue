<script setup lang="ts">
import { computed, ref } from 'vue'
import { api, command } from '../api'
import { isProjectConflict, projectError, projectMoney, type ProjectAction, type ProjectDetail, type ProjectRecord } from '../projects'
const props = defineProps<{ project: ProjectRecord; action: ProjectAction; canCustomerPrices: boolean; canFinance?: boolean }>()
const emit = defineEmits<{ close: []; saved: [id: string] }>()
const current = ref({ ...props.project }), saving = ref(false), refreshing = ref(false), error = ref('')
const submit = command()
const label = computed(() => props.action === 'close' ? '结项' : props.action === 'disable' ? '停用' : '重新启用')
const unchanged = computed(() => current.value.status === (props.action === 'close' ? '已结项' : props.action === 'disable' ? '已停用' : '进行中'))
async function save() {
  if (saving.value || refreshing.value || unchanged.value) return
  saving.value = true; error.value = ''
  try {
    await submit('/project', { id: current.value.id, version: Number(current.value.version), company_id: current.value.company_id, name: current.value.name, address: current.value.address, action: props.action })
    emit('saved', current.value.id)
  } catch (e) { error.value = projectError(e) }
  finally { saving.value = false }
}
async function refresh() {
  if (saving.value || refreshing.value) return
  refreshing.value = true
  try { const result: ProjectDetail = await api('/projects?' + new URLSearchParams({ id: current.value.id, page: '1' })); current.value = result.project; error.value = '' }
  catch (e) { error.value = projectError(e) }
  finally { refreshing.value = false }
}
</script>
<template>
  <div class="overlay" @keydown.esc="!saving && !refreshing && emit('close')">
    <section class="modal status-dialog" role="dialog" aria-modal="true" aria-labelledby="project-status-title">
      <div class="modal-head"><h2 id="project-status-title">确认{{ label }}工地</h2><button type="button" aria-label="关闭状态确认" :disabled="saving || refreshing" @click="emit('close')">×</button></div>
      <div class="body">
        <div v-if="error" class="error" role="alert">{{ error }}<p v-if="isProjectConflict(error)">其他人可能已更正工地。请读取最新资料，核对后重新确认。</p><button v-if="isProjectConflict(error)" type="button" :disabled="saving || refreshing" @click="refresh">{{ refreshing ? '读取中…' : '读取最新工地状态' }}</button></div>
        <strong>{{ current.name }} · {{ current.number }}</strong><p>{{ current.company_name }} · {{ current.address }}</p><p>当前状态：{{ current.status }}；未收金额：{{ projectMoney(current.outstanding_cents, canCustomerPrices && !!canFinance) }}</p>
        <p v-if="action === 'disable'" class="notice">停用后不能在这个工地下新增施工单。各期单据和收款记录会保留，已有单据可继续办理交单、收款；需要新开单时可重新启用。</p>
        <p v-else-if="action === 'close'" class="notice">结项表示本工地施工结束，之后不能新增施工单。结项不会自动结清款项，现有未收款仍需在应收应付办理；需要追加施工时可重新启用。</p>
        <p v-else class="notice">重新启用后，工地恢复“进行中”，有制单权限的人员可以继续开单。历史施工单、收款记录和金额会保留。</p>
        <p v-if="unchanged" class="notice">最新状态已经完成此操作，可关闭窗口。</p>
        <footer><button type="button" :disabled="saving || refreshing" @click="emit('close')">取消</button><button class="primary" :disabled="saving || refreshing || unchanged" @click="save">{{ saving ? '保存中…' : '确认' + label }}</button></footer>
      </div>
    </section>
  </div>
</template>
<style scoped>
.status-dialog{width:640px}.body{padding:18px 24px}.body>p{margin-top:10px}.body strong{overflow-wrap:anywhere}.error p{margin:6px 0}.error button{margin-top:8px}footer{flex-wrap:wrap}
@media(max-width:600px){.overlay{padding:10px}.body{padding:14px}.modal-head h2{font-size:18px}}
</style>
