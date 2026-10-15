package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0xERR0R/blocky/config"
	"github.com/0xERR0R/blocky/evt"
	"github.com/0xERR0R/blocky/log"
	"github.com/0xERR0R/blocky/server"
	"github.com/0xERR0R/blocky/util"

	"github.com/spf13/cobra"
)

//nolint:gochecknoglobals
var (
	done              = make(chan bool, 1)
	isConfigMandatory = true
	signals           = make(chan os.Signal, 1)

	// raiseNetBindService is a seam so tests can stub the capability raise.
	raiseNetBindService = util.RaiseNetBindService
)

const shutdownTimeout = 10 * time.Second

func newServeCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "serve",
		Args:              cobra.NoArgs,
		Short:             "start blocky DNS server (default command)",
		RunE:              startServer,
		PersistentPreRunE: initConfigPreRun,
		SilenceUsage:      true,
	}
}

// privilegedPortCapHint describes how to satisfy the CAP_NET_BIND_SERVICE
// requirement for binding ports below 1024.
const privilegedPortCapHint = "grant CAP_NET_BIND_SERVICE (Kubernetes " +
	"securityContext capabilities.add, or docker run --cap-add NET_BIND_SERVICE) " +
	"or use a port >= 1024"

// warnMissingPrivilegedPortCapability raises CAP_NET_BIND_SERVICE if it is
// available, and warns when a privileged port (< 1024) is configured but the
// capability could not be obtained. It never fails: any real bind error
// surfaces later from server.NewServer.
// generateAPIKey creates a random 32-byte API key, base64-encoded.
func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// persistAPIKey writes key into the single config file without reordering or
// dropping the rest of the file, including comments. A directory, a missing
// path, or a file that cannot be updated is left unchanged.
func persistAPIKey(configPath, key string) error {
	info, err := os.Stat(configPath)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return fmt.Errorf("config path %s is a directory", configPath)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	updated := insertAPIKey(data, key)

	return os.WriteFile(configPath, updated, info.Mode().Perm())
}

// insertAPIKey sets api.apiKey in a YAML document. Other lines stay in place.
func insertAPIKey(data []byte, key string) []byte {
	newline := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		newline = "\r\n"
	}

	text := string(data)
	trailingNewline := text == "" || strings.HasSuffix(text, newline)
	lines := strings.Split(text, newline)
	quoted := strconv.Quote(key)

	apiAt := -1

	for i, line := range lines {
		if yamlKey(line) == "api" {
			apiAt = i

			break
		}
	}

	if apiAt < 0 {
		lines = appendYAML(lines, trailingNewline, "api:", "  apiKey: "+quoted)

		return joinYAML(lines, newline, trailingNewline || text == "")
	}

	sectionEnd := len(lines)
	childIndent := ""
	keyAt := -1

	for i := apiAt + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := yamlIndent(lines[i])
		if len(indent) <= len(yamlIndent(lines[apiAt])) {
			sectionEnd = i

			break
		}

		if childIndent == "" {
			childIndent = indent
		}

		if yamlKey(lines[i]) == "apiKey" {
			keyAt = i
			childIndent = indent

			break
		}
	}

	if childIndent == "" {
		childIndent = yamlIndent(lines[apiAt]) + "  "
	}

	replacement := childIndent + "apiKey: " + quoted
	if keyAt >= 0 {
		lines[keyAt] = replacement
	} else {
		lines = insertYAMLLine(lines, apiAt+1, replacement)
		_ = sectionEnd
	}

	return joinYAML(lines, newline, trailingNewline)
}

func yamlIndent(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

func yamlKey(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}

	name, _, found := strings.Cut(trimmed, ":")
	if !found || name == "" || strings.ContainsAny(name, " \t#") {
		return ""
	}

	return name
}

func insertYAMLLine(lines []string, index int, line string) []string {
	if index < 0 || index > len(lines) {
		return append(lines, line)
	}

	lines = append(lines, "")
	copy(lines[index+1:], lines[index:])
	lines[index] = line

	return lines
}

func appendYAML(lines []string, trailing bool, section ...string) []string {
	if trailing && len(lines) > 0 && lines[len(lines)-1] == "" {
		head := append([]string{}, lines[:len(lines)-1]...)
		head = append(head, section...)

		return append(head, "")
	}

	return append(lines, section...)
}

func joinYAML(lines []string, newline string, trailing bool) []byte {
	out := strings.Join(lines, newline)
	if trailing && !strings.HasSuffix(out, newline) {
		out += newline
	}

	return []byte(out)
}

func warnMissingPrivilegedPortCapability(cfg *config.Config) {
	effective, err := raiseNetBindService()
	if err != nil {
		if privileged := cfg.PrivilegedPorts(); len(privileged) > 0 {
			log.Log().Warnf("could not adjust process capabilities (%v); binding "+
				"privileged port(s) %s may fail — %s",
				err, strings.Join(privileged, ", "), privilegedPortCapHint)

			return
		}

		log.Log().Warnf("could not adjust process capabilities: %v", err)

		return
	}

	if effective {
		return
	}

	if privileged := cfg.PrivilegedPorts(); len(privileged) > 0 {
		log.Log().Warnf("configured to listen on privileged port(s) %s without "+
			"CAP_NET_BIND_SERVICE; %s", strings.Join(privileged, ", "), privilegedPortCapHint)
	}
}

func startServer(_ *cobra.Command, _ []string) error {
	printBanner()

	cfg, err := config.LoadConfig(configPath, isConfigMandatory)
	if err != nil {
		return fmt.Errorf("unable to load configuration: %w", err)
	}

	// BLOCKYX_API_KEY takes precedence over the api.apiKey config file value.
	if apiKey := os.Getenv("BLOCKYX_API_KEY"); apiKey != "" {
		cfg.API.APIKey = config.Secret(apiKey)
	}

	// Security requirement: the server must never run without an API key.
	// Generate a random one when none is configured, persist it to the
	// config file so it survives restarts, and print it to stderr.
	if cfg.API.APIKey.Reveal() == "" {
		key, err := generateAPIKey()
		if err != nil {
			return fmt.Errorf("failed to generate API key: %w", err)
		}

		cfg.API.APIKey = config.Secret(key)

		if err := persistAPIKey(configPath, key); err != nil {
			fmt.Fprintf(os.Stderr, "blockyx: no API key configured, generated one: %s\n", key)
			fmt.Fprintf(os.Stderr, "blockyx: could not persist to config file (%v); this key is valid for this run only\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "blockyx: no API key configured, generated one and saved to %s: %s\n", configPath, key)
		}

		fmt.Fprintf(os.Stderr, "blockyx: set api.apiKey in config or BLOCKYX_API_KEY env var to use your own\n")
	}

	log.Configure(&cfg.Log)

	warnMissingPrivilegedPortCapability(cfg)

	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancelFn := context.WithCancel(context.Background())
	defer cancelFn()

	srv, err := server.NewServer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("can't start server: %w", err)
	}

	const errChanSize = 10
	errChan := make(chan error, errChanSize)

	srv.Start(ctx, errChan)

	var terminationErr error

	go func() {
		select {
		case <-signals:
			log.Log().Infof("Terminating...")

			// Cancel background operations (periodic refresh, etc.)
			cancelFn()

			// Create timeout context for graceful shutdown
			stopCtx, stopCancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer stopCancel()

			util.LogOnError(stopCtx, "can't stop server: ", srv.Stop(stopCtx))
			done <- true

		case err := <-errChan:
			log.Log().Error("server start failed: ", err)
			terminationErr = err
			done <- true
		}
	}()

	evt.Bus().Publish(evt.ApplicationStarted, util.Version, util.BuildTime)
	<-done

	return terminationErr
}

func printBanner() {
	log.Log().Info("_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/")
	log.Log().Info("_/                                                              _/")
	log.Log().Info("_/                                                              _/")
	log.Log().Info("_/       _/        _/                      _/                   _/")
	log.Log().Info("_/      _/_/_/    _/    _/_/      _/_/_/  _/  _/    _/    _/    _/")
	log.Log().Info("_/     _/    _/  _/  _/    _/  _/        _/_/      _/    _/     _/")
	log.Log().Info("_/    _/    _/  _/  _/    _/  _/        _/  _/    _/    _/      _/")
	log.Log().Info("_/   _/_/_/    _/    _/_/      _/_/_/  _/    _/    _/_/_/       _/")
	log.Log().Info("_/                                                    _/        _/")
	log.Log().Info("_/                                               _/_/           _/")
	log.Log().Info("_/                                                              _/")
	log.Log().Info("_/                                                              _/")
	log.Log().Infof("_/  Version: %-18s Build time: %-18s  _/", util.Version, util.BuildTime)
	log.Log().Info("_/                                                              _/")
	log.Log().Info("_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/_/")
}
