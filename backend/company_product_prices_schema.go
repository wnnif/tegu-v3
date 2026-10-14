package main

import "database/sql"

const companyProductPricesMigrationVersion = 456

func migrateCompanyProductPrices(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS company_product_prices(
		id VARCHAR(64) PRIMARY KEY,
		seed_id VARCHAR(100) NOT NULL,
		company_id VARCHAR(64) NOT NULL,
		product_id VARCHAR(64) NOT NULL,
		input_unit VARCHAR(10) NOT NULL,
		price_cents BIGINT NOT NULL,
		source_id VARCHAR(64) NOT NULL,
		source_sheet VARCHAR(100) NOT NULL,
		source_row INT NOT NULL,
		note VARCHAR(1000) NOT NULL DEFAULT '',
		version BIGINT NOT NULL DEFAULT 1,
		UNIQUE KEY uq_company_product_price_unit(company_id,product_id,input_unit),
		INDEX idx_company_product_prices_product(product_id,company_id),
		CONSTRAINT fk_company_product_prices_seed FOREIGN KEY(seed_id) REFERENCES catalog_seed_imports(seed_id),
		CONSTRAINT fk_company_product_prices_company FOREIGN KEY(company_id) REFERENCES companies(id),
		CONSTRAINT fk_company_product_prices_product FOREIGN KEY(product_id) REFERENCES products(id),
		CHECK(input_unit IN ('base','aux')),
		CHECK(price_cents BETWEEN 0 AND 100000000),
		CHECK(source_row > 0)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`); err != nil {
		return err
	}
	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='order_sales' AND COLUMN_NAME='company_product_price_id'`).Scan(&columnCount); err != nil {
		return err
	}
	if columnCount == 0 {
		if _, err := db.Exec(`ALTER TABLE order_sales ADD COLUMN company_product_price_id VARCHAR(64) NULL`); err != nil {
			return err
		}
	}
	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='order_sales' AND INDEX_NAME='idx_order_sales_company_product_price'`).Scan(&indexCount); err != nil {
		return err
	}
	if indexCount == 0 {
		if _, err := db.Exec(`CREATE INDEX idx_order_sales_company_product_price ON order_sales(company_product_price_id)`); err != nil {
			return err
		}
	}
	var constraintCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE CONSTRAINT_SCHEMA=DATABASE() AND TABLE_NAME='order_sales' AND CONSTRAINT_NAME='fk_order_sales_company_product_price'`).Scan(&constraintCount); err != nil {
		return err
	}
	if constraintCount == 0 {
		if _, err := db.Exec(`ALTER TABLE order_sales ADD CONSTRAINT fk_order_sales_company_product_price FOREIGN KEY(company_product_price_id) REFERENCES company_product_prices(id)`); err != nil {
			return err
		}
	}
	_, err := db.Exec(`INSERT IGNORE INTO schema_migrations(version) VALUES(?)`, companyProductPricesMigrationVersion)
	return err
}
