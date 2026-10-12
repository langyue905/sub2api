package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultArkBaseURL       = "https://ark.cn-beijing.volces.com/api/v3"
	defaultMaxResponseSize  = int64(4 << 20)
	providerVolcengineArk   = "volcengine_ark"
	providerOpenAICompat    = "openai_compatible"
	providerWaninter        = "waninter"
	defaultCreatePath       = "/v1/videos/generations"
	waninterCreatePath      = "/v1/videos"
	waninterKlingTextPath   = "/kling/v1/videos/text2video"
	waninterKlingImagePath  = "/kling/v1/videos/image2video"
	waninterKlingStatusPath = "/kling/v1/videos/text2video/{id}"
	defaultStatusPath       = "/v1/videos/{id}"
	defaultContentPath      = "/v1/videos/{id}/content"
	defaultModelsPath       = "/v1/models"
	seedanceStatusPath      = "/v1/contents/generations/tasks/{id}"
	defaultDownloadHosts    = ".volces.com,.volcengine.com,img.mikuapi.org,v15-kling.klingai.com,ivd.bjstlx.com"
	headerProvider          = "X-Sub2-Video-Upstream-Protocol"
	headerProviderBaseURL   = "X-Sub2-Video-Upstream-Base-URL"
	headerCreatePath        = "X-Sub2-Video-Upstream-Create-Path"
	headerStatusPath        = "X-Sub2-Video-Upstream-Status-Path"
	headerContentPath       = "X-Sub2-Video-Upstream-Content-Path"
	headerModelsPath        = "X-Sub2-Video-Upstream-Models-Path"
)

// compatibleVideoURLFields covers the result envelopes used by OpenAI-compatible
// relays.  Keep this list explicit so arbitrary provider response fields are not
// accidentally treated as downloadable media URLs.
var compatibleVideoURLFields = []string{
	"video.url", "video_url", "url", "result_url", "download_url",
	"content.video_url", "content.url",
	"data.video.url", "data.video_url", "data.url", "data.result_url", "data.download_url",
	"data.content.video_url", "data.content.url",
	"data.result.video.url", "data.result.video_url", "data.result.url", "data.result.download_url",
	"data.result.content.video_url", "data.result.content.url",
	"data.task_result.videos.0.url", "data.task_result.videos.0.watermark_url",
	"data.task_result.video.url", "data.task_result.video_url",
	"data.result.output.video.url", "data.result.output.video_url", "data.result.output.url",
	"result.video.url", "result.video_url", "result.url", "result.download_url",
	"result.content.video_url", "result.content.url",
	"output.video.url", "output.video_url", "output.url", "output.download_url",
	"output.content.video_url", "output.content.url",
	"response.video.url", "response.video_url", "response.url",
}

type config struct {
	listenAddr             string
	arkBaseURL             string
	defaultProvider        string
	defaultUpstreamBaseURL string
	sharedSecret           string
	maxResponseBytes       int64
	allowedDownloadHosts   []string
	taskRouteDir           string
}

type server struct {
	cfg        config
	client     *http.Client
	taskRoutes sync.Map
}

type seedanceTaskRoute struct {
	StatusPath  string    `json:"status_path"`
	ContentPath string    `json:"content_path"`
	CreatedAt   time.Time `json:"created_at"`
}

type providerConfig struct {
	nativeGrok  bool
	protocol    string
	baseURL     string
	createPath  string
	statusPath  string
	contentPath string
	modelsPath  string
}

type createRequest struct {
	Model                 string          `json:"model"`
	Prompt                string          `json:"prompt"`
	Image                 json.RawMessage `json:"image"`
	Images                json.RawMessage `json:"images"`
	ReferenceImages       json.RawMessage `json:"reference_images"`
	Duration              int             `json:"duration,omitempty"`
	Ratio                 string          `json:"ratio,omitempty"`
	AspectRatio           string          `json:"aspect_ratio,omitempty"`
	Resolution            string          `json:"resolution,omitempty"`
	GenerateAudio         *bool           `json:"generate_audio,omitempty"`
	Watermark             *bool           `json:"watermark,omitempty"`
	Seed                  *int64          `json:"seed,omitempty"`
	ServiceTier           string          `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *int            `json:"execution_expires_after,omitempty"`
	ReturnLastFrame       *bool           `json:"return_last_frame,omitempty"`
	SafetyIdentifier      string          `json:"safety_identifier,omitempty"`
}

type arkTask struct {
	ID         string `json:"id"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	Resolution string `json:"resolution,omitempty"`
	Ratio      string `json:"ratio,omitempty"`
	Duration   int    `json:"duration,omitempty"`
	Content    struct {
		VideoURL string `json:"video_url"`
	} `json:"content,omitempty"`
	Usage struct {
		CompletionTokens int64 `json:"completion_tokens,omitempty"`
		TotalTokens      int64 `json:"total_tokens,omitempty"`
	} `json:"usage,omitempty"`
	Error json.RawMessage `json:"error,omitempty"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	srv := newServer(cfg)
	httpServer := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       90 * time.Second,
	}
	slog.Info("video plugin listening", "address", cfg.listenAddr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("video plugin stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	cfg := config{
		listenAddr:             envOr("LISTEN_ADDR", ":8081"),
		arkBaseURL:             strings.TrimRight(envOr("VOLCENGINE_BASE_URL", defaultArkBaseURL), "/"),
		defaultProvider:        strings.TrimSpace(envOr("VIDEO_PLUGIN_PROVIDER", providerVolcengineArk)),
		defaultUpstreamBaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("VIDEO_PLUGIN_UPSTREAM_BASE_URL")), "/"),
		sharedSecret:           strings.TrimSpace(os.Getenv("VIDEO_PLUGIN_SHARED_SECRET")),
		taskRouteDir:           strings.TrimSpace(os.Getenv("VIDEO_PLUGIN_TASK_ROUTE_DIR")),
		maxResponseBytes:       defaultMaxResponseSize,
		allowedDownloadHosts:   splitCSV(envOr("VIDEO_PLUGIN_ALLOWED_DOWNLOAD_HOSTS", defaultDownloadHosts)),
	}
	if raw := strings.TrimSpace(os.Getenv("VIDEO_PLUGIN_MAX_RESPONSE_BYTES")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return config{}, fmt.Errorf("VIDEO_PLUGIN_MAX_RESPONSE_BYTES must be a positive integer")
		}
		cfg.maxResponseBytes = value
	}
	base, err := url.Parse(cfg.arkBaseURL)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return config{}, fmt.Errorf("VOLCENGINE_BASE_URL must be a clean https URL")
	}
	if len(cfg.allowedDownloadHosts) == 0 {
		return config{}, fmt.Errorf("VIDEO_PLUGIN_ALLOWED_DOWNLOAD_HOSTS cannot be empty")
	}
	if cfg.defaultProvider != providerVolcengineArk && cfg.defaultProvider != providerOpenAICompat && cfg.defaultProvider != providerWaninter {
		return config{}, fmt.Errorf("VIDEO_PLUGIN_PROVIDER must be %s, %s, or %s", providerVolcengineArk, providerOpenAICompat, providerWaninter)
	}
	if (cfg.defaultProvider == providerOpenAICompat || cfg.defaultProvider == providerWaninter) && strings.TrimSpace(cfg.defaultUpstreamBaseURL) == "" {
		return config{}, fmt.Errorf("VIDEO_PLUGIN_UPSTREAM_BASE_URL is required for %s", cfg.defaultProvider)
	}
	return cfg, nil
}

func newServer(cfg config) *server {
	return &server{cfg: cfg, client: &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /plugin/v1/manifest", s.withPluginAuth(s.manifest))
	mux.HandleFunc("POST /plugin/v1/videos", s.withPluginAuth(s.createVideo))
	mux.HandleFunc("GET /plugin/v1/videos/{id}", s.withPluginAuth(s.getVideo))
	mux.HandleFunc("GET /plugin/v1/videos/{id}/content", s.withPluginAuth(s.getVideoContent))
	return mux
}

func (s *server) withPluginAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.sharedSecret != "" {
			provided := r.Header.Get("X-Sub2-Plugin-Token")
			if len(provided) != len(s.cfg.sharedSecret) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.sharedSecret)) != 1 {
				writeError(w, http.StatusUnauthorized, "authentication_error", "invalid plugin token")
				return
			}
		}
		if strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) == "" {
			writeError(w, http.StatusUnauthorized, "authentication_error", "upstream api key is required")
			return
		}
		next(w, r)
	}
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"plugin":   "video-upstream-adapter",
		"provider": s.cfg.defaultProvider,
	})
}

func (s *server) manifest(w http.ResponseWriter, r *http.Request) {
	provider, err := s.providerConfig(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider_config", err.Error())
		return
	}
	if provider.protocol == providerOpenAICompat || provider.protocol == providerWaninter {
		models, err := s.fetchCompatibleModels(r, provider)
		if err != nil {
			writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"protocol":     "sub2-video-plugin/v1",
			"provider":     provider.protocol,
			"capabilities": []string{"text_to_video", "image_to_video", "content_proxy"},
			"models":       models,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol":     "sub2-video-plugin/v1",
		"provider":     "volcengine-ark",
		"capabilities": []string{"text_to_video", "image_to_video", "first_last_frame", "audio", "content_proxy", "token_usage"},
		"models": []string{
			"doubao-seedance-2-5-260628",
			"doubao-seedance-2-0-pro-250528",
			"doubao-seedance-2-0-lite-250428",
			"doubao-seedance-1-5-pro-251215",
		},
	})
}

func (s *server) createVideo(w http.ResponseWriter, r *http.Request) {
	provider, err := s.providerConfig(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider_config", err.Error())
		return
	}
	if provider.protocol == providerOpenAICompat || provider.protocol == providerWaninter {
		s.createCompatibleVideo(w, r, provider)
		return
	}
	defer func() { _ = r.Body.Close() }()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	decoder.DisallowUnknownFields()
	var in createRequest
	if err := decoder.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid video request: "+err.Error())
		return
	}
	in.Model = strings.TrimSpace(in.Model)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Model == "" || in.Prompt == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model and prompt are required")
		return
	}

	content := []map[string]any{{"type": "text", "text": in.Prompt}}
	images, err := collectImageURLs(in.Image, in.Images, in.ReferenceImages)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	for i, imageURL := range images {
		item := map[string]any{"type": "image_url", "image_url": map[string]string{"url": imageURL}}
		if len(images) == 1 || i == 0 {
			item["role"] = "first_frame"
		} else if i == len(images)-1 {
			item["role"] = "last_frame"
		} else {
			item["role"] = "reference_image"
		}
		content = append(content, item)
	}

	payload := map[string]any{"model": in.Model, "content": content}
	setOptional(payload, "duration", in.Duration > 0, in.Duration)
	ratio := firstNonEmpty(in.Ratio, in.AspectRatio)
	setOptional(payload, "ratio", ratio != "", ratio)
	setOptional(payload, "resolution", strings.TrimSpace(in.Resolution) != "", strings.TrimSpace(in.Resolution))
	setOptional(payload, "generate_audio", in.GenerateAudio != nil, derefBool(in.GenerateAudio))
	setOptional(payload, "watermark", in.Watermark != nil, derefBool(in.Watermark))
	setOptional(payload, "seed", in.Seed != nil, derefInt64(in.Seed))
	setOptional(payload, "service_tier", strings.TrimSpace(in.ServiceTier) != "", strings.TrimSpace(in.ServiceTier))
	setOptional(payload, "execution_expires_after", in.ExecutionExpiresAfter != nil, derefInt(in.ExecutionExpiresAfter))
	setOptional(payload, "return_last_frame", in.ReturnLastFrame != nil, derefBool(in.ReturnLastFrame))
	setOptional(payload, "safety_identifier", strings.TrimSpace(in.SafetyIdentifier) != "", strings.TrimSpace(in.SafetyIdentifier))

	respBody, status, err := s.callArkJSON(r, http.MethodPost, "/contents/generations/tasks", payload)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	if status >= http.StatusBadRequest {
		writeUpstreamError(w, status, respBody)
		return
	}
	var task arkTask
	if err := json.Unmarshal(respBody, &task); err != nil || strings.TrimSpace(task.ID) == "" {
		writeError(w, http.StatusBadGateway, "upstream_error", "volcengine returned an invalid task response")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": task.ID, "model": firstNonEmpty(task.Model, in.Model), "status": normalizeStatus(task.Status)})
}

func (s *server) getVideo(w http.ResponseWriter, r *http.Request) {
	provider, err := s.providerConfig(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider_config", err.Error())
		return
	}
	if provider.protocol == providerOpenAICompat || provider.protocol == providerWaninter {
		s.getCompatibleVideo(w, r, provider)
		return
	}
	task, raw, status, err := s.fetchTask(r, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	if status >= http.StatusBadRequest {
		writeUpstreamError(w, status, raw)
		return
	}
	writeJSON(w, http.StatusOK, normalizeTask(task))
}

func (s *server) getVideoContent(w http.ResponseWriter, r *http.Request) {
	provider, err := s.providerConfig(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider_config", err.Error())
		return
	}
	if provider.protocol == providerOpenAICompat || provider.protocol == providerWaninter {
		s.getCompatibleVideoContent(w, r, provider)
		return
	}
	task, raw, status, err := s.fetchTask(r, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	if status >= http.StatusBadRequest {
		writeUpstreamError(w, status, raw)
		return
	}
	if normalizeStatus(task.Status) != "done" || strings.TrimSpace(task.Content.VideoURL) == "" {
		writeError(w, http.StatusConflict, "video_not_ready", "video is not ready")
		return
	}
	videoURL, err := s.validateDownloadURL(task.Content.VideoURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, videoURL.String(), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "failed to build video download request")
		return
	}
	if value := strings.TrimSpace(r.Header.Get("Range")); value != "" {
		req.Header.Set("Range", value)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "video download failed")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		writeError(w, http.StatusBadGateway, "upstream_error", "video download redirect is not allowed")
		return
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *server) providerConfig(r *http.Request) (providerConfig, error) {
	protocol := strings.TrimSpace(r.Header.Get(headerProvider))
	if protocol == "" {
		protocol = strings.TrimSpace(s.cfg.defaultProvider)
	}
	if protocol == "" {
		protocol = providerVolcengineArk
	}
	nativeGrok := protocol == "grok_native"
	if nativeGrok {
		protocol = providerOpenAICompat
	}
	if protocol != providerVolcengineArk && protocol != providerOpenAICompat && protocol != providerWaninter {
		return providerConfig{}, fmt.Errorf("unsupported video provider protocol: %s", protocol)
	}

	baseURL := strings.TrimSpace(r.Header.Get(headerProviderBaseURL))
	if baseURL == "" {
		if protocol == providerOpenAICompat || protocol == providerWaninter {
			baseURL = strings.TrimSpace(s.cfg.defaultUpstreamBaseURL)
		} else {
			baseURL = strings.TrimSpace(s.cfg.arkBaseURL)
		}
	}
	validated, err := validateProviderBaseURL(baseURL)
	if err != nil {
		return providerConfig{}, err
	}

	provider := providerConfig{
		nativeGrok:  nativeGrok,
		protocol:    protocol,
		baseURL:     strings.TrimRight(validated, "/"),
		createPath:  headerOrDefault(r, headerCreatePath, defaultCreatePath),
		statusPath:  headerOrDefault(r, headerStatusPath, defaultStatusPath),
		contentPath: headerOrDefault(r, headerContentPath, defaultContentPath),
		modelsPath:  headerOrDefault(r, headerModelsPath, defaultModelsPath),
	}
	if nativeGrok {
		provider.createPath = "/v1/videos/generations"
		provider.statusPath = "/v1/videos/generations/{id}"
		provider.contentPath = provider.statusPath
	}
	// Waninter exposes its OpenAI-video models through /v1/videos. Older
	// accounts were saved with the generic plugin default, so migrate that
	// legacy value at request time without requiring every account to be edited.
	if protocol == providerWaninter && provider.createPath == defaultCreatePath {
		provider.createPath = waninterCreatePath
	}
	for _, item := range []struct {
		name  string
		value string
	}{
		{headerCreatePath, provider.createPath},
		{headerStatusPath, provider.statusPath},
		{headerContentPath, provider.contentPath},
		{headerModelsPath, provider.modelsPath},
	} {
		if err := validateProviderPath(item.value); err != nil {
			return providerConfig{}, fmt.Errorf("invalid %s: %w", item.name, err)
		}
	}
	return provider, nil
}

func headerOrDefault(r *http.Request, header, fallback string) string {
	if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
		return value
	}
	return fallback
}

func validateProviderBaseURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", fmt.Errorf("video upstream base URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("video upstream base URL is invalid")
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "video-plugin-"))) {
		return "", fmt.Errorf("video upstream base URL must use https, except local test hosts")
	}
	return raw, nil
}

func validateProviderPath(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#") {
		return fmt.Errorf("path must start with / and cannot contain query or fragment")
	}
	return nil
}

func providerPath(baseURL, template, id string) string {
	pathValue := strings.ReplaceAll(template, "{id}", url.PathEscape(id))
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(pathValue, "/v1/") {
		pathValue = strings.TrimPrefix(pathValue, "/v1")
	}
	return strings.TrimRight(baseURL, "/") + pathValue
}

func (s *server) createCompatibleVideo(w http.ResponseWriter, r *http.Request, provider providerConfig) {
	defer func() { _ = r.Body.Close() }()
	var payload map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	if err := decoder.Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid video request: "+err.Error())
		return
	}
	model, _ := payload["model"].(string)
	if strings.TrimSpace(model) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	createPath := provider.createPath
	if provider.nativeGrok {
		var err error
		payload, err = normalizeNativeGrokPayload(payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
	}
	statusPath := provider.statusPath
	contentPath := provider.contentPath
	// 画布对 1.5 可能显式传入文档路径，但当前 waninter 账号的实际
	// 兼容入口仍是 /v1/videos；按模型强制统一，避免只回退默认值的遗漏。
	if provider.protocol == providerWaninter && isWaninterGrokVideoPayload(payload) {
		createPath = waninterCreatePath
		statusPath = defaultStatusPath
		contentPath = defaultContentPath
	}
	seedanceRoute := provider.protocol == providerWaninter && isWaninterSeedancePath(createPath)
	klingRoute := provider.protocol == providerWaninter && isWaninterNativeKlingModel(model) && isWaninterDefaultVideoPath(createPath)
	if provider.protocol == providerWaninter {
		if klingRoute {
			if hasCompatibleImages(payload) {
				createPath = waninterKlingImagePath
				statusPath = strings.Replace(waninterKlingImagePath, "image2video", "image2video/{id}", 1)
			} else {
				createPath = waninterKlingTextPath
				statusPath = waninterKlingStatusPath
			}
			contentPath = statusPath
			normalizeWaninterKlingPayload(payload, model)
		} else if seedanceRoute {
			if !isWaninterSeedancePath(statusPath) {
				statusPath = seedanceStatusPath
			}
			if !isWaninterSeedancePath(contentPath) {
				contentPath = seedanceStatusPath
			}
			normalizeWaninterSeedancePayload(payload)
		} else {
			normalizeWaninterVideoPayload(payload)
		}
	}
	slog.Info("video provider create request",
		"protocol", provider.protocol,
		"path", createPath,
		"model", model,
		"reference_shape", compatibleReferenceShape(payload),
	)
	body, status, err := s.callCompatibleJSON(r, http.MethodPost, providerPath(provider.baseURL, createPath, ""), payload)
	if err != nil {
		slog.Warn("video provider create transport failed", "protocol", provider.protocol, "path", createPath, "model", model)
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	slog.Info("video provider create response", "protocol", provider.protocol, "path", createPath, "model", model, "status", status)
	if status >= http.StatusBadRequest {
		writeUpstreamError(w, status, body)
		return
	}
	id := firstJSONString(body, "id", "request_id", "task_id", "data.id", "data.request_id", "data.task_id")
	if id == "" {
		writeError(w, http.StatusBadGateway, "upstream_error", "compatible video provider returned no task id")
		return
	}
	result := map[string]any{"id": id, "model": model, "status": normalizeCompatibleStatus(firstJSONString(body, "status", "state", "data.status", "data.state"))}
	if result["status"] == "failed" && firstJSONString(body, "error.message", "error", "data.error.message") != "" {
		result["error"] = firstJSONString(body, "error.message", "error", "data.error.message")
	}
	if seedanceRoute || klingRoute {
		if err := s.storeTaskRoute(r, provider, id, seedanceTaskRoute{StatusPath: statusPath, ContentPath: contentPath, CreatedAt: time.Now()}); err != nil {
			slog.Error("persist video task route failed", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func isWaninterDefaultVideoPath(path string) bool {
	path = strings.TrimSpace(path)
	return path == defaultCreatePath || path == waninterCreatePath
}

func isWaninterNativeKlingModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "kling-video-v3":
		return true
	default:
		return false
	}
}

func hasCompatibleImages(payload map[string]any) bool {
	for _, key := range []string{"image", "images", "reference_images"} {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch item := value.(type) {
		case string:
			if strings.TrimSpace(item) != "" {
				return true
			}
		case []any:
			if len(item) > 0 {
				return true
			}
		case []string:
			if len(item) > 0 {
				return true
			}
		case map[string]any:
			return compatibleImageURL(item) != ""
		}
	}
	return false
}

func normalizeWaninterKlingPayload(payload map[string]any, publicModel string) {
	model := strings.TrimSpace(publicModel)
	payload["model"] = model
	payload["model_name"] = model
	if duration, ok := payload["duration"]; ok {
		payload["duration"] = compatibleNumberString(duration)
	} else if seconds, ok := payload["seconds"]; ok {
		payload["duration"] = compatibleNumberString(seconds)
	}
	if _, exists := payload["aspect_ratio"]; !exists {
		if ratio, ok := payload["ratio"].(string); ok && strings.TrimSpace(ratio) != "" {
			payload["aspect_ratio"] = strings.TrimSpace(ratio)
		}
	}
	for _, key := range []string{"image", "images", "reference_images"} {
		value, ok := payload[key]
		if !ok {
			continue
		}
		if imageURL := firstCompatibleImageURL(value); imageURL != "" {
			payload["image"] = imageURL
		}
		break
	}
	delete(payload, "seconds")
	delete(payload, "ratio")
	delete(payload, "resolution")
	delete(payload, "images")
	delete(payload, "reference_images")
}

func firstCompatibleImageURL(value any) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case map[string]any:
		return compatibleImageURL(item)
	case map[string]string:
		return compatibleStringImageURL(item)
	case []any:
		for _, entry := range item {
			if imageURL := firstCompatibleImageURL(entry); imageURL != "" {
				return imageURL
			}
		}
	case []string:
		for _, entry := range item {
			if strings.TrimSpace(entry) != "" {
				return strings.TrimSpace(entry)
			}
		}
	}
	return ""
}

func normalizeWaninterVideoPayload(payload map[string]any) {
	grok := isWaninterGrokVideoPayload(payload)
	grok15 := grok && isWaninterGrok15VideoPayload(payload)
	if grok {
		normalizeWaninterGrokImagePayload(payload)
	} else {
		for _, key := range []string{"image", "images", "reference_images"} {
			value, ok := payload[key]
			if !ok {
				continue
			}
			switch item := value.(type) {
			case map[string]any:
				if imageURL := compatibleImageURL(item); imageURL != "" {
					payload[key] = imageURL
				}
			case []any:
				images := make([]string, 0, len(item))
				for _, entry := range item {
					if imageURL := compatibleImageValueURL(entry); imageURL != "" {
						images = append(images, imageURL)
					}
				}
				if len(images) > 0 {
					payload[key] = images
				}
			}
		}
	}
	if grok && !grok15 {
		if _, exists := payload["aspect_ratio"]; !exists {
			if ratio, ok := payload["ratio"].(string); ok && strings.TrimSpace(ratio) != "" {
				payload["aspect_ratio"] = strings.TrimSpace(ratio)
			}
		}
		delete(payload, "ratio")
		return
	}
	if _, exists := payload["seconds"]; !exists {
		if duration, ok := payload["duration"]; ok {
			payload["seconds"] = compatibleNumberString(duration)
		}
	}
	if _, exists := payload["aspect_ratio"]; !exists {
		if ratio, ok := payload["ratio"].(string); ok && strings.TrimSpace(ratio) != "" {
			payload["aspect_ratio"] = strings.TrimSpace(ratio)
		}
	}
	if !grok15 {
		if _, exists := payload["images"]; !exists {
			if references, ok := payload["reference_images"]; ok {
				payload["images"] = references
			} else if image, ok := payload["image"]; ok {
				payload["images"] = []any{image}
			}
		}
	}
	delete(payload, "ratio")
	delete(payload, "reference_images")
	if grok15 {
		delete(payload, "images")
	} else {
		delete(payload, "image")
	}
}

func isWaninterGrokVideoPayload(payload map[string]any) bool {
	model, _ := payload["model"].(string)
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "grok-imagine-video")
}

func isWaninterGrok15VideoPayload(payload map[string]any) bool {
	model, _ := payload["model"].(string)
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasSuffix(model, "-1.5") || strings.HasSuffix(model, "-1.5-preview")
}

// 普通 Grok 使用 reference_images 对象数组。当前 waninter 的旧兼容入口
// 只会把顶层 image_url 字符串转换成 1.5 所需的 image URL 对象。
func normalizeWaninterGrokImagePayload(payload map[string]any) {
	var source any
	for _, key := range []string{"images", "reference_images", "image", "image_url"} {
		if value, ok := payload[key]; ok {
			source = value
			break
		}
	}
	if source == nil {
		return
	}

	items := make([]any, 0, 4)
	appendValue := func(value any) {}
	appendValue = func(value any) {
		switch item := value.(type) {
		case []any:
			for _, entry := range item {
				appendValue(entry)
			}
		case []string:
			for _, entry := range item {
				appendValue(entry)
			}
		default:
			if imageURL := compatibleImageValueURL(item); imageURL != "" {
				items = append(items, map[string]string{"url": imageURL})
			}
		}
	}
	appendValue(source)
	preview := isWaninterGrok15VideoPayload(payload)
	if preview {
		if len(items) > 0 {
			payload["image_url"] = compatibleImageValueURL(items[0])
		}
	} else if len(items) > 0 {
		payload["reference_images"] = items
	}
	if !preview {
		delete(payload, "images")
		delete(payload, "image")
		delete(payload, "image_url")
	} else {
		delete(payload, "image")
		delete(payload, "images")
		delete(payload, "reference_images")
	}
}

func compatibleReferenceShape(payload map[string]any) string {
	parts := make([]string, 0, 4)
	for _, key := range []string{"image", "image_url", "images", "reference_images"} {
		value, ok := payload[key]
		if !ok {
			continue
		}
		shape := "unknown"
		switch item := value.(type) {
		case string:
			shape = "string"
		case map[string]any, map[string]string:
			shape = "object"
		case []any:
			shape = "array"
			if len(item) > 0 {
				switch item[0].(type) {
				case string:
					shape = "array<string>"
				case map[string]any, map[string]string:
					shape = "array<object>"
				}
			}
		case []string:
			shape = "array<string>"
		}
		parts = append(parts, key+"="+shape)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

// 万次元自有画布的 Seedance 接口使用 content 多模态数组，不能直接透传统一视频请求。
func isWaninterSeedancePath(path string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(path)), "/contents/generations/tasks")
}

func compatibleNumberString(value any) string {
	switch item := value.(type) {
	case json.Number:
		return item.String()
	case float64:
		return strconv.FormatFloat(item, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(item), 'f', -1, 32)
	case int:
		return strconv.Itoa(item)
	case int64:
		return strconv.FormatInt(item, 10)
	case string:
		return strings.TrimSpace(item)
	default:
		return fmt.Sprint(item)
	}
}

func normalizeWaninterSeedancePayload(payload map[string]any) {
	if _, exists := payload["content"]; exists {
		return
	}
	normalizeWaninterVideoPayload(payload)

	content := make([]map[string]any, 0, 5)
	if prompt, ok := payload["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
		content = append(content, map[string]any{"type": "text", "text": strings.TrimSpace(prompt)})
	}
	for _, key := range []string{"image", "images", "reference_images"} {
		value, ok := payload[key]
		if !ok {
			continue
		}
		appendSeedanceImages(&content, value)
	}
	if len(content) > 0 {
		payload["content"] = content
	}
	for _, key := range []string{"prompt", "image", "images", "reference_images"} {
		delete(payload, key)
	}
}

func appendSeedanceImages(content *[]map[string]any, value any) {
	appendImage := func(imageURL string) {
		imageURL = strings.TrimSpace(imageURL)
		if imageURL == "" {
			return
		}
		*content = append(*content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": imageURL},
			"role":      "reference_image",
		})
	}
	switch item := value.(type) {
	case string:
		appendImage(item)
	case []string:
		for _, imageURL := range item {
			appendImage(imageURL)
		}
	case []any:
		for _, entry := range item {
			if imageURL := compatibleImageValueURL(entry); imageURL != "" {
				appendImage(imageURL)
			}
		}
	case map[string]any:
		appendImage(compatibleImageURL(item))
	}
}

func compatibleImageValueURL(value any) string {
	if imageURL, ok := value.(string); ok {
		return strings.TrimSpace(imageURL)
	}
	if object, ok := value.(map[string]any); ok {
		return compatibleImageURL(object)
	}
	if object, ok := value.(map[string]string); ok {
		return compatibleStringImageURL(object)
	}
	return ""
}

func compatibleStringImageURL(object map[string]string) string {
	for _, key := range []string{"url", "image_url"} {
		if imageURL := strings.TrimSpace(object[key]); imageURL != "" {
			return imageURL
		}
	}
	return ""
}

func compatibleImageURL(object map[string]any) string {
	for _, key := range []string{"url", "image_url"} {
		value, ok := object[key]
		if !ok {
			continue
		}
		if imageURL, ok := value.(string); ok && strings.TrimSpace(imageURL) != "" {
			return strings.TrimSpace(imageURL)
		}
		if nested, ok := value.(map[string]any); ok {
			if imageURL := compatibleImageURL(nested); imageURL != "" {
				return imageURL
			}
		}
	}
	return ""
}

func (s *server) getCompatibleVideo(w http.ResponseWriter, r *http.Request, provider providerConfig) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.ContainsAny(id, "/\\") {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid video task id")
		return
	}
	statusPath, _ := s.videoTaskPaths(r, provider, id)
	body, status, err := s.callCompatibleJSON(r, http.MethodGet, providerPath(provider.baseURL, statusPath, id), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	if status == http.StatusNotFound && provider.protocol == providerWaninter && !isWaninterSeedancePath(statusPath) && provider.statusPath == defaultStatusPath {
		fallbackBody, fallbackStatus, fallbackErr := s.callCompatibleJSON(r, http.MethodGet, providerPath(provider.baseURL, seedanceStatusPath, id), nil)
		if fallbackErr == nil && fallbackStatus < http.StatusBadRequest {
			body, status = fallbackBody, fallbackStatus
		}
	}
	if status == http.StatusNotFound && provider.protocol == providerWaninter && isWaninterDefaultVideoPath(provider.createPath) {
		fallbackPath := strings.Replace(waninterKlingTextPath, "text2video", "text2video/{id}", 1)
		fallbackBody, fallbackStatus, fallbackErr := s.callCompatibleJSON(r, http.MethodGet, providerPath(provider.baseURL, fallbackPath, id), nil)
		if fallbackErr == nil && fallbackStatus < http.StatusBadRequest {
			body, status = fallbackBody, fallbackStatus
		}
	}
	if status >= http.StatusBadRequest {
		writeUpstreamError(w, status, body)
		return
	}
	result := normalizeCompatibleTask(body, id)
	if provider.nativeGrok {
		switch result["status"] {
		case "done":
			result["status"] = "completed"
		case "expired":
			result["status"] = "failed"
			result["error"] = map[string]string{"message": "视频任务已过期"}
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) getCompatibleVideoContent(w http.ResponseWriter, r *http.Request, provider providerConfig) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || strings.ContainsAny(id, "/\\") {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid video task id")
		return
	}
	statusPath, contentPath := s.videoTaskPaths(r, provider, id)
	statusBody, status, err := s.callCompatibleJSON(r, http.MethodGet, providerPath(provider.baseURL, statusPath, id), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	if status == http.StatusNotFound && provider.protocol == providerWaninter && !isWaninterSeedancePath(statusPath) && provider.statusPath == defaultStatusPath {
		fallbackBody, fallbackStatus, fallbackErr := s.callCompatibleJSON(r, http.MethodGet, providerPath(provider.baseURL, seedanceStatusPath, id), nil)
		if fallbackErr == nil && fallbackStatus < http.StatusBadRequest {
			statusBody, status = fallbackBody, fallbackStatus
			statusPath, contentPath = seedanceStatusPath, seedanceStatusPath
		}
	}
	if status == http.StatusNotFound && provider.protocol == providerWaninter && isWaninterDefaultVideoPath(provider.createPath) {
		fallbackPath := strings.Replace(waninterKlingTextPath, "text2video", "text2video/{id}", 1)
		fallbackBody, fallbackStatus, fallbackErr := s.callCompatibleJSON(r, http.MethodGet, providerPath(provider.baseURL, fallbackPath, id), nil)
		if fallbackErr == nil && fallbackStatus < http.StatusBadRequest {
			statusBody, status = fallbackBody, fallbackStatus
			statusPath, contentPath = fallbackPath, fallbackPath
		}
	}
	if status >= http.StatusBadRequest {
		writeUpstreamError(w, status, statusBody)
		return
	}
	videoURL := firstJSONString(statusBody, compatibleVideoURLFields...)
	if videoURL == "" && contentPath != statusPath {
		contentURL := providerPath(provider.baseURL, contentPath, id)
		contentReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, contentURL, nil)
		if err != nil {
			writeError(w, http.StatusBadGateway, "upstream_error", "failed to build compatible video content request")
			return
		}
		copyCompatibleAuthorization(contentReq, r)
		if value := strings.TrimSpace(r.Header.Get("Range")); value != "" {
			contentReq.Header.Set("Range", value)
		}
		contentResp, err := s.client.Do(contentReq)
		if err != nil {
			writeError(w, http.StatusBadGateway, "upstream_error", "compatible video content request failed")
			return
		}
		defer func() { _ = contentResp.Body.Close() }()
		if contentResp.StatusCode >= http.StatusBadRequest {
			body, readErr := io.ReadAll(io.LimitReader(contentResp.Body, s.cfg.maxResponseBytes+1))
			if readErr != nil {
				writeError(w, http.StatusBadGateway, "upstream_error", "failed to read compatible video content response")
				return
			}
			writeUpstreamError(w, contentResp.StatusCode, body)
			return
		}
		contentType := strings.ToLower(contentResp.Header.Get("Content-Type"))
		if strings.HasPrefix(contentType, "video/") || strings.Contains(contentType, "octet-stream") || strings.Contains(contentType, "mp4") {
			copyVideoResponse(w, contentResp)
			return
		}
		body, readErr := io.ReadAll(io.LimitReader(contentResp.Body, s.cfg.maxResponseBytes+1))
		if readErr != nil || int64(len(body)) > s.cfg.maxResponseBytes {
			writeError(w, http.StatusBadGateway, "upstream_error", "compatible video content response is invalid")
			return
		}
		videoURL = firstJSONString(body, compatibleVideoURLFields...)
	}
	if videoURL == "" {
		writeError(w, http.StatusConflict, "video_not_ready", "video is not ready")
		return
	}
	validated, err := s.validateDownloadURLForProvider(videoURL, provider)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, validated.String(), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "failed to build video download request")
		return
	}
	if value := strings.TrimSpace(r.Header.Get("Range")); value != "" {
		req.Header.Set("Range", value)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", "video download failed")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	copyVideoResponse(w, resp)
}

func copyCompatibleAuthorization(req *http.Request, source *http.Request) {
	if auth := strings.TrimSpace(source.Header.Get("Authorization")); auth != "" {
		req.Header.Set("Authorization", auth)
	}
}

func copyVideoResponse(w http.ResponseWriter, resp *http.Response) {
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *server) fetchCompatibleModels(r *http.Request, provider providerConfig) ([]string, error) {
	body, status, err := s.callCompatibleJSON(r, http.MethodGet, providerPath(provider.baseURL, provider.modelsPath, ""), nil)
	if err != nil {
		return nil, err
	}
	if status >= http.StatusBadRequest {
		return nil, fmt.Errorf("compatible provider model list returned HTTP %d: %s", status, truncateProviderBody(string(body), 512))
	}
	models, err := extractCompatibleModelIDs(body)
	if err != nil {
		return nil, fmt.Errorf("compatible provider model list is not valid JSON")
	}
	return dedupeStrings(models), nil
}

func extractCompatibleModelIDs(body []byte) ([]string, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}

	models := make([]string, 0)
	var collect func(any)
	collect = func(value any) {
		switch item := value.(type) {
		case []any:
			for _, entry := range item {
				if modelID := compatibleModelID(entry); modelID != "" {
					models = append(models, modelID)
					continue
				}
				collect(entry)
			}
		case map[string]any:
			for _, key := range []string{"data", "models", "items", "list", "result"} {
				if nested, ok := item[key]; ok {
					collect(nested)
				}
			}
		}
	}
	collect(root)
	return dedupeStrings(models), nil
}

func compatibleModelID(value any) string {
	if modelID, ok := value.(string); ok {
		return strings.TrimSpace(modelID)
	}
	item, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"id", "model", "model_id", "modelId", "slug", "name"} {
		if modelID, ok := item[key].(string); ok && strings.TrimSpace(modelID) != "" {
			return strings.TrimSpace(modelID)
		}
	}
	return ""
}

func (s *server) callCompatibleJSON(r *http.Request, method, target string, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(r.Context(), method, target, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("compatible video provider request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	bytes, err := io.ReadAll(io.LimitReader(resp.Body, s.cfg.maxResponseBytes+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read compatible provider response")
	}
	if int64(len(bytes)) > s.cfg.maxResponseBytes {
		return nil, resp.StatusCode, fmt.Errorf("compatible provider response is too large")
	}
	return bytes, resp.StatusCode, nil
}

func normalizeCompatibleTask(body []byte, fallbackID string) map[string]any {
	id := firstJSONString(body, "id", "request_id", "task_id", "data.id", "data.request_id", "data.task_id", "data.task.id", "data.result.id", "data.result.request_id", "data.result.task_id", "result.id", "result.request_id", "result.task_id", "output.id", "output.request_id", "output.task_id")
	if id == "" {
		id = fallbackID
	}
	result := map[string]any{
		"id":     id,
		"model":  firstJSONString(body, "model", "data.model", "data.result.model", "result.model", "output.model"),
		"status": normalizeCompatibleStatus(firstJSONString(body, "status", "state", "task_status", "data.status", "data.state", "data.task_status", "data.result.status", "data.result.state", "result.status", "result.state", "output.status", "output.state")),
	}
	if urlValue := firstJSONString(body, compatibleVideoURLFields...); urlValue != "" {
		video := map[string]any{"url": urlValue}
		if duration, err := strconv.ParseFloat(firstJSONString(body, "video.duration", "duration", "seconds", "data.video.duration", "data.duration", "data.seconds", "data.result.video.duration", "data.result.duration", "result.duration", "output.duration"), 64); err == nil && duration > 0 {
			video["duration"] = duration
		}
		result["video"] = video
	}
	if resolution := firstJSONString(body, "resolution", "data.resolution", "data.result.resolution", "result.resolution", "output.resolution"); resolution != "" {
		result["resolution"] = resolution
	}
	var root map[string]any
	if json.Unmarshal(body, &root) == nil {
		for _, path := range []string{"usage", "data.usage", "data.result.usage", "result.usage", "output.usage"} {
			var value any = root
			for _, segment := range strings.Split(path, ".") {
				object, ok := value.(map[string]any)
				if !ok {
					value = nil
					break
				}
				value = object[segment]
			}
			if usage, ok := value.(map[string]any); ok {
				result["usage"] = usage
				break
			}
		}
	}
	if errorValue := firstJSONString(body, "error.message", "error", "data.error.message", "data.error"); errorValue != "" {
		result["error"] = errorValue
	}
	return result
}

func normalizeCompatibleStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "done", "completed", "complete", "succeeded", "succeed", "success", "ready":
		return "done"
	case "queued", "pending", "running", "processing", "in_progress", "in-progress", "created", "submitted":
		return "pending"
	case "cancelled", "canceled", "expired":
		return strings.ToLower(strings.TrimSpace(raw))
	case "", "unknown":
		return "pending"
	default:
		return "failed"
	}
}

func firstJSONString(body []byte, paths ...string) string {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return ""
	}
	for _, pathValue := range paths {
		value := root
		for _, segment := range strings.Split(pathValue, ".") {
			if index, err := strconv.Atoi(segment); err == nil {
				items, ok := value.([]any)
				if !ok || index < 0 || index >= len(items) {
					value = nil
					break
				}
				value = items[index]
				continue
			}
			object, ok := value.(map[string]any)
			if !ok {
				value = nil
				break
			}
			value = object[segment]
		}
		switch result := value.(type) {
		case string:
			if strings.TrimSpace(result) != "" {
				return strings.TrimSpace(result)
			}
		case float64:
			if result != 0 {
				return strconv.FormatInt(int64(result), 10)
			}
		}
	}
	return ""
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func truncateProviderBody(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func (s *server) fetchTask(r *http.Request, id string) (arkTask, []byte, int, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\") {
		return arkTask{}, nil, http.StatusBadRequest, fmt.Errorf("invalid video task id")
	}
	body, status, err := s.callArkJSON(r, http.MethodGet, "/contents/generations/tasks/"+url.PathEscape(id), nil)
	if err != nil || status >= http.StatusBadRequest {
		return arkTask{}, body, status, err
	}
	var task arkTask
	if err := json.Unmarshal(body, &task); err != nil {
		return arkTask{}, body, http.StatusBadGateway, fmt.Errorf("volcengine returned an invalid task response")
	}
	return task, body, status, nil
}

func (s *server) callArkJSON(r *http.Request, method, endpoint string, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(r.Context(), method, s.cfg.arkBaseURL+endpoint, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("volcengine request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, resp.StatusCode, fmt.Errorf("volcengine redirect is not allowed")
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, s.cfg.maxResponseBytes+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read volcengine response")
	}
	if int64(len(bodyBytes)) > s.cfg.maxResponseBytes {
		return nil, resp.StatusCode, fmt.Errorf("volcengine response is too large")
	}
	return bodyBytes, resp.StatusCode, nil
}

func (s *server) validateDownloadURL(raw string) (*url.URL, error) {
	return s.validateDownloadURLForProvider(raw, providerConfig{})
}

func (s *server) validateDownloadURLForProvider(raw string, provider providerConfig) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("volcengine returned an unsafe video URL")
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range s.cfg.allowedDownloadHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if strings.HasPrefix(allowed, ".") && strings.HasSuffix(host, allowed) || host == allowed {
			return u, nil
		}
	}
	if provider.baseURL != "" {
		if providerURL, parseErr := url.Parse(provider.baseURL); parseErr == nil && strings.EqualFold(providerURL.Hostname(), u.Hostname()) {
			return u, nil
		}
	}
	return nil, fmt.Errorf("video download host is not allowed")
}

func normalizeTask(task arkTask) map[string]any {
	out := map[string]any{
		"id": task.ID, "model": task.Model, "status": normalizeStatus(task.Status),
		"resolution": task.Resolution, "ratio": task.Ratio,
		"usage": map[string]any{"completion_tokens": task.Usage.CompletionTokens, "total_tokens": task.Usage.TotalTokens},
	}
	if task.Content.VideoURL != "" {
		out["video"] = map[string]any{"url": task.Content.VideoURL, "duration": task.Duration}
	}
	if len(task.Error) > 0 && string(task.Error) != "null" {
		out["error"] = json.RawMessage(task.Error)
	}
	return out
}

func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "done":
		return "done"
	case "queued", "running", "pending":
		return "pending"
	case "cancelled", "canceled":
		return "cancelled"
	case "expired":
		return "expired"
	default:
		return "failed"
	}
}

func collectImageURLs(values ...json.RawMessage) ([]string, error) {
	out := make([]string, 0)
	for _, raw := range values {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var items []json.RawMessage
		if raw[0] == '[' {
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, fmt.Errorf("invalid image list")
			}
		} else {
			items = []json.RawMessage{raw}
		}
		for _, item := range items {
			var value string
			if err := json.Unmarshal(item, &value); err != nil {
				var object struct {
					URL      string          `json:"url"`
					ImageURL json.RawMessage `json:"image_url"`
				}
				if err := json.Unmarshal(item, &object); err != nil {
					return nil, fmt.Errorf("invalid image value")
				}
				value = object.URL
				if value == "" && len(object.ImageURL) > 0 {
					if err := json.Unmarshal(object.ImageURL, &value); err != nil {
						var nested struct {
							URL string `json:"url"`
						}
						if err := json.Unmarshal(object.ImageURL, &nested); err != nil {
							return nil, fmt.Errorf("invalid image_url value")
						}
						value = nested.URL
					}
				}
			}
			value = strings.TrimSpace(value)
			if value == "" {
				return nil, fmt.Errorf("image URL cannot be empty")
			}
			out = append(out, value)
		}
	}
	return out, nil
}

func writeUpstreamError(w http.ResponseWriter, status int, raw []byte) {
	message := "volcengine request failed"
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &parsed) == nil {
		message = firstNonEmpty(parsed.Error.Message, parsed.Message, message)
	}
	writeError(w, status, "upstream_error", message)
}

func writeError(w http.ResponseWriter, status int, errorType, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"type": errorType, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func setOptional(target map[string]any, key string, present bool, value any) {
	if present {
		target[key] = value
	}
}

func derefBool(value *bool) bool { return value != nil && *value }
func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
