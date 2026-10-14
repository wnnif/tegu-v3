package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
)

const companyWageOverrideMigrationVersion = 453

func migrateCompanyWageOverrides(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS company_wage_overrides(
		company_id VARCHAR(64) NOT NULL,
		craft_id VARCHAR(64) NOT NULL,
		wage_cents BIGINT NOT NULL,
		classification VARCHAR(24) NOT NULL,
		source JSON NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		PRIMARY KEY(company_id,craft_id),
		INDEX idx_company_wage_override_craft(craft_id),
		CONSTRAINT fk_company_wage_override_company FOREIGN KEY(company_id) REFERENCES companies(id),
		CONSTRAINT fk_company_wage_override_craft FOREIGN KEY(craft_id) REFERENCES crafts(id),
		CHECK(wage_cents>=0 AND wage_cents<=100000000),
		CHECK(classification IN ('confirmed','test_only'))
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	_, err := db.Exec("INSERT IGNORE INTO schema_migrations(version) VALUES(?)", companyWageOverrideMigrationVersion)
	return err
}

func companyWageOverrideProvenanceID(companyID, craftID string) string {
	digest := sha256.Sum256([]byte(companyID + ":" + craftID))
	return hex.EncodeToString(digest[:])
}

func insertCompanyWageOverride(ctx context.Context, tx *sql.Tx, seedID string, sources []catalogSeedSource, override catalogSeedCompanyWageOverride) error {
	sourceJSON, err := json.Marshal(struct {
		Link catalogSeedReference `json:"link"`
		Wage catalogSeedValue     `json:"wage"`
	}{Link: override.Source, Wage: override.Wage})
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO company_wage_overrides(company_id,craft_id,wage_cents,classification,source)
		VALUES(?,?,?,?,?)`, override.CompanyID, override.CraftID, override.Wage.Cents, override.Wage.Classification, string(sourceJSON)); err != nil {
		return err
	}
	return insertCatalogSeedProvenance(ctx, tx, seedID, sources, "company_wage_override", companyWageOverrideProvenanceID(override.CompanyID, override.CraftID), "wage_override", override.Wage.Classification, override.Wage.Source, override.Wage.Classification == "test_only", map[string]any{
		"company_id":  override.CompanyID,
		"craft_id":    override.CraftID,
		"link_source": override.Source,
		"wage":        override.Wage,
	})
}
