package netproxy

import (
	"net/url"
	"testing"
)

// ALL_PROXY, which curl reads and Go doesn't, is magpie's too when neither
// HTTPS_PROXY nor HTTP_PROXY is set: a shell with only
// ALL_PROXY=socks5://… gets magpie update through it (akic404 on Discord),
// and NO_PROXY still keeps hosts off it.
func TestAllProxyFromTheEnvironment(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
	sysCache.at, sysCache.p = farFuture, Proxy{}
	t.Cleanup(func() { sysCache.at = zero })

	t.Setenv("all_proxy", "socks5h://127.0.0.1:1080")
	t.Setenv("NO_PROXY", "internal.example, .corp.example:443")
	gh, _ := url.Parse("https://github.com/yetone/magpie-releases/releases/download/v1/magpie")
	if p, err := For(gh); err != nil || p == nil || p.String() != "socks5h://127.0.0.1:1080" {
		t.Fatalf("ALL_PROXY: %v %v", p, err)
	}
	if proxy, source := Describe(); proxy != "socks5h://127.0.0.1:1080" || source != "environment" {
		t.Fatalf("described as %q (%s)", proxy, source)
	}
	for _, raw := range []string{"https://internal.example/x", "https://api.internal.example/x", "https://git.corp.example/x", "http://127.0.0.1:3/x"} {
		u, _ := url.Parse(raw)
		if p, _ := For(u); p != nil {
			t.Errorf("%s went through %v despite NO_PROXY", raw, p)
		}
	}
	t.Setenv("NO_PROXY", "*")
	if p, _ := For(gh); p != nil {
		t.Fatalf("NO_PROXY=* still proxied: %v", p)
	}
}

// A host NO_PROXY keeps off the proxy the environment names goes direct,
// as curl and Go send it, not through the system's proxy instead: on a
// Windows with Clash in Internet Options, Settings › Proxy's Test of a
// proxy NO_PROXY passes by went to Clash. A NO_PROXY with no proxy named
// beside it leaves the system's to decide.
func TestNoProxyGoesDirectNotThroughTheSystems(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
	sysCache.at, sysCache.p = farFuture, Proxy{URL: "http://127.0.0.1:7890"}
	t.Cleanup(func() { sysCache.at = zero })
	chat, _ := url.Parse("https://chatgpt.com/backend-api")
	other, _ := url.Parse("https://vendor.example/v1")

	t.Setenv("NO_PROXY", "chatgpt.com")
	if p, _ := For(chat); p == nil || p.String() != "http://127.0.0.1:7890" {
		t.Fatalf("NO_PROXY alone: %v, want the system's", p)
	}

	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1080")
	if p, _ := For(chat); p != nil {
		t.Fatalf("ALL_PROXY passed by: %v, want direct", p)
	}
	if proxy, source := Describe(); proxy != "" || source != "none" {
		t.Fatalf("described as %q (%s), want direct", proxy, source)
	}
	if p, _ := For(other); p == nil || p.String() != "socks5://127.0.0.1:1080" {
		t.Fatalf("a host NO_PROXY doesn't name: %v", p)
	}

	// HTTPS_PROXY names one for https alone: http is still the system's
	t.Setenv("ALL_PROXY", "")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8080")
	if p, _ := For(chat); p != nil {
		t.Fatalf("HTTPS_PROXY passed by: %v, want direct", p)
	}
	plain, _ := url.Parse("http://chatgpt.com/x")
	if p, _ := For(plain); p == nil || p.String() != "http://127.0.0.1:7890" {
		t.Fatalf("http with only HTTPS_PROXY: %v, want the system's", p)
	}
}

func TestNoProxyList(t *testing.T) {
	for _, c := range []struct {
		host, list string
		want       bool
	}{
		{"github.com", "", false},
		{"github.com", "github.com", true},
		{"objects.github.com", "github.com", true},
		{"notgithub.com", "github.com", false},
		{"api.example.com", "*.example.com", true},
		{"example.com", ".example.com", true},
		{"example.com", "example.com:443", true},
		{"localhost", "", true},
	} {
		if got := noProxy(c.host, c.list); got != c.want {
			t.Errorf("noProxy(%q, %q) = %v, want %v", c.host, c.list, got, c.want)
		}
	}
}
