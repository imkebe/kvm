package kvm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// oemModulesRoot is where the vendor image keeps its kernel modules: a flat
// directory with its own modules.dep, outside modprobe's /lib/modules tree,
// so nothing loads them on demand.
var oemModulesRoot = "/oem/usr/ko"

var oemModulesMu sync.Mutex

// nftModules are the nftables parts that NetBird's firewall and the USB
// network's table both need (the USB input filter also needs nft_ct).
var nftModules = []string{"nf_tables", "nft_chain_nat", "nft_masq", "nft_counter"}

// runCommand runs a program (no shell); with ignoreError the error carries no output.
func runCommand(args []string, ignoreError bool) error {
	if len(args) == 0 {
		return nil
	}
	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ignoreError {
			return err
		}
		return fmt.Errorf("%s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// insmodModule loads one module file; replaced in tests.
var insmodModule = func(path string) error {
	return runCommand([]string{"insmod", path}, false)
}

// loadOEMModules loads the named modules (without ".ko") and their
// dependencies from oemModulesRoot, skipping those already loaded. An image
// without the OEM directory uses standard module autoload, so it does nothing.
func loadOEMModules(names ...string) error {
	oemModulesMu.Lock()
	defer oemModulesMu.Unlock()

	data, err := os.ReadFile(filepath.Join(oemModulesRoot, "modules.dep"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	loadedData, err := os.ReadFile("/proc/modules")
	if err != nil {
		return err
	}
	return loadOEMModulesFrom(data, loadedData, names)
}

func loadOEMModulesFrom(depData, loadedData []byte, names []string) error {
	dependencies := map[string][]string{}
	for _, line := range strings.Split(string(depData), "\n") {
		name, deps, ok := strings.Cut(line, ":")
		if ok {
			dependencies[filepath.Base(name)] = strings.Fields(deps)
		}
	}
	loaded := map[string]bool{}
	for _, line := range strings.Split(string(loadedData), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			// /proc/modules lists names with underscores; files may use dashes.
			loaded[fields[0]+".ko"] = true
		}
	}
	isLoaded := func(file string) bool {
		return loaded[file] || loaded[strings.ReplaceAll(file, "-", "_")]
	}
	var load func(string) error
	load = func(name string) error {
		name = filepath.Base(name)
		if isLoaded(name) {
			return nil
		}
		deps, exists := dependencies[name]
		if !exists {
			return fmt.Errorf("kernel module %s is missing from %s", name, oemModulesRoot)
		}
		for _, dep := range deps {
			if err := load(dep); err != nil {
				return err
			}
		}
		if err := insmodModule(filepath.Join(oemModulesRoot, name)); err != nil {
			return err
		}
		loaded[name] = true
		return nil
	}
	for _, name := range names {
		if err := load(strings.TrimSuffix(name, ".ko") + ".ko"); err != nil {
			return err
		}
	}
	return nil
}
