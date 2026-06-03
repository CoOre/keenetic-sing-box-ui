package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/auth"
	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
	"github.com/CoOre/keenetic-sing-box-ui/internal/config"
	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
	"github.com/CoOre/keenetic-sing-box-ui/internal/settings"
	"github.com/CoOre/keenetic-sing-box-ui/internal/singbox"
	"github.com/CoOre/keenetic-sing-box-ui/internal/system"
)

var errIntentional = errors.New("intentional test failure")

const tokenForTest = "test-token-aaaaaaaaaaaaaaaaaaaaaaaa"

type testEnv struct {
	t      *testing.T
	root   string
	paths  system.Paths
	deps   *Deps
	authn  *auth.Authenticator
	runner *cmdrun.Fake
	srv    *httptest.Server
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	paths := system.PathsRooted(root)
	runner := &cmdrun.Fake{Responses: map[string]cmdrun.FakeResponse{}}

	det := system.NewDetector(paths)
	det.Runner = runner
	svc := &singbox.Service{InitPath: paths.SingBoxInit, Runner: runner}
	opkg := &singbox.Opkg{Bin: paths.Opkg, Runner: runner}
	ck := &config.Checker{SingBoxBin: paths.SingBoxBin, Runner: runner}

	deps := &Deps{
		Paths:    paths,
		Detector: det,
		Service:  svc,
		Opkg:     opkg,
		Github:   &singbox.Github{HTTP: http.DefaultClient, Repo: singbox.DefaultRepo, DestBin: paths.SingBoxBin},
		Config:   config.NewStore(paths.SingBoxConfig),
		Checker:  ck,
		LogPath:  paths.SingBoxLog,
		Servers:  servers.NewStore(filepath.Join(root, "servers.json")),
		Settings: settings.NewStore(filepath.Join(root, "singbox-settings.json")),
	}
	authn := auth.NewAuthenticator(tokenForTest, auth.NewSessionStore(time.Hour))

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", authn.Login)
	mux.HandleFunc("POST /api/logout", authn.Logout)
	Register(mux, authn, deps)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &testEnv{t: t, root: root, paths: paths, deps: deps, authn: authn, runner: runner, srv: srv}
}

func (e *testEnv) bearer(method, path string, body []byte) *http.Response {
	e.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	var req *http.Request
	var err error
	if rdr == nil {
		req, err = http.NewRequest(method, e.srv.URL+path, nil)
	} else {
		req, err = http.NewRequest(method, e.srv.URL+path, rdr)
	}
	if err != nil {
		e.t.Fatalf("req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenForTest)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("do: %v", err)
	}
	return resp
}

func mustJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("json: %v; body: %s", err, body)
	}
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAPI_RequiresAuth(t *testing.T) {
	e := newEnv(t)
	resp, _ := http.Get(e.srv.URL + "/api/system")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestSystem_EmptyRoot(t *testing.T) {
	e := newEnv(t)
	resp := e.bearer(http.MethodGet, "/api/system", nil)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	var info system.Info
	mustJSON(t, body, &info)
	if info.Entware != nil {
		t.Errorf("expected no entware: %+v", info.Entware)
	}
}

func TestInstallStatus_NotInstalled(t *testing.T) {
	e := newEnv(t)
	resp := e.bearer(http.MethodGet, "/api/install/status", nil)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	var r installStatusResp
	mustJSON(t, body, &r)
	if r.Installed || r.Entware {
		t.Errorf("expected not installed, no entware: %+v", r)
	}
}

func TestService_StartCallsInit(t *testing.T) {
	e := newEnv(t)
	// init script must exist for Do() to call it.
	if err := os.MkdirAll(filepath.Dir(e.paths.SingBoxInit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.paths.SingBoxInit, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.runner.Default = cmdrun.FakeResponse{Stdout: "sing-box is alive\n"}

	resp := e.bearer(http.MethodPost, "/api/service/start", nil)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if len(e.runner.Calls) == 0 || e.runner.Calls[0].Args[1] != "start" {
		t.Errorf("expected start call, got %+v", e.runner.Calls)
	}
}

func TestService_InvalidAction(t *testing.T) {
	e := newEnv(t)
	resp := e.bearer(http.MethodPost, "/api/service/nuke", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestConfig_ReadWrite_Roundtrip(t *testing.T) {
	e := newEnv(t)
	resp := e.bearer(http.MethodPut, "/api/config", []byte(`{"hello":"world"}`))
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write status %d body %s", resp.StatusCode, body)
	}

	resp = e.bearer(http.MethodGet, "/api/config", nil)
	body = readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"hello":"world"`) {
		t.Errorf("body: %s", body)
	}
}

func TestConfigBackups_ListAndRead(t *testing.T) {
	e := newEnv(t)
	// Two writes → one backup.
	e.bearer(http.MethodPut, "/api/config", []byte(`{"v":1}`))
	e.bearer(http.MethodPut, "/api/config", []byte(`{"v":2}`))

	resp := e.bearer(http.MethodGet, "/api/config/backups", nil)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	var listed struct {
		Backups []config.BackupMeta `json:"backups"`
	}
	mustJSON(t, body, &listed)
	if len(listed.Backups) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(listed.Backups))
	}

	name := listed.Backups[0].Name
	resp = e.bearer(http.MethodGet, "/api/config/backups/"+name, nil)
	got := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read backup status %d", resp.StatusCode)
	}
	if string(got) != `{"v":1}` {
		t.Errorf("backup content: %s", got)
	}
}

func TestConfigBackups_BadName(t *testing.T) {
	e := newEnv(t)
	resp := e.bearer(http.MethodGet, "/api/config/backups/config.json", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for non-backup name, got %d", resp.StatusCode)
	}
}

func TestConfig_Read_NotFound(t *testing.T) {
	e := newEnv(t)
	resp := e.bearer(http.MethodGet, "/api/config", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestConfig_Check_UsesContent(t *testing.T) {
	e := newEnv(t)
	e.runner.Default = cmdrun.FakeResponse{Stdout: "ok\n"}
	resp := e.bearer(http.MethodPost, "/api/config/check", []byte(`{"a":1}`))
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	var res config.CheckResult
	mustJSON(t, body, &res)
	if !res.OK {
		t.Errorf("expected ok, got %+v", res)
	}
	if len(e.runner.Calls) == 0 || e.runner.Calls[0].Args[0] != "check" {
		t.Errorf("expected check call, got %+v", e.runner.Calls)
	}
}

func TestRepairInvalidConfig(t *testing.T) {
	e := newEnv(t)
	// Write a config that the (fake) checker will reject.
	if err := os.MkdirAll(filepath.Dir(e.paths.SingBoxConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := `{"dns":{"servers":[{"address":"tls://8.8.8.8"}]}}`
	if err := os.WriteFile(e.paths.SingBoxConfig, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	// Fake `sing-box check` → failure (legacy config).
	e.runner.Default = cmdrun.FakeResponse{
		Stderr: "FATAL legacy DNS servers is deprecated",
		Err:    errIntentional,
	}

	h := &handlers{d: e.deps}
	out := &installResp{}
	r := httptest.NewRequest(http.MethodPost, "/api/install", nil)
	h.repairInvalidConfig(r, out)

	if !out.ConfigRegnerated {
		t.Fatal("expected config to be regenerated")
	}
	if out.ConfigBackup == "" {
		t.Error("expected a backup path")
	}
	// New config must contain the modern typed DNS server (no legacy address).
	body, _ := os.ReadFile(e.paths.SingBoxConfig)
	if strings.Contains(string(body), `"address"`) {
		t.Errorf("regenerated config still has legacy address: %s", body)
	}
	if !strings.Contains(string(body), `"default_domain_resolver"`) {
		t.Errorf("regenerated config missing modern field")
	}
	// Backup must hold the original broken content.
	bk, _ := os.ReadFile(out.ConfigBackup)
	if string(bk) != broken {
		t.Errorf("backup mismatch: %s", bk)
	}
}

func TestRepairInvalidConfig_LeavesValidAlone(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(filepath.Dir(e.paths.SingBoxConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	good := `{"valid":true}`
	os.WriteFile(e.paths.SingBoxConfig, []byte(good), 0o644)
	// Fake check → OK (no error).
	e.runner.Default = cmdrun.FakeResponse{Stdout: ""}

	h := &handlers{d: e.deps}
	out := &installResp{}
	h.repairInvalidConfig(httptest.NewRequest(http.MethodPost, "/x", nil), out)

	if out.ConfigRegnerated {
		t.Error("valid config must not be regenerated")
	}
	body, _ := os.ReadFile(e.paths.SingBoxConfig)
	if string(body) != good {
		t.Errorf("valid config was modified: %s", body)
	}
}

func TestServers_ParseSaveApply(t *testing.T) {
	e := newEnv(t)
	link := "vless://b379c1d9-0b37-41b0-96b8-467c29b8ca9d@45.9.13.188:8443" +
		"?type=tcp&security=reality&pbk=KEY&sid=96543f22d4e8445c&sni=x.example&fp=chrome&flow=xtls-rprx-vision#my"

	// parse
	resp := e.bearer(http.MethodPost, "/api/servers/parse", []byte(`{"link":"`+link+`"}`))
	pbody := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("parse status %d: %s", resp.StatusCode, pbody)
	}
	// save the parsed server
	resp = e.bearer(http.MethodPost, "/api/servers", pbody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save status %d: %s", resp.StatusCode, readAll(t, resp))
	}
	// list
	resp = e.bearer(http.MethodGet, "/api/servers", nil)
	var listed struct {
		Servers []servers.Entry `json:"servers"`
	}
	mustJSON(t, readAll(t, resp), &listed)
	if len(listed.Servers) != 1 || listed.Servers[0].Server.UUID == "" {
		t.Fatalf("list wrong: %+v", listed.Servers)
	}

	// apply (fake checker returns OK by default)
	resp = e.bearer(http.MethodPost, "/api/servers/apply", []byte(`{"restart":false}`))
	abody := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status %d: %s", resp.StatusCode, abody)
	}
	var ar serversApplyResp
	mustJSON(t, abody, &ar)
	if !ar.Applied || ar.Servers != 1 {
		t.Errorf("apply resp: %+v", ar)
	}
	// config.json must now exist; default inbound mode is socks (mixed).
	cfg, err := e.deps.Config.Read()
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(cfg), `"mixed"`) {
		t.Errorf("assembled config should use mixed (socks) inbound by default")
	}
}

func TestSettings_RoundTripAndAffectsApply(t *testing.T) {
	e := newEnv(t)
	// default
	resp := e.bearer(http.MethodGet, "/api/settings", nil)
	var st settings.Settings
	mustJSON(t, readAll(t, resp), &st)
	if st.InboundMode != "socks" {
		t.Errorf("default mode: %q", st.InboundMode)
	}
	// switch to tun
	resp = e.bearer(http.MethodPut, "/api/settings", []byte(`{"inbound_mode":"tun","tun_stack":"gvisor"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save settings: %d", resp.StatusCode)
	}
	// add a server and apply → config should now be tun
	e.bearer(http.MethodPost, "/api/servers", []byte(`{"type":"vless","server":"h","server_port":1,"uuid":"u"}`))
	e.bearer(http.MethodPost, "/api/servers/apply", []byte(`{}`))
	cfg, _ := e.deps.Config.Read()
	if !strings.Contains(string(cfg), `"tun"`) || !strings.Contains(string(cfg), `"gvisor"`) {
		t.Errorf("apply did not honor tun mode setting")
	}
}

func TestServers_ApplyBlocksOnBadCheck(t *testing.T) {
	e := newEnv(t)
	e.bearer(http.MethodPost, "/api/servers", []byte(`{"type":"vless","server":"h","server_port":1,"uuid":"u"}`))
	// Make `sing-box check` fail.
	e.runner.Default = cmdrun.FakeResponse{Stderr: "FATAL bad", Err: errIntentional}
	resp := e.bearer(http.MethodPost, "/api/servers/apply", []byte(`{}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on bad check, got %d", resp.StatusCode)
	}
	// config must NOT have been written.
	if _, err := e.deps.Config.Read(); err == nil {
		t.Error("config should not be written when check fails")
	}
}

func TestLogs_NoFile_EmptyLines(t *testing.T) {
	e := newEnv(t)
	resp := e.bearer(http.MethodGet, "/api/logs?tail=50", nil)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	var r logsResp
	mustJSON(t, body, &r)
	if len(r.Lines) != 0 {
		t.Errorf("expected empty lines, got %v", r.Lines)
	}
}

func TestLogs_TailLastN(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(filepath.Dir(e.paths.SingBoxLog), 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString("line-")
		sb.WriteString(strings.Repeat("x", i))
		sb.WriteString("\n")
	}
	os.WriteFile(e.paths.SingBoxLog, []byte(sb.String()), 0o644)

	resp := e.bearer(http.MethodGet, "/api/logs?tail=3", nil)
	body := readAll(t, resp)
	var r logsResp
	mustJSON(t, body, &r)
	if len(r.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d (%v)", len(r.Lines), r.Lines)
	}
	if r.Lines[2] != "line-xxxxxxxxx" {
		t.Errorf("last line: %q", r.Lines[2])
	}
}
