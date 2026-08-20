package leaseaudit

import (
	"leasereg/internal/lease"
	"testing"
)

func TestCheckHealthySnapshot(t *testing.T) {
	findings := Check(lease.Analysis{
		Resources: []lease.ResourceAnalysis{{Name: "printer-1", Held: true}},
		Tokens:    []lease.TokenAnalysis{{Resource: "printer-1", Current: 1, Next: 2, Monotonic: true}},
	})
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none", findings)
	}
}

func TestCheckReportsBrokenFencingCounter(t *testing.T) {
	findings := Check(lease.Analysis{
		Tokens: []lease.TokenAnalysis{{Resource: "printer-1", Current: 4, Next: 4, Monotonic: true}},
	})
	if len(findings) != 1 || findings[0].Code != "fencing_counter_invalid" {
		t.Fatalf("findings = %+v, want fencing counter finding", findings)
	}
}
