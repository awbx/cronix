package historyutil

import "testing"

func TestFoldJournaldWrappedRecords(t *testing.T) {
	raw := []byte(`{"__REALTIME_TIMESTAMP":"1714820000000000","_SYSTEMD_UNIT":"cronix-billing-reconcile-0.service","MESSAGE":"{\"time\":\"2026-05-04T12:33:20Z\",\"level\":\"INFO\",\"msg\":\"trigger: success\",\"run_id\":\"run-A\",\"status\":200,\"attempt\":1}"}
{"__REALTIME_TIMESTAMP":"1714820100000000","_SYSTEMD_UNIT":"cronix-billing-reconcile-0.service","MESSAGE":"{\"msg\":\"trigger: server error\",\"run_id\":\"run-B\",\"status\":500,\"attempt\":1}"}
{"__REALTIME_TIMESTAMP":"1714820200000000","_SYSTEMD_UNIT":"cronix-billing-reconcile-0.service","MESSAGE":"{\"msg\":\"trigger: retries exhausted\",\"run_id\":\"run-B\",\"status\":500,\"attempt\":3}"}
`)
	entries := FoldShimLogs(raw, "billing", "reconcile", "journald", "")
	if len(entries) != 2 {
		t.Fatalf("expected 2 runs, got %d: %+v", len(entries), entries)
	}
	if entries[0].RunID != "run-A" || entries[0].Status != "ok" || entries[0].Source != "journald" {
		t.Errorf("first wrong: %+v", entries[0])
	}
	if entries[1].RunID != "run-B" || entries[1].Status != "failed" || entries[1].Attempt != 3 {
		t.Errorf("second wrong: %+v", entries[1])
	}
	if entries[0].App != "billing" || entries[0].Job != "reconcile" {
		t.Errorf("app/job not stamped: %+v", entries[0])
	}
}

func TestFoldRawShimLines(t *testing.T) {
	// k8s pod log style — no journald wrapping.
	raw := []byte(`{"time":"2026-05-04T12:00:00Z","msg":"trigger: success","run_id":"run-X","status":200,"attempt":1}
{"time":"2026-05-04T12:05:00Z","msg":"trigger: app rejected","run_id":"run-Y","status":401,"attempt":1}
`)
	entries := FoldShimLogs(raw, "billing", "ping", "k8s-pod-log", "")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Source != "k8s-pod-log" {
		t.Errorf("source not stamped: %+v", entries[0])
	}
	if entries[1].Status != "failed" {
		t.Errorf("expected failed for app rejected, got %q", entries[1].Status)
	}
}

func TestFoldStatusFilter(t *testing.T) {
	raw := []byte(`{"msg":"trigger: success","run_id":"a","attempt":1}
{"msg":"trigger: app rejected","run_id":"b","status":401,"attempt":1}
`)
	entries := FoldShimLogs(raw, "billing", "ping", "test", "failed")
	if len(entries) != 1 || entries[0].RunID != "b" {
		t.Errorf("expected only failed run, got %+v", entries)
	}
}

func TestFoldSkipsNonLifecycleEvents(t *testing.T) {
	raw := []byte(`{"msg":"trigger: load spec","run_id":"x","attempt":0}
{"msg":"trigger: lock acquire","run_id":"x","attempt":0}
{"msg":"trigger: success","run_id":"x","status":200,"attempt":1}
`)
	entries := FoldShimLogs(raw, "billing", "ping", "test", "")
	if len(entries) != 1 || entries[0].Status != "ok" {
		t.Errorf("expected only success folded in, got %+v", entries)
	}
}

func TestClassifyShimEvent(t *testing.T) {
	cases := map[string]struct {
		status   string
		terminal bool
	}{
		"trigger: success":           {"ok", true},
		"trigger: app rejected":      {"failed", true},
		"trigger: retries exhausted": {"failed", true},
		"trigger: lock contended":    {"lock-contended", true},
		"trigger: panic":             {"failed", true},
		"trigger: server error":      {"failed", false},
		"trigger: attempt failed":    {"failed", false},
		"trigger: lock acquire":      {"", false},
		"":                           {"", false},
	}
	for msg, want := range cases {
		gotStatus, gotTerm := ClassifyShimEvent(msg)
		if gotStatus != want.status || gotTerm != want.terminal {
			t.Errorf("classify(%q) = (%q, %v), want (%q, %v)", msg, gotStatus, gotTerm, want.status, want.terminal)
		}
	}
}
