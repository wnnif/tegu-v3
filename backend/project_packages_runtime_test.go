package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidProjectPackageScope(t *testing.T) {
	for rooms := 2; rooms <= 5; rooms++ {
		if !validProjectPackageScope("room_count", rooms) {
			t.Fatalf("legacy room-count package with %d rooms rejected", rooms)
		}
	}
	if !validProjectPackageScope("fixed_scope", 0) {
		t.Fatal("fixed-scope package with zero room count rejected")
	}
	for _, test := range []struct {
		packageType string
		roomCount   int
	}{{"room_count", 0}, {"room_count", 1}, {"room_count", 6}, {"fixed_scope", 1}, {"fixed_scope", 2}, {"other", 0}} {
		if validProjectPackageScope(test.packageType, test.roomCount) {
			t.Fatalf("invalid package scope accepted: type=%s rooms=%d", test.packageType, test.roomCount)
		}
	}
}

func TestFixedScopePackageCoverageUsesExactCompanyCraftAndSubitemIDs(t *testing.T) {
	pkg := &projectPackageSnapshot{
		PackageType: "fixed_scope",
		RoomCount:   0,
		Coverage: []packageCoverageSnapshot{{
			ItemName: "整套施工中的墙固",
			CraftID:  "company-craft-wall",
			SubitemID: "wall",
			Mapping:  "mapped",
		}},
	}
	if !packageCoversLine(pkg, "company-craft-wall", "wall") {
		t.Fatal("the explicitly mapped package line must be covered")
	}
	if packageCoversLine(pkg, "company-craft-wall", "floor") || packageCoversLine(pkg, "other-company-craft", "wall") {
		t.Fatal("coverage must not extend to similarly named crafts or other subitems")
	}
	if packageCoversLine(&projectPackageSnapshot{PackageType: "fixed_scope", Coverage: []packageCoverageSnapshot{{CraftID: "unmapped", Mapping: "pending"}}}, "unmapped", "") {
		t.Fatal("pending source rows must not suppress an individual charge")
	}
}

func TestFixedScopeProjectPackageSnapshotExposesTypeWithoutRoomCountLabel(t *testing.T) {
	data, err := json.Marshal(projectPackageChargeView{ID: "pkg", Name: "百姓家整套收费", PackageType: "fixed_scope", RoomCount: 0, Amount: 180000})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"package_type":"fixed_scope"`) || !strings.Contains(text, `"room_count":0`) {
		t.Fatalf("fixed-scope type and stored zero scope should be explicit in the model: %s", text)
	}
}
