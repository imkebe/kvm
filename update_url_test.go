package kvm

import "testing"

func TestUpdateURLIsTheForksUnlessSetOtherwise(t *testing.T) {
	fork := forkUpdateURL + "/releases"
	for saved, want := range map[string]string{
		"":                         fork,
		DefaultAPIURL:              fork, // what every config saved by official firmware holds
		"https://updates.example/": "https://updates.example/releases",
	} {
		if got := (&Config{UpdateAPIURL: saved}).GetUpdateAPIURL(); got != want {
			t.Errorf("update_api_url %q: got %q, want %q", saved, got, want)
		}
	}
}
