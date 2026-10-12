package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const taskRouteTTL = 48 * time.Hour

// Namespace by provider and the authenticated gateway account. For external
// callers without an account ID use a hash of authorization; persist no keys.
func taskRouteKey(r *http.Request, provider providerConfig, id string) string {
	account := strings.TrimSpace(r.Header.Get("X-Sub2-Video-Account-ID"))
	if account == "" || account == "0" {
		auth := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		account = hex.EncodeToString(auth[:])
	}
	data, _ := json.Marshal([]string{provider.protocol, provider.baseURL, account, id})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (s *server) storeTaskRoute(r *http.Request, provider providerConfig, id string, route seedanceTaskRoute) error {
	key := taskRouteKey(r, provider, id)
	s.taskRoutes.Store(key, route)
	if s.cfg.taskRouteDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.cfg.taskRouteDir, 0o700); err != nil {
		return err
	}
	// Delete expired metadata so long-running adapters do not grow indefinitely.
	entries, err := os.ReadDir(s.cfg.taskRouteDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > taskRouteTTL {
				_ = os.Remove(filepath.Join(s.cfg.taskRouteDir, entry.Name()))
			}
		}
	}
	data, err := json.Marshal(route)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.cfg.taskRouteDir, ".route-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(s.cfg.taskRouteDir, key+".json"))
}

func (s *server) videoTaskPaths(r *http.Request, provider providerConfig, id string) (string, string) {
	key := taskRouteKey(r, provider, id)
	var route seedanceTaskRoute
	if value, ok := s.taskRoutes.Load(key); ok {
		route, _ = value.(seedanceTaskRoute)
	} else if s.cfg.taskRouteDir != "" {
		data, err := os.ReadFile(filepath.Join(s.cfg.taskRouteDir, key+".json"))
		if err == nil && json.Unmarshal(data, &route) == nil {
			s.taskRoutes.Store(key, route)
		}
	}
	if !route.CreatedAt.IsZero() && time.Since(route.CreatedAt) < taskRouteTTL && route.StatusPath != "" {
		return route.StatusPath, route.ContentPath
	}
	s.taskRoutes.Delete(key)
	return provider.statusPath, provider.contentPath
}
