<script setup lang="ts">
import {customerMoney} from '../customerPrices'
import type {Numeric} from '../workflows/models'
defineProps<{amount?:Numeric;canCustomerPrices:boolean}>()
</script>
<template>
  <div v-if="canCustomerPrices" class="order-total-scroll" aria-label="整张施工单客户收费合计">
    <div class="order-total-row" role="row">
      <span class="order-total-label" role="cell">整张施工单客户收费合计（含售料）：</span>
      <strong class="order-total-value" role="cell">{{customerMoney(amount,canCustomerPrices)}}</strong>
    </div>
  </div>
</template>
<style src="../orderNineColumns.css"></style>
