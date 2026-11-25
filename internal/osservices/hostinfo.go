package osservices

import (
	"bytes"
	"encoding/json"
)

// Host info service (service 7, minor version 1): the sender reports the
// target's system information as JSON. The session checks that each report
// is a JSON object within its limit and keeps it as it came; the readers of
// the JSON ignore the members that they do not know.
//
// An invalid host info message is dropped and counted
// (Counters.HostInfoDropped). It neither ends the session nor faults the
// service: nothing else depends on host info, and the next valid report
// replaces what the KVM shows.

const (
	// HostInfoIntervalMS is the interval of hostinfo.metrics that the KVM
	// asks for.
	HostInfoIntervalMS = 1000
	// The KVM renews its subscription this often; the sender drops one that
	// was not renewed for 15 seconds.
	hostInfoRenewMS = 5000
	// HostInfoQueryTimeoutMS: the KVM abandons a query after this long.
	HostInfoQueryTimeoutMS = 5000
	// The message of a failed query, cut to this many bytes.
	hostInfoMessageMax = 256

	hostInfoSessionEnded = "the OS Services session ended"
	hostInfoWithdrawn    = "the target computer withdrew its system information"
	hostInfoDisabled     = "system information is turned off in the OS Services settings"
	hostInfoNoAnswer     = "the target computer did not answer in time"
	hostInfoTooLarge     = "the answer is above 1 MiB"
	hostInfoNotJSON      = "the answer is not a JSON object"
	hostInfoUnavailable  = "the target computer does not offer this information"
	hostInfoBusy         = "another query is in progress"
)

// HostInfoPlatform hears of metrics and of the answers to queries.
type HostInfoPlatform interface {
	// HostInfoMetrics: a hostinfo.metrics arrived. metrics is a JSON object
	// that is never changed, so the platform can keep it.
	HostInfoMetrics(metrics json.RawMessage)
	// HostInfoDone: a query ended, exactly once per query that
	// HostInfoQuery started and HostInfoQueryCancel did not end. result is
	// the JSON object of the answer; it is nil when the query failed, and
	// failure then says why.
	HostInfoDone(requestID uint32, result json.RawMessage, failure string)
}

type hostInfoService struct {
	// The section bits of the sender's hostinfo.capabilities; 0 for none.
	sections  uint32
	inventory json.RawMessage
	metrics   json.RawMessage

	// The subscription: a hostinfo.subscribe with an interval went out at
	// subscribedMS and none with interval 0 since.
	subscribed   bool
	subscribedMS int64

	// The query in flight.
	queryOpen     bool
	queryID       uint32
	querySection  uint8
	queryError    bool
	queryDeadline int64
	queryBody     []byte
	lastRequestID uint32
}

// jsonObject reports whether data is one JSON object in valid UTF-8.
func jsonObject(data []byte) bool {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '{' && UTF8Valid(data) && json.Valid(data)
}

// endHostInfoQuery ends the query in flight and tells the platform.
func (s *Session) endHostInfoQuery(result json.RawMessage, failure string) {
	h := &s.hostInfo
	if !h.queryOpen {
		return
	}
	id := h.queryID
	h.queryOpen = false
	h.queryBody = nil
	if s.hostPlatform != nil {
		s.hostPlatform.HostInfoDone(id, result, failure)
	}
}

// clearHostInfo forgets the sender's host info and fails the query in
// flight. The request IDs go on.
func (s *Session) clearHostInfo(reason string) {
	s.endHostInfoQuery(nil, reason)
	s.hostInfo = hostInfoService{lastRequestID: s.hostInfo.lastRequestID}
}

// HostInfoAvailable reports whether the sender of the open session offers
// host info and the settings allow it.
func (s *Session) HostInfoAvailable() bool {
	return s.link == linkOpen && s.hostInfo.sections != 0 && s.settings.HostInfoEnabled
}

func (s *Session) hostInfoDrop() { s.Counters.HostInfoDropped++ }

func (s *Session) handleHostInfo(payload []byte) {
	h := &s.hostInfo
	switch s.kind {
	case hostInfoCapabilities:
		if len(payload) != hostInfoCapabilitiesLen || le.Uint32(payload)&^uint32(hostInfoSectionsDefined) != 0 {
			s.hostInfoDrop()
			return
		}
		sections := le.Uint32(payload)
		if !s.settings.HostInfoEnabled || s.protocolMinor < 1 {
			// The sender has not read the session.config yet, or it
			// speaks 5.0, which has no service 7.
			sections = 0
		}
		if sections == h.sections {
			return
		}
		if sections == 0 {
			s.clearHostInfo(hostInfoWithdrawn)
		} else {
			if h.queryOpen && !hostInfoOffers(sections, h.querySection) {
				s.endHostInfoQuery(nil, hostInfoWithdrawn)
			}
			h.sections = sections
			if sections&HostInfoSectionMetrics == 0 {
				h.metrics = nil
				h.subscribed = false
			}
			if sections&HostInfoSectionInventory == 0 {
				h.inventory = nil
			}
		}
		s.notifyState()
	case hostInfoInventory, hostInfoMetrics:
		if !s.HostInfoAvailable() {
			return
		}
		if len(payload) > HostInfoMaxJSON || !jsonObject(payload) {
			s.hostInfoDrop()
			return
		}
		if s.kind == hostInfoInventory {
			// The sender sends it again only when it changed, but a new
			// subscription or registration can repeat it.
			if bytes.Equal(h.inventory, payload) {
				return
			}
			h.inventory = append(json.RawMessage(nil), payload...)
			s.notifyState()
			return
		}
		h.metrics = append(json.RawMessage(nil), payload...)
		if s.hostPlatform != nil {
			s.hostPlatform.HostInfoMetrics(h.metrics)
		}
	case hostInfoResult:
		s.handleHostInfoResult(payload)
	default:
		s.Counters.MessagesSkipped++
	}
}

func (s *Session) handleHostInfoResult(payload []byte) {
	h := &s.hostInfo
	if len(payload) < hostInfoResultHeaderLen || len(payload)-hostInfoResultHeaderLen > hostInfoMaxChunk ||
		payload[5]&^byte(hostInfoResultLast|hostInfoResultError) != 0 || payload[6] != 0 || payload[7] != 0 {
		s.hostInfoDrop()
		return
	}
	if !h.queryOpen || le.Uint32(payload) != h.queryID {
		return // abandoned; a late answer is ignored
	}
	if payload[4] != h.querySection {
		s.hostInfoDrop()
		s.endHostInfoQuery(nil, "the answer is for another section")
		return
	}
	chunk := payload[hostInfoResultHeaderLen:]
	if len(h.queryBody)+len(chunk) > HostInfoMaxResult {
		s.hostInfoDrop()
		s.endHostInfoQuery(nil, hostInfoTooLarge)
		return
	}
	h.queryBody = append(h.queryBody, chunk...)
	h.queryError = h.queryError || payload[5]&hostInfoResultError != 0
	if payload[5]&hostInfoResultLast == 0 {
		return
	}
	body := h.queryBody
	switch {
	case h.queryError:
		message := "the target computer could not collect the information"
		if len(body) > 0 && UTF8Valid(body) {
			message = truncate(string(body), hostInfoMessageMax)
		}
		s.endHostInfoQuery(nil, message)
	case !jsonObject(body):
		s.hostInfoDrop()
		s.endHostInfoQuery(nil, hostInfoNotJSON)
	default:
		s.endHostInfoQuery(json.RawMessage(body), "")
	}
}

func hostInfoOffers(sections uint32, section uint8) bool {
	switch section {
	case HostInfoQueryInventory:
		return sections&HostInfoSectionInventory != 0
	case HostInfoQueryProcesses, HostInfoQueryTop:
		return sections&HostInfoSectionProcesses != 0
	case HostInfoQueryUSB:
		return sections&HostInfoSectionUSB != 0
	}
	return false
}

// HostInfoOffers reports whether a query for a section (HostInfoQuery*) can
// be answered: host info is available and the sender offers the section.
func (s *Session) HostInfoOffers(section uint8) bool {
	return s.HostInfoAvailable() && hostInfoOffers(s.hostInfo.sections, section)
}

// HostInfoQuery sends a hostinfo.query for a section (HostInfoQuery*). It
// returns the request ID, or 0 and the reason when the query cannot start:
// the sender does not offer the section, or a query is in flight. The answer
// comes with HostInfoDone, at the latest after HostInfoQueryTimeoutMS.
func (s *Session) HostInfoQuery(section uint8) (uint32, string) {
	h := &s.hostInfo
	if !s.HostInfoAvailable() || !hostInfoOffers(h.sections, section) || s.hostPlatform == nil {
		return 0, hostInfoUnavailable
	}
	if h.queryOpen {
		return 0, hostInfoBusy
	}
	id := h.lastRequestID + 1
	if id == 0 {
		id = 1
	}
	var query [hostInfoQueryLen]byte
	le.PutUint32(query[:], id)
	query[4] = section
	if !s.sendMessage(ServiceHostInfo, hostInfoQuery, query[:]) {
		return 0, inEndpointStuck
	}
	h.lastRequestID = id
	h.queryOpen = true
	h.queryID = id
	h.querySection = section
	h.queryError = false
	h.queryBody = nil
	h.queryDeadline = s.nowMS() + HostInfoQueryTimeoutMS
	return id, ""
}

// HostInfoQueryCancel abandons a query without a HostInfoDone; its answer
// is ignored.
func (s *Session) HostInfoQueryCancel(requestID uint32) {
	h := &s.hostInfo
	if h.queryOpen && h.queryID == requestID {
		h.queryOpen = false
		h.queryBody = nil
	}
}

func (s *Session) hostInfoTick(now int64) {
	if h := &s.hostInfo; h.queryOpen && now >= h.queryDeadline {
		s.endHostInfoQuery(nil, hostInfoNoAnswer)
	}
}

func (s *Session) sendHostInfoSubscribe(intervalMS uint32) bool {
	var subscribe [hostInfoSubscribeLen]byte
	// The flags are reserved and stay 0.
	le.PutUint32(subscribe[:], intervalMS)
	return s.sendMessage(ServiceHostInfo, hostInfoSubscribe, subscribe[:])
}

// HostInfoWatch keeps the metrics subscription as wanted; call it every
// tick. While somebody watches, a hostinfo.subscribe goes out at once and
// then every 5 seconds; when nobody does any more, one with interval 0.
func (s *Session) HostInfoWatch(wanted bool) {
	h := &s.hostInfo
	if !s.HostInfoAvailable() || h.sections&HostInfoSectionMetrics == 0 {
		return
	}
	now := s.nowMS()
	switch {
	case wanted && (!h.subscribed || now-h.subscribedMS >= hostInfoRenewMS):
		if s.sendHostInfoSubscribe(HostInfoIntervalMS) {
			h.subscribed = true
			h.subscribedMS = now
		}
	case !wanted && h.subscribed:
		h.subscribed = false
		s.sendHostInfoSubscribe(0)
	}
}
