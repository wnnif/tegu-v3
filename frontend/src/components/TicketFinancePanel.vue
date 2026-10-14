<script setup lang="ts">
import {ref} from 'vue'
import ReceiptManagementPanel from './ReceiptManagementPanel.vue'
defineProps<{canWages:boolean;canCustomerPrices:boolean;canManage?:boolean}>()
const panel=ref<InstanceType<typeof ReceiptManagementPanel>|null>(null)
defineExpose({refresh:()=>panel.value?.refresh()})
</script>
<template><ReceiptManagementPanel ref="panel" :can-finance="true" :can-customer-prices="canCustomerPrices" :can-manage="!!canManage"/></template>
