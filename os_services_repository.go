package kvm

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// An OS Services repository holds the releases: the update index
// (releases.json, releases-dev.json, each with a .sig), each release's
// files under <version>/, and the newest stable offline disc as
// jetkvm-os-services.iso. JetKVM's is osServicesBucketURL.
//
// A GitHub repository (https://github.com/<owner>/<repo>) keeps them as
// release assets: a file under <version>/ is an asset of the release tagged
// <version>, and a top-level file is an asset of the latest release. Any
// other URL is a folder laid out as JetKVM's.

const maxRepositoryURL = 300

// normalizeOSServicesRepository checks a repository URL and gives the form
// that is stored: no trailing slash, empty for JetKVM's.
func normalizeOSServicesRepository(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" || raw == osServicesBucketURL {
		return "", nil
	}
	if len(raw) > maxRepositoryURL {
		return "", errors.New("the repository URL is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("the repository URL must be an http or https URL")
	}
	// Plain http only on a private or loopback address, such as a test server on the LAN.
	if ip := net.ParseIP(u.Hostname()); u.Scheme == "http" && (ip == nil || !(ip.IsPrivate() || ip.IsLoopback())) {
		return "", errors.New("the repository URL must be https, or http with a private or loopback IP address")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("the repository URL cannot have a query, a fragment or a user name")
	}
	if isGitHubHost(u.Host) {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", errors.New("a GitHub repository URL is https://github.com/<owner>/<repository>")
		}
		return "https://github.com/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git"), nil
	}
	return raw, nil
}

func isGitHubHost(host string) bool {
	return strings.EqualFold(host, "github.com") || strings.EqualFold(host, "www.github.com")
}

func isGitHubRepository(repository string) bool {
	return strings.HasPrefix(repository, "https://github.com/")
}

// osServicesRepository is the repository in use.
func osServicesRepository() string {
	configLock.Lock()
	defer configLock.Unlock()
	if config != nil && config.OSServices != nil && config.OSServices.RepositoryURL != "" {
		return config.OSServices.RepositoryURL
	}
	return osServicesBucketURL
}

// osServicesRepositoryFile is the URL of a file of the repository, by its
// path in JetKVM's layout.
func osServicesRepositoryFile(repository, path string) string {
	path = strings.TrimLeft(path, "/")
	if isGitHubRepository(repository) {
		if strings.Contains(path, "/") {
			return repository + "/releases/download/" + path
		}
		return repository + "/releases/latest/download/" + path
	}
	return repository + "/" + path
}

// osServicesRepositoryPage is the page the installer disc opens for another
// repository, empty for JetKVM's, whose disc opens jetkvm.com.
func osServicesRepositoryPage(repository string) string {
	switch {
	case repository == osServicesBucketURL:
		return ""
	case isGitHubRepository(repository):
		return repository + "/releases/latest"
	default:
		return repository + "/"
	}
}
