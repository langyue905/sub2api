package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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

func TestVideoSeedanceReferencesBecomePublicURLs(t *testing.T) {
	for _, model := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-offical", "doubao-seedance-2-0"} {
		require.True(t, videoPluginModelNeedsPublicReference(model))
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "https://media.example/v1/videos/generations", nil)
	// ID3 is enough for Go's format sniffer; no model/provider is contacted.
	mp3 := "data:audio/mpeg;base64," + base64.StdEncoding.EncodeToString([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"))
	body, err := json.Marshal(map[string]any{
		"model": "seedance-2.0", "prompt": "animate", "duration": 7,
		"image":   map[string]string{"url": "data:image/png;base64,iVBORw0KGgo="},
		"audios":  []string{mp3},
		"videos":  []string{"https://cdn.example/reference.mp4"},
		"content": []any{map[string]any{"type": "audio_url", "audio_url": map[string]string{"url": mp3}}},
	})
	require.NoError(t, err)
	got, err := externalizeVideoPluginReferenceImages(c, &config.Config{}, body)
	require.NoError(t, err)
	require.Equal(t, "animate", gjson.GetBytes(got, "prompt").String())
	require.Equal(t, int64(7), gjson.GetBytes(got, "duration").Int())
	for _, field := range []string{"image.url", "audios.0", "content.0.audio_url.url"} {
		require.True(t, strings.HasPrefix(gjson.GetBytes(got, field).String(), "https://media.example/v1/video-references/"), field)
	}
	require.Equal(t, "https://cdn.example/reference.mp4", gjson.GetBytes(got, "videos.0").String())
}

func TestVideoReferenceUploadStreamsMediaAndSupportsRange(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/v1/video-references", NewVideoReferenceUploadHandler(&config.Config{}))
	router.GET("/v1/video-references/:token", ServeTemporaryVideoReference)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "untrusted.html")
	require.NoError(t, err)
	mp3 := []byte("ID3\x04\x00\x00\x00\x00\x00\x00reference-audio")
	_, err = part.Write(mp3)
	require.NoError(t, err)
	require.NoError(t, form.Close())
	req := httptest.NewRequest(http.MethodPost, "https://media.example/v1/video-references", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	publicURL := gjson.GetBytes(response.Body.Bytes(), "url").String()
	require.True(t, strings.HasSuffix(publicURL, ".mp3"))
	get := httptest.NewRequest(http.MethodGet, publicURL, nil)
	get.Header.Set("Range", "bytes=0-2")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, get)
	require.Equal(t, http.StatusPartialContent, response.Code)
	require.Equal(t, "ID3", response.Body.String())
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
}

func TestVideoReferenceRejectsNonMediaQuotaAndExpiredFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := saveTemporaryVideoMedia(context.Background(), dir, strings.NewReader("<html>not media</html>"))
	require.ErrorContains(t, err, "unsupported reference format")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "failed uploads must be removed")
	name := "0123456789abcdef0123456789abcdef.mp4"
	file, err := os.Create(filepath.Join(dir, name))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(temporaryVideoReferenceMaxBytes))
	require.NoError(t, file.Close())
	_, err = saveTemporaryVideoMedia(context.Background(), dir, strings.NewReader("ID3audio"))
	require.ErrorContains(t, err, "storage is full")
	expired := time.Now().Add(-temporaryVideoReferenceTTL - time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(dir, name), expired, expired))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/video-references/"+name, nil)
	c.Params = gin.Params{{Key: "token", Value: name}}
	serveTemporaryVideoReferenceFromDir(c, dir, time.Now())
	require.Equal(t, http.StatusNotFound, c.Writer.Status())
}

func TestVideoReferenceUploadRequiresHTTPS(t *testing.T) {
	router := gin.New()
	router.POST("/v1/video-references", NewVideoReferenceUploadHandler(&config.Config{}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://media.example/v1/video-references", nil))
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "HTTPS")
}
