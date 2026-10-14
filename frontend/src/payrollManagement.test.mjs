import {test} from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {existsSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import ts from 'typescript'

const payrollSource=await readFile(new URL('./payroll.ts',import.meta.url),'utf8')
const payrollUrl='data:text/javascript,'+encodeURIComponent(ts.transpileModule(payrollSource,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
const payroll=await import(payrollUrl)

test('currency parsing stays in integer cents and rejects ambiguous, negative and zero payments',()=>{
 assert.equal(payroll.parsePayrollAmount('500'),50000)
 assert.equal(payroll.parsePayrollAmount('-12.34',{signed:true}),-1234)
 assert.throws(()=>payroll.parsePayrollAmount('1.239'),/最多保留两位小数/)
 assert.throws(()=>payroll.parsePayrollAmount('0'),/必须大于 0/)
 assert.throws(()=>payroll.parsePayrollAmount('-1'),/正数金额/)
 assert.equal(payroll.payrollMoney('-12550'),'-¥ 125.50')
 assert.throws(()=>payroll.buildPayrollAction({action:'create-advance',worker_id:'worker',amount:'100',advance_date:'2026-02-30',project_id:'',note:''}),/有效的预支日期/)
})

test('bonus actions use a separate payroll action namespace',()=>{
 assert.equal(payroll.bonusPayrollActionName('create-adjustment'),'bonus-create-adjustment')
 assert.equal(payroll.bonusPayrollActionName('reverse-adjustment'),'bonus-reverse-adjustment')
 assert.equal(payroll.bonusPayrollActionName('create-settlement'),'bonus-create-settlement')
 assert.equal(payroll.bonusPayrollActionName('reverse-settlement'),'bonus-reverse-settlement')
 assert.equal(payroll.bonusPayrollActionName('create-payment'),'bonus-create-payment')
 assert.equal(payroll.bonusPayrollActionName('reverse-payment'),'bonus-reverse-payment')
})

test('bonus ledger uses backend entry kinds and only allows reversing active manual entries',()=>{
 assert.equal(payroll.bonusEntryKindLabel('automatic'),'施工计提')
 assert.equal(payroll.bonusEntryKindLabel('manual'),'手动调整')
 assert.equal(payroll.bonusEntryKindLabel('reversal'),'更正冲销')
 assert.equal(payroll.bonusEntryCanReverse('manual'),true)
 assert.equal(payroll.bonusEntryCanReverse('manual','reversal-id'),false)
 assert.equal(payroll.bonusEntryCanReverse('automatic'),false)
 assert.equal(payroll.bonusEntryCanReverse('reversal'),false)
})

test('payroll CSV export keeps applied filters and excludes list pagination',()=>{
 const summaryFilters=new URLSearchParams({q:'张 三',page:'3'})
 const summaryURL=new URL(payroll.payrollExportUrl('/payroll-workers',summaryFilters),'http://localhost')
 assert.equal(summaryURL.pathname,'/api/payroll-workers')
 assert.equal(summaryURL.searchParams.get('q'),'张 三')
 assert.equal(summaryURL.searchParams.get('export'),'1')
 assert.equal(summaryURL.searchParams.has('page'),false)
 const detailFilters=new URLSearchParams({worker_id:'worker/1',tab:'deductions',from:'2026-10-01',to:'2026-10-12',project_id:'project 2',page:'4'})
 const detailURL=new URL(payroll.payrollExportUrl('/payroll',detailFilters),'http://localhost')
 assert.equal(detailURL.pathname,'/api/payroll')
 assert.equal(detailURL.searchParams.get('worker_id'),'worker/1')
 assert.equal(detailURL.searchParams.get('tab'),'deductions')
 assert.equal(detailURL.searchParams.get('from'),'2026-10-01')
 assert.equal(detailURL.searchParams.get('to'),'2026-10-12')
 assert.equal(detailURL.searchParams.get('project_id'),'project 2')
 assert.equal(detailURL.searchParams.get('export'),'1')
 assert.equal(detailURL.searchParams.has('page'),false)
 assert.equal(summaryFilters.get('page'),'3')
})

test('payroll history snapshots are formatted as text and missing snapshots stay explicit',()=>{
 const snapshot={amount_cents:50000,reason:'<script>alert(1)</script>'}
 assert.equal(payroll.payrollSnapshotText(snapshot),JSON.stringify(snapshot,null,2))
 assert.equal(payroll.payrollSnapshotText(null),'—')
})

test('confirmed deduction corrections must preserve a newly checked legal or agreed basis',()=>{
 const pending=payroll.buildPayrollAction({action:'edit-deduction',id:'d1',version:2,confirmed:false,amount:'25.00',deduction_date:'2026-10-10',project_id:'',reason:'更正事实',legal_basis:''})
 assert.equal(pending.legal_basis,'')
 assert.throws(()=>payroll.buildPayrollAction({action:'edit-deduction',id:'d1',version:2,confirmed:true,amount:'25.00',deduction_date:'2026-10-10',project_id:'',reason:'更正事实',legal_basis:''}),/合法或约定依据/)
 const confirmed=payroll.buildPayrollAction({action:'edit-deduction',id:'d1',version:2,confirmed:true,amount:'25.00',deduction_date:'2026-10-10',project_id:'',reason:'更正事实',legal_basis:'双方书面约定'})
 assert.equal(confirmed.legal_basis,'双方书面约定')
 assert.equal('confirmed' in confirmed,false)
})

test('settlement records sources once, deducts a confirmed advance, then records only actual salary paid',()=>{
 const selection={
  wages:[{id:'w1',order_id:'o1',number:'A-1',work_date:'2026-10-01',wage_cents:80000,delivery_wage_cents:20000,settled:false,version:4}],
  adjustments:[],deductions:[{id:'d1',deduction_date:'2026-10-02',amount_cents:5000,status:'confirmed',settled:false,version:2}],
  advances:[{id:'a1',advance_date:'2026-10-03',amount_cents:20000,allocated_cents:0,remaining_cents:20000,status:'active',version:3}],
 }
 assert.equal(payroll.payrollSettlementBase(selection),95000n)
 assert.equal(payroll.maxAdvanceAllocation(selection),20000n)
 const settlement=payroll.buildPayrollAction({action:'create-settlement',worker_id:'worker',settlement_date:'2026-10-12',note:'',selection,advance_allocation:'200.00'})
 assert.deepEqual(settlement,{action:'create-settlement',worker_id:'worker',settlement_date:'2026-10-12',note:'',wage_ids:['w1'],wage_versions:[{id:'w1',version:4}],adjustment_ids:[],deduction_ids:['d1'],deduction_versions:[{id:'d1',version:2}],advance_allocations:[{id:'a1',version:3,amount_cents:20000}]})
 const payable=payroll.payrollSettlementBase(selection)-20000n
 assert.equal(payable,75000n)
 const payment=payroll.buildPayrollAction({action:'create-payment',settlement_id:'s1',settlement_version:1,amount:'500.00',paid_date:'2026-10-12',method:'转账',note:''})
 assert.equal(payment.amount_cents,50000)
 assert.equal(payable-BigInt(payment.amount_cents),25000n)
})

test('prepayment larger than selected wages cannot create negative settlement wages',()=>{
 const selection={wages:[{id:'w1',order_id:'o1',number:'A-1',work_date:'2026-10-01',wage_cents:10000,delivery_wage_cents:0,settled:false}],adjustments:[],deductions:[],advances:[{id:'a1',advance_date:'2026-10-02',amount_cents:50000,allocated_cents:0,remaining_cents:50000,status:'active',version:1}]}
 assert.equal(payroll.maxAdvanceAllocation(selection),10000n)
 assert.throws(()=>payroll.buildPayrollAction({action:'create-settlement',worker_id:'worker',settlement_date:'2026-10-12',note:'',selection,advance_allocation:'101.00'}),/不能超过/)
 const bounded=payroll.buildPayrollAction({action:'create-settlement',worker_id:'worker',settlement_date:'2026-10-12',note:'',selection,advance_allocation:'100.00'})
 assert.deepEqual(bounded.advance_allocations,[{id:'a1',version:1,amount_cents:10000}])
 assert.equal(payroll.payrollCents(selection.advances[0].remaining_cents)-BigInt(bounded.advance_allocations[0].amount_cents),40000n)
})

test('zero-pay settlements keep selected payroll sources while an advance alone cannot settle',()=>{
 const advance={id:'a1',advance_date:'2026-10-02',amount_cents:10000,allocated_cents:0,remaining_cents:10000,status:'active',version:1}
 const advanceOnly={wages:[],adjustments:[],deductions:[],advances:[advance]}
 assert.equal(payroll.payrollSettlementCanConfirm(advanceOnly),false)
 const selection={
  wages:[{id:'w1',order_id:'o1',number:'A-1',work_date:'2026-10-01',wage_cents:10000,delivery_wage_cents:0,settled:false,version:1}],
  adjustments:[],deductions:[],advances:[advance],
 }
 assert.equal(payroll.payrollSettlementCanConfirm(selection),true)
 const settlement=payroll.buildPayrollAction({action:'create-settlement',worker_id:'worker',settlement_date:'2026-10-12',note:'',selection,advance_allocation:'100.00'})
 assert.deepEqual(settlement.advance_allocations,[{id:'a1',version:1,amount_cents:10000}])
 assert.equal(payroll.payrollSettlementBase(selection)-BigInt(settlement.advance_allocations[0].amount_cents),0n)
})

test('settlement selections cannot mix unlinked and different worksite sources',()=>{
 const wage=(id,project_id)=>({id,order_id:id,number:id,work_date:'2026-10-01',project_id,wage_cents:10000,delivery_wage_cents:0,settled:false})
 const sameProject={wages:[wage('a1','project-a'),wage('a2','project-a')],adjustments:[],deductions:[],advances:[]}
 const unlinked={wages:[wage('u1',''),wage('u2','')],adjustments:[],deductions:[],advances:[]}
 const mixedUnlinked={wages:[wage('u1',''),wage('a1','project-a')],adjustments:[],deductions:[],advances:[]}
 const mixedProjects={wages:[wage('a1','project-a'),wage('b1','project-b')],adjustments:[],deductions:[],advances:[]}
 assert.equal(payroll.payrollSettlementCanConfirm(sameProject),true)
 assert.equal(payroll.payrollSettlementCanConfirm(unlinked),true)
 assert.equal(payroll.payrollSettlementCanConfirm(mixedUnlinked),false)
 assert.equal(payroll.payrollSettlementCanConfirm(mixedProjects),false)
})

test('settlement reversal is offered only before any payment history exists',()=>{
 const settlement={status:'confirmed',legacy_paid:false,paid_cents:0,payments:[]}
 assert.equal(payroll.payrollSettlementCanReverse(settlement),true)
 assert.equal(payroll.payrollSettlementCanReverse({...settlement,payments:[{id:'p1',amount_cents:10000,reversed:true}]}),false)
 assert.equal(payroll.payrollSettlementCanReverse({...settlement,status:'voided'}),false)
 assert.equal(payroll.payrollSettlementCanReverse({...settlement,legacy_paid:true}),false)
})

test('only confirmed deductions, unpaid wages, unsettled adjustments and advances with balance are selectable',()=>{
 const base={id:'x'}
 assert.equal(payroll.payrollSourceSelectable('deductions',{...base,deduction_date:'2026-10-01',amount_cents:500,status:'pending'}),false)
 assert.equal(payroll.payrollSourceSelectable('deductions',{...base,deduction_date:'2026-10-01',amount_cents:500,status:'confirmed',settled:true}),false)
 assert.equal(payroll.payrollSourceSelectable('deductions',{...base,deduction_date:'2026-10-01',amount_cents:500,status:'confirmed'}),true)
 assert.equal(payroll.payrollSourceSelectable('adjustments',{...base,effective_date:'2026-10-01',amount_cents:500,reversal_of:'original'}),false)
 assert.equal(payroll.payrollAdjustmentCanReverse({...base,effective_date:'2026-10-01',amount_cents:500,reversal_id:'replacement'}),false)
 assert.equal(payroll.payrollSourceSelectable('wages',{...base,order_id:'o1',number:'1',work_date:'2026-10-01',wage_cents:400,delivery_wage_cents:100,settled:false}),true)
 assert.equal(payroll.payrollSourceSelectable('wages',{...base,order_id:'pending-order',number:'2',work_date:'2026-10-02',delivery_wage_cents:100,wage_configured:false,settled:false}),false)
 assert.equal(payroll.payrollSourceSelectable('wages',{...base,order_id:'zero-order',number:'3',work_date:'2026-10-03',wage_cents:0,delivery_wage_cents:0,wage_configured:true,settled:false}),true)
 assert.equal(payroll.payrollSourceSelectable('advances',{...base,advance_date:'2026-10-01',amount_cents:500,allocated_cents:500,remaining_cents:0,status:'active'}),false)
})

test('pending wages block settlement while an explicitly configured zero remains valid',()=>{
 const base={id:'w',order_id:'o',number:'1',work_date:'2026-10-01',delivery_wage_cents:0,settled:false}
 const selection={wages:[{...base,wage_configured:false}],adjustments:[],deductions:[],advances:[]}
 assert.equal(payroll.payrollSettlementCanConfirm(selection),false)
 assert.throws(()=>payroll.buildPayrollAction({action:'create-settlement',worker_id:'a',settlement_date:'2026-10-12',note:'',selection,advance_allocation:'0'}),/工价待配置/)
 const explicitZero={wages:[{...base,wage_cents:0,wage_configured:true}],adjustments:[],deductions:[],advances:[]}
 assert.equal(payroll.payrollSettlementCanConfirm(explicitZero),true)
})

test('payroll, bonus and dashboard views compile as Vue components with their actual templates',async()=>{
 for(const name of ['WageFinancePanel','PayrollDetailPanel','PayrollActionDialog','PayrollBonusPanel','DashboardPanel']){
  const path=new URL(`./components/${name}.vue`,import.meta.url)
  assert.ok(existsSync(fileURLToPath(path)),`${name} exists`)
  const {descriptor,errors}=parse(await readFile(path,'utf8'),{filename:name+'.vue'})
  assert.deepEqual(errors,[])
  const script=compileScript(descriptor,{id:'payroll-'+name})
  const template=compileTemplate({id:'payroll-'+name,source:descriptor.template.content,filename:name+'.vue',compilerOptions:{bindingMetadata:script.bindings}})
  assert.deepEqual(template.errors,[],`${name} template compiles`)
 }
})

test('bonus management stays in a separate annual ledger and payroll action family',async()=>{
 const panel=await readFile(new URL('./components/PayrollBonusPanel.vue',import.meta.url),'utf8')
 assert.ok(panel.includes("api<BonusPage>('/payroll-bonus?'+params)"))
 assert.ok(panel.includes("submit('/payroll-action',payload)"))
 assert.ok(panel.includes("type Tab='ledger'|'payments'"))
 assert.match(panel,/本年施工计提/)
 assert.match(panel,/本次奖金发放不能超过/)
 assert.match(panel,/丙纶奖金只支持手动登记/)
})
