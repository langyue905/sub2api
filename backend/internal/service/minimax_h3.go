package service

import (
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/tidwall/sjson"
)

// Keep this provider contract in sync with plugins/video-volcengine/minimax_h3.go.
// Source: https://image.kkone.vip/1/docs.html#model-minimax
var miniMaxH3Sizes = map[string][3]string{
	"16:9": {"864x480", "1376x768", "1920x1088"},
	"9:16": {"480x864", "768x1376", "1088x1920"},
	"1:1":  {"640x640", "1024x1024", "1440x1440"},
	"2:3":  {"544x800", "832x1248", "1184x1760"},
	"3:2":  {"800x544", "1248x832", "1760x1184"},
	"3:4":  {"576x736", "896x1184", "1248x1664"},
	"4:3":  {"736x576", "1184x896", "1664x1248"},
	"21:9": {"992x416", "1568x672", "2208x960"},
}

func isMiniMaxH3(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "MiniMax-H3")
}

// Normalize against the mapped provider contract while retaining the public
// model for pricing. Parsing the alias first can misclassify 2K as image quality.
func PrepareVideoPluginCreateRequest(account *Account, contentType string, body []byte) ([]byte, GrokMediaRequestInfo, error) {
	info := ParseGrokMediaRequest(contentType, body)
	model := info.Model
	mapped := account.GetMappedModel(model)
	if !isMiniMaxH3(model) && !isMiniMaxH3(mapped) {
		out, err := normalizeVideoPluginRequestBody(contentType, body)
		return out, ParseGrokMediaRequest("application/json", out), err
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if !strings.EqualFold(mediaType, "application/json") {
		return nil, info, fmt.Errorf("MiniMax-H3 requires application/json with public reference URLs")
	}
	out, err := sjson.SetBytes(body, "model", "MiniMax-H3")
	if err != nil {
		return nil, info, err
	}
	out, err = NormalizeMiniMaxH3Request("application/json", out)
	if err != nil {
		return nil, info, err
	}
	info = ParseGrokMediaRequest("application/json", out)
	info.Model = model
	out, err = sjson.SetBytes(out, "model", model)
	return out, info, err
}

func miniMaxH3Tier(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "768", "768p":
		return "768p"
	case "1080", "1080p":
		return "1080p"
	case "2k":
		return "2k"
	case "4k":
		return "4k"
	default:
		return ""
	}
}

// Returns a whitelist of upstream fields and the independent billing tier.
// Data URLs are allowed only inside the gateway, before HTTPS externalization.
func normalizeMiniMaxH3Payload(in map[string]any, allowData bool) (map[string]any, string, error) {
	text := func(key string) string { v, _ := in[key].(string); return strings.TrimSpace(v) }
	prompt := text("prompt")
	if prompt == "" {
		return nil, "", fmt.Errorf("MiniMax-H3 prompt is required")
	}
	seconds := 5
	for _, key := range []string{"seconds", "duration"} {
		if value, exists := in[key]; exists {
			n, err := strconv.ParseFloat(fmt.Sprint(value), 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n < 4 || n > 15 {
				return nil, "", fmt.Errorf("MiniMax-H3 seconds must be an integer from 4 to 15")
			}
			if _, both := in["seconds"]; both && key == "duration" && seconds != int(n) {
				return nil, "", fmt.Errorf("MiniMax-H3 seconds and duration conflict")
			}
			seconds = int(n)
		}
	}
	tier := ""
	for _, key := range []string{"resolution", "resolution_name"} {
		if _, exists := in[key]; exists {
			next := miniMaxH3Tier(text(key))
			if next == "" || (tier != "" && tier != next) {
				return nil, "", fmt.Errorf("MiniMax-H3 resolution must be 768p, 1080p, 2k or 4k and cannot conflict")
			}
			tier = next
		}
	}
	ratio := text("aspect_ratio")
	if alternate := text("ratio"); alternate != "" {
		if ratio != "" && ratio != alternate {
			return nil, "", fmt.Errorf("MiniMax-H3 aspect ratios conflict")
		}
		ratio = alternate
	}
	size := strings.ToLower(text("size"))
	if size == "2k" || size == "4k" {
		if tier != "" && tier != size {
			return nil, "", fmt.Errorf("MiniMax-H3 size and billing resolution conflict")
		}
		tier = size
	} else if size != "" {
		found := false
		for r, sizes := range miniMaxH3Sizes {
			for index, fixed := range sizes {
				if fixed != size {
					continue
				}
				selected := []string{"480p", "768p", "1080p"}[index]
				if index == 0 || (tier != "" && tier != selected) || (ratio != "" && ratio != r) {
					return nil, "", fmt.Errorf("MiniMax-H3 fixed size conflicts with resolution/aspect ratio or has no configured 480p price")
				}
				tier, ratio, found = selected, r, true
			}
		}
		if !found {
			return nil, "", fmt.Errorf("MiniMax-H3 size is not in the provider size table")
		}
	}
	if tier == "" {
		return nil, "", fmt.Errorf("MiniMax-H3 requires a supported size or resolution")
	}
	if ratio == "" && tier != "2k" && tier != "4k" {
		ratio = "16:9"
	}
	sizes, ok := miniMaxH3Sizes[ratio]
	if !ok {
		return nil, "", fmt.Errorf("MiniMax-H3 requires one of the eight supported aspect ratios")
	}
	if size == "" {
		switch tier {
		case "768p":
			size = sizes[1]
		case "1080p":
			size = sizes[2]
		default:
			size = tier
		}
	}
	out := map[string]any{"model": "MiniMax-H3", "prompt": prompt, "seconds": seconds, "size": size}
	if tier == "2k" || tier == "4k" {
		out["size"] = strings.ToUpper(tier)
		out["aspect_ratio"] = ratio
	}
	counts := map[string]int{}
	for _, group := range []struct {
		target  string
		sources []string
		limit   int
	}{
		{"images", []string{"image", "images", "reference_images"}, 9},
		{"reference_videos", []string{"videos", "reference_videos"}, 3},
		{"reference_audios", []string{"audios", "reference_audios"}, 3},
	} {
		values := []string{}
		var collect func(any) error
		collect = func(value any) error {
			switch v := value.(type) {
			case []any:
				for _, entry := range v {
					if err := collect(entry); err != nil {
						return err
					}
				}
				return nil
			case []string:
				for _, entry := range v {
					if err := collect(entry); err != nil {
						return err
					}
				}
				return nil
			case map[string]any:
				for _, key := range []string{"url", "image_url", "video_url", "audio_url"} {
					if entry, ok := v[key]; ok {
						return collect(entry)
					}
				}
			case string:
				v = strings.TrimSpace(v)
				u, err := url.Parse(v)
				valid := err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
				if valid {
					host := strings.ToLower(u.Hostname())
					ip := net.ParseIP(host)
					valid = host != "localhost" && !strings.HasSuffix(host, ".local") && (ip == nil || (!ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast()))
				}
				if allowData && (strings.HasPrefix(v, "data:image/") || strings.HasPrefix(v, "data:video/") || strings.HasPrefix(v, "data:audio/")) {
					valid = true
				}
				if !valid {
					return fmt.Errorf("MiniMax-H3 %s must contain public media URLs", group.target)
				}
				values = append(values, v)
				return nil
			}
			return fmt.Errorf("MiniMax-H3 %s contains an invalid reference", group.target)
		}
		for _, key := range group.sources {
			if value, exists := in[key]; exists {
				if err := collect(value); err != nil {
					return nil, "", err
				}
			}
		}
		if len(values) > group.limit {
			return nil, "", fmt.Errorf("MiniMax-H3 %s permits at most %d references", group.target, group.limit)
		}
		counts[group.target] = len(values)
		if len(values) > 0 {
			out[group.target] = values
		}
	}
	if counts["images"] == 0 && (counts["reference_videos"] > 0 || counts["reference_audios"] > 0) {
		return nil, "", fmt.Errorf("MiniMax-H3 video/audio references require at least one reference image")
	}
	if counts["images"] == 0 && (tier == "2k" || tier == "4k") {
		return nil, "", fmt.Errorf("MiniMax-H3 2K/4K 超分至少需要一张参考图 (super-resolution requires at least one reference image)")
	}
	workflow := "text-to-video"
	if counts["images"] > 0 {
		workflow = "multi-reference"
		if tier == "2k" || tier == "4k" {
			workflow = "cf-multi-reference"
		}
	}
	if mode := text("mode"); mode != "" {
		if mode != "first_last_frame" || counts["images"] != 2 {
			return nil, "", fmt.Errorf("MiniMax-H3 first_last_frame requires exactly two ordered reference images")
		}
		out["mode"] = mode
	}
	if given := text("workflow_id"); given != "" && given != workflow {
		return nil, "", fmt.Errorf("MiniMax-H3 workflow conflicts with references or size")
	}
	out["workflow_id"] = workflow
	return out, tier, nil
}

// NormalizeMiniMaxH3Request validates before routing, moderation and pricing.
// Keep the billing tier internally; the adapter strips it from the upstream JSON.
func NormalizeMiniMaxH3Request(contentType string, body []byte) ([]byte, error) {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if strings.EqualFold(mediaType, "multipart/form-data") {
		var info GrokMediaRequestInfo
		parseGrokMediaMultipartRequest(contentType, body, &info)
		if isMiniMaxH3(info.Model) {
			return nil, fmt.Errorf("MiniMax-H3 requires application/json with public reference URLs; multipart is not supported")
		}
		return body, nil
	}
	var in map[string]any
	if json.Unmarshal(body, &in) != nil {
		return body, nil
	}
	model, _ := in["model"].(string)
	if !isMiniMaxH3(model) {
		return body, nil
	}
	out, tier, err := normalizeMiniMaxH3Payload(in, true)
	if err != nil {
		return nil, err
	}
	out["resolution"] = tier
	return json.Marshal(out)
}

func h3HasExactVideoPrice(resolved *ResolvedPricing, tier string) bool {
	if resolved == nil || resolved.Mode != BillingModeVideo || miniMaxH3Tier(tier) == "" {
		return false
	}
	for _, interval := range resolved.RequestTiers {
		// Billing uses an exact tier label; aliases must not pass validation and bill zero.
		if interval.TierLabel == tier && interval.PerRequestPrice != nil {
			price := *interval.PerRequestPrice
			return validH3Price(price)
		}
	}
	return false
}

func validH3Price(price float64) bool {
	return price > 0 && !math.IsNaN(price) && !math.IsInf(price, 0)
}
