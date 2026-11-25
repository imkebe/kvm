import { test, expect } from "bun:test";
import { osServicesRepositoryHttpAllowed } from "../src/utils/osServicesRepository";
// Same cases as TestNormalizeOSServicesRepository on the Go side.
test("plain http only for a private or loopback IP address", () => {
  for (const ok of [
    "https://example.com/releases",
    "http://192.168.2.2:8770",
    "http://127.0.0.1:8770/r",
    "http://[fd00::1]",
    "http://10.1.2.3",
    "http://172.16.0.1",
  ]) {
    expect(osServicesRepositoryHttpAllowed(ok)).toBe(true);
  }
  for (const bad of [
    "http://example.com/releases",
    "http://8.8.8.8",
    "http://169.254.1.1",
    "http://172.32.0.1",
    "http://100.64.0.1",
  ]) {
    expect(osServicesRepositoryHttpAllowed(bad)).toBe(false);
  }
});
