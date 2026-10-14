package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// Uses only the opt-in disposable schemas after the external runner has
// verified the independent MySQL identity and its schema-limited grants.
func TestProjectCombinationSnapshotRoundTripMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	d := f.save(t, Order{Company: "loc-co", Address: "合成组合恢复", Date: "2026-10-01", Lines: combinationActualLines()})
	d = f.set(t, d, []ProjectCombination{combinationPair(d, "polypropylene", 40000000)}, 200)
	before := f.read(t, d.ID)
	target, _ := maintenanceFaultDB(t)
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	file, err := f.a.backup()
	if err != nil {
		t.Fatal("write real combination snapshot", err)
	}
	counts, err := inspectSnapshot(filepath.Join(dir, file))
	if err != nil || counts["project_combinations"] != 1 {
		t.Fatal("snapshot omitted combination relationships", counts, err)
	}
	if err = target.restore(filepath.Join(dir, file)); err != nil {
		t.Fatal("restore combination snapshot to separate disposable schema", err)
	}
	restored := f
	restored.a = target
	restored.admin = restored.session(t, f.admin.id)
	after := restored.read(t, d.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("restored order/combination/price/actual-work snapshots differ", before, after)
	}
	restored.assertWages(t, after, 120000, 150000)
	restored.assertDraftTicket(t, after.ProjectID, 480000)
	var legacyConfig string
	if err = target.db.QueryRow("SELECT included_by_craft_id FROM crafts WHERE id='combo-p'").Scan(&legacyConfig); err != nil || legacyConfig != "combo-w" {
		t.Fatal("legacy configuration lost during additive restore", legacyConfig, err)
	}
	// A restored record remains editable, not merely readable from a dump.
	restored.set(t, after, []ProjectCombination{}, 200)
	after = restored.read(t, d.ID)
	if after.TotalCents != 600000 || len(after.Combinations) != 0 {
		t.Fatal("restored combination cannot be explicitly released", after)
	}
	restored.assertWages(t, after, 120000, 150000)
	restored.assertDraftTicket(t, after.ProjectID, 600000)
}

func TestProjectCombinationConcurrentCrossOrderReservationMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	p := f.save(t, Order{Company: "loc-co", Address: "合成跨单并发预占", Date: "2026-10-01", Lines: combinationActualLines()[1:]})
	orders := []orderDetailView{}
	for _, date := range []string{"2026-10-08", "2026-10-09"} {
		orders = append(orders, f.save(t, Order{Company: "loc-co", Project: p.ProjectID, Address: p.Address, Date: date, Lines: combinationActualLines()[:1]}))
	}
	makePair := func(w, poly orderDetailView) ProjectCombination {
		return ProjectCombination{WaterproofLine: w.Lines[0].ID, PolypropyleneLine: poly.Lines[0].ID, WaterproofVersion: w.Version, PolypropyleneVersion: poly.Version, Direction: "polypropylene", WaivedQty: 70000000}
	}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, w := range orders {
		wg.Add(1)
		go func(w orderDetailView) {
			defer wg.Done()
			_, err := f.call(f.u, "/api/project-combination", id(), combinationPayload(w, []ProjectCombination{makePair(w, p)}))
			errors <- err
		}(w)
	}
	wg.Wait()
	close(errors)
	succeeded, conflicted := 0, 0
	for err := range errors {
		if err == nil {
			succeeded++
		} else if e, ok := err.(failure); ok && e.Code == 409 {
			conflicted++
		} else {
			t.Fatal("unexpected cross-order race failure", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatal("concurrent cross-order area double-reserved", succeeded, conflicted)
	}
	p = f.read(t, p.ID)
	if p.TotalCents != 90000 || len(p.Combinations) != 1 || p.Lines[0].WaivedQty != 70000000 {
		t.Fatal("race did not preserve exactly one 70-square-metre waiver", p)
	}
	f.assertWages(t, p, 0, 150000)
	for _, w := range orders {
		current := f.read(t, w.ID)
		if current.TotalCents != 300000 {
			t.Fatal("unwaived water charge changed", current)
		}
		f.assertWages(t, current, 120000, 0)
		if len(current.Combinations) == 0 {
			// Even a fresh project/order version cannot spend the same area twice.
			f.set(t, current, []ProjectCombination{makePair(current, p)}, 409)
		}
	}
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM project_combinations", 1)
	maintenanceFaultCount(t, f.a.db, "SELECT COUNT(*) FROM order_worker_wages", 3)
	f.assertDraftTicket(t, p.ProjectID, 690000)
}

func TestProjectCombinationPagedRelatedLineAndCompanyScopeMySQL(t *testing.T) {
	f := newCombinationMySQLFixture(t)
	d := f.save(t, Order{Company: "loc-co", Address: "合成远期组合候选", Date: "2026-10-01", Lines: combinationActualLines()})
	d = f.set(t, d, []ProjectCombination{combinationPair(d, "polypropylene", 40000000)}, 200)
	lines := []Line{}
	for i := 0; i < 51; i++ {
		lines = append(lines, Line{ClientRef: fmt.Sprintf("paged-%d", i), CraftID: "combo-w", Worker: "loc-a", ActualLocation: fmt.Sprintf("后续施工部位%d", i), Segments: []Segment{{Length: 1, Width: 1}}})
	}
	f.save(t, Order{Company: "loc-co", Project: d.ProjectID, Address: d.Address, Date: "2026-10-10", Lines: lines})
	readCandidates := func(query string) struct {
		Rows     []combinationCandidate `json:"rows"`
		Total    int                    `json:"total"`
		Page     int                    `json:"page"`
		PageSize int                    `json:"page_size"`
	} {
		var result struct {
			Rows     []combinationCandidate `json:"rows"`
			Total    int                    `json:"total"`
			Page     int                    `json:"page"`
			PageSize int                    `json:"page_size"`
		}
		response := f.request(t, f.admin, "GET", "/api/project-combinations?project_id="+d.ProjectID+query, "", nil, 200)
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	original := map[string]bool{}
	for _, line := range d.Lines {
		original[line.ID] = true
	}
	first := readCandidates("&page=1")
	if first.Total != 53 || first.Page != 1 || first.PageSize != 50 || len(first.Rows) != 50 {
		t.Fatal("candidate pagination silently drops construction rows", first)
	}
	for _, line := range first.Rows {
		if original[line.ID] {
			t.Fatal("old relation fixture did not reach another page")
		}
	}
	second := readCandidates("&page=2")
	found := 0
	for _, line := range second.Rows {
		if original[line.ID] {
			found++
		}
	}
	if len(second.Rows) != 3 || found != 2 {
		t.Fatal("older actual-work lines missing from page 2", second)
	}
	targeted := readCandidates("&line_ids=" + d.Lines[0].ID + "," + d.Lines[1].ID)
	if targeted.Total != 2 || len(targeted.Rows) != 2 {
		t.Fatal("reopen cannot recover explicit off-page relation", targeted)
	}
	reopened := f.read(t, d.ID)
	if !reflect.DeepEqual(reopened.Combinations, d.Combinations) || reopened.TotalCents != 480000 {
		t.Fatal("paged candidates changed persisted pair", reopened)
	}
	f.exec(t, "INSERT INTO companies(id,name,ticket_mode) VALUES('combo-other-co','合成另一公司','single')")
	f.exec(t, "INSERT INTO crafts(id,company_id,source_id,category,name,mode,price_cents,wage_cents) VALUES('combo-other-water','combo-other-co','combo-w-source','防水涂料','另一公司防水','平方',6000,0)")
	other := f.save(t, Order{Company: "combo-other-co", Address: d.Address, Date: "2026-10-10", Lines: []Line{{CraftID: "combo-other-water", Worker: "loc-a", ActualLocation: "另一公司部位", Segments: []Segment{{Length: 1, Width: 1}}}}})
	scoped := readCandidates("&line_ids=" + d.Lines[0].ID + "," + other.Lines[0].ID)
	if scoped.Total != 1 || len(scoped.Rows) != 1 || scoped.Rows[0].ID != d.Lines[0].ID {
		t.Fatal("explicit line lookup bypassed company/project boundary", scoped)
	}
	attempted := ProjectCombination{WaterproofLine: other.Lines[0].ID, PolypropyleneLine: d.Lines[1].ID, WaterproofVersion: other.Version, PolypropyleneVersion: d.Version, Direction: "polypropylene", WaivedQty: 1000000}
	f.set(t, reopened, []ProjectCombination{attempted}, 400)
	if f.read(t, d.ID).TotalCents != 480000 || f.read(t, other.ID).TotalCents != 6000 {
		t.Fatal("rejected cross-company waiver changed saved charges")
	}
}
