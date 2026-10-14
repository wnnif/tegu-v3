package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// This fixture runs the real ticket methods with an in-memory SQL driver. It
// neither opens a network connection nor models MySQL locking/concurrency.
// Its write allowlist rejects any attempt to post receivables, stock, wages,
// receipts, or an actual construction change from a ticket-only operation.
type ticketOfflineStore struct {
	Companies map[string]string
	Orders    map[string]ticketOfflineOrder
	Items     map[string]ticketBatchItemView
	Packages  map[string]ticketOfflinePackage
	Claims    map[string]string
	Batches   map[string]ticketOfflineBatch
	Snapshots map[string][]string
	Versions  map[string]map[int64]string
	Sources   map[string]int64
	Receipts  map[string]int64
	Stock     map[string]int64
	Wages     map[string]int64
}

type ticketOfflinePackage struct {
	ProjectID, PackageID, Name, ChargedTicketID string
	RoomCount                                   int64
	PriceCents                                  int64
}

type ticketOfflineOrder struct {
	CompanyID, ProjectID, Number, Date, ProjectName, Address, WorkerName string
	Managed                                                              bool
	Version                                                              int64
}

type ticketOfflineBatch struct {
	View      ticketBatchView
	CreatedBy string
	VoidedBy  string
}

type ticketOfflineFixture struct {
	store      *ticketOfflineStore
	calls      []string
	unexpected []string
}

func newTicketOfflineFixture(t *testing.T) (*App, *ticketOfflineFixture) {
	t.Helper()
	f := &ticketOfflineFixture{store: &ticketOfflineStore{
		Companies: map[string]string{"company-a": "合成公司甲"},
		Orders: map[string]ticketOfflineOrder{
			"order-a": {"company-a", "site-a", "SG-A", "2026-10-12", "工地甲", "合成地址甲", "师傅甲", true, 1},
			"order-b": {"company-a", "site-b", "SG-B", "2026-10-12", "工地乙", "合成地址乙", "师傅乙", true, 1},
		},
		Items: map[string]ticketBatchItemView{
			"line/free": {ItemID: "free", ItemKind: "line", OrderID: "order-a", ProjectID: "site-a", ItemName: "免费施工", CraftID: "craft-a", Quantity: 3, Unit: "平方", TotalCents: 0, SourceProjectTicketID: "source-a"},
			"line/work": {ItemID: "work", ItemKind: "line", OrderID: "order-b", ProjectID: "site-b", ItemName: "实际施工", CraftID: "craft-b", SubitemID: "part-b", SubitemName: "主卫", Location: "东侧", WorkerName: "师傅乙", Quantity: 2.5, Unit: "平方", TotalCents: 12500, SourceProjectTicketID: "source-b"},
			"sale/sale": {ItemID: "sale", ItemKind: "sale", OrderID: "order-a", ProjectID: "site-a", ItemName: "单独售料", Quantity: 2, Unit: "桶", TotalCents: 6000, SourceProjectTicketID: "source-a"},
		},
		Packages: map[string]ticketOfflinePackage{},
		Claims:   map[string]string{}, Batches: map[string]ticketOfflineBatch{}, Snapshots: map[string][]string{}, Versions: map[string]map[int64]string{},
		Sources:  map[string]int64{"source-a": 6000, "source-b": 12500},
		Receipts: map[string]int64{"old-receipt": 1000}, Stock: map[string]int64{"warehouse/product": 9000000}, Wages: map[string]int64{"worker-a": 2200},
	}}
	db := sql.OpenDB(ticketOfflineConnector{f})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return &App{db: db}, f
}

type ticketOfflineConnector struct{ fixture *ticketOfflineFixture }

func (c ticketOfflineConnector) Connect(context.Context) (driver.Conn, error) {
	return &ticketOfflineConn{fixture: c.fixture}, nil
}
func (c ticketOfflineConnector) Driver() driver.Driver { return ticketOfflineDriver{} }

type ticketOfflineDriver struct{}

func (ticketOfflineDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("offline connector only; no DSN is accepted")
}

type ticketOfflineConn struct {
	fixture *ticketOfflineFixture
	tx      *ticketOfflineStore
}

func (c *ticketOfflineConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (c *ticketOfflineConn) Close() error { return nil }
func (c *ticketOfflineConn) Begin() (driver.Tx, error) {
	raw, err := json.Marshal(c.fixture.store)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &c.tx); err != nil {
		return nil, err
	}
	return c, nil
}
func (c *ticketOfflineConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *ticketOfflineConn) Commit() error {
	c.fixture.store, c.tx = c.tx, nil
	return nil
}
func (c *ticketOfflineConn) Rollback() error { c.tx = nil; return nil }
func (c *ticketOfflineConn) state() *ticketOfflineStore {
	if c.tx != nil {
		return c.tx
	}
	return c.fixture.store
}

func ticketOfflineArg(args []driver.NamedValue, n int) string { return fmt.Sprint(args[n].Value) }
func ticketOfflineInt(args []driver.NamedValue, n int) int64  { return args[n].Value.(int64) }
func ticketOfflineKey(kind, id string) string                 { return kind + "/" + id }
func ticketOfflineNullable(value string) driver.Value {
	if value == "" {
		return nil
	}
	return value
}
func ticketOfflineSQL(q string) string { return strings.Join(strings.Fields(q), " ") }
func (c *ticketOfflineConn) unexpected(q string) error {
	c.fixture.unexpected = append(c.fixture.unexpected, q)
	return fmt.Errorf("offline fixture rejected unexpected SQL: %s", q)
}

func (c *ticketOfflineConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	q := ticketOfflineSQL(query)
	c.fixture.calls = append(c.fixture.calls, q)
	s := c.state()
	switch {
	case strings.HasPrefix(q, "INSERT INTO ticket_batches("):
		id := ticketOfflineArg(args, 0)
		s.Batches[id] = ticketOfflineBatch{View: ticketBatchView{ID: id, CompanyID: ticketOfflineArg(args, 1), CompanyName: ticketOfflineArg(args, 2), Number: ticketOfflineArg(args, 3), TicketNo: ticketOfflineArg(args, 4), DeliveredAt: ticketOfflineArg(args, 5), Recipient: ticketOfflineArg(args, 6), Status: "delivered", Version: 1, TotalCents: ticketOfflineInt(args, 7), CreatedByName: ticketOfflineArg(args, 9), CreatedAt: "2026-10-12 10:00:00"}, CreatedBy: ticketOfflineArg(args, 8)}
	case strings.HasPrefix(q, "UPDATE order_lines SET ticket_batch_id=?"), strings.HasPrefix(q, "UPDATE order_sales SET ticket_batch_id=?"):
		kind := "line"
		if strings.HasPrefix(q, "UPDATE order_sales") {
			kind = "sale"
		}
		key := ticketOfflineKey(kind, ticketOfflineArg(args, 1))
		if _, exists := s.Items[key]; !exists || s.Claims[key] != "" {
			return driver.RowsAffected(0), nil
		}
		s.Claims[key] = ticketOfflineArg(args, 0)
	case strings.HasPrefix(q, "INSERT INTO ticket_batch_items("):
		batch := ticketOfflineArg(args, 0)
		s.Snapshots[batch] = append(s.Snapshots[batch], ticketOfflineArg(args, 15))
	case strings.HasPrefix(q, "INSERT INTO ticket_batch_versions("):
		batch, version := ticketOfflineArg(args, 0), ticketOfflineInt(args, 1)
		if s.Versions[batch] == nil {
			s.Versions[batch] = map[int64]string{}
		}
		if _, exists := s.Versions[batch][version]; exists {
			return nil, errors.New("duplicate version")
		}
		s.Versions[batch][version] = ticketOfflineArg(args, 3)
	case strings.HasPrefix(q, "UPDATE ticket_batches SET status='voided'"):
		id := ticketOfflineArg(args, 3)
		b, exists := s.Batches[id]
		if !exists || b.View.Status != "delivered" || b.View.Version != ticketOfflineInt(args, 4) {
			return driver.RowsAffected(0), nil
		}
		b.View.Status, b.View.Version = "voided", b.View.Version+1
		b.View.VoidReason, b.VoidedBy, b.View.VoidedByName = ticketOfflineArg(args, 0), ticketOfflineArg(args, 1), ticketOfflineArg(args, 2)
		s.Batches[id] = b
	case strings.HasPrefix(q, "UPDATE order_lines SET ticket_batch_id=NULL"), strings.HasPrefix(q, "UPDATE order_sales SET ticket_batch_id=NULL"):
		if !strings.HasSuffix(q, "WHERE id=? AND ticket_batch_id=?") {
			return nil, c.unexpected(q)
		}
		kind := "line"
		if strings.HasPrefix(q, "UPDATE order_sales") {
			kind = "sale"
		}
		key := ticketOfflineKey(kind, ticketOfflineArg(args, 0))
		if s.Claims[key] != ticketOfflineArg(args, 1) {
			return driver.RowsAffected(0), nil
		}
		delete(s.Claims, key)
	case strings.HasPrefix(q, "INSERT INTO audit("):
	case q == "UPDATE project_ticket_receipts SET batch_id='' WHERE batch_id=? AND voided=0":
		// This fixture has no batch-labelled allocation; existing real cash stays.
	default:
		return nil, c.unexpected(q)
	}
	return driver.RowsAffected(1), nil
}

func (c *ticketOfflineConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q := ticketOfflineSQL(query)
	c.fixture.calls = append(c.fixture.calls, q)
	s := c.state()
	switch {
	case q == "SELECT DISTINCT receipt_id FROM project_ticket_receipts WHERE batch_id=? AND voided=0 ORDER BY receipt_id", q == "SELECT id FROM customer_discounts WHERE batch_id=? AND voided=0 ORDER BY id":
		return ticketOfflineRowsOf(), nil
	case q == "SELECT name FROM companies WHERE id=?":
		return ticketOfflineRowsOf([]driver.Value{s.Companies[ticketOfflineArg(args, 0)]}), nil
	case strings.HasPrefix(q, "SELECT COUNT(*) FROM (SELECT * FROM (SELECT l.id AS item_id"):
		return ticketOfflineRowsOf([]driver.Value{int64(len(ticketOfflineCandidates(s, ticketOfflineArg(args, 0))))}), nil
	case strings.HasPrefix(q, "SELECT * FROM (SELECT l.id AS item_id"):
		candidates := ticketOfflineCandidates(s, ticketOfflineArg(args, 0))
		start, size := int(ticketOfflineInt(args, len(args)-1)), int(ticketOfflineInt(args, len(args)-2))
		if start >= len(candidates) {
			return ticketOfflineRowsOf(), nil
		}
		end := start + size
		if end > len(candidates) {
			end = len(candidates)
		}
		return ticketOfflineRowsOf(candidates[start:end]...), nil
	case strings.HasPrefix(q, "SELECT 'line' AS item_kind"), strings.HasPrefix(q, "SELECT 'sale' AS item_kind"):
		out := [][]driver.Value{}
		for _, arg := range args {
			for _, kind := range []string{"line", "sale"} {
				item, exists := s.Items[ticketOfflineKey(kind, fmt.Sprint(arg.Value))]
				if !exists {
					continue
				}
				o := s.Orders[item.OrderID]
				if o.Managed {
					out = append(out, []driver.Value{kind, item.ItemID, item.OrderID, item.ProjectID, o.CompanyID})
				}
			}
		}
		return ticketOfflineRowsOf(out...), nil
	case strings.HasPrefix(q, "SELECT pp.project_id,pp.package_id,pp.name,pp.room_count,pp.price_cents,pp.charged_ticket_id FROM project_packages pp WHERE (pp.project_id,pp.charged_ticket_id) IN ("):
		selected := make(map[string]string, len(args)/2)
		for i := 0; i+1 < len(args); i += 2 {
			selected[ticketOfflineArg(args, i)+"\x00"+ticketOfflineArg(args, i+1)] = ticketOfflineArg(args, i+1)
		}
		packageIDs := make([]string, 0, len(s.Packages))
		for id := range s.Packages {
			packageIDs = append(packageIDs, id)
		}
		sort.Strings(packageIDs)
		out := [][]driver.Value{}
		for _, id := range packageIDs {
			pkg := s.Packages[id]
			if ticket, ok := selected[pkg.ProjectID+"\x00"+pkg.ChargedTicketID]; !ok || ticket != pkg.ChargedTicketID {
				continue
			}
			alreadyDelivered := false
			for batchID, batch := range s.Batches {
				if batch.View.Status != "delivered" {
					continue
				}
				for _, raw := range s.Snapshots[batchID] {
					var item ticketBatchItemView
					if err := json.Unmarshal([]byte(raw), &item); err != nil {
						return nil, err
					}
					if item.ProjectID == pkg.ProjectID && item.SourceProjectTicketID == pkg.ChargedTicketID && (item.ItemKind == "line" || item.ItemKind == "sale" || item.ItemKind == "package") {
						alreadyDelivered = true
						break
					}
				}
				if alreadyDelivered {
					break
				}
			}
			if !alreadyDelivered {
				out = append(out, []driver.Value{pkg.ProjectID, pkg.PackageID, pkg.Name, pkg.RoomCount, pkg.PriceCents, pkg.ChargedTicketID})
			}
		}
		return ticketOfflineRowsOf(out...), nil
	case strings.HasPrefix(q, "SELECT id FROM projects WHERE id=?"):
		id := ticketOfflineArg(args, 0)
		for _, o := range s.Orders {
			if o.ProjectID == id && (len(args) == 1 || o.CompanyID == ticketOfflineArg(args, 1)) {
				return ticketOfflineRowsOf([]driver.Value{id}), nil
			}
		}
		return ticketOfflineRowsOf(), nil
	case strings.HasPrefix(q, "SELECT id,company_id,project_id,status,number,"):
		id := ticketOfflineArg(args, 0)
		o := s.Orders[id]
		return ticketOfflineRowsOf([]driver.Value{id, o.CompanyID, o.ProjectID, "进行中", o.Number, o.Date, o.ProjectName, o.Address, o.WorkerName, o.Managed, o.Version}), nil
	case q == "SELECT id FROM orders WHERE id=? FOR UPDATE":
		id := ticketOfflineArg(args, 0)
		if _, exists := s.Orders[id]; !exists {
			return ticketOfflineRowsOf(), nil
		}
		return ticketOfflineRowsOf([]driver.Value{id}), nil
	case strings.HasPrefix(q, "SELECT name,craft_id,category,subitem_id,"):
		item, exists := s.Items[ticketOfflineKey("line", ticketOfflineArg(args, 0))]
		if !exists || item.OrderID != ticketOfflineArg(args, 1) {
			return ticketOfflineRowsOf(), nil
		}
		return ticketOfflineRowsOf([]driver.Value{item.ItemName, item.CraftID, item.Category, item.SubitemID, item.SubitemName, item.Location, item.WorkerName, item.Quantity, item.QtyMicro, item.Unit, ticketOfflinePrintPrice(item), item.TotalCents, item.Segments, item.Region, item.Description, item.ChargeMode, item.ChargeNote, item.IncludedPrice, ticketOfflineNullable(s.Claims[ticketOfflineKey("line", item.ItemID)])}), nil
	case strings.HasPrefix(q, "SELECT product_name,CAST(sale_qty_micro"):
		item, exists := s.Items[ticketOfflineKey("sale", ticketOfflineArg(args, 0))]
		if !exists || item.OrderID != ticketOfflineArg(args, 1) {
			return ticketOfflineRowsOf(), nil
		}
		return ticketOfflineRowsOf([]driver.Value{item.ItemName, item.Quantity, item.QtyMicro, item.Unit, ticketOfflinePrintPrice(item), item.TotalCents, ticketOfflineNullable(s.Claims[ticketOfflineKey("sale", item.ItemID)])}), nil
	case strings.HasPrefix(q, "SELECT COALESCE(t.id,''),COALESCE(t.status,''),i.order_id,"):
		item := s.Items[ticketOfflineKey(ticketOfflineArg(args, 1), ticketOfflineArg(args, 0))]
		o := s.Orders[item.OrderID]
		return ticketOfflineRowsOf([]driver.Value{item.SourceProjectTicketID, "草稿", item.OrderID, o.CompanyID, item.ProjectID}), nil
	case strings.HasPrefix(q, "SELECT DATE_FORMAT(created_at,"):
		return ticketOfflineRowsOf([]driver.Value{"2026-10-12 10:00:00"}), nil
	case strings.HasPrefix(q, "SELECT DISTINCT project_id,order_id FROM ticket_batch_items"):
		out, seen := [][]driver.Value{}, map[string]bool{}
		for _, raw := range s.Snapshots[ticketOfflineArg(args, 0)] {
			var item ticketBatchItemView
			if err := json.Unmarshal([]byte(raw), &item); err != nil {
				return nil, err
			}
			key := item.ProjectID + "/" + item.OrderID
			if !seen[key] {
				seen[key] = true
				out = append(out, []driver.Value{item.ProjectID, item.OrderID})
			}
		}
		return ticketOfflineRowsOf(out...), nil
	case strings.HasPrefix(q, "SELECT id,company_id,company_name,number,ticket_no,"):
		b := s.Batches[ticketOfflineArg(args, 0)]
		v := b.View
		return ticketOfflineRowsOf([]driver.Value{v.ID, v.CompanyID, v.CompanyName, v.Number, v.TicketNo, v.DeliveredAt, v.Recipient, v.Status, v.Version, v.TotalCents, v.VoidReason, v.VoidedByName, v.CreatedByName, v.CreatedAt, b.CreatedBy, b.VoidedBy}), nil
	case strings.HasPrefix(q, "SELECT snapshot FROM ticket_batch_items"):
		out := [][]driver.Value{}
		for _, raw := range s.Snapshots[ticketOfflineArg(args, 0)] {
			out = append(out, []driver.Value{raw})
		}
		return ticketOfflineRowsOf(out...), nil
	case strings.HasPrefix(q, "SELECT snapshot FROM ticket_batch_versions"):
		versions := s.Versions[ticketOfflineArg(args, 0)]
		latest := int64(0)
		for v := range versions {
			if v > latest {
				latest = v
			}
		}
		if latest == 0 {
			return ticketOfflineRowsOf(), nil
		}
		return ticketOfflineRowsOf([]driver.Value{versions[latest]}), nil
	case strings.HasPrefix(q, "SELECT ticket_batch_id FROM order_lines"), strings.HasPrefix(q, "SELECT ticket_batch_id FROM order_sales"):
		kind := "line"
		if strings.Contains(q, "FROM order_sales") {
			kind = "sale"
		}
		return ticketOfflineRowsOf([]driver.Value{ticketOfflineNullable(s.Claims[ticketOfflineKey(kind, ticketOfflineArg(args, 0))])}), nil
	default:
		return nil, c.unexpected(q)
	}
}

func ticketOfflineCandidates(s *ticketOfflineStore, companyID string) [][]driver.Value {
	keys := make([]string, 0, len(s.Items))
	for key := range s.Items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := [][]driver.Value{}
	for _, key := range keys {
		item := s.Items[key]
		order := s.Orders[item.OrderID]
		if order.CompanyID != companyID || !order.Managed {
			continue
		}
		status := "pending"
		if s.Claims[key] != "" {
			status = "delivered"
		} else if item.SourceProjectTicketID == "" {
			status = "unlinked"
		}
		result = append(result, []driver.Value{item.ItemID, item.ItemKind, item.OrderID, order.Date, order.Number, item.ProjectID, order.ProjectName, order.Address, item.CraftID, item.SubitemID, item.ItemName, item.SubitemName, item.Location, item.WorkerName, item.Quantity, item.Unit, item.TotalCents, status, s.Claims[key], order.Version, item.Category, item.QtyMicro, ticketOfflinePrintPrice(item), item.Segments, item.Region, item.Description, item.ChargeMode, item.ChargeNote, item.IncludedPrice})
	}
	return result
}

func ticketOfflinePrintPrice(item ticketBatchItemView) driver.Value {
	if item.PriceCents == nil {
		return int64(0)
	}
	return *item.PriceCents
}

type ticketOfflineRows struct {
	values  [][]driver.Value
	at      int
	columns []string
}

func ticketOfflineRowsOf(values ...[]driver.Value) driver.Rows {
	columns := []string{}
	if len(values) > 0 {
		for i := range values[0] {
			columns = append(columns, fmt.Sprintf("column%d", i))
		}
	}
	return &ticketOfflineRows{values: values, columns: columns}
}
func (r *ticketOfflineRows) Columns() []string { return r.columns }
func (r *ticketOfflineRows) Close() error      { return nil }
func (r *ticketOfflineRows) Next(dest []driver.Value) error {
	if r.at == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.at])
	r.at++
	return nil
}

func ticketOfflineCreate(t *testing.T, a *App, u User, selected ...ticketSelection) (ticketBatchResponse, error) {
	t.Helper()
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw, err := json.Marshal(ticketBatchCreateRequest{CompanyID: "company-a", Items: selected, DeliveredAt: "2026-10-12", Recipient: "合成收票人", TicketNo: "纸票号"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := a.createTicketBatch(tx, u, raw, "offline-ticket-operation")
	if err != nil {
		return ticketBatchResponse{}, err
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return value.(ticketBatchResponse), nil
}

func ticketOfflineStatus(t *testing.T, err error, want int) {
	t.Helper()
	var f failure
	if !errors.As(err, &f) || f.Code != want {
		t.Fatalf("error=%v; want HTTP %d", err, want)
	}
}

func TestTicketBatchOfflineCrossSiteFreeRowAndDuplicateClaim(t *testing.T) {
	a, f := newTicketOfflineFixture(t)
	u := User{ID: "actor", Name: "合成交票人", Role: "employee", Permissions: []string{"finance", "customer_prices"}}
	batch, err := ticketOfflineCreate(t, a, u, ticketSelection{ItemID: "work", ItemKind: "line", OrderVersion: 1}, ticketSelection{ItemID: "free", ItemKind: "line", OrderVersion: 1}, ticketSelection{ItemID: "sale", ItemKind: "sale", OrderVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Batch.TotalCents != 18500 || len(batch.Items) != 3 {
		t.Fatalf("cross-site total/snapshot=%+v", batch)
	}
	sites := map[string]bool{}
	for _, item := range batch.Items {
		sites[item.ProjectID] = true
		if item.SourceProjectTicketID == "" {
			t.Fatal("snapshot lost receivable source")
		}
		if item.ItemID == "free" && (item.TotalCents != 0 || item.Quantity != 3) {
			t.Fatal("free performed row was charged or lost")
		}
	}
	if len(sites) != 2 {
		t.Fatalf("cross-site selection collapsed: %v", sites)
	}
	_, err = ticketOfflineCreate(t, a, u, ticketSelection{ItemID: "work", ItemKind: "line", OrderVersion: 1})
	ticketOfflineStatus(t, err, 409)
	if len(f.store.Batches) != 1 || len(f.unexpected) != 0 {
		t.Fatalf("duplicate posted a batch or an unexpected SQL: %v", f.unexpected)
	}
}

func TestTicketBatchOfflinePackageChargeIsSnapshottedOnce(t *testing.T) {
	a, f := newTicketOfflineFixture(t)
	f.store.Packages["package-a"] = ticketOfflinePackage{
		ProjectID: "site-a", PackageID: "package-a", Name: "合成房型套餐",
		RoomCount: 3, PriceCents: 45000, ChargedTicketID: "source-a",
	}
	f.store.Sources["source-a"] += 45000
	ledgersBefore := jsonText([]any{f.store.Sources, f.store.Receipts, f.store.Stock, f.store.Wages})
	u := User{ID: "actor", Role: "admin"}
	batch, err := ticketOfflineCreate(t, a, u,
		ticketSelection{ItemID: "work", ItemKind: "line", OrderVersion: 1},
		ticketSelection{ItemID: "sale", ItemKind: "sale", OrderVersion: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Batch.TotalCents != 63500 || len(batch.Items) != 3 {
		t.Fatalf("package amount was omitted or duplicated: %+v", batch)
	}
	packageCount := 0
	for _, item := range batch.Items {
		if item.ItemKind != "package" {
			continue
		}
		packageCount++
		if item.PackageID != "package-a" || item.PackageRoomCount != 3 || item.TotalCents != 45000 || item.SourceProjectTicketID != "source-a" {
			t.Fatalf("package snapshot lost its saved billing identity: %+v", item)
		}
	}
	if packageCount != 1 {
		t.Fatalf("expected one package row for two selected rows from the same ticket, got %d", packageCount)
	}
	second, err := ticketOfflineCreate(t, a, u, ticketSelection{ItemID: "free", ItemKind: "line", OrderVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if second.Batch.TotalCents != 0 || len(second.Items) != 1 || second.Items[0].ItemKind != "line" {
		t.Fatalf("a later batch for another row on the same source ticket repeated the package charge: %+v", second)
	}
	if after := jsonText([]any{f.store.Sources, f.store.Receipts, f.store.Stock, f.store.Wages}); after != ledgersBefore {
		t.Fatal("ticketing a package posted another receivable, payment, stock movement, or worker wage")
	}
}

func TestTicketBatchOfflineVoidReopenPreservesOtherBatchAndLedgers(t *testing.T) {
	a, f := newTicketOfflineFixture(t)
	u := User{ID: "actor", Name: "合成交票人", Role: "admin"}
	ledgersBefore := jsonText([]any{f.store.Sources, f.store.Receipts, f.store.Stock, f.store.Wages})
	one, err := ticketOfflineCreate(t, a, u, ticketSelection{ItemID: "free", ItemKind: "line", OrderVersion: 1}, ticketSelection{ItemID: "work", ItemKind: "line", OrderVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	other, err := ticketOfflineCreate(t, a, u, ticketSelection{ItemID: "sale", ItemKind: "sale", OrderVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(jsonText(ticketBatchActionRequest{ID: one.Batch.ID, Version: 1, Action: "void", Reason: "核对后更正"}))
	value, err := a.ticketBatchAction(tx, u, raw, "offline-void-operation")
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	voided := value.(ticketBatchResponse)
	if voided.Batch.Status != "voided" || voided.Batch.VoidReason != "核对后更正" || voided.Batch.VoidedByName != u.Name {
		t.Fatalf("void history incomplete: %+v", voided.Batch)
	}
	if f.store.Claims["line/free"] != "" || f.store.Claims["line/work"] != "" || f.store.Claims["sale/sale"] != other.Batch.ID {
		t.Fatalf("void touched wrong claims: %v", f.store.Claims)
	}
	if f.store.Batches[other.Batch.ID].View.Status != "delivered" {
		t.Fatal("void affected another normal batch")
	}
	if len(f.store.Versions[one.Batch.ID]) != 2 || len(f.store.Snapshots[one.Batch.ID]) != 2 {
		t.Fatal("void erased original history")
	}
	if after := jsonText([]any{f.store.Sources, f.store.Receipts, f.store.Stock, f.store.Wages}); after != ledgersBefore {
		t.Fatal("create/void changed receivables, existing receipts, stock, or wages")
	}

	// Simulate the result of a separately permitted source-row correction after
	// void. This test does not claim to test the order-edit transaction itself.
	corrected := f.store.Items["line/work"]
	corrected.Quantity, corrected.TotalCents = 2, 10000
	f.store.Items["line/work"] = corrected
	f.store.Sources["source-b"] = 10000
	order := f.store.Orders["order-b"]
	order.Version++
	f.store.Orders["order-b"] = order
	ledgersCorrected := jsonText([]any{f.store.Sources, f.store.Receipts, f.store.Stock, f.store.Wages})
	reopened, err := ticketOfflineCreate(t, a, u, ticketSelection{ItemID: "free", ItemKind: "line", OrderVersion: 1}, ticketSelection{ItemID: "work", ItemKind: "line", OrderVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Batch.ID == one.Batch.ID || reopened.Batch.TotalCents != 10000 {
		t.Fatalf("reopen did not use corrected source: %+v", reopened.Batch)
	}
	if after := jsonText([]any{f.store.Sources, f.store.Receipts, f.store.Stock, f.store.Wages}); after != ledgersCorrected {
		t.Fatal("reopen added a receivable, stock, wage, or receipt posting")
	}
	if ledgersBefore == ledgersCorrected {
		t.Fatal("fixture correction failed to change the source charge")
	}
	r := httptest.NewRecorder()
	if err := a.ticketBatchRead(r, httptest.NewRequest("GET", "/api/ticket-batch?id="+one.Batch.ID, nil), u); err != nil {
		t.Fatal(err)
	}
	var history ticketBatchResponse
	if err := json.Unmarshal(r.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if history.Batch.Status != "voided" || history.Batch.TotalCents != 12500 {
		t.Fatalf("voided snapshot was rewritten: %+v", history.Batch)
	}
	sort.Slice(history.Items, func(i, j int) bool { return history.Items[i].ItemID < history.Items[j].ItemID })
	if history.Items[1].ItemID != "work" || history.Items[1].Quantity != 2.5 || history.Items[1].TotalCents != 12500 {
		t.Fatalf("original item snapshot changed: %+v", history.Items)
	}
	if len(f.unexpected) != 0 {
		t.Fatalf("non-ticket SQL attempted: %v", f.unexpected)
	}
}

func TestTicketBatchOfflineRejectsMissingSourceAndOtherCompany(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ticketOfflineStore)
		code   int
	}{
		{name: "missing receivable source", change: func(s *ticketOfflineStore) {
			item := s.Items["line/work"]
			item.SourceProjectTicketID = ""
			s.Items["line/work"] = item
		}, code: 409},
		{name: "other company site", change: func(s *ticketOfflineStore) {
			order := s.Orders["order-b"]
			order.CompanyID = "company-other"
			s.Orders["order-b"] = order
		}, code: 400},
		{name: "unmanaged legacy order", change: func(s *ticketOfflineStore) {
			order := s.Orders["order-b"]
			order.Managed = false
			s.Orders["order-b"] = order
		}, code: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, f := newTicketOfflineFixture(t)
			tc.change(f.store)
			_, err := ticketOfflineCreate(t, a, User{ID: "actor", Role: "admin"}, ticketSelection{ItemID: "work", ItemKind: "line", OrderVersion: 1})
			ticketOfflineStatus(t, err, tc.code)
			if len(f.store.Batches) != 0 || len(f.store.Claims) != 0 || len(f.unexpected) != 0 {
				t.Fatalf("rejected selection changed batch claims or attempted unrelated SQL: %+v", f)
			}
		})
	}
}

func TestTicketBatchOfflinePermissionsRejectBeforeSQL(t *testing.T) {
	for _, permissions := range [][]string{{"finance"}, {"customer_prices"}, {}} {
		t.Run(fmt.Sprint(permissions), func(t *testing.T) {
			a, f := newTicketOfflineFixture(t)
			u := User{ID: "denied", Role: "employee", Permissions: permissions}
			_, err := ticketOfflineCreate(t, a, u, ticketSelection{ItemID: "work", ItemKind: "line", OrderVersion: 1})
			ticketOfflineStatus(t, err, 403)
			tx, err := a.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.ticketBatchAction(tx, u, []byte(jsonText(ticketBatchActionRequest{ID: "any", Version: 1, Action: "void", Reason: "合成原因"})), "offline-denied-operation")
			tx.Rollback()
			ticketOfflineStatus(t, err, 403)
			if len(f.calls) != 0 {
				t.Fatalf("denied user reached SQL: %v", f.calls)
			}
		})
	}
}

func TestTicketBatchOfflineRejectsChangedOrderAfterPreview(t *testing.T) {
	for _, changedKey := range []string{"line/work", "sale/sale"} {
		t.Run(changedKey, func(t *testing.T) {
			a, f := newTicketOfflineFixture(t)
			u := User{ID: "actor", Role: "admin"}
			tx, err := a.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			_, rows, err := readTicketIssueRows(tx, "company-a", "", 1, 100)
			tx.Rollback()
			if err != nil {
				t.Fatal(err)
			}
			printed := []ticketSelection{}
			for _, row := range rows {
				if row.ItemID == "work" || row.ItemID == "sale" {
					printed = append(printed, ticketSelection{ItemID: row.ItemID, ItemKind: row.ItemKind, OrderVersion: row.OrderVersion})
				}
			}
			if len(printed) != 2 {
				t.Fatalf("preview lost cross-site construction/sale candidates: %+v", rows)
			}
			// A separately saved correction changes the source before the paper
			// delivery is confirmed. No actual order-save/MySQL work is performed.
			changed := f.store.Items[changedKey]
			changed.TotalCents += 500
			f.store.Items[changedKey] = changed
			f.store.Sources[changed.SourceProjectTicketID] += 500
			order := f.store.Orders[changed.OrderID]
			order.Version++
			f.store.Orders[changed.OrderID] = order
			before := jsonText(f.store)
			_, err = ticketOfflineCreate(t, a, u, printed...)
			ticketOfflineStatus(t, err, 409)
			if !strings.Contains(err.Error(), "刷新核对后重新打印") {
				t.Fatalf("version conflict did not tell the user to reprint: %v", err)
			}
			if jsonText(f.store) != before || len(f.unexpected) != 0 {
				t.Fatal("stale preview created claims/batch or changed source/stock/wage/receipt history")
			}
			for i := range printed {
				if printed[i].ItemID == changed.ItemID && printed[i].ItemKind == changed.ItemKind {
					printed[i].OrderVersion = order.Version
				}
			}
			batch, err := ticketOfflineCreate(t, a, u, printed...)
			if err != nil || batch.Batch.TotalCents != 19000 {
				t.Fatalf("freshly reviewed version did not confirm corrected charge: %+v err=%v", batch, err)
			}
		})
	}
}
