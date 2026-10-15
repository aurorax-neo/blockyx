package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/0xERR0R/blocky/cache"
	"github.com/0xERR0R/blocky/config"
	"github.com/0xERR0R/blocky/log"
	"github.com/0xERR0R/blocky/metrics"
	"github.com/0xERR0R/blocky/model"
	"github.com/0xERR0R/blocky/redis"
	"github.com/0xERR0R/blocky/resolver"
	"github.com/0xERR0R/blocky/server/freebind"

	"github.com/0xERR0R/blocky/util"
	goredis "github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/hashicorp/go-multierror"

	"github.com/go-chi/chi/v5"
	"github.com/miekg/dns"
	"github.com/pires/go-proxyproto"
	"github.com/quic-go/quic-go"
	"github.com/sirupsen/logrus"
)

const (
	maxUDPBufferSize = 65535
	caExpiryYears    = 10
	certExpiryYears  = 5

	networkUDP    = "udp"
	networkTCP    = "tcp"
	networkTCPTLS = "tcp-tls"
)

// Server controls the endpoints for DNS and HTTP
type Server struct {
	dnsServers    []*dns.Server
	queryResolver resolver.ChainedResolver
	cfg           *config.Config

	servers          map[net.Listener]*httpServer
	http3Server      *http3Server     // nil when disabled
	http3PacketConns []net.PacketConn // one per address in ports.https
	closers          []io.Closer
	redisCancel      context.CancelFunc
}

func logger() *logrus.Entry {
	return log.PrefixedLog("server")
}

func tlsCipherSuites() []uint16 {
	tlsCipherSuites := []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
	}

	return tlsCipherSuites
}

type NewServerFunc func(address string) (*dns.Server, error)

func retrieveCertificate(cfg *config.Config) (cert tls.Certificate, err error) {
	if cfg.CertFile == "" && cfg.KeyFile == "" {
		cert, err = util.TLSGenerateSelfSignedCert([]string{"blocky.invalid", "*"})
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("unable to generate self-signed certificate: %w", err)
		}

		log.Log().Info("using self-signed certificate")
	} else {
		cert, err = tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("can't load certificate files: %w", err)
		}
	}

	return cert, nil
}

func newTLSConfig(cfg *config.Config) (*tls.Config, error) {
	var cert tls.Certificate

	cert, err := retrieveCertificate(cfg)
	if err != nil {
		return nil, fmt.Errorf("can't retrieve cert: %w", err)
	}

	// #nosec G402 // See TLSVersion.validate
	res := &tls.Config{
		MinVersion:   uint16(cfg.MinTLSServeVer), //nolint:gosec // TLS version constants fit safely in uint16
		CipherSuites: tlsCipherSuites(),
		Certificates: []tls.Certificate{cert},
	}

	return res, nil
}

// NewServer creates new server instance with passed config
//
//nolint:funlen
func NewServer(ctx context.Context, cfg *config.Config) (server *Server, err error) {
	var tlsCfg *tls.Config
	needsTLS := cfg.Web.TLS || len(cfg.DNS.DOT) > 0 || (cfg.DNS.DOH.TLS && len(cfg.DNS.DOH.Port) > 0)
	if needsTLS {
		tlsCfg, err = newTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS configuration: %w", err)
		}
	}

	if cfg.DNS.FreeBind && !freebind.Supported {
		logger().Warn("dns.freeBind: true is only supported on Linux; " +
			"ignoring on this platform (binding normally)")
	}

	dnsServers, err := createServers(ctx, cfg, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("server creation failed: %w", err)
	}

	listeners, err := createHTTPAndDOHListeners(ctx, cfg, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP/DoH listeners: %w", err)
	}

	metrics.RegisterEventListeners()

	bootstrap, err := resolver.NewBootstrap(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create bootstrap resolver: %w", err)
	}

	var (
		redisConn      *goredis.Client
		redisConnected bool
		redisCancel    context.CancelFunc
		redisClosers   []io.Closer
	)

	redisCtx := ctx
	if cfg.Redis.IsEnabled() {
		redisCtx, redisCancel = context.WithCancel(ctx)
		defer func() {
			if err != nil {
				redisCancel()
				closeAll(redisClosers)
			}
		}()

		redisConn, redisConnected, err = createRedisClient(redisCtx, &cfg.Redis)
		if err != nil {
			return nil, err
		}

		redisClosers = append(redisClosers, redisConn)
	}

	addRedisCloser := func(c io.Closer) { redisClosers = append([]io.Closer{c}, redisClosers...) }

	redisResult, err := createRedisCacheDecorator(redisCtx, redisConn, cfg.Redis.Required, redisConnected, addRedisCloser)
	if err != nil {
		return nil, err
	}

	if redisResult.bridge != nil {
		addRedisCloser(redisResult.bridge)
	}

	queryResolver, queryError := createQueryResolver(ctx, cfg, bootstrap, redisResult.decorator)
	if queryError != nil {
		return nil, queryError
	}

	// Flush the statistics buckets synchronously on shutdown so the dashboard
	// data is persisted even though the consumer goroutine stops asynchronously.
	// Only registered when persistence is enabled, so a disabled subsystem
	// holds no resources.
	closers := redisClosers
	if sr, err := resolver.GetFromChainWithType[*resolver.StatsResolver](queryResolver); err == nil && sr.Closeable() {
		closers = append([]io.Closer{sr}, closers...)
	}

	server = &Server{
		dnsServers:       dnsServers,
		queryResolver:    queryResolver,
		cfg:              cfg,
		servers:          make(map[net.Listener]*httpServer),
		http3PacketConns: listeners.http3PacketConns,
		redisCancel:      redisCancel,
		closers:          closers,
	}

	server.printConfiguration()

	server.registerDNSHandlers(ctx)

	openAPIImpl, err := server.createOpenAPIInterfaceImpl()
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAPI interface implementation: %w", err)
	}

	// 1. Dedicated Web UI & Management API Server (ports.web)
	if len(listeners.uiListeners) > 0 {
		srvName := "http"
		if listeners.uiTLS {
			srvName = "https"
		}
		uiRouter := createHTTPRouter(cfg, openAPIImpl)
		srv := newHTTPServer(srvName, uiRouter, cfg)

		for _, l := range listeners.uiListeners {
			server.servers[l] = srv
		}
	}

	// 2. Dedicated DoH DNS Resolver Server (ports.dns.doh)
	if len(listeners.dohListeners) > 0 {
		dohRouter := chi.NewRouter()
		server.registerDoHEndpoints(dohRouter, cfg)

		var dohHandler http.Handler = dohRouter
		if len(listeners.http3PacketConns) > 0 {
			server.http3Server = newHTTP3Server(dohRouter, newH3TLSConfig(tlsCfg))
			dohHandler = newAltSvcMiddleware(server.http3Server)(dohRouter)
		}

		srvName := "doh-http"
		if listeners.dohTLS {
			srvName = "doh-https"
		}
		srv := newHTTPServer(srvName, dohHandler, cfg)

		for _, l := range listeners.dohListeners {
			server.servers[l] = srv
		}
	}

	return server, err
}

func createServers(ctx context.Context, cfg *config.Config, tlsCfg *tls.Config) ([]*dns.Server, error) {
	var dnsServers []*dns.Server

	var err *multierror.Error

	freeBind := cfg.DNS.FreeBind
	addServers := func(newServer NewServerFunc, addresses config.ListenConfig) error {
		for _, address := range addresses {
			server, err := newServer(address)
			if err != nil {
				return err
			}

			dnsServers = append(dnsServers, server)
		}

		return nil
	}

	err = multierror.Append(err,
		addServers(func(address string) (*dns.Server, error) {
			return createUDPServer(ctx, address, listenerOptions{freeBind: freeBind})
		}, cfg.DNS.Port),
		addServers(func(address string) (*dns.Server, error) {
			return createTCPServer(ctx, address, listenerOptions{
				freeBind:      freeBind,
				proxyProtocol: cfg.DNS.ProxyProtocol.Has(config.ProxyProtocolTypeDns),
			})
		}, cfg.DNS.Port),
		addServers(func(address string) (*dns.Server, error) {
			return createTLSServer(ctx, address, tlsCfg, listenerOptions{
				freeBind:      freeBind,
				proxyProtocol: cfg.DNS.ProxyProtocol.Has(config.ProxyProtocolTypeTls),
			})
		}, cfg.DNS.DOT))

	if multiErr := err.ErrorOrNil(); multiErr != nil {
		return nil, fmt.Errorf("failed to create DNS servers: %w", multiErr)
	}

	return dnsServers, nil
}

type webDOHListeners struct {
	uiListeners      []net.Listener
	uiTLS            bool
	dohListeners     []net.Listener
	dohTLS           bool
	http3PacketConns []net.PacketConn
}

func createHTTPAndDOHListeners(
	ctx context.Context, cfg *config.Config, tlsCfg *tls.Config,
) (*webDOHListeners, error) {
	res := &webDOHListeners{
		uiTLS:  cfg.Web.TLS,
		dohTLS: cfg.DNS.DOH.TLS,
	}

	// 1. Web UI / API listeners
	if len(cfg.Web.Port) > 0 {
		var err error
		if cfg.Web.TLS {
			res.uiListeners, err = newTLSListeners(ctx, "https", cfg.Web.Port, tlsCfg,
				cfg.DNS.ProxyProtocol.Has(config.ProxyProtocolTypeHttp))
		} else {
			res.uiListeners, err = newTCPListeners(ctx, "http", cfg.Web.Port,
				cfg.DNS.ProxyProtocol.Has(config.ProxyProtocolTypeHttp))
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create Web UI listener: %w", err)
		}
	}

	// 2. DoH listeners
	if len(cfg.DNS.DOH.Port) > 0 {
		var err error
		if cfg.DNS.DOH.TLS {
			res.dohListeners, err = newTLSListeners(ctx, "https", cfg.DNS.DOH.Port, tlsCfg,
				cfg.DNS.ProxyProtocol.Has(config.ProxyProtocolTypeHttps))
		} else {
			res.dohListeners, err = newTCPListeners(ctx, "http", cfg.DNS.DOH.Port,
				cfg.DNS.ProxyProtocol.Has(config.ProxyProtocolTypeHttps) || cfg.DNS.ProxyProtocol.Has(config.ProxyProtocolTypeHttp))
		}
		if err != nil {
			closeAll(res.uiListeners)

			return nil, fmt.Errorf("failed to create DoH listener: %w", err)
		}
	}

	h3Enabled := cfg.DNS.DOH.H3 || cfg.HTTP3.IsEnabled()
	if h3Enabled {
		switch {
		case len(cfg.DNS.DOH.Port) == 0 || !cfg.DNS.DOH.TLS:
			logger().Warn("h3 is enabled but dns.doh is not configured with TLS; HTTP/3 disabled")
		case cfg.DNS.ProxyProtocol.Has(config.ProxyProtocolTypeHttps):
			logger().Warn("h3 is enabled but dns.proxyProtocol includes 'https'; " +
				"HTTP/3 cannot carry PROXY protocol headers and is disabled to keep the client IP consistent")
		default:
			var err error
			res.http3PacketConns, err = newUDPPacketConns(ctx, cfg.DNS.DOH.Port)
			if err != nil {
				closeAll(res.uiListeners)
				closeAll(res.dohListeners)

				return nil, fmt.Errorf("failed to create HTTP/3 UDP listeners: %w", err)
			}
		}
	}

	return res, nil
}

func closeAll[T io.Closer](closers []T) {
	for _, c := range closers {
		_ = c.Close()
	}
}

func newTCPListeners(
	ctx context.Context, proto string, addresses config.ListenConfig, proxyProtocol bool,
) ([]net.Listener, error) {
	listeners := make([]net.Listener, 0, len(addresses))
	lc := &net.ListenConfig{}

	for _, address := range addresses {
		listener, err := lc.Listen(ctx, networkTCP, address)
		if err != nil {
			return nil, fmt.Errorf("start %s listener on %s failed: %w", proto, address, err)
		}

		listener = newProxyProtocolListener(listener, proxyProtocol)

		listeners = append(listeners, listener)
	}

	return listeners, nil
}

func newTLSListeners(
	ctx context.Context, proto string, addresses config.ListenConfig, tlsCfg *tls.Config, proxyProtocol bool,
) ([]net.Listener, error) {
	listeners, err := newTCPListeners(ctx, proto, addresses, proxyProtocol)
	if err != nil {
		return nil, fmt.Errorf("failed to create TCP listeners for TLS: %w", err)
	}

	for i, inner := range listeners {
		listeners[i] = tls.NewListener(inner, tlsCfg)
	}

	return listeners, nil
}

func newProxyProtocolListener(listener net.Listener, enabled bool) net.Listener {
	if !enabled {
		return listener
	}

	return &proxyproto.Listener{
		Listener: listener,
		ConnPolicy: func(proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
			return proxyproto.REQUIRE, nil
		},
	}
}

// listenerOptions bundles the socket-level options applied when a DNS listener is pre-created
// before miekg/dns starts serving (freebind socket option, PROXY protocol wrapping).
type listenerOptions struct {
	freeBind      bool
	proxyProtocol bool
}

func createDNSServer(ctx context.Context, network, address string, tlsCfg *tls.Config, opts listenerOptions,
) (*dns.Server, error) {
	srv := &dns.Server{
		Addr:    address,
		Net:     network,
		Handler: dns.NewServeMux(),
		NotifyStartedFunc: func() {
			logger().Infof("%s server is up and running on address %s", strings.ToUpper(network), address)
		},
	}

	if network == networkUDP {
		srv.UDPSize = maxUDPBufferSize
	}

	if tlsCfg != nil {
		srv.TLSConfig = tlsCfg
	}

	// When freeBind is enabled (and supported), pre-create the listener with the IP_FREEBIND socket
	// option and hand it to the server, which is then started via ActivateAndServe (see Server.Start).
	if (opts.freeBind && freebind.Supported) || (opts.proxyProtocol && network != networkUDP) {
		if err := attachListener(ctx, srv, network, address, tlsCfg, listenerOptions{
			freeBind:      opts.freeBind && freebind.Supported,
			proxyProtocol: opts.proxyProtocol,
		}); err != nil {
			return nil, err
		}
	}

	return srv, nil
}

// attachListener creates a listener/packet connection for DNS servers that need custom socket handling
// before miekg/dns starts serving, such as freebind or PROXY protocol wrapping.
func attachListener(ctx context.Context, srv *dns.Server, network, address string,
	tlsCfg *tls.Config, opts listenerOptions,
) error {
	lc := net.ListenConfig{}
	if opts.freeBind {
		lc.Control = freebind.Control
	}

	switch network {
	case networkUDP:
		pc, err := lc.ListenPacket(ctx, networkUDP, address)
		if err != nil {
			return fmt.Errorf("freebind udp listener on %s failed: %w", address, err)
		}

		srv.PacketConn = pc
	case networkTCP:
		l, err := lc.Listen(ctx, networkTCP, address)
		if err != nil {
			return fmt.Errorf("tcp listener on %s failed: %w", address, err)
		}

		l = newProxyProtocolListener(l, opts.proxyProtocol)
		srv.Listener = l
	case networkTCPTLS:
		l, err := lc.Listen(ctx, networkTCP, address)
		if err != nil {
			return fmt.Errorf("tcp-tls listener on %s failed: %w", address, err)
		}

		l = newProxyProtocolListener(l, opts.proxyProtocol)
		srv.Listener = tls.NewListener(l, tlsCfg)
	default:
		return fmt.Errorf("unsupported DNS listener network %q", network)
	}

	return nil
}

func createTLSServer(ctx context.Context, address string, tlsCfg *tls.Config, opts listenerOptions,
) (*dns.Server, error) {
	return createDNSServer(ctx, networkTCPTLS, address, tlsCfg, opts)
}

func createTCPServer(ctx context.Context, address string, opts listenerOptions) (*dns.Server, error) {
	return createDNSServer(ctx, networkTCP, address, nil, opts)
}

func createUDPServer(ctx context.Context, address string, opts listenerOptions) (*dns.Server, error) {
	return createDNSServer(ctx, networkUDP, address, nil, opts)
}

// createRedisClient keeps an optional client whose startup Ping failed so it can reconnect.
func createRedisClient(ctx context.Context, cfg *config.Redis) (client *goredis.Client, connected bool, err error) {
	client, err = redis.New(ctx, cfg)
	if err == nil {
		return client, true, nil
	}

	if cfg.Required {
		if client != nil {
			_ = client.Close()
		}

		return nil, false, fmt.Errorf("failed to create required Redis client: %w", err)
	}

	logger().WithError(err).Warn("Redis is optional and unavailable; continuing with reconnection enabled")

	return client, false, nil
}

type redisBridgeResult struct {
	decorator resolver.CacheDecorator
	bridge    *redis.EventBusBridge
}

func createRedisCacheDecorator(
	ctx context.Context, redisConn *goredis.Client, required, connected bool, addCloser func(io.Closer),
) (*redisBridgeResult, error) {
	if redisConn == nil {
		return &redisBridgeResult{}, nil
	}

	bridge, err := redis.NewEventBusBridgeWithOptions(ctx, redisConn, redis.EventBusBridgeOptions{
		BackgroundConnect: !required,
	})
	if err != nil {
		if required {
			return nil, fmt.Errorf("failed to create required Redis event bridge: %w", err)
		}

		logger().Warn("failed to create Redis event bridge: ", err)
	}

	decorator := func(inner cache.ExpiringCache[[]byte]) (cache.ExpiringCache[[]byte], error) {
		redisCache, err := cache.NewRedisExpiringByteCache(ctx, inner, redisConn, cache.RedisOptions[[]byte]{
			Prefix:          "blocky:cache:",
			Channel:         "blocky_cache_sync",
			SkipInitialLoad: !connected,
		})
		if err != nil {
			return nil, err
		}

		addCloser(redisCache)

		return redisCache, nil
	}

	return &redisBridgeResult{decorator: decorator, bridge: bridge}, nil
}

func createQueryResolver(
	ctx context.Context,
	cfg *config.Config,
	bootstrap *resolver.Bootstrap,
	cacheDecorator resolver.CacheDecorator,
) (resolver.ChainedResolver, error) {
	upstreamTree, utErr := resolver.NewUpstreamTreeResolver(ctx, cfg.Upstreams, bootstrap)
	blocking, blErr := resolver.NewBlockingResolver(ctx, cfg.Blocking, bootstrap)
	queryLogging, qlErr := resolver.NewQueryLoggingResolver(ctx, cfg.QueryLog)
	condUpstream, cuErr := resolver.NewConditionalUpstreamResolver(ctx, cfg.Conditional, cfg.Upstreams, bootstrap)
	customDNS := resolver.NewCustomDNSResolver(cfg.CustomDNS)
	hostsFile, hfErr := resolver.NewHostsFileResolver(ctx, cfg.HostsFile, bootstrap)
	// client name resolution consults local reverse sources (custom DNS, hosts file) before the rDNS upstream
	clientNames, cnErr := resolver.NewClientNamesResolver(
		ctx, cfg.ClientLookup, cfg.Upstreams, bootstrap, customDNS, hostsFile)
	decorator := cacheDecorator
	if !cfg.Caching.IsEnabled() {
		decorator = nil
	}

	// cfg.DNSSEC gates the DO bit on prefetch reloads (they bypass the DNSSEC
	// resolver above the cache).
	cachingResolver, crErr := resolver.NewCachingResolver(ctx, cfg.Caching, cfg.DNSSEC, decorator)
	// Pass upstreamTree to DNSSEC resolver so it can query for DNSKEY/DS records
	dnssecResolver, dsErr := resolver.NewDNSSECResolver(ctx, cfg.DNSSEC, upstreamTree)

	multiErr := multierror.Append(
		multierror.Prefix(utErr, "upstream tree resolver: "),
		multierror.Prefix(blErr, "blocking resolver: "),
		multierror.Prefix(qlErr, "query logging resolver: "),
		multierror.Prefix(cnErr, "client names resolver: "),
		multierror.Prefix(cuErr, "conditional upstream resolver: "),
		multierror.Prefix(hfErr, "hosts file resolver: "),
		multierror.Prefix(crErr, "caching resolver: "),
		multierror.Prefix(dsErr, "dnssec resolver: "),
	).ErrorOrNil()
	if multiErr != nil {
		return nil, fmt.Errorf("failed to create query resolver components: %w", multiErr)
	}

	r := resolver.Chain(
		resolver.NewStatsResolver(ctx, cfg.Statistics),
		// stays above the ECS and client-name lookups: its bucket key must remain the
		// connection's source IP. Keyed on the ECS address instead (ecs.useAsClient), the
		// key would be attacker-controlled, letting a client both evade its own bucket and
		// fill the bounded bucket store, which drops queries of every new client once full.
		// Dropped queries therefore carry no client name and are attributed to the client
		// IP in the statistics.
		resolver.NewRateLimitingResolver(ctx, cfg.RateLimit),
		// adopts the ECS subnet as the internal client IP (ecs.useAsClient) before the
		// client-name lookup, blocking and the cache consume the client identity, so the
		// ECS client is used for those features and is preserved across cache hits
		resolver.NewECSClientResolver(cfg.ECS),
		// above filtering and fqdnOnly, which answer a query on their own: a lookup below
		// them would leave request.ClientNames empty for every query they short-circuit,
		// and the statistics would attribute those to the raw client IP while the same
		// client's other queries are attributed to its name
		clientNames,
		resolver.NewFilteringResolver(cfg.Filtering),
		resolver.NewFQDNOnlyResolver(cfg.FQDNOnly),
		resolver.NewEDEResolver(cfg.EDE),
		queryLogging,
		resolver.NewMetricsResolver(cfg.Prometheus),
		customDNS,
		hostsFile,
		// above blocking and the cache: it inspects only RESOLVED/CACHED answers
		// (conditional/custom DNS/hosts file/SUDN/blocked answers are recognized
		// by response type and pass through; the cache stores only
		// upstream-derived answers, so CACHED implies upstream origin), cached
		// answers — incl. entries synced via redis — are re-inspected on every
		// hit, and blocking's internal FQDN client-identifier lookups enter the
		// chain below it
		resolver.NewRebindingProtectionResolver(cfg.RebindingProtection),
		blocking,
		dnssecResolver, // DNSSEC validation BEFORE caching - validates all responses before they are cached
		cachingResolver,
		resolver.NewDNS64Resolver(cfg.DNS64), // DNS64 synthesis AFTER caching
		resolver.NewECSResolver(cfg.ECS),
		condUpstream,
		resolver.NewSpecialUseDomainNamesResolver(cfg.SUDN),
		upstreamTree,
	)

	return r, nil
}

func (s *Server) registerDNSHandlers(ctx context.Context) {
	for _, server := range s.dnsServers {
		//nolint:forcetypeassert // handler is always *dns.ServeMux; set during server construction
		handler := server.Handler.(*dns.ServeMux)
		handler.HandleFunc(".", func(w dns.ResponseWriter, m *dns.Msg) {
			s.OnRequest(ctx, w, m)
		})
		handler.HandleFunc("healthcheck.blocky", func(w dns.ResponseWriter, m *dns.Msg) {
			s.OnHealthCheck(ctx, w, m)
		})
	}
}

func (s *Server) printConfiguration() {
	logger().Info("current configuration:")

	if s.cfg.Redis.IsEnabled() {
		logger().Info("Redis:")
		log.WithIndent(logger(), "  ", s.cfg.Redis.LogConfig)
	}

	resolver.ForEach(s.queryResolver, func(res resolver.Resolver) {
		resolver.LogResolverConfig(res, logger())
	})

	logger().Info("listeners:")
	log.WithIndent(logger(), "  ", func(e *logrus.Entry) {
		s.cfg.Web.LogConfig(e)
		s.cfg.DNS.LogConfig(e)
	})

	logger().Info("runtime information:")

	// force garbage collector
	runtime.GC()
	debug.FreeOSMemory()

	logger().Infof("  numCPU =       %d", runtime.NumCPU())
	logger().Infof("  numGoroutine = %d", runtime.NumGoroutine())

	// gather memory stats
	var m runtime.MemStats

	runtime.ReadMemStats(&m)

	logger().Infof("  memory:")
	logger().Infof("    heap =     %10v MB", toMB(m.HeapAlloc))
	logger().Infof("    sys =      %10v MB", toMB(m.Sys))
	logger().Infof("    numGC =    %10v", m.NumGC)
}

func toMB(b uint64) uint64 {
	const bytesInKB = 1024

	return b / bytesInKB / bytesInKB
}

// Start starts the server
func (s *Server) Start(ctx context.Context, errCh chan<- error) {
	logger().Info("Starting server")

	for _, srv := range s.dnsServers {
		go func() {
			// When a listener/packet connection was pre-created (freeBind), serve it via
			// ActivateAndServe; otherwise let miekg/dns create the socket via ListenAndServe.
			serve := srv.ListenAndServe
			if srv.Listener != nil || srv.PacketConn != nil {
				serve = srv.ActivateAndServe
			}

			if err := serve(); err != nil {
				errCh <- fmt.Errorf("start %s listener failed: %w", srv.Net, err)
			}
		}()
	}

	for listener, srv := range s.servers {
		go func() {
			logger().Infof("%s server is up and running on addr/port %s", srv, listener.Addr())

			err := srv.Serve(ctx, listener)
			if err != nil {
				errCh <- fmt.Errorf("%s on %s: %w", srv, listener.Addr(), err)
			}
		}()
	}

	if s.http3Server != nil {
		for _, pc := range s.http3PacketConns {
			go func() {
				logger().Infof("%s server is up and running on addr/port %s",
					s.http3Server, pc.LocalAddr())

				err := s.http3Server.inner.Serve(pc)
				if err != nil &&
					!errors.Is(err, quic.ErrServerClosed) &&
					!errors.Is(err, http.ErrServerClosed) &&
					!errors.Is(err, net.ErrClosed) {
					errCh <- fmt.Errorf("%s on %s: %w", s.http3Server, pc.LocalAddr(), err)
				}
			}()
		}
	}

	registerPrintConfigurationTrigger(ctx, s)
}

// Stop stops the server
func (s *Server) Stop(ctx context.Context) error {
	logger().Info("Stopping server")

	// Shut down HTTP/3 in order: server first (drains in-flight
	// requests and unblocks the Serve goroutines), then UDP packet
	// conns. Closing the packet conns first would cause Serve to
	// return a non-sentinel error that would land in errCh as a
	// spurious "server start failed".
	if s.http3Server != nil {
		if err := s.http3Server.Close(); err != nil {
			logger().Warn("failed to close http3 server: ", err)
		}
	}

	for _, pc := range s.http3PacketConns {
		if err := pc.Close(); err != nil {
			logger().Warn("failed to close http3 packet conn: ", err)
		}
	}

	if s.redisCancel != nil {
		s.redisCancel()
	}

	for _, c := range s.closers {
		if err := c.Close(); err != nil {
			logger().Warn("failed to close resource: ", err)
		}
	}

	for _, server := range s.dnsServers {
		if err := server.ShutdownContext(ctx); err != nil {
			return fmt.Errorf("stop %s listener failed: %w", server.Net, err)
		}
	}

	return nil
}

func extractClientIDFromHost(hostName string) string {
	const clientIDPrefix = "id-"
	if strings.HasPrefix(hostName, clientIDPrefix) && strings.Contains(hostName, ".") {
		return hostName[len(clientIDPrefix):strings.Index(hostName, ".")]
	}

	return ""
}

func newRequest(
	ctx context.Context,
	clientIP net.IP, clientID string,
	protocol model.RequestProtocol, request *dns.Msg,
) (context.Context, *model.Request) {
	ctx, logger := log.CtxWithFields(ctx, logrus.Fields{
		"req_id":    uuid.New().String(),
		"question":  util.QuestionToString(request.Question),
		"client_ip": clientIP,
	})

	logger.WithFields(logrus.Fields{
		"client_request_id": request.Id,
		"client_id":         clientID,
		"protocol":          protocol,
	}).Trace("new incoming request")

	req := model.Request{
		ClientIP:        clientIP,
		RequestClientID: clientID,
		Protocol:        protocol,
		Req:             request,
		RequestTS:       time.Now(),
	}

	return ctx, &req
}

func newRequestFromDNS(ctx context.Context, rw dns.ResponseWriter, msg *dns.Msg) (context.Context, *model.Request) {
	var (
		clientIP net.IP
		protocol model.RequestProtocol
	)

	if rw != nil {
		clientIP, protocol = resolveClientIPAndProtocol(rw.RemoteAddr())
	}

	var clientID string
	if con, ok := rw.(dns.ConnectionStater); ok && con.ConnectionState() != nil {
		clientID = extractClientIDFromHost(con.ConnectionState().ServerName)
	}

	return newRequest(ctx, clientIP, clientID, protocol, msg)
}

func newRequestFromHTTP(ctx context.Context, req *http.Request, msg *dns.Msg) (context.Context, *model.Request) {
	protocol := model.RequestProtocolTCP
	clientIP := util.HTTPClientIP(req)

	clientID := chi.URLParam(req, "clientID")
	if clientID == "" {
		clientID = extractClientIDFromHost(req.Host)
	}

	return newRequest(ctx, clientIP, clientID, protocol, msg)
}

// OnRequest will be executed if a new DNS request is received
func (s *Server) OnRequest(ctx context.Context, w dns.ResponseWriter, msg *dns.Msg) {
	ctx, request := newRequestFromDNS(ctx, w, msg)

	s.handleReq(ctx, request, w)
}

type msgWriter interface {
	WriteMsg(msg *dns.Msg) error
}

func (s *Server) handleReq(ctx context.Context, request *model.Request, w msgWriter) {
	response, err := s.resolve(ctx, request)
	switch {
	case errors.Is(err, resolver.ErrRateLimited):
		return
	case err != nil:
		log.FromCtx(ctx).Error("error on processing request:", err)
		m := new(dns.Msg)
		m.SetRcode(request.Req, dns.RcodeServerFailure)
		err := w.WriteMsg(m)
		util.LogOnError(ctx, "can't write message: ", err)
	default:
		err := w.WriteMsg(response.Res)
		util.LogOnError(ctx, "can't write message: ", err)
	}
}

func (s *Server) resolve(ctx context.Context, request *model.Request) (response *model.Response, rerr error) {
	defer func() {
		if val := recover(); val != nil {
			rerr = fmt.Errorf("panic occurred: %v", val)
		}
	}()

	contextUpstreamTimeoutMultiplier := 100
	timeoutDuration := time.Duration(contextUpstreamTimeoutMultiplier) * s.cfg.Upstreams.Timeout.ToDuration()

	ctx, cancel := context.WithTimeout(ctx, timeoutDuration)

	defer cancel()

	// The resolver chain mutates request.Req in place, so capture what the client itself asked for
	// up front: the response is normalized against that, not the mutated request.
	query := newClientQuery(request)

	// Blocky doesn't implement DNS Cookies (RFC 7873), so a Server Cookie a client returns is one
	// an upstream issued and blocky can neither validate nor reissue it. Forwarding it is worse
	// than dropping it: it is likely to reach a different upstream than the one that issued it
	// (`parallel_best` picks at random), which can't validate it either and may answer BADCOOKIE.
	// The OPT record itself is kept, since it still carries the DO bit and the buffer size the
	// client advertised.
	util.RemoveEdns0OptionKeepRecord[*dns.EDNS0_COOKIE](request.Req)

	switch {
	case len(request.Req.Question) == 0:
		m := new(dns.Msg)
		m.SetRcode(request.Req, dns.RcodeFormatError)

		log.FromCtx(ctx).Error("query has no questions")

		response = &model.Response{Res: m, RType: model.ResponseTypeCUSTOMDNS, Reason: "CUSTOM DNS"}
	default:
		var err error

		response, err = s.queryResolver.Resolve(ctx, request)
		if err != nil {
			var upstreamErr *resolver.UpstreamServerError

			if errors.As(err, &upstreamErr) {
				response = &model.Response{Res: upstreamErr.Msg, RType: model.ResponseTypeRESOLVED, Reason: upstreamErr.Error()}
			} else {
				return nil, fmt.Errorf("query resolution failed: %w", err)
			}
		}
	}

	query.normalizeResponse(response.Res)

	return response, nil
}

// OnHealthCheck Handler for docker health check. Just returns OK code without delegating to resolver chain
func (s *Server) OnHealthCheck(ctx context.Context, w dns.ResponseWriter, request *dns.Msg) {
	resp := new(dns.Msg)
	resp.SetReply(request)
	resp.Rcode = dns.RcodeSuccess

	err := w.WriteMsg(resp)
	util.LogOnError(ctx, "can't write message: ", err)
}

func resolveClientIPAndProtocol(addr net.Addr) (ip net.IP, protocol model.RequestProtocol) {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a.IP, model.RequestProtocolUDP
	case *net.TCPAddr:
		return a.IP, model.RequestProtocolTCP
	}

	return nil, model.RequestProtocolUDP
}
