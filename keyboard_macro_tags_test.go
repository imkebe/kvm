package kvm

import (
	"strings"
	"testing"
)

func TestKeyboardMacroTags(t *testing.T) {
	macro := KeyboardMacro{
		Name:  "Lock",
		Steps: []KeyboardMacroStep{{Keys: []string{"KeyL"}, Modifiers: []string{"MetaLeft"}}},
		Tags:  []string{" Windows ", "windows", "", "MacOS"},
	}
	if err := macro.Validate(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(macro.Tags, ",") != "windows,macos" {
		t.Fatalf("tags = %v", macro.Tags)
	}

	macro.Tags = []string{strings.Repeat("x", MaxTagLength+1)}
	if err := macro.Validate(); err == nil {
		t.Fatal("a long tag was accepted")
	}
	macro.Tags = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	if err := macro.Validate(); err == nil {
		t.Fatal("too many tags were accepted")
	}
}
