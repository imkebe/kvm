package kvm

import (
	"strings"
	"testing"
	"github.com/jetkvm/kvm/internal/native"
	"github.com/jetkvm/kvm/internal/usbgadget"
)

// Text the app does not write itself cannot add a card, a page or a chart
// line to the screen's markup.
func TestHomeCardsEscapeText(t *testing.T) {
	savedConfig, savedVideo := config, lastVideoState
	defer func() { config, lastVideoState = savedConfig, savedVideo }()
	const hostile = "1\f~x\v\n~9;g;5"
	config = &Config{UsbDevices: &usbgadget.Devices{}, VideoCodecPreference: hostile}

	for _, video := range []native.VideoState{
		{Ready: true, Width: 1920, Height: 1080, FramePerSecond: 60},
		{Error: hostile},
	} {
		lastVideoState = video
		cards := strings.Split(homeCards(), "\f")
		// The Video card and the System card.
		if len(cards) != 2 {
			t.Fatalf("%d cards: %q", len(cards), cards)
		}
		if pages := strings.Count(cards[0], "\v") + 1; video.Ready && pages != 4 || !video.Ready && pages != 1 {
			t.Fatalf("Video card has %d pages: %q", pages, cards[0])
		}
		for _, line := range strings.FieldsFunc(cards[0], func(r rune) bool { return r == '\v' || r == '\n' }) {
			if strings.HasPrefix(line, "~x") || strings.HasPrefix(line, "~9") {
				t.Fatalf("injected line %q", line)
			}
		}
	}
}

// homeNative records the screen variables the app sets.
type homeNative struct {
	native.EmptyNativeInterface
	set map[string]int
}

func (n *homeNative) UISetVar(name, _ string) { n.set[name]++ }

func homeTestScreen(t *testing.T) *homeNative {
	t.Helper()
	savedConfig, savedNative, savedBacklight := config, nativeInstance, backlightState
	savedSent := homeScreenSent
	savedPage, savedPageResend := homeCardChosen, homeCardResend
	t.Cleanup(func() {
		config, nativeInstance, backlightState = savedConfig, savedNative, savedBacklight
		homeScreenSent = savedSent
		homeCardChosen, homeCardResend = savedPage, savedPageResend
	})
	screen := &homeNative{set: map[string]int{}}
	config = &Config{UsbDevices: &usbgadget.Devices{}}
	nativeInstance = screen
	homeScreenSent = [3]string{}
	homeCardChosen, homeCardResend = "", false
	return screen
}

// A screen-process restart resends the card that setDisplayCard chose, after
// the cards; with the backlight off, once it wakes. The minute resend does not.
func TestHomePageResentAfterRestart(t *testing.T) {
	screen := homeTestScreen(t)
	homeScreenResend()
	if screen.set["home_page"] != 0 {
		t.Fatalf("a page nobody chose was sent: %v", screen.set)
	}
	homeCardChosen = "3"
	homeScreenResend()
	if screen.set["home_page"] != 1 {
		t.Fatalf("after restart sent %v", screen.set)
	}
	homeScreenTick(0, false)
	backlightState = 2
	homeScreenResend()
	backlightState = 0
	homeScreenTick(1, true)
	if screen.set["home_page"] != 2 {
		t.Fatalf("after restart while dark sent %v", screen.set)
	}
}

// With the backlight off the screen gets nothing; the first tick after it
// wakes sends the cards too, not only the status.
func TestHomeTickerIdleWhenScreenOff(t *testing.T) {
	screen := homeTestScreen(t)
	backlightState = 2
	asleep := false
	for tick := 0; tick < 25; tick++ {
		asleep = homeScreenTick(tick, asleep)
	}
	updateHomeScreen()
	if !asleep || len(screen.set) != 0 {
		t.Fatalf("asleep %v, sent %v", asleep, screen.set)
	}
	backlightState = 0
	if homeScreenTick(1, asleep) || screen.set["home_cards"] != 1 || screen.set["home_status"] != 1 {
		t.Fatalf("after wake sent %v", screen.set)
	}
	// Unchanged status and details are not sent again (the cards show live
	// memory and uptime figures, so they may change).
	homeScreenTick(10, false)
	if screen.set["home_status"] != 1 || screen.set["home_details"] != 1 {
		t.Fatalf("unchanged content sent again: %v", screen.set)
	}
}

// After a native restart all of the content is sent again, at once while
// the screen is lit, else when it wakes.
func TestHomeResendAfterNativeRestart(t *testing.T) {
	screen := homeTestScreen(t)
	homeScreenTick(0, false)
	homeScreenResend()
	if screen.set["home_cards"] != 2 || screen.set["home_status"] != 2 || screen.set["home_details"] != 2 {
		t.Fatalf("lit resend %v", screen.set)
	}
	backlightState = 2
	homeScreenResend()
	if screen.set["home_status"] != 2 {
		t.Fatalf("sent while dark: %v", screen.set)
	}
	backlightState = 0
	homeScreenTick(3, true)
	if screen.set["home_cards"] != 3 || screen.set["home_status"] != 3 || screen.set["home_details"] != 3 {
		t.Fatalf("wake resend %v", screen.set)
	}
}

// R5: while lit, everything is sent again once a minute, in case a send was
// lost; while dark, nothing.
func TestHomePeriodicResendOnlyWhileLit(t *testing.T) {
	screen := homeTestScreen(t)
	homeScreenTick(0, false)
	homeScreenTick(1, false)
	if screen.set["home_status"] != 1 || screen.set["home_details"] != 1 {
		t.Fatalf("unchanged content sent again: %v", screen.set)
	}
	homeScreenTick(homeScreenResendTicks, false)
	if screen.set["home_status"] != 2 || screen.set["home_details"] != 2 || screen.set["home_cards"] != 2 {
		t.Fatalf("periodic resend %v", screen.set)
	}
	backlightState = 2
	homeScreenTick(2*homeScreenResendTicks, false)
	if screen.set["home_status"] != 2 {
		t.Fatalf("sent while dark: %v", screen.set)
	}
}
