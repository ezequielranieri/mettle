package sandbox

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestInvokeAndRecord(t *testing.T) {
	s := New(FixtureTool("lookup_record", "acme", "inventory", false, "", "", map[string]any{"stock": 42}))
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := `{"tool":"lookup_record","args":{"product_id":42},"tenant":"acme","domain":"inventory","request_id":"req-1"}`
	resp, err := http.Post(srv.URL+"/tools/lookup_record", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var res CallResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !res.OK || res.Empty {
		t.Errorf("result ok/empty = %v/%v, want true/false", res.OK, res.Empty)
	}

	records := s.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.Request.Tool != "lookup_record" {
		t.Errorf("recorded tool = %q", rec.Request.Tool)
	}
	if rec.Request.Tenant != "acme" || rec.Request.Domain != "inventory" {
		t.Errorf("recorded tenant/domain = %s/%s", rec.Request.Tenant, rec.Request.Domain)
	}
	if rec.Request.Args["product_id"] != float64(42) {
		t.Errorf("recorded args = %v", rec.Request.Args)
	}
	if rec.Request.RequestID != "req-1" {
		t.Errorf("recorded request_id = %q", rec.Request.RequestID)
	}
}

func TestDiscoveryListsToolSpace(t *testing.T) {
	s := New(
		FixtureTool("lookup_record", "acme", "inventory", false, "", "", nil),
		FixtureTool("audit_log", "acme", "inventory", false, "", "", nil),
	)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/tools")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Tools []string `json:"tools"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Tools) != 2 || out.Tools[0] != "audit_log" || out.Tools[1] != "lookup_record" {
		t.Errorf("tools = %v, want sorted [audit_log lookup_record]", out.Tools)
	}
}

func TestUnknownToolIs404(t *testing.T) {
	s := New()
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/tools/nope", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if recs := s.Records(); len(recs) != 0 {
		t.Errorf("records = %d, want 0 (unknown tool is not a call)", len(recs))
	}
}

func TestEmptyIsExplicitAndDistinct(t *testing.T) {
	s := New(
		FixtureTool("not_found", "acme", "inventory", true, "record does not exist", "", nil),
		FixtureTool("no_data", "acme", "inventory", true, "record exists without data", "", nil),
	)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	for _, tc := range []struct{ tool, summary string }{
		{"not_found", "record does not exist"},
		{"no_data", "record exists without data"},
	} {
		resp, err := http.Post(srv.URL+"/tools/"+tc.tool, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("Post %s: %v", tc.tool, err)
		}
		var res CallResult
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			resp.Body.Close()
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		if !res.OK || !res.Empty {
			t.Errorf("%s: ok/empty = %v/%v, want true/true", tc.tool, res.OK, res.Empty)
		}
		if res.DataSummary != tc.summary {
			t.Errorf("%s: summary = %q, want %q", tc.tool, res.DataSummary, tc.summary)
		}
	}
}

func TestErrorIsExplicit(t *testing.T) {
	s := New(FixtureTool("flaky", "", "", false, "", "backend down", nil))
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/tools/flaky", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	var res CallResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.OK {
		t.Error("ok = true, want false")
	}
	if res.Error != "backend down" {
		t.Errorf("error = %q, want backend down", res.Error)
	}
}

func TestConcurrentCallsAreAllRecorded(t *testing.T) {
	s := New(FixtureTool("lookup_record", "acme", "inventory", false, "", "", nil))
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"tool":"lookup_record","args":{"i":%d}}`, i)
			resp, err := http.Post(srv.URL+"/tools/lookup_record", "application/json", strings.NewReader(body))
			if err != nil {
				t.Errorf("Post: %v", err)
				return
			}
			resp.Body.Close()
		}(i)
	}
	wg.Wait()

	if recs := s.Records(); len(recs) != n {
		t.Errorf("records = %d, want %d (no lost updates)", len(recs), n)
	}
}

func TestResetClearsLog(t *testing.T) {
	s := New(FixtureTool("lookup_record", "acme", "inventory", false, "", "", nil))
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/tools/lookup_record", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	resp.Body.Close()

	s.Reset()
	if recs := s.Records(); len(recs) != 0 {
		t.Errorf("records after Reset = %d, want 0", len(recs))
	}
}