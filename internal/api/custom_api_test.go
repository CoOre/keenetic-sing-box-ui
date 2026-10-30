package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

const customEgress = `{
  "inbounds": [{"type": "mixed", "tag": "in-kg", "listen": "192.168.6.1", "listen_port": 1091}],
  "outbounds": [{"type": "direct", "tag": "via-kg", "bind_interface": "lo"}],
  "route": {"rules": [{"inbound": ["in-kg"], "outbound": "via-kg"}]}
}`

func putCustom(e *testEnv, content string) (int, serversApplyResp) {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"content": content, "restart": false})
	resp := e.bearer(http.MethodPut, "/api/config/custom", body)
	raw := readAll(e.t, resp)
	var out serversApplyResp
	mustJSON(e.t, raw, &out)
	return resp.StatusCode, out
}

func TestCustomWriteMergesIntoConfig(t *testing.T) {
	e := newEnv(t)
	status, out := putCustom(e, customEgress)
	if status != http.StatusOK || !out.Applied {
		t.Fatalf("status %d: %+v", status, out)
	}
	saved, err := os.ReadFile(e.deps.Custom.Path)
	if err != nil || string(saved) != customEgress {
		t.Fatalf("custom.json not saved: %q, %v", saved, err)
	}
	cfg, _ := os.ReadFile(e.paths.SingBoxConfig)
	if !strings.Contains(string(cfg), `"in-kg"`) || !strings.Contains(string(cfg), `"via-kg"`) {
		t.Fatalf("custom not merged into config.json:\n%s", cfg)
	}

	// A later plain apply keeps the stored additions.
	resp := e.bearer(http.MethodPost, "/api/servers/apply", []byte(`{"restart":false}`))
	readAll(t, resp)
	cfg, _ = os.ReadFile(e.paths.SingBoxConfig)
	if !strings.Contains(string(cfg), `"in-kg"`) {
		t.Fatal("servers/apply dropped custom.json")
	}

	// GET returns what was stored.
	resp = e.bearer(http.MethodGet, "/api/config/custom", nil)
	var got struct {
		Content string `json:"content"`
		Exists  bool   `json:"exists"`
	}
	mustJSON(t, readAll(t, resp), &got)
	if !got.Exists || got.Content != customEgress {
		t.Fatalf("GET: %+v", got)
	}

	// Blank content removes the additions.
	if status, _ := putCustom(e, ""); status != http.StatusOK {
		t.Fatalf("clear: status %d", status)
	}
	if _, err := os.Stat(e.deps.Custom.Path); !os.IsNotExist(err) {
		t.Fatal("custom.json must be removed")
	}
	cfg, _ = os.ReadFile(e.paths.SingBoxConfig)
	if strings.Contains(string(cfg), `"in-kg"`) {
		t.Fatal("cleared additions still in config.json")
	}
}

func TestCustomWriteRejectsWithoutSaving(t *testing.T) {
	cases := map[string]struct {
		content string
		want    string
	}{
		"syntax":    {"{\n  \"inbounds\": [\n    {\"type\" \"mixed\"}\n  ]\n}", "custom.json:3:"},
		"typeClash": {`{"inbounds": {"type": "mixed"}}`, "ожидается массив"},
		"badRef":    {`{"route": {"rules": [{"inbound": ["in-x"], "outbound": "nope"}]}}`, `нет outbound с тегом "nope"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			status, out := putCustom(e, c.content)
			if status != http.StatusBadRequest || out.Applied {
				t.Fatalf("status %d: %+v", status, out)
			}
			if errs := strings.Join(out.Check.Errors, "\n"); !strings.Contains(errs, c.want) {
				t.Fatalf("errors %q lack %q", errs, c.want)
			}
			if name == "syntax" && (out.Syntax == nil || out.Syntax.Line != 3) {
				t.Fatalf("syntax position: %+v", out.Syntax)
			}
			if _, err := os.Stat(e.deps.Custom.Path); !os.IsNotExist(err) {
				t.Fatal("rejected content must not be saved")
			}
			if _, err := os.Stat(e.paths.SingBoxConfig); !os.IsNotExist(err) {
				t.Fatal("rejected content must not reach config.json")
			}
		})
	}
}

func TestCustomWriteSingBoxCheckFails(t *testing.T) {
	e := newEnv(t)
	e.runner.Responses[e.paths.SingBoxBin] = cmdrun.FakeResponse{
		Stderr: "FATAL[0000] decode config: unknown field \"listen_prt\"",
		Err:    errIntentional,
	}
	status, out := putCustom(e, customEgress)
	if status != http.StatusBadRequest || out.Check.OK {
		t.Fatalf("status %d: %+v", status, out)
	}
	if _, err := os.Stat(e.deps.Custom.Path); !os.IsNotExist(err) {
		t.Fatal("content failing sing-box check must not be saved")
	}
}

func TestCustomWritePortBusy(t *testing.T) {
	e := newEnv(t)
	listenProbe = func(addr string) error {
		if strings.HasSuffix(addr, ":1091") {
			return errors.New("address already in use")
		}
		return nil
	}
	status, out := putCustom(e, customEgress)
	if status != http.StatusBadRequest {
		t.Fatalf("status %d: %+v", status, out)
	}
	if errs := strings.Join(out.Check.Errors, "\n"); !strings.Contains(errs, "192.168.6.1:1091") {
		t.Fatalf("errors: %q", errs)
	}
}

func TestCustomCheckDoesNotSave(t *testing.T) {
	e := newEnv(t)
	body, _ := json.Marshal(map[string]any{"content": `{"log": {"level": "debug"}, "outbounds": [{"type": "direct", "tag": "x", "bind_interface": "no-such-if0"}]}`})
	resp := e.bearer(http.MethodPost, "/api/config/custom/check", body)
	raw := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	var out serversApplyResp
	mustJSON(t, raw, &out)
	if !out.Check.OK {
		t.Fatalf("check: %+v", out.Check)
	}
	w := strings.Join(out.Warnings, "\n")
	if !strings.Contains(w, "переопределяет log.level") || !strings.Contains(w, `"no-such-if0"`) {
		t.Fatalf("warnings: %q", w)
	}
	if _, err := os.Stat(e.deps.Custom.Path); !os.IsNotExist(err) {
		t.Fatal("check must not save")
	}

	// A syntax error is reported in the body (200) with its position.
	body, _ = json.Marshal(map[string]any{"content": "{\"a\": }"})
	resp = e.bearer(http.MethodPost, "/api/config/custom/check", body)
	mustJSON(t, readAll(t, resp), &out)
	if out.Syntax == nil || out.Syntax.Line != 1 || out.Syntax.Col != 7 {
		t.Fatalf("syntax: %+v", out.Syntax)
	}
}

func TestBackupCarriesCustom(t *testing.T) {
	src := newEnv(t)
	if status, _ := putCustom(src, customEgress); status != http.StatusOK {
		t.Fatalf("put: %d", status)
	}
	resp := src.bearer(http.MethodGet, "/api/backup/export", nil)
	arch := readAll(t, resp)

	dst := newEnv(t)
	resp = dst.bearer(http.MethodPost, "/api/backup/import", arch)
	if body := readAll(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("import %d: %s", resp.StatusCode, body)
	}
	got, err := os.ReadFile(dst.deps.Custom.Path)
	if err != nil || string(got) != customEgress {
		t.Fatalf("custom.json not restored: %q, %v", got, err)
	}
}
