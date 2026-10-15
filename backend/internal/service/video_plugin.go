package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const videoPluginTokenHeader = "X-Sub2-Plugin-Token"

const (
	temporaryVideoReferenceTTL      = 24 * time.Hour
	temporaryVideoReferenceMaxBytes = int64(256 << 20)
)

var temporaryVideoReferenceNamePattern = regexp.MustCompile(`^[a-f0-9]{32}\.(?:png|jpg|webp|gif|mp4|webm|mp3|wav|ogg|m4a)$`)

const (
	videoPluginProviderHeader     = "X-Sub2-Video-Upstream-Protocol"
	videoPluginProviderBaseHeader = "X-Sub2-Video-Upstream-Base-URL"
	videoPluginCreatePathHeader   = "X-Sub2-Video-Upstream-Create-Path"
	videoPluginStatusPathHeader   = "X-Sub2-Video-Upstream-Status-Path"
	videoPluginContentPathHeader  = "X-Sub2-Video-Upstream-Content-Path"
	videoPluginModelsPathHeader   = "X-Sub2-Video-Upstream-Models-Path"
)

// ForwardVideoPlugin forwards the stable Sub2API video contract to an isolated
// adapter. Provider-specific request and response conversion stays outside the
// main process.
func (s *OpenAIGatewayService) ForwardVideoPlugin(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	endpoint GrokMediaEndpoint,
	requestID string,
	body []byte,
	contentType string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	if account == nil || !account.IsVideoPlugin() {
		return nil, fmt.Errorf("video plugin account is required")
	}
	if endpoint != GrokMediaEndpointVideosGenerations &&
		endpoint != GrokMediaEndpointVideoStatus &&
		endpoint != GrokMediaEndpointVideoContent {
		return nil, fmt.Errorf("video plugin endpoint %s is not supported", endpoint)
	}

	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		return nil, fmt.Errorf("video plugin upstream api key is missing")
	}
	baseURL, err := validateVideoPluginBaseURL(account.GetVideoPluginBaseURL())
	if err != nil {
		return nil, err
	}

	if endpoint.RequiresRequestBody() {
		body, err = normalizeVideoPluginRequestBody(contentType, body)
		if err != nil {
			return nil, err
		}
		contentType = "application/json"
	}

	requestInfo := ParseGrokMediaRequest(contentType, body)
	upstreamModel := requestInfo.Model
	if endpoint.RequiresRequestBody() {
		if mapped := strings.TrimSpace(account.GetMappedModel(requestInfo.Model)); mapped != "" {
			upstreamModel = mapped
		}
		if upstreamModel != requestInfo.Model {
			body, err = sjson.SetBytes(body, "model", upstreamModel)
			if err != nil {
				return nil, fmt.Errorf("rewrite video plugin mapped model: %w", err)
			}
		}
		body, err = externalizeVideoPluginReferenceImages(c, s.cfg, body)
		if err != nil {
			return nil, err
		}
	}

	if endpoint == GrokMediaEndpointVideoContent {
		statusBody, statusHeaders, err := s.doVideoPluginJSONRequest(ctx, c, account, apiKey, baseURL, GrokMediaEndpointVideoStatus, requestID, nil)
		if err != nil {
			return nil, err
		}
		statusUsage := videoPluginUsageFromResponse(GrokMediaEndpointVideoStatus, GrokMediaRequestInfo{}, statusBody)
		result := videoPluginForwardResult(statusUsage, requestInfo.Model, upstreamModel, statusHeaders, startTime)

		contentURL, err := videoPluginEndpointURL(baseURL, endpoint, requestID)
		if err != nil {
			return nil, err
		}
		req, err := newVideoPluginRequest(ctx, http.MethodGet, contentURL, nil, apiKey, account.GetCredential("plugin_token"))
		if err != nil {
			return nil, err
		}
		applyVideoPluginUpstreamHeaders(req, account)
		if rangeHeader := strings.TrimSpace(c.GetHeader("Range")); rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		resp, err := s.doVideoPluginRequest(c, account, req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			return nil, s.videoPluginError(c, resp)
		}
		if err := writeGrokMediaContentResponse(c, resp); err != nil {
			return nil, err
		}
		return result, nil
	}

	responseBody, responseHeaders, err := s.doVideoPluginJSONRequest(ctx, c, account, apiKey, baseURL, endpoint, requestID, body)
	if err != nil {
		return nil, err
	}
	usage := videoPluginUsageFromResponse(endpoint, requestInfo, responseBody)
	if endpoint == GrokMediaEndpointVideoStatus {
		responseBody = rewriteGrokMediaVideoContentURLs(responseBody, requestID, grokMediaContentProxyURL(c, requestID))
		responseBody, err = normalizeVideoPluginPublicStatus(responseBody)
		if err != nil {
			return nil, err
		}
	}
	writeGrokMediaResponse(c, &http.Response{StatusCode: http.StatusOK, Header: responseHeaders}, responseBody, s.responseHeaderFilter)
	return videoPluginForwardResult(usage, requestInfo.Model, upstreamModel, responseHeaders, startTime), nil
}

func externalizeVideoPluginReferenceImages(c *gin.Context, cfg *config.Config, body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse video plugin request for reference images: %w", err)
	}
	model, _ := payload["model"].(string)
	if !videoPluginModelNeedsPublicReference(model) {
		return body, nil
	}
	publicBaseURL, err := videoReferencePublicBaseURL(c, cfg)
	if err != nil {
		return nil, err
	}
	dir := temporaryVideoReferenceDir()
	changed := false
	for _, field := range []string{"image", "images", "reference_images", "videos", "audios", "reference_videos", "reference_audios", "video", "audio", "content"} {
		value, ok := payload[field]
		if !ok {
			continue
		}
		next, fieldChanged, fieldErr := externalizeVideoReferenceValue(c.Request.Context(), dir, publicBaseURL, value)
		if fieldErr != nil {
			return nil, fmt.Errorf("prepare %s video reference: %w", field, fieldErr)
		}
		if fieldChanged {
			payload[field] = next
			changed = true
		}
	}
	if !changed && !isGrokVideoModel(model) {
		return body, nil
	}
	return normalizeVideoPluginGrokReferenceShape(model, payload)
}

func normalizeVideoPluginGrokReferenceShape(model string, payload map[string]any) ([]byte, error) {
	if !isGrokVideoModel(model) {
		return json.Marshal(payload)
	}
	references := collectVideoReferenceValues(payload)
	if len(references) == 0 {
		return json.Marshal(payload)
	}
	if isGrokVideo15Model(model) {
		if len(references) > 1 {
			return nil, fmt.Errorf("grok-imagine-video-1.5 only supports one starting image")
		}
		imageURL := videoReferenceURL(references[0])
		if imageURL == "" {
			return nil, fmt.Errorf("grok-imagine-video-1.5 starting image must include a public URL")
		}
		payload["image"] = map[string]string{"url": imageURL}
		delete(payload, "images")
		delete(payload, "reference_images")
	} else {
		normalized := make([]map[string]string, 0, len(references))
		for _, reference := range references {
			imageURL := videoReferenceURL(reference)
			if imageURL == "" {
				return nil, fmt.Errorf("grok video reference image must include a public URL")
			}
			normalized = append(normalized, map[string]string{"url": imageURL})
		}
		payload["reference_images"] = normalized
		delete(payload, "image")
		delete(payload, "images")
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode video plugin request with temporary references: %w", err)
	}
	return out, nil
}

func isGrokVideoModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "grok-imagine-video")
}

func isGrokVideo15Model(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "grok-imagine-video-1.5")
}

func collectVideoReferenceValues(payload map[string]any) []any {
	references := make([]any, 0, 4)
	for _, field := range []string{"image", "images", "reference_images"} {
		value, ok := payload[field]
		if !ok {
			continue
		}
		references = appendVideoReferenceValues(references, value)
	}
	return references
}

func appendVideoReferenceValues(references []any, value any) []any {
	switch item := value.(type) {
	case string:
		if strings.TrimSpace(item) != "" {
			return append(references, item)
		}
	case map[string]any:
		if videoReferenceURL(item) != "" {
			return append(references, item)
		}
	case []any:
		for _, entry := range item {
			references = appendVideoReferenceValues(references, entry)
		}
	case []string:
		for _, entry := range item {
			if strings.TrimSpace(entry) != "" {
				references = append(references, entry)
			}
		}
	}
	return references
}

func videoReferenceURL(value any) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case map[string]any:
		for _, key := range []string{"url", "image_url"} {
			if nested, ok := item[key]; ok {
				if value := videoReferenceURL(nested); value != "" {
					return value
				}
			}
		}
	case map[string]string:
		for _, key := range []string{"url", "image_url"} {
			if value := strings.TrimSpace(item[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func videoPluginModelNeedsPublicReference(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "grok-") || strings.HasPrefix(model, "kling-") || isSeedanceVideoModel(model)
}

func externalizeVideoReferenceValue(ctx context.Context, dir, publicBaseURL string, value any) (any, bool, error) {
	switch item := value.(type) {
	case string:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(item)), "data:audio/") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(item)), "data:video/") {
			name, err := saveTemporaryVideoMediaDataURL(ctx, dir, strings.TrimSpace(item))
			if err != nil {
				return nil, false, err
			}
			return strings.TrimRight(publicBaseURL, "/") + "/v1/video-references/" + name, true, nil
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(item)), "data:image/") {
			return value, false, nil
		}
		uploader := NewImageResultUploader(nil, "", defaultImageMaxDownloadBytes, nil)
		data, contentType, err := uploader.decodeImageDataURL(strings.TrimSpace(item))
		if err != nil {
			return nil, false, err
		}
		name, err := saveTemporaryVideoReference(ctx, dir, contentType, data)
		if err != nil {
			return nil, false, err
		}
		return strings.TrimRight(publicBaseURL, "/") + "/v1/video-references/" + name, true, nil
	case []any:
		changed := false
		out := make([]any, len(item))
		for index, entry := range item {
			next, entryChanged, err := externalizeVideoReferenceValue(ctx, dir, publicBaseURL, entry)
			if err != nil {
				return nil, false, err
			}
			out[index] = next
			changed = changed || entryChanged
		}
		if !changed {
			return value, false, nil
		}
		return out, true, nil
	case map[string]any:
		changed := false
		out := make(map[string]any, len(item))
		for key, entry := range item {
			next := entry
			entryChanged := false
			var err error
			if key == "url" || key == "image_url" || key == "video_url" || key == "audio_url" {
				next, entryChanged, err = externalizeVideoReferenceValue(ctx, dir, publicBaseURL, entry)
			}
			if err != nil {
				return nil, false, err
			}
			out[key] = next
			changed = changed || entryChanged
		}
		if !changed {
			return value, false, nil
		}
		return out, true, nil
	default:
		return value, false, nil
	}
}

func videoReferencePublicBaseURL(c *gin.Context, cfg *config.Config) (string, error) {
	candidates := make([]string, 0, 2)
	if cfg != nil && strings.TrimSpace(cfg.Server.FrontendURL) != "" {
		candidates = append(candidates, strings.TrimSpace(cfg.Server.FrontendURL))
	}
	scheme := strings.ToLower(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")))
	if scheme != "http" && scheme != "https" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	if strings.TrimSpace(c.Request.Host) != "" {
		candidates = append(candidates, scheme+"://"+strings.TrimSpace(c.Request.Host))
	}
	for _, candidate := range candidates {
		parsed, err := url.Parse(candidate)
		if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil {
			return parsed.Scheme + "://" + parsed.Host, nil
		}
	}
	return "", fmt.Errorf("public URL is unavailable for temporary video references")
}

func temporaryVideoReferenceDir() string {
	return filepath.Join(os.TempDir(), "sub2api-video-references")
}

func saveTemporaryVideoReference(_ context.Context, dir, contentType string, data []byte) (string, error) {
	temporaryVideoReferenceMu.Lock()
	defer temporaryVideoReferenceMu.Unlock()
	if len(data) == 0 || int64(len(data)) > defaultImageMaxDownloadBytes {
		return "", fmt.Errorf("temporary video reference has invalid size")
	}
	if detected := detectedImageContentType(data); detected == "" {
		return "", fmt.Errorf("temporary video reference is not a supported image")
	} else {
		contentType = detected
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create temporary video reference directory: %w", err)
	}
	if err := cleanupTemporaryVideoReferences(dir, time.Now()); err != nil {
		return "", err
	}
	used, err := temporaryVideoReferenceBytes(dir)
	if err != nil {
		return "", err
	}
	if used+int64(len(data)) > temporaryVideoReferenceMaxBytes {
		return "", fmt.Errorf("temporary video reference storage is full")
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("create temporary video reference token: %w", err)
	}
	name := fmt.Sprintf("%x%s", token, extensionForContentType(contentType))
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		return "", fmt.Errorf("write temporary video reference: %w", err)
	}
	return name, nil
}

func temporaryVideoReferenceBytes(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read temporary video reference directory: %w", err)
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !temporaryVideoReferenceNamePattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err == nil {
			total += info.Size()
		}
	}
	return total, nil
}

func cleanupTemporaryVideoReferences(dir string, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read temporary video reference directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !temporaryVideoReferenceNamePattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err == nil && now.Sub(info.ModTime()) >= temporaryVideoReferenceTTL {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}

func ServeTemporaryVideoReference(c *gin.Context) {
	serveTemporaryVideoReferenceFromDir(c, temporaryVideoReferenceDir(), time.Now())
}

func serveTemporaryVideoReferenceFromDir(c *gin.Context, dir string, now time.Time) {
	name := strings.TrimSpace(c.Param("token"))
	if !temporaryVideoReferenceNamePattern.MatchString(name) {
		c.Status(http.StatusNotFound)
		return
	}
	filePath := filepath.Join(dir, name)
	info, err := os.Stat(filePath)
	age := time.Duration(0)
	if err == nil {
		age = now.Sub(info.ModTime())
	}
	if err != nil || !info.Mode().IsRegular() || age >= temporaryVideoReferenceTTL {
		if err == nil && age >= temporaryVideoReferenceTTL {
			_ = os.Remove(filePath)
		}
		c.Status(http.StatusNotFound)
		return
	}
	remaining := temporaryVideoReferenceTTL - age
	c.Header("Cache-Control", fmt.Sprintf("public, max-age=%d", max(0, int(remaining.Seconds()))))
	c.Header("X-Content-Type-Options", "nosniff")
	c.File(filePath)
}

func normalizeVideoPluginRequestBody(contentType string, body []byte) ([]byte, error) {
	if gjson.ValidBytes(body) {
		resolution := firstNonEmpty(gjson.GetBytes(body, "resolution").String(), gjson.GetBytes(body, "resolution_name").String())
		if resolution != "" {
			if _, ok := LookupVideoBillingResolution(resolution); !ok {
				return nil, fmt.Errorf("video plugin resolution must be 480p, 720p or 1080p")
			}
		}
		return body, nil
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if mediaType != "multipart/form-data" {
		return nil, fmt.Errorf("video plugin accepts application/json or multipart/form-data requests")
	}

	var rawInfo GrokMediaRequestInfo
	parseGrokMediaMultipartRequest(contentType, body, &rawInfo)
	if rawInfo.Resolution != "" {
		if _, ok := LookupVideoBillingResolution(rawInfo.Resolution); !ok {
			return nil, fmt.Errorf("video plugin resolution must be 480p, 720p or 1080p")
		}
	}
	info := ParseGrokMediaRequest(contentType, body)
	if info.Model == "" || info.Prompt == "" {
		return nil, fmt.Errorf("video plugin multipart request requires model and prompt")
	}
	payload := map[string]any{
		"model":      info.Model,
		"prompt":     info.Prompt,
		"duration":   info.DurationSeconds,
		"resolution": info.Resolution,
	}
	if ratio := videoPluginRatioFromSize(info.Size); ratio != "" {
		payload["ratio"] = ratio
	}

	images := make([]string, 0, len(info.InputImageURLs)+len(info.Uploads))
	for _, imageURL := range info.InputImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, imageURL)
		}
	}
	for _, upload := range info.Uploads {
		if dataURL := upload.ModerationDataURL(); dataURL != "" {
			images = append(images, dataURL)
		}
	}
	if len(images) > 0 {
		payload["reference_images"] = images
	}

	converted, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("convert video plugin multipart request: %w", err)
	}
	return converted, nil
}

func videoPluginRatioFromSize(size string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return ""
	}
	width, widthErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, heightErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return ""
	}
	switch {
	case width == height:
		return "1:1"
	case width > height:
		return "16:9"
	default:
		return "9:16"
	}
}

func normalizeVideoPluginPublicStatus(body []byte) ([]byte, error) {
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("video plugin returned an invalid status response")
	}
	out := body
	status := firstVideoPluginString(out, "status", "state", "data.status", "data.state", "data.result.status", "data.result.state", "result.status", "result.state", "output.status", "output.state")
	if normalized := normalizeVideoPluginStatus(status); normalized != "" && normalized != status {
		var err error
		out, err = sjson.SetBytes(out, "status", normalized)
		if err != nil {
			return nil, fmt.Errorf("normalize video plugin completion status: %w", err)
		}
	}
	if !gjson.GetBytes(out, "url").Exists() {
		for _, field := range videoPluginResultURLFields {
			videoURL := firstVideoPluginString(out, field)
			if videoURL == "" {
				continue
			}
			var err error
			out, err = sjson.SetBytes(out, "url", videoURL)
			if err != nil {
				return nil, fmt.Errorf("normalize video plugin result url: %w", err)
			}
			break
		}
	}
	return out, nil
}

var videoPluginResultURLFields = []string{
	"video.url", "video_url", "url", "result_url", "download_url",
	"content.video_url", "content.url",
	"data.video.url", "data.video_url", "data.url", "data.download_url",
	"data.content.video_url", "data.content.url",
	"data.result.video.url", "data.result.video_url", "data.result.url", "data.result.download_url",
	"data.result.content.video_url", "data.result.content.url",
	"data.result.output.video.url", "data.result.output.video_url", "data.result.output.url",
	"result.video.url", "result.video_url", "result.url", "result.download_url",
	"result.content.video_url", "result.content.url",
	"output.video.url", "output.video_url", "output.url", "output.download_url",
	"output.content.video_url", "output.content.url",
	"response.video.url", "response.video_url", "response.url",
}

func firstVideoPluginString(body []byte, fields ...string) string {
	for _, field := range fields {
		value := strings.TrimSpace(gjson.GetBytes(body, field).String())
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizeVideoPluginStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "done", "completed", "complete", "succeeded", "success", "ready", "finished":
		return "completed"
	case "queued", "pending", "running", "processing", "in_progress", "in-progress", "created":
		return "pending"
	case "cancelled", "canceled", "expired":
		return "cancelled"
	case "failed", "error":
		return "failed"
	default:
		return strings.TrimSpace(raw)
	}
}

func (s *OpenAIGatewayService) doVideoPluginJSONRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	apiKey string,
	baseURL *url.URL,
	endpoint GrokMediaEndpoint,
	requestID string,
	body []byte,
) ([]byte, http.Header, error) {
	targetURL, err := videoPluginEndpointURL(baseURL, endpoint, requestID)
	if err != nil {
		return nil, nil, err
	}
	method := http.MethodGet
	var reader io.Reader
	if endpoint.RequiresRequestBody() {
		method = http.MethodPost
		reader = bytes.NewReader(body)
	}
	req, err := newVideoPluginRequest(ctx, method, targetURL, reader, apiKey, account.GetCredential("plugin_token"))
	if err != nil {
		return nil, nil, err
	}
	if endpoint.RequiresRequestBody() {
		req.Header.Set("Content-Type", "application/json")
	}
	applyVideoPluginUpstreamHeaders(req, account)
	resp, err := s.doVideoPluginRequest(c, account, req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, nil, s.videoPluginError(c, resp)
	}
	responseBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, nil, err
	}
	return responseBody, resp.Header.Clone(), nil
}

func applyVideoPluginUpstreamHeaders(req *http.Request, account *Account) {
	if req == nil || account == nil {
		return
	}
	req.Header.Set("X-Sub2-Video-Account-ID", strconv.FormatInt(account.ID, 10))
	for _, item := range []struct {
		header string
		key    string
	}{
		{videoPluginProviderHeader, "provider_protocol"},
		{videoPluginProviderBaseHeader, "provider_base_url"},
		{videoPluginCreatePathHeader, "provider_create_path"},
		{videoPluginStatusPathHeader, "provider_status_path"},
		{videoPluginContentPathHeader, "provider_content_path"},
		{videoPluginModelsPathHeader, "provider_models_path"},
	} {
		if value := strings.TrimSpace(account.GetCredential(item.key)); value != "" {
			req.Header.Set(item.header, value)
		}
	}
}

func (s *OpenAIGatewayService) doVideoPluginRequest(c *gin.Context, account *Account, req *http.Request) (*http.Response, error) {
	startedAt := time.Now()
	resp, err := s.httpUpstream.Do(req, "", account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(startedAt).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(req.Context(), c, account, err, false)
	}
	return resp, nil
}

func (s *OpenAIGatewayService) videoPluginError(c *gin.Context, resp *http.Response) error {
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return err
	}
	setOpsUpstreamError(c, resp.StatusCode, "video plugin request failed", truncateString(string(body), 512))
	return &UpstreamFailoverError{
		StatusCode:      resp.StatusCode,
		ResponseBody:    body,
		ResponseHeaders: resp.Header.Clone(),
	}
}

func validateVideoPluginBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("video plugin base_url is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("video plugin base_url is invalid")
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme == "http" {
		if host != "localhost" && host != "127.0.0.1" && host != "::1" && !strings.HasPrefix(host, "video-plugin-") {
			return nil, fmt.Errorf("insecure video plugin base_url is only allowed for local plugin hosts")
		}
	} else if u.Scheme != "https" {
		return nil, fmt.Errorf("video plugin base_url must use http or https")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

func videoPluginEndpointURL(baseURL *url.URL, endpoint GrokMediaEndpoint, requestID string) (string, error) {
	if baseURL == nil {
		return "", fmt.Errorf("video plugin base_url is required")
	}
	u := *baseURL
	switch endpoint {
	case GrokMediaEndpointVideosGenerations:
		u.Path = path.Join(u.Path, "/plugin/v1/videos")
	case GrokMediaEndpointVideoStatus:
		if strings.TrimSpace(requestID) == "" {
			return "", fmt.Errorf("video plugin request_id is required")
		}
		u.Path = path.Join(u.Path, "/plugin/v1/videos", url.PathEscape(requestID))
	case GrokMediaEndpointVideoContent:
		if strings.TrimSpace(requestID) == "" {
			return "", fmt.Errorf("video plugin request_id is required")
		}
		u.Path = path.Join(u.Path, "/plugin/v1/videos", url.PathEscape(requestID), "content")
	default:
		return "", fmt.Errorf("unsupported video plugin endpoint %s", endpoint)
	}
	return u.String(), nil
}

func newVideoPluginRequest(ctx context.Context, method, targetURL string, body io.Reader, apiKey, pluginToken string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	pluginToken = strings.TrimSpace(pluginToken)
	if pluginToken == "" {
		pluginToken = strings.TrimSpace(os.Getenv("VIDEO_PLUGIN_SHARED_SECRET"))
	}
	if pluginToken != "" {
		req.Header.Set(videoPluginTokenHeader, pluginToken)
	}
	return req, nil
}

func videoPluginUsageFromResponse(endpoint GrokMediaEndpoint, info GrokMediaRequestInfo, body []byte) grokMediaUsageMetadata {
	meta := grokMediaUsageFromResponse(endpoint, info, body)
	if endpoint == GrokMediaEndpointVideoStatus && meta.VideoCount > 0 {
		// A status may omit model. Defer duration normalization until the handler
		// restores the create-time billing model, instead of applying Grok limits.
		if duration := gjson.GetBytes(body, "video.duration"); duration.Type == gjson.Number && duration.Int() > 0 {
			meta.VideoDurationSeconds = int(duration.Int())
		}
	}
	return meta
}

func videoPluginForwardResult(meta grokMediaUsageMetadata, requestModel, upstreamModel string, headers http.Header, start time.Time) *OpenAIForwardResult {
	model := firstNonEmpty(requestModel, meta.Model)
	billingModel := firstNonEmpty(requestModel, meta.BillingModel, model)
	return &OpenAIForwardResult{
		ResponseID:           meta.ResponseID,
		Usage:                meta.Usage,
		Model:                model,
		BillingModel:         billingModel,
		UpstreamModel:        upstreamModel,
		ResponseHeaders:      headers,
		Duration:             time.Since(start),
		VideoCount:           meta.VideoCount,
		VideoResolution:      meta.VideoResolution,
		VideoDurationSeconds: meta.VideoDurationSeconds,
	}
}

// An arbitrary video provider has no built-in per-second price. Reject an
// unpriced create before spending upstream funds instead of using image rates.
func (s *OpenAIGatewayService) ValidateVideoPluginPricing(ctx context.Context, apiKey *APIKey, model, resolution string) error {
	if apiKeyHasConfiguredVideoPrice(apiKey, model, NormalizeVideoBillingResolutionOrDefault(resolution)) {
		return nil
	}
	if resolved := s.resolveOpenAIChannelPricing(ctx, model, apiKey); resolved != nil {
		switch resolved.Mode {
		case BillingModeToken, BillingModeVideo, BillingModePerRequest, BillingModeImage:
			return nil
		}
	}
	return fmt.Errorf("%w: video model %s", ErrModelPricingUnavailable, model)
}
