package kvm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jetkvm/kvm/internal/osservices"
	"github.com/jetkvm/kvm/internal/usbgadget"
)

// The home screen of the device shows, below the cards, a list of the USB
// functions and the video input, and two icons for the USB and HDMI cables.
// Each has one of four states:
//
//	off     the function is not enabled (gray)
//	active  it is in use (green)
//	idle    it is enabled, and nothing uses it now (yellow)
//	failed  it has an error (red)
//
// The cards beside the network card show statistics. The screen code is
// internal/native/cgo/home_ext.c; it takes the content as three UI variables.

const (
	homeOff    = 'x'
	homeActive = 'g'
	homeIdle   = 'y'
	homeFailed = 'r'
)

var (
	// Counted where a video frame goes to a browser.
	videoFramesSent atomic.Uint64
	videoBytesSent  atomic.Uint64

	homeScreenMu   sync.Mutex
	homeScreenSent [3]string
	homeRates      homeRateSample
	// homeCardChosen is the card that setDisplayCard chose last;
	// homeCardResend: the screen process restarted and lost it.
	homeCardChosen string
	homeCardResend bool
)

// homeHistoryLen is the number of readings, one a second, that a chart
// shows.
const homeHistoryLen = 60

// homeHistory is the last readings of one value, oldest first.
type homeHistory []int

func (h *homeHistory) push(value float64) {
	*h = append(*h, int(value+0.5))
	if len(*h) > homeHistoryLen {
		*h = (*h)[len(*h)-homeHistoryLen:]
	}
}

// homeRateSample is the last reading of the counters that the cards show as
// rates, and the history of each rate.
type homeRateSample struct {
	at           time.Time
	videoFrames  uint64
	videoBytes   uint64
	cpuBusy      uint64
	cpuTotal     uint64
	// The rates between the last two readings.
	videoFPS  float64
	videoKbps float64
	cpu       float64

	videoKbpsHistory homeHistory
	videoFPSHistory  homeHistory
	cpuHistory       homeHistory
	memoryHistory    homeHistory
	heatHistory      homeHistory
}

// homeStatsUntil is the time, in Unix nanoseconds, up to which the screen
// shows statistics. The screen renews it every 5 seconds.
var homeStatsUntil atomic.Int64

// homeStatisticsShown is the screen's report that a statistics card came
// into view or left it.
func homeStatisticsShown(shown bool) {
	if !shown {
		homeStatsUntil.Store(0)
		return
	}
	homeStatsUntil.Store(time.Now().Add(12 * time.Second).UnixNano())
}

// homeStatisticsWanted reports whether a lit screen shows statistics. Only
// then the app reads the processor, memory and interface counters.
func homeStatisticsWanted() bool {
	return backlightState != 2 && time.Now().UnixNano() < homeStatsUntil.Load()
}

// homeScreenTicker keeps the home screen's content current while the screen
// is lit; with the backlight off it does nothing. The status icons are
// refreshed once a second, the statistics sampled once a second while the
// screen shows them, and the cards otherwise every ten seconds and when the
// screen wakes, and everything again once a minute. After a native restart
// homeScreenResend sends everything.
func homeScreenTicker() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	asleep := false
	for tick := 0; ; tick++ {
		<-ticker.C
		asleep = homeScreenTick(tick, asleep)
	}
}

const homeScreenResendTicks = 60

// homeScreenTick is one tick of homeScreenTicker; it returns whether the
// backlight is off.
func homeScreenTick(tick int, wasAsleep bool) bool {
	if backlightState == 2 {
		return true
	}
	// A send made before the screen process was ready, or lost to an error,
	// is not retried otherwise: send everything again once a minute while lit.
	if tick%homeScreenResendTicks == 0 {
		homeScreenMu.Lock()
		homeScreenSent = [3]string{}
		homeScreenMu.Unlock()
	}
	wanted := homeStatisticsWanted()
	homeScreenRefresh(wanted, wanted || wasAsleep || tick%10 == 0)
	return false
}

func updateHomeScreen() {
	if backlightState == 2 {
		return
	}
	homeScreenRefresh(false, homeStatisticsWanted())
}

// homeScreenResend sends all of the content again: the screen process lost
// it when it restarted. With the backlight off, the tick that sees it wake
// sends it.
func homeScreenResend() {
	homeScreenMu.Lock()
	homeScreenSent = [3]string{}
	homeCardResend = homeCardChosen != ""
	homeScreenMu.Unlock()
	if backlightState != 2 {
		homeScreenRefresh(false, true)
	}
}

// homeScreenRefresh sends the content that changed: the status icons and
// the network details, and the statistics cards when cards is set. sample
// takes a reading of the statistics first.
func homeScreenRefresh(sample, cards bool) {
	if nativeInstance == nil || config == nil || config.UsbDevices == nil {
		return
	}
	homeScreenMu.Lock()
	defer homeScreenMu.Unlock()
	if sample {
		homeSampleRates()
	}
	values := []string{homeStatus(), "", homeDetails()}
	if cards {
		values[1] = homeCards()
	}
	for i, value := range values {
		if value == homeScreenSent[i] || (i == 1 && !cards) {
			continue
		}
		homeScreenSent[i] = value
		nativeInstance.UISetVar([]string{"home_status", "home_cards", "home_details"}[i], value)
	}
	// After the cards, which the page refers to.
	if cards && homeCardResend {
		homeCardResend = false
		nativeInstance.UISetVar("home_page", homeCardChosen)
	}
}

// homeEnabled maps an enabled function to active or idle, or to failed when
// the USB gadget has an error.
func homeEnabled(enabled, active, usbFailed bool, lastError string) byte {
	switch {
	case !enabled:
		return homeOff
	case usbFailed || lastError != "":
		return homeFailed
	case active:
		return homeActive
	}
	return homeIdle
}

// homeUSBFailed reports an error of the USB gadget: the kernel did not bind
// it to the USB controller although USB emulation is on.
func homeUSBFailed() bool {
	if gadget == nil || !usbEmulationDesired {
		return false
	}
	bound, err := gadget.IsUDCBound()
	return err != nil || !bound
}

func homeStatus() string {
	devices := *config.UsbDevices
	configured := homeUSBState() == usbgadget.USBStateConfigured
	failed := homeUSBFailed()
	var b strings.Builder
	chip := func(name string, state byte) { fmt.Fprintf(&b, "%s:%c ", name, state) }

	usb := byte(homeOff)
	if failed {
		usb = homeFailed
	} else if configured {
		usb = homeActive
	}
	chip("USB", usb)
	video := byte(homeOff)
	if lastVideoState.Ready {
		video = homeActive
	}
	chip("HDMI", video)

	// The input devices have no state of their own: they are active while
	// the target has configured the USB device.
	chip("KBD", homeEnabled(devices.Keyboard, configured, failed, ""))
	chip("ABS", homeEnabled(devices.AbsoluteMouse, configured, failed, ""))
	chip("REL", homeEnabled(devices.RelativeMouse, configured, failed, ""))

	virtualMediaStateMutex.RLock()
	mounted := currentVirtualMediaState != nil
	virtualMediaStateMutex.RUnlock()
	chip("DISK", homeEnabled(devices.MassStorage, configured && mounted, failed, ""))

	audioMu.Lock()
	listening := audioTrack != nil
	audioMu.Unlock()
	chip("AUD", homeEnabled(devices.Audio, configured && listening, failed, ""))
	chip("SER", homeEnabled(devices.SerialConsole, configured, failed, ""))

	_, connected := homeOSServices()
	chip("OSS", homeEnabled(osServicesUSBEnabled(), connected, failed, ""))

	// The video input: active with a signal, failed when the capture
	// reports an error other than a missing signal.
	capture := byte(homeIdle)
	switch {
	case lastVideoState.Ready:
		capture = homeActive
	case lastVideoState.Error != "" && lastVideoState.Error != "no_signal":
		capture = homeFailed
	}
	chip("VID", capture)
	return strings.TrimSpace(b.String())
}

func homeUSBState() string {
	usbStateLock.Lock()
	defer usbStateLock.Unlock()
	return usbState
}

func homeOSServices() (osservices.State, bool) {
	if osServices == nil {
		return osservices.State{}, false
	}
	var state osservices.State
	osServices.do(func(session *osservices.Session) { state = session.State() })
	return state, state.Connected && !state.ApprovalPending
}

func homeSampleRates() {
	now := time.Now()
	sample := homeRates
	seconds := now.Sub(sample.at).Seconds()
	if seconds < 0.9 {
		return
	}
	frames, bytes := videoFramesSent.Load(), videoBytesSent.Load()
	busy, total := homeCPUTimes()
	if seconds >= 5 {
		// Sampling was paused: the charts start again.
		sample = homeRateSample{}
	}
	if !sample.at.IsZero() && seconds < 10 {
		sample.videoFPS = float64(frames-sample.videoFrames) / seconds
		sample.videoKbps = float64(bytes-sample.videoBytes) * 8 / 1000 / seconds
		if total > sample.cpuTotal {
			sample.cpu = 100 * float64(busy-sample.cpuBusy) / float64(total-sample.cpuTotal)
		}
	}
	sample.at = now
	sample.videoFrames, sample.videoBytes = frames, bytes
	sample.cpuBusy, sample.cpuTotal = busy, total

	sample.videoKbpsHistory.push(sample.videoKbps)
	sample.videoFPSHistory.push(sample.videoFPS)
	sample.cpuHistory.push(sample.cpu)
	if memTotal, available := homeMemory(); memTotal > 0 {
		sample.memoryHistory.push(100 * float64(memTotal-available) / float64(memTotal))
	}
	sample.heatHistory.push(float64(homeTemperature()))
	homeRates = sample
}

// homeTemperature is the processor temperature in degrees Celsius, or 0.
func homeTemperature() int {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	milli, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return milli / 1000
}

// homeChart is the chart line of a page: the top of the scale, then a colour
// letter and the readings of each line. A top of 0 fits the scale to the
// readings, with floor as the least top.
func homeChart(top, floor int, lines ...any) string {
	if top == 0 {
		top = floor
		for i := 1; i < len(lines); i += 2 {
			for _, value := range lines[i].(homeHistory) {
				if value > top {
					top = value
				}
			}
		}
		// A little room above the highest reading.
		top += top / 8
	}
	var b strings.Builder
	fmt.Fprintf(&b, "~%d", top)
	for i := 0; i+1 < len(lines); i += 2 {
		history := lines[i+1].(homeHistory)
		if len(history) == 0 {
			history = homeHistory{0}
		}
		fmt.Fprintf(&b, ";%c;", lines[i].(byte))
		for j, value := range history {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Itoa(value))
		}
	}
	return b.String()
}

// homePage is one page of a card: a title and its lines.
func homePage(title string, lines ...string) string {
	return title + "\n" + strings.Join(lines, "\n")
}

// homeCPUTimes reads the busy and the total processor time from /proc/stat.
func homeCPUTimes() (busy, total uint64) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}
	for i, field := range fields[1:] {
		value, _ := strconv.ParseUint(field, 10, 64)
		total += value
		// Fields 4 and 5 are idle and iowait.
		if i != 3 && i != 4 {
			busy += value
		}
	}
	return busy, total
}

func homeRate(kbps float64) string {
	if kbps >= 1000 {
		return fmt.Sprintf("%.1f Mbit/s", kbps/1000)
	}
	return fmt.Sprintf("%.0f kbit/s", kbps)
}

func homeBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d kB", n>>10)
}

// homeCards is the content of the statistics cards. A card is a list of
// pages: the first shows on the home screen, and a tap on it opens the
// others. Only the cards of enabled functions show.
func homeCards() string {
	devices := *config.UsbDevices
	rates := homeRates
	var cards []string
	add := func(pages ...string) { cards = append(cards, strings.Join(pages, "\v")) }
	const green, yellow, blue = byte(homeActive), byte(homeIdle), byte('b')

	viewers := fmt.Sprintf("%d viewing", getActiveSessions())
	if lastVideoState.Ready {
		mode := fmt.Sprintf("%dx%d at %.0f Hz", lastVideoState.Width, lastVideoState.Height, lastVideoState.FramePerSecond)
		bitrate := homeChart(0, 1000, green, rates.videoKbpsHistory)
		add(homePage("Video", mode+"  "+homeRate(rates.videoKbps), bitrate),
			homePage("Video bitrate", homeRate(rates.videoKbps), bitrate),
			homePage("Video frame rate", fmt.Sprintf("%.0f fps sent", rates.videoFPS),
				homeChart(0, 30, green, rates.videoFPSHistory)),
			homePage("Video input", mode, viewers, "Codec: "+homeText(config.VideoCodecPreference, 20)))
	} else {
		reason := lastVideoState.Error
		if reason == "" {
			reason = "no signal"
		}
		add(homePage("Video", homeText(strings.ReplaceAll(reason, "_", " "), homeLineChars), viewers))
	}

	var audio []string
	if devices.Audio {
		audioMu.Lock()
		listening := audioTrack != nil
		audioMu.Unlock()
		out := "idle"
		if listening {
			out = "playing in the browser"
		}
		audio = append(audio, "Speaker: "+out)
	}
	if len(audio) > 0 {
		add(homePage("Audio", audio...))
	}

	if devices.SerialConsole {
		baud := 0
		if defaultMode != nil {
			baud = defaultMode.BaudRate
		}
		add(homePage("Serial console", fmt.Sprintf("%d baud", baud)))
	}

	if osServicesUSBEnabled() {
		state, connected := homeOSServices()
		line := "no program on the target"
		switch {
		case state.ApprovalPending:
			line = "waiting for approval"
		case connected:
			line = "connected  " + homeText(state.SenderVersion, 18)
		}
		target := ""
		if connected && state.ClipboardOS != 0 {
			target = state.ClipboardOS.String()
		}
		add(homePage("OS Services", line, target))
	}

	memTotal, available := homeMemory()
	memory := fmt.Sprintf("Memory %d of %d MB", (memTotal-available)>>10, memTotal>>10)
	heat := homeTemperature()
	cpu := homeChart(100, 0, green, rates.cpuHistory)
	add(homePage("System", fmt.Sprintf("CPU %.0f%%  %d C", rates.cpu, heat), cpu),
		homePage("Processor", fmt.Sprintf("%.0f%% busy", rates.cpu), cpu),
		homePage("Memory", memory, homeChart(100, 0, blue, rates.memoryHistory)),
		homePage("Temperature", fmt.Sprintf("%d C", heat), homeChart(0, 60, yellow, rates.heatHistory)),
		homePage("System state", homeSystemLines()...))
	return strings.Join(cards, "\f")
}

// The limits of internal/native/cgo/home_ext.c: the cards of the screen
// (MAX_CARDS), the pages of a card (MAX_PAGES), and the lines and the
// characters of a line that a page of the 300 pixel wide screen shows.
const (
	homeMaxCards  = 8
	homeMaxPages  = 6
	homePageLines = 7
	homeLineChars = 30
)

// homeText makes text from the target fit a line of the screen: the screen's
// font has ASCII only, a control character would end the line, the page or
// the card, and a line that starts with '~' is a chart.
func homeText(text string, limit int) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(text) {
		if r < 0x20 || r > 0x7e {
			r = '?'
		}
		b.WriteRune(r)
	}
	out := b.String()
	if strings.HasPrefix(out, "~") {
		out = "-" + out[1:]
	}
	if len(out) > limit && limit > 2 {
		out = out[:limit-2] + ".."
	}
	return out
}

func homeSystemLines() []string {
	var lines []string
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 2 {
			lines = append(lines, "Load "+strings.Join(fields[:3], " "))
		}
	}
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil {
				up := time.Duration(seconds) * time.Second
				lines = append(lines, fmt.Sprintf("Up %dd %dh %dm", int(up.Hours())/24, int(up.Hours())%24, int(up.Minutes())%60))
			}
		}
	}
	return append(lines, "App "+builtAppVersion)
}

// homeMemory is the total and the available memory in kB.
func homeMemory() (total, available uint64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = value
		case "MemAvailable:":
			available = value
		}
	}
	return total, available
}

// homeDetails is the text that a tap on the network card opens.
func homeDetails() string {
	var lines []string
	if networkManager != nil {
		lines = append(lines,
			"Name: "+homeText(networkManager.Hostname(), 64),
			"IPv4: "+homeText(networkManager.IPv4String(), 64),
			"IPv6: "+homeText(networkManager.IPv6String(), 64),
			"MAC: "+homeText(networkManager.MACString(), 64))
	}
	lines = append(lines,
		"USB: "+homeUSBState(),
		"App: "+builtAppVersion,
		fmt.Sprintf("Sessions: %d", getActiveSessions()))
	return homePage("Network", lines...)
}

// rpcSetDisplayCard shows one card of the home screen: 0 is the network
// card, and the statistics cards follow. 100 and up open the details of a
// card: 100 those of the network card. It wakes the screen.
func rpcSetDisplayCard(index int) error {
	if index < 0 || (index > 8 && index < 100) || index > 108 {
		return fmt.Errorf("the home screen has no card %d", index)
	}
	wakeDisplay(true, "display_card")
	nativeInstance.SwitchToScreenIfDifferent("home_screen")
	updateHomeScreen()
	page := strconv.Itoa(index)
	homeScreenMu.Lock()
	homeCardChosen, homeCardResend = page, false
	homeScreenMu.Unlock()
	nativeInstance.UISetVar("home_page", page)
	return nil
}
