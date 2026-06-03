package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/api"
	"github.com/CoOre/keenetic-sing-box-ui/internal/auth"
	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
	"github.com/CoOre/keenetic-sing-box-ui/internal/config"
	"github.com/CoOre/keenetic-sing-box-ui/internal/lists"
	"github.com/CoOre/keenetic-sing-box-ui/internal/resolve"
	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
	"github.com/CoOre/keenetic-sing-box-ui/internal/settings"
	"github.com/CoOre/keenetic-sing-box-ui/internal/singbox"
	"github.com/CoOre/keenetic-sing-box-ui/internal/system"
	"github.com/CoOre/keenetic-sing-box-ui/internal/transparent"
	"github.com/CoOre/keenetic-sing-box-ui/web"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const (
	defaultUIConfigPath = "/opt/etc/keenetic-sing-box-ui/config.json"
	defaultHTTPListen   = "0.0.0.0:9091"
	defaultHTTPSListen  = "0.0.0.0:9443"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "token" {
		os.Exit(runTokenSubcommand(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "firewall" {
		os.Exit(runFirewallSubcommand(os.Args[2:]))
	}
	os.Exit(runServer(os.Args[1:]))
}

// runFirewallSubcommand drives the transparent-proxy firewall. The `apply`
// action is what the netfilter.d hook calls on every router firewall rebuild
// (`firewall apply --table <table>`); `up`/`clean` are for manual/diagnostic
// use. It loads the persisted sing-box settings and translates them into a
// transparent.Config — keeping a single source of truth for the rules.
func runFirewallSubcommand(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: keenetic-sing-box-ui firewall {apply|up|clean} [--table t] [--config path]")
		return 2
	}
	action := args[0]
	fs := flag.NewFlagSet("firewall", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultUIConfigPath, "UI config path")
	table := fs.String("table", "", "iptables table (for apply)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	st := settings.NewStore(filepath.Join(filepath.Dir(*cfgPath), "singbox-settings.json"))
	s, err := st.Get()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load settings:", err)
		return 1
	}
	eng := &transparent.Engine{Runner: cmdrun.OS{}, Log: logger}
	cfg := transparentConfig(s)
	ctx := context.Background()

	switch action {
	case "apply":
		// Debounce: Keenetic rebuilds its firewall many times in quick succession
		// (e.g. on a route change), calling this hook once per table per rebuild.
		// Without debouncing, parallel apply invocations race on the same chains.
		// We use an exclusive lockfile per table; if another apply holds it, exit
		// 0 silently — the in-flight invocation will install the rules anyway.
		lockPath := "/opt/tmp/ksbui_fw_" + *table + ".lock"
		lf, lerr := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if lerr != nil {
			// If the lock is stale (> 30 s old), remove it and retry once.
			if st, sterr := os.Stat(lockPath); sterr == nil && time.Since(st.ModTime()) > 30*time.Second {
				_ = os.Remove(lockPath)
				lf, lerr = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			}
		}
		if lerr != nil {
			return 0 // another apply running for this table
		}
		defer os.Remove(lockPath)
		_ = lf.Close()
		err = eng.Apply(ctx, cfg, *table)
	case "up":
		err = eng.Up(ctx, cfg)
	case "clean":
		err = eng.Clean(ctx)
	default:
		fmt.Fprintln(os.Stderr, "unknown firewall action:", action)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "firewall "+action+":", err)
		return 1
	}
	return 0
}

// transparentConfig maps persisted settings to a transparent.Config. socks/tun
// modes map to ModeOff (no firewall capture).
func transparentConfig(s settings.Settings) transparent.Config {
	mode := transparent.ModeOff
	switch s.InboundMode {
	case "tproxy":
		mode = transparent.ModeTProxy
	case "redirect":
		mode = transparent.ModeRedirect
	}
	return transparent.Config{
		Mode:         mode,
		TProxyPort:   s.InboundPort,
		RedirectPort: s.InboundPort,
		PolicyName:   s.PolicyName,
		ExtraExclude: s.ExcludeCIDR,
		UseConntrack: s.UseConntrack,
		RouteCIDR:    s.RouteCIDR, // static seed; resolver folds in resolved domain IPs
	}
}

func runTokenSubcommand(args []string) int {
	if len(args) < 1 || args[0] != "show" {
		fmt.Fprintln(os.Stderr, "usage: keenetic-sing-box-ui token show [--config path]")
		return 2
	}
	fs := flag.NewFlagSet("token show", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultUIConfigPath, "UI config path")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	cfg, _, err := auth.LoadOrInit(*cfgPath, auth.UIConfigDefaults{
		Dir:         filepath.Dir(*cfgPath),
		HTTPListen:  defaultHTTPListen,
		HTTPSListen: defaultHTTPSListen,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		return 1
	}
	fmt.Println(cfg.AdminToken)
	return 0
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func runServer(args []string) int {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultUIConfigPath, "UI config path")
	listen := fs.String("listen", "", "override HTTP listen address (default from config)")
	httpsListen := fs.String("https-listen", "", "override HTTPS listen address (default from config)")
	disableHTTPS := fs.Bool("disable-https", false, "do not start HTTPS listener (HTTP only)")
	optRoot := fs.String("opt-root", "/", "root prefix for /opt paths (use a temp dir for dev)")
	showVersion := fs.Bool("version", false, "print version and exit")
	var tlsSAN stringList
	fs.Var(&tlsSAN, "tls-san", "extra SAN (DNS name or IP) for self-signed cert; may be repeated")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// On memory-constrained routers (256–512 MB total RAM) the Go runtime's
	// default GC behaviour keeps freed heap pages in its own pool, causing RSS
	// to creep up after large transient allocations (e.g. list JSON parsing).
	// Setting a soft limit tells the runtime to GC aggressively and return
	// memory to the OS when approaching the threshold.
	// 120 MiB soft limit. Gives enough headroom for fetching large IP lists
	// (e.g. YouTube has ~23k CIDRs, ~2 MB JSON → ~40 MB transient allocation)
	// while keeping steady-state RSS well below router's available RAM.
	debug.SetMemoryLimit(120 << 20)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if *showVersion {
		fmt.Printf("keenetic-sing-box-ui %s (commit %s, built %s, %s/%s)\n",
			version, commit, date, runtime.GOOS, runtime.GOARCH)
		return 0
	}

	uiCfg, created, err := auth.LoadOrInit(*cfgPath, auth.UIConfigDefaults{
		Dir:         filepath.Dir(*cfgPath),
		HTTPListen:  defaultHTTPListen,
		HTTPSListen: defaultHTTPSListen,
	})
	if err != nil {
		logger.Error("load ui config", "path", *cfgPath, "err", err)
		return 1
	}
	if created {
		logger.Info("first run: generated admin token", "token", uiCfg.AdminToken, "config", *cfgPath)
		logger.Info("this token is printed once; recover later via 'keenetic-sing-box-ui token show'")
	}

	tlsCreated, err := auth.EnsureTLS(auth.TLSPaths{CertPath: uiCfg.TLSCertPath, KeyPath: uiCfg.TLSKeyPath}, tlsSAN)
	if err != nil {
		logger.Error("ensure tls", "err", err)
		return 1
	}
	if tlsCreated {
		logger.Info("generated self-signed certificate",
			"cert", uiCfg.TLSCertPath, "key", uiCfg.TLSKeyPath, "sans", tlsSAN)
	}

	store := auth.NewSessionStore(time.Duration(uiCfg.SessionTTLHours) * time.Hour)
	authn := auth.NewAuthenticator(uiCfg.AdminToken, store)
	cfgPathForPersist := *cfgPath
	authn.SetPasswordHash(uiCfg.PasswordHash, func(hash string) error {
		return auth.PersistPasswordHash(cfgPathForPersist, hash)
	})

	paths := system.PathsRooted(*optRoot)
	// Keep the sing-box init script in sync with the embedded template on every
	// start, so a UI upgrade alone refreshes it (no reinstall needed).
	if err := singbox.EnsureInitScript(paths.SingBoxInit); err != nil {
		logger.Warn("ensure sing-box init script", "err", err)
	}
	// Self-heal: if sing-box is installed but its config went missing, lay down
	// the directories, init script and a default config so the service can run.
	if fileExists(paths.SingBoxBin) && !fileExists(paths.SingBoxConfig) {
		if _, err := singbox.Bootstrap(singbox.BootstrapPaths{
			ConfigPath: paths.SingBoxConfig,
			InitPath:   paths.SingBoxInit,
			LogPath:    paths.SingBoxLog,
			CacheDir:   filepath.Dir(paths.SingBoxCache),
		}); err != nil {
			logger.Warn("ensure sing-box config", "err", err)
		} else {
			logger.Info("regenerated missing sing-box config", "path", paths.SingBoxConfig)
		}
	}
	clashAddr, clashSecret := resolveClash(paths.SingBoxConfig, uiCfg.ClashSecret)
	deps := &api.Deps{
		Paths:       paths,
		Detector:    system.NewDetector(paths),
		Service:     singbox.NewService(paths.SingBoxInit),
		Opkg:        singbox.NewOpkg(paths.Opkg),
		Github:      singbox.NewGithub(paths.SingBoxBin),
		Config:      config.NewStore(paths.SingBoxConfig),
		Checker:     config.NewChecker(paths.SingBoxBin),
		LogPath:     paths.SingBoxLog,
		ClashAddr:   clashAddr,
		ClashSecret: clashSecret,
		Servers:     servers.NewStore(filepath.Join(filepath.Dir(*cfgPath), "servers.json")),
		Settings:    settings.NewStore(filepath.Join(filepath.Dir(*cfgPath), "singbox-settings.json")),
		Firewall:    &transparent.Engine{Runner: cmdrun.OS{}, Log: logger, Bin: selfPath()},
		Lists:       lists.NewStore(filepath.Join(filepath.Dir(*cfgPath), "lists.json")),
	}
	// List runner: fetches URL sources on schedule and caches results.
	// No auto-restart of sing-box — lists are applied at the next manual
	// "Apply & Restart" click. Auto-restart was too heavy for the router's RAM.
	deps.ListRunner = &lists.Runner{
		Store: deps.Lists,
		Log:   logger,
	}
	go deps.ListRunner.Start()
	// Route resolver: keeps the transparent route ipset current with the live
	// IPs of the proxied domains (redirect mode). Started below once we have a
	// cancellable context; constructed here so handlers can trigger Refresh.
	deps.Resolver = &resolve.Resolver{
		Engine:   deps.Firewall,
		Settings: deps.Settings,
		Lists:    deps.Lists,
		Log:      logger,
	}
	// Background: auto-start sing-box if installed + watchdog to revive it.
	go runWatchdog(deps, logger)

	mux := buildMux(authn, deps)

	httpAddr := orDefault(*listen, uiCfg.HTTPListen, defaultHTTPListen)
	httpsAddr := orDefault(*httpsListen, uiCfg.HTTPSListen, defaultHTTPSListen)
	startHTTPS := !*disableHTTPS && !uiCfg.HTTPSOnly
	if uiCfg.HTTPSOnly {
		startHTTPS = !*disableHTTPS
	}

	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           httpHandler(mux, httpsAddr, uiCfg.HTTPSOnly),
		ReadHeaderTimeout: 10 * time.Second,
	}
	var httpsSrv *http.Server
	if startHTTPS {
		httpsSrv = &http.Server{
			Addr:              httpsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go deps.Resolver.Start(ctx)

	go func() {
		logger.Info("listening http", "addr", httpAddr, "https_redirect", uiCfg.HTTPSOnly)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http listen", "err", err)
			stop()
		}
	}()
	if httpsSrv != nil {
		go func() {
			logger.Info("listening https", "addr", httpsAddr)
			if err := httpsSrv.ListenAndServeTLS(uiCfg.TLSCertPath, uiCfg.TLSKeyPath); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("https listen", "err", err)
				stop()
			}
		}()
	}

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown", "err", err)
	}
	if httpsSrv != nil {
		if err := httpsSrv.Shutdown(shutdownCtx); err != nil {
			logger.Error("https shutdown", "err", err)
		}
	}
	return 0
}

func buildMux(a *auth.Authenticator, deps *api.Deps) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"` + version + `"}`))
	})

	mux.HandleFunc("GET /api/auth/status", a.AuthStatus)
	mux.HandleFunc("POST /api/login", a.Login)
	mux.HandleFunc("POST /api/logout", a.Logout)
	// SetPassword does its own auth (TOFU first-run, else current-password or
	// Bearer), so it is mounted outside RequireAuth/RequireCSRF.
	mux.HandleFunc("POST /api/password", a.SetPassword)
	mux.Handle("GET /api/whoami", a.RequireAuth(http.HandlerFunc(whoami)))

	api.Register(mux, a, deps)

	static, err := web.Static()
	if err == nil {
		mux.Handle("/", http.FileServerFS(static))
	} else {
		slog.Default().Error("embed static", "err", err)
	}
	return mux
}

func whoami(w http.ResponseWriter, r *http.Request) {
	sess, ok := auth.SessionFromContext(r.Context())
	resp := map[string]any{"auth": "bearer"}
	if ok {
		resp = map[string]any{
			"auth":      "session",
			"expires":   sess.Expires.Unix(),
			"created":   sess.Created.Unix(),
			"last_seen": sess.LastSeen.Unix(),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// httpHandler wraps the mux so that HTTP requests are either served normally
// or redirected to HTTPS, depending on httpsOnly. /healthz is always served
// over HTTP for liveness probes.
func httpHandler(mux http.Handler, httpsAddr string, httpsOnly bool) http.Handler {
	if !httpsOnly {
		return mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			mux.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		_, port, _ := strings.Cut(httpsAddr, ":")
		target := "https://" + host + ":" + port + r.RequestURI
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}

// selfPath returns the absolute path to this binary, embedded into the
// netfilter.d hook so KeeneticOS can call back into us. Falls back to the
// conventional install path.
func selfPath() string {
	if p, err := os.Executable(); err == nil {
		if abs, aerr := filepath.Abs(p); aerr == nil {
			return abs
		}
		return p
	}
	return "/opt/bin/keenetic-sing-box-ui"
}

// runWatchdog auto-starts sing-box on UI startup and revives it if it dies.
// Ticks every 2 minutes (low overhead — cheap /proc check each time).
// Firewall.Up() is called ONLY when sing-box was actually dead and we started
// it — not on every tick. The netfilter.d hook handles rule restoration after
// Keenetic rebuilds its own firewall, so we don't need to do it here.
func runWatchdog(deps *api.Deps, log *slog.Logger) {
	if deps.Service == nil || deps.Settings == nil {
		return
	}

	ensureSingbox := func() {
		if !fileExists(deps.Paths.SingBoxBin) || !fileExists(deps.Paths.SingBoxConfig) {
			return
		}
		s, err := deps.Settings.Get()
		if err != nil {
			return
		}
		fcfg := transparentConfig(s)

		// Cheap check: read /proc/net/tcp, zero subprocess spawning.
		if transparent.ProxyListening(fcfg.InboundPort()) {
			return // already up — do nothing
		}

		// sing-box is down: start it.
		log.Info("watchdog: sing-box not listening, starting")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, serr := deps.Service.Do(ctx, singbox.ActionStart); serr != nil {
			log.Warn("watchdog: start failed", "err", serr)
			return
		}
		transparent.WaitProxy(fcfg.InboundPort(), 6*time.Second)

		// Only assert firewall after an actual start event (not every tick).
		if deps.Firewall == nil || fcfg.Mode == transparent.ModeOff {
			return
		}
		// Seed the route ipset with the URL-list CIDRs too (transparentConfig is
		// settings-only). Up adds these additively, so resolver-added domain IPs
		// already in the set are preserved.
		if deps.Lists != nil {
			if _, lCIDRs, lerr := deps.Lists.MergedEntries(); lerr == nil {
				fcfg.RouteCIDR = append(fcfg.RouteCIDR, lCIDRs...)
			}
		}
		if err := deps.Firewall.Up(context.Background(), fcfg); err != nil {
			log.Warn("watchdog: firewall up", "err", err)
		}
		// Re-resolve proxied domains into the set after reviving sing-box.
		if deps.Resolver != nil {
			deps.Resolver.Refresh(context.Background())
		}
	}

	// Brief startup delay, then one immediate check (post-reboot / post-deploy).
	time.Sleep(5 * time.Second)
	ensureSingbox()

	// 2-minute heartbeat. The cheap /proc check costs essentially nothing.
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for range t.C {
		ensureSingbox()
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func orDefault(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolveClash reads the live sing-box config.json to discover the Clash API
// controller address and secret (these live in sing-box's config, not ours).
// Falls back to the loopback default and the UI config's clash_secret if the
// file is absent or lacks the block. Returns empty addr only if there is
// truly nothing to proxy to.
func resolveClash(singBoxConfigPath, fallbackSecret string) (addr, secret string) {
	addr = "127.0.0.1:9090"
	secret = fallbackSecret
	body, err := os.ReadFile(singBoxConfigPath)
	if err != nil {
		return addr, secret
	}
	var cfg struct {
		Experimental struct {
			ClashAPI struct {
				ExternalController string `json:"external_controller"`
				Secret             string `json:"secret"`
			} `json:"clash_api"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return addr, secret
	}
	if c := cfg.Experimental.ClashAPI.ExternalController; c != "" {
		addr = c
	}
	if s := cfg.Experimental.ClashAPI.Secret; s != "" {
		secret = s
	}
	return addr, secret
}
