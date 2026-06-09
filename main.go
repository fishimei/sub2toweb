package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Source                SourceConfig `json:"source"`
	Target                TargetConfig `json:"target"`
	RequestTimeoutSeconds int          `json:"request_timeout_seconds"`
	MaxImportCount        int          `json:"max_import_count"`
	DryRun                bool         `json:"dry_run"`
}

type SourceConfig struct {
	BaseURL          string `json:"base_url"`
	AccountsDataPath string `json:"accounts_data_path"`
	AdminKey         string `json:"admin_key"`
	AdminKeyHeader   string `json:"admin_key_header"`
	AdminKeyScheme   string `json:"admin_key_scheme"`
	Status           string `json:"status"`
	SortBy           string `json:"sort_by"`
	SortOrder        string `json:"sort_order"`
	Timezone         string `json:"timezone"`
}

type TargetConfig struct {
	BaseURL         string `json:"base_url"`
	AccountsPath    string `json:"accounts_path"`
	Token           string `json:"token"`
	TokenHeader     string `json:"token_header"`
	TokenScheme     string `json:"token_scheme"`
	DefaultProvider string `json:"default_provider"`
}

type SourceAccount struct {
	AccessToken string `json:"access_token"`
	Provider    string `json:"provider"`
}

type ImportPayload struct {
	Tokens   []string `json:"tokens"`
	Provider string   `json:"provider"`
}

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}

	if err := cfg.validate(); err != nil {
		log.Fatalf("配置无效: %v", err)
	}

	timeout := time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	client := &http.Client{Timeout: timeout}
	ctx := context.Background()

	accounts, err := fetchAccounts(ctx, client, cfg)
	if err != nil {
		log.Fatalf("拉取账户数据失败: %v", err)
	}
	if len(accounts) == 0 {
		log.Println("没有找到包含 access_token 的账户，未执行导入")
		return
	}

	log.Printf("找到 %d 个包含 access_token 的账户", len(accounts))
	accounts = limitAccounts(accounts, cfg.MaxImportCount)
	if cfg.MaxImportCount > 0 {
		log.Printf("本次最多导入 %d 个账户，实际将处理 %d 个", cfg.MaxImportCount, len(accounts))
	}
	if cfg.DryRun {
		for _, account := range accounts {
			provider := providerFor(account, cfg.Target.DefaultProvider)
			log.Printf("dry_run: 将导入 provider=%q access_token=%s", provider, maskToken(account.AccessToken))
		}
		return
	}

	imported := 0
	for _, account := range accounts {
		provider := providerFor(account, cfg.Target.DefaultProvider)
		if provider == "" {
			log.Printf("跳过 access_token=%s：账户缺少 provider，且 target.default_provider 未配置", maskToken(account.AccessToken))
			continue
		}
		if err := postAccount(ctx, client, cfg, ImportPayload{Tokens: []string{account.AccessToken}, Provider: provider}); err != nil {
			log.Fatalf("导入账户失败 access_token=%s provider=%q: %v", maskToken(account.AccessToken), provider, err)
		}
		imported++
		log.Printf("已导入账户 provider=%q access_token=%s", provider, maskToken(account.AccessToken))
	}

	log.Printf("完成：成功导入 %d/%d 个账户", imported, len(accounts))
}

func loadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()

	var cfg Config
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg *Config) validate() error {
	if cfg.RequestTimeoutSeconds <= 0 {
		cfg.RequestTimeoutSeconds = 30
	}
	if cfg.Source.BaseURL == "" {
		return errors.New("source.base_url 不能为空")
	}
	if cfg.Source.AccountsDataPath == "" {
		return errors.New("source.accounts_data_path 不能为空")
	}
	if cfg.Source.AdminKey == "" || strings.Contains(cfg.Source.AdminKey, "请在这里填写") {
		return errors.New("source.admin_key 需要填写管理员密钥")
	}
	if cfg.Source.AdminKeyHeader == "" {
		cfg.Source.AdminKeyHeader = "Authorization"
	}
	if cfg.Target.BaseURL == "" {
		return errors.New("target.base_url 不能为空")
	}
	if cfg.Target.AccountsPath == "" {
		return errors.New("target.accounts_path 不能为空")
	}
	if cfg.Target.Token == "" || strings.Contains(cfg.Target.Token, "请在这里填写") {
		return errors.New("target.token 需要填写目标接口 token")
	}
	if cfg.Target.TokenHeader == "" {
		cfg.Target.TokenHeader = "Authorization"
	}
	return nil
}

func fetchAccounts(ctx context.Context, client *http.Client, cfg Config) ([]SourceAccount, error) {
	endpoint, err := buildSourceURL(cfg.Source)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	setAuthHeader(req, cfg.Source.AdminKeyHeader, cfg.Source.AdminKeyScheme, cfg.Source.AdminKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s 返回 %s: %s", endpoint, resp.Status, truncateBody(body))
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	accounts := extractAccounts(payload, cfg.Target.DefaultProvider)
	return dedupeAccounts(accounts), nil
}

func buildSourceURL(source SourceConfig) (string, error) {
	endpoint, err := joinURL(source.BaseURL, source.AccountsDataPath)
	if err != nil {
		return "", err
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	addQueryIfNotEmpty(query, "status", source.Status)
	addQueryIfNotEmpty(query, "sort_by", source.SortBy)
	addQueryIfNotEmpty(query, "sort_order", source.SortOrder)
	addQueryIfNotEmpty(query, "timezone", source.Timezone)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func postAccount(ctx context.Context, client *http.Client, cfg Config, payload ImportPayload) error {
	endpoint, err := joinURL(cfg.Target.BaseURL, cfg.Target.AccountsPath)
	if err != nil {
		return err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	setAuthHeader(req, cfg.Target.TokenHeader, cfg.Target.TokenScheme, cfg.Target.Token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s 返回 %s: %s", endpoint, resp.Status, truncateBody(respBody))
	}
	return nil
}

func extractAccounts(value any, defaultProvider string) []SourceAccount {
	accounts := make([]SourceAccount, 0)
	walkJSON(value, func(object map[string]any) {
		accessToken := stringField(object, "access_token")
		if accessToken == "" {
			return
		}
		provider := stringField(object, "provider")
		if provider == "" {
			provider = defaultProvider
		}
		accounts = append(accounts, SourceAccount{AccessToken: accessToken, Provider: provider})
	})
	return accounts
}

func walkJSON(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)
		for _, child := range typed {
			walkJSON(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkJSON(child, visit)
		}
	}
}

func dedupeAccounts(accounts []SourceAccount) []SourceAccount {
	seen := make(map[string]struct{}, len(accounts))
	result := make([]SourceAccount, 0, len(accounts))
	for _, account := range accounts {
		key := account.AccessToken + "\x00" + account.Provider
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, account)
	}
	return result
}

func limitAccounts(accounts []SourceAccount, maxCount int) []SourceAccount {
	if maxCount <= 0 || maxCount >= len(accounts) {
		return accounts
	}
	return accounts[:maxCount]
}

func providerFor(account SourceAccount, defaultProvider string) string {
	if account.Provider != "" {
		return account.Provider
	}
	return defaultProvider
}

func stringField(object map[string]any, key string) string {
	value, ok := object[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func setAuthHeader(req *http.Request, headerName, scheme, token string) {
	value := strings.TrimSpace(token)
	if scheme != "" {
		value = strings.TrimSpace(scheme) + " " + value
	}
	req.Header.Set(headerName, value)
}

func joinURL(baseURL, path string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("base_url 无效: %s", baseURL)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return parsed.String(), nil
}

func addQueryIfNotEmpty(query url.Values, key, value string) {
	if value != "" {
		query.Set(key, value)
	}
}

func maskToken(token string) string {
	if len(token) <= 10 {
		return "***"
	}
	return token[:6] + "..." + token[len(token)-4:]
}

func truncateBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 500 {
		return text[:500] + "..."
	}
	return text
}
