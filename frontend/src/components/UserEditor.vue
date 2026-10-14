<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {api,SessionChangedError} from '../api'
import type {UserDraft,Craft,PageResult} from '../workflows/models'
const props=defineProps<{editing:UserDraft;categories:string[];permissionNames:Record<string,string>;busy:boolean;canAssignCombinations:boolean}>()
const emit=defineEmits<{save:[]}>()
const category=ref('全部'),query=ref(''),crafts=ref<Craft[]>([]),page=ref(1),total=ref(0),loading=ref(false),error=ref('')
const names=computed<Record<string,string>>(()=>({...props.permissionNames,orders_temporary:'临时工艺填写'}))
let generation=0
async function loadCrafts(next=1){
 const current=++generation;error.value='';crafts.value=[];total.value=0;page.value=next
 if(props.editing.role!=='employee'){loading.value=false;return}
 loading.value=true
 const params=new URLSearchParams({entity:'crafts',q:query.value.trim(),page:String(next)})
 if(category.value!=='全部')params.set('category',category.value)
 try{const result=await api<PageResult<Craft>>('/reference-options?'+params);if(current===generation){crafts.value=result.rows;total.value=result.total}}
 catch(e){if(current===generation&&!(e instanceof SessionChangedError))error.value=(e as Error).message}
 finally{if(current===generation)loading.value=false}
}
watch(()=>[props.editing.id,props.editing.role],()=>{category.value='全部';query.value='';void loadCrafts()},{immediate:true})
onBeforeUnmount(()=>{generation++})
function selectPage(checked:boolean){const ids=crafts.value.filter(c=>!c.company_id&&(!checked||Number(c.active))).map(c=>c.id);props.editing.crafts=checked?[...new Set([...props.editing.crafts,...ids])]:props.editing.crafts.filter(id=>!ids.includes(id))}
</script>
<template>
 <form @submit.prevent="emit('save')" autocomplete="off"><fieldset :disabled="busy">
  <div class="form-grid"><label>姓名<input v-model="editing.name" required></label><label>账号<input v-model="editing.username" required></label><label>角色<select v-model="editing.role"><option v-if="editing.role==='admin'" value="admin">系统管理员</option><option value="operator">普通管理员</option><option value="employee">员工</option></select></label><label>{{editing.id?'新密码（留空不改）':'初始密码'}}<input type="password" v-model="editing.password" :required="!editing.id" autocomplete="new-password"></label><label class="check"><input type="checkbox" v-model="editing.active">启用账号</label></div>
  <section v-if="editing.role==='operator'"><h3>独立收费操作授权</h3><label class="check"><input type="checkbox" v-model="editing.permissions" value="combo_pricing" data-permission="combo_pricing" aria-label="防水/丙纶组合免收" :disabled="!canAssignCombinations">防水/丙纶组合免收</label></section>
  <p v-if="editing.role!=='admin'" class="hint">组合免收改变客户收费，只有系统管理员可授予或撤销；普通管理员、员工默认没有此权。此权限不代替客户价格查看、双方工艺填写、工资或财务权限。</p>
  <template v-if="editing.role==='employee'">
   <h3>可用功能</h3><div class="checks"><label v-for="(name,p) in names" :key="p" class="check"><input type="checkbox" v-model="editing.permissions" :value="p" :data-permission="p" :aria-label="name" :disabled="p==='combo_pricing'&&!canAssignCombinations">{{name}}</label></div>
   <p class="hint">“查看客户价格”独立控制客户单价、小计、合计及客户金额文件；不授予施工、工艺、工资或财务功能。无此权限仍可填写已授权固定工艺的普通施工信息。</p>
   <h3>可填写的公共工艺</h3><p class="hint">公共工艺只需授权一次。各公司选用同一公共工艺后，该员工都可填写，无需按公司重复授权；未授权工艺只读。新增公共工艺需要另行授权。</p>
   <div class="toolbar"><label>施工大类<select v-model="category" :disabled="loading" @change="loadCrafts()"><option>全部</option><option v-for="item in categories" :key="item">{{item}}</option></select></label><input v-model="query" placeholder="搜索公共工艺名称或规格" aria-label="搜索可授权公共工艺" @keyup.enter.prevent="loadCrafts()"><button type="button" :disabled="loading" @click="loadCrafts()">查询</button><button type="button" :disabled="loading||!crafts.length" @click="selectPage(true)">勾选本页</button><button type="button" :disabled="loading||!crafts.length" @click="selectPage(false)">清除本页</button></div>
   <p v-if="error" class="error" role="alert">{{error}} <button type="button" @click="loadCrafts(page)">重试</button></p>
   <div class="craft-grants"><label v-for="craft in crafts" :key="craft.id" class="check"><input type="checkbox" v-model="editing.crafts" :value="craft.id" :disabled="!!craft.company_id||(!editing.crafts.includes(craft.id)&&!Number(craft.active))"><span>{{craft.category}} · {{craft.name}}<small>{{craft.spec}} · {{craft.mode}}{{!Number(craft.active)?' · 已停用':''}}</small></span></label></div>
   <p v-if="!crafts.length&&!error" class="hint">{{loading?'正在读取公共工艺…':'没有匹配的公共工艺，请先在工艺管理中设置。'}}</p>
   <div class="toolbar"><span>公共工艺 {{total}} 项 · 第 {{page}} / {{Math.max(1,Math.ceil(total/50))}} 页</span><button type="button" :disabled="loading||page<=1" @click="loadCrafts(page-1)">上一页</button><button type="button" :disabled="loading||page*50>=total" @click="loadCrafts(page+1)">下一页</button></div>
   <p class="hint">共授权 {{editing.crafts.length}} 项公共工艺，切换大类或翻页保留已勾选内容。停用工艺不能新增授权，已有授权可以取消。填写授权不授予工艺资料维护、客户价格或工资查看权；临时工艺另由“临时工艺填写”控制。</p>
  </template>
  <footer><button class="primary" :disabled="busy">{{busy?'正在保存…':'保存账号'}}</button></footer>
 </fieldset></form>
</template>
<style scoped>
.craft-grants{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:8px}.craft-grants label{border:1px solid #dce3eb;border-radius:6px;padding:10px;align-items:flex-start}.craft-grants small{display:block;color:#68768a;margin-top:4px}.toolbar input{min-width:0}@media(max-width:600px){.craft-grants{grid-template-columns:1fr}}
</style>
