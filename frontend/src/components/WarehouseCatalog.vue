<script setup lang="ts">
import {ref,watch} from 'vue'
import type {CatalogRow as Item} from '../workflows/models'
const props=defineProps<{kind:'products'|'warehouses';items:Item[];workers:Item[];busy:boolean;error?:string;query:string;status:string;page:number;total:number}>()
const emit=defineEmits<{edit:[item?:Item];toggle:[item:Item];inventory:[id:string];transfer:[id:string];delete:[item:Item];filter:[query:string,status:string];page:[page:number]}>()
const queryDraft=ref(props.query)
watch(()=>props.query,value=>{queryDraft.value=value})
const worker=(id?:string)=>props.workers.find(w=>w.id===id)?.name||'未关联'
</script>

<template>
  <section class="panel">
    <div class="toolbar">
      <input v-model="queryDraft" @keyup.enter="emit('filter',queryDraft,status)" :placeholder="kind==='products'?'搜索商品、品牌、规格或颜色':'搜索仓库、分类或备注'" aria-label="搜索资料" autocomplete="off">
      <button @click="emit('filter',queryDraft,status)">查询</button>
      <label>显示范围<select :value="status" @change="emit('filter',queryDraft,($event.target as HTMLSelectElement).value)"><option value="1">启用</option><option value="0">停用</option><option value="">全部</option></select></label>
      <span>共 {{total}} 项</span>
      <button class="primary toolbar-end" :disabled="busy" @click="emit('edit')">＋ 新增{{kind==='products'?'商品':'仓库'}}</button>
    </div>
    <div class="table-wrap"><table><thead><tr>
      <th>{{kind==='products'?'商品名称 / 规格':'仓库名称 / 施工人'}}</th>
      <template v-if="kind==='products'"><th>品牌</th><th>颜色 / 重量</th><th>单位换算</th></template>
      <template v-else><th>分类</th><th>仓库类型</th></template>
      <th>备注</th><th>状态</th><th>操作</th>
    </tr></thead><tbody>
      <tr v-for="item in items" :key="item.id">
        <td>{{item.name}}<small>{{kind==='products'?(item.spec||'—'):(item.worker_id?'个人结余：'+(item.worker_name||worker(item.worker_id)):'独立仓库 / 总仓')}}</small></td>
        <template v-if="kind==='products'">
          <td>{{item.brand||'—'}}</td><td>{{item.color||'—'}}<small>{{Number(item.weight_kg)||'—'}} kg / {{item.unit}}</small></td>
          <td>1 {{item.unit}}<span v-if="item.aux_unit"> = {{item.aux_ratio}} {{item.aux_unit}}</span></td>
        </template>
        <template v-else><td>{{item.category||'默认分类'}}</td><td>{{item.warehouse_type||'普通仓库'}}</td></template>
        <td class="wrap-cell">{{item.note||'—'}}</td><td><span class="tag" :class="{'muted-tag':!Number(item.active)}">{{Number(item.active)?'启用':'停用'}}</span></td>
        <td><div class="row-actions">
          <button class="link" :disabled="busy" @click="emit('edit',item)">编辑</button>
          <button class="link" :disabled="busy" @click="emit('toggle',item)">{{Number(item.active)?'停用':'启用'}}</button>
          <template v-if="kind==='warehouses'"><button class="link" @click="emit('inventory',item.id)">查看库存</button><button class="link" :disabled="busy||!Number(item.active)" @click="emit('transfer',item.id)">调拨</button><button class="link" :disabled="busy" @click="emit('delete',item)">删除</button></template>
        </div></td>
      </tr>
      <tr v-if="!items.length"><td :colspan="kind==='products'?7:6" class="empty">{{busy?'正在读取资料…':error?'读取失败，请重试':'当前显示范围内暂无'+(kind==='products'?'商品':'仓库')}}</td></tr>
    </tbody></table></div>
    <div class="toolbar"><button :disabled="busy||page<=1" @click="emit('page',page-1)">上一页</button><span>第 {{page}} / {{Math.max(1,Math.ceil(total/50))}} 页</span><button :disabled="busy||page*50>=total" @click="emit('page',page+1)">下一页</button></div>
  </section>
</template>
