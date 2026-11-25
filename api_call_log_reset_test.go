package kvm

import (
	"encoding/json"
	"strings"
	"testing"
)

// After a reset the record is empty, and its lists are [] in JSON, not null
// (the browser reads them right away).
func TestResetApiCallLogJSON(t *testing.T) {
	recordAPICall(APICallEntry{Source: apiCallSourceMCP, Name: "kvm_status", OK: true})
	log := rpcResetApiCallLog()
	if len(log.Entries) != 0 || len(log.Counts) != 0 || len(log.Totals) != 0 {
		t.Fatalf("not empty after reset: %+v", log)
	}
	data, err := json.Marshal(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"entries":[]`, `"counts":[]`, `"totals":{}`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("%s missing in %s", want, data)
		}
	}
}
