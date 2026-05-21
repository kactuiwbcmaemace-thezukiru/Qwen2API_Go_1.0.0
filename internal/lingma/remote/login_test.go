package remote

import (
	"net/url"
	"strings"
	"testing"
)

func TestCustomBase64RoundTrip(t *testing.T) {
	raw := []byte("user-1\norg-1\nname")
	encoded := customBase64Encode(raw)
	decoded, err := customBase64Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(raw) {
		t.Fatalf("decoded = %q, want %q", decoded, raw)
	}
}

func TestParseLoginCallbackDecodesAuthAndToken(t *testing.T) {
	auth := customBase64Encode([]byte("user-1\norg-1\nUser Name"))
	token := customBase64Encode([]byte("oauth-token\nrefresh-token\n4102444800000"))
	callback := "http://127.0.0.1:12345/?state=2-test&auth=" + url.QueryEscape(auth) + "&token=" + url.QueryEscape(token)

	got, err := ParseLoginCallback(callback)
	if err != nil {
		t.Fatal(err)
	}
	if got.Params["state"] != "2-test" || got.AuthParts.UserID != "user-1" || got.AuthParts.OrgOrAccount != "org-1" || got.AuthParts.Name != "User Name" {
		t.Fatalf("unexpected auth callback = %#v", got)
	}
	if got.TokenParts.SecurityOAuthToken != "oauth-token" || got.TokenParts.RefreshToken != "refresh-token" || got.TokenParts.ExpireTime != "4102444800000" {
		t.Fatalf("unexpected token callback = %#v", got.TokenParts)
	}
}

func TestParseLoginCallbackRejectsNonV2StateWithActionableError(t *testing.T) {
	callback := "http://127.0.0.1:3000/auth/callback?state=83a736c737b7f06da65bdaad41ff5b14&auth=grPxL.uMB%25lR%23%28j%40%23YjM_%24n%25lR_Ql%40N.V%40N%5EVMNIlRp%5EoOV%40%26fl%40zkj%40TkV%5EBklR%26wVf&token=pIPRLDPkDD%23pn%25j%5E%26wj%40a%28jM%26Il%40a%5EjZPaK*oEKwG%40gLJ.dDJ%28LKJTjgmO%25ZuTNZptaZJa%23%5EDhPEKTV%40JKC%40LdCMjYVz"
	_, err := ParseLoginCallback(callback)
	if err == nil {
		t.Fatal("expected parse error")
	}
	text := err.Error()
	if !strings.Contains(text, "unsupported Lingma callback encoding") || !strings.Contains(text, "/api/lingma/login-url") {
		t.Fatalf("error = %q, want actionable v2 login hint", text)
	}
}

func TestGenerateLoginURL(t *testing.T) {
	got := GenerateLoginURL(34567, "", "2", "http://127.0.0.1/proxy")
	if !strings.HasPrefix(got.URL, DefaultLoginURL+"?") || !strings.HasPrefix(got.State, "2-") || got.Nonce == "" {
		t.Fatalf("unexpected login URL = %#v", got)
	}
	parsed, err := url.Parse(got.URL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("port") != "34567" || parsed.Query().Get("state") != got.State || parsed.Query().Get("redirectProxy") == "" {
		t.Fatalf("unexpected query = %s", parsed.RawQuery)
	}
}

func TestBuildEncodedPayloadWrapsCustomEncodedHTTPPayload(t *testing.T) {
	encoded, err := BuildEncodedPayload(map[string]any{"Ak": "ak", "NeedRefresh": false})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := customBase64Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	text := string(decoded)
	if !strings.Contains(text, `"EncodeVersion":"1"`) || !strings.Contains(text, `"Payload":"`) || !strings.Contains(text, `\"Ak\":\"ak\"`) {
		t.Fatalf("unexpected decoded payload = %s", text)
	}
}
