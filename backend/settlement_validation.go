package main

import (
	"strings"
	"time"
)

type SettlementOrder struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type SettlementRequest struct {
	Action           string            `json:"action"`
	Company          string            `json:"company_id"`
	AmountCents      int64             `json:"amount_cents"`
	ReceivedAt       string            `json:"received_at"`
	Method           string            `json:"method"`
	VoucherNo        string            `json:"voucher_no"`
	Attachment       string            `json:"attachment"`
	Note             string            `json:"note"`
	TicketNo         string            `json:"ticket_no"`
	DeliveredAt      string            `json:"delivered_at"`
	Recipient        string            `json:"recipient"`
	TicketAttachment string            `json:"ticket_attachment"`
	ReceiptFile      *AttachmentUpload `json:"receipt_file"`
	TicketFile       *AttachmentUpload `json:"ticket_file"`
	Orders           []SettlementOrder `json:"orders"`
}

func (p *SettlementRequest) validate() error {
	if !contains([]string{"submit", "deliver", "receive", "receive_full", "receive_partial", "return", "pay", "undo-pay"}, p.Action) {
		return fail(400, "结算操作无效")
	}
	if len(p.Orders) == 0 || len(p.Orders) > 100 {
		return fail(400, "请选择1至100张单据")
	}
	seen := map[string]bool{}
	for _, o := range p.Orders {
		if o.ID == "" || len(o.ID) > 64 || o.Version < 1 {
			return fail(400, "单据编号或版本无效，请重新选择")
		}
		if seen[o.ID] {
			return fail(400, "不能重复选择同一张单据")
		}
		seen[o.ID] = true
	}
	for _, field := range []struct {
		value *string
		limit int
		name  string
	}{
		{&p.Method, 40, "收款方式"}, {&p.VoucherNo, 100, "凭证号"}, {&p.Attachment, 500, "收款附件说明"},
		{&p.Note, 1000, "备注或退回说明"}, {&p.TicketNo, 100, "票据号"}, {&p.Recipient, 100, "收票人"}, {&p.TicketAttachment, 500, "交票附件说明"},
	} {
		*field.value = strings.TrimSpace(*field.value)
		if len([]rune(*field.value)) > field.limit {
			return fail(400, field.name+"超出长度限制")
		}
	}
	if strings.HasPrefix(strings.ToLower(p.Attachment), "file:") || strings.HasPrefix(strings.ToLower(p.TicketAttachment), "file:") {
		return fail(400, "已保存附件编号不能手工填写，请选择文件上传")
	}
	if p.ReceiptFile != nil && (!contains([]string{"receive", "receive_full", "receive_partial"}, p.Action) || p.Attachment != "") {
		return fail(400, "收款附件只能在收款时上传，且不能同时填写附件链接")
	}
	if p.TicketFile != nil && (p.Action != "deliver" || p.TicketAttachment != "") {
		return fail(400, "票据附件只能在交单时上传，且不能同时填写附件链接")
	}
	if (p.ReceiptFile != nil || p.TicketFile != nil) && len(p.Orders) != 1 {
		return fail(400, "上传附件时请一次只选择一张单据")
	}
	if p.Action == "deliver" {
		if p.Recipient == "" {
			return fail(400, "交单时必须填写收票人")
		}
		if _, e := time.Parse("2006-01-02", p.DeliveredAt); e != nil {
			return fail(400, "请填写有效的交付日期")
		}
	}
	if p.Action == "receive" || p.Action == "receive_full" || p.Action == "receive_partial" {
		if p.Action == "receive" && p.ReceivedAt == "" {
			p.ReceivedAt = time.Now().Format("2006-01-02")
		}
		if _, e := time.Parse("2006-01-02", p.ReceivedAt); e != nil {
			return fail(400, "请填写有效的到账日期")
		}
	}
	if p.Action == "receive_partial" {
		if len(p.Orders) != 1 {
			return fail(400, "部分收款一次只能选择一张施工单")
		}
		if p.AmountCents < 1 || p.AmountCents > 100000000000000 {
			return fail(400, "收款金额超出有效范围")
		}
	}
	if p.Action == "return" && p.Note == "" {
		return fail(400, "退回时必须填写原因")
	}
	return nil
}
