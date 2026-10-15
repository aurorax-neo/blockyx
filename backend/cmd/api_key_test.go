package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"

	"github.com/0xERR0R/blocky/helpertest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("API key on control commands", func() {
	var (
		got string
		ts  *httptest.Server
	)

	BeforeEach(func() {
		got = "unset"
		ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("X-API-Key")
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"enabled":true}`))
			Expect(err).Should(Succeed())
		}))
		DeferCleanup(ts.Close)
		DeferCleanup(func() {
			apiKey = ""
			os.Unsetenv("BLOCKYX_API_KEY")
			os.Unsetenv(configFileEnvVar)
		})
	})

	pointAtServer := func() {
		parsed, err := url.Parse(ts.URL)
		Expect(err).Should(Succeed())
		apiHost = parsed.Hostname()
		port, err := strconv.Atoi(parsed.Port())
		Expect(err).Should(Succeed())
		apiPort = uint16(port)
	}

	It("sends api.apiKey from the config file", func() {
		file := helpertest.NewTmpFolder("cfg").CreateStringFile("config.yml",
			"api:",
			"  apiKey: from-file",
		)
		os.Setenv(configFileEnvVar, file.Path)
		os.Unsetenv("BLOCKYX_API_KEY")
		configPath = defaultConfigPath

		Expect(initConfig()).Should(Succeed())
		pointAtServer()
		Expect(statusBlocking(withContext(newBlockingCommand()), nil)).Should(Succeed())
		Expect(got).Should(Equal("from-file"))
	})

	It("prefers BLOCKYX_API_KEY over the config file", func() {
		file := helpertest.NewTmpFolder("cfg").CreateStringFile("config.yml",
			"api:",
			"  apiKey: from-file",
		)
		os.Setenv(configFileEnvVar, file.Path)
		os.Setenv("BLOCKYX_API_KEY", "from-env")
		configPath = defaultConfigPath

		Expect(initConfig()).Should(Succeed())
		pointAtServer()
		Expect(statusBlocking(withContext(newBlockingCommand()), nil)).Should(Succeed())
		Expect(got).Should(Equal("from-env"))
	})

	It("sends no key when none is configured", func() {
		file := helpertest.NewTmpFolder("cfg").CreateStringFile("config.yml",
			"log:",
			"  level: info",
		)
		os.Setenv(configFileEnvVar, file.Path)
		os.Unsetenv("BLOCKYX_API_KEY")
		configPath = defaultConfigPath

		Expect(initConfig()).Should(Succeed())
		pointAtServer()
		Expect(statusBlocking(withContext(newBlockingCommand()), nil)).Should(Succeed())
		Expect(got).Should(BeEmpty())
	})
})

var _ = Describe("persistAPIKey", func() {
	It("keeps comments and other keys", func() {
		file := helpertest.NewTmpFolder("cfg").CreateStringFile("config.yml",
			"# keep this comment",
			"upstreams:",
			"  groups:",
			"    default:",
			"      - 1.1.1.1",
		)

		Expect(persistAPIKey(file.Path, "generated-key")).Should(Succeed())

		data, err := os.ReadFile(file.Path)
		Expect(err).Should(Succeed())
		text := string(data)
		Expect(text).Should(HavePrefix("# keep this comment"))
		Expect(text).Should(ContainSubstring("1.1.1.1"))
		Expect(text).Should(ContainSubstring("apiKey: \"generated-key\""))
	})

	It("replaces an existing apiKey and keeps the rest of the file", func() {
		file := helpertest.NewTmpFolder("cfg").CreateStringFile("config.yml",
			"# header",
			"api:",
			"  apiKey: old-key",
			"  extra: keep",
			"blocking:",
			"  blockType: zeroIp",
		)

		Expect(persistAPIKey(file.Path, "new-key")).Should(Succeed())

		data, err := os.ReadFile(file.Path)
		Expect(err).Should(Succeed())
		text := string(data)
		Expect(text).Should(ContainSubstring("# header"))
		Expect(text).Should(ContainSubstring("apiKey: \"new-key\""))
		Expect(text).ShouldNot(ContainSubstring("old-key"))
		Expect(text).Should(ContainSubstring("extra: keep"))
		Expect(text).Should(ContainSubstring("blockType: zeroIp"))
	})

	It("does not write when the config path is a directory", func() {
		dir := helpertest.NewTmpFolder("cfgdir")
		before, err := os.ReadDir(dir.Path)
		Expect(err).Should(Succeed())

		Expect(persistAPIKey(dir.Path, "generated-key")).Should(HaveOccurred())

		after, err := os.ReadDir(dir.Path)
		Expect(err).Should(Succeed())
		Expect(after).Should(BeEmpty())
		Expect(before).Should(BeEmpty())
	})
})
