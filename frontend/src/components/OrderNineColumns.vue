<script setup lang="ts">
withDefaults(defineProps<{label?:string}>(),{label:'九列施工单'})
const columns=['施工人员','工艺','部位','单价','方量明细','方量小计','金额小计','备注','合计金额']
</script>
<template>
  <div class="order-grid-scroll order-nine-columns">
    <div class="order-grid" role="table" :aria-label="label" aria-colcount="9">
      <div class="order-grid-head" role="row">
        <strong v-for="column in columns" :key="column" role="columnheader">{{column}}</strong>
      </div>
      <slot />
    </div>
  </div>
</template>
<style src="../orderNineColumns.css"></style>
