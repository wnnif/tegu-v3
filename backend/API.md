# 新系统国庆修改版 API 对照

## V3.0 第四阶段B：收款管理（本地源码，未发布）

本节优先于下方历史票内收款合同。`project_tickets` 仍是施工和商品收费的唯一来源；交票批次只记录纸票交付，不能另加应收。`company_receipts` 是独立真实到账，`project_ticket_receipts` 是这笔钱的工地分配，`customer_discounts` 是老板确认的独立减免。收到钱时即使没有工地或尚未交票，也能先登记并保留未分配。

所有读取、登记及分配都需 `finance` 和 `customer_prices`；更正/作废收款、确认/作废减免还须 `admin` 或 `operator` 角色。权限在幂等成功缓存重放前检查，撤权后不能用旧操作编号继续操作。写接口均带 `Idempotency-Key`，并在同事务保存金额变更、版本、审计和幂等；同编号同内容重试不重复记钱，同编号不同内容或新请求使用旧版本返回409。

| API | 请求及响应 |
| --- | --- |
| `GET /api/company-receipts` | `company_id/status=active或voided/unallocated=1/month=YYYY-MM/q/page` 均可选；月份按实际到账DATE，搜索公司快照名、凭证号或备注。返回 `{rows,total,page,page_size:50}`，默认全部状态/月份；`unallocated=1` 仅有效且有未分配款的收款。 |
| `GET /api/company-receipt?id=...` | 可加 `history_page`；返回 `{receipt,allocations,history,history_total,history_page,history_page_size:50}`。分配数组为当前有效分配，历史按版本倒序且保留更正前的分配与金额。 |
| `POST /api/company-receipt` | `{company_id,received_at,amount_cents,method,voucher_no,note}`；日期 `YYYY-MM-DD`、正整数分金额和方式必填，凭证号/备注可空；成功返回收款详情。无需工地、交票或分配。 |
| `POST /api/receipt-allocation` | `{receipt_id,version,reason,allocations:[{ticket_id,batch_id,amount_cents}]}`；替换该笔钱的完整当前分配清单，最多100处；`batch_id` 可空，空清单释放全部分配。须填原因，成功返回收款详情。 |
| `GET /api/receipt-targets` | `company_id` 必填，`project_id/page` 可选；返回 `{rows,total,page,page_size:50}`。每行 `id,project_id,company_id,number,address,total_cents,allocated_cents,discount_cents,outstanding_cents,batches`；批次含 `id,number,total_cents,available_cents`，只列同来源有效交票批次。 |
| `POST /api/company-receipt-action` | `{id,version,action:"correct"或"void",reason}`；`correct` 还需完整登记字段。作废用于误记/重复登记，保留记录并解除分配；更正公司会解除原公司全部分配，更正金额减少会释放超额分配。成功返回详情。 |
| `GET /api/customer-discounts` | `company_id/project_id/page` 可选，返回 `{rows,total,page,page_size:50}`，含有效与已作废减免。 |
| `GET /api/customer-discount?id=...` | 可加 `history_page`；返回 `{discount,history,history_total,history_page,history_page_size:50}`。 |
| `POST /api/customer-discount` | `{company_id,ticket_id,batch_id,amount_cents,reason}`；`batch_id` 可空，正金额和减免原因必填，管理员确认；成功返回减免详情。 |
| `POST /api/customer-discount-action` | `{id,version,action:"void",reason}`；撤销减免恢复尚未核销金额，保留历史；修改减免采用撤销原记录后重新确认。 |

收款对象含 `id,company_id,company_name,amount_cents,received_at,method,voucher_no,note,status,version,created_by_name,created_at,updated_at,void_reason,allocated_cents,unallocated_cents`；公司名保存到账时快照。分配对象含 `id,receipt_id,ticket_id,batch_id,project_id,address,number,batch_number,amount_cents,voided,created_at`。历史含 `version,event,reason,actor_name,created_at,snapshot`，`snapshot` 是JSON对象。金额单次上限100000000000000分；方式最多40字、凭证号100字、备注1000字，操作/减免原因1至1000字。

一笔到账可以分给同公司多个工地、多个有效交票批次，也可以以后继续调整。每个 `ticket_id+batch_id` 在一次清单中只能出现一次。分配合计不得超过真实到账，工地分配加减免不得超过有效收费，指定批次时还须核对同公司、该收费来源确实在批次中、批次未作废以及批次尚未核销金额；不能跨公司或因分配多次重复累计到账。

### 公司欠款和工地尚未核销

`GET /api/finance-companies?company_id=&status=欠款或结清或预收&q=&page=1` 返回公司累计全部月份余额，**不接受非空 `month`**；到账月份仅在收款列表筛选。按公司ID分组，同名不合并，没有工地但有预收的公司也能查看；公司目录中的无业务公司可返回0金额。返回 `{rows,total,page,page_size:100,balance_scope:"累计全部月份",...十项完整筛选金额合计}`，每行除公司ID/名称、`ticket_count` 外有相同十项金额字段。行和顶层合计的金额都为十进制整数分 **JSON字符串**，避免多笔累计超过JavaScript精确整数范围；单笔收款、分配、减免和目标接口仍为整数分数值。

| 字段 | 页面名称和口径 |
| --- | --- |
| `total_cents` | 累计收费：有效施工和商品收费 |
| `delivered_cents` | 已交票金额：有效实际行认领及历史真实交票；不能依据收款/结清状态猜交票 |
| `undelivered_cents` | 尚未交票金额：累计收费减已交票 |
| `received_cents` | 实际到账：所有有效真实收款，包括预收和未分配 |
| `allocated_cents` | 已分配收款：有效工地分配 |
| `unallocated_cents` | 未分配收款：实际到账减已分配 |
| `discount_cents` | 客户确认减免：独立有效减免，不算到账 |
| `unsettled_cents` | 工地尚未核销：收费减减免减已分配 |
| `outstanding_cents` | 公司实际仍需收回：`max(收费-减免-实际到账,0)` |
| `advance_cents` | 公司预收余额：`max(实际到账+减免-收费,0)` |

例如收费1500元、到账1000元、已分配700元，没有减免时，未分配为300元、工地尚未核销为800元、公司实际待收为500元。不能把800元当公司实际欠款。公司汇总同时兼容仅查询的历史非工地票资料，一份收费/收款只纳入一次；关联到收款头的分配不再作为另一笔到账累加。`export=1` 导出全部匹配公司，不按当前页截断，金额为元；服务端在同一只读快照完成全部筛选合计和分页。

工地总表及工地票的 `received_cents` 为兼容字段，含义是该工地**已分配收款**，`outstanding_cents` 是**工地尚未核销**，已扣有效减免；公司实际到账和实际待收以公司汇总为准。工地同时提供 `allocated_cents,discount_cents,delivered_cents,undelivered_cents,unsettled_cents` 等适用字段，后端依财务/客户金额权限过滤。客户减免不得超过尚未核销收费；施工免费、补做或组合免收已归零的收费不能再次减免。原收费10000元、付款分配9000元，不同意减免仍有1000元；确认1000元减免才能结清，到账保持9000元。

### 作废重开、收费下降和旧入口

作废交票批次只解除收款分配/减免的批次标签，工地分配、减免和真实到账保留，并保存调整历史。施工纠错减少有效收费时，先调整超过收费的减免，再由最新分配开始释放超额部分，原分配行保留作废记录，收款头金额不变。收费1000元、到账并分配600元，更正收费500元后为分配500元、未分配100元；重开纸票不会新生成一份应收或一笔收款。其他正常批次和施工工资/耗材/库存不因交票、收款再次计算。

旧 `/api/project-ticket-action` 的 `deliver/receive/void_receipt`、`/api/project-ticket-batch` 的旧交票/收齐，以及 `/api/settle` 的所有写操作，在通过对应模块权限检查后均返回409；无权请求仍返回403。工资写入请使用 `/api/payroll-action`，收款和交票请使用各自的V3管理接口。历史金额和记录仍可查询。

迁移445新增四张真实收款/减免与版本表、分配的 `receipt_id/batch_id` 和索引，并纳入备份。旧票收款按原ID逐笔转为真实到账头，不按同金额/同日期猜合并；状态/公司/金额/日期不一致拒绝409。旧备份恢复事务也执行逐笔回填，重复执行不新增重复到账。历史非工地票 `payment_receipts` 仅兼容查询。月归档的收款流水使用真实到账日期及收款头，票据应收标明“已分配/尚未核销”并保留 `finance_scope:"allocation"`，真实到账合计另用 `cash_received_cents`；旧归档不回写。

实现合同见 `.trellis/spec/backend/v3-phase4b-receipts.md`。4B的真实数据库、并发、浏览器和编译结果以 `.trellis/tasks/10-12-v3-phase4b-receipts/validation.md` 为准；接口存在不代表已通过目标环境验收。本轮不推送、不部署，不进入第五阶段。

## V3.0 第四阶段A：交票管理（本地源码，未发布）

交票批次是实际收费明细的纸票交付记录，不是新增应收账本。施工保存仍维护唯一的 `project_tickets` 应收来源及原有效收款；批次合计仅供打印和核对，不可与工地票金额再次相加。本阶段没有新增实际到账、预收、未分配收款、跨工地分配或减免管理。

四个入口均须 `finance` 和 `customer_prices`，服务端独立检查；写接口沿用 `Idempotency-Key` 和同事务审计。当前权限检查在幂等成功重放前执行，撤权不能凭旧键重放操作。

| API | 请求及响应 |
| --- | --- |
| `GET /api/ticket-issue` | `company_id` 必填，支持 `q/page/page_size/batch_page/batch_page_size`；返回 `company/rows/reminders/batches/total/page/page_size/batch_total/batch_page/batch_page_size`。施工/销售分页默认100、最大500；历史批次独立分页默认20、最大100。 |
| `POST /api/ticket-batch` | `{company_id,items:[{item_kind:"line"或"sale",item_id,order_version}],delivered_at,recipient,ticket_no}`；确认纸票实际交付，成功返回 `{batch,items}`。日期必填，收票人1至100字，纸票号可选且最多100字。选择1至500个完整实际行并带候选读取的订单版本，不能带部分数量、比例或客户端金额。 |
| `GET /api/ticket-batch?id=...` | 读取批次当前版本的不可变内容快照 `{batch,items}`；作废后仍保留原数量、金额及来源，批次状态/原因/操作人反映作废结果。 |
| `POST /api/ticket-batch-action` | `{id,version,action:"void",reason}`；原因1至1000字，版本或已作废冲突409。只解除本批次实际行指针，保留明细/版本/审计，不撤销施工、库存、工资或收款。纠错后重新选择生成新批次。 |

候选 `rows` 以 `item_kind+item_id` 唯一识别，含施工单与 `order_version`、日期、工地/地址、工艺、配置小项 `subitem_name`、实际位置 `location`、实际数量/单位及整数分金额。状态 `pending` 可选；`delivered` 禁止重复交票；`unlinked` 表示应收关联缺失、重复或归属异常，显示“关联待核对”且不可选。旧非 `ticket_managed` 记录不自动转新票；旧已交工地票明细仍不可再次交票。

确认时服务端按工地→订单→实际行稳定顺序加锁，重新读取整行收费，核对订单版本、同公司、未取消、无现行批次且存在唯一准确的工地应收来源；跨工地同公司可合批，不同日期的实际行独立。预览/打印后原订单发生修改，则整批409，须刷新核对后重新打印，不能把新内容确认到旧纸票。有效认领用 nullable `order_lines/order_sales.ticket_batch_id` 和条件更新保护。免费/补做实际施工数量保留，客户金额0，不虚增收费。打印和预览只使用已读快照，不调用写接口。

`reminders` 按公司当前固定工艺/配置小项及稳定工地ID统计，`no_work/pending/delivered/mixed` 分别表示尚无施工、待交、已交、两种状态共存；提供 `total_count/pending_count/delivered_count`。配置变更后不按同名猜映射，无法对应的实际施工另列 `unmapped:true`，当前配置仍可显示尚无记录。提醒不参与确认阻断条件，不设置固定交票周期。

施工详情 `lines[].ticket_claimed` 表示有效批次认领，此时 `editable=false`；售料也返回 `editable=false`。共同资料/整单取消受保护；同单未交或已作废行仍走原保存和库存差额事务。隐藏/省略已交施工行由后端保留；提交修改拒绝409，不能通过省略删除。已交售料不能删除或改数量/单位/价格/来源；只删除重插未认领售料，保留认领指针和排序。已交施工行耗材及组合免收目标独立保护，其他实际行及其批次不改。原任何人工资已发的整单保护继续有效。

旧 `POST /api/project-ticket-action` 和 `/api/project-ticket-batch` 的 `deliver` 统一拒绝409并指向交票管理，避免绕过实际行选择；第四阶段B还停用原票内收款/撤销写入口，历史查询保留。旧工地票状态不是新批次交票状态，应从实际交票事实查看交付情况。

迁移444在443后添加 `ticket_batches/ticket_batch_items/ticket_batch_versions` 及两实际表认领字段/索引，并加入备份清单；不重算旧账或自动生成交票。当前批次认领由应用事务、锁和条件更新维护，不声称实际行指针具备数据库外键约束。旧备份缺新字段使用NULL，缺新表为空；新备份不能直接交给旧程序恢复。第四阶段B开始前已在本项目新40971隔离MySQL完成444新建/升级、合批、重复/并发认领、作废更正重开及其他批次保护的定向验证；浏览器打印触发前后数据无变化，纸面版式未验收。4A本地恢复提交为 `830298e`，未推送；分层证据见 `.trellis/tasks/10-12-v3-phase4a-ticket-management/validation.md`。

## V3.0 第三阶段：施工单与工地总表（本地开发，未发布）

继续使用 `POST /api/order`、`GET /api/order?id=...` 和分页 `GET /api/project-work?project_id=...`；没有新增送货制单接口或工地专用编辑路径。工地总表读取各次独立施工单及现有票据/收款基础，编辑仍返回原施工单。实际验证见第三阶段任务记录，未运行 NAS、Docker、云或生产验收。

- `lines[].charge_mode` 为 `normal/free/redo`，新增省略按 `normal`，编辑省略/空值保留原状态。`free/redo` 只将客户 `total_cents` 归零，保留实际尺寸、数量、人员、标准工资及实际工资；普通保存和防水丙纶组合设置/解除都保持零客户收费。改收费状态须 `customer_prices`，无客户价格权限的响应同时省略 `charge_mode`；固定工艺价格仍由服务端公司配置或已存快照取得，客户端不能改价。
- `lines[].wage_override_cents` 为可选实际行工资（整数分、0至100000000000000）；非空覆盖该行 `labor_cents`，省略保留已存覆盖，显式 `null` 恢复保存的标准工资单价与实际数量计算。新增覆盖、覆盖变化/解除及已有临时项工资单价变化须 `wages` 和非空 `wage_adjustment_reason`（最多1000字）。普通数量变化按标准工资计算，不自动产生人工调整；未改变覆盖的备注保存保留原调整原因。
- 人工工资调整在同一施工保存事务追加 `order_line_wage_adjustments`，保存前/后/标准工资、人员姓名快照、原因、操作人、用户命名空间操作编号和时间。`GET /api/order` 仅向有 `wages` 权限者返回行内及整单 `wage_adjustments`，元素为 `{id,line_id,worker_name,line_deleted,before_cents,after_cents,reason,actor_name,created_at}`；已删行仍可从整单历史查看。无工资权限省略覆盖、原因、历史、工资和工资账户字段；历史票据版本同样过滤。撤销工资权限后，旧幂等键也不能重放工资调整成功。
- `materials[].line_id` 为空表示整单公共耗材；非空必须属于本次最终实际施工行。新行可由同请求 `lines[].client_ref` 引用，服务端映射为生成的行ID后保存；未知、跨单或已删除行拒绝，删行必须在同事务明确移除或重新归属其耗材。返回材料保留 `line_id/product_id/warehouse_id/qty_quantum/input_qty/input_unit/conversion_ratio` 及名称/单位快照。
- 耗材唯一键为 `order_id+line_id+product_id+warehouse_id`：同商品可在不同施工明细、公共耗材及不同来源登记，同一个关联行/商品/来源组合不可重复。继续使用第二阶段精确双单位和既有库存事务，按仓库/商品汇总实际前后差额；施工耗材与销售分别记录流水来源类型，不能再次按行重复扣库。其他来源仍需 `warehouse` 权限；纯商品销售允许 `lines=[]`，仍须公司、工地、送货责任人和实际库存来源，客户销售金额计入整单一次。
- 原有版本冲突、已交票、任一人工资已发、只读工艺/共同资料和组合保护继续适用。隐藏只读行不会删除它；最终保存只处理实际提交行与服务端保留的只读快照，空白公司工艺由前端排除。
- 迁移443在442之后添加收费/工资覆盖快照、材料归属字段/复合主键和工资调整表，并纳入备份。现有行默认 `normal`、覆盖空、原因空；旧耗材默认空 `line_id`，不重算旧收费、工资、数量或姓名。旧格式备份恢复时缺新字段使用这些默认值，缺新表视作0条；新备份保留调整历史和材料归属。不能将含新字段/新表的备份直接恢复到旧二阶段程序。

## V3.0 第二阶段：商品与库存管理（当前源码合同，未替代实测报告）

本节描述当前第二阶段代码已经提供的 HTTP 入口、字段和边界。它是实现合同，不等于本轮已经完成独立数据库、浏览器或 NAS 验收；实际验证结果必须另见任务记录。第二阶段不连接线上服务器、不修改第三阶段施工制单页面之外的票据/工资业务，也不把编译或静态检查写成业务通过。

### 商品、单位和精确数量

- `GET /api/catalog?entity=products`、`GET /api/reference?tables=products` 和 `GET /api/reference-options?entity=products` 返回商品的 `id/name/unit/aux_unit/aux_ratio/active/version` 及规格、品牌、颜色、重量和备注（具体读取权限沿既有目录规则）。
- `POST /api/entity` 的 `entity="products"` 使用商品表单保存商品。请求字段为 `id/version/name/unit/aux_unit/aux_ratio/active/color/weight_kg/brand/spec/note`；商品维护需要 `warehouse` 权限。基本单位必填，辅助单位可空；比例是大于0、最多6位小数的十进制文本，不能靠浮点舍入保存。
- 商品单位定义同时写入 `product_unit_definitions`：`base_unit`、`aux_unit`、`base_factor`、`aux_factor`、`quantity_scale=6`。例如 `1桶=6小组` 使用 `base_factor=6, aux_factor=1`。内部权威数量是整数 `quantity_quantum`，含义为“所选单位数量×对应因子×1,000,000”；大整数在 JSON 中使用字符串。
- `qty_micro`/`delta_micro` 及订单中的旧微量字段仅为兼容和展示字段，不能再次参加换算。输入若同时带 `quantity_quantum` 和数量/单位，服务端要求两者完全一致；无法精确表示、溢出或负数直接拒绝。
- 已有库存流水的商品不能修改单位、辅助单位或换算比例。这样可以保证历史快照不会因基础资料编辑而改变；需要新比例时应建立新商品或先处理业务允许的历史边界。

### 库存位置、单据和权限

仓库表中的每个位置由稳定 `warehouse_id` 标识。老板总仓是没有 `worker_id` 的仓库；施工人自动建立或手动关联的仓库代表个人材料结余，即使没有实体仓库也可以记录余额。仓库/工人目录维护沿 `warehouse` 权限及现有员工权限规则。领料、退料和工人之间转料只移动存放位置，不等同于施工消耗。

- `GET /api/inventory`：库存读取允许 `warehouse` 或 `orders` 权限。`group_by=product` 返回全公司商品汇总；`group_by=warehouse` 或省略时返回仓库分布。支持 `warehouse_id/product_id/q/state=active|inactive/show_zero=1/page`，每页100行；产品汇总的 `quantity_quantum`、`quantity`、`auxiliary_quantity`、`reference_value_cents` 为服务端精确结果。汇总会计入匹配的仓库余额，包括停用仓库中仍存在的余额；不会把前端当前页自行相加。
- `GET /api/stock-documents`：`warehouse` 权限下分页读取库存单据，支持 `warehouse/kind/state=all|void/from/to/q`。返回 `id/kind/work_date/source_id/destination_id` 及两端名称、`handler/note/total_cents/version/voided/void_reason`。`GET /api/stock-document?id=...` 在同一权限边界下附带保存的 `line_items` 快照。
- `POST /api/stock`：需要 `warehouse` 权限，使用 `{id,version,kind,date,from,to,note,handler,lines}`。`kind` 为 `进货、领料、退料、转料、调拨、报损、报溢`；明细最多500条，经手人最多100字、备注最多5000字、明细备注最多1000字。进货/报溢必须有去向，报损必须有来源，领料/退料/转料/调拨必须有不同来源和去向；领料为总仓到工人位置，退料为工人位置到总仓，转料为工人位置到工人位置。
- 明细字段包括 `product_id`、`input_quantity`（兼容 `input_qty`）、`input_unit`、`quantity_quantum`、`price_cents`、`total_cents`、名称/单位/辅助单位/比例快照及备注。进货直接保存本次总金额；商品参考单价由总金额和精确数量推导，不是供应商结算价。
- `POST /api/stock-state`：需要 `warehouse` 权限，使用 `{id,version,action:"void"|"restore",reason}`。作废必须填写原因；作废/恢复会反向或重新应用同一单据的库存差额和进货累计金额。
- `GET /api/stock-movements`：需要 `warehouse` 权限，支持 `product_id/warehouse_id/from/to/page`，返回 `delta_quantum`、换算数量/单位、仓库、`source_type/source_id` 和可关联的订单号、工地地址。日期过滤使用开始日包含、结束日次日排除。施工耗材和商品销售分别记录来源类型，查询按来源类型连接施工单，避免同一编号跨表串联。
- `GET /api/materials`：需要 `warehouse` 权限，按月、公司、仓库和关键字读取施工耗材历史；数量同时返回精确量子、输入单位和保存的比例快照。

所有写接口通过既有 `Idempotency-Key` 请求表处理重放；同一用户同一键的不同请求体不得复用旧结果。库存商品按 ID 排序锁定，余额按仓库/商品行锁定，负库存返回409。编辑单据会先撤销旧流水再应用新差额；版本或作废状态不一致返回409。库存、单据、审计和幂等结果必须在同一事务内提交。

### 参考货值

`product_purchase_costs` 保存每个商品累计的 `purchased_quantum` 和 `purchase_cents`。当前库存参考货值（分）为：

```text
ROUND(当前精确库存量 × 累计进货金额 ÷ 累计进货精确数量, 0)
```

没有有效进货累计时返回0。退料、报损、报溢和施工/售料改变实际余额；只有进货单的保存、编辑、作废、恢复会反向调整进货累计数量和金额。该值是简单平均参考估值，不是批次成本、供应商欠款或利润。

### 施工耗材与售料库存边界

`POST /api/order` 的材料和售料明细均带 `product_id/warehouse_id` 及精确数量。默认来源为施工人关联仓库；选择其他来源需要 `warehouse` 权限。施工耗材、售料从实际来源仓库扣减，取消或修改先恢复旧精确数量，再应用新数量；商品必须启用，库存不足返回409。第三阶段将耗材重复检查明确为同一 `line_id+product_id+warehouse_id`，允许不同施工明细和公共耗材登记同商品；已保存售料不能换商品。订单、库存流水、审计和幂等在同一事务中处理。销售金额受 `customer_prices` 权限保护，库存来源操作仍受仓储边界保护。

### 迁移和拒绝条件

- 迁移441增加 `stock_balances.qty_quantum`、`stock_movements.delta_quantum` 和 `product_purchase_costs`，以旧微量值乘商品 `base_factor` 重建精确余额/流水，并从未作废进货单重建累计进货量和金额。若找不到单位定义、数量乘因子溢出有符号64位、进货缺明细/JSON不可解析、进货数量或金额为负、精确快照无效或累计值不能安全写入，迁移返回错误并不应写入版本完成标记。
- 迁移442增加订单耗材的 `qty_quantum/input_qty/input_unit/conversion_ratio` 和售料的 `qty_quantum`，从历史基础微量值按单位因子转换。缺单位定义、历史数量非正或乘因子溢出时拒绝；不得静默把不明确的旧数量解释为另一单位。

第二阶段必须继续通过第一阶段的公共工艺统一授权、公司固定工艺来源及精确双单位基础合同；本文不改变工艺价格、工资、组合免收或历史施工快照规则。

## V3.0 第一阶段本地开发合同（2026-10-11，未发布）

- 公共工艺管理继续 `POST /api/entity {entity:"crafts", category, name, spec, mode, wage_cents, subitems:[{id,name}], active}`，部位ID稳定、有序；公共客户价格仍强制0。工资在公共维护，公司只展示。八大类、五计量和已保存施工快照保持。
- 公司 `fixed_crafts` 保存独立source、客户单价及数组顺序，服务端依次写 `display_order`；`/reference`、`/reference-options`、工艺目录返回该字段并按顺序读取，公司大类顺序继续使用 `craft_category_order`。同公司/source唯一，取消后重新选用复用固定ID；公司保存版本、事务、审计不变。旧测试重复来源不猜合并，开发验收用新隔离空库。
- 员工账号 `crafts` 仅公共ID；公司固定ID、大类和不存在的ID拒绝。停用工艺不得新增授权，已授停用项可保留/取消。选择器读取不带company/order scope的公共工艺分页。填写权只管普通施工，客户价格/工资/工艺资料维护另查权限。服务端目录editable和所有写入从真实关联解析，无权项可见只读；旧幂等键重放也校验当前公共授权。
- 商品 `aux_ratio` 规范化为精确十进制、最多6位，不悄悄舍入；同事务更新 `product_unit_definitions` 的两整数因子/单位/scale6/version，加入全量备份。`productQuantitySnapshot`定义后续数量与单位快照合同，未连接旧库存业务接口。1桶=6小组的因子6/1可使六次1小组精确等于1桶；**完整进货、领退调、耗材、售料的V3换算和参考货值属于第二阶段**。
- 结构迁移439/440，API和保存测试结果详见 `.trellis/tasks/10-11-v3-phase1-foundation/validation.md`。本阶段不改交票收款/工资结算等后续模型，不部署、不推送。

## 2026-10-11 公司工艺大类展示顺序（增量迁移438，未发布）

本节新增公司级展示设置，不新增排序专用接口，不改变收费、工资、库存、组合、交票或打印算法。本轮已在34971核验身份后执行真实MySQL/API/Edge Playwright；分层结果见 `.trellis/tasks/10-11-order-ui-company-sorting/validation.md`，不表示用户人工、NAS/Docker/云或生产验收。

- 公司行新增 `craft_category_order: string[]`。`GET /api/catalog?entity=companies`、`GET /api/reference?tables=companies`、`GET /api/reference-options?entity=companies` 的公司对象均返回该键；SQL NULL、兼容旧资料中的JSON null及明确默认值均显示 `[]`，不省略、不返回 `null`。各入口原有读取权限及联系人/电话/备注隐私过滤不变。
- `POST /api/entity` 的 `entity="companies"` 可选提交 `craft_category_order`，复用公司 `id/version`、完整普通资料表单、事务、幂等和 `保存companies` 审计。**只新增字段的省略语义，不把整个公司接口改成任意部分更新接口**；排序保存仍应携带现有普通字段，纯排序可省略 `fixed_crafts`。
- 字段省略：新增公司写SQL NULL；编辑公司保留原设置。明确 `[]`：保存空JSON数组、恢复默认展示。非空数组允许只指定部分大类；明确 `null` 不视为省略或重置，返回400。
- 类别身份是 `backend/main.go` 的 `categories` 精确字符串：`开工`、`包管`、`安全防护架`、`回填`、`防水涂料`、`室外防水`、`丙纶`、`维修`。数组最多当前注册类别数（8），元素必须为不重复的精确有效字符串；非数组、数字/布尔/null/对象元素、未知键、重复或超限返回400。不修剪类别值，不接受类别ID、工艺名或名称包含词猜分类；合法注册类别不要求当下公司已经有该组。
- 公司固定工艺以自身 `id` 为身份，`source_id` 明确关联公共 `crafts.id`；没有独立类别ID表。同名不同ID不得合并，室外防水不得按名字合入防水涂料。
- 保存排序由后端显式检查 `customers`；`/api/entity` 的公司请求还在 `mutate` 的幂等缓存快捷返回前检查当前公司管理权。撤权后旧会话401，重新登录的旧键/新键请求都403，不返回旧缓存成功、不写数据库。不要求、不推导 `customer_prices`、`wages` 或 `combo_pricing`。仅有公司配置权限、无价格查看权也可排序。若同时保存固定工艺，仍沿用既有无价保价和新增定价限制：不得借排序改已有客户价/工资或绕过新增定价的403。版本过期返回409；后续固定工艺校验失败须连同公司设置、审计与幂等结果回滚。
- 展示使用稳定投影：非空自定义数组先过滤当前不存在的组，再按当前配置原次序追加未列出的真实大类；空数组默认依次优先 `开工、包管、安全防护架、回填、防水涂料、丙纶`，其他真实组按原配置次序追加。大类内工艺和小项原有次序保持。A/B公司隔离，新建/编辑/保存查看共用展示规则；不改源数组、工艺/小项ID、明细key、`sort_order`、人员或订单保存快照，不重算历史量价工资。

合成示例（普通公司字段沿用已有值，`Idempotency-Key`及鉴权沿用原合同）：

```json
{"entity":"companies","id":"example-company-a","version":7,"name":"合成示例公司","contact":"合成联系人","phone":"10001","customer_type":"","note":"","ticket_mode":"single","craft_category_order":["丙纶","室外防水","开工"]}
```

成功仍返回 `{"id":"example-company-a","ok":true}`。恢复默认只把数组明确改为 `[]`；旧客户端未维护此字段时应省略，不能用 `null` 或自动补 `[]` 代替。

迁移438在 `initSchema` 现有数据库级锁内、437之后增加 `companies.craft_category_order JSON NULL`，检查列存在并幂等记录版本；不历史UPDATE/DELETE、不回填排序、不重算业务。共享备份按实际列读取，恢复按快照显式列插入：新字段自动保留，旧快照缺字段恢复为SQL NULL；带新字段的快照须恢复到已包含438列的目标。升级及回退不自动删列/删迁移记录或修改历史，详见[初始化与数据说明](../source-deploy/初始化与数据说明.md)和[安全与回滚](../source-deploy/安全与回滚.md)。

本轮真实MySQL、API、撤权后既有幂等键重放、浏览器及目标平台结果由主会话补充；接口和测试存在不等于上述实测通过。

## V2.0 安全初始化与发布接口边界 — 2026-10-11

业务HTTP路径/已确认计价合同不在打包阶段改动。新增程序命令：
- `tegu init-db`：读取明确MYSQL配置，仅执行版本化迁移（已发布v2.0基线33表/max437、v2.0.1为33表/max438；当前V3第一阶段开发源码34表/max440），不创建管理员/导入种子。
- `tegu init-admin`：空用户库时从`ADMIN_INITIAL_PASSWORD_FILE`（推荐）或`ADMIN_INITIAL_PASSWORD`二选一读取本人提供的非空单行UTF8密码（bcrypt最多72字节，不强制8位）；命名锁/事务保证并发一个admin，已有可用admin不读取初始密码、不重置，有非空用户但无可用admin拒绝。密码/DSN/密钥不输出。
- `tegu version`/`--version`：输出Version/BuildCommit，不构造数据库连接。未知命令/多余参数明确拒绝，且不回显可能含秘密的参数。
- `serve`/无命令：沿用业务服务、先迁移与安全管理员存在检查。新V2目录恢复必须在init-db之后、init-admin/serve之前；恢复不是HTTP自动操作，也不改原工艺目录。

V2正式构建ldflags为`-X main.Version=2.0 -X main.BuildCommit=<发布commit>`。公共包不包含实际目录/密码/旧哈希；私密SQL验证/失败恢复见独立本机恢复说明。

## 2026-10-10 阶段C：已保存施工单九列只读查看

- 正常输入与已保存查看共用九列结构：施工人员、工艺、部位、单价、方量明细、方量小计、金额小计、备注、合计金额。`mode` 继续使用个、米、平方、立方、金额各自既有输入与后端计算；本阶段不改计量、工资或阶段B免收算法，不新增迁移。
- 查看直接使用 `GET /api/order?id=...` 已保存的人员、工艺/配置小项、独立 `actual_location`、尺寸、`qty_micro/price_cents/total_cents` 和备注快照。配置小项显示在工艺列，实际部位单独显示在部位列；同一 `subitem_id` 的多个实际部位连续展示，不改写明细ID、保存顺序或数据。公司/工艺/人员后来变化不重算查看金额。整单客户合计只显示一次，使用订单保存的 `total_cents`，保留售料货款、不加工资。
- `GET /api/order` 在原有 `combinations/combination_version` 之外增加只读 `combination_details` 数组。每项为 `{id, waterproof, polypropylene}`，`id` 对应原组合关系；无关联时为 `[]`。双方均为真实保存明细，不按名称、日期、部位或当前配置猜测匹配。
- 双方字段为 `id/order_id/number/work_date/craft_id/category/name/subitem_id/subitem_name/actual_location/worker_id/worker_name/mode/qty_micro/waived_qty_micro/chargeable_qty_micro`；有客户价格权限时附 `price_cents/total_cents`。`work_date` 为 `YYYY-MM-DD`；量为百万分之一计量单位整数，钱为整数分。`waived_qty_micro` 是该实际行所有有效关联的累计免收量，`chargeable_qty_micro = qty_micro - waived_qty_micro`；金额仍直接读保存值，不由查看页重新算价。
- 关联读取与本订单共用只读、repeatable-read事务，同时限定关系及双方订单属于请求订单的同一公司、同一工地。原订单访问校验仍适用；跨公司/工地的异常关联不会返回相关订单快照。
- `customer_prices` 独立控制主单及关联双方的客户单价、金额小计、售料金额和整单总额：无权限响应**省略**金额键，不返回0替代；前端不渲染受限金额。关联双方快照始终不含工资字段，不因 `wages` 或 `finance` 权限而授予价格。`combo_pricing` 仍只控制组合修改，普通查看不产生免收或工资写入。
- 前端 `saved_snapshot` 仅用于保持“查看已保存施工单”与未保存输入分离；`combination_details` 仅用于显示。本轮明确从 `POST /api/order` 请求剔除这两项，普通编辑仍沿用阶段B组合清单省略/空数组的既有含义。切换查看、关闭或返回编辑不能提交订单、工资、库存、票据或审计变更。


## 2026-10-10 阶段B：手动防水与丙纶组合免收（最新合同）

- 默认不组合。正常施工单分别收费、分别计薪；旧 `included_by_craft_id` 配置只作历史兼容，不再强制新单自动免收。公司固定工艺保持明确的正常客户价；原正常价为0时不猜价、不批量补价。已有含价订单保留原收费/工资快照，普通备注编辑不会把历史零工资自动重算。
- 独立权限键 `combo_pricing`，名称“防水丙纶组合免收”，存于现有 `users.permissions` JSON。仅系统管理员默认全权；普通管理员与员工必须由系统管理员显式授予。普通管理员不能自授或转授此键。组合操作还须有 `orders` 及关联双方具体固定工艺的填写权；`customer_prices`、`wages`、`finance` 互不替代，不回填历史账号。
- 只支持同公司、同工地、未取消、非临时、非旧含价快照，且 `mode="平方"` 的实际防水涂料/室外防水及丙纶明细。手动选择双方实际行；支持不同日期、部位、数量、施工人员。先丙纶后防水或同工地存在两种工艺都不会自动配对。
- `GET /api/project-combinations?project_id=...&page=1`：50条/页，返回 `rows,total,page,page_size,project_id,combination_version`。候选含稳定 `id/order_id/order_version`、日期/部位/施工人、`kind`、`qty_micro/waived_qty_micro/available_qty_micro`、`editable/blocked_reason`。`line_ids=id1,id2` 可定向补读至多50条远期明细，仍保持同公司/同工地与价格权限边界。无 `customer_prices` 响应省略 `price_cents/total_cents`，不会用0代替。
- `GET /api/order?id=...` 增加 `combinations` 和 `combination_version`，每行增加普通施工信息 `waived_qty_micro/chargeable_qty_micro`；原A2-1小项、部位、尺寸和人员字段保持。
- `POST /api/order` 可带 `combination_version`、`combinations`。**省略** `combinations` 表示普通编辑保留已有关系；**明确空数组**表示解除本订单关联的组合，仍须组合操作权限。新行用 `client_ref` 作为本次请求身份，关系的明细ID可引用该值，由后端映射到真正 `order_lines.id`，不把前端草稿键存入关系表。不得把UI状态或未授权空数组随普通保存发送。
- `POST /api/project-combination` 要求 `project_id/order_id/order_version/combination_version/combinations`，关系清单仅替换触及当前订单的关系，不影响同工地其他关联；省略清单非法。两条实际行的 `waterproof_order_version/polypropylene_order_version` 也须最新。保存与订单共用同一事务路径，回包为 `id/version/combination_version`，仅有价格权限时附 `total_cents`。
- 一组关系必填 `waterproof_line_id/polypropylene_line_id/waive_direction/waived_qty_micro`。方向只能 `waterproof` 或 `polypropylene`；面积按百万分之一平方米整数保存，必须人工输入正数。每个目标明细的所有有效关系免收面积合计不得超过实际量；重复配对拒绝，其他日期已占用的面积不可重复使用。关闭组合使用空清单，不以0面积作为关系。
- 客户收费按被免收行的剩余收费量乘保存的正常公司单价，沿用 `scaledAmount` 的整数分舍入。两项 `qty_micro/worker_id/wage_cents/labor_cents` 与人员工资账户不因免收折减；新保存从服务端工艺工资配置计算。Go已删除旧含价分支将防水工资清零的逻辑；不得据此修正历史快照。
- 项目锁、订单/组合版本、关联记录保护、关系与订单/草稿票金额、审计及幂等记录同事务。已交票、有效收款、任一施工人工资已结算或旧含价快照的关联订单不能直接修改组合；失败返回说明且全部回滚。相同幂等键同内容重放不重复收费/免收/计薪，内容不同或过期版本409。
- 错误：400（无效方向/正面积/计量或明细/跨公司工地/重复配对），403（缺组合或双方工艺填写权限），404（目标订单或工地缺失），409（版本冲突、超剩余面积或账务保护）。同一面积超额分配的并发请求最多一份提交，其余冲突。
- 版本化迁移437新增 `projects.combination_version` 和 `project_combinations`，纳入真实备份与恢复；不回填组合、不重算旧账、不删除旧字段。升级回退见 `升级与恢复说明.md`，可执行合同见 `.trellis/spec/backend/project-combinations.md`。

独立接口示例（面积40㎡；并非真实业务资料）：

```json
{"project_id":"synthetic-project","order_id":"synthetic-order","order_version":1,"combination_version":0,"combinations":[{"waterproof_line_id":"saved-water-line","polypropylene_line_id":"saved-poly-line","waterproof_order_version":1,"polypropylene_order_version":1,"waive_direction":"polypropylene","waived_qty_micro":40000000}]}
```

本节替代下文历史阶段的自动含价规则，不改原打印模板；本轮仅阶段B，不进入C，不操作业务库、不推送/部署/打包。实测证据见 `.trellis/tasks/10-10-waterproof-polypropylene-combination-pricing/validation.md`。

## 2026-10-10 A2-2：独立查看客户价格权限

- 权限键 `customer_prices`（界面名称“查看客户价格”）复用 `users.permissions` JSON，现有“员工权限”账号表单和 `POST /api/user` 可授予/撤销。员工必须显式包含此键；单有 `orders`、`rates`、`customers`、`wages` 或 `finance` 不视为拥有价格权限。`admin/operator` 沿用原角色规则，无数据库新列、新迁移或历史员工批量授权。账号修改沿用会话失效机制，旧会话返回401。
- 价格权限只允许在原有模块/工艺读取范围内看公司收费单价、施工/售料金额小计及客户收费合计，不能授予制单、填写工艺、工资或财务权限。`wage_cents/labor_cents/delivery_wage_cents/wage_records` 仍依原工资权限；收款、余额、收款记录仍需财务权限，不因能看价格而返回。
- 无价格权限时，订单详情、订单/工资列表与列表合计、工艺目录/公司配置/引用、工地列表/详情/累计内容、仪表盘等适用JSON响应省略客户金额字段，而非返回0或null。包括 `price_cents/total_cents`、金额计量段的 `amount_cents`、售料单价/小计及适用收款/余额字段。普通工艺、小项、实际部位、尺寸、数量、人员和备注仍可读。库存采购成本和独立工资金额不按客户价格键过滤。
- `GET /api/order`、`GET /api/orders?view=orders|wages`、`GET /api/projects`、`GET /api/project-work`、`GET /api/reference`、`GET /api/reference-options`、有原资料权限时的 `GET /api/catalog` 均在Go响应边界执行过滤。`GET /api/project-ticket` 也校验价格及原详情访问权限，价格可见但没有财务权限时仍不返回收款/余额/收款记录。
- 财务票据列表/公司汇总/旧财务列表及修改、票据操作/批量操作和财务附件下载同时要求原财务权限与价格权限。包含客户金额的订单/工地/财务导出及施工/财务归档文件、公司整月ZIP中的相应文件需原模块权限及价格权限；无权限返回403或不提供该类文件。工资CSV与纯工资归档继续只依工资权限，不能以新增权限扩大工资读取。`GET /api/attachment-capability` 只在财务及价格权限同时满足时启用。
- `POST /api/order` 固定工艺收费按服务端配置或已存历史快照及原计量规则计算，不能信任前端提交单价、小计、合计。无价员工编辑已有临时项/售料时保留原收费单价，伪造或缺省金额不能清零；已有金额模式行保留服务端金额。新增需要手录收费的临时项/售料或调整售料计价单位，缺价格权限明确403，Vue隐藏相应自由定价入口，失败保留草稿。普通尺寸/数量/部位/备注编辑仍允许，修改后金额按原规则重算。
- 保存回包只向有价格权限账号提供 `total_cents`；订单幂等重试按当前权限重新过滤已有缓存。财务及工资敏感操作在读取幂等缓存前再次检查原模块权限，撤销权限后不能以旧操作编号获取金额或绕过限制。
- Vue金额字段可缺省，统一显示“无查看权限”，保存请求不补0；真实授权金额0仍显示0。保留A1九列、A2-1独立部位、施工人员和只读隐藏/显示；未改工资计算、防水/丙纶组合规则或打印模板。包含客户收费的打印入口仅对已有制单范围且有价格权限账号开放，模板本身逐字节保留。

无价订单响应片段（普通信息仍可读，金额字段不是0而是不存在）：

```json
{"id":"synthetic-order","version":2,"lines":[{"craft_id":"fixed-wall","subitem_id":"wall","subitem_name":"墙面涂刷","actual_location":"厨房","qty_micro":6000000,"segments":[{"length":2,"width":3}]}]}
```

合同与回归映射见 `.trellis/spec/backend/customer-price-access.md`；本轮实测见 `.trellis/tasks/10-10-a2-2-customer-price-permission/validation.md`。下述A1/A2-1中的“价格权限未实施”是当时阶段范围，本节为最新合同。业务/预览库未操作，未部署、打包或推送。

## 2026-10-10 A2-1：小项与实际施工部位独立存读

- 本节仅描述 A2-1；A1 九列布局、现有计价/人员/权限/事务沿用，不实施组合计价、价格权限或打印改版。实测状态见当前子任务验证记录。
- `POST /api/order` 的每条 `lines` 和 `GET /api/order?id=...` 的施工明细分别包含 `subitem_id`（配置身份）、`subitem_name`（名称快照）、`actual_location`（实际施工部位）和 `region`（旧混合原文）。固定新行的 `subitem_name` 由后端按公司工艺配置获取，不能由客户端伪造；已存小项名称及旧 `region` 保留快照。
- 同一 `craft_id/subitem_id` 可有多条明细；例如“墙面涂刷”的厨房、主卫、次卫各占一行，分别保存 `segments`、服务端计算的 `qty_micro`、`description`、人员及 `sort_order`。不能按小项 ID 去重，也不能在新增部位时复制上个部位的尺寸、备注或数据库行 ID。
- 新前端行的 `region` 为空；`actual_location` 只来自实际部位输入，不从 `region`、小项名称或尺寸段 `surface` 猜测。历史记录中新字段为空即为空，页面单独提示旧原文，迁移不回填、不重算历史数量/金额。
- `actual_location` 最多200个 Unicode 字符。编辑已存行时，省略该字段表示旧客户端，保留已存值；明确空串（或 JSON null）表示清空。字段存在性与 Go JSON 解码的大小写匹配规则一致，不能把 `ACTUAL_LOCATION` 的更新误判为省略。除 null 外的非字符串值、超长文本或只读行被篡改仍返回相应400/403，整次事务失败不留部分明细。
- 版本化迁移435/436仅为 `order_lines` 增加 nullable `VARCHAR(200)` 的 `subitem_name/actual_location`；读取 NULL 为 JSON 空串。详情重开返回全部独立明细及原排序。升级/回退限制见根目录升级说明。

请求中的位置示例（其余单头及既有字段省略）：

```json
{"lines":[{"craft_id":"fixed-wall","subitem_id":"wall","actual_location":"厨房","description":"北墙","segments":[{"mode":"平方","length":2,"width":3}]},{"craft_id":"fixed-wall","subitem_id":"wall","actual_location":"主卫","description":"门侧","segments":[{"mode":"平方","length":1.5,"width":2}]}]}
```

## 2026-10-09 正常制单、防水/丙纶和施工单打印

- 制单页面只使用正常版。每个工艺大类可选默认施工人，小项可以指定自己的施工人；后端仍逐行检查公司固定工艺填写权和施工人技能。页面隐藏只读项不扩大后端权限。
- 新固定工艺施工行由服务端读取公司固定工艺及公共来源，不采用客户端提交的收费/工资单价。含价组合仅允许“防水涂料”或“室外防水”为收费主项、丙纶为含价项；有效组合下防水收公司费用且工资为0，丙纶收费为0并按公共工资标准及实际工程量计薪。错误主项、公司配置或关联会拒绝保存。售料仍按商品销售价格计入公司应收。
- 编辑已保存记录时，历史行沿用已保存收费/工资快照，新组合不追溯重算。订单、工资和材料库存仍遵循既有事务、幂等及权限合同。
- 普通施工单标题为“宜宾特固建材施工单”，列序为“施工人员、品项、部位、方量、方量小计、金额合计、备注、总金额”。客户版不返回工资字段或金额；内部版要求 `wages` 权限。未填写项目不打印。


## 2026-10-08 工资人员及票据公司汇总

- `GET /api/wage-workers`：旧人员余额汇总已停用，持有 `wages` 权限也返回409，避免把一次部分付款错误地分摊到施工月份；使用 `/api/payroll-workers` 查看工资台账余额。工资来源及按日期查询仍使用 `/api/orders?view=wages`，`wage_paid` 仅保护已实际发放来源的施工数据，不再表示工资账户余额。
- V3第五阶段工资管理：`GET /api/payroll-workers?q=&page=1` 汇总全部日期的工资台账；`GET /api/payroll?worker_id=&tab=wages|adjustments|deductions|advances|settlements&from=&to=&project_id=&page=1` 查询指定工人分页明细，顶部余额始终按全部日期累计。仪表盘 `unpaid_wage_cents` 同样是全部日期累计未发余额；所选月份只影响施工进度及其余按月指标。扣款、预支、结算、付款和更正均由 `POST /api/payroll-action` 写入并保留 `payroll_history`。一次结算的工资来源必须全部属于同一工地，或全部未关联工地；不能混合未关联来源和工地来源，也不能混合多个工地，服务端返回400并要求按范围拆分。`payroll_settlement_sources.project_id` 固定结算时的归属；之后补关联旧施工单不会把未关联来源或整笔结算/付款历史显示到新工地。新建工资调整只登记正数补发，减少工资必须走独立扣款记录并确认法律/合同依据。
- V3年终奖金：`GET /api/payroll-bonus?worker_id=ID&year=YYYY&tab=ledger|payments&page=1` 必须有 `wages` 权限，返回所选人员/年度分页流水及累计摘要；`export=1` 导出对应全量CSV。工资和奖金采用独立台账、结算及付款，奖金数额不会并入普通工资应付或工资结算。当前仅自动计提两条已确认规则：正常分开两遍防水 `0.50元/㎡`；精开工“墙地固·墙” `0.15元/㎡`。规则按稳定工艺/小项ID匹配，并兼容精确旧工艺名；只有有效、非临时、平方施工行计提。丙纶及其他未确认规则均不自动计提，老板手动录入。施工更正/取消按人员和奖金年度汇总差额，在同一事务和人员锁内先校验更正后的可结算余额；如果确认结算金额会超过修正后的应得奖金，返回409并拒绝改动。跨年更正须先处理旧年度结算，不能把已结算奖金转到新年度重复领取。
- `POST /api/payroll-action` 的奖金操作以 `bonus-` 开头，共用工资权限、幂等键、事务和 `payroll_history`：`bonus-create-adjustment` 需要 `worker_id,bonus_year,effective_date,amount_cents,reason`，可用正数补发或负数扣减；若负数调整会使确认结算超过调整后的奖金余额，返回409。`bonus-reverse-adjustment` 需要 `id,version,reason`，仅冲销手工调整并保留反向流水；冲销会先检查对应年度已有结算。`bonus-create-settlement` 需要 `worker_id,bonus_year,settlement_date,amount_cents,note`，不能超过未结算奖金；`bonus-reverse-settlement` 需要 `id,version,reason`，须先撤销全部有效付款；`bonus-create-payment` 需要 `settlement_id,settlement_version,amount_cents,paid_date,method,note`，支持分次付款且不得超过该结算余额；`bonus-reverse-payment` 需要 `id,version,reason`，冲销后恢复可付余额。金额均为整数分；同一操作键重放不重复入账，版本冲突返回409。施工记录更正/取消在原订单事务中追加自动奖金差额或冲减，不覆写旧流水；448迁移只为有效历史施工记录回填这两条已确认规则。

## V3.0 套餐及公司工资覆盖

- `GET /api/company-packages?company_id=ID` 需要 `orders` 与 `customer_prices`，返回所选公司的明确房型套餐、金额和稳定工艺/小项覆盖关系。工地创建后可显式选择套餐并保存快照；费用只附加在首张工地票据，票据更正或追加流程不能重复挂接该套餐费用。当前客户收费只取套餐费，套餐覆盖施工行标记 `package_included=true` 且不另收施工费，工人工资仍按保存的工资单价和有效工程量计算。
- `GET /api/project-work` 和订单详情在客户价格权限下返回工地套餐快照；`GET /api/order?id=ID` 的施工明细包含 `package_included`。没有 `customer_prices` 时，该标记和客户金额一起省略。覆盖关系仅按随包 seed 中唯一映射的工艺/小项ID生效；不能唯一对应的原始说明仍待核，不自动免收。
- 公司固定工艺工资可独立设置可空覆盖值；空值继承公共工艺工资，显式0是明确零工资。工资权限与公司维护权限分别检查，客户收费编辑不会覆盖特殊工资。新施工把生效工资保存到工资快照，历史施工不回算。迁移453新增独立覆盖表；迁移454增加工地套餐快照与施工行套餐内标记；迁移455为奖金余额校验添加按施工人稳定存在的并发锁行并纳入备份恢复。
- `GET /api/payroll-history?worker_id=ID&from=YYYY-MM-DD&to=YYYY-MM-DD&project_id=ID&page=1`：必须有 `wages` 权限；日期按历史事件 `created_at` 的日期筛选，工地按事件前后快照或结算所含工资来源筛选。返回 `{rows:[{id,entity_type,entity_id,event,reason,before_snapshot,after_snapshot,actor_name,operation_id,created_at}],total,page,page_size:100}`。前后快照作为JSON对象返回，首次创建的 `before_snapshot` 为 `null`；默认按创建时间和历史ID倒序。无工资权限先返回403，不执行工资历史查询。
- `GET /api/finance-companies` 的V3当前合同见本文第四阶段B：须 `finance/customer_prices`、累计全部月份、十项JSON字符串金额以及真实到账与分配分别统计，非空月份400；上版三项公司汇总不再是当前完整响应。
- 当前公司汇总 `/api/finance-companies` 的 `total` 为全部匹配公司数，金额为分页前完整筛选合计，按实际待收、公司名称、ID稳定排序，每页100；读取和导出在一致只读快照内完成，无相应权限403。`export=1` 导出全部匹配公司，文本采用公式安全转义，金额单位元。
- 历史工资来源钻取/原工资CSV新增显式 `view=wages&all_months=1&month=` 时读取全部施工日期。给定月份仍严格校验；未提交此标记的旧工资请求、其它施工/财务页面和月归档继续要求有效月份。工资来源 `worker_id` 对应本人账户，查看继续使用工资账户ID及版本。原 `/project-tickets` 明细仍要求公司ID；新工资结算和付款只通过 `/payroll-action` 写入。

## 2026-10-08 工资本人明细与财务总单

- `GET /api/wage?id=工资账户ID`：须 `wages` 权限，供工资来源只读查询使用；不是施工记录编辑接口。沿用工资列表账户ID及历史单人回退，返回 `id,order_id,number,company_id,company_name,project_id,project_name,address,work_date,worker_id,worker_name,status,wage_cents,wage_paid,version,delivery_wage_cents,legacy,handles_delivery,lines`。第五阶段中 `wage_paid` 仅保护已开始实际发放来源的施工数据，不表示其结算全部付清。
- `lines` 只含所选账户工人的保存明细：`id,category,name,spec,region,description,mode,subitem_id,sort_order,worker_id,worker_name,qty_micro,wage_cents,labor_cents,segments`；尺寸段仅含长宽高、面、数量及模式。包含此人的0工资行，带货工资只属材料/送货责任人；不返回其他工人的行、客户单价/应收/金额段货款、材料或销售明细。旧单人 `legacy` 保留账户原总工资，不能按旧明细强制重算。
- 无工资权限403；空或超过64字节的账户ID400；账户不存在404；多人账户缺失或金额/归属不完整422。读取使用同一只读一致性事务，不因查询修改工资或单据。发放/撤销仍使用账户ID和版本，编辑施工记录仍从制单入口按 `order_id` 打开。
- 应收应付的单票/全工地查看及打印逐行显示工艺、施工位置、保存施工人姓名和实际尺寸/数量。不同人、位置、日期的明细不合并成一个无归属小计；客户版仍剔除工资单价、工资小计、账户及发放状态。票范围按所选票实际施工行，全工地范围按全部有效记录，姓名取保存快照。

1.03初始化命令`import-crafts`兼容v1/v2，支持可选`subitems:[{id,name}]`并复用工艺小项校验，保留稳定ID/顺序；旧文件无小项保存空数组。非法小项整批回滚，已有工艺库拒绝复导。随包50公共工艺/26小项仅用于空库初始化，工资0/停用需后续补齐。

## 2026-10-08审查修复合同

- `/user`保存和 `/login`认证共享 `normalizeUsername`：去首尾Unicode空白、转小写、非空、最多100个Unicode字符。非法保存不修改账号或撤销会话；合法编辑沿用旧会话撤销规则。权限和短密码规则不变。
- 库存变更与商品维护按商品ID固定顺序锁定：消费者 `FOR SHARE` 读取名称/单位/换算/启停，维护 `FOR UPDATE`，首次流水检查使用当前锁定读及版本15新增的商品前导索引。进货、调拨、施工耗材和售料均适用；历史库存单编辑保留原单位/换算快照，不因当前资料变化重算旧数量。
- `Idempotency-Key`仍允许16～100字符；库存流水内部操作编号短值保留旧 `userID:key`，超过100字节时使用 `v2-` 加SHA256用户及key的结构化数组，固定67字节。请求幂等仍按原完整编号比较内容，不迁移旧流水。
- 财务最近业务变动月份将上海UTC+8月初/月末转换为UTC半开区间；票列表、完整CSV、财务月归档和自动归档候选月份一致。施工 `work_date`、到账 `received_at` 的DATE口径不变。已有归档不回写。
- 逐人工资完整性校验使用已有 `order_id` 索引稳定查找施工行及账户；工资列表、本人详情和恢复保持同一归属/金额/已发校验，错误仍422。大批恢复统计未刷新时不依赖优化器偶然选择全扫描，不跳过校验、不在事务中执行隐式提交的ANALYZE。
- 恢复进度绑定实际任务ID、备份文件及 `result.target`；切换文件不能继承另一任务的成功状态。工程量前端按实际数字序列精确相乘相加、汇总后六位舍入，再按整数分单价计费；后端仍最终计算，前端不以浮点预先舍入到单段。

## 2026-10-04 施工小项人员与逐人工资

- `POST /order` 的每条实际 `lines[]` 可提交 `worker_id`，返回 `worker_id,worker_name` 为保存时的人员快照；客户端姓名不作为保存依据。同一工艺的小项可选不同人，含价丙纶仍0客户收费且工资归该行工人。每行同时校验账号的具体固定工艺填写权和工人的八类技能；不存在或技能不符拒绝整次保存，工人维护本次未增加停用字段。
- 单头 `worker_id` 是默认施工人兼材料/送货责任人。新行省略人员时继承单头；历史行未提交新的人员时保留原归属。纯施工各行明确人员时允许单头为空；有耗材、售料或带货工资时单头必填。仓库仍自动匹配单头，带货总工资只归此人一次，不因小项多人自动拆分材料。只读历史行的人员、单价、工资及小项由服务端保留；共有字段仍保护。
- `GET /order?id=原施工记录ID` 在工资权限下增加 `wage_records:[{id,order_id,worker_id,worker_name,wage_cents,wage_paid,version,delivery_wage_cents,number,work_date}]`。没有工资权限时完全不返回工资账户、工资单价、小计、总额或发放状态。`worker_names` 为实际施工/送货人员快照名单，单头 `worker_name` 继续表示默认/送货人员；普通施工列表和工地记录摘要同样返回名单，使用JSON聚合而不是会截断的GROUP_CONCAT。
- `GET /orders?view=wages&month=YYYY-MM&company_id=&worker_id=&wage_paid=0|1&page=1` 每行是一条施工记录下一个人的工资。`id` 是工资账户唯一ID，`order_id` 是原施工记录ID，`version` 为工资账户版本；`worker_id,worker_name,wage_cents,wage_paid` 为此人对应数据。`total` 为账户行数，`wage_cents` 为完整筛选总工资，不能重复把整次施工金额用于工资汇总。工资本人明细使用 `/api/wage?id=账户ID`，制单编辑使用 `order_id`。100行服务端分页，工资CSV、每公司整月工资归档及工作台待发工资遵守同一分人工资来源。
- 旧 `POST /api/wage-settlement` 的 `pay/undo-pay` 与 `POST /api/settle` 的工资写操作均已停用，返回409并提示改用工资管理，不会修改工资或付款记录。当前工资结算、实际付款和纠错仅通过 `POST /api/payroll-action`；端点和操作字段见上方“2026-10-08 工资人员及票据公司汇总”中的V3第五阶段工资管理合同。
- `orders.wage_paid` 为“已有人工资发放”的编辑保护标记，任一工资已发即禁止修改/取消整次记录，全部撤销后恢复原规则；它不表示全部工人都已发，逐人状态取 `wage_records`。已取消记录不参与待发工资和发放操作。工人/工艺改名或公司修改现价不回写历史快照。
- 工地/票据打印每行带人员快照，内部版可显示按人工资及状态；单票只汇总该票实际行和归该票的带货工资，不重复整单工资。客户版与历史票据版本查询按当前权限剔除全部工资字段，旧版缺小项人员只继承该版本的单头快照，不读取当前人员补造历史。
- 结构迁移430–434与备份恢复兼容见《升级与恢复说明.md》。旧无账户记录只按原单头保留旧总工资、姓名和发放状态；明显多人明细却缺分人工资账户时明确报归属不完整，不能静默给单头发薪。

## 2026-10-01 工艺表格与工地票据合同

本节保留工艺/施工合同及历史工地票读取说明。交票和实际收款写入已由本文V3第四阶段A/B合同替代，原单与原票内财务写入口均停用；历史单、票和收款仍可查询。工资按实际小项施工人分组；旧 `/wage-settlement` 与 `/settle` 工资写操作返回409，当前工资操作使用 `/payroll-action`。

- 公共 `/entity` 工艺保存 `subitems:[{id,name}]`（最多100个、有序、同项名称与ID不重复）、`mode=平方|立方|金额|米|个|根|处|孔位|副|张|套`、统一 `wage_cents`，不接受公共客户价。`个/根/处/孔位/副/张/套` 均为整数计数模式，数量按整数累计并分别以自身单位显示。公司 `fixed_crafts` 保存 `source_id,price_cents,ticket_slot,included_by_craft_id`；工资、名称、规格、单位和小项从公共来源读取，新记录保存独立历史快照。来源缺失时只读且不能新施工，须公司表单显式关联。
- 公司 `ticket_mode=single|split`，固定项 `ticket_slot=1|2`；统一票只能选1。组合含价主项须同公司、同票位，禁止自身和循环关联。新工地保存交票方式和分组快照，后改公司配置不搬动已建工地。
- V3第一阶段：`/user` 和 `/me` 的 `crafts` 是公共工艺ID数组，一次授权适用于所有选用同一公共工艺的公司；固定工艺ID和八大类名不能提交为授权。后端从真实公司/source关联逐行检查，客户端source字段不能给自己授权。`workers.crafts`仍是施工人的八类技能；临时项需 `orders_temporary`，维护/客户价/工资/财务权限互不替代。
- `/reference?tables=crafts&craft_scope=order&company_id=ID&project_id=工地ID` 返回该公司的全部固定项及逐项 `editable`；选择工地时须传 `project_id`，组合主项及票位取工地快照，后增固定项首次使用时固定当前配置。`/reference-options?entity=crafts&company_id=ID&project_id=工地ID` 遵守同一快照口径；公司与工地不匹配拒绝。省略工地只返回当前公司配置。无权项可见只读，基础目录查询也不能越权读取工资。`/order` 保留全部历史行并按工资权限剔除工资。
- 新 `POST /order` 填公司、施工地址、实际 `work_date`、施工/送货人；首次施工可省略 `project_id`，保存时同事务建立工地，后续明确选择原地址并提交 `project_id`。首次仅允许正常归票，详见下文地址建档合同。`lines` 包含稳定 `id,subitem_id,sort_order`；平方录长宽、立方录长宽高，单行所有段须使用同一计量；按计数单位 `segments:[{mode:"根",count:2}]` 中 `count` 为1至1000000整数，单位可为个、根、处、孔位、副、张或套，2单位乘公司100元应收200元，乘统一30元工资60元。金额固定项在制单勾选已施工后按公司整项金额使用，每个填写小项计一次，不接受客户端临时改固定金额。
- 防水组合只允许把丙纶含在“防水涂料”或“室外防水”固定工艺中，双方须归同一张票。公司设置保存时含价丙纶的客户收费强制为0；制单时后端按公司/工地组合快照自动认定含价，忽略客户端价格和含价标记，要求同时填写对应防水主项。组合新增行由防水主项收客户费用且人工费为0，丙纶收费为0并按公共工艺标准工资及实际施工量计发；已保存施工与工资快照不重算。历史已有无效组合配置不会自动改写，新的施工保存会拒绝不符合方向的组合。
- 2026-10-02新增按米：`mode:"米"` 的 `segments` 只填 `length`，每段大于0且不超过10000米；`width,height,count,amount_cents` 必须为0，段模式为空或米，最多100段、总长不超过1000000米。工程量按总长度以百万分之一米保存，收费/工资按总长乘服务器单价、沿用整数分舍入。客户端不能覆盖固定模式、数量或单价。工艺维护、制单、已存记录、打印、月表/验收及备份导入均保留米单位；无需新增数据库字段。
- `sales` 独立于 `materials`：提交 `product_id,input_unit=base|aux,input_qty`（十进制字符串）、销售 `price_cents`。后端保存商品/单位/换算率快照、计算货款和基本库存量。`delivery_wage_cents` 是本次带货总工资，只有工资权限可填，2桶填30元仍为30元。纯售料可保存；售料与耗材同仓同商品合计检查并扣库，修改返差和取消返还同事务记录。
- V1公司商品固定价使用独立 `company_product_prices`，不复用进货成本。`GET /api/company-product-prices?company_id=ID&product_id=ID` 需要 `customer_prices`，返回该公司商品已配置的 `input_unit=base|aux` 单价数组；空数组表示该公司商品没有固定售价。新增售料由服务器按公司、商品和输入单位取价并把来源行及价格写入售料快照；同单位编辑保留历史单价快照，不能因后来改价而重算。该配置不会修改 `product_purchase_costs` 或工人工资；未配置公司固定价的其他商品仍沿用原手录售价流程。
- 归票 `ticket_action=normal|correct|append`，更正需财务权限、`ticket_id` 和选票时的 `ticket_version`，原票已变化返回409，不能用新版本自动重试旧内容。正常按工地分组累计未交票；临时项和售料共用 `sales_ticket_slot=1|2`，统一票只能归1。已交后新增须明确更正或追加。更正原号留旧版本与实收；追加只含本次新增，编辑追加票草稿保留原票号和归属。已交票/已发工资的原实际记录锁定，后续新增另存记录；归票与重印不再次扣库存或发工资。
- 保存仅提交有权填写的实填行。后端保留省略的只读历史行；禁止伪造行ID、改其工艺/小项/单位、跨公司和临时项绕权。含只读行的记录不能通过改共同日期、师傅、材料、售料顺序/票位或带货工资间接改归属。施工人技能改变导致已存明细不再匹配时，前端阻止保存并保留输入，不能将其当空项静默省略。库存、票据、单据、日志和幂等记录同事务。

| 页面/操作 | 当前接口与响应 |
|---|---|
| 工地总表 | `GET /project-work?id=工地ID&page=1&page_size=25`，每页1至100实际记录，返回 `project,orders,tickets,total,total_cents,page,page_size` |
| 指定票/旧版本 | `GET /project-ticket?id=票ID&version=版本`；省略version为当前，返回 `ticket,orders,versions`，财务权限另有 `payment_receipts`；历史版按当前工资权限过滤 |
| 全工地打印 | `GET /project-ticket?project_id=工地ID`，读取全部有效记录，不受列表当前页限制；客户版无工资，内部版需工资权限 |
| 原单票交票/收款 | `POST /project-ticket-action` 的 `deliver/receive/void_receipt` 已停用，返回409；使用V3交票管理和收款管理 |
| 工地收费来源明细 | `GET /project-tickets?company_id=ID&month=YYYY-MM&status=&q=&page=1`，公司必选、月份按票最近业务变动，100条分页并返回完整筛选合计及工地 `ticket_mode_snapshot`；`received_cents/outstanding_cents` 表示已分配/尚未核销，已扣减免；`export=1` 导出全部匹配行CSV，票名按工地快照区分统一票/第一票/第二票。公司真正累计欠款使用 `/finance-companies`，不能用本月明细替代 |
| 原批量交票/收齐 | `POST /project-ticket-batch` 已停用，返回409；同公司多个工地交票使用 `/ticket-batch`，多处分配使用 `/receipt-allocation` |

新票已分配款只计未作废的 `project_ticket_receipts`，真实到账由 `company_receipts` 计一次。原收费1000元、分配1000元，更正新增200元后收费1200、分配1000、工地尚未核销200；追加路径原收费来源不变、新来源200尚未核销。票位唯一、收费明细有效归票唯一；旧版和新纸票批次不重复参与金额总计。公司、项目名和地址随建票保存，票明细/导出不跟随基础资料更名。

月度施工、售料、施工工资和带货工资按实际施工/送货月份；工地财务快照按最近票变动月份、真实公司收款流水按到账月份，分配不再充当实际到账流水。旧单财务归档排除新票管理记录，避免重复统计。当前表与新字段纳入备份恢复，445收款头回填规则见本文第四阶段B。本节描述实现合同，实际验证范围以任务 `validation.md` 和功能审计为准。

新增施工人时，在同一事务自动创建同名、已启用的普通仓库，分类为施工人仓库，worker_id关联新施工人。创建施工人只需workers权限，自动建仓不额外要求warehouse权限；手动修改仓库仍要求warehouse权限。任一步失败全部回滚；同一Idempotency-Key重试不会重复建仓。编辑施工人不创建额外仓库，也不自动改已有仓库名称。

同源 `/api`，JSON；登录后使用 HttpOnly Cookie。写操作还检查 Origin。
除登录、退出、维护接口外，POST 必须带 `Idempotency-Key`（16～100字符随机操作编号）。网络断开重试必须保留同一个编号；修改表单内容后使用新编号。相同编号但不同内容返回409。
编辑提交记录当前 `version`，旧版本返回409，必须刷新核对，禁止静默覆盖。
金额为整数分，库存/工程量为整数百万分之一基本单位。客户端不能决定固定工艺单价，新单后端读取公司设置；原单同一固定工艺保留原价。固定金额按后台整项价计费，临时项才允许输入金额。公司应付给我们的施工款就是应收款，不建立供应商应付接口。

| 页面 | 查询接口 GET | 写接口 POST |
|---|---|---|
| 登录 | `/me` | `/login`（username、password）、`/logout` |
| 基础资料/工艺 | `/catalog?entity=companies&page=1&q=关键词&active=1&category=开工`、`/reference-options?entity=companies&page=1&q=关键词`、`/reference?tables=companies,workers` | `/entity`（entity、id、version及字段） |
| 账号权限 | `/users?page=1&q=关键词` | `/user`（id、version、username、name、role、permissions、crafts、active、password） |
| 施工单 | `/orders?month=2026-09&company_id=ID&page=1&view=orders`、`/order?id=ID` | `/order` |
| 库存/进货/调拨 | `/inventory`、`/stock-documents` | `/stock` |
| 收款管理 | `/finance-companies`、`/company-receipts`、`/company-receipt`、`/receipt-targets`、`/customer-discounts`、`/customer-discount` | `/company-receipt`、`/receipt-allocation`、`/company-receipt-action`、`/customer-discount`、`/customer-discount-action` |
| 工资管理 | `/payroll-workers`、`/payroll?worker_id=ID&tab=wages|adjustments|deductions|advances|settlements`、`/payroll-history?worker_id=ID`；施工工资来源 `/orders?...&view=wages` | `/payroll-action`（调整/扣款/预支/结算/实际付款及纠错）；旧余额汇总 `/wage-workers` 返回409 |
| 操作日志 | `/audit?page=1` | 随业务事务写入 |
| 库存纠错 | `/stock-document?id=ID` | `/stock`（带id/version修改）；`/stock-state`（id/version/action/reason） |
| 财务批量修改 | `/orders?view=finance&...` | `/finance-edit`（company_id、orders） |
| 验收打印设置 | `/print-settings` | `/print-settings`（title、company，需要工艺管理权限） |
| 月度归档 | `/archives`、`/archives?file=归档标识`、`/maintenance/jobs?kind=archive` | `/maintenance/archive`（month、company） |
| 数据备份 | `/backups`、`/maintenance/jobs?kind=backup` | `/maintenance/backup` |
| 存活检查 | `/health` | — |
| 我的账号 | `/me` | `/account-password`（current、password） |
| 工作台 | `/dashboard?month=2026-09` | — |
| 施工耗材 | `/materials?month=2026-09&page=1` | — |
| 当前筛选导出 | `/export?view=inventory或materials或orders或finance或wages` | — |
| 未使用仓库删除 | `/reference` | `/warehouse-delete`（id、version） |
| 备份下载/恢复 | `/backups?page=1`、`/backup-file?file=文件名&checksum=1`、`/maintenance/jobs?kind=restore` | `/maintenance/verify`（file）、`/maintenance/restore`（file、target、confirm） |
| 云参数/检查 | `/backup-settings` | `/maintenance/settings`、`/maintenance/cloud-check` |

`entity`允许 companies、workers、warehouses、products、crafts。具体字段见schema.go。账户角色：admin系统管理员、operator普通管理员、employee员工。员工权限由模块permissions和工艺大类crafts两组多选控制。

`/catalog` 按实体权限返回 `{rows,total,page,page_size}`，每页50条；`active` 仅商品、仓库、工艺可用，`category` 仅工艺可用，工艺管理列表只显示公共工艺。`/users` 同样每页50条且支持姓名/账号搜索。`/reference?tables=...` 只返回请求的资料类别；`tables=crafts&craft_scope=order&company_id=ID` 只取该公司固定工艺，制单和客户编辑使用此有界范围。未指定 `tables` 的旧接口仍会返回权限允许的完整资料，当前网页不在页面加载时调用它。

`/reference-options` 供表单按需选择资料：`entity` 必填，支持 companies、workers、warehouses、products、crafts；可用 `q` 搜索、`page` 翻页、`id` 回查已选项，每页最多50条，返回 `{rows,total,page,page_size}`。商品、仓库、工艺支持 `active=0|1`；仓库支持 `worker_id`，`q` 匹配仓库名/分类/关联施工人的当前姓名，人员改名不改仓库名，旧仓库名与新人员名均可查找，仓库搜索不匹配或返回施工人电话；施工人支持 `craft` 筛选可施工大类；工艺支持八类 `category` 及 `company_id`（返回公共工艺及该公司工艺，省略时只返回公共工艺）。无对应业务读取权限返回403；仓库读取仍需仓储或施工权限，员工工艺权限、联系方式和工资单价按 `/reference` 的权限口径过滤。进货、调拨和库存纠错表单仍直接列出全部商品，只有打开这些表单时才读取完整商品清单。

施工人是独立业务资料，不绑定登录账号。`/entity` 的 workers 新增/编辑需 `workers` 模块权限；系统管理员和普通管理员默认拥有，员工在账号表单中勾选“施工人管理”。制单和工资等模块可读取业务所需施工人列表，但不能因此修改施工人。制单工艺须同时符合当前登录账号与所选施工人的授权；选择师傅仍自动匹配仓库。workers.user_id 列不参与业务或权限判断。

施工单提交包含company_id、work_date、address、worker_id、contact、note、lines、materials。每条lines包含craft_id、category、name、mode、segments、temporary；临时项另含price_cents和wage_cents。segments是length/width/height/surface数组。materials是product_id/qty_micro数组；仓库由施工人自动匹配。delete=true表示取消，返还材料；系统管理员和普通管理员可取消，员工必须同时拥有 `orders` 与 `order_delete`（删除施工单）权限。取消保留历史单据和反向库存流水，不物理删除。

库存提交包含kind（进货/调拨/报损/报溢）、date、from、to、note、lines（product_id、qty_micro、进货的price_cents）。数量为0的行忽略；后端计算每行total_cents及单据total_cents，随line_items一起保存，不能信任前端传入合计。调拨和施工耗材都在数据库事务中校验、扣减。旧单没有金额时返回null，不能显示为零元。

商品字段另含color（最多100字）、weight_kg（每基本单位公斤数，0至1000000，最多数据库六位小数）。aux_ratio表示一个基本单位包含的辅助单位数。当前功能和本项目验证状态见项目根目录《功能表单对照与审计.md》。

商品还含brand（100字）、spec（200字）、note（1000字）。仓库含category（100字）、warehouse_type（普通仓库/工地仓库/临时仓库）、note（1000字）、worker_id、active。启停沿用/entity完整资料提交及version校验；停用不删除历史数据。/inventory返回aux_unit、aux_ratio，辅助数量=qty_micro/1000000*aux_ratio。界面的显示范围和零库存按钮只影响展示，不修改库存余额。

错误JSON为`{"error":"可显示的提示"}`；400表单错误、401未登录、403无权限、404不存在、409冲突、500内部失败。分页每页100条，同时返回整月筛选结果数量和合计。

## 库存纠错、日志和批量修改

- `/stock-documents`和`/audit`现在均返回`{rows,total,page}`，不再返回裸数组。两者支持page/q/from/to，日期为YYYY-MM-DD、含结束日。库存额外支持warehouse/kind/state（active默认、void回收站、all）；日志支持actor（姓名）、action（操作名称）。每页100条。
- 库存详情包含line_items（JSON字符串）、version、voided、void_reason。商品名称和单位随新明细保存；旧单缺少明细时明确禁止自动编辑/恢复，不推测历史数量。
- 修改库存单沿用`/stock`，提交id/version及完整新明细；后端锁定原单，按库存流水净额计算差额，一次事务写库存、单据、日志和防重记录。不允许更改原单类型。
- `/stock-state`的action为void或restore。void必填reason（最多1000字）；作废反向调整现存流水净额，restore使用已保存明细恢复，不接受前端伪造数量。库存不足或资料停用会拒绝并整体回滚，历史单保留。
- `/finance-edit`的orders为1至100项{id,version,address,contact,work_date,note}，要求同一company_id。仅修改工地基本资料，不改施工金额和库存；全部锁定校验、同一事务提交，任一冲突全部回滚。
- 本段旧工资流程仅保留历史追溯；当前工资结算和实际发放使用`/payroll-action`，旧`/settle`工资写操作返回409。打印从已保存单据加载，单独模板仅使用公开施工字段，不输出工资和未施工项目；打印标题与施工单位保存于settings并纳入数据库备份。
- MySQL1213死锁整体回滚后，业务事务保持原提交编号最多重试4次，其他错误不盲目重试。当前文档描述接口契约，本项目实际验证范围见根目录《功能表单对照与审计.md》。

## 补齐字段与分页契约

- 客户增加 customer_type、note；工艺增加 spec；施工单增加 phone，工艺行增加 spec/region/description。固定工艺的 spec 和名称由后端设置或保留历史，不能靠提交覆盖。
- 施工单新增 company_name/worker_name 历史快照；材料增加 product_name/unit/warehouse_name。这些名称由服务端确定；原记录为空时不能重建过去的名称。
- `/order` 读取单头、工艺、材料使用同一只读快照，避免读到两次修改混合内容。取消保留明细并返还库存。
- 库存单增加 handler，明细增加 note、input_unit、input_qty。input_unit 为实际基本/辅助单位名称，input_qty 必须是十进制字符串（例如 `"100"`），后端换算成 qty_micro；前端数量输入即使来自 number 控件也必须在提交前转回字符串，否则 JSON 解码会返回“表单格式不正确”。施工材料的 input_unit 为 base/aux，input_qty 同为字符串；未传 input_qty 时使用 qty_micro。不能混用两种接口的单位枚举。
- `/inventory` 返回 `{rows,total,page}`，支持 `warehouse_id/product_id/q/state`（active/inactive/全部为空）/`show_zero`（1显示零）/`page`，每页100条，`quantity` 与 `auxiliary_quantity` 为数据库精确计算的六位小数字符串。省略 `group_by` 保留原仓库商品明细口径，`state` 同时筛商品/仓库启停，`q` 可匹配商品名、品牌、规格或仓库名。
- `group_by=product` 按商品汇总匹配仓库的完整库存，每商品一行，先全量求和再分页；`state` 只筛商品启停，`q` 只匹配商品名称/品牌/规格，不排除停用仓库的真实库存。返回商品原字段，`warehouse_id/warehouse_name` 为空字符串、`version=0`；汇总 `qty_micro` 是十进制整数字符串，避免多仓合计超过int64，不能转为浮点数再计算。`warehouse_count` 对应当前可展开分布行数量：隐藏零时为非零仓数，显示零时为全部匹配仓数。没有仓库时，显示零可返回商品目录且仓数0，仓库明细为空。`warehouse_id` 可限定汇总仓库，不存在的仓库返回空列表。
- `group_by=warehouse` 是新库存页面的单仓/商品分布模式，仍返回仓库商品明细，但 `q/state` 与商品汇总保持一致，只筛商品。展开商品传 `product_id` 并继承已应用筛选，分布同样100条分页；停用仓库数量仍可见。仅 `product_id` 的分布查询也采用此商品筛选口径，原不带该参数的明细行为保留。无效 `group_by` 返回400；读取仍需 `warehouse` 或 `orders`，库存导出仍仅 `warehouse`。
- `/materials` 返回 `{rows,total,page}`，支持 month/company_id/warehouse_id/q/page；取消施工单不计入有效耗材。
- `/export` 复用对应列表筛选，不按当前页截断。finance 必须选择公司；工资使用 wage_paid=0或1及 worker_id；库存/耗材使用各自筛选参数。`view=inventory&group_by=product` 导出全部匹配商品汇总（商品/规格/基本单位/基本数量/辅助单位/辅助数量/仓库数/商品状态）；`group_by=warehouse` 或旧省略方式导出相应完整仓库明细，单位、数量及零库存口径与页面一致。返回 UTF-8 BOM CSV，可由 Excel/WPS 打开，文本前导公式符号已转义。
- `/archives` 返回 `{rows,total,page,latest}`，按 month/page 读取归档批次；每批 files 有 file/company/kind/count/amount_cents/available。latest 含最近归档月份、时间和失败提示；无记录时为 null。下载仍以 file 标识定位，不受最近100批限制。生成接口的 company 为空表示按全部公司分别生成。每公司施工月份另生成 `施工验收表`（客户版 XLSX，需 orders）与 `施工内部留档表`（含逐项工资和材料，需 wages）；列表和下载分别在后端过滤/拒绝，客户版查询不读取工资字段。旧 CSV 保留。
- `/backups` 返回 rows/total/page/used_bytes/free_bytes/low_space；free_bytes 不可读取时为 null，low_space 表示少于5GB。校验返回 counts/checksum/format；恢复目标必须是已授权、无任何表的 tegu_restore_ 前缀库，不能当前库。
- 备份设置 interval 单位分钟，允许15/30/60/360/1440，默认60；retain 控制最近份数，另保留35个日期与12个月份各一份。云连接检查使用已保存设置，产生 connection-check.txt，不保存用户输入到日志。
- 备份设置、云连接、快照、校验和恢复端点在后端强制系统管理员权限，不能通过伪造 permissions 获取维护权。`/maintenance/archive` 属业务表格生成，按可读文件分类授权；施工/财务客户金额需相应模块及`customer_prices`，工资、费用和进货分别按`wages/expenses/warehouse`开放独立账表，列表、下载和任务轮询沿同一权限边界。
- 手动归档、备份、恢复返回 HTTP 202 和持久任务编号 `id`，不代表操作已成功。`GET /maintenance/jobs?id=编号` 查询单条；`?kind=archive|backup|restore|all&page=1` 返回 `{rows,total,page}`，每页100条；`all` 只合并备份与恢复，限系统管理员。状态为运行中、完成、失败或中断，`stage/progress/total` 是当前阶段，不是预计耗时百分比；完成时 `result` 才有文件名、表格份数或恢复目标。归档任务只有发起人和系统管理员可读，备份/恢复任务仅系统管理员可读。相同发起人、相同内容在同类任务仍运行时复用编号，不同内容或账号返回409；任务完成后再次提交会新建任务，断线后应先查询记录再决定是否重做。
- 任务心跳每10秒更新；超过2分钟没有心跳的运行中任务在下次查询或提交时标为中断，必须核对文件和目标库，不能将中断直接视为失败回滚。跨进程归档和备份另有数据库操作锁。已结束任务记录随数据库快照保存，快照不复制仍在运行的任务。自动备份和自动归档继续在原有记录表显示结果，尚未纳入手动任务编号列表。真实NAS与恢复验收状态见上级文档。

## 施工人工艺授权

`/entity` 的 workers 新增/编辑必须提交 `crafts: string[]`（八类施工工艺，可为空，不可重复）；空列表表示不能施工。`/reference` 的 workers.crafts 返回数组。workers 模块权限控制维护，沿用版本检查、事务、审计和幂等；不绑定登录账号。

`/order` 非取消保存必须选择施工人，后端在事务内读取并共享锁定 workers.crafts，核对全部明细（含固定、临时及当前登录员工不可见的保留明细）；即使管理员也不能绕过。撤销工人权限不改历史数据，但再次保存单据需要符合当前权限；取消旧单不受新授权限制。前端选择施工人更新未填固定工艺，已填越权项保留并提示，禁止静默删除。
## 分段计费

`POST /api/order` 的 `lines[].segments[]` 保留 `mode` 和 `amount_cents`。省略 mode 沿用整条 mode；新保存不允许同一单价混用计量方式。立方段必须有高度；按个、根、处、孔位、副、张、套填写整数count，每条最多100段。历史混合单位只用于读取和展示，不能按同一单价重新计费保存。

### 工地汇总与分期施工

`POST /api/project`：保留创建合同以兼容已有调用，传 `company_id`、`name`、`address` 创建进行中工地，返回 `id,number,status,version`；页面不再提供独立新建入口，首次建档由 `/order` 事务完成。修改时传 `id,company_id,name,address,version`；`action` 留空更正名称/地址，`disable` 停用，`close` 结项，`enable` 将停用或结项工地重新启用。所属公司不可改；旧版本、重复状态操作返回冲突，成功后版本加一。同公司新建或改地址不允许与另一工地地址精确重复，含停用/结项工地，返回409；历史重复资料不自动合并。新建和改址统一先锁公司再锁工地，串行核对地址。所有变更的前后值与操作在同一事务审计。需施工制单或客户权限。“工地汇总”菜单保留总表及现有工地维护表单，不把无页面的独立创建接口算作页面交付。

`GET /api/projects?company_id=&q=&status=&page=1`：按公司、项目名称/地址/编号、状态筛选，`q` 最多300字，100 条分页，返回 `rows,total,page`。需施工制单、客户或财务权限。每项包含 `version,status,order_count,total_cents,received_cents,undelivered_cents,delivered_outstanding_cents,outstanding_cents`。单数包含已取消单，但取消单金额和收款均不计；旧版只有“已收款”状态而无收款流水的单据按整单已收计。未交单金额统计进行中、待交单的未收余额；已交单待收统计已交单、部分收款的未收余额。`GET /api/projects?id=项目ID&page=1` 返回 `project,orders,page`，各期单跨月份分页，单据收款字段遵循同一口径。以上金额均不含工资。

`GET /api/export?view=project&project_id=项目ID`：跨月导出该项目全部施工单的金额、状态等字段；需施工制单或财务权限，不导出工资，成功生成后记录导出审计。

`POST /api/order` 新建记录：有 `project_id` 时沿用同公司、进行中工地及其交票/组合快照；无 `project_id` 时按 `company_id,address` 建档，地址去首尾空白后须为1–300个Unicode字符，显示名取前200字，完整地址不截断。首次 `ticket_action` 只能留空或 `normal`，更正/追加须选已有地址。同公司已存在完全相同地址（任意状态或历史多条）返回409，提示查找原地址，停用/结项需在“工地汇总”中启用；不静默套用旧工地或旧票。同公司建档/改址共用公司行锁及二进制精确地址比较，并发首次保存至多建一个；不同公司同址允许独立建立。新工地、票位快照、施工/售料、库存、工资、审计和幂等同事务，任一失败不留下空工地；成功响应含 `project_id`。

后续施工由所选工地持续归票。停用或结项项目不能关联新记录。项目名称和编号存历史快照，改名不改历史。旧无工地记录编辑不触发自动建档，也不按地址猜合并；`GET /api/projects?unassigned=1&company_id=公司ID&q=&page=1` 保留旧单列表，返回 `rows,total,page`，无工资；员工须获原单全部具体工艺和临时项授权才能关联。`POST /api/project-associate` 传 `order_id,project_id,version`，只允许逐张关联同公司旧未归属单，保留旧交票/收款体系，不自动变成新票；版本冲突409且同事务审计。原单归属不可普通编辑更换，项目停用后可按旧规则处理历史。

下节的 `SettlementRequest` 仅记录历史 `/api/settle` 字段格式；该写入口现已停用，任何旧工资、收款或交票操作均不能通过它入账。当前工资写入使用 `/api/payroll-action`，真实收款和交票分别使用V3管理接口。

新记录平方/立方先合计工程量到百万分之一，再乘单价取整分；按个整数累加；整项金额数量为1并计整项统一工资。保留每次施工款/工资100000000000000分上限。历史混合单位打印按各段单位展示，不能把平方和立方相加显示。源文件修改后须重新构建部署，不能据此声称目标运行环境已更新。

## 历史财务读取、工资隔离与归档

下方旧 `SettlementRequest` 收款/交单示例及财务action仅保留历史格式追溯。V3第四阶段B已禁止 `/settle` 的所有非工资写入；V3第五阶段也已停用 `/settle` 的旧工资写入及 `/wage-settlement`，这些请求均返回409且不写账。新的真实收款登记、分配、减免和纠错使用本文第四阶段B接口，工资结算及付款使用 `/payroll-action`。旧附件和流水查询保留，新收款凭证目前是可选凭证号/备注，没有新增文件上传接口。历史归档文件字节保留，新归档真实到账/分配口径以第四阶段B为准。

### 结算请求与校验

后端以 `SettlementRequest` / `SettlementOrder` 解析请求，`version` 和 `amount_cents` 必须为 JSON 整数，不能传字符串或小数。请求示例（非真实业务数据）：

```json
{
  "action": "receive_partial",
  "company_id": "公司ID",
  "orders": [{"id": "施工单ID", "version": 3}],
  "amount_cents": 40000,
  "received_at": "2026-09-28",
  "method": "转账",
  "voucher_no": "凭证编号",
  "attachment": "已有附件链接或说明",
  "note": "本次到账备注"
}
```

`orders` 为1至100项，编号不得重复，单据版本至少为1。财务请求必须为同一公司；前端先选公司并提交 `company_id`，兼容调用省略公司时后端从第一张单据确定并逐张核对。工资批量操作不受同公司限制。按单据ID固定顺序加锁，在同一事务检查版本、写状态/流水/结算记录/审计与防重记录；任一项失败整批回滚。成功响应为 `{ok:true,count:处理单数}`，客户端重新读取列表/详情取得新版本和余额。

| action | 权限与状态 | 必填与金额规则 |
|---|---|---|
| `submit` | finance；进行中→待交单 | 单据编号及版本 |
| `deliver` | finance；待交单→已交单 | 仅 `delivered_at`交付日期、`recipient`收票人必填。`ticket_no`与`ticket_attachment`为旧调用可选兼容字段；新网页不再要求或展示其输入，历史已存值保留 |
| `receive_partial` | finance；已交单或部分收款 | 一次仅一张；`received_at`到账日期；`amount_cents`为1至100000000000000分且不得超过剩余款。收清剩余款后自动变已收款，否则部分收款 |
| `receive_full` | finance；已交单或部分收款 | `received_at`必填；后端分别计算每张单据的剩余款并收清，不信任客户端整单金额。合法0元单可结清且不生成0元收款流水 |
| `receive` | 同`receive_full`，兼容旧调用 | 仅此兼容动作允许省略到账日期，后端使用当天；新页面填写日期 |
| `return` | finance；待交单/已交单/部分收款/已收款→进行中；已发工资须先撤销 | `note`退回原因必填；已收/部分收款的有效流水标记`voided=1`，保留原流水和审计，不物理删除；该动作不会实际转账退款 |
| `pay` / `undo-pay` | wages；非取消单且有施工人；发放状态须确实改变 | 工资发放无需客户先收款；使用后端工资金额，版本递增，发放/撤销留结算和审计 |

日期使用有效的 `YYYY-MM-DD`。文本去首尾空白后按字符限制：`method`40、`voucher_no`100、`attachment`500、`note`1000、兼容字段`ticket_no`100、`recipient`100、兼容字段`ticket_attachment`500。整批确认已交单时，同一交付日期和收票人应用于选中的各单；整批收款时同一凭证资料应用于选中的各单。接口接受可选票据号和附件；退回后再次交单若未传这些字段，原票据资料不会被空值覆盖。当前项目的交单表单只填写交付日期和收票人，实际功能验证状态见根目录审计说明。

兼容旧接口调用时，`deliver` 仍可提交 `ticket_file:{name,content_base64}`，与 `ticket_attachment` 链接/说明二选一；新网页交单弹窗不再提供这些输入。收款动作继续可提交 `receipt_file:{name,content_base64}`，与 `attachment` 链接/说明二选一。上传文件时 `orders` 必须只有一张；每个文件最多2 MiB，仅接收内容确认为PNG、JPEG、WEBP或PDF的文件。零金额收款不生成流水，因而不能附加收款文件。文件、交单/收款状态、审计和防重记录同事务提交，错误时一并回滚；成功后原附件字段存 `file:32位ID`。历史外部链接和说明保持原值。

`GET /api/attachment-file?id=32位ID` 仅允许拥有 finance 权限的会话下载；服务端核对保存长度和SHA-256后以附件方式返回，并禁止缓存和内容嗅探。文件原名、类型和Base64内容存入 `attachment_files`，作为数据库快照表随本地/云数据库快照备份，并可在独立库恢复；文件内容不写入审计明细。数据库快照容量随附件增长，长期容量与真实NAS/云恢复仍须单独验收。

### 查询、修改与工资权限

- `/order` 返回 `received_cents,outstanding_cents`；有 finance 权限时另返回全部 `payment_receipts`（含`voided`作废记录），每项含 `id,amount_cents,received_at,method,voucher_no,attachment,note,created_by,created_at,voided`。有效已收只累计未作废流水。无 finance 权限不返回逐笔收款明细。
- `/order` 详情和 `/orders` 列表中的 `ticket_no,delivered_at,recipient,ticket_attachment,return_note` 仅 finance 可读；只有 wages 权限的账号也不能据此读取交票或退回资料。施工金额及统一余额汇总仍按现有查询合同返回。`wage_cents` 和 `wage_paid` 只给 wages；无 wages 账号不能用 `wage_paid` 筛选施工/财务列表或导出。`/order` 另返回布尔值 `editable`，表示该账号可进入当前状态的普通施工编辑，隐藏工资状态后前端不再自行推断锁定原因；保存仍以服务端权限、版本及业务校验为准。
- 统一余额规则：取消单为已收0、未收0；历史只有“已收款”状态而没有逐笔流水的单据按总额已收、未收0；其他单据为有效收款合计及总额减该合计。`/orders`、`/order`、项目、工作台、财务/项目导出和月财务表采用相同口径。历史无逐笔资料只显示已收汇总，不伪造到账日期或凭证。
- 已发生订单的 `wage_cents`、明细 `wage_cents/labor_cents` 仅 wages 可读。rates 工艺维护权限不会自动开放施工单工资；客户版施工打印、客户验收表及项目/财务导出不含工资。内部打印须 wages 权限且 `/order` 返回工资字段，内部月度 XLSX 仅 wages 可列表/下载。工艺资料中的基础人工单价仍依工艺维护授权处理。
- 临时项人工费录入需 wages；无权限新增临时项人工费置0，编辑旧临时项保留原人工费。固定工艺人工单价仍由后端读取/保留历史，不能靠隐藏字段提交改价。
- 普通 `/order` 保存忽略客户端交票、退回字段：新单由后端置空，旧单从已保存记录保留。V3实际交票使用 `/ticket-batch`，作废使用 `/ticket-batch-action`；旧 `/settle` 不再处理交票或退回财务状态。
- 财务页面用明确的订单/收款类型并以整数分处理：查询值兼容数字或十进制整数字符串，金额汇总用BigInt；部分收款输入最多两位小数后转整数分。保存中禁用输入、重复提交保护、失败保留输入；遇409需关闭后刷新核对，不能静默覆盖。

### 导出与月度归档

- `GET /api/archive-companies?month=&company_id=&history=0&page=1`：返回`rows,total,page,page_size=100,legacy_total,latest,can_orders,can_finance,can_wages`。按公司/月默认最新；`history=1`查看各批历史，年月为空可全部。新组由稳定公司ID区分，同名不会合并、改名保留身份；最新批按manifest UTC纳秒生成时间再run排序，旧批回退表时间。先决定真实最新再按模块权限筛组和文件，不能选回旧批。完整范围分组、计数后分页。
- 每组含`key,run_id,company_id,company,month,created_at,automatic,legacy,files`及授权下的可选`order_count,construction_cents,receivable_cents,received_cents,outstanding_cents,wage_cents`。金额来自同批已保存文件元数据，施工只取一份客户验收Excel、工资取工资明细，不重复相加。旧未记录元数据不补当前余额；缺权限字段省略。新完整组省略的空报告按0。无ID旧批仅在历史/全部公司按原批+原名SHA256查看，具体公司ID不猜映射；`legacy_total`为当前权限及筛选下旧组数。
- `GET /api/archive-company-download?run_id=32位批ID&key=组key`：新key为`c:公司ID`，旧key为`l:原名SHA256`，必须绑定run；返回该公司该版本的全部当前可读CSV/XLSX的ZIP。未知kind不开放，工资及内部Excel须wages，客户Excel/施工/售料须orders，应收/收款须finance；各层共用分类。安全路径、目录/叶链接和已打开句柄校验，重复类型安全编号，任一授权文件缺失明确失败；ZIP全部写成后发送且临时文件清理。原单文件`/archives?file=`兼容且同样校验。
- 新生成无实际行的CSV不保存空表；真实0元仍保留。新kind明确`售料明细/票据应收/旧单据应收`；原`财务应收`可读，旧表可按有界CSV首行只读识别名称，文件字节不改。手动无需满一个月，当前月为截至生成时的快照；重新生成保留旧批，自动已结束月份范围不变。
- 财务 CSV 包含施工金额、已收、剩余应收、票据号、交票日期、收票人、附件说明、退回说明。项目 CSV 对仅有 orders 权限的账号只导出项目、施工金额、状态及备注；有 finance 权限时再增加已收、剩余应收和交票/退回列。项目导出每行优先使用订单保存的项目名/编号快照；施工/工资/财务导出项目编号同样优先快照。完整筛选导出不受当前页限制。
- `施工明细`、`工资明细`、`财务应收`继续按**施工月份**归档；施工明细 CSV 末行列出施工金额合计，月财务表增加已收、剩余款和交票资料及完整合计。四类 CSV 生成均检查金额溢出。
- 新增`收款流水`，按**到账月份**为公司生成CSV：即使施工发生在旧月份，当月到账也进入该月流水。包含收款编号、关联施工单及施工日期、到账日期、本笔金额、方式、凭证、附件说明、备注和有效/撤销状态。撤销记录保留，不计有效收款合计；表格下载须 finance 权限。没有该月流水的公司不生成空流水表。
- `/archives` 的 `files[].kind` 包括四类原 CSV 和两类施工 XLSX；`count`分别对应明细/单据/流水行数，`收款流水.amount_cents`为有效收款合计。自动补档月份同时考虑施工日期和到账日期；自动归档仍保留首次版本，后续补录/退回后手动重生成相关月份并保留旧文件。
- 云上传失败仍保留已成功生成的本地快照，并继续执行最近份数+每日/月度分层保留；云失败继续在记录显示。此修复不代替NAS离线告警、归档文件/密钥异机备份或真实云恢复验收。

本项目的页面、数据库事务、并发、导出和恢复仍需在独立环境中验证；本文不提供业务或 NAS 验收结论。

当前财务菜单分为交票管理和收款管理，不再显示旧施工单应收列表。`project-work` 的票据、`project-ticket` 当前及历史版本均提供 `ticket_mode_snapshot`，只用于收费来源票名显示；历史版缺该字段时只补读工地已存快照，不改版本内容。`single` 正常票为“统一票”，`split` 为“第一票/第二票”，追加及全工地累计保持独立票名；未设置快照只显示“正常票”，不猜公司当前配置。施工记录及逐人工资仍使用原列表，交票与收款不再次计算工资或库存。

## 2026-10-12 V3.0第六阶段报销与费用预支

- 所有报销和费用预支读取、写入、历史及进货选项均要求独立 `expenses` 权限。它与 `wages`、`finance` 分开；操作权限在接口和幂等响应重放前检查。
- `GET /api/expenses?tab=claims|advances|history&from=&to=&status=&q=&page=1` 每页100条。报销行含报销人、日期、类别、金额、状态、预支核销额、实际付款额、待付额及可选进货ID/快照；预支行分别返回实际付出、已核销、已退回和余额；历史行返回操作人、原因、操作号和前后快照。
- `GET /api/expense-purchases?q=&page=1` 返回可关联的有效进货单快照。关联只预留原进货总额内的报销额度，不新增库存、库存流水或商品采购成本。
- `POST /api/expense-action` 支持 `create-claim/update-claim/confirm-claim/void-claim/pay-claim/reverse-payment/create-advance/correct-advance/reverse-advance/allocate-advance/reverse-allocation/return-advance/reverse-return`。现有记录操作提交版本；撤销/冲销须带原因。报销确认不付款；分次实际付款不得超过确认金额扣除有效预支核销和既有实际付款后的余额。
- 费用预支余额为实际预支额减有效报销核销和员工实际退回；报销实际待付为确认金额减预支核销和实际已付。仅由预支完全核销的报销显示为“预支已核销”，不能误标为“已付清”。同名工资预支仍只从工资账本读取。

## 2026-10-12 V3.0第七阶段财务汇总、打印与备份

本节优先于上方历史归档分类说明；没有新增业务写入或迁移。具体可执行合同见 `.trellis/spec/backend/v3-phase7-finance-print-backup.md`，实际测试与限制以第七阶段任务记录为准。

### 财务汇总及同口径CSV

`GET /api/finance-summary?from=2026-10-01&to=2026-10-31` 要求 `finance`，在一次可重复读只读事务内汇总现有账本。日期含首尾；无参数默认上海本月1日至今天；两端必须同时填写，开始晚于结束或非法日期返回400。`all=1`为1000-01-01至今天，`all`仅接受空/0/1。`export=1`输出 UTF-8 BOM CSV，来自同一查询结果及权限投影，含分类、指标、金额元/分、统计口径、开始/结束日期、查询时间；GET不新增审计或改变业务状态，返回禁止缓存。

JSON响应为`{range:{from,to,all},queried_at,balance_note,groups:[{key,label,metrics:[{key,label,amount_cents,basis,source}]}]}`。金额`amount_cents`是十进制整数字符串，以免大额汇总丢失精度。无权组从JSON与CSV完全省略；导航到来源还须来源模块权限。

| group | 附加权限（均需finance） | 指标key及口径 |
|---|---|---|
| `customer` | `customer_prices` | `construction/sales`期间施工/售料收费；`received`期间实际到账；`outstanding`截至真正待收；`charge_balance/delivered/undelivered`累计收费/已交/未交；`unallocated/discount/customer_advance`截至未分配/减免/预收；`received_undated`历史无日期已收。 |
| `inventory` | `warehouse` | `purchase/purchase_balance`期间/累计有效进货；`inventory_value`查询时全部仓库当前参考货值。 |
| `payroll` | `wages` | `wage/wage_balance`期间/累计施工工资；`wage_adjustment`累计调整；`wage_paid/wage_final_paid`期间预支+最终现金/其中最终现金；`wage_unpaid`截至未发；`wage_deduction/wage_deduction_applied`确认扣款/其中已结算；`wage_advance/wage_advance_balance`期间工资预支/截至未抵扣；`wage_paid_undated`无日期历史已发。 |
| `expenses` | `expenses` | `expense_registered`期间登记费用；`expense_paid`期间实际补付报销；`expense_unpaid/expense_pending`截至已确认待付/待审核；`expense_advance/expense_advance_applied/expense_advance_returned`期间报账预支/核销/退回；`expense_advance_balance`截至未核销；`expense_cash`期间净付款。 |

收费只取保存的施工/售料金额一次；交票不生成第二份收费。真实到账不加收款分配或客户减免，待收按每公司`max(累计收费-减免-真实到账,0)`后相加，未分配到账同样减少该公司待收，预收不能抵另一公司欠款。工资预支与最终付款只计现金一次；未发扣除已结算扣款、已抵扣预支和最终已付，确认尚未结算扣款单列。报账核销不加现金；净付款=预支+实际补付-实际退回。

发生额按施工`work_date`、进货`work_date`、费用`expense_date`；到账/工资付款/报销付款分别按`received_at/paid_date`。余额累计至结束日期，使用当前有效更正结果，不重建历史关账。无日期旧已收/已付仅参与余额核对，不伪造期间现金。库存复用第二阶段商品合并全部仓库和累计采购均价估值；调拨不减少整体货值，采购报销不再次增加采购或库存。库存明确为查询时当前值；无采购价格商品参考货值0并提示。

### 费用导出

`GET /api/expenses?tab=claims|advances|history&from=&to=&status=&q=&export=1` 要求 `expenses`，复用该tab的完整已应用筛选，无当前页截断。CSV分别含报销登记/核销/补付/待付和进货快照、费用预支/核销/退回/余额、历史前后快照。金额按整数分精确转换成元，文本防CSV公式注入；核销列是余额移动，不冒充新付款。已有工资、库存、施工、交票及收款导出继续沿用其模块权限。

### 现有打印快照补齐

`ticketBatchItemView`（交票候选与批次保存快照）新增可选`qty_micro,price_cents,segments,category,region,charge_mode,charge_note,use_included_price`。新批次保存原单价/尺寸/部位/免费补做说明；旧快照没有字段时显示“未记录”，不从当前目录或订单补造旧票。响应客户投影保持无工资、罚款及库存金额。

施工客户版沿九列显示人员、工艺、配置小项及实际/原部位、单价、保存方量明细、方量小计、金额小计、备注、合计；方量和金额直接使用保存值，免费/补做、组合含价和特殊收费备注独立，售料单列且整单保存总额含售料。公司汇总和各工地打印读同一选择或批次快照，工地金额加总等于公司合计。内部施工版仅工资权限且返回工资时开放，独立付款/扣款/预支/未发以工资管理为准。预览和打印无交票、收款、库存写入。

### V3月度归档

- 原公司施工/工资按施工日期、实际到账按到账日期；原汇总`construction_cents`取一份客户验收表整单金额，包含售料，页面名称为“施工及售料金额”。施工CSV保留保存配置小项、实际部位、旧region，并增加收费模式/收费备注列，不重算金额。
- 新kind为`工资付款/工资调整/工资扣款/工资预支/进货金额/报销登记/报销付款/报账预支/报账核销/报账退回`，依次按`paid_date/effective_date/deduction_date/advance_date/work_date/expense_date/paid_date/advance_date/applied_date/returned_date`归档。负数调整保留；撤销行保留但不计有效合计；空表略过，真实0元记录保留。
- 新独立账表仅在`company`为空的全部公司生成，归入`company_id=system:business`、名称“全公司（工资付款、进货及报销）”，组key=`c:system:business`；不猜无公司费用归属或拆分跨公司工资付款。该组无公司施工/应收/工资汇总，不补造0。选择指定公司只生成该公司业务文件。
- 工资新CSV要求`wages`，费用新CSV要求`expenses`，进货要求`warehouse`，这些独立文件不要求客户价。内部施工Excel仍需`wages+customer_prices`；客户施工/售料/验收需`orders+customer_prices`，应收/到账需`finance+customer_prices`。列表、单文件、整组ZIP和归档任务权限同源；无权直接下载返回403。自动补档月份增加所有新发生/付款/核销/退回日期，仍只补已结束月份。

### 备份恢复边界

V3沿现有64表`backupTables`白名单、NDJSON/SHA、结束行数校验和新空`tegu_restore_*`目标恢复。包含施工、工地、售料/耗材、交票/批次/明细认领/版本、真实到账/分配/减免、精确库存/参考采购成本、工资及奖金并发锁、费用六表、账号权限、附件、审计/幂等和必要历史。会话不恢复；数据库迁移由目标初始化生成，不覆盖当前库，也不切换服务配置。

完整恢复必须独立保存匹配程序版本及运行/数据库配置、NDJSON及SHA、`BACKUP_DIR/monthly`原月表文件、`DATA_DIR/backup.key`（使用加密云凭据时）。附件本体在数据库内；归档manifest只记录路径，不能代替文件。无原key不能解密云凭据，需迁移key或重新填写。清单见`backend/v3-backup-recovery.md`；本阶段本机隔离合成恢复不代表最终服务器、NAS、Docker或云已验收。
