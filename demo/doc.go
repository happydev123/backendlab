// Package demo holds the automated demonstration scenario. It runs
// against the in-process demonstration service and a real PostgreSQL
// database and asserts on observed results:
//
//  1. structural response comparison
//  2. volatile-field exclusion
//  3. numeric tolerance
//  4. validation failure for inconsistent invoice totals
//  5. SSE parsing across fragmented writes
//  6. stream timeout and completion classification
//  7. offline plan comparison
//  8. background run submission and completion
//  9. cancellation of a running background run
//  10. an interrupted mutating run that is not retried automatically
//
// Run it with:
//
//	BACKENDLAB_TEST_DATABASE_URL=postgres://... go test -tags demo -count=1 -v ./demo/
//
// The scenario lives in build-tagged test files so ordinary "go test ./..."
// runs stay fast and independent of PostgreSQL.
package demo
