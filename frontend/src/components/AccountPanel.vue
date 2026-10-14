<script setup lang="ts">
import {ref} from 'vue'
import {command} from '../api'
import type {LoginUser} from '../workflows/models'
const props=defineProps<{user:LoginUser;permissions:Record<string,string>}>()
const emit=defineEmits<{signedOut:[]}>()
const current=ref(''),password=ref(''),repeat=ref(''),busy=ref(false),error=ref(''),submit=command()
async function save(){if(busy.value)return;error.value='';if(password.value!==repeat.value){error.value='两次新密码不一致';return}busy.value=true;try{await submit('/account-password',{current:current.value,password:password.value});emit('signedOut')}catch(e){error.value=(e as Error).message}finally{busy.value=false}}
</script>
<template><section class="panel"><h2>{{user.name}} · {{user.username}}</h2><p>{{user.role==='admin'?'系统管理员':user.role==='operator'?'普通管理员':'员工'}}</p><div class="checks"><span v-for="p in (user.role==='employee'?user.permissions:Object.keys(permissions))" class="tag">{{permissions[p]||p}}</span></div><p>可填写工艺：{{user.role==='employee'?`${user.crafts.length} 项`:'全部公司固定工艺'}}</p><form autocomplete="off" @submit.prevent="save"><h3>修改本人密码</h3><div class="form-grid"><label>原密码<input type="password" v-model="current" :disabled="busy" required autocomplete="current-password"></label><label>新密码<input type="password" v-model="password" :disabled="busy" required autocomplete="new-password"></label><label>确认新密码<input type="password" v-model="repeat" :disabled="busy" required autocomplete="new-password"></label></div><p v-if="error" class="error">{{error}}</p><button class="primary" :disabled="busy">保存并重新登录</button></form></section></template>
