package kvm

import "testing"

func TestNormalizeOSServicesRepository(t *testing.T) {
	good := map[string]string{
		"":                                   "",
		"https://os-services.jetkvm.com/":    "",
		" https://github.com/me/os-svc.git/": "https://github.com/me/os-svc",
		"https://example.com/releases/":      "https://example.com/releases",
		"http://192.168.2.2:8770":            "http://192.168.2.2:8770",
		"http://127.0.0.1:8770/r":            "http://127.0.0.1:8770/r",
		"http://[fd00::1]":                   "http://[fd00::1]",
	}
	for in, want := range good {
		got, err := normalizeOSServicesRepository(in)
		if err != nil || got != want {
			t.Fatalf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"ftp://example.com", "example.com/x", "https://github.com/me",
		"https://github.com/me/repo/releases", "https://example.com/?x=1", "https://u@example.com",
		"http://example.com/releases", "http://8.8.8.8", "http://169.254.1.1",
	} {
		if _, err := normalizeOSServicesRepository(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestOSServicesRepositoryFile(t *testing.T) {
	cases := []struct{ repo, path, want string }{
		{osServicesBucketURL, "releases.json", osServicesBucketURL + "/releases.json"},
		{osServicesBucketURL, "0.3.1/a.iso", osServicesBucketURL + "/0.3.1/a.iso"},
		{"https://github.com/me/r", "releases.json", "https://github.com/me/r/releases/latest/download/releases.json"},
		{"https://github.com/me/r", "0.3.1/a.iso.sig", "https://github.com/me/r/releases/download/0.3.1/a.iso.sig"},
		{"https://example.com/x", "/0.3.1/a.iso", "https://example.com/x/0.3.1/a.iso"},
	}
	for _, c := range cases {
		if got := osServicesRepositoryFile(c.repo, c.path); got != c.want {
			t.Fatalf("%s %s: got %s", c.repo, c.path, got)
		}
	}
	if osServicesRepositoryPage(osServicesBucketURL) != "" ||
		osServicesRepositoryPage("https://github.com/me/r") != "https://github.com/me/r/releases/latest" {
		t.Fatal("repository page")
	}
}
