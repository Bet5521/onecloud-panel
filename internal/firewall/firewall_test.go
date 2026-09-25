package firewall

import (
	"reflect"
	"testing"
)

func TestParseUfwStatus(t *testing.T) {
	out := `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
80/udp                     DENY IN    Anywhere
50000:50100/tcp            LIMIT IN    192.168.1.0/24
Anywhere on eth0           ALLOW IN    Anywhere
80                         ALLOW IN    Anywhere (v6)
`
	st := parseUfwStatus(out)
	if st.Backend != BackendUFW || !st.Active {
		t.Fatalf("backend/active = %s/%v", st.Backend, st.Active)
	}
	want := []Rule{
		{Port: "22", Proto: "tcp", Action: ActionAllow},
		{Port: "80", Proto: "udp", Action: ActionDeny},
		{Port: "50000:50100", Proto: "tcp", Action: ActionAllow, Source: "192.168.1.0/24"},
	}
	if !reflect.DeepEqual(st.Rules, want) {
		t.Fatalf("rules = %+v", st.Rules)
	}
}

func TestParseUfwInactive(t *testing.T) {
	st := parseUfwStatus("Status: inactive\n")
	if st.Active {
		t.Fatal("inactive 被误判为 active")
	}
	if len(st.Rules) != 0 {
		t.Fatalf("rules = %+v", st.Rules)
	}
}

func TestParseFirewalld(t *testing.T) {
	st := parseFirewalld(true, "80/tcp 443/udp 50000-50100/tcp",
		`rule family="ipv4" source address="10.0.0.0/8" port port="8080" protocol="tcp" reject
rule port port="9090" protocol="tcp" accept`)
	if st.Backend != BackendFirewalld || !st.Active {
		t.Fatalf("backend/active = %s/%v", st.Backend, st.Active)
	}
	want := []Rule{
		{Port: "80", Proto: "tcp", Action: ActionAllow},
		{Port: "443", Proto: "udp", Action: ActionAllow},
		{Port: "50000:50100", Proto: "tcp", Action: ActionAllow},
		{Port: "8080", Proto: "tcp", Action: ActionDeny, Source: "10.0.0.0/8"},
		{Port: "9090", Proto: "tcp", Action: ActionAllow},
	}
	if !reflect.DeepEqual(st.Rules, want) {
		t.Fatalf("rules = %+v", st.Rules)
	}
}

func TestParseIptables(t *testing.T) {
	out := `-P INPUT DROP
-A INPUT -m state --state RELATED,ESTABLISHED -j ACCEPT
-A INPUT -p tcp -m multiport --dports 80,443 -j ACCEPT
-A INPUT -s 10.0.0.0/8 -p udp --dport 53 -j DROP
-A INPUT -p tcp --dport 22 -j ACCEPT
`
	st := parseIptables(out)
	if st.Backend != BackendIptables || !st.Active {
		t.Fatalf("backend/active = %s/%v", st.Backend, st.Active)
	}
	want := []Rule{
		{Port: "53", Proto: "udp", Action: ActionDeny, Source: "10.0.0.0/8"},
		{Port: "22", Proto: "tcp", Action: ActionAllow},
	}
	if !reflect.DeepEqual(st.Rules, want) {
		t.Fatalf("rules = %+v", st.Rules)
	}
}

func TestParseNftables(t *testing.T) {
	out := `table inet filter {
	chain INPUT {
		type filter hook input priority 0; policy accept;
		tcp dport 22 accept
		ip saddr 10.0.0.0/8 udp dport 53 drop
		ip6 saddr fd00::/8 tcp dport 8080 accept
	}
	chain output {
		type filter hook output priority 0; policy accept;
		udp dport 53 accept
	}
}
`
	st := parseNftables(out)
	if st.Backend != BackendNftables || !st.Active {
		t.Fatalf("backend/active = %s/%v", st.Backend, st.Active)
	}
	want := []Rule{
		{Port: "22", Proto: "tcp", Action: ActionAllow},
		{Port: "53", Proto: "udp", Action: ActionDeny, Source: "10.0.0.0/8"},
		{Port: "8080", Proto: "tcp", Action: ActionAllow, Source: "fd00::/8"},
	}
	if !reflect.DeepEqual(st.Rules, want) {
		t.Fatalf("rules = %+v", st.Rules)
	}
}

func TestValidateRule(t *testing.T) {
	bad := []Rule{
		{Port: "0", Action: ActionAllow},
		{Port: "65536", Action: ActionAllow},
		{Port: "50000:5000", Action: ActionAllow},
		{Port: "abc", Action: ActionAllow},
		{Port: "80", Proto: "icmp", Action: ActionAllow},
		{Port: "80", Source: "a;b", Action: ActionAllow},
		{Port: "80", Action: "abc"},
		{Port: "80", Action: "reject"},
	}
	for _, r := range bad {
		if err := validateRule(r); err == nil {
			t.Fatalf("应拒绝: %+v", r)
		}
	}
	good := []Rule{
		{Port: "80", Proto: "tcp", Action: ActionAllow},
		{Port: "53", Proto: "", Action: ActionDeny, Source: "10.0.0.0/8"},
		{Port: "50000:50100", Proto: "udp", Action: ActionAllow},
	}
	for _, r := range good {
		if err := validateRule(r); err != nil {
			t.Fatalf("应通过: %+v, err=%v", r, err)
		}
	}
}

func TestExpandProto(t *testing.T) {
	both := expandProto(Rule{Port: "80", Action: ActionAllow, Source: "10.0.0.0/8"})
	want := []Rule{
		{Port: "80", Proto: "tcp", Action: ActionAllow, Source: "10.0.0.0/8"},
		{Port: "80", Proto: "udp", Action: ActionAllow, Source: "10.0.0.0/8"},
	}
	if !reflect.DeepEqual(both, want) {
		t.Fatalf("both = %+v", both)
	}
	single := expandProto(Rule{Port: "53", Proto: "udp", Action: ActionDeny})
	if len(single) != 1 || single[0].Proto != "udp" {
		t.Fatalf("single = %+v", single)
	}
}

func TestMatchNftHandle(t *testing.T) {
	out := `	chain INPUT { # handle 4
		tcp dport 22 accept # handle 5
		ip saddr 10.0.0.0/8 udp dport 53 drop # handle 6
	}
`
	cases := []struct {
		rule   Rule
		handle string
	}{
		{Rule{Port: "53", Proto: "udp", Action: ActionDeny, Source: "10.0.0.0/8"}, "6"},
		{Rule{Port: "53", Proto: "udp", Action: ActionDeny}, ""}, // 来源不符（行内有 saddr）
		{Rule{Port: "22", Proto: "tcp", Action: ActionAllow}, "5"},
		{Rule{Port: "22", Proto: "tcp", Action: ActionDeny}, ""},                     // 动作不符
		{Rule{Port: "22", Proto: "tcp", Action: ActionAllow, Source: "1.2.3.4"}, ""}, // 来源不符
		{Rule{Port: "443", Proto: "tcp", Action: ActionAllow}, ""},                   // 端口不符
	}
	for _, c := range cases {
		if got := matchNftHandle(out, c.rule); got != c.handle {
			t.Fatalf("matchNftHandle(%+v) = %q, want %q", c.rule, got, c.handle)
		}
	}
}
