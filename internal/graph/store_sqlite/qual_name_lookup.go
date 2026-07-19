package store_sqlite

import "encoding/json"

// nodes_by_qual is the existing UNIQUE partial qualified-name index. The
// explicit partial predicate lets SQLite prove the index is applicable, while
// one JSON value keeps even very large resolver pages to a single bind and a
// single indexed query. nodes is WITHOUT ROWID, so id is the stable secondary
// order (and the unique index guarantees at most one row per qual_name).
const nodesByQualNameLookupSQL = `SELECT ` + lookupNodeCols + `
FROM nodes INDEXED BY nodes_by_qual
WHERE qual_name <> ''
  AND qual_name IN (
    SELECT CAST(value AS TEXT)
    FROM json_each(?)
    WHERE CAST(value AS TEXT) <> ''
  )
ORDER BY qual_name, id`

func qualNameLookupPayload(qualNames []string) string {
	payload, err := json.Marshal(qualNames)
	if err != nil {
		// Encoding a []string cannot fail. Keep the lookup fail-closed if the
		// standard library ever changes that contract.
		return "[]"
	}
	return string(payload)
}
