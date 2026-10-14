<script setup lang="ts">
import { computed, ref } from 'vue'
import { api, command } from '../api'
import { isProjectConflict, projectError, type ProjectDetail, type ProjectRecord } from '../projects'

const props = defineProps<{ project: ProjectRecord }>()
const emit = defineEmits<{ close: []; saved: [id: string] }>()
const submit = command()
const draft = ref({
  id: props.project.id, version: Number(props.project.version),
  company_id: props.project.company_id,
  name: props.project.name, address: props.project.address, action: '',
})
const saving = ref(false), refreshing = ref(false), error = ref(''), latest = ref<ProjectRecord | null>(null)
const busy = computed(() => saving.value || refreshing.value)
async function save() {
  if (busy.value) return
  error.value = ''
  const payload = { ...draft.value, name: draft.value.name.trim(), address: draft.value.address.trim() }
  if (!payload.id) { error.value = '未找到工地资料，请关闭表单后重新打开'; return }
  if (!payload.name || !payload.address) { error.value = '请填写工地名称和施工地址'; return }
  if ([...payload.name].length > 200 || [...payload.address].length > 300) { error.value = '工地名称最多 200 字，施工地址最多 300 字'; return }
  saving.value = true
  try { const result = await submit('/project', payload) as { id: string }; emit('saved', result.id) }
  catch (e) { error.value = projectError(e) }
  finally { saving.value = false }
}
async function readLatest() {
  if (busy.value || !draft.value.id) return
  refreshing.value = true
  try { const result: ProjectDetail = await api('/projects?' + new URLSearchParams({ id: draft.value.id, page: '1' })); latest.value = result.project }
  catch (e) { error.value = projectError(e) }
  finally { refreshing.value = false }
}
function useLatest() {
  if (!latest.value || busy.value) return
  draft.value = { id: latest.value.id, version: Number(latest.value.version), company_id: latest.value.company_id, name: latest.value.name, address: latest.value.address, action: '' }
  latest.value = null; error.value = ''
}
</script>

<template>
  <div class="overlay" @keydown.esc="!busy && emit('close')">
    <section class="modal project-editor" role="dialog" aria-modal="true" aria-labelledby="project-editor-title">
      <div class="modal-head"><h2 id="project-editor-title">更正工地资料</h2><button type="button" :disabled="busy" aria-label="关闭工地资料表单" @click="emit('close')">×</button></div>
      <form @submit.prevent="save">
        <div v-if="error" class="error" role="alert">{{ error }}<p>填写内容已保留。</p><button v-if="isProjectConflict(error)" type="button" :disabled="busy" @click="readLatest">{{ refreshing ? '读取中…' : '查看最新资料' }}</button></div>
        <div v-if="latest" class="notice"><strong>当前已保存的工地资料</strong><p>{{ latest.name }} · {{ latest.status }}</p><p>{{ latest.address }}</p><p>选择下方按钮会替换本表单中未保存的名称和地址，请核对后重新填写。</p><button type="button" :disabled="busy" @click="useLatest">使用最新资料重新填写</button></div>
        <fieldset :disabled="busy" class="form-grid">
          <label>所属公司<input :value="project.company_name" disabled><small>工地所属公司不可更换。</small></label>
          <label>工地名称 *<input v-model="draft.name" maxlength="200" required placeholder="便于识别工地的名称"></label>
          <label class="address">施工地址 *<textarea v-model="draft.address" maxlength="300" rows="3" required placeholder="填写具体施工地址"></textarea></label>
        </fieldset>
        <p class="notice">更正用于以后开单时带入资料；已经保存的施工单地址和金额保留原记录。</p>
        <footer><button type="button" :disabled="busy" @click="emit('close')">取消</button><button class="primary" :disabled="busy">{{ saving ? '保存中…' : '保存更正' }}</button></footer>
      </form>
    </section>
  </div>
</template>

<style scoped>
.project-editor{width:680px}.address{grid-column:1/-1}.notice p,.error p{margin:6px 0}.notice button,.error button{margin-top:8px}textarea{resize:vertical;width:100%}footer{flex-wrap:wrap}
@media(max-width:600px){.overlay{padding:10px}.project-editor .form-grid{grid-template-columns:1fr}.modal-head h2{font-size:18px}}
</style>
