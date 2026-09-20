package system

import "testing"

func TestParsers(t *testing.T) {
	osr := parseOSRelease([]byte(`NAME="Ubuntu"
VERSION="22.04 LTS (Jammy Jellyfish)"
PRETTY_NAME="Ubuntu 22.04 LTS"
`))
	if osr["NAME"] != "Ubuntu" || osr["PRETTY_NAME"] != "Ubuntu 22.04 LTS" {
		t.Fatalf("os-release parse: %+v", osr)
	}

	mem := parseMemInfo([]byte(`MemTotal:        1016440 kB
MemFree:          123456 kB
MemAvailable:     654321 kB
Buffers:           10000 kB
Cached:           200000 kB
`))
	if mem["MemTotal"] != 1016440 || mem["MemAvailable"] != 654321 {
		t.Fatalf("meminfo: %+v", mem)
	}

	la := parseLoadAvg([]byte("0.42 0.55 0.60 1/100 1234\n"))
	if la[0] != 0.42 || la[2] != 0.60 {
		t.Fatalf("loadavg: %+v", la)
	}

	up := parseUptime([]byte("12345.67 6789.01\n"))
	if up != 12345 {
		t.Fatalf("uptime: %d", up)
	}

	model, cores := parseCPUInfo([]byte(`processor	: 0
model name	: ARMv7 Processor rev 5 (v7l)
Hardware	: Amlogic Meson
processor	: 1
model name	: ARMv7 Processor rev 5 (v7l)
Hardware	: Amlogic Meson
`))
	if cores != 2 {
		t.Fatalf("cores = %d, want 2", cores)
	}
	if model != "Amlogic Meson" {
		t.Fatalf("model = %q", model)
	}
}

func TestPercents(t *testing.T) {
	h := HostInfo{MemTotal: 1000, MemUsed: 250, DiskTotal: 100, DiskUsed: 50, CPUCores: 4}
	h.LoadAvg[0] = 2
	if h.MemPercent() != 25 {
		t.Fatalf("mem pct = %f", h.MemPercent())
	}
	if h.DiskPercent() != 50 {
		t.Fatalf("disk pct = %f", h.DiskPercent())
	}
	if h.CPUPercent() != 50 {
		t.Fatalf("cpu pct = %f", h.CPUPercent())
	}
}
