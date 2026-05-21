package remote

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/bits"
	"net/url"
	"strings"
)

const (
	DefaultLoginURL = "https://devops.aliyun.com/lingma/login"
	customAlphabet  = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
	stdAlphabet     = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
)

type LoginURL struct {
	URL   string `json:"url"`
	State string `json:"state"`
	Nonce string `json:"nonce"`
}

type LoginCallback struct {
	Params     map[string]string  `json:"params"`
	AuthParts  CallbackAuthParts  `json:"auth_parts,omitempty"`
	TokenParts CallbackTokenParts `json:"token_parts,omitempty"`
}

type CallbackAuthParts struct {
	UserID       string `json:"user_id"`
	OrgOrAccount string `json:"org_or_account"`
	Name         string `json:"name"`
}

type CallbackTokenParts struct {
	SecurityOAuthToken string `json:"security_oauth_token"`
	RefreshToken       string `json:"refresh_token"`
	ExpireTime         string `json:"expire_time"`
}

func GenerateLoginURL(port int, loginURL string, loginVersion string, redirectProxy string) LoginURL {
	if strings.TrimSpace(loginURL) == "" {
		loginURL = DefaultLoginURL
	}
	if strings.TrimSpace(loginVersion) == "" {
		loginVersion = "2"
	}
	nonce := newHexID()
	state := strings.TrimSpace(loginVersion) + "-" + nonce
	values := url.Values{
		"port":  {fmt.Sprintf("%d", port)},
		"state": {state},
	}
	if strings.TrimSpace(redirectProxy) != "" {
		values.Set("redirectProxy", strings.TrimSpace(redirectProxy))
	}
	return LoginURL{URL: strings.TrimRight(loginURL, "?") + "?" + values.Encode(), State: state, Nonce: nonce}
}

func ParseLoginCallback(raw string) (LoginCallback, error) {
	query := raw
	if strings.Contains(raw, "://") || strings.Contains(raw, "?") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return LoginCallback{}, err
		}
		query = parsed.RawQuery
	} else {
		query = strings.TrimPrefix(raw, "?")
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return LoginCallback{}, err
	}
	params := make(map[string]string, len(values))
	for key, value := range values {
		if len(value) > 0 {
			params[key] = value[len(value)-1]
		} else {
			params[key] = ""
		}
	}

	out := LoginCallback{Params: params}
	if auth := params["auth"]; auth != "" {
		parts, err := decryptCallbackParts(auth, 3)
		if err != nil {
			return LoginCallback{}, callbackDecodeError(params, "auth", err)
		}
		out.AuthParts = CallbackAuthParts{UserID: parts[0], OrgOrAccount: parts[1], Name: parts[2]}
	}
	if token := params["token"]; token != "" {
		parts, err := decryptCallbackParts(token, 3)
		if err != nil {
			return LoginCallback{}, callbackDecodeError(params, "token", err)
		}
		out.TokenParts = CallbackTokenParts{SecurityOAuthToken: parts[0], RefreshToken: parts[1], ExpireTime: parts[2]}
	}
	return out, nil
}

func callbackDecodeError(params map[string]string, field string, err error) error {
	state := strings.TrimSpace(params["state"])
	if state != "" && !strings.HasPrefix(state, "2-") {
		return fmt.Errorf("unsupported Lingma callback encoding for state=%s: %s decode failed: %w; reopen Lingma login from /api/lingma/login-url so the state starts with 2-", state, field, err)
	}
	return fmt.Errorf("decode Lingma callback %s failed: %w", field, err)
}

func BuildEncodedPayload(obj map[string]any) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"Payload":       compactJSONObject(obj),
		"EncodeVersion": "1",
	})
	if err != nil {
		return "", err
	}
	return customBase64Encode(payload), nil
}

func decryptCallbackParts(text string, expected int) ([]string, error) {
	raw, err := customBase64Decode(text)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(raw), "\n")
	if len(parts) != expected {
		return nil, fmt.Errorf("decoded %d callback parts, expected %d", len(parts), expected)
	}
	return parts, nil
}

func customBase64Encode(raw []byte) string {
	encoded := base64.StdEncoding.EncodeToString(raw)
	translated := strings.Map(func(r rune) rune {
		idx := strings.IndexRune(stdAlphabet, r)
		if idx < 0 {
			return r
		}
		return rune(customAlphabet[idx])
	}, encoded)
	pivot := callbackSplitIndex(len(translated))
	return translated[pivot:] + translated[:pivot]
}

func customBase64Decode(text string) ([]byte, error) {
	pivot := callbackSplitIndex(len(text))
	prefixLen := len(text) - pivot
	unshuffled := text[prefixLen:] + text[:prefixLen]
	translated := strings.Map(func(r rune) rune {
		idx := strings.IndexRune(customAlphabet, r)
		if idx < 0 {
			return r
		}
		return rune(stdAlphabet[idx])
	}, unshuffled)
	return base64.StdEncoding.DecodeString(translated)
}

func callbackSplitIndex(n int) int {
	hi, _ := bits.Mul64(uint64(n), 0xaaaaaaaaaaaaaaab)
	return int((uint64(n) + hi) >> 1)
}

func compactJSONObject(obj map[string]any) string {
	raw, err := json.Marshal(obj)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
