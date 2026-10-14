package main

import "database/sql"

// Caller holds the source worksite lock. Charge corrections keep the real cash
// header untouched and release newest allocations first, preserving old rows.
func reconcileTicketCash(tx *sql.Tx, u User, ticketID string, total int64, event string) error {
	_, allocated, discount, err := ticketMoneyBalance(tx, ticketID)
	if err != nil {
		return err
	}
	if total < 0 {
		return fail(400, "工地有效收费超出范围")
	}
	reason := event + "：有效收费调整，超过收费部分返回未分配余额"
	if discount > total {
		rows, err := scanRows(tx, "SELECT id FROM customer_discounts WHERE ticket_id=? AND voided=0 ORDER BY created_at DESC,id DESC FOR UPDATE", []any{ticketID}, func(r *sql.Rows, v *string) error { return r.Scan(v) })
		if err != nil {
			return err
		}
		for _, discountID := range rows {
			if discount <= total {
				break
			}
			d, err := readCustomerDiscount(tx, discountID, true)
			if err != nil {
				return err
			}
			release := discount - total
			if release > d.Amount {
				release = d.Amount
			}
			remaining := d.Amount - release
			if remaining == 0 {
				_, err = tx.Exec("UPDATE customer_discounts SET voided=1,void_reason=?,version=version+1 WHERE id=?", reason, discountID)
			} else {
				_, err = tx.Exec("UPDATE customer_discounts SET amount_cents=?,version=version+1 WHERE id=?", remaining, discountID)
			}
			if err != nil {
				return err
			}
			if err = saveCustomerDiscountVersion(tx, u, discountID, "收费更正调整减免", reason); err != nil {
				return err
			}
			if err = audit(tx, u, "收费更正调整减免", discountID, map[string]any{"before": d, "after_cents": remaining, "reason": reason}); err != nil {
				return err
			}
			discount -= release
		}
	}
	capacity := total - discount
	if allocated <= capacity {
		return nil
	}
	allocations, err := scanRows(tx, `SELECT r.id,r.receipt_id,r.ticket_id,r.batch_id,t.project_id,t.address,t.number,COALESCE(b.number,''),r.amount_cents,r.voided,DATE_FORMAT(r.created_at,'%Y-%m-%d %H:%i:%s') FROM project_ticket_receipts r JOIN project_tickets t ON t.id=r.ticket_id LEFT JOIN ticket_batches b ON b.id=r.batch_id WHERE r.ticket_id=? AND r.voided=0 ORDER BY r.created_at DESC,r.id DESC`, []any{ticketID}, func(r *sql.Rows, v *receiptAllocationView) error {
		return r.Scan(&v.ID, &v.ReceiptID, &v.TicketID, &v.BatchID, &v.ProjectID, &v.Address, &v.Number, &v.BatchNumber, &v.Amount, &v.Voided, &v.CreatedAt)
	})
	if err != nil {
		return err
	}
	changed := map[string]bool{}
	for _, d := range allocations {
		if d.ReceiptID == "" {
			return fail(409, "历史收款尚未关联实际到账，不能自动调整分配")
		}
		changed[d.ReceiptID] = true
	}
	for _, receiptID := range sortedKeys(changed) {
		if _, err = readCompanyReceipt(tx, receiptID, true); err != nil {
			return err
		}
	}
	changed = map[string]bool{}
	for _, d := range allocations {
		if allocated <= capacity {
			break
		}
		release := allocated - capacity
		if release > d.Amount {
			release = d.Amount
		}
		if err = resizeReceiptAllocation(tx, u, d, d.Amount-release, reason); err != nil {
			return err
		}
		allocated -= release
		changed[d.ReceiptID] = true
	}
	for _, receiptID := range sortedKeys(changed) {
		if _, err = tx.Exec("UPDATE company_receipts SET version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?", receiptID); err != nil {
			return err
		}
		if err = saveCompanyReceiptVersion(tx, u, receiptID, "收费更正返还未分配款", reason); err != nil {
			return err
		}
	}
	return nil
}

// Paper voiding changes a label only. It never voids real cash or its worksite
// allocation. The same source is available for a later corrected paper batch.
func unlinkTicketBatchCash(tx *sql.Tx, u User, batchID, reason string) error {
	rows, err := scanRows(tx, "SELECT DISTINCT receipt_id FROM project_ticket_receipts WHERE batch_id=? AND voided=0 ORDER BY receipt_id", []any{batchID}, func(r *sql.Rows, v *string) error { return r.Scan(v) })
	if err != nil {
		return err
	}
	for _, receiptID := range rows {
		if receiptID == "" {
			return fail(409, "批次分配缺少真实收款关联")
		}
		if _, err = readCompanyReceipt(tx, receiptID, true); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE project_ticket_receipts SET batch_id='' WHERE batch_id=? AND voided=0", batchID); err != nil {
		return err
	}
	for _, receiptID := range rows {
		if _, err = tx.Exec("UPDATE company_receipts SET version=version+1,updated_at=UTC_TIMESTAMP() WHERE id=?", receiptID); err != nil {
			return err
		}
		if err = saveCompanyReceiptVersion(tx, u, receiptID, "作废交票保留工地分配", reason); err != nil {
			return err
		}
	}
	discounts, err := scanRows(tx, "SELECT id FROM customer_discounts WHERE batch_id=? AND voided=0 ORDER BY id", []any{batchID}, func(r *sql.Rows, v *string) error { return r.Scan(v) })
	if err != nil {
		return err
	}
	for _, discountID := range discounts {
		if _, err = tx.Exec("UPDATE customer_discounts SET batch_id='',version=version+1 WHERE id=?", discountID); err != nil {
			return err
		}
		if err = saveCustomerDiscountVersion(tx, u, discountID, "作废交票保留工地减免", reason); err != nil {
			return err
		}
	}
	return nil
}
