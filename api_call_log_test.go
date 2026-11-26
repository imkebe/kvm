package kvm

import (
	"strings"
	"testing"
)

func TestSummarizeAPIArgs(t *testing.T) {
	got := summarizeAPIArgs([]byte(`{"text":"secret words","x":3,"keys":[1,2],"command":"ls -la"}`))
	for _, want := range []string{`"text":"<12 characters>"`, `"keys":"<2 items>"`, `"command":"ls -la"`, `"x":3`} {
		if !strings.Contains(got, want) {
			t.Fatalf("%s lacks %s", got, want)
		}
	}
	if strings.Contains(got, "secret") {
		t.Fatalf("private text leaked: %s", got)
	}
	if summarizeAPIArgs(nil) != "" || summarizeAPIArgs([]byte(`{}`)) != "" {
		t.Fatal("empty arguments")
	}
	long := summarizeAPIArgs([]byte(`{"command":"` + strings.Repeat("a", 1000) + `"}`))
	if len([]rune(long)) > apiCallArgsMax+1 {
		t.Fatalf("not shortened: %d", len(long))
	}
}

// The RPC log line uses the summary: a NetBird setup key or a URL with
// credentials never reaches it, and the handler's params stay as they were.
func TestSummarizeAPIArgsHidesSecrets(t *testing.T) {
	params := map[string]any{"managementURL": "https://nb.example", "setupKey": "ABCD-1234", "url": "http://user:pw@h/x.iso"}
	got := summarizeAPIArgMap(params)
	if strings.Contains(got, "ABCD-1234") || strings.Contains(got, "user:pw") || !strings.Contains(got, "nb.example") {
		t.Fatalf("summary %s", got)
	}
	if params["setupKey"] != "ABCD-1234" {
		t.Fatal("the summary changed the params")
	}
	nested := map[string]any{"settings": map[string]any{"host": "mq.example", "password": "hunter22"},
		"list": []any{map[string]any{"token": "t0ken99"}}}
	got = summarizeAPIArgMap(nested)
	if strings.Contains(got, "hunter22") || strings.Contains(got, "t0ken99") || !strings.Contains(got, "mq.example") {
		t.Fatalf("nested summary %s", got)
	}
	if nested["settings"].(map[string]any)["password"] != "hunter22" {
		t.Fatal("the summary changed the nested params")
	}
}

func TestAPICallLogRing(t *testing.T) {
	for i := 0; i < apiCallLogSize+5; i++ {
		recordAPICall(APICallEntry{Source: apiCallSourceMCP, Name: "kvm_status", OK: i%2 == 0})
	}
	log := rpcGetApiCallLog()
	if len(log.Entries) != apiCallLogSize {
		t.Fatalf("%d entries", len(log.Entries))
	}
	if log.Entries[len(log.Entries)-1].ID <= log.Entries[0].ID {
		t.Fatal("not oldest first")
	}
	if log.Totals[apiCallSourceMCP] < apiCallLogSize+5 || log.Counts[0].Errors == 0 {
		t.Fatalf("counts %+v totals %+v", log.Counts[0], log.Totals)
	}
}
