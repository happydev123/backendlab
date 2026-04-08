package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
)

func TestListRunsPagingAndFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	var ids []int64
	for i := 0; i < 5; i++ {
		r, err := db.EnqueueRun(ctx, newRun(fmt.Sprintf("suite%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	// Cancel runs 1 and 3.
	if _, err := db.Pool().Exec(ctx, `
		UPDATE runs SET status = 'cancelled', finished_at = now(), cancel_requested_at = now()
		WHERE id = ANY($1)`, []int64{ids[1], ids[3]}); err != nil {
		t.Fatal(err)
	}

	page, total, err := db.ListRuns(ctx, store.ListRunsParams{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(page) != 2 || page[0].ID != ids[4] || page[1].ID != ids[3] {
		t.Fatalf("first page = %v (total %d)", summaryIDs(page), total)
	}
	page, _, err = db.ListRuns(ctx, store.ListRunsParams{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != ids[0] {
		t.Fatalf("last page = %v", summaryIDs(page))
	}

	cancelled, total, err := db.ListRuns(ctx, store.ListRunsParams{Statuses: []store.Status{store.StatusCancelled}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(cancelled) != 2 || cancelled[0].ID != ids[3] || cancelled[1].ID != ids[1] {
		t.Fatalf("cancelled = %v (total %d)", summaryIDs(cancelled), total)
	}
	if !cancelled[0].CancelRequested || cancelled[0].FinishedAt == nil || cancelled[0].Status != store.StatusCancelled {
		t.Errorf("summary = %+v", cancelled[0])
	}
	both, total, err := db.ListRuns(ctx, store.ListRunsParams{
		Statuses: []store.Status{store.StatusQueued, store.StatusCancelled},
	})
	if err != nil || total != 5 || len(both) != 5 {
		t.Errorf("two statuses: %d rows, total %d, err %v", len(both), total, err)
	}
}

func TestListRunsRejectsBadParams(t *testing.T) {
	t.Parallel()
	db, _ := storetest.New(t)
	ctx := context.Background()
	for _, p := range []store.ListRunsParams{
		{Limit: store.MaxListLimit + 1},
		{Limit: -1},
		{Offset: -1},
		{Statuses: []store.Status{"bogus"}},
	} {
		if _, _, err := db.ListRuns(ctx, p); err == nil {
			t.Errorf("params %+v accepted", p)
		}
	}
}

func TestListCaseResultsAndReport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	r, err := db.EnqueueRun(ctx, newRun("orders"))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := db.ListCaseResults(ctx, r.ID)
	if err != nil || len(empty) != 0 {
		t.Fatalf("results of a fresh run = %v, %v", empty, err)
	}
	if _, err := db.Pool().Exec(ctx, `
		INSERT INTO case_results (run_id, case_index, case_id, status, findings, observations, bodies, written_token, recorded_at)
		VALUES ($1, 1, 'second', 'failed',
		        '[{"module":"compare","code":"compare.value_changed","kind":"difference","severity":"error","message":"m"}]',
		        '[{"target":"baseline","status_code":200}]', '{"baseline":"{}"}', 1, now()),
		       ($1, 0, 'first', 'passed', '[]', '[]', NULL, 1, now())`, r.ID); err != nil {
		t.Fatal(err)
	}
	results, err := db.ListCaseResults(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].CaseID != "first" || results[1].CaseID != "second" {
		t.Fatalf("results = %+v", results)
	}
	second := results[1]
	if len(second.Findings) != 1 || second.Findings[0].Code != "compare.value_changed" ||
		len(second.Observations) != 1 || second.Observations[0].StatusCode != 200 ||
		second.Bodies["baseline"] != "{}" {
		t.Errorf("second = %+v", second)
	}
	if results[0].Bodies != nil {
		t.Errorf("bodies of first = %v, want nil", results[0].Bodies)
	}

	if _, err := db.GetReport(ctx, r.ID); !errors.Is(err, store.ErrNoReport) {
		t.Errorf("report of queued run: err = %v", err)
	}
	if _, err := db.GetReport(ctx, 987654); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("report of missing run: err = %v", err)
	}
	if _, err := db.Pool().Exec(ctx, `UPDATE runs SET report = '{"schema":"x"}' WHERE id = $1`, r.ID); err != nil {
		t.Fatal(err)
	}
	data, err := db.GetReport(ctx, r.ID)
	if err != nil || string(data) != `{"schema":"x"}` {
		t.Errorf("report = %q, %v", data, err)
	}
}

func summaryIDs(list []store.RunSummary) []int64 {
	out := make([]int64, len(list))
	for i, s := range list {
		out[i] = s.ID
	}
	return out
}
