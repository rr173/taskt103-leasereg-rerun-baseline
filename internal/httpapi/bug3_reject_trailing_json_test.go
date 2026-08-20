package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRejectsTrailingJSONValues(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	resp, err := http.Post(ts.URL+"/acquire", "application/json", strings.NewReader(`{"resource":"R","holder":"alice","ttl_seconds":60}{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
