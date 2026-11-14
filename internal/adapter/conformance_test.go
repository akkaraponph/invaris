package adapter

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/akkaraponph/invaris/internal/domain"
	"github.com/akkaraponph/invaris/internal/runner"
)

const helperArg = "invaris-adapter-helper"

// TestMain lets the test binary act as a scripted provider. The mode travels in
// argv because the runner withholds the parent environment.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == helperArg {
		os.Exit(helper(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

func helper(mode string, args []string) int {
	switch mode {
	case "respond": // respond <exit> <template with $ID>: echoes the request ID into the template
		in, _ := io.ReadAll(os.Stdin)
		var req struct {
			RequestID string `json:"requestId"`
		}
		_ = json.Unmarshal(in, &req)
		code, _ := strconv.Atoi(args[0])
		fmt.Print(strings.ReplaceAll(args[1], "$ID", req.RequestID))
		fmt.Fprint(os.Stderr, "diagnostic on stderr only")
		return code
	case "raw-hex": // raw-hex <hex bytes>: exact bytes, no templating
		b, _ := hex.DecodeString(args[0])
		os.Stdout.Write(b)
	case "flood":
		n, _ := strconv.Atoi(args[0])
		os.Stdout.Write(bytes.Repeat([]byte("x"), n))
	case "sleep":
		time.Sleep(30 * time.Second)
	case "strict-stdin": // the request must be exactly one JSON object and a newline
		in, _ := io.ReadAll(os.Stdin)
		var req Request
		if !bytes.HasSuffix(in, []byte("\n")) || bytes.Count(in, []byte("\n")) != 1 || json.Unmarshal(in, &req) != nil || req.Version != Version || req.Operation != OpDiscover {
			return 3
		}
		fmt.Printf(`{"version":%q,"requestId":%q,"provider":{"name":"p","version":"1","toolName":null,"toolVersion":null},"capabilities":{"languages":[],"policyIds":[],"operations":["discover"],"proofTypes":[],"reportFormats":[],"available":true,"prerequisites":[]}}`, Version, req.RequestID)
	}
	return 0
}

func respondCommand(exit int, template string) runner.Command {
	return runner.Command{Program: os.Args[0], Args: []string{helperArg, "respond", strconv.Itoa(exit), template}, Timeout: 5 * time.Second}
}

func golden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func checkRequest() Request {
	return Request{RequestID: "req-1", Operation: OpCheck, PolicyIDs: []string{"no-forbidden-import"}, Scope: []string{"src"}}
}

// edited returns the clean golden response with edit applied, as a template.
func edited(t *testing.T, file string, edit func(m map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(golden(t, file)), &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	b, _ := json.Marshal(m)
	return string(b)
}

func check(m map[string]any) map[string]any { return m["check"].(map[string]any) }

func TestGoldenResponses(t *testing.T) {
	ctx := context.Background()
	resp, err := Call(ctx, t.TempDir(), respondCommand(0, golden(t, "discover.json")), Request{Operation: OpDiscover})
	if err != nil || resp.Capabilities == nil || !resp.Capabilities.Available || resp.Capabilities.PolicyIDs[0] != "no-forbidden-import" {
		t.Fatalf("discover: %+v %v", resp, err)
	}
	resp, err = Call(ctx, t.TempDir(), respondCommand(0, golden(t, "clean.json")), checkRequest())
	if err != nil || resp.Check.Status != domain.Pass || resp.Provider.ToolVersion == nil || *resp.Provider.ToolVersion != "9.1.0" {
		t.Fatalf("clean: %+v %v", resp, err)
	}
	// A policy violation is a successful, typed FAIL, not an error.
	resp, err = Call(ctx, t.TempDir(), respondCommand(0, golden(t, "violation.json")), checkRequest())
	if err != nil || resp.Check.Status != domain.Fail || len(resp.Check.Findings) != 1 || resp.Check.Findings[0].RuleID != "no-forbidden-import" || resp.Check.Findings[0].Path != "src/main.js" {
		t.Fatalf("violation: %+v %v", resp, err)
	}
	// Scope "." covers any canonical path.
	req := checkRequest()
	req.Scope = []string{"."}
	if _, err = Call(ctx, t.TempDir(), respondCommand(0, golden(t, "violation.json")), req); err != nil {
		t.Fatalf("dot scope: %v", err)
	}
	// Trailing whitespace after the single value is fine.
	if _, err = Call(ctx, t.TempDir(), respondCommand(0, golden(t, "clean.json")+"\n\n"), checkRequest()); err != nil {
		t.Fatalf("trailing whitespace: %v", err)
	}
}

func TestRequestFraming(t *testing.T) {
	cmd := runner.Command{Program: os.Args[0], Args: []string{helperArg, "strict-stdin"}, Timeout: 5 * time.Second}
	if _, err := Call(context.Background(), t.TempDir(), cmd, Request{Operation: OpDiscover}); err != nil {
		t.Fatalf("provider did not receive exactly one JSON request line: %v", err)
	}
}

func TestConformance(t *testing.T) {
	invalid := map[string]string{
		"wrong request ID":              edited(t, "clean.json", func(m map[string]any) { m["requestId"] = "someone-else" }),
		"wrong version":                 edited(t, "clean.json", func(m map[string]any) { m["version"] = "invaris.adapter/v2" }),
		"missing provider name":         edited(t, "clean.json", func(m map[string]any) { m["provider"].(map[string]any)["name"] = "" }),
		"missing provider version":      edited(t, "clean.json", func(m map[string]any) { m["provider"].(map[string]any)["version"] = " " }),
		"check from another provider":   edited(t, "clean.json", func(m map[string]any) { check(m)["providerId"] = "impostor" }),
		"unknown top-level field":       edited(t, "clean.json", func(m map[string]any) { m["extra"] = 1 }),
		"unknown nested field":          edited(t, "clean.json", func(m map[string]any) { check(m)["extra"] = 1 }),
		"unrequested policy":            edited(t, "clean.json", func(m map[string]any) { check(m)["policyId"] = "other-policy" }),
		"PASS with zero targets":        edited(t, "clean.json", func(m map[string]any) { check(m)["inspected"] = 0 }),
		"PASS without target count":     edited(t, "clean.json", func(m map[string]any) { check(m)["inspected"] = nil }),
		"PASS without exit code":        edited(t, "clean.json", func(m map[string]any) { check(m)["exitCode"] = nil }),
		"PASS with failing native exit": edited(t, "clean.json", func(m map[string]any) { check(m)["exitCode"] = 1 }),
		"PASS with crash exit":          edited(t, "clean.json", func(m map[string]any) { check(m)["exitCode"] = 127 }),
		"PASS carrying a finding":       edited(t, "violation.json", func(m map[string]any) { check(m)["status"] = "PASS"; check(m)["exitCode"] = 0 }),
		"FAIL with clean exit":          edited(t, "violation.json", func(m map[string]any) { check(m)["exitCode"] = 0 }),
		"FAIL with exit 2":              edited(t, "violation.json", func(m map[string]any) { check(m)["exitCode"] = 2 }),
		"FAIL without findings":         edited(t, "violation.json", func(m map[string]any) { check(m)["findings"] = []any{} }),
		"NOT_RUN from provider":         edited(t, "clean.json", func(m map[string]any) { check(m)["status"] = "NOT_RUN" }),
		"REFUSE without diagnostics":    edited(t, "clean.json", func(m map[string]any) { check(m)["status"] = "REFUSE" }),
		"null findings":                 edited(t, "clean.json", func(m map[string]any) { check(m)["findings"] = nil }),
		"finding for another policy":    edited(t, "violation.json", func(m map[string]any) { check(m)["findings"].([]any)[0].(map[string]any)["policyId"] = "other" }),
		"finding without native rule":   edited(t, "violation.json", func(m map[string]any) { check(m)["findings"].([]any)[0].(map[string]any)["ruleId"] = "" }),
		"absolute finding path":         edited(t, "violation.json", func(m map[string]any) { check(m)["findings"].([]any)[0].(map[string]any)["path"] = "/etc/passwd" }),
		"escaping finding path":         edited(t, "violation.json", func(m map[string]any) { check(m)["findings"].([]any)[0].(map[string]any)["path"] = "src/../../x" }),
		"backslash finding path":        edited(t, "violation.json", func(m map[string]any) { check(m)["findings"].([]any)[0].(map[string]any)["path"] = `src\main.js` }),
		"finding outside scope":         edited(t, "violation.json", func(m map[string]any) { check(m)["findings"].([]any)[0].(map[string]any)["path"] = "lib/main.js" }),
		"finding in sibling prefix":     edited(t, "violation.json", func(m map[string]any) { check(m)["findings"].([]any)[0].(map[string]any)["path"] = "src2/main.js" }),
		"capabilities for check": edited(t, "clean.json", func(m map[string]any) {
			delete(m, "check")
			m["capabilities"] = map[string]any{"languages": []any{}, "policyIds": []any{}, "operations": []any{}, "proofTypes": []any{}, "reportFormats": []any{}, "available": true, "prerequisites": []any{}}
		}),
		"check and error together": edited(t, "clean.json", func(m map[string]any) { m["error"] = map[string]any{"code": CodeInternalFailure, "message": "x"} }),
		"neither check nor error":  edited(t, "clean.json", func(m map[string]any) { delete(m, "check") }),
		"mutation for check": edited(t, "clean.json", func(m map[string]any) {
			delete(m, "check")
			m["mutation"] = map[string]any{"faultId": "f", "path": "src/a", "beforeSha256": strings.Repeat("a", 64), "afterSha256": strings.Repeat("b", 64)}
		}),
		"unknown error code": edited(t, "clean.json", func(m map[string]any) {
			delete(m, "check")
			m["error"] = map[string]any{"code": "WEIRD", "message": "x"}
		}),
		"malformed JSON":       `{"version":`,
		"empty response":       ``,
		"whitespace only":      "  \n",
		"not an object":        `[]`,
		"two JSON values":      golden(t, "clean.json") + golden(t, "clean.json"),
		"trailing garbage":     golden(t, "clean.json") + "junk",
		"duplicate key":        strings.Replace(golden(t, "clean.json"), `"status":"PASS"`, `"status":"FAIL","status":"PASS"`, 1),
		"duplicate nested key": strings.Replace(golden(t, "clean.json"), `"inspected":4`, `"inspected":0,"inspected":4`, 1),
	}
	for name, template := range invalid {
		t.Run(name, func(t *testing.T) {
			resp, err := Call(context.Background(), t.TempDir(), respondCommand(0, template), checkRequest())
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("want ErrInvalidResponse, got %v (response %+v)", err, resp)
			}
			if resp.Check != nil {
				t.Fatal("an invalid response leaked a check")
			}
		})
	}

	t.Run("invalid UTF-8", func(t *testing.T) {
		// Otherwise a valid response whose diagnostic message holds a stray byte:
		// encoding/json would silently replace it, so UTF-8 must be checked first.
		body := strings.ReplaceAll(edited(t, "clean.json", func(m map[string]any) {
			check(m)["diagnostics"] = []any{map[string]any{"code": "NOTE", "message": "BADBYTE"}}
		}), "$ID", "req-1")
		raw := hex.EncodeToString(bytes.Replace([]byte(body), []byte("BADBYTE"), []byte{'a', 0xff, 'b'}, 1))
		cmd := runner.Command{Program: os.Args[0], Args: []string{helperArg, "raw-hex", raw}, Timeout: 5 * time.Second}
		if _, err := Call(context.Background(), t.TempDir(), cmd, checkRequest()); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("got %v", err)
		}
		// The same response without the stray byte is accepted.
		ok := hex.EncodeToString([]byte(strings.Replace(body, "BADBYTE", "ab", 1)))
		cmd.Args = []string{helperArg, "raw-hex", ok}
		if _, err := Call(context.Background(), t.TempDir(), cmd, checkRequest()); err != nil {
			t.Fatalf("control response rejected: %v", err)
		}
	})
	t.Run("oversized response", func(t *testing.T) {
		cmd := runner.Command{Program: os.Args[0], Args: []string{helperArg, "flood", "70000"}, Timeout: 5 * time.Second}
		if _, err := Call(context.Background(), t.TempDir(), cmd, checkRequest()); !errors.Is(err, ErrTransport) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("nonzero transport exit with a valid violation", func(t *testing.T) {
		resp, err := Call(context.Background(), t.TempDir(), respondCommand(1, golden(t, "violation.json")), checkRequest())
		if !errors.Is(err, ErrTransport) || resp.Check != nil {
			t.Fatalf("a failing provider process must not yield a check: %+v %v", resp, err)
		}
	})
	t.Run("missing executable", func(t *testing.T) {
		_, err := Call(context.Background(), t.TempDir(), runner.Command{Program: "invaris-missing-provider-4242"}, checkRequest())
		if !errors.Is(err, ErrTransport) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		start := time.Now()
		cmd := runner.Command{Program: os.Args[0], Args: []string{helperArg, "sleep"}, Timeout: 300 * time.Millisecond}
		if _, err := Call(context.Background(), t.TempDir(), cmd, checkRequest()); !errors.Is(err, ErrTransport) || time.Since(start) > 5*time.Second {
			t.Fatalf("got %v after %v", err, time.Since(start))
		}
	})
	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Call(ctx, t.TempDir(), respondCommand(0, golden(t, "clean.json")), checkRequest()); !errors.Is(err, ErrTransport) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("unsupported capability is a typed provider error", func(t *testing.T) {
		tmpl := edited(t, "clean.json", func(m map[string]any) {
			delete(m, "check")
			m["error"] = map[string]any{"code": CodeUnsupportedCap, "message": "no such policy"}
		})
		resp, err := Call(context.Background(), t.TempDir(), respondCommand(0, tmpl), checkRequest())
		var pe *ProtocolError
		if !errors.As(err, &pe) || pe.Code != CodeUnsupportedCap || resp.Check != nil || errors.Is(err, ErrTransport) || errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("got %+v %v", resp, err)
		}
	})
	t.Run("provider refusal never accepts", func(t *testing.T) {
		tmpl := edited(t, "clean.json", func(m map[string]any) {
			check(m)["status"] = "REFUSE"
			check(m)["inspected"] = nil
			check(m)["exitCode"] = 2
			check(m)["diagnostics"] = []any{map[string]any{"code": "TOOL_CRASH", "message": "native tool crashed"}}
		})
		resp, err := Call(context.Background(), t.TempDir(), respondCommand(0, tmpl), checkRequest())
		if err != nil || resp.Check.Status != domain.Refuse {
			t.Fatalf("typed refusal: %+v %v", resp, err)
		}
	})
}

func TestInvalidRequestsNeverRunProvider(t *testing.T) {
	missing := runner.Command{Program: "invaris-missing-provider-4242"} // would be a transport error if run
	base := checkRequest
	for name, edit := range map[string]func(*Request){
		"wrong version":        func(r *Request) { r.Version = "other" },
		"unknown operation":    func(r *Request) { r.Operation = "explode" },
		"check without policy": func(r *Request) { r.PolicyIDs = nil },
		"check without scope":  func(r *Request) { r.Scope = nil },
		"duplicate policy":     func(r *Request) { r.PolicyIDs = []string{"a", "a"} },
		"escaping scope":       func(r *Request) { r.Scope = []string{"../x"} },
		"absolute scope":       func(r *Request) { r.Scope = []string{"/x"} },
		"empty segment":        func(r *Request) { r.Scope = []string{"a//b"} },
		"null options":         func(r *Request) { r.Options = json.RawMessage("null") },
		"invalid options":      func(r *Request) { r.Options = json.RawMessage("{") },
		"oversized options":    func(r *Request) { r.Options = json.RawMessage(`"` + strings.Repeat("x", MaxRequestBytes) + `"`) },
	} {
		req := base()
		edit(&req)
		if _, err := Call(context.Background(), t.TempDir(), missing, req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestMutationValidation(t *testing.T) {
	req := Request{Operation: OpMutate, PolicyIDs: []string{"p"}, Scope: []string{"src"}}
	mutation := func(edit func(m map[string]any)) string {
		m := map[string]any{"version": Version, "requestId": "$ID", "provider": map[string]any{"name": "g", "version": "1", "toolName": nil, "toolVersion": nil},
			"mutation": map[string]any{"faultId": "f", "path": "src/a.js", "beforeSha256": strings.Repeat("a", 64), "afterSha256": strings.Repeat("b", 64)}}
		edit(m)
		b, _ := json.Marshal(m)
		return string(b)
	}
	mut := func(m map[string]any) map[string]any { return m["mutation"].(map[string]any) }
	if _, err := Call(context.Background(), t.TempDir(), respondCommand(0, mutation(func(map[string]any) {})), req); err != nil {
		t.Fatalf("valid mutation: %v", err)
	}
	for name, edit := range map[string]func(map[string]any){
		"outside scope": func(m map[string]any) { mut(m)["path"] = "lib/a.js" },
		"no fault":      func(m map[string]any) { mut(m)["faultId"] = "" },
		"bad hash":      func(m map[string]any) { mut(m)["beforeSha256"] = "xyz" },
		"unchanged":     func(m map[string]any) { mut(m)["afterSha256"] = mut(m)["beforeSha256"] },
	} {
		if _, err := Call(context.Background(), t.TempDir(), respondCommand(0, mutation(edit)), req); !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// ---- Provider conformance kit -------------------------------------------------
//
// These tests exercise a real provider process. They run against the bundled
// Python example, and against any third-party provider named by
// INVARIS_CONFORMANCE_PROVIDER: the path of a JSON file holding the argv array
// (never a shell string), e.g. ["/usr/bin/node", "/path/provider.mjs"].

func providers(t *testing.T) map[string]runner.Command {
	t.Helper()
	out := map[string]runner.Command{}
	if python, err := exec.LookPath("python3"); err == nil {
		script, _ := filepath.Abs(filepath.Join("..", "..", "adapters", "example", "main.py"))
		out["example"] = runner.Command{Program: python, Args: []string{script}, Timeout: 20 * time.Second}
	} else {
		t.Log("python3 not found: bundled example provider not exercised")
	}
	if path := os.Getenv("INVARIS_CONFORMANCE_PROVIDER"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("INVARIS_CONFORMANCE_PROVIDER: %v", err)
		}
		var argv []string
		if err := json.Unmarshal(raw, &argv); err != nil || len(argv) == 0 {
			t.Fatalf("INVARIS_CONFORMANCE_PROVIDER must be a non-empty JSON array of strings: %v", err)
		}
		out["third-party"] = runner.Command{Program: argv[0], Args: argv[1:], Timeout: 30 * time.Second}
	}
	if len(out) == 0 {
		t.Skip("no provider available: python3 is absent and INVARIS_CONFORMANCE_PROVIDER is unset")
	}
	return out
}

// raw sends arbitrary bytes, bypassing Call's request validation, and returns
// the provider's stdout and process error.
func raw(command runner.Command, root string, stdin string) runner.Result {
	return runner.RunInput(context.Background(), root, command, []byte(stdin))
}

func TestProviderConformance(t *testing.T) {
	for name, command := range providers(t) {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			disc, err := Call(context.Background(), root, command, Request{Operation: OpDiscover})
			if err != nil || disc.Capabilities == nil {
				t.Fatalf("discover: %+v %v", disc, err)
			}
			ops := map[string]bool{}
			for _, op := range disc.Capabilities.Operations {
				ops[op] = true
			}
			if !ops[OpDiscover] {
				t.Error("a provider must advertise discover")
			}
			// notAccepted: a hostile request must not produce a check or mutation.
			notAccepted := func(label, request string, wantCodes ...string) {
				res := raw(command, root, request)
				if res.Err != nil { // nonzero exit is an acceptable refusal
					return
				}
				var resp Response
				d := json.NewDecoder(strings.NewReader(res.Stdout))
				if err := d.Decode(&resp); err != nil || resp.Check != nil || resp.Mutation != nil || resp.Error == nil {
					t.Errorf("%s: provider produced %q instead of a refusal", label, res.Stdout)
					return
				}
				for _, c := range wantCodes {
					if resp.Error.Code == c {
						return
					}
				}
				t.Errorf("%s: error code %s, want one of %v", label, resp.Error.Code, wantCodes)
			}
			notAccepted("unsupported protocol version", `{"version":"invaris.adapter/v0","requestId":"r","operation":"discover","policyIds":[],"scope":[]}`+"\n", CodeUnsupportedProtocol, CodeInvalidRequest)
			notAccepted("unknown operation", `{"version":"`+Version+`","requestId":"r","operation":"explode","policyIds":[],"scope":[]}`+"\n", CodeInvalidRequest, CodeUnsupportedCap)
			notAccepted("garbage request", "not json\n", CodeInvalidRequest)
			notAccepted("escaping scope", `{"version":"`+Version+`","requestId":"r","operation":"check","policyIds":["`+firstPolicy(disc)+`"],"scope":["../outside"]}`+"\n", CodeInvalidRequest, CodeUnsupportedCap)
			notAccepted("absolute scope", `{"version":"`+Version+`","requestId":"r","operation":"check","policyIds":["`+firstPolicy(disc)+`"],"scope":["/etc"]}`+"\n", CodeInvalidRequest, CodeUnsupportedCap)
			notAccepted("unknown policy", `{"version":"`+Version+`","requestId":"r","operation":"check","policyIds":["invaris-conformance-unknown-policy"],"scope":["."]}`+"\n", CodeUnsupportedCap, CodeInvalidRequest)
			if !ops[OpMutate] {
				notAccepted("unsupported mutate", `{"version":"`+Version+`","requestId":"r","operation":"mutate","policyIds":["x"],"scope":["."]}`+"\n", CodeUnsupportedCap)
			}
		})
	}
}

func firstPolicy(resp Response) string {
	if len(resp.Capabilities.PolicyIDs) > 0 {
		return resp.Capabilities.PolicyIDs[0]
	}
	return "unknown"
}

// TestExampleProvider checks clean and violating projects with the bundled
// example. It is skipped only when Python is absent.
func TestExampleProvider(t *testing.T) {
	cmd, ok := providers(t)["example"]
	if !ok {
		t.Skip("python3 not found: bundled example provider not exercised")
	}
	root := t.TempDir()
	write := func(rel, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("src/a.txt", "fine")
	write("src/deep/b.txt", "fine too")
	write("other/c.txt", "FORBIDDEN_MARKER outside the requested scope")
	req := func(scope ...string) Request {
		return Request{Operation: OpCheck, PolicyIDs: []string{"no-forbidden-marker"}, Scope: scope}
	}
	resp, err := Call(context.Background(), root, cmd, req("src"))
	if err != nil || resp.Check.Status != domain.Pass || *resp.Check.Inspected != 2 || *resp.Check.ExitCode != 0 {
		t.Fatalf("clean: %+v %v", resp.Check, err)
	}
	write("src/deep/b.txt", "has FORBIDDEN_MARKER inside")
	resp, err = Call(context.Background(), root, cmd, req("src"))
	if err != nil || resp.Check.Status != domain.Fail || len(resp.Check.Findings) != 1 || resp.Check.Findings[0].Path != "src/deep/b.txt" || resp.Check.Findings[0].RuleID != "forbidden-marker" {
		t.Fatalf("violation: %+v %v", resp.Check, err)
	}
	if _, err = Call(context.Background(), root, cmd, req("src/a.txt")); err != nil {
		t.Fatalf("file scope: %v", err)
	}
	// Options reach the provider.
	r := req("src")
	r.Options = json.RawMessage(`{"marker":"fine too"}`)
	if resp, err = Call(context.Background(), root, cmd, r); err != nil || resp.Check.Status != domain.Pass {
		t.Fatalf("options: %+v %v", resp.Check, err) // marker no longer present in b.txt
	}
	// Mutation is explicitly unsupported, never a passing result.
	_, err = Call(context.Background(), root, cmd, Request{Operation: OpMutate, PolicyIDs: []string{"no-forbidden-marker"}, Scope: []string{"src"}})
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != CodeUnsupportedCap {
		t.Fatalf("mutate: %v", err)
	}
	// Empty evidence is a refusal, not a clean result.
	if err = os.MkdirAll(filepath.Join(root, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = Call(context.Background(), root, cmd, req("empty")); !errors.As(err, &pe) || pe.Code != CodeEvidenceUnavailable {
		t.Fatalf("empty scope: %v", err)
	}
	// A symlink out of scope is not followed.
	if err = os.Symlink(filepath.Join(root, "other"), filepath.Join(root, "src", "link")); err != nil {
		t.Fatal(err)
	}
	write("src/deep/b.txt", "clean again")
	if resp, err = Call(context.Background(), root, cmd, req("src")); err != nil || resp.Check.Status != domain.Pass {
		t.Fatalf("symlink followed: %+v %v", resp.Check, err)
	}
}
