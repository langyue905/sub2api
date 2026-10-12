package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestValidateVideoPluginBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "docker service", value: "http://video-plugin-volcengine:8081"},
		{name: "localhost", value: "http://127.0.0.1:8081"},
		{name: "https plugin", value: "https://video.example.com/adapter"},
		{name: "private host rejected", value: "http://redis:6379", wantErr: true},
		{name: "userinfo rejected", value: "https://user:pass@video.example.com", wantErr: true},
		{name: "query rejected", value: "https://video.example.com?token=secret", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateVideoPluginBaseURL(test.value)
			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestVideoPluginEndpointURL(t *testing.T) {
	base, err := validateVideoPluginBaseURL("http://video-plugin-volcengine:8081/root")
	require.NoError(t, err)
	got, err := videoPluginEndpointURL(base, GrokMediaEndpointVideoStatus, "cgt-123")
	require.NoError(t, err)
	require.Equal(t, "http://video-plugin-volcengine:8081/root/plugin/v1/videos/cgt-123", got)
}

func TestVideoPluginCredentialIsSensitive(t *testing.T) {
	require.True(t, IsSensitiveCredentialKey("plugin_token"))
}

func TestNewVideoPluginRequestUsesServerTokenFallback(t *testing.T) {
	t.Setenv("VIDEO_PLUGIN_SHARED_SECRET", "server-managed-token")
	req, err := newVideoPluginRequest(context.Background(), http.MethodGet,
		"http://127.0.0.1:8081/plugin/v1/manifest", nil, "ark-key", "")
	require.NoError(t, err)
	require.Equal(t, "server-managed-token", req.Header.Get(videoPluginTokenHeader))
}

func TestApplyVideoPluginUpstreamHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8081/plugin/v1/manifest", nil)
	require.NoError(t, err)
	account := &Account{Platform: PlatformVideoPlugin, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"provider_protocol":     "openai_compatible",
		"provider_base_url":     "https://api.waninter.com",
		"provider_create_path":  "/v1/videos/generations",
		"provider_status_path":  "/v1/videos/{id}",
		"provider_content_path": "/v1/videos/{id}/content",
		"provider_models_path":  "/v1/models",
	}}
	applyVideoPluginUpstreamHeaders(req, account)
	require.Equal(t, "openai_compatible", req.Header.Get(videoPluginProviderHeader))
	require.Equal(t, "https://api.waninter.com", req.Header.Get(videoPluginProviderBaseHeader))
	require.Equal(t, "/v1/videos/{id}", req.Header.Get(videoPluginStatusPathHeader))
}

func TestNormalizeVideoPluginRequestBodyConvertsInfiniteCanvasMultipart(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "doubao-seedance-2-0"))
	require.NoError(t, writer.WriteField("prompt", "a red ball bouncing once"))
	require.NoError(t, writer.WriteField("seconds", "6"))
	require.NoError(t, writer.WriteField("size", "720x1280"))
	require.NoError(t, writer.WriteField("resolution_name", "480p"))
	require.NoError(t, writer.WriteField("preset", "normal"))
	partHeader := textproto.MIMEHeader{}
	partHeader.Set("Content-Disposition", `form-data; name="input_reference[]"; filename="first.png"`)
	partHeader.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(partHeader)
	require.NoError(t, err)
	_, err = part.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	converted, err := normalizeVideoPluginRequestBody(writer.FormDataContentType(), body.Bytes())
	require.NoError(t, err)
	require.Equal(t, "doubao-seedance-2-0", gjson.GetBytes(converted, "model").String())
	require.Equal(t, "a red ball bouncing once", gjson.GetBytes(converted, "prompt").String())
	require.Equal(t, int64(6), gjson.GetBytes(converted, "duration").Int())
	require.Equal(t, "480p", gjson.GetBytes(converted, "resolution").String())
	require.Equal(t, "9:16", gjson.GetBytes(converted, "ratio").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(converted, "reference_images.0").String(), "data:image/png;base64,"))
}

func TestNormalizeVideoPluginRequestBodyPreservesJSON(t *testing.T) {
	body := []byte(`{"model":"grok-video-1.5","prompt":"waves","duration":6}`)
	converted, err := normalizeVideoPluginRequestBody("application/json", body)
	require.NoError(t, err)
	require.Equal(t, body, converted)
}

func TestNormalizeVideoPluginRequestBodyRejectsUnsupportedResolution(t *testing.T) {
	_, err := normalizeVideoPluginRequestBody("application/json", []byte(`{"model":"sora-2","prompt":"test","resolution":"4k"}`))
	require.Error(t, err)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "sora-2"))
	require.NoError(t, writer.WriteField("prompt", "test"))
	require.NoError(t, writer.WriteField("resolution_name", "4k"))
	require.NoError(t, writer.Close())
	_, err = normalizeVideoPluginRequestBody(writer.FormDataContentType(), body.Bytes())
	require.Error(t, err)
}

func TestVideoPluginModelNeedsPublicReference(t *testing.T) {
	require.True(t, videoPluginModelNeedsPublicReference("grok-imagine-video-1.5-preview"))
	require.True(t, videoPluginModelNeedsPublicReference("kling-video-v3"))
	require.False(t, videoPluginModelNeedsPublicReference("Minimax-H3-768p-933-10s"))
}

func TestExternalizeVideoReferenceValueCreatesTemporaryPublicImage(t *testing.T) {
	dir := t.TempDir()
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)

	got, changed, err := externalizeVideoReferenceValue(
		context.Background(), dir, "https://mckj.hk.cn", map[string]any{"url": dataURL},
	)
	require.NoError(t, err)
	require.True(t, changed)
	publicURL := got.(map[string]any)["url"].(string)
	require.True(t, strings.HasPrefix(publicURL, "https://mckj.hk.cn/v1/video-references/"))
	name := strings.TrimPrefix(publicURL, "https://mckj.hk.cn/v1/video-references/")
	require.Regexp(t, temporaryVideoReferenceNamePattern, name)
	stored, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	require.Equal(t, png, stored)
}

func TestExternalizeVideoPluginReferenceImagesLeavesMinimaxUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "https://mckj.hk.cn/v1/videos", nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	body := []byte(`{"model":"Minimax-H3-768p-933-10s","reference_images":["data:image/png;base64,iVBORw0KGgo="]}`)

	got, err := externalizeVideoPluginReferenceImages(c, &config.Config{}, body)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestExternalizeVideoPluginReferenceImagesNormalizesGrok15ToSingleImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "https://mckj.hk.cn/v1/videos", nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	body := []byte(`{"model":"grok-imagine-video-1.5-preview","prompt":"animate","reference_images":["data:image/png;base64,iVBORw0KGgo="]}`)

	got, err := externalizeVideoPluginReferenceImages(c, &config.Config{}, body)
	require.NoError(t, err)
	require.Equal(t, "grok-imagine-video-1.5-preview", gjson.GetBytes(got, "model").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(got, "image.url").String(), "https://mckj.hk.cn/v1/video-references/"))
	require.False(t, gjson.GetBytes(got, "reference_images").Exists())
}

func TestExternalizeVideoPluginReferenceImagesRejectsMultipleGrok15Images(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "https://mckj.hk.cn/v1/videos", nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	body := []byte(`{"model":"grok-imagine-video-1.5","reference_images":["https://example.com/a.png","https://example.com/b.png"]}`)

	_, err := externalizeVideoPluginReferenceImages(c, &config.Config{}, body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "only supports one starting image")
}

func TestExternalizeVideoPluginReferenceImagesNormalizesGrokReferencesToObjects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "https://mckj.hk.cn/v1/videos", nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	body := []byte(`{"model":"grok-imagine-video","prompt":"animate","reference_images":["https://example.com/a.png",{"image_url":"https://example.com/b.png"}]}`)

	got, err := externalizeVideoPluginReferenceImages(c, &config.Config{}, body)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/a.png", gjson.GetBytes(got, "reference_images.0.url").String())
	require.Equal(t, "https://example.com/b.png", gjson.GetBytes(got, "reference_images.1.url").String())
	require.False(t, gjson.GetBytes(got, "reference_images.0.image_url").Exists())
}

func TestVideoReferencePublicBaseURLPrefersConfiguredFrontend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "http://internal-service/v1/videos", nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	c.Request.Host = "untrusted.example"
	c.Request.Header.Set("X-Forwarded-Proto", "http")
	cfg := &config.Config{}
	cfg.Server.FrontendURL = "https://mckj.hk.cn/some/path"

	got, err := videoReferencePublicBaseURL(c, cfg)
	require.NoError(t, err)
	require.Equal(t, "https://mckj.hk.cn", got)
}

func TestServeTemporaryVideoReferenceFromDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	name := "0123456789abcdef0123456789abcdef.png"
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), png, 0o600))

	router := gin.New()
	router.GET("/v1/video-references/:token", func(c *gin.Context) {
		serveTemporaryVideoReferenceFromDir(c, dir, time.Now())
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/video-references/"+name, nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, png, recorder.Body.Bytes())
	require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/video-references/not-valid.png", nil))
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestNormalizeVideoPluginPublicStatusSupportsInfiniteCanvas(t *testing.T) {
	body := []byte(`{"id":"task-1","status":"done","video":{"url":"https://mckj.hk.cn/v1/videos/task-1/content"}}`)
	normalized, err := normalizeVideoPluginPublicStatus(body)
	require.NoError(t, err)
	require.Equal(t, "completed", gjson.GetBytes(normalized, "status").String())
	require.Equal(t, "https://mckj.hk.cn/v1/videos/task-1/content", gjson.GetBytes(normalized, "url").String())
	require.Equal(t, "https://mckj.hk.cn/v1/videos/task-1/content", gjson.GetBytes(normalized, "video.url").String())
}
