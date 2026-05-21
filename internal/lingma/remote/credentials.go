package remote

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Credential struct {
	CosyKey         string
	EncryptUserInfo string
	UserID          string
	MachineID       string
	Source          string
	TokenExpireTime int64
}

type storedCredentialFile struct {
	Email           string `json:"email"`
	Source          string `json:"source"`
	TokenExpireTime string `json:"token_expire_time"`
	Auth            struct {
		CosyKey         string `json:"cosy_key"`
		EncryptUserInfo string `json:"encrypt_user_info"`
		UserID          string `json:"user_id"`
		MachineID       string `json:"machine_id"`
	} `json:"auth"`
	Lingma *storedCredentialFile `json:"lingma"`
}

type storedCredentialBundle struct {
	Accounts []storedCredentialFile `json:"accounts"`
}

var errNoExplicitCredential = errors.New("Lingma explicit COSY credential is not configured")

func LoadCredential(authFile string) (Credential, error) {
	creds, err := LoadCredentials(authFile)
	if err != nil {
		return Credential{}, err
	}
	if len(creds) == 0 {
		return Credential{}, errors.New("no Lingma remote credential was loaded")
	}
	return creds[0], nil
}

func LoadCredentials(authFile string) ([]Credential, error) {
	if creds, err := LoadExplicitCredentials(authFile); err == nil && len(creds) > 0 {
		return creds, nil
	}

	cred, err := importLingmaCacheCredential()
	if err != nil {
		return nil, err
	}
	return []Credential{cred}, nil
}

func LoadExplicitCredentials(authFile string) ([]Credential, error) {
	if creds, err := loadEnvCredentials(); err == nil && len(creds) > 0 {
		return creds, nil
	}

	if paths := parseCSV(authFile); len(paths) > 0 {
		var out []Credential
		var attempts []string
		for _, path := range paths {
			creds, err := loadCredentialFile(expandHome(path))
			if err != nil {
				if errors.Is(err, os.ErrNotExist) || errors.Is(err, errNoExplicitCredential) {
					continue
				}
				attempts = append(attempts, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			out = append(out, creds...)
		}
		if len(out) > 0 {
			return out, nil
		}
		if len(attempts) == 0 {
			return nil, errors.New("Lingma explicit COSY credential is not configured")
		}
		return nil, fmt.Errorf("load Lingma remote auth files: %s", strings.Join(attempts, "; "))
	}
	return nil, errNoExplicitCredential
}

func loadEnvCredentials() ([]Credential, error) {
	userIDs := parseCSV(os.Getenv("LINGMA_COSY_USER"))
	keys := parseCSV(os.Getenv("LINGMA_COSY_KEY"))
	infos := parseCSV(os.Getenv("LINGMA_AUTH_INFO"))
	machineIDs := parseCSV(os.Getenv("LINGMA_MACHINE_ID"))
	if len(userIDs) == 0 && len(keys) == 0 && len(infos) == 0 {
		return nil, errors.New("Lingma env credential is not configured")
	}

	maxLen := max(len(userIDs), len(keys), len(infos), len(machineIDs))
	out := make([]Credential, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		cred := Credential{
			UserID:          pickCSV(userIDs, i),
			CosyKey:         pickCSV(keys, i),
			EncryptUserInfo: pickCSV(infos, i),
			MachineID:       valueOr(pickCSV(machineIDs, i), defaultMachineID()),
			Source:          "environment",
		}
		if err := validateCredential(cred); err != nil {
			return nil, err
		}
		out = append(out, cred)
	}
	return out, nil
}

func loadCredentialFile(path string) ([]Credential, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read remote auth file: %w", err)
	}

	var bundle storedCredentialBundle
	if err := json.Unmarshal(body, &bundle); err == nil && len(bundle.Accounts) > 0 {
		return storedCredentials(bundle.Accounts, path)
	}

	var list []storedCredentialFile
	if err := json.Unmarshal(body, &list); err == nil && len(list) > 0 {
		return storedCredentials(list, path)
	}

	var stored storedCredentialFile
	if err := json.Unmarshal(body, &stored); err != nil {
		return nil, fmt.Errorf("parse remote auth file: %w", err)
	}
	return storedCredentials([]storedCredentialFile{stored}, path)
}

func storedCredentials(items []storedCredentialFile, path string) ([]Credential, error) {
	out := make([]Credential, 0, len(items))
	for i, stored := range items {
		if stored.Lingma != nil {
			nested := *stored.Lingma
			if nested.Source == "" {
				nested.Source = valueOr(stored.Email, stored.Source)
			}
			stored = nested
		}
		if strings.TrimSpace(stored.Auth.CosyKey) == "" &&
			strings.TrimSpace(stored.Auth.EncryptUserInfo) == "" &&
			strings.TrimSpace(stored.Auth.UserID) == "" {
			continue
		}
		source := valueOr(stored.Source, path)
		if len(items) > 1 && stored.Source == "" {
			source = fmt.Sprintf("%s#%d", path, i+1)
		}
		cred := Credential{
			CosyKey:         stored.Auth.CosyKey,
			EncryptUserInfo: stored.Auth.EncryptUserInfo,
			UserID:          stored.Auth.UserID,
			MachineID:       stored.Auth.MachineID,
			Source:          source,
			TokenExpireTime: parseExpire(stored.TokenExpireTime),
		}
		if err := validateCredential(cred); err != nil {
			return nil, err
		}
		out = append(out, cred)
	}
	if len(out) == 0 {
		return nil, errNoExplicitCredential
	}
	return out, nil
}

func pickCSV(values []string, index int) string {
	if len(values) == 0 {
		return ""
	}
	if index < len(values) {
		return strings.TrimSpace(values[index])
	}
	return strings.TrimSpace(values[len(values)-1])
}

func defaultMachineID() string {
	if value := strings.TrimSpace(os.Getenv("LINGMA_MACHINE_ID")); value != "" {
		return value
	}
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host) + "-" + newHexID()
	}
	return newHexID()
}

func importLingmaCacheCredential() (Credential, error) {
	var attempts []string
	for _, lingmaDir := range candidateLingmaCacheDirs() {
		cred, err := importLingmaCacheCredentialFromDir(lingmaDir)
		if err == nil {
			return cred, nil
		}
		attempts = append(attempts, fmt.Sprintf("%s: %v", lingmaDir, err))
	}
	if len(attempts) == 0 {
		return Credential{}, errors.New("no Lingma cache directory candidate was found")
	}
	return Credential{}, fmt.Errorf("load Lingma login cache: %s", strings.Join(attempts, "; "))
}

func importLingmaCacheCredentialFromDir(lingmaDir string) (Credential, error) {
	userPath := filepath.Join(lingmaDir, "cache", "user")
	encrypted, err := os.ReadFile(userPath)
	if err != nil {
		return Credential{}, fmt.Errorf("read %s: %w", userPath, err)
	}
	machineID, err := loadMachineID(lingmaDir)
	if err != nil {
		return Credential{}, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encrypted)))
	if err != nil {
		return Credential{}, fmt.Errorf("decode %s: %w", userPath, err)
	}
	plaintext, err := decryptCacheUser(machineID, ciphertext)
	if err != nil {
		return Credential{}, err
	}
	var payload struct {
		Key             string `json:"key"`
		EncryptUserInfo string `json:"encrypt_user_info"`
		UserID          string `json:"uid"`
		ExpireTime      any    `json:"expire_time"`
	}
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return Credential{}, fmt.Errorf("parse %s: %w", userPath, err)
	}
	cred := Credential{
		CosyKey:         payload.Key,
		EncryptUserInfo: payload.EncryptUserInfo,
		UserID:          payload.UserID,
		MachineID:       machineID,
		Source:          userPath,
		TokenExpireTime: parseExpireAny(payload.ExpireTime),
	}
	return cred, validateCredential(cred)
}

func candidateLingmaCacheDirs() []string {
	if explicit := strings.TrimSpace(os.Getenv("LINGMA_CACHE_DIR")); explicit != "" {
		return []string{expandHome(explicit)}
	}

	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		dirs = append(dirs,
			filepath.Join(home, ".lingma"),
			filepath.Join(home, ".lingma", "vscode", "sharedClientCache"),
			filepath.Join(home, ".config", "Lingma"),
			filepath.Join(home, ".local", "share", "Lingma"),
		)
	}
	for _, envName := range []string{"APPDATA", "LOCALAPPDATA", "ProgramData"} {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			dirs = append(dirs,
				filepath.Join(value, "Lingma"),
				filepath.Join(value, "lingma"),
			)
		}
	}
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		dirs = append(dirs, filepath.Join(value, "Lingma"))
	}
	return uniquePathStrings(dirs)
}

func loadMachineID(lingmaDir string) (string, error) {
	if body, err := os.ReadFile(filepath.Join(lingmaDir, "cache", "id")); err == nil {
		if value := strings.TrimSpace(string(body)); value != "" {
			return value, nil
		}
	}

	for _, path := range candidateMachineIDLogFiles(lingmaDir) {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if value := extractMachineIDFromText(string(body)); value != "" {
			return value, nil
		}
	}

	return "", errors.New("remote credential requires cache/id or Lingma log machine id; checked cache/id, Lingma app logs, and VS Code Lingma plugin logs")
}

func candidateMachineIDLogFiles(lingmaDir string) []string {
	paths := []string{
		filepath.Join(lingmaDir, "logs", "lingma.log"),
		filepath.Join(lingmaDir, "logs", "Lingma.log"),
		filepath.Join(lingmaDir, "logs", "main.log"),
		filepath.Join(lingmaDir, "logs", "renderer.log"),
		filepath.Join(lingmaDir, "logs", "sharedprocess.log"),
	}
	paths = append(paths, recursiveLogFiles(filepath.Join(lingmaDir, "logs"), 24)...)

	if home, err := os.UserHomeDir(); err == nil {
		for _, root := range lingmaLogRoots(home) {
			paths = append(paths, recentLingmaAppLogs(root)...)
			paths = append(paths, recursiveLogFiles(root, 24)...)
		}
	}
	return uniquePathStrings(paths)
}

func recursiveLogFiles(root string, limit int) []string {
	type item struct {
		path    string
		modTime int64
	}
	items := make([]item, 0)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".log") && !strings.Contains(name, "lingma") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		items = append(items, item{path: path, modTime: info.ModTime().UnixNano()})
		return nil
	})
	sort.Slice(items, func(i, j int) bool { return items[i].modTime > items[j].modTime })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.path)
	}
	return out
}

func extractMachineIDFromText(text string) string {
	markers := []string{
		"using machine id from file:",
		"machine id:",
		"machine_id:",
		"machineId:",
		"machine-id:",
	}
	lowerText := strings.ToLower(text)
	for _, marker := range markers {
		index := strings.LastIndex(lowerText, strings.ToLower(marker))
		if index < 0 {
			continue
		}
		line := text[index+len(marker):]
		if newline := strings.IndexByte(line, '\n'); newline >= 0 {
			line = line[:newline]
		}
		if value := normalizeMachineID(line); value != "" {
			return value
		}
	}

	re := regexp.MustCompile(`(?i)"?(machine[_-]?id|machineId)"?\s*[:=]\s*"?([A-Za-z0-9._:-]{16,})"?`)
	matches := re.FindAllStringSubmatch(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if len(matches[i]) >= 3 {
			if value := normalizeMachineID(matches[i][2]); value != "" {
				return value
			}
		}
	}
	return ""
}

func normalizeMachineID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, ` "'<>),]}`)
	if idx := strings.IndexAny(value, " \t\r\n,;"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.Trim(value, ` "'<>),]}`)
	if len(value) < aes.BlockSize {
		return ""
	}
	return value
}

func decryptCacheUser(machineID string, ciphertext []byte) ([]byte, error) {
	if len(machineID) < aes.BlockSize {
		return nil, errors.New("machine id too short for cache decryption")
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("invalid cache/user ciphertext size")
	}
	key := []byte(machineID[:aes.BlockSize])
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key).CryptBlocks(plaintext, ciphertext)
	return unpadPKCS7(plaintext)
}

func unpadPKCS7(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty plaintext")
	}
	padLen := int(data[len(data)-1])
	if padLen <= 0 || padLen > aes.BlockSize || padLen > len(data) {
		return nil, errors.New("invalid cache/user padding")
	}
	for _, b := range data[len(data)-padLen:] {
		if int(b) != padLen {
			return nil, errors.New("invalid cache/user padding bytes")
		}
	}
	return data[:len(data)-padLen], nil
}

func validateCredential(cred Credential) error {
	if strings.TrimSpace(cred.CosyKey) == "" {
		return errors.New("remote credential missing cosy_key")
	}
	if strings.TrimSpace(cred.EncryptUserInfo) == "" {
		return errors.New("remote credential missing encrypt_user_info")
	}
	if strings.TrimSpace(cred.UserID) == "" {
		return errors.New("remote credential missing user_id")
	}
	if strings.TrimSpace(cred.MachineID) == "" {
		return errors.New("remote credential missing machine_id")
	}
	return nil
}

func parseExpire(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func parseExpireAny(value any) int64 {
	switch typed := value.(type) {
	case string:
		return parseExpire(typed)
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	default:
		return 0
	}
}

func IsExpired(cred Credential, margin time.Duration) bool {
	return cred.TokenExpireTime > 0 && time.Now().Add(margin).UnixMilli() > cred.TokenExpireTime
}

func MachineOSHeader() string {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "arm64_darwin"
		}
		return "x86_64_darwin"
	case "windows":
		if runtime.GOARCH == "arm64" {
			return "arm64_windows"
		}
		return "x86_64_windows"
	case "linux":
		if runtime.GOARCH == "arm64" {
			return "arm64_linux"
		}
		return "x86_64_linux"
	default:
		return runtime.GOARCH + "_" + runtime.GOOS
	}
}

func uniquePathStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cleaned := filepath.Clean(value)
		key := strings.ToLower(cleaned)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}
