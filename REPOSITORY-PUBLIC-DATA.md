# Public Source and Wage Data

This repository contains the Tegu V3 application source and the public catalog dataset. The new wage values below come from the user's explicit written instructions dated 2026-10-16; the original Excel workbooks are not included.

## Wage Data

| Work item | Wage | Unit / note |
| --- | ---: | --- |
| Ordinary polypropylene | 5.00 yuan | per square meter; special site subsidy is recorded separately |
| Dongjian ordinary polypropylene | 5.50 yuan | per square meter |
| Yiren ordinary polypropylene | 5.50 yuan | per square meter |
| Self-adhesive membrane | 8.00 yuan | per square meter |
| Consecutive two-coat waterproofing | 3.50 yuan | per square meter |
| Normal two-coat waterproofing | 4.50 yuan | per square meter, plus a separate 0.50 yuan per square meter year-end bonus |
| Scraped-tooth waterproofing | 4.50 yuan | per square meter |
| Three coats and one cloth | 10.00 yuan | per square meter; special cases are recorded by actual site conditions |
| Roughcast over polypropylene | 2.50 yuan | per square meter; two-component back adhesive or interface agent with cement and sand |

The ordinary polypropylene 5%-15% year-end award remains an annual manual assessment and is not calculated automatically by area. Waterproof items not listed above have no wage configured in the updated catalog.

## Data Location and Limits

- Updated catalog: `catalog/catalog-seed-v1.json`.
- The catalog is an importable source file; this Git publication does not mean the values have been written to any running database.
- No raw Excel workbook, database dump, business transactions, account data, credentials, backups, or local `.env` files are included.
- The private repository receives the source-code changes without this updated wage catalog.
- Tests and builds were not run for this publication, per the user's instruction.
