package node

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in    string
		want  [3]int
		isErr bool
	}{
		{"1.2.3", [3]int{1, 2, 3}, false},
		{"v1.2.3", [3]int{1, 2, 3}, false},
		{"V2.0.0", [3]int{2, 0, 0}, false},
		{"1.2", [3]int{1, 2, 0}, false},
		{"3", [3]int{3, 0, 0}, false},
		{"1.2.3-rc1", [3]int{1, 2, 3}, false},
		{"1.2.3+gitabc", [3]int{1, 2, 3}, false},
		{"dev", [3]int{}, true},
		{"", [3]int{}, true},
	}
	for _, c := range cases {
		got, err := parseVersion(c.in)
		if c.isErr {
			if err == nil {
				t.Errorf("parseVersion(%q) 期望报错，实际得到 %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseVersion(%q) 意外报错: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseVersion(%q) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

func TestIsOlder(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"1.2.3", "1.3.0", true},
		{"1.2.3", "2.0.0", true},
		{"1.2", "1.2.1", true},
		{"v1.2.4", "v1.2.3", false},
		{"1.3.0", "1.2.3", false},
		{"2.0.0", "1.9.9", false},
		{"1.2.3", "1.2.3", false},
		{"dev", "1.2.3", false},
		{"1.2.3", "dev", false},
		{"", "1.2.3", false},
	}
	for _, c := range cases {
		if got := isOlder(c.a, c.b); got != c.want {
			t.Errorf("isOlder(%q, %q) = %v, 期望 %v", c.a, c.b, got, c.want)
		}
	}
}
