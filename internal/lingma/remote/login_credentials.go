package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type CredentialProvider func(context.Context) ([]Credential, error)

type LoginAccount struct {
	Source             string
	UserID             string
	OrgID              string
	AuthUserName       string
	AuthOrgID          string
	Token              string
	SecurityOAuthToken string
	RefreshToken       string
	ExpireTime         string
	PersonalToken      string
	AK                 string
	SK                 string
}

type loginCredentialProvider struct {
	cfg    Config
	client *http.Client

	mu    sync.Mutex
	cache map[string]Credential
	next  atomic.Uint64
}

func NewLoginCredentialProvider(cfg Config) CredentialProvider {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return (&loginCredentialProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
		cache:  map[string]Credential{},
	}).Credentials
}

func (p *loginCredentialProvider) Credentials(ctx context.Context) ([]Credential, error) {
	direct, directErr := LoadExplicitCredentials(p.cfg.AuthFile)
	if len(direct) > 0 {
		return direct, nil
	}

	accounts, err := LoadLoginAccounts(p.cfg.AuthFile)
	if err != nil {
		if directErr != nil {
			return nil, fmt.Errorf("%v; %v", directErr, err)
		}
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, errors.New("no Lingma login credential configured; open /api/lingma/login-url with an admin key, complete the browser login, then retry")
	}

	start := int(p.next.Add(1)-1) % len(accounts)
	now := time.Now().UnixMilli()
	var attempts []string
	for i := 0; i < len(accounts); i++ {
		account := accounts[(start+i)%len(accounts)]
		cacheKey := account.cacheKey()
		if cred, ok := p.cached(cacheKey, now); ok {
			return []Credential{cred}, nil
		}
		cred, err := p.exchange(ctx, account)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("%s: %v", account.source(), err))
			continue
		}
		p.store(cacheKey, cred)
		return []Credential{cred}, nil
	}
	return nil, fmt.Errorf("load Lingma credentials from login data: all %d account(s) failed; first errors: %s", len(accounts), strings.Join(firstItems(attempts, 3), "; "))
}

func (p *loginCredentialProvider) cached(key string, nowMillis int64) (Credential, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cred, ok := p.cache[key]
	if !ok {
		return Credential{}, false
	}
	if cred.TokenExpireTime > 0 && cred.TokenExpireTime-nowMillis < int64(5*time.Minute/time.Millisecond) {
		delete(p.cache, key)
		return Credential{}, false
	}
	return cred, true
}

func (p *loginCredentialProvider) store(key string, cred Credential) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[key] = cred
}

func (p *loginCredentialProvider) exchange(ctx context.Context, account LoginAccount) (Credential, error) {
	baseURL := ResolveBaseURL(p.cfg.BaseURL)
	machineID := defaultMachineID()
	var attempts []string

	if account.hasLoginToken() {
		for _, payload := range account.statusPayloads() {
			raw, err := p.postEncoded(ctx, baseURL, "/api/v3/user/status", payload)
			if err != nil {
				attempts = append(attempts, err.Error())
				continue
			}
			cred, err := credentialFromAuthResponse(raw, account.source(), machineID)
			if err == nil {
				_ = SaveLingmaCredential(p.cfg.AuthFile, account.source(), cred)
				return cred, nil
			}
			attempts = append(attempts, err.Error())
		}
	}
	if account.PersonalToken != "" || account.AK != "" || account.SK != "" {
		raw, err := p.postEncoded(ctx, baseURL, "/api/v3/user/grantAuthInfos", account.grantPayload())
		if err == nil {
			cred, parseErr := credentialFromAuthResponse(raw, account.source(), machineID)
			if parseErr == nil {
				_ = SaveLingmaCredential(p.cfg.AuthFile, account.source(), cred)
				return cred, nil
			}
			err = parseErr
		}
		attempts = append(attempts, err.Error())
	}
	return Credential{}, fmt.Errorf("exchange Lingma login token failed: %s", strings.Join(firstItems(attempts, 3), "; "))
}

func (p *loginCredentialProvider) postEncoded(ctx context.Context, baseURL string, path string, payload map[string]any) ([]byte, error) {
	body, err := BuildEncodedPayload(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path+"?Encode=1", bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("X-Request-ID", newHexID())

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Lingma auth status %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}
	return raw, nil
}

func (a LoginAccount) statusPayloads() []map[string]any {
	payload := authQueryPayload()
	payload["UserId"] = a.UserID
	payload["OrgId"] = a.OrgID
	payload["Token"] = a.Token
	payload["SecurityOauthToken"] = a.SecurityOAuthToken
	payload["RefreshToken"] = a.RefreshToken
	payload["NeedRefresh"] = false
	if a.PersonalToken != "" {
		payload["PersonalToken"] = a.PersonalToken
	}
	out := []map[string]any{payload}
	if a.RefreshToken != "" {
		refresh := cloneMap(payload)
		refresh["NeedRefresh"] = true
		out = append(out, refresh)
	}
	return out
}

func (a LoginAccount) grantPayload() map[string]any {
	payload := authQueryPayload()
	payload["UserId"] = a.UserID
	payload["PersonalToken"] = a.PersonalToken
	payload["Ak"] = a.AK
	payload["Sk"] = a.SK
	return payload
}

func (a LoginAccount) customLoginPayload() map[string]any {
	payload := authQueryPayload()
	orgID := strings.TrimSpace(a.AuthOrgID)
	if orgID == "" {
		orgID = strings.TrimSpace(a.OrgID)
	}
	if orgID == "" {
		orgID = strings.TrimSpace(a.AuthUserName)
	}
	payload["AuthInfo"] = map[string]any{
		"UserName": strings.TrimSpace(a.AuthUserName),
		"OrgId":    orgID,
	}
	return payload
}

func authQueryPayload() map[string]any {
	return map[string]any{
		"Ak":                 "",
		"Sk":                 "",
		"SecurityToken":      "",
		"UserId":             "",
		"OrgId":              "",
		"Token":              "",
		"PersonalToken":      "",
		"SecurityOauthToken": "",
		"RefreshToken":       "",
		"NeedRefresh":        false,
		"AuthInfo": map[string]any{
			"UserName": "",
			"OrgId":    "",
		},
	}
}

func cloneMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (a LoginAccount) source() string {
	if strings.TrimSpace(a.Source) != "" {
		return strings.TrimSpace(a.Source)
	}
	if strings.TrimSpace(a.AuthUserName) != "" {
		return strings.TrimSpace(a.AuthUserName)
	}
	if strings.TrimSpace(a.UserID) != "" {
		return strings.TrimSpace(a.UserID)
	}
	return "lingma-login"
}

func (a LoginAccount) cacheKey() string {
	return strings.Join([]string{
		a.source(),
		strings.TrimSpace(a.UserID),
		strings.TrimSpace(a.OrgID),
		strings.TrimSpace(a.AuthUserName),
		strings.TrimSpace(a.AuthOrgID),
		strings.TrimSpace(a.Token),
		strings.TrimSpace(a.SecurityOAuthToken),
		strings.TrimSpace(a.RefreshToken),
		strings.TrimSpace(a.PersonalToken),
		strings.TrimSpace(a.AK),
		strings.TrimSpace(a.SK),
	}, "\x00")
}

func (a LoginAccount) hasLoginToken() bool {
	return strings.TrimSpace(a.SecurityOAuthToken) != "" ||
		strings.TrimSpace(a.Token) != "" ||
		strings.TrimSpace(a.RefreshToken) != ""
}

func firstItems(items []string, limit int) []string {
	if len(items) <= limit {
		return items
	}
	return append(append([]string{}, items[:limit]...), fmt.Sprintf("... %d more", len(items)-limit))
}

func LoadLoginAccounts(authFile string) ([]LoginAccount, error) {
	accounts := loginAccountsFromEnv()
	if paths := parseCSV(authFile); len(paths) > 0 {
		var attempts []string
		for _, path := range paths {
			items, err := loadLoginAccountsFile(expandHome(path))
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				attempts = append(attempts, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			accounts = append(accounts, items...)
		}
		if len(accounts) > 0 {
			return accounts, nil
		}
		if len(attempts) == 0 {
			return accounts, nil
		}
		return nil, fmt.Errorf("load Lingma login auth files: %s", strings.Join(attempts, "; "))
	}
	return accounts, nil
}

func loginAccountsFromEnv() []LoginAccount {
	userIDs := parseCSV(os.Getenv("LINGMA_LOGIN_USER_ID"))
	orgIDs := parseCSV(os.Getenv("LINGMA_LOGIN_ORG_ID"))
	sources := parseCSV(os.Getenv("LINGMA_LOGIN_SOURCE"))
	oauthTokens := parseCSV(os.Getenv("LINGMA_SECURITY_OAUTH_TOKEN"))
	refreshTokens := parseCSV(os.Getenv("LINGMA_REFRESH_TOKEN"))
	loginTokens := parseCSV(os.Getenv("LINGMA_TOKEN"))
	expireTimes := parseCSV(os.Getenv("LINGMA_LOGIN_EXPIRE_TIME"))
	personalTokens := parseCSV(os.Getenv("LINGMA_PERSONAL_TOKEN"))
	aks := parseCSV(os.Getenv("LINGMA_AK"))
	sks := parseCSV(os.Getenv("LINGMA_SK"))
	maxLen := max(len(userIDs), len(orgIDs), len(sources), len(loginTokens), len(oauthTokens), len(refreshTokens), len(personalTokens), len(aks), len(sks))
	out := make([]LoginAccount, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		account := LoginAccount{
			Source:             pickCSV(sources, i),
			UserID:             pickCSV(userIDs, i),
			OrgID:              pickCSV(orgIDs, i),
			AuthUserName:       pickCSV(sources, i),
			AuthOrgID:          pickCSV(orgIDs, i),
			Token:              pickCSV(loginTokens, i),
			SecurityOAuthToken: pickCSV(oauthTokens, i),
			RefreshToken:       pickCSV(refreshTokens, i),
			ExpireTime:         pickCSV(expireTimes, i),
			PersonalToken:      pickCSV(personalTokens, i),
			AK:                 pickCSV(aks, i),
			SK:                 pickCSV(sks, i),
		}
		if account.valid() {
			out = append(out, account)
		}
	}
	return out
}

func (a LoginAccount) valid() bool {
	return strings.TrimSpace(a.SecurityOAuthToken) != "" ||
		strings.TrimSpace(a.Token) != "" ||
		strings.TrimSpace(a.RefreshToken) != "" ||
		strings.TrimSpace(a.PersonalToken) != "" ||
		strings.TrimSpace(a.AK) != "" ||
		strings.TrimSpace(a.SK) != ""
}

func loadLoginAccountsFile(path string) ([]LoginAccount, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := collectLoginAccounts(raw, path)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func SaveLoginCallback(path string, callback LoginCallback) error {
	path = strings.TrimSpace(path)
	if path == "" {
		path = filepath.Join("data", "data.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	var existing map[string]any
	if body, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(body))) > 0 {
		_ = json.Unmarshal(body, &existing)
	}
	if existing == nil {
		existing = map[string]any{}
	}

	accounts, _ := existing["accounts"].([]any)
	item := map[string]any{
		"source":               strings.TrimSpace(callback.AuthParts.Name),
		"user_id":              callback.AuthParts.UserID,
		"org_id":               callback.AuthParts.OrgOrAccount,
		"security_oauth_token": callback.TokenParts.SecurityOAuthToken,
		"refresh_token":        callback.TokenParts.RefreshToken,
		"expire_time":          callback.TokenParts.ExpireTime,
		"params":               callback.Params,
		"saved_at":             time.Now().Unix(),
	}
	if item["source"] == "" {
		item["source"] = callback.AuthParts.UserID
	}

	replaced := false
	userID := strings.TrimSpace(callback.AuthParts.UserID)
	source := strings.TrimSpace(fmt.Sprint(item["source"]))
	for i, raw := range accounts {
		account, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		existingUser := ""
		if lingma, ok := account["lingma"].(map[string]any); ok {
			existingUser = stringField(lingma, "user_id", "userId")
		}
		email := stringField(account, "email")
		if source != "" && email != "" && strings.EqualFold(email, source) {
			account["lingma"] = item
			accounts[i] = account
			replaced = true
			break
		}
		if userID != "" && strings.EqualFold(existingUser, userID) {
			account["lingma"] = item
			accounts[i] = account
			replaced = true
			break
		}
	}
	if !replaced {
		email := source
		if email == "" {
			email = userID
		}
		accounts = append(accounts, map[string]any{
			"email":  email,
			"lingma": item,
		})
	}
	existing["accounts"] = accounts

	body, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0644)
}

func SaveLingmaCredential(path string, source string, cred Credential) error {
	path = strings.TrimSpace(path)
	if path == "" {
		path = filepath.Join("data", "data.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	var existing map[string]any
	if body, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(body))) > 0 {
		_ = json.Unmarshal(body, &existing)
	}
	if existing == nil {
		existing = map[string]any{}
	}

	accounts, _ := existing["accounts"].([]any)
	item := map[string]any{
		"source":            valueOr(strings.TrimSpace(source), strings.TrimSpace(cred.Source)),
		"token_expire_time": fmt.Sprintf("%d", cred.TokenExpireTime),
		"auth": map[string]any{
			"cosy_key":          cred.CosyKey,
			"encrypt_user_info": cred.EncryptUserInfo,
			"user_id":           cred.UserID,
			"machine_id":        cred.MachineID,
		},
		"saved_at": time.Now().Unix(),
	}

	replaced := false
	lookup := strings.TrimSpace(source)
	for i, raw := range accounts {
		account, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		email := stringField(account, "email")
		lingma, _ := account["lingma"].(map[string]any)
		existingUser := ""
		if lingma != nil {
			existingUser = stringField(lingma, "user_id", "userId")
			for k, v := range item {
				lingma[k] = v
			}
		}
		if lingma == nil {
			lingma = item
		}
		if lookup != "" && email != "" && strings.EqualFold(email, lookup) {
			account["lingma"] = lingma
			accounts[i] = account
			replaced = true
			break
		}
		if cred.UserID != "" && strings.EqualFold(existingUser, cred.UserID) {
			account["lingma"] = lingma
			accounts[i] = account
			replaced = true
			break
		}
	}
	if !replaced {
		email := valueOr(lookup, cred.Source)
		if strings.TrimSpace(email) == "" {
			email = cred.UserID
		}
		accounts = append(accounts, map[string]any{
			"email":  email,
			"lingma": item,
		})
	}
	existing["accounts"] = accounts

	body, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0644)
}

func collectLoginAccounts(raw any, source string) []LoginAccount {
	switch value := raw.(type) {
	case []any:
		var out []LoginAccount
		for i, item := range value {
			out = append(out, collectLoginAccounts(item, fmt.Sprintf("%s#%d", source, i+1))...)
		}
		return out
	case map[string]any:
		if list, ok := value["accounts"].([]any); ok {
			var out []LoginAccount
			for i, item := range list {
				out = append(out, collectLoginAccounts(item, fmt.Sprintf("%s#%d", source, i+1))...)
			}
			return out
		}
		account := loginAccountFromMap(value, source)
		if account.valid() {
			return []LoginAccount{account}
		}
	}
	return nil
}

func collectAutoLoginAccounts(raw any, source string) []LoginAccount {
	switch value := raw.(type) {
	case []any:
		var out []LoginAccount
		for i, item := range value {
			out = append(out, collectAutoLoginAccounts(item, fmt.Sprintf("%s#%d", source, i+1))...)
		}
		return out
	case map[string]any:
		if list, ok := value["accounts"].([]any); ok {
			var out []LoginAccount
			for i, item := range list {
				out = append(out, collectAutoLoginAccounts(item, fmt.Sprintf("%s#%d", source, i+1))...)
			}
			return out
		}
		email := stringField(value, "email")
		if email == "" {
			return nil
		}
		orgID := ""
		if lingma, ok := value["lingma"].(map[string]any); ok {
			orgID = stringField(lingma, "org_id", "orgId", "org_or_account", "orgOrAccount")
		}
		if orgID == "" {
			orgID = email
		}
		return []LoginAccount{{
			Source:       email,
			AuthUserName: email,
			AuthOrgID:    orgID,
		}}
	}
	return nil
}

func appendMissingLoginAccounts(accounts []LoginAccount, candidates []LoginAccount) []LoginAccount {
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		seen[strings.ToLower(account.source())] = struct{}{}
	}
	for _, candidate := range candidates {
		if !candidate.valid() {
			continue
		}
		key := strings.ToLower(candidate.source())
		if _, ok := seen[key]; ok {
			continue
		}
		accounts = append(accounts, candidate)
		seen[key] = struct{}{}
	}
	return accounts
}

func loginAccountFromMap(value map[string]any, source string) LoginAccount {
	nestedLogin := false
	if nested, ok := firstNestedLoginMap(value); ok {
		nestedLogin = true
		for k, v := range value {
			if isParentAccountField(k) {
				continue
			}
			if _, exists := nested[k]; !exists {
				nested[k] = v
			}
		}
		value = nested
	}
	account := LoginAccount{
		Source:             stringField(value, "source", "email", "name"),
		UserID:             stringField(value, "user_id", "userId", "UserId"),
		OrgID:              stringField(value, "org_id", "orgId", "OrgId", "org_or_account", "orgOrAccount"),
		AuthUserName:       stringField(value, "auth_user_name", "authUserName"),
		AuthOrgID:          stringField(value, "auth_org_id", "authOrgId"),
		Token:              lingmaTokenField(value, nestedLogin),
		SecurityOAuthToken: stringField(value, "security_oauth_token", "securityOAuthToken", "SecurityOauthToken"),
		RefreshToken:       stringField(value, "refresh_token", "refreshToken", "RefreshToken"),
		ExpireTime:         stringField(value, "expire_time", "expireTime", "token_expire_time"),
		PersonalToken:      stringField(value, "personal_token", "personalToken", "PersonalToken"),
		AK:                 stringField(value, "ak", "Ak", "access_key_id", "accessKeyId"),
		SK:                 stringField(value, "sk", "Sk", "access_key_secret", "accessKeySecret"),
	}
	if account.Source == "" {
		account.Source = source
	}
	if auth, ok := value["auth_parts"].(map[string]any); ok {
		if account.UserID == "" {
			account.UserID = stringField(auth, "user_id", "userId")
		}
		if account.OrgID == "" {
			account.OrgID = stringField(auth, "org_or_account", "org_id", "orgId")
		}
		if account.Source == source {
			if name := stringField(auth, "name"); name != "" {
				account.Source = name
			}
		}
	}
	if token, ok := value["token_parts"].(map[string]any); ok {
		if account.SecurityOAuthToken == "" {
			account.SecurityOAuthToken = stringField(token, "security_oauth_token", "securityOAuthToken")
		}
		if account.RefreshToken == "" {
			account.RefreshToken = stringField(token, "refresh_token", "refreshToken")
		}
		if account.ExpireTime == "" {
			account.ExpireTime = stringField(token, "expire_time", "expireTime")
		}
	}
	return account
}

func lingmaTokenField(value map[string]any, nestedLogin bool) string {
	keys := []string{"lingma_token", "lingmaToken", "login_token", "loginToken", "Token"}
	if nestedLogin {
		keys = append(keys, "token")
	}
	for _, key := range keys {
		if v, ok := value[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(v))
			if text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func isParentAccountField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "password", "token", "expires":
		return true
	default:
		return false
	}
}

func firstNestedLoginMap(value map[string]any) (map[string]any, bool) {
	for _, key := range []string{"lingma", "lingma_login", "lingmaLogin", "login"} {
		if nested, ok := value[key].(map[string]any); ok {
			return nested, true
		}
	}
	return nil, false
}

func stringField(value map[string]any, keys ...string) string {
	for _, key := range keys {
		for k, v := range value {
			if strings.EqualFold(k, key) {
				text := strings.TrimSpace(fmt.Sprint(v))
				if text != "" && text != "<nil>" {
					return text
				}
			}
		}
	}
	return ""
}

func credentialFromAuthResponse(raw []byte, source string, machineID string) (Credential, error) {
	payload, err := decodeAuthResponse(raw)
	if err != nil {
		return Credential{}, err
	}
	cred := Credential{
		CosyKey:         firstString(payload, "cosy_key", "cosyKey", "key", "Key"),
		EncryptUserInfo: firstString(payload, "encrypt_user_info", "encryptUserInfo"),
		UserID:          firstString(payload, "user_id", "userId", "uid", "UserId"),
		MachineID:       machineID,
		Source:          source,
		TokenExpireTime: firstExpire(payload, "token_expire_time", "expire_time", "expireTime"),
	}
	if mid := firstString(payload, "machine_id", "machineId"); mid != "" {
		cred.MachineID = mid
	}
	if err := validateCredential(cred); err != nil {
		return Credential{}, fmt.Errorf("%w body=%s", err, truncate(string(raw), 500))
	}
	return cred, nil
}

func decodeAuthResponse(raw []byte) (any, error) {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse Lingma auth response: %w body=%s", err, truncate(string(raw), 300))
	}
	if encoded := firstString(payload, "Payload", "payload"); encoded != "" {
		decoded, err := customBase64Decode(encoded)
		if err != nil {
			return nil, err
		}
		var inner any
		if err := json.Unmarshal(decoded, &inner); err == nil {
			return inner, nil
		}
		var text string
		if err := json.Unmarshal(decoded, &text); err == nil {
			if err := json.Unmarshal([]byte(text), &inner); err == nil {
				return inner, nil
			}
		}
	}
	return payload, nil
}

func firstString(value any, keys ...string) string {
	for _, key := range keys {
		if found, ok := findValue(value, key); ok {
			if text := strings.TrimSpace(fmt.Sprint(found)); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func firstExpire(value any, keys ...string) int64 {
	for _, key := range keys {
		if found, ok := findValue(value, key); ok {
			if exp := parseExpireAny(found); exp > 0 {
				return exp
			}
		}
	}
	return 0
}

func findValue(value any, key string) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for k, v := range typed {
			if strings.EqualFold(k, key) {
				return v, true
			}
		}
		for _, v := range typed {
			if found, ok := findValue(v, key); ok {
				return found, true
			}
		}
	case []any:
		for _, item := range typed {
			if found, ok := findValue(item, key); ok {
				return found, true
			}
		}
	}
	return nil, false
}
