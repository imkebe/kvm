package kvm

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// The Target card stays within the screen's limits whatever the target
// reports: 6 pages, 512 bytes of text a page, and no character that would
// end a page or a card or start a chart.
func TestHomeTargetPagesLimits(t *testing.T) {
	long := strings.Repeat("~w\f\v\nĄ", 40)
	inventory := map[string]any{
		"os":     map[string]any{"name": long, "version": long, "kernel": long, "arch": long, "hostname": long},
		"cpu":    map[string]any{"brand": long, "physical_cores": 64, "logical_cores": 128},
		"memory": map[string]any{"total": 1 << 40},
	}
	metrics := map[string]any{
		"uptime_s": 1e9, "cpu": map[string]any{"total": 250.0},
		"memory": map[string]any{"used": 3 << 30, "total": 4 << 30}, "process_count": 99999,
	}
	var gpus, disks, networks, rates, temps, top, gpuLoads []any
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("~%d%s", i, long)
		gpus = append(gpus, map[string]any{"name": name, "vram": 8 << 30})
		gpuLoads = append(gpuLoads, map[string]any{"name": name, "load": 99.5, "vram_used": 7 << 30})
		disks = append(disks, map[string]any{"mount": name, "total": 5e14, "available": 1e14})
		networks = append(networks, map[string]any{"name": name, "addresses": []string{long}, "up": i%2 == 0})
		rates = append(rates, map[string]any{"name": name, "rx_bps": 9e11, "tx_bps": 9e11})
		temps = append(temps, map[string]any{"label": name, "c": 99.9})
		top = append(top, map[string]any{"pid": i, "name": name, "cpu": 6400.0, "cpu_time_ms": 9e12, "mem": 9e13})
	}
	inventory["gpus"], inventory["disks"], inventory["networks"] = gpus, disks, networks
	metrics["gpus"], metrics["disks"], metrics["networks"], metrics["temps"] = gpuLoads, disks, rates, temps
	// The answers to the query for the busiest processes: averages over a
	// window, and the first answer of a session, which has none.
	averages := map[string]any{"schema": 1, "window_s": 10.04, "process_count": 99999, "top": top}
	first := map[string]any{"schema": 1, "window_s": 0, "process_count": 99999, "top": top}
	var inv hostInventory
	var met hostMetrics
	var avg, fst hostTop
	for from, to := range map[any]any{&inventory: &inv, &metrics: &met, &averages: &avg, &first: &fst} {
		data, _ := json.Marshal(from)
		if err := json.Unmarshal(data, to); err != nil {
			t.Fatal(err)
		}
	}
	var history homeHistory
	for i := 0; i < 100; i++ {
		history.push(100)
	}
	check := func(pages []string, want int) {
		t.Helper()
		if len(pages) != want || len(pages) > homeMaxPages {
			t.Fatalf("%d pages, want %d", len(pages), want)
		}
		for _, page := range pages {
			lines := strings.Split(page, "\n")
			body, charts := 0, 0
			for i, line := range lines {
				if strings.ContainsAny(line, "\f\v") || len(line) == 0 {
					t.Fatalf("line %q of page %q", line, lines[0])
				}
				if line[0] == '~' {
					charts++
					if i == 0 || strings.Count(line, ",") > 2*(homeHistoryLen-1) {
						t.Fatalf("chart %q of page %q", line, lines[0])
					}
					continue
				}
				if i > 0 {
					body += len(line) + 1
				}
				if len(line) > homeLineChars || strings.TrimFunc(line, func(r rune) bool { return r >= 0x20 && r <= 0x7e }) != "" {
					t.Fatalf("line %q of page %q", line, lines[0])
				}
			}
			if body >= 500 || len(lines)-1-charts > homePageLines || charts > 1 {
				t.Fatalf("page %q: %d bytes, %d lines, %d charts", lines[0], body, len(lines)-1, charts)
			}
		}
	}
	pages := homeTargetPages(&inv, &met, history, history, &avg)
	check(pages, 6)
	if !strings.HasPrefix(pages[0], "Target\n") || !strings.Contains(pages[0], "CPU 250%  Memory 75%") {
		t.Fatalf("summary %q", pages[0])
	}
	// The processes page says what its averages cover.
	if !strings.HasPrefix(pages[5], "Target processes\n99999 procs, 10 s avg\n") || !strings.Contains(pages[5], " 6400% ") {
		t.Fatalf("processes %q", pages[5])
	}
	// The first answer has no averages: no percent figure at all.
	pages = homeTargetPages(&inv, &met, history, history, &fst)
	check(pages, 6)
	if !strings.HasPrefix(pages[5], "Target processes\n99999 procs, CPU time\n") || strings.Contains(pages[5], "%") ||
		!strings.Contains(pages[5], " 9999d ") {
		t.Fatalf("first processes %q", pages[5])
	}
	// The metrics carry no processes any more: without an answer, no page.
	check(homeTargetPages(&inv, &met, history, history, nil), 5)
	check(homeTargetPages(&inv, &met, history, history, &hostTop{WindowS: 10, ProcessCount: 5}), 5)
	// Without metrics: no rates or heat, and the summary says so.
	pages = homeTargetPages(&inv, nil, nil, nil, nil)
	check(pages, 5)
	if !strings.Contains(pages[0], "waiting for readings") {
		t.Fatalf("summary %q", pages[0])
	}
	// An empty inventory leaves the summary alone.
	pages = homeTargetPages(&hostInventory{}, nil, nil, nil, nil)
	check(pages, 1)
}

// The lines of the processes page for a real answer.
func TestHomeTargetProcessesPage(t *testing.T) {
	page := func(document string) string {
		t.Helper()
		top := &hostTop{}
		if err := json.Unmarshal([]byte(document), top); err != nil {
			t.Fatal(err)
		}
		pages := homeTargetPages(&hostInventory{}, nil, nil, nil, top)
		if len(pages) != 2 {
			t.Fatalf("%d pages for %s", len(pages), document)
		}
		return pages[1]
	}
	got := page(`{"schema":1,"t":1795000000000,"window_s":10.0,"process_count":950,"top":[
		{"pid":1,"ppid":0,"name":"firefox","user":"u","cpu":142.4,"cpu_time_ms":7200000,"mem":1288490188,"start":1},
		{"pid":2,"name":"idle","cpu":0.0,"cpu_time_ms":0,"mem":1048576}]}`)
	if want := "Target processes\n950 procs, 10 s avg\nfirefox 142% 1.2 GB\nidle 0% 1 MB"; got != want {
		t.Fatalf("page %q, want %q", got, want)
	}
	// Without a window: the processor time, or the memory alone.
	got = page(`{"schema":1,"window_s":0,"process_count":950,"top":[
		{"name":"firefox","cpu":0.0,"cpu_time_ms":7200000,"mem":1288490188},
		{"name":"sshd","cpu":0.0,"cpu_time_ms":95000,"mem":1048576},
		{"name":"system","cpu":0.0,"cpu_time_ms":0,"mem":2097152}]}`)
	if want := "Target processes\n950 procs, CPU time\nfirefox 2.0h 1.2 GB\nsshd 2m 1 MB\nsystem 2 MB"; got != want {
		t.Fatalf("page %q, want %q", got, want)
	}
	// A long window, after the screen was dark, and no count.
	got = page(`{"window_s":1800,"top":[{"name":"a","cpu":1.4,"mem":0}]}`)
	if want := "Target processes\n30 min avg\na 1% 0 kB"; got != want {
		t.Fatalf("page %q, want %q", got, want)
	}
}

// The screen forgets the busiest processes after 30 seconds, and when they
// are dropped.
func TestHomeTargetTopAge(t *testing.T) {
	h := &hostInfoGlue{}
	h.init()
	now := time.Now()
	if h.screenTop(now) != nil {
		t.Fatalf("processes before an answer")
	}
	top := &hostTop{WindowS: 10}
	h.setTop(top, now)
	if h.screenTop(now.Add(hostInfoTopMaxAge-time.Second)) != top {
		t.Fatalf("a recent answer is not shown")
	}
	if h.screenTop(now.Add(hostInfoTopMaxAge)) != nil {
		t.Fatalf("an old answer is shown")
	}
	h.setTop(nil, time.Time{})
	if h.screenTop(now) != nil {
		t.Fatalf("a dropped answer is shown")
	}
}

// Queries go to the target one at a time and in order; calls that come
// while a query for the same section waits share it; the wait is bounded.
func TestOSServicesHostInfoQueue(t *testing.T) {
	h := &hostInfoGlue{}
	h.init()
	var mu sync.Mutex
	running, most := 0, 0
	var order []string
	gate := make(chan struct{})
	run := func(name string) func() (json.RawMessage, error) {
		return func() (json.RawMessage, error) {
			mu.Lock()
			running++
			most = max(most, running)
			order = append(order, name)
			mu.Unlock()
			<-gate
			mu.Lock()
			running--
			mu.Unlock()
			return json.RawMessage(`"` + name + `"`), nil
		}
	}
	started := func(count int) {
		t.Helper()
		for i := 0; i < 500; i++ {
			mu.Lock()
			n := len(order)
			mu.Unlock()
			if n == count {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		t.Fatalf("%d queries did not start: %v", count, order)
	}
	waiting := func(section uint8) {
		t.Helper()
		for i := 0; i < 500; i++ {
			h.queueMu.Lock()
			_, ok := h.waiting[section]
			h.queueMu.Unlock()
			if ok {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		t.Fatalf("no query waits for section %d", section)
	}
	results := make(chan string, 8)
	call := func(section uint8, name string, wait time.Duration) {
		go func() {
			result, err := h.queued(section, wait, run(name))
			if err != nil {
				results <- name + ": " + err.Error()
				return
			}
			results <- name + " got " + string(result)
		}()
	}
	// The screen's query is in flight.
	call(4, "top", time.Minute)
	started(1)
	// Two calls of the browser for the processes and one for the USB
	// devices wait; the second processes call shares the first one's query.
	call(2, "p1", time.Minute)
	waiting(2)
	call(2, "p2", time.Minute)
	call(3, "usb", time.Minute)
	waiting(3)
	// A query that cannot wait long enough fails, and nothing is sent.
	if _, err := h.queued(1, 20*time.Millisecond, run("inventory")); err == nil || err.Error() != hostInfoNoAnswer {
		t.Fatalf("a query beyond its wait: %v", err)
	}
	h.queueMu.Lock()
	if _, ok := h.waiting[1]; ok {
		t.Fatalf("the failed query still waits")
	}
	h.queueMu.Unlock()
	close(gate)
	got := map[string]bool{}
	for i := 0; i < 4; i++ {
		select {
		case result := <-results:
			got[result] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("calls did not return: %v", got)
		}
	}
	mu.Lock()
	if most != 1 || len(order) != 3 || order[0] != "top" {
		t.Fatalf("most %d at once, order %v", most, order)
	}
	mu.Unlock()
	if !got[`top got "top"`] || !got[`p1 got "p1"`] || !got[`p2 got "p1"`] || !got[`usb got "usb"`] {
		t.Fatalf("results %v", got)
	}
	// After that a call is a new query.
	if result, err := h.queued(2, time.Minute, run("p3")); err != nil || string(result) != `"p3"` {
		t.Fatalf("a later call: %s, %v", result, err)
	}
}
