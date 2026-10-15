package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

const temporaryVideoMediaFileMaxBytes = int64(32 << 20)

var temporaryVideoReferenceMu sync.Mutex

func isSeedanceVideoModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "seedance-") || strings.HasPrefix(model, "doubao-seedance-")
}

// NewVideoReferenceUploadHandler receives a single media file behind API-key
// authentication. Stream it to disk rather than buffering video as JSON/base64.
func NewVideoReferenceUploadHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		base, err := videoReferencePublicBaseURL(c, cfg)
		if err != nil || !strings.HasPrefix(base, "https://") {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Reference uploads require a public HTTPS frontend URL"}})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, temporaryVideoMediaFileMaxBytes+(64<<10))
		reader, err := c.Request.MultipartReader()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Expected multipart/form-data with a file field"}})
			return
		}
		part, err := reader.NextPart()
		if err != nil || part.FormName() != "file" || part.FileName() == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "A reference media file is required"}})
			return
		}
		defer part.Close()
		name, err := saveTemporaryVideoMedia(c.Request.Context(), temporaryVideoReferenceDir(), part)
		if err != nil {
			status := http.StatusBadRequest
			var sizeError *http.MaxBytesError
			if errors.As(err, &sizeError) {
				status = http.StatusRequestEntityTooLarge
			}
			c.JSON(status, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"url": base + "/v1/video-references/" + name, "expires_in": int(temporaryVideoReferenceTTL.Seconds())})
	}
}

func saveTemporaryVideoMediaDataURL(ctx context.Context, dir, value string) (string, error) {
	header, encoded, ok := strings.Cut(value, ",")
	if !ok || !strings.HasSuffix(strings.ToLower(header), ";base64") || int64(len(encoded)) > (temporaryVideoMediaFileMaxBytes+2)/3*4 {
		return "", fmt.Errorf("reference media must be base64 and no larger than 32 MiB")
	}
	return saveTemporaryVideoMedia(ctx, dir, base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)))
}

func saveTemporaryVideoMedia(ctx context.Context, dir string, source io.Reader) (string, error) {
	temporaryVideoReferenceMu.Lock()
	defer temporaryVideoReferenceMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("temporary reference storage is unavailable")
	}
	if err := cleanupTemporaryVideoReferences(dir, time.Now()); err != nil {
		return "", fmt.Errorf("temporary reference cleanup failed")
	}
	used, err := temporaryVideoReferenceBytes(dir)
	if err != nil {
		return "", fmt.Errorf("temporary reference storage is unavailable")
	}
	capacity := min(temporaryVideoMediaFileMaxBytes, temporaryVideoReferenceMaxBytes-used)
	if capacity <= 0 {
		return "", fmt.Errorf("temporary reference storage is full")
	}
	file, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", fmt.Errorf("cannot save temporary reference")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(source, capacity+1))
	if err != nil {
		return "", fmt.Errorf("read reference media: %w", err)
	}
	if size == 0 || size > capacity {
		return "", fmt.Errorf("reference is empty, exceeds 32 MiB, or temporary storage is full")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("cannot read temporary reference")
	}
	var header [512]byte
	n, _ := file.Read(header[:])
	extension := temporaryVideoMediaExtension(header[:n])
	if extension == "" {
		return "", fmt.Errorf("unsupported reference format; use PNG/JPEG/WebP/GIF, MP4/WebM, MP3/WAV/OGG/M4A")
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("cannot finish temporary reference")
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("cannot create reference token")
	}
	name := fmt.Sprintf("%x%s", token, extension)
	if err := os.Rename(file.Name(), filepath.Join(dir, name)); err != nil {
		return "", fmt.Errorf("cannot publish temporary reference")
	}
	return name, nil
}

func temporaryVideoMediaExtension(header []byte) string {
	if contentType := detectedImageContentType(header); contentType != "" {
		return extensionForContentType(contentType)
	}
	switch http.DetectContentType(header) {
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wave", "audio/x-wav":
		return ".wav"
	case "application/ogg":
		return ".ogg"
	}
	// Go's sniffer does not classify the ISO BMFF M4A brand as audio.
	if len(header) >= 12 && string(header[4:8]) == "ftyp" && string(header[8:12]) == "M4A " {
		return ".m4a"
	}
	return ""
}
