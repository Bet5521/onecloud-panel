package system

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// parseOSRelease 解析 /etc/os-release。
func parseOSRelease(b []byte) map[string]string {
	out := map[string]string{}
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		out[k] = v
	}
	return out
}

// parseMemInfo 解析 /proc/meminfo，返回 kB 数值表。
func parseMemInfo(b []byte) map[string]int64 {
	out := map[string]int64{}
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		line := s.Text()
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(v))
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		out[k] = n
	}
	return out
}

// parseLoadAvg 解析 /proc/loadavg。
func parseLoadAvg(b []byte) [3]float64 {
	var la [3]float64
	fields := strings.Fields(string(b))
	for i := 0; i < 3 && i < len(fields); i++ {
		la[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return la
}

// parseUptime 解析 /proc/uptime（秒）。
func parseUptime(b []byte) int64 {
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	f, _ := strconv.ParseFloat(fields[0], 64)
	return int64(f)
}

// parseCPUInfo 从 /proc/cpuinfo 提取 CPU 型号与核数；优先 Hardware 字段。
func parseCPUInfo(b []byte) (string, int) {
	var model, hardware string
	cores := 0
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		line := s.Text()
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "processor" {
			cores++
		}
		if key == "Hardware" && hardware == "" {
			hardware = val
		}
		if key == "model name" && model == "" {
			model = val
		}
	}
	if hardware != "" {
		return hardware, cores
	}
	return model, cores
}
