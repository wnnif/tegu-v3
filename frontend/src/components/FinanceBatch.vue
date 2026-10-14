<script setup lang="ts">
import { ref, computed } from 'vue'
import { api, command } from '../api'
import { readAttachment } from '../attachment'
import { cents, parseReceiptAmount, settlementMoney as money, wageSettlementPayload, type FinanceOrder } from '../settlements'
const props = defineProps<{ orders: FinanceOrder[]; company: string; action: string; wages: boolean }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const rows = ref<FinanceOrder[]>(props.orders.map(row => ({ ...row }))), busy = ref(false), error = ref(''), submit = command()
const amountInput = ref('')
const receiptFile = ref<File | null>(null)
const receiptInput = ref<HTMLInputElement | null>(null)
function selectFile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0] || null
  receiptFile.value = file
  if (file) fields.value.attachment = ''
}
function removeFile() {
  receiptFile.value = null
  if (receiptInput.value) receiptInput.value.value = ''
}
const fields = ref({ received_at: new Date().toLocaleDateString('sv-SE'), method: '', voucher_no: '', attachment: '', note: '', delivered_at: new Date().toLocaleDateString('sv-SE'), recipient: '' })
const labels: Record<string, string> = { edit: '批量修改', submit: '提交待交单', deliver: '确认已交单', receive: '整单收款', receive_full: '整单收款', receive_partial: '部分收款', return: '退回修改', pay: '确认工资发放', 'undo-pay': '撤销工资发放' }
const receiving = computed(() => ['receive', 'receive_full', 'receive_partial'].includes(props.action))
const partial = computed(() => props.action === 'receive_partial')
const faceTotal = computed(() => rows.value.reduce((sum, row) => sum + cents(props.wages ? row.wage_cents : row.total_cents), 0n))
const remaining = computed(() => rows.value.reduce((sum, row) => sum + cents(row.outstanding_cents), 0n))
const priorReceived = computed(() => rows.value.reduce((sum, row) => sum + cents(row.received_cents), 0n))
const invalidSelection = computed(() => partial.value && rows.value.length !== 1)
const canCompleteZero = (row: FinanceOrder) => !partial.value && ['已交单', '部分收款'].includes(row.status) && cents(row.total_cents) === 0n && cents(row.received_cents) === 0n && cents(row.outstanding_cents) === 0n
const zeroCompletionCount = computed(() => receiving.value ? rows.value.filter(canCompleteZero).length : 0)
function validDate(value: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false
  const date = new Date(value + 'T00:00:00Z')
  return !Number.isNaN(date.getTime()) && date.toISOString().slice(0, 10) === value
}
async function save() {
  if (busy.value) return
  error.value = ''
  try {
    if (!rows.value.length) throw new Error('请先选择施工单')
    if (!props.wages && (!props.company || rows.value.some(row => row.company_id !== props.company))) throw new Error('请选择同一公司的施工单后再操作')
    if (invalidSelection.value) throw new Error('部分收款一次只能选择一张施工单')
    if (receiptFile.value && rows.value.length !== 1) throw new Error('上传附件时请一次只选择一张单据')
    if (receiptFile.value && fields.value.attachment.trim()) throw new Error('收款附件请选择上传文件或填写链接，两者只能选一种')
    if (receiving.value && !validDate(fields.value.received_at)) throw new Error('请填写有效的到账日期')
    if (receiving.value && rows.value.some(row => cents(row.outstanding_cents) <= 0n && !canCompleteZero(row))) throw new Error('所选单据中有无需继续收款的单据，请重新核对选择')
    if (props.action === 'return' && !fields.value.note.trim()) throw new Error('请填写退回原因')
    if (props.action === 'deliver' && (!fields.value.recipient.trim() || !validDate(fields.value.delivered_at))) throw new Error('请填写有效的交付日期和收票人')
    const amount = partial.value ? parseReceiptAmount(amountInput.value) : 0
    if (partial.value && BigInt(amount) > remaining.value) throw new Error('本次收款不能超过所选单据的剩余未收金额')
    if (props.action === 'edit') {
      if (rows.value.some(row => !row.address.trim() || !validDate(row.work_date))) throw new Error('请填写每张单据的工地地址和有效施工日期')
    }
    busy.value = true
    if (receiptFile.value) {
      const capability = await api<{enabled:boolean}>('/attachment-capability')
      if (!capability.enabled) throw new Error('当前账号没有上传财务附件的权限')
    }
    const receiptUpload = receiptFile.value ? await readAttachment(receiptFile.value) : undefined
    const payload = props.wages ? wageSettlementPayload(props.action, rows.value, fields.value.note) : props.action === 'edit'
      ? { company_id: props.company, orders: rows.value.map(row => ({ id: row.id, version: row.version, address: row.address.trim(), contact: row.contact.trim(), work_date: row.work_date, note: row.note })) }
      : { ...fields.value, receipt_file: receiptUpload, amount_cents: amount, action: props.action, company_id: props.company, orders: rows.value.map(row => ({ id: row.id, version: row.version })) }
    await submit(props.wages ? '/wage-settlement' : props.action === 'edit' ? '/finance-edit' : '/settle', payload)
    emit('saved')
  } catch (e) { error.value = e instanceof Error ? e.message : '操作失败，请稍后重试' }
  finally { busy.value = false }
}
</script>
<template>
  <Teleport to="body"><div class="overlay" @keydown.esc="!busy && emit('close')"><section class="modal wide finance-batch" role="dialog" aria-modal="true" aria-labelledby="finance-batch-title">
    <div class="modal-head"><h2 id="finance-batch-title">{{ labels[action] }}</h2><button type="button" :disabled="busy" @click="emit('close')" aria-label="关闭财务操作">×</button></div>
    <form @submit.prevent="save" autocomplete="off">
      <p v-if="receiving">选中 {{ rows.length }} 张 · 已收 {{ money(priorReceived) }} · {{ partial ? '可收剩余' : '本次到账合计' }} <strong>{{ money(remaining) }}</strong></p>
      <p v-else>选中 {{ rows.length }} {{ wages ? '笔' : '张' }} · {{ wages ? '工资合计' : '施工合计' }} {{ money(faceTotal) }}</p>
      <p v-if="receiving && !partial" class="notice">本次将按每张单据的剩余未收金额收清。下方到账日期、方式和凭证信息应用于本次选中的每张单据，请确认款项均已实际到账。</p>
      <p v-if="zeroCompletionCount" class="notice">其中 {{ zeroCompletionCount }} 张施工单金额为 0，本次只将其标记为已收款完成，不新增到账流水。</p>
      <p v-if="action === 'return'" class="error">退回后单据恢复“进行中”，已有有效收款记录会标记为作废并保留历史，已收金额将重新计算。此操作不会实际退还款项，请核实账务并填写原因。</p>
      <p v-if="action === 'deliver' && rows.length > 1" class="notice">下方交付日期和收票人应用于所有选中单据；不同日期或收票人请分开办理。</p>
      <div v-if="error" class="error" role="alert">{{ error }}<p>本次填写已保留。若提示单据已被修改，请取消窗口、刷新列表并重新选择后核对。</p></div>
      <p v-if="invalidSelection" class="error" role="alert">部分收款只能选择一张施工单，请取消后重新选择。</p>
      <fieldset :disabled="busy">
        <div v-if="receiving" class="form-grid receipt-fields">
          <label v-if="partial">本次收款（元） *<input v-model="amountInput" type="text" inputmode="decimal" maxlength="18" placeholder="如：1234.50" required><small>最多两位小数，不超过 {{ money(remaining) }}</small></label>
          <label>到账日期 *<input v-model="fields.received_at" type="date" required></label><label>收款方式<input v-model="fields.method" maxlength="40" placeholder="如：转账、现金"></label><label>凭证号<input v-model="fields.voucher_no" maxlength="100"></label><div class="attachment-picker"><label>上传收款凭证<input ref="receiptInput" type="file" accept="image/png,image/jpeg,image/webp,application/pdf" :disabled="rows.length !== 1 || zeroCompletionCount > 0" @change="selectFile($event)"><small v-if="rows.length !== 1">上传时请逐张办理</small></label><button v-if="receiptFile" type="button" @click="removeFile()">移除文件</button></div><label>附件链接或说明<input v-model="fields.attachment" maxlength="500" :disabled="!!receiptFile" placeholder="已有外部链接或说明"></label><label>收款备注<textarea v-model="fields.note" maxlength="1000" rows="2"></textarea></label>
        </div>
        <div v-if="action === 'deliver'" class="form-grid receipt-fields"><label>交付日期 *<input v-model="fields.delivered_at" type="date" required></label><label>收票人 *<input v-model="fields.recipient" required maxlength="100"></label></div>
        <label v-if="action === 'return'" class="return-reason">退回原因 *<textarea v-model="fields.note" rows="3" maxlength="1000" required placeholder="说明退回原因及已收款项的核对情况"></textarea></label>
        <div class="table-wrap"><table><thead><tr><th>单号</th><th>工地地址</th><th v-if="wages">施工人</th><template v-if="action === 'edit'"><th>项目经理</th><th>施工日期</th><th>备注</th></template><template v-else><th>当前状态</th><th>{{ wages ? '工资金额' : '施工金额' }}</th><template v-if="!wages"><th>已收</th><th>{{ receiving ? '剩余未收 / 本次可收' : '剩余未收' }}</th></template></template></tr></thead><tbody><tr v-for="row in rows" :key="row.id"><td>{{ row.number }}</td><template v-if="action === 'edit'"><td><input v-model="row.address" maxlength="300" required aria-label="工地地址"></td><td><input v-model="row.contact" maxlength="100" aria-label="项目经理"></td><td><input v-model="row.work_date" type="date" required aria-label="施工日期"></td><td><input v-model="row.note" maxlength="10000" aria-label="备注"></td></template><template v-else><td class="address-cell">{{ row.address }}</td><td v-if="wages">{{row.worker_name||'—'}}</td><td>{{ wages ? (Number(row.wage_paid) ? '已发放' : '待发放') : row.status }}</td><td>{{ money(wages ? row.wage_cents : row.total_cents) }}</td><template v-if="!wages"><td>{{ money(row.received_cents) }}</td><td>{{ money(row.outstanding_cents) }}</td></template></template></tr></tbody></table></div>
      </fieldset>
      <footer><button type="button" :disabled="busy" @click="emit('close')">取消</button><button class="primary" :disabled="busy || !rows.length || invalidSelection">{{ busy ? '保存中…' : action === 'edit' ? '保存全部修改' : labels[action] }}</button></footer>
    </form>
  </section></div></Teleport>
</template>
<style scoped>
td input{width:170px}td:first-child{white-space:normal;min-width:150px;max-width:180px;overflow-wrap:anywhere}.address-cell{max-width:260px;min-width:150px;white-space:normal;overflow-wrap:anywhere}.receipt-fields,.return-reason{margin:16px 0}.receipt-fields textarea,.return-reason textarea{width:100%;resize:vertical}.attachment-picker{min-width:0}.attachment-picker label{display:block}.attachment-picker input{width:100%;min-width:0}.attachment-picker button{margin-top:5px}.error p{margin-top:6px}footer{flex-wrap:wrap}
@media(max-width:600px){.overlay{padding:10px}.finance-batch{width:100%}.modal-head h2{font-size:18px}}
</style>

