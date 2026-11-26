package kvm

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jetkvm/kvm/internal/osservices"
)

// Host info: the program on the target computer reports the target's system
// information over OS Services (service 7). The browser reads it with
// getHostInfo, getHostProcesses and getHostUsbDevices, and the device screen
// shows it on the "Target" card. Metrics come only while somebody watches:
// the browser (watchHostInfo renews a lease) or a lit screen that shows
// statistics.
//
// The target refreshes its process list only for a query: one refresh for
// each getHostProcesses of the browser, and one every 10 seconds for the
// screen's page of the busiest processes while a lit screen shows
// statistics. The queries go to the target one at a time, in a queue.

const (
	// watchHostInfo keeps the browser's lease for this long; the browser
	// calls it every 5 seconds.
	hostInfoBrowserLease = 12 * time.Second
	// A query waits this long for the queries before it, and then this long
	// for its answer: the session's own timeout and some slack. A caller
	// has its answer or an error after twice this at the latest.
	hostInfoQueryWait = osservices.HostInfoQueryTimeoutMS*time.Millisecond + 500*time.Millisecond
	// Metrics that come after a pause this long start the charts again.
	hostInfoHistoryGap = 5 * time.Second
	// The screen asks for the busiest processes this often, and does not
	// show an answer that is older than hostInfoTopMaxAge.
	hostInfoTopInterval = 10 * time.Second
	hostInfoTopMaxAge   = 30 * time.Second

	// A larger answer does not fit one message of the browser's RPC
	// channel (Chrome takes 256 KiB).
	hostInfoAnswerMax = 240 * 1024

	hostInfoUnavailable = "no system information from the target computer"
	hostInfoTooLarge    = "the answer is too large for the browser"
	hostInfoNoAnswer    = "the target computer did not answer in time"
)

type hostInfoResult struct {
	result  json.RawMessage
	failure string
}

// hostInventory and hostMetrics are the members of the reports that the
// device screen shows. Every number is a float64, so that a number in
// another notation does not spoil the whole report.
type hostInventory struct {
	OS struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Kernel   string `json:"kernel"`
		Arch     string `json:"arch"`
		Hostname string `json:"hostname"`
	} `json:"os"`
	CPU struct {
		Brand         string  `json:"brand"`
		PhysicalCores float64 `json:"physical_cores"`
		LogicalCores  float64 `json:"logical_cores"`
	} `json:"cpu"`
	Memory struct {
		Total float64 `json:"total"`
	} `json:"memory"`
	GPUs []struct {
		Name string  `json:"name"`
		VRAM float64 `json:"vram"`
	} `json:"gpus"`
	Disks    []hostDisk `json:"disks"`
	Networks []struct {
		Name      string   `json:"name"`
		Addresses []string `json:"addresses"`
		Up        bool     `json:"up"`
	} `json:"networks"`
}

type hostDisk struct {
	Mount     string  `json:"mount"`
	Total     float64 `json:"total"`
	Available float64 `json:"available"`
}

type hostMetrics struct {
	UptimeS float64 `json:"uptime_s"`
	CPU     struct {
		Total float64 `json:"total"`
	} `json:"cpu"`
	Memory struct {
		Used  float64 `json:"used"`
		Total float64 `json:"total"`
	} `json:"memory"`
	Networks []struct {
		Name  string  `json:"name"`
		RxBps float64 `json:"rx_bps"`
		TxBps float64 `json:"tx_bps"`
	} `json:"networks"`
	Disks []hostDisk `json:"disks"`
	Temps []struct {
		Label string  `json:"label"`
		C     float64 `json:"c"`
	} `json:"temps"`
	GPUs []struct {
		Name     string   `json:"name"`
		Load     *float64 `json:"load"`
		VRAMUsed *float64 `json:"vram_used"`
	} `json:"gpus"`
}

// hostTop is the answer to the query for the busiest processes. CPU is the
// average percent of one core over the WindowS seconds before the answer;
// without a window (0, the first answer of a session) every CPU is 0.
type hostTop struct {
	WindowS      float64 `json:"window_s"`
	ProcessCount float64 `json:"process_count"`
	Top          []struct {
		Name      string  `json:"name"`
		CPU       float64 `json:"cpu"`
		CPUTimeMS float64 `json:"cpu_time_ms"`
		Mem       float64 `json:"mem"`
	} `json:"top"`
}

func (m *hostMetrics) memoryPercent() float64 {
	if m.Memory.Total <= 0 {
		return 0
	}
	return 100 * m.Memory.Used / m.Memory.Total
}

// hostInfoCall is a query that waits for its turn; done closes when result
// and err are set.
type hostInfoCall struct {
	done   chan struct{}
	result json.RawMessage
	err    error
}

type hostInfoGlue struct {
	// browserUntil is the end of the browser's lease in Unix nanoseconds.
	browserUntil atomic.Int64
	// slot holds the one query in flight.
	slot chan struct{}
	// waiting has, for a section, the query that has not gone to the target
	// yet; queueMu guards it.
	queueMu sync.Mutex
	waiting map[uint8]*hostInfoCall
	// Guarded by link.Mu.
	waits map[uint32]chan hostInfoResult

	// The metrics that the session got last, for the notifier.
	pending atomic.Pointer[json.RawMessage]
	signal  chan struct{}

	// What the screen shows; mu guards the rest.
	mu            sync.Mutex
	metrics       *hostMetrics
	metricsAt     time.Time
	cpuHistory    homeHistory
	memoryHistory homeHistory
	inventoryRaw  json.RawMessage
	inventory     *hostInventory
	top           *hostTop
	topAt         time.Time
	// The state that hostInfoState told the browser last; only the state
	// notifier uses it.
	sentState hostInfoStateJSON
	sentOnce  bool

	// topStop ends hostInfoTopLoop; nil while it does not run. topMu
	// guards it.
	topMu   sync.Mutex
	topStop chan struct{}
}

func (h *hostInfoGlue) init() {
	h.slot = make(chan struct{}, 1)
	h.waits = map[uint32]chan hostInfoResult{}
	h.waiting = map[uint8]*hostInfoCall{}
	h.signal = make(chan struct{}, 1)
}

func (h *hostInfoGlue) browserWatches() bool {
	return time.Now().UnixNano() < h.browserUntil.Load()
}

// hostInfoWatched reports whether somebody looks at the target's metrics:
// the browser's panel, or a lit screen that shows statistics.
func (s *osServicesService) hostInfoWatched() bool {
	return s.hostInfo.browserWatches() || homeStatisticsWanted()
}

// --- Platform -------------------------------------------------------------------

// HostInfoMetrics runs with the session lock held: the notifier does the
// work.
func (s *osServicesService) HostInfoMetrics(metrics json.RawMessage) {
	s.hostInfo.pending.Store(&metrics)
	select {
	case s.hostInfo.signal <- struct{}{}:
	default:
	}
}

func (s *osServicesService) HostInfoDone(id uint32, result json.RawMessage, failure string) {
	wait, ok := s.hostInfo.waits[id]
	if !ok {
		return
	}
	delete(s.hostInfo.waits, id)
	wait <- hostInfoResult{result: result, failure: failure}
}

// hostInfoNotifier takes each hostinfo.metrics: the readings for the
// screen's charts while a lit screen shows statistics, and the hostMetrics
// event while the browser watches. The browser gets the JSON as it came, so
// it is parsed only for the screen.
func (s *osServicesService) hostInfoNotifier() {
	h := &s.hostInfo
	for range h.signal {
		raw := h.pending.Swap(nil)
		if raw == nil {
			continue
		}
		if homeStatisticsWanted() {
			h.screenMetrics(*raw, time.Now())
		}
		if session := currentSession; session != nil && h.browserWatches() {
			writeJSONRPCEvent("hostMetrics", *raw, session)
		}
	}
}

// screenMetrics takes metrics for the screen: the last readings and the
// charts.
func (h *hostInfoGlue) screenMetrics(raw json.RawMessage, now time.Time) {
	parsed := &hostMetrics{}
	// Members of another type leave the rest of what was read.
	_ = json.Unmarshal(raw, parsed)
	h.mu.Lock()
	defer h.mu.Unlock()
	if now.Sub(h.metricsAt) >= hostInfoHistoryGap {
		h.cpuHistory, h.memoryHistory = nil, nil
	}
	h.metrics, h.metricsAt = parsed, now
	h.cpuHistory.push(min(max(parsed.CPU.Total, 0), 100))
	h.memoryHistory.push(min(max(parsed.memoryPercent(), 0), 100))
}

// setTop keeps the busiest processes for the screen; nil forgets them.
func (h *hostInfoGlue) setTop(top *hostTop, now time.Time) {
	h.mu.Lock()
	h.top, h.topAt = top, now
	h.mu.Unlock()
}

// screenTop is the busiest processes for the screen: nil for none, or when
// the answer is older than hostInfoTopMaxAge. The caller holds mu.
func (h *hostInfoGlue) screenTop(now time.Time) *hostTop {
	if h.top == nil || now.Sub(h.topAt) >= hostInfoTopMaxAge {
		return nil
	}
	return h.top
}

// hostInfoTopLoop asks the target for its busiest processes every
// hostInfoTopInterval while a lit screen shows statistics, the first time
// at once. The browser's lease alone asks for nothing, and nothing is sent
// when the target offers no host info or no processes: then the screen's
// page goes.
// hostInfoTopRun starts or stops hostInfoTopLoop: it runs only while host
// info is on.
func (s *osServicesService) hostInfoTopRun(on bool) {
	h := &s.hostInfo
	h.topMu.Lock()
	defer h.topMu.Unlock()
	if on == (h.topStop != nil) {
		return
	}
	if on {
		h.topStop = make(chan struct{})
		go s.hostInfoTopLoop(h.topStop)
		return
	}
	close(h.topStop)
	h.topStop = nil
	h.setTop(nil, time.Time{})
}

func (s *osServicesService) hostInfoTopLoop(stop <-chan struct{}) {
	h := &s.hostInfo
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var asked time.Time
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		if !homeStatisticsWanted() || time.Since(asked) < hostInfoTopInterval {
			continue
		}
		offered := false
		s.do(func(session *osservices.Session) { offered = session.HostInfoOffers(osservices.HostInfoQueryTop) })
		if !offered {
			h.setTop(nil, time.Time{})
			continue
		}
		asked = time.Now()
		raw, err := s.hostInfoQuery(osservices.HostInfoQueryTop)
		if err != nil {
			// A late answer leaves the last one for the screen until it is
			// too old; any other failure is the end of the session or of
			// the service.
			if err.Error() != hostInfoNoAnswer {
				h.setTop(nil, time.Time{})
			}
			continue
		}
		top := &hostTop{}
		_ = json.Unmarshal(raw, top)
		h.setTop(top, time.Now())
	}
}

// hostInfoScreen is what the Target card shows: the inventory, the last
// metrics (nil for none, or when they are old), the two charts and the
// busiest processes (nil for none, or when they are old).
func (s *osServicesService) hostInfoScreen(raw json.RawMessage) (inventory *hostInventory, metrics *hostMetrics, cpu, memory homeHistory, top *hostTop) {
	h := &s.hostInfo
	h.mu.Lock()
	defer h.mu.Unlock()
	// The session replaces the inventory and never changes it in place, so
	// the same bytes are the same inventory.
	if h.inventory == nil || !sameBytes(h.inventoryRaw, raw) {
		h.inventory = &hostInventory{}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, h.inventory)
		}
		h.inventoryRaw = raw
	}
	if time.Since(h.metricsAt) < hostInfoHistoryGap {
		metrics = h.metrics
	}
	return h.inventory, metrics, append(homeHistory(nil), h.cpuHistory...), append(homeHistory(nil), h.memoryHistory...), h.screenTop(time.Now())
}

func sameBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return len(a) == 0 || &a[0] == &b[0]
}

// --- State for the browser ------------------------------------------------------

type hostInfoSectionsJSON struct {
	Inventory bool `json:"inventory"`
	Metrics   bool `json:"metrics"`
	Processes bool `json:"processes"`
	USB       bool `json:"usb"`
}

// hostInfoStateJSON is the hostInfoState event; inventory is null for none.
type hostInfoStateJSON struct {
	Available bool                 `json:"available"`
	Sections  hostInfoSectionsJSON `json:"sections"`
	Inventory json.RawMessage      `json:"inventory"`
}

// hostInfoJSON is the getHostInfo answer; metrics is null for none.
type hostInfoJSON struct {
	hostInfoStateJSON
	Metrics json.RawMessage `json:"metrics"`
}

func hostInfoFromState(st osservices.State) hostInfoJSON {
	info := hostInfoJSON{}
	if !st.HostInfo {
		return info
	}
	info.Available = true
	info.Sections = hostInfoSectionsJSON{
		Inventory: st.HostInfoSections&osservices.HostInfoSectionInventory != 0,
		Metrics:   st.HostInfoSections&osservices.HostInfoSectionMetrics != 0,
		Processes: st.HostInfoSections&osservices.HostInfoSectionProcesses != 0,
		USB:       st.HostInfoSections&osservices.HostInfoSectionUSB != 0,
	}
	info.Inventory = st.HostInfoInventory
	info.Metrics = st.HostInfoMetrics
	return info
}

func (s *osServicesService) hostInfoNow() hostInfoJSON {
	var st osservices.State
	s.do(func(session *osservices.Session) { st = session.State() })
	return hostInfoFromState(st)
}

// hostInfoStateChanged sends hostInfoState when the availability, the
// sections or the inventory changed. The state notifier calls it.
func (s *osServicesService) hostInfoStateChanged() {
	h := &s.hostInfo
	state := s.hostInfoNow().hostInfoStateJSON
	if h.sentOnce && state.Available == h.sentState.Available && state.Sections == h.sentState.Sections &&
		bytes.Equal(state.Inventory, h.sentState.Inventory) {
		return
	}
	if !state.Available || !state.Sections.Processes {
		// The session ended or the processes went: the screen's page of
		// the busiest processes goes too.
		h.setTop(nil, time.Time{})
	}
	h.sentOnce = true
	h.sentState = state
	if session := currentSession; session != nil {
		writeJSONRPCEvent("hostInfoState", state, session)
	}
}

// --- RPC ------------------------------------------------------------------------

func rpcGetHostInfo() (hostInfoJSON, error) {
	if osServices == nil {
		return hostInfoJSON{}, nil
	}
	return osServices.hostInfoNow(), nil
}

// rpcWatchHostInfo renews the browser's lease on the target's metrics.
func rpcWatchHostInfo() (bool, error) {
	if osServices != nil {
		osServices.hostInfo.browserUntil.Store(time.Now().Add(hostInfoBrowserLease).UnixNano())
	}
	return true, nil
}

// queued runs one query for a section when its turn comes and returns what
// run returned. The queries go to the target one at a time, in the order of
// the calls. A call that comes while a query for the same section still
// waits for its turn shares that query: its answer is taken after the call,
// and calls that come faster than the target answers do not pile up. A
// query that waited for its turn longer than wait fails with
// hostInfoNoAnswer and does not go to the target.
func (h *hostInfoGlue) queued(section uint8, wait time.Duration, run func() (json.RawMessage, error)) (json.RawMessage, error) {
	h.queueMu.Lock()
	call := h.waiting[section]
	if call == nil {
		call = &hostInfoCall{done: make(chan struct{})}
		h.waiting[section] = call
		go func() {
			defer close(call.done)
			turn := time.NewTimer(wait)
			defer turn.Stop()
			select {
			case h.slot <- struct{}{}:
			case <-turn.C:
				h.queueMu.Lock()
				delete(h.waiting, section)
				h.queueMu.Unlock()
				call.err = errors.New(hostInfoNoAnswer)
				return
			}
			defer func() { <-h.slot }()
			// From here a new call is a new query.
			h.queueMu.Lock()
			delete(h.waiting, section)
			h.queueMu.Unlock()
			call.result, call.err = run()
		}()
	}
	h.queueMu.Unlock()
	<-call.done
	return call.result, call.err
}

// hostInfoQuery asks the target for a section and waits for the answer: at
// most hostInfoQueryWait for the queries before it, and as long again for
// the answer.
func (s *osServicesService) hostInfoQuery(section uint8) (json.RawMessage, error) {
	return s.hostInfo.queued(section, hostInfoQueryWait, func() (json.RawMessage, error) {
		return s.hostInfoAsk(section)
	})
}

// hostInfoAsk sends one query and waits for its answer. Only queued calls
// it, so that no other query is in flight.
func (s *osServicesService) hostInfoAsk(section uint8) (json.RawMessage, error) {
	h := &s.hostInfo
	deadline := time.NewTimer(hostInfoQueryWait)
	defer deadline.Stop()
	wait := make(chan hostInfoResult, 1)
	var id uint32
	var reason string
	s.do(func(session *osservices.Session) {
		if !session.HostInfoAvailable() {
			reason = hostInfoUnavailable
			return
		}
		if id, reason = session.HostInfoQuery(section); id != 0 {
			h.waits[id] = wait
		}
	})
	if id == 0 {
		return nil, errors.New(reason)
	}
	select {
	case answer := <-wait:
		if answer.result == nil {
			return nil, errors.New(answer.failure)
		}
		if len(answer.result) > hostInfoAnswerMax {
			return nil, errors.New(hostInfoTooLarge)
		}
		return answer.result, nil
	case <-deadline.C:
		s.do(func(session *osservices.Session) {
			delete(h.waits, id)
			session.HostInfoQueryCancel(id)
		})
		return nil, errors.New(hostInfoNoAnswer)
	}
}

func rpcGetHostProcesses() (json.RawMessage, error) {
	s, err := requireOSServices()
	if err != nil {
		return nil, errors.New(hostInfoUnavailable)
	}
	return s.hostInfoQuery(osservices.HostInfoQueryProcesses)
}

func rpcGetHostUsbDevices() (json.RawMessage, error) {
	s, err := requireOSServices()
	if err != nil {
		return nil, errors.New(hostInfoUnavailable)
	}
	return s.hostInfoQuery(osservices.HostInfoQueryUSB)
}
