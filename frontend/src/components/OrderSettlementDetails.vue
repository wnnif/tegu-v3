<script setup lang="ts">
import { computed } from 'vue'
import { settlementMoney, type OrderSettlement } from '../settlements'
import { CUSTOMER_PRICE_HIDDEN, hasCustomerPrice } from '../customerPrices'
import { savedAttachmentLink } from '../attachment'
const props = defineProps<{ order: OrderSettlement }>()
const money = (value: unknown) => hasCustomerPrice(value) ? settlementMoney(value) : CUSTOMER_PRICE_HIDDEN
const receipts = computed(() => props.order.payment_receipts || [])
const attachmentLink = savedAttachmentLink
function attachmentLabel(value?: string) { return value?.startsWith('file:') ? '下载已保存附件' : value }
function date(value?: string) { return value ? value.slice(0, 10) : '—' }
</script>
<template>
  <section class="settlement-details no-print" aria-label="交票和收款记录">
    <h3>交票和收款记录</h3>
    <div class="settlement-totals"><span>施工金额 <strong>{{ money(order.total_cents) }}</strong></span><span>有效已收 <strong>{{ money(order.received_cents) }}</strong></span><span>剩余未收 <strong>{{ money(order.outstanding_cents) }}</strong></span></div>
    <dl class="ticket-details"><div><dt>交付日期</dt><dd>{{ date(order.delivered_at) }}</dd></div><div><dt>收票人</dt><dd>{{ order.recipient || '—' }}</dd></div><div v-if="order.ticket_no"><dt>历史票据号</dt><dd>{{ order.ticket_no }}</dd></div><div v-if="order.ticket_attachment" class="ticket-attachment"><dt>历史票据附件</dt><dd><a v-if="attachmentLink(order.ticket_attachment)" :href="attachmentLink(order.ticket_attachment)" target="_blank" rel="noopener noreferrer">{{ attachmentLabel(order.ticket_attachment) }}</a><span v-else>{{ order.ticket_attachment }}</span></dd></div></dl>
    <p v-if="order.return_note" class="return-note"><strong>最近退回原因：</strong>{{ order.return_note }}</p>
    <div class="table-wrap"><table><thead><tr><th>到账日期</th><th>收款金额</th><th>方式</th><th>凭证号</th><th>附件</th><th>备注</th><th>记录状态</th></tr></thead><tbody><tr v-for="receipt in receipts" :key="receipt.id" :class="{ voided: !!Number(receipt.voided) }"><td>{{ date(receipt.received_at) }}</td><td>{{ money(receipt.amount_cents) }}</td><td>{{ receipt.method || '—' }}</td><td class="wrapping-cell">{{ receipt.voucher_no || '—' }}</td><td class="wrapping-cell"><a v-if="attachmentLink(receipt.attachment)" :href="attachmentLink(receipt.attachment)" target="_blank" rel="noopener noreferrer">{{ attachmentLabel(receipt.attachment) }}</a><span v-else>{{ receipt.attachment || '—' }}</span></td><td class="wrapping-cell">{{ receipt.note || '—' }}</td><td>{{ Number(receipt.voided) ? '已作废，不计入已收' : '有效' }}</td></tr><tr v-if="!receipts.length"><td colspan="7" class="empty">暂无逐次收款记录</td></tr></tbody></table></div>
    <p class="settlement-hint">作废记录保留供核对，不计入有效已收金额。收款和退回操作在“财务 → 应收应付”办理。</p>
  </section>
</template>
<style scoped>
.settlement-details{border:1px solid #e0e7f1;border-radius:6px;margin:8px 0 16px;padding:12px;min-width:0}.settlement-details h3{margin:0 0 10px}.settlement-totals{display:flex;flex-wrap:wrap;gap:8px 22px}.settlement-totals strong{margin-left:5px}.ticket-details{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;margin:14px 0}.ticket-details>div{min-width:0}.ticket-details dt{color:#78879b;font-size:12px}.ticket-details dd{margin:2px 0 0;overflow-wrap:anywhere}.ticket-attachment{grid-column:1/-1}.return-note{background:#fff3e9;padding:10px;margin-bottom:12px;overflow-wrap:anywhere;white-space:pre-wrap}.wrapping-cell{white-space:normal;min-width:100px;max-width:220px;overflow-wrap:anywhere}.voided{color:#778396}.settlement-hint{font-size:12px;color:#78879b;margin-top:10px}
@media(max-width:600px){.ticket-details{grid-template-columns:1fr 1fr}.settlement-totals{display:grid;gap:6px}.settlement-details{padding:10px}}
</style>
