package osservices

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Host info service kinds from the contract.
const (
	kHostCaps      = 1
	kHostSubscribe = 2
	kHostInventory = 3
	kHostMetrics   = 4
	kHostQuery     = 5
	kHostResult    = 6
)

const (
	testInventory = `{"schema":1,"os":{"family":"linux","name":"Ubuntu","version":"24.04"}}`
	testMetrics   = `{"schema":1,"t":1795000000000,"cpu":{"total":12.5}}`
)

// openSessionMinor1 opens a session with a 5.1 sender: host info needs it.
func (h *harness) openSessionMinor1() {
	h.t.Helper()
	token := h.resetAndAdmit()
	h.send(svcSession, kHello, makeHello(5, 1, token, 3))
	h.expect(svcSession, kReady)
	settings := h.s.Settings()
	h.checkConfig(h.expect(svcSession, kConfig), settings.TerminalEnabled, settings.FilesEnabled, settings.CaptureDisplay)
	h.expectNone()
}

func hostSettings() Settings {
	s := defaultSettings()
	s.HostInfoEnabled = true
	return s
}

// openHostInfo is an open session whose sender offers every section.
func openHostInfo(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, hooksServices, hostSettings())
	h.openSessionMinor1()
	h.send(svcHostInfo, kHostCaps, le32(15))
	h.expectNone()
	if !h.s.HostInfoAvailable() {
		t.Fatalf("host info not available after the capabilities")
	}
	return h
}

func (h *harness) expectSubscribe(interval uint32) {
	h.t.Helper()
	m := h.expect(svcHostInfo, kHostSubscribe)
	if want := cat(le32(interval), le32(0)); !bytes.Equal(m.payload, want) {
		h.t.Fatalf("hostinfo.subscribe % x, want % x", m.payload, want)
	}
}

func (h *harness) expectQuery(id uint32, section byte) {
	h.t.Helper()
	m := h.expect(svcHostInfo, kHostQuery)
	if want := cat(le32(id), []byte{section, 0, 0, 0}); !bytes.Equal(m.payload, want) {
		h.t.Fatalf("hostinfo.query % x, want % x", m.payload, want)
	}
}

func hostResult(id uint32, section, flags byte, chunk string) []byte {
	return cat(le32(id), []byte{section, flags, 0, 0}, []byte(chunk))
}

func (h *harness) lastHostDone(count int) hostDoneEvent {
	h.t.Helper()
	if len(h.f.hostDone) != count {
		h.t.Fatalf("%d HostInfoDone calls, want %d: %+v", len(h.f.hostDone), count, h.f.hostDone)
	}
	return h.f.hostDone[count-1]
}

func TestHostInfoMinorVersion(t *testing.T) {
	// A 5.0 sender gets 5.0, a 5.1 sender 5.1.
	for minor := uint16(0); minor <= 1; minor++ {
		h := newHarness(t, hooksServices, hostSettings())
		token := h.resetAndAdmit()
		h.send(svcSession, kHello, makeHello(5, minor, token, 1))
		m := h.expect(svcSession, kReady)
		if got := wle.Uint16(m.payload[2:]); got != minor || !h.s.Open() {
			t.Fatalf("hello minor %d: session.ready minor %d, open %v", minor, got, h.s.Open())
		}
	}
}

func TestHostInfoCapabilities(t *testing.T) {
	h := newHarness(t, hooksServices, hostSettings())
	h.openSessionMinor1()
	if st := h.s.State(); st.HostInfo || st.HostInfoSections != 0 {
		t.Fatalf("host info before the capabilities: %+v", st)
	}
	changes := h.f.stateChanges
	h.send(svcHostInfo, kHostCaps, le32(HostInfoSectionInventory|HostInfoSectionMetrics))
	st := h.s.State()
	if !st.HostInfo || st.HostInfoSections != 3 || h.f.stateChanges != changes+1 {
		t.Fatalf("state %+v, %d state changes", st, h.f.stateChanges-changes)
	}
	// The same capabilities again change nothing.
	h.send(svcHostInfo, kHostCaps, le32(3))
	if h.f.stateChanges != changes+1 {
		t.Fatalf("repeated capabilities changed the state")
	}
	// Invalid capabilities are dropped: the session and the registration
	// stay.
	h.send(svcHostInfo, kHostCaps, le32(16))
	h.send(svcHostInfo, kHostCaps, []byte{3, 0})
	h.send(svcHostInfo, kHostCaps, le32(3), le32(0))
	if st := h.s.State(); !st.HostInfo || st.HostInfoSections != 3 || !h.s.Open() || h.s.Counters.HostInfoDropped != 3 {
		t.Fatalf("after invalid capabilities: %+v, dropped %d", st, h.s.Counters.HostInfoDropped)
	}
	// A query for a section that is not offered does not start.
	if id, reason := h.s.HostInfoQuery(HostInfoQueryProcesses); id != 0 || reason == "" {
		t.Fatalf("query for a section not offered: id %d", id)
	}
	if id, reason := h.s.HostInfoQuery(HostInfoQueryTop); id != 0 || reason != hostInfoUnavailable {
		t.Fatalf("top query without the processes section: id %d, %q", id, reason)
	}
	if id, reason := h.s.HostInfoQuery(5); id != 0 || reason != hostInfoUnavailable {
		t.Fatalf("query for an unknown section: id %d, %q", id, reason)
	}
	h.expectNone()
	// The KVM's own kinds from the sender are skipped.
	h.send(svcHostInfo, kHostSubscribe, le32(1000), le32(1))
	h.send(svcHostInfo, kHostQuery, le32(1), le32(1))
	if !h.s.Open() || !h.s.HostInfoAvailable() {
		t.Fatalf("a KVM-to-sender kind ended host info")
	}
	// A platform without the hooks still shows the reports.
	h = newHarness(t, hooksPlain, hostSettings())
	h.openSessionMinor1()
	h.send(svcHostInfo, kHostCaps, le32(15))
	h.send(svcHostInfo, kHostMetrics, []byte(testMetrics))
	if st := h.s.State(); !st.HostInfo || string(st.HostInfoMetrics) != testMetrics {
		t.Fatalf("without hooks: %+v", st)
	}
	if id, _ := h.s.HostInfoQuery(HostInfoQueryUSB); id != 0 {
		t.Fatalf("a query started without the hooks")
	}
}

func TestHostInfoInventoryAndMetrics(t *testing.T) {
	h := openHostInfo(t)
	changes := h.f.stateChanges
	h.send(svcHostInfo, kHostInventory, []byte(testInventory))
	st := h.s.State()
	if string(st.HostInfoInventory) != testInventory || st.HostInfoMetrics != nil || h.f.stateChanges != changes+1 {
		t.Fatalf("inventory %q, %d state changes", st.HostInfoInventory, h.f.stateChanges-changes)
	}
	// The same inventory again is no change; another one is.
	h.send(svcHostInfo, kHostInventory, []byte(testInventory))
	if h.f.stateChanges != changes+1 {
		t.Fatalf("an identical inventory changed the state")
	}
	h.send(svcHostInfo, kHostInventory, []byte(`{"schema":1}`))
	if string(h.s.State().HostInfoInventory) != `{"schema":1}` || h.f.stateChanges != changes+2 {
		t.Fatalf("the new inventory was not taken")
	}
	// Metrics go to the platform and do not count as a state change.
	h.send(svcHostInfo, kHostMetrics, []byte(testMetrics))
	if len(h.f.hostMetrics) != 1 || h.f.hostMetrics[0] != testMetrics ||
		string(h.s.State().HostInfoMetrics) != testMetrics || h.f.stateChanges != changes+2 {
		t.Fatalf("metrics %q, hook %q", h.s.State().HostInfoMetrics, h.f.hostMetrics)
	}
	// What is not a JSON object is dropped, and the last report stays.
	for _, bad := range []string{"", "[1]", `"text"`, `{"a":`, "{\"a\":\"\xff\"}", `{} {}`} {
		h.send(svcHostInfo, kHostInventory, []byte(bad))
		h.send(svcHostInfo, kHostMetrics, []byte(bad))
	}
	st = h.s.State()
	if string(st.HostInfoInventory) != `{"schema":1}` || string(st.HostInfoMetrics) != testMetrics ||
		len(h.f.hostMetrics) != 1 || !h.s.Open() || h.s.Counters.HostInfoDropped != 12 {
		t.Fatalf("after invalid reports: inventory %q metrics %q dropped %d", st.HostInfoInventory,
			st.HostInfoMetrics, h.s.Counters.HostInfoDropped)
	}
	// A report of 64 KiB, the limit, is taken.
	large := `{"pad":"` + strings.Repeat("x", HostInfoMaxJSON-10) + `"}`
	if len(large) != HostInfoMaxJSON {
		t.Fatalf("test report is %d bytes", len(large))
	}
	h.send(svcHostInfo, kHostMetrics, []byte(large))
	if string(h.s.State().HostInfoMetrics) != large {
		t.Fatalf("a 64 KiB report was not taken")
	}
	// A larger one is above the limit of every control payload: the session
	// ends, as for any other service.
	h.feed(wireMsg(svcHostInfo, kHostMetrics, []byte(large+" "))[:8])
	if h.s.Open() {
		t.Fatalf("a payload above 64 KiB did not end the session")
	}
	// The session's end forgets everything.
	if st := h.s.State(); st.HostInfo || st.HostInfoInventory != nil || st.HostInfoMetrics != nil {
		t.Fatalf("host info after the session: %+v", st)
	}
	// Reports before the capabilities are ignored.
	h.openSessionMinor1()
	h.send(svcHostInfo, kHostInventory, []byte(testInventory))
	h.send(svcHostInfo, kHostMetrics, []byte(testMetrics))
	if st := h.s.State(); st.HostInfo || len(h.f.hostMetrics) != 2 {
		t.Fatalf("reports before the capabilities: %+v", st)
	}
}

func TestHostInfoSubscription(t *testing.T) {
	h := newHarness(t, hooksServices, hostSettings())
	h.openSessionMinor1()
	// Nothing to subscribe to yet.
	h.s.HostInfoWatch(true)
	h.expectNone()
	h.send(svcHostInfo, kHostCaps, le32(15))
	h.s.HostInfoWatch(false)
	h.expectNone()
	h.s.HostInfoWatch(true)
	h.expectSubscribe(1000)
	// Renewed every 5 seconds.
	h.f.now += 4999
	h.s.HostInfoWatch(true)
	h.expectNone()
	h.f.now++
	h.s.HostInfoWatch(true)
	h.expectSubscribe(1000)
	// Nobody watches: one subscribe with interval 0.
	h.s.HostInfoWatch(false)
	h.expectSubscribe(0)
	h.s.HostInfoWatch(false)
	h.expectNone()
	// A new registration needs a new subscription.
	h.s.HostInfoWatch(true)
	h.expectSubscribe(1000)
	h.send(svcHostInfo, kHostCaps, le32(0))
	h.send(svcHostInfo, kHostCaps, le32(15))
	h.s.HostInfoWatch(true)
	h.expectSubscribe(1000)
	// A sender without metrics gets no subscribe.
	h.send(svcHostInfo, kHostCaps, le32(HostInfoSectionInventory))
	h.f.now += 6000
	h.ping()
	h.s.HostInfoWatch(true)
	h.s.HostInfoWatch(false)
	h.expectNone()
}

func TestHostInfoChunkedResult(t *testing.T) {
	h := openHostInfo(t)
	id, reason := h.s.HostInfoQuery(HostInfoQueryProcesses)
	if id != 1 || reason != "" {
		t.Fatalf("query: id %d, %q", id, reason)
	}
	h.expectQuery(1, 2)
	// One query in flight.
	if second, reason := h.s.HostInfoQuery(HostInfoQueryUSB); second != 0 || reason != hostInfoBusy {
		t.Fatalf("a second query started: id %d, %q", second, reason)
	}
	h.expectNone()
	// 150 KiB in three chunks, with a chunk of another request between.
	processes := `{"schema":1,"processes":[` + strings.Repeat(`{"pid":1,"name":"a"},`, 7500) + `{"pid":2}]}`
	if !json.Valid([]byte(processes)) || len(processes) < 150000 {
		t.Fatalf("test answer: %d bytes", len(processes))
	}
	const chunk = 60 * 1024
	h.send(svcHostInfo, kHostResult, hostResult(1, 2, 0, processes[:chunk]))
	h.send(svcHostInfo, kHostResult, hostResult(9, 2, 1, `{}`))
	h.send(svcHostInfo, kHostResult, hostResult(1, 2, 0, processes[chunk:2*chunk]))
	if len(h.f.hostDone) != 0 {
		t.Fatalf("the query ended before its last chunk")
	}
	h.send(svcHostInfo, kHostResult, hostResult(1, 2, 1, processes[2*chunk:]))
	if done := h.lastHostDone(1); done.requestID != 1 || done.result != processes || done.failure != "" {
		t.Fatalf("answer of %d bytes, failure %q", len(done.result), done.failure)
	}
	// A late chunk is ignored.
	h.send(svcHostInfo, kHostResult, hostResult(1, 2, 1, `{}`))
	h.lastHostDone(1)

	// The next query has the next ID; an error answer carries a message.
	if id, _ := h.s.HostInfoQuery(HostInfoQueryUSB); id != 2 {
		t.Fatalf("second query has id %d", id)
	}
	h.expectQuery(2, 3)
	h.send(svcHostInfo, kHostResult, hostResult(2, 3, 3, "no USB access"))
	if done := h.lastHostDone(2); done.requestID != 2 || done.result != "" || done.failure != "no USB access" {
		t.Fatalf("error answer: %+v", done)
	}
	// An answer that is not a JSON object fails the query.
	h.s.HostInfoQuery(HostInfoQueryInventory)
	h.expectQuery(3, 1)
	h.send(svcHostInfo, kHostResult, hostResult(3, 1, 1, `[1,2]`))
	if done := h.lastHostDone(3); done.failure != hostInfoNotJSON {
		t.Fatalf("answer without an object: %+v", done)
	}
	// An answer for another section fails it too.
	h.s.HostInfoQuery(HostInfoQueryInventory)
	h.expectQuery(4, 1)
	h.send(svcHostInfo, kHostResult, hostResult(4, 2, 1, `{}`))
	if done := h.lastHostDone(4); done.requestID != 4 || done.failure == "" {
		t.Fatalf("answer for another section: %+v", done)
	}
	// Invalid chunk headers are dropped; the query waits on.
	h.s.HostInfoQuery(HostInfoQueryInventory)
	h.expectQuery(5, 1)
	dropped := h.s.Counters.HostInfoDropped
	h.send(svcHostInfo, kHostResult, le32(5))
	h.send(svcHostInfo, kHostResult, hostResult(5, 1, 4, `{}`))
	h.send(svcHostInfo, kHostResult, cat(le32(5), []byte{1, 1, 1, 0}, []byte(`{}`)))
	h.send(svcHostInfo, kHostResult, hostResult(5, 1, 1, strings.Repeat(" ", chunk+1)))
	if h.s.Counters.HostInfoDropped != dropped+4 || len(h.f.hostDone) != 4 || !h.s.Open() {
		t.Fatalf("invalid chunks: dropped %d, done %d", h.s.Counters.HostInfoDropped-dropped, len(h.f.hostDone))
	}
	// No answer in 5 seconds: the query times out.
	h.f.now += HostInfoQueryTimeoutMS - 1
	h.ping()
	h.tick()
	h.lastHostDone(4)
	h.f.now++
	h.tick()
	if done := h.lastHostDone(5); done.requestID != 5 || done.failure != hostInfoNoAnswer {
		t.Fatalf("timeout: %+v", done)
	}
	// A cancelled query ends without a call, and the next one can start.
	id, _ = h.s.HostInfoQuery(HostInfoQueryInventory)
	h.s.HostInfoQueryCancel(id)
	h.send(svcHostInfo, kHostResult, hostResult(id, 1, 1, `{}`))
	h.lastHostDone(5)
	if next, _ := h.s.HostInfoQuery(HostInfoQueryInventory); next != id+1 {
		t.Fatalf("query after a cancelled one: id %d", next)
	}
}

func TestHostInfoOversizeResult(t *testing.T) {
	h := openHostInfo(t)
	h.s.HostInfoQuery(HostInfoQueryProcesses)
	h.expectQuery(1, 2)
	// 17 chunks of 60 KiB are 1020 KiB; the 18th passes 1 MiB.
	chunk := strings.Repeat(" ", 60*1024)
	for i := 0; i < 17; i++ {
		h.send(svcHostInfo, kHostResult, hostResult(1, 2, 0, chunk))
	}
	if len(h.f.hostDone) != 0 {
		t.Fatalf("the query ended below 1 MiB")
	}
	h.send(svcHostInfo, kHostResult, hostResult(1, 2, 0, chunk))
	if done := h.lastHostDone(1); done.result != "" || done.failure != hostInfoTooLarge {
		t.Fatalf("oversize answer: %+v", done)
	}
	// The rest of the answer is ignored, and the session stays.
	h.send(svcHostInfo, kHostResult, hostResult(1, 2, 1, `{}`))
	h.lastHostDone(1)
	if !h.s.Open() || !h.s.HostInfoAvailable() {
		t.Fatalf("an oversize answer ended host info")
	}
	// Exactly 1 MiB is taken.
	h.s.HostInfoQuery(HostInfoQueryProcesses)
	h.expectQuery(2, 2)
	exact := `{"pad":"` + strings.Repeat("x", HostInfoMaxResult-10) + `"}`
	for off := 0; off < len(exact); off += 60 * 1024 {
		end := min(off+60*1024, len(exact))
		flags := byte(0)
		if end == len(exact) {
			flags = 1
		}
		h.send(svcHostInfo, kHostResult, hostResult(2, 2, flags, exact[off:end]))
	}
	if done := h.lastHostDone(2); len(done.result) != HostInfoMaxResult || done.failure != "" {
		t.Fatalf("1 MiB answer: %d bytes, %q", len(done.result), done.failure)
	}
}

func TestHostInfoWithdrawnAndSessionEnd(t *testing.T) {
	h := openHostInfo(t)
	h.send(svcHostInfo, kHostInventory, []byte(testInventory))
	h.send(svcHostInfo, kHostMetrics, []byte(testMetrics))
	h.s.HostInfoWatch(true)
	h.expectSubscribe(1000)
	h.s.HostInfoQuery(HostInfoQueryUSB)
	h.expectQuery(1, 3)
	changes := h.f.stateChanges
	// All-zero capabilities withdraw the service and fail the query.
	h.send(svcHostInfo, kHostCaps, le32(0))
	st := h.s.State()
	if st.HostInfo || st.HostInfoSections != 0 || st.HostInfoInventory != nil || st.HostInfoMetrics != nil ||
		h.f.stateChanges != changes+1 || !h.s.Open() {
		t.Fatalf("after the withdrawal: %+v", st)
	}
	if done := h.lastHostDone(1); done.requestID != 1 || done.failure != hostInfoWithdrawn {
		t.Fatalf("query after the withdrawal: %+v", done)
	}
	h.s.HostInfoWatch(false)
	h.expectNone()
	// Capabilities without a section drop what belongs to it.
	h.send(svcHostInfo, kHostCaps, le32(15))
	h.send(svcHostInfo, kHostInventory, []byte(testInventory))
	h.send(svcHostInfo, kHostMetrics, []byte(testMetrics))
	h.s.HostInfoQuery(HostInfoQueryUSB)
	h.expectQuery(2, 3)
	h.send(svcHostInfo, kHostCaps, le32(HostInfoSectionInventory))
	st = h.s.State()
	if !st.HostInfo || string(st.HostInfoInventory) != testInventory || st.HostInfoMetrics != nil {
		t.Fatalf("after narrower capabilities: %+v", st)
	}
	h.lastHostDone(2)
	// The session's end fails the query and forgets the registration; the
	// next session starts without host info, and the IDs go on.
	h.send(svcHostInfo, kHostCaps, le32(15))
	h.s.HostInfoQuery(HostInfoQueryProcesses)
	h.expectQuery(3, 2)
	h.s.End("test")
	if done := h.lastHostDone(3); done.failure != hostInfoSessionEnded {
		t.Fatalf("query after the session: %+v", done)
	}
	h.openSessionMinor1()
	if st := h.s.State(); st.HostInfo || st.HostInfoInventory != nil {
		t.Fatalf("host info in a new session: %+v", st)
	}
	h.send(svcHostInfo, kHostCaps, le32(15))
	if id, _ := h.s.HostInfoQuery(HostInfoQueryProcesses); id != 4 {
		t.Fatalf("query in the next session has id %d", id)
	}
}

func TestHostInfoConfigFlag(t *testing.T) {
	// session.config carries the setting (checkConfig reads it).
	off := newHarness(t, hooksServices, defaultSettings())
	off.openSessionMinor1()
	// While it is off, capabilities register nothing.
	off.send(svcHostInfo, kHostCaps, le32(15))
	off.send(svcHostInfo, kHostInventory, []byte(testInventory))
	if st := off.s.State(); st.HostInfo || st.HostInfoInventory != nil {
		t.Fatalf("host info while it is off: %+v", st)
	}
	// Turned on: a new session.config, and the sender registers.
	on := hostSettings()
	off.s.ApplySettings(on)
	m := off.expect(svcSession, kConfig)
	off.checkConfig(m, on.TerminalEnabled, on.FilesEnabled, "")
	if !bytes.Contains(m.payload, []byte(`"hostinfo_enabled":true`)) {
		t.Fatalf("session.config %s", m.payload)
	}
	off.send(svcHostInfo, kHostCaps, le32(15))
	off.send(svcHostInfo, kHostInventory, []byte(testInventory))
	off.s.HostInfoQuery(HostInfoQueryUSB)
	off.expectQuery(1, 3)
	if !off.s.State().HostInfo {
		t.Fatalf("host info did not register after it was turned on")
	}
	// Turned off: the config, the query fails and the reports are gone at
	// once, before the sender withdraws.
	off.s.ApplySettings(defaultSettings())
	m = off.expect(svcSession, kConfig)
	if !bytes.Contains(m.payload, []byte(`"hostinfo_enabled":false`)) {
		t.Fatalf("session.config %s", m.payload)
	}
	if done := off.lastHostDone(1); done.failure != hostInfoDisabled {
		t.Fatalf("query after turning host info off: %+v", done)
	}
	if st := off.s.State(); st.HostInfo || st.HostInfoInventory != nil {
		t.Fatalf("host info after it was turned off: %+v", st)
	}
	off.s.HostInfoWatch(true)
	off.send(svcHostInfo, kHostMetrics, []byte(testMetrics))
	off.send(svcHostInfo, kHostCaps, le32(0))
	off.expectNone()
	if len(off.f.hostMetrics) != 0 {
		t.Fatalf("metrics reached the platform while host info is off")
	}
}

// The busiest processes are section 4, offered with the processes section.
func TestHostInfoTopQuery(t *testing.T) {
	h := newHarness(t, hooksServices, hostSettings())
	h.openSessionMinor1()
	h.send(svcHostInfo, kHostCaps, le32(HostInfoSectionProcesses))
	id, reason := h.s.HostInfoQuery(HostInfoQueryTop)
	if id != 1 || reason != "" {
		t.Fatalf("top query: id %d, %q", id, reason)
	}
	h.expectQuery(1, 4)
	// An answer for the processes section is not the answer.
	h.send(svcHostInfo, kHostResult, hostResult(1, 2, 1, `{}`))
	if done := h.lastHostDone(1); done.result != "" || done.failure == "" {
		t.Fatalf("an answer for another section: %+v", done)
	}
	top := `{"schema":1,"t":1795000000000,"window_s":10.0,"process_count":950,"top":[{"pid":1,"name":"a","cpu":12.5}]}`
	if id, _ = h.s.HostInfoQuery(HostInfoQueryTop); id != 2 {
		t.Fatalf("second top query: id %d", id)
	}
	h.expectQuery(2, 4)
	h.send(svcHostInfo, kHostResult, hostResult(2, 4, 1, top))
	if done := h.lastHostDone(2); done.requestID != 2 || done.result != top || done.failure != "" {
		t.Fatalf("top answer: %+v", done)
	}
	// Without the processes section the query in flight ends.
	h.s.HostInfoQuery(HostInfoQueryTop)
	h.expectQuery(3, 4)
	h.send(svcHostInfo, kHostCaps, le32(HostInfoSectionInventory))
	if done := h.lastHostDone(3); done.failure != hostInfoWithdrawn {
		t.Fatalf("top query after the section went: %+v", done)
	}
}

func TestHostInfoNotForMinor0Sender(t *testing.T) {
	h := newHarness(t, hooksServices, hostSettings())
	h.openSession() // a 5.0 sender
	h.send(svcHostInfo, kHostCaps, le32(15))
	if h.s.HostInfoAvailable() || h.s.State().HostInfo {
		t.Fatalf("host info registered for a 5.0 sender")
	}
	if id, _ := h.s.HostInfoQuery(HostInfoQueryProcesses); id != 0 {
		t.Fatalf("query started for a 5.0 sender")
	}
	h.s.HostInfoWatch(true)
	h.expectNone()
}
