<script setup lang="ts">
import {ref,onMounted} from 'vue'
import {api,command} from '../api'
const title=ref(''),company=ref(''),busy=ref(false),error=ref(''),notice=ref(''),submit=command()
onMounted(async()=>{try{const s=await api<{title:string;company:string}>('/print-settings');title.value=s.title;company.value=s.company}catch(e){error.value=(e as Error).message}})
async function save(){busy.value=true;error.value='';notice.value='';try{await submit('/print-settings',{title:title.value,company:company.value});notice.value='打印设置已保存'}catch(e){error.value=(e as Error).message}finally{busy.value=false}}
</script>
<template><details class="print-settings"><summary>验收单打印设置</summary><form class="toolbar" @submit.prevent="save" autocomplete="off"><label>打印标题<input v-model="title" maxlength="100" required></label><label>施工单位<input v-model="company" maxlength="100" required></label><button :disabled="busy">保存打印设置</button></form><p v-if="error" class="error">{{error}}</p><p v-if="notice" class="notice">{{notice}}</p></details></template>
