package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLoginCredentialProviderExchangesCallbackTokenAndUsesSource(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/algo/api/v3/user/status" || r.URL.Query().Get("Encode") != "1" {
			t.Fatalf("unexpected auth request path=%s query=%s", r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty for Lingma login auth", got)
		}
		raw, _ := io.ReadAll(r.Body)
		if strings.TrimSpace(string(raw)) == "" {
			t.Fatal("expected encoded auth payload")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"key":               "cosy-key",
				"encrypt_user_info": "encrypted-user",
				"uid":               "user-id",
				"expire_time":       "4102444800000",
			},
		})
	}))
	defer server.Close()

	auth := customBase64Encode([]byte("user-id\norg-id\nUser Name"))
	token := customBase64Encode([]byte("oauth-token\nrefresh-token\n4102444800000"))
	path := filepath.Join(t.TempDir(), "lingma-login.json")
	callback, err := ParseLoginCallback("http://127.0.0.1:12345/?auth=" + url.QueryEscape(auth) + "&token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(callback)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	provider := NewLoginCredentialProvider(Config{BaseURL: server.URL, AuthFile: path})
	creds, err := provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 {
		t.Fatalf("credentials len = %d, want 1", len(creds))
	}
	cred := creds[0]
	if cred.Source != "User Name" || cred.CosyKey != "cosy-key" || cred.UserID != "user-id" {
		t.Fatalf("credential = %#v", cred)
	}

	_, err = provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want cached 1", calls.Load())
	}
}

func TestLoginCredentialProviderUsesLingmaTokenFieldWithoutQwenToken(t *testing.T) {
	t.Setenv("LINGMA_TOKEN", "lingma-login-token")
	var sawToken atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		decoded, err := customBase64Decode(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		var httpPayload struct {
			Payload string `json:"Payload"`
		}
		if err := json.Unmarshal(decoded, &httpPayload); err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(httpPayload.Payload), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["Token"] != "lingma-login-token" {
			t.Fatalf("Token = %#v, want LINGMA_TOKEN", payload["Token"])
		}
		sawToken.Store(true)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"key":               "cosy-key",
				"encrypt_user_info": "encrypted-user",
				"uid":               "user-id",
			},
		})
	}))
	defer server.Close()

	provider := NewLoginCredentialProvider(Config{BaseURL: server.URL})
	if _, err := provider(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sawToken.Load() {
		t.Fatal("server did not receive Lingma token payload")
	}
}

func TestLoadLoginAccountsIgnoresTopLevelQwenToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	body := `{"accounts":[{"email":"user@example.com","token":"qwen-jwt","expires":4102444800}]}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	accounts, err := LoadLoginAccounts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("accounts = %#v, want no Lingma login accounts", accounts)
	}
}

func TestLoginCredentialProviderErrorIsSummarized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errorCode":"Forbidden"}`, http.StatusForbidden)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "lingma-logins.json")
	body := `{"accounts":[
		{"source":"a","user_id":"u1","security_oauth_token":"oauth-a","refresh_token":"refresh-a"},
		{"source":"b","user_id":"u2","security_oauth_token":"oauth-b","refresh_token":"refresh-b"},
		{"source":"c","user_id":"u3","security_oauth_token":"oauth-c","refresh_token":"refresh-c"},
		{"source":"d","user_id":"u4","security_oauth_token":"oauth-d","refresh_token":"refresh-d"}
	]}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	provider := NewLoginCredentialProvider(Config{BaseURL: server.URL, AuthFile: path})
	_, err := provider(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	text := err.Error()
	if !strings.Contains(text, "all 4 account(s) failed") || !strings.Contains(text, "...") {
		t.Fatalf("error was not summarized: %s", text)
	}
}

func TestLoginCredentialProviderMissingAuthFileAsksForLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "lingma-login.json")
	provider := NewLoginCredentialProvider(Config{AuthFile: path})
	_, err := provider(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	text := err.Error()
	if !strings.Contains(text, "/api/lingma/login-url") {
		t.Fatalf("error should point to login URL endpoint: %s", text)
	}
	if strings.Contains(text, "open "+path) || strings.Contains(text, "read remote auth file") {
		t.Fatalf("missing file leaked as hard read error: %s", text)
	}
}

func TestLoginCredentialProviderDataJSONWithoutLingmaAsksForLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	body := `{"accounts":[{"email":"user@example.com","password":"pw","token":"qwen-token","expires":4102444800}]}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	provider := NewLoginCredentialProvider(Config{AuthFile: path})
	_, err := provider(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	text := err.Error()
	if !strings.Contains(text, "/api/lingma/login-url") {
		t.Fatalf("error should point to login URL endpoint: %s", text)
	}
	if strings.Contains(text, "customLoginAuth") || strings.Contains(text, "Forbidden") {
		t.Fatalf("plain qwen account pool should not trigger unsigned Lingma login: %s", text)
	}
}

func TestSaveLoginCallbackMergesIntoDataJSONAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	body := `{
  "defaultHeaders": null,
  "defaultCookie": null,
  "accounts": [
    {"email":"user@example.com","password":"pw","token":"qwen-token","expires":4102444800}
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	auth := customBase64Encode([]byte("lingma-user\norg-id\nuser@example.com"))
	token := customBase64Encode([]byte("oauth-token\nrefresh-token\n4102444800000"))
	callback, err := ParseLoginCallback("http://127.0.0.1:3000/?auth=" + url.QueryEscape(auth) + "&token=" + url.QueryEscape(token))
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveLoginCallback(path, callback); err != nil {
		t.Fatal(err)
	}

	var saved struct {
		Accounts []struct {
			Email  string         `json:"email"`
			Token  string         `json:"token"`
			Lingma map[string]any `json:"lingma"`
		} `json:"accounts"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Accounts) != 1 {
		t.Fatalf("accounts len = %d, want merged 1", len(saved.Accounts))
	}
	if saved.Accounts[0].Token != "qwen-token" {
		t.Fatalf("qwen account fields were not preserved: %#v", saved.Accounts[0])
	}
	if saved.Accounts[0].Lingma["security_oauth_token"] != "oauth-token" {
		t.Fatalf("lingma callback was not merged: %#v", saved.Accounts[0].Lingma)
	}

	accounts, err := LoadLoginAccounts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Source != "user@example.com" || accounts[0].SecurityOAuthToken != "oauth-token" {
		t.Fatalf("loaded login accounts = %#v", accounts)
	}
}
