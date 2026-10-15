package config

import (
	"github.com/creasty/defaults"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DNSConfig.FreeBind", func() {
	It("defaults to false", func() {
		var cfg Config
		Expect(defaults.Set(&cfg)).Should(Succeed())
		Expect(cfg.DNS.FreeBind).Should(BeFalse())
	})
})

var _ = Describe("DNSConfig.ProxyProtocol", func() {
	It("defaults to no PROXY protocol listeners", func() {
		var cfg Config
		Expect(defaults.Set(&cfg)).Should(Succeed())
		Expect(cfg.DNS.ProxyProtocol).Should(BeEmpty())
		Expect(cfg.DNS.ProxyProtocol.Has(ProxyProtocolTypeDns)).Should(BeFalse())
	})

	It("reports the configured listener families via Has", func() {
		dns := DNSConfig{ProxyProtocol: ProxyProtocolListeners{ProxyProtocolTypeHttps, ProxyProtocolTypeTls}}
		Expect(dns.ProxyProtocol.Has(ProxyProtocolTypeHttps)).Should(BeTrue())
		Expect(dns.ProxyProtocol.Has(ProxyProtocolTypeTls)).Should(BeTrue())
		Expect(dns.ProxyProtocol.Has(ProxyProtocolTypeDns)).Should(BeFalse())
		Expect(dns.ProxyProtocol.Has(ProxyProtocolTypeHttp)).Should(BeFalse())
	})

	It("rejects duplicate listener families", func() {
		dns := DNSConfig{
			DOH: DOHConfig{Path: "/dns-query"},
			ProxyProtocol: ProxyProtocolListeners{
				ProxyProtocolTypeHttps,
				ProxyProtocolTypeHttps,
			},
		}

		Expect(dns.validate()).Should(MatchError(ContainSubstring(
			`dns.proxyProtocol contains duplicate listener family "https"`)))
	})

	It("rejects listener families without a configured port", func() {
		dns := DNSConfig{
			DOH:           DOHConfig{Path: "/dns-query"},
			ProxyProtocol: ProxyProtocolListeners{ProxyProtocolTypeTls},
		}

		Expect(dns.validate()).Should(MatchError(ContainSubstring(
			`dns.proxyProtocol includes "tls" but corresponding port is empty`)))
	})
})

var _ = Describe("Config.PrivilegedPorts", func() {
	It("returns privileged listen entries across DNS, HTTP, HTTPS and TLS", func() {
		cfg := Config{
			DNS: DNSConfig{
				Port: ListenConfig{":53"},
				DOT:  ListenConfig{"[::1]:853"},
				DOH:  DOHConfig{Port: ListenConfig{"127.0.0.1:443"}},
			},
			Web: WebConfig{
				Port: ListenConfig{":4000"},
			},
		}

		Expect(cfg.PrivilegedPorts()).Should(ConsistOf(":53", "127.0.0.1:443", "[::1]:853"))
	})

	It("returns empty when every configured port is unprivileged", func() {
		cfg := Config{
			DNS: DNSConfig{Port: ListenConfig{":1053"}},
			Web: WebConfig{Port: ListenConfig{":4000"}},
		}

		Expect(cfg.PrivilegedPorts()).Should(BeEmpty())
	})

	It("treats port 1023 as privileged and 1024 as unprivileged", func() {
		cfg := Config{
			DNS: DNSConfig{Port: ListenConfig{":1023"}},
			Web: WebConfig{Port: ListenConfig{":1024"}},
		}

		Expect(cfg.PrivilegedPorts()).Should(ConsistOf(":1023"))
	})

	It("handles a bare port without a colon (robustness; prefixPorts normalises these during unmarshal)", func() {
		cfg := Config{DNS: DNSConfig{Port: ListenConfig{"53"}}}

		Expect(cfg.PrivilegedPorts()).Should(ConsistOf("53"))
	})

	It("ignores empty and unparseable entries", func() {
		cfg := Config{DNS: DNSConfig{Port: ListenConfig{"", "notaport"}}}

		Expect(cfg.PrivilegedPorts()).Should(BeEmpty())
	})
	It("does not treat port 0 (OS-assigned ephemeral) as privileged", func() {
		cfg := Config{
			DNS: DNSConfig{Port: ListenConfig{":0"}},
			Web: WebConfig{Port: ListenConfig{"127.0.0.1:0"}},
		}

		Expect(cfg.PrivilegedPorts()).Should(BeEmpty())
	})
})
var _ = Describe("extractPort", func() {
	DescribeTable("parses the port from a listen address",
		func(addr string, expectedPort uint16, expectedOK bool) {
			port, ok := extractPort(addr)
			Expect(ok).Should(Equal(expectedOK))
			if expectedOK {
				Expect(port).Should(Equal(expectedPort))
			}
		},
		Entry("bare port", "53", uint16(53), true),
		Entry("colon-prefixed", ":53", uint16(53), true),
		Entry("ipv4 host", "127.0.0.1:53", uint16(53), true),
		Entry("ipv6 host", "[::1]:853", uint16(853), true),
		Entry("hostname high port", "localhost:5353", uint16(5353), true),
		Entry("empty string", "", uint16(0), false),
		Entry("non-numeric", "notaport", uint16(0), false),
	)
})
