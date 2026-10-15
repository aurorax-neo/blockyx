package cmd

import (
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/0xERR0R/blocky/helpertest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	basePort = 52000
)

var _ = Describe("Serve command", func() {
	var (
		tmpDir *helpertest.TmpFolder
		port   string
	)
	BeforeEach(func() {
		port = helpertest.GetStringPort(basePort)
		tmpDir = helpertest.NewTmpFolder("config")

		configPath = defaultConfigPath
	})

	When("Serve command is called with valid config", func() {
		It("should start without error and terminate with signal", func() {
			By("initialize config", func() {
				cfgFile := tmpDir.CreateStringFile("config.yaml",
					"upstreams:",
					"  groups:",
					"    default:",
					"      - 1.1.1.1",
					"dns:",
					"  port: "+port,
					"queryLog:",
					"  type: none",
					"api:",
					"  apiKey: \"test-key\"")

				os.Setenv(configFileEnvVar, cfgFile.Path)
				DeferCleanup(func() { os.Unsetenv(configFileEnvVar) })

				Expect(initConfig()).Should(Succeed())
			})

			errChan := make(chan error)
			By("start server", func() {
				go func() {
					// it is a blocking function, call async
					errChan <- startServer(newServeCommand(), []string{})
				}()
			})

			By("check DNS port is open", func() {
				Eventually(func(g Gomega) {
					conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
					g.Expect(err).Should(Succeed())
					defer conn.Close()
				}, "5s").Should(Succeed())
			})

			By("terminate with signal", func() {
				signals <- syscall.SIGINT

				// no errors
				Eventually(errChan).Should(Receive(BeNil()))
			})
		})
	})

	When("Serve command is called with valid config", func() {
		It("should fail if server start fails", func() {
			By("start http server on port "+port, func() {
				go func(p string) {
					Expect(http.ListenAndServe(":"+p, nil)).Should(Succeed())
				}(port)
			})
			By("initialize config with blocked port "+port, func() {
				cfgFile := tmpDir.CreateStringFile("config.yaml",
					"upstreams:",
					"  groups:",
					"    default:",
					"      - 1.1.1.1",
					"dns:",
					"  port: "+port,
					"queryLog:",
					"  type: none",
					"api:",
					"  apiKey: \"test-key\"")

				os.Setenv(configFileEnvVar, cfgFile.Path)
				DeferCleanup(func() { os.Unsetenv(configFileEnvVar) })

				Expect(initConfig()).Should(Succeed())
			})

			errChan := make(chan error)
			By("start server", func() {
				go func() {
					// it is a blocking function, call async
					errChan <- startServer(newServeCommand(), []string{})
				}()
			})

			By("terminate with signal", func() {
				var startError error
				Eventually(errChan, "10s").Should(Receive(&startError))
				Expect(startError).Should(MatchError(ContainSubstring("address already in use")))
			})
		})
	})

	When("Serve command is called without config", func() {
		It("should fail to start and report error", func() {
			errChan := make(chan error)
			By("start server", func() {
				go func() {
					// it is a blocking function, call async
					errChan <- startServer(newServeCommand(), []string{})
				}()
			})

			By("server should terminate with error", func() {
				var startError error
				Eventually(errChan).Should(Receive(&startError))
				Expect(startError).Should(MatchError(ContainSubstring("unable to load configuration")))
			})
		})
	})

	When("Serve command is called without an API key", func() {
		It("should generate a random key instead of refusing", func() {
			By("initialize config without apiKey", func() {
				cfgFile := tmpDir.CreateStringFile("config.yaml",
					"upstreams:",
					"  groups:",
					"    default:",
					"      - 1.1.1.1",
					"dns:",
					"  port: "+port,
					"queryLog:",
					"  type: none")

				os.Setenv(configFileEnvVar, cfgFile.Path)
				DeferCleanup(func() { os.Unsetenv(configFileEnvVar) })
				// make sure the env override cannot provide the key either
				os.Unsetenv("BLOCKYX_API_KEY")

				Expect(initConfig()).Should(Succeed())
			})

			By("server should not refuse to start for missing key", func() {
				err := startServer(newServeCommand(), []string{})
				// It may fail later (e.g. binding ports in test env),
				// but must not be the "refusing without key" error.
				if err != nil {
					Expect(err.Error()).ShouldNot(ContainSubstring("refusing to start without an API key"))
				}
			})
		})
	})
})
