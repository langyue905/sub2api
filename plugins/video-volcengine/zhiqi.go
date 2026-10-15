package main

import (
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

func isZhiqiBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (strings.EqualFold(u.Hostname(), "zhiqiapi.com") || strings.HasSuffix(strings.ToLower(u.Hostname()), ".zhiqiapi.com"))
}

// Adapt the gateway's stable media contract only for Zhiqi's strict JSON API.
// Other Waninter providers retain their existing paths and payloads.
func normalizeZhiqiVideoPayload(input map[string]any) (map[string]any, error) {
	out := make(map[string]any)
	for _, field := range []string{"model", "prompt", "size", "resolution", "negative_prompt", "generate_audio", "return_last_frame", "seed", "n", "bypass_face_check", "grid_strength", "strength", "auto_face_mask"} {
		if value, ok := input[field]; ok {
			out[field] = value
		}
	}
	allowed := map[string]bool{}
	for key := range out {
		allowed[key] = true
	}
	groups := []struct {
		name    string
		aliases []string
		media   bool
		array   bool
	}{
		{"duration", []string{"duration", "seconds"}, false, false},
		{"aspect_ratio", []string{"aspect_ratio", "ratio"}, false, false},
		{"input_reference", []string{"input_reference", "image", "image_url"}, true, false},
		{"reference_image_urls", []string{"reference_image_urls", "images", "image_urls", "reference_images"}, true, true},
		{"reference_video_urls", []string{"reference_video_urls", "videos"}, true, true},
		{"reference_audio_urls", []string{"reference_audio_urls", "audios"}, true, true},
		{"first_frame_image_url", []string{"first_frame_image_url"}, true, false},
		{"last_frame_image_url", []string{"last_frame_image_url"}, true, false},
	}
	for _, group := range groups {
		var selected any
		found := false
		for _, alias := range group.aliases {
			allowed[alias] = true
			value := input[alias]
			if value == nil || value == "" {
				continue
			}
			if items, ok := value.([]any); ok && len(items) == 0 {
				continue
			}
			if items, ok := value.([]string); ok && len(items) == 0 {
				continue
			}
			if group.name == "duration" {
				number, err := strconv.ParseFloat(fmt.Sprint(value), 64)
				if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 || number >= float64(math.MaxInt) || number != math.Trunc(number) {
					return nil, fmt.Errorf("duration must be an integer number of seconds")
				}
				value = int(number)
			}
			if found {
				if group.name == "duration" && reflect.DeepEqual(selected, value) {
					continue
				}
				return nil, fmt.Errorf("conflicting aliases for %s", group.name)
			}
			selected, found = value, true
		}
		if !found {
			continue
		}
		if group.media {
			values := []any{selected}
			if group.array {
				switch items := selected.(type) {
				case []any:
					values = items
				case []string:
					values = make([]any, len(items))
					for i, item := range items {
						values[i] = item
					}
				default:
					return nil, fmt.Errorf("%s must be an array of public URLs", group.name)
				}
			}
			urls := make([]string, 0, len(values))
			for _, value := range values {
				raw := compatibleImageValueURL(value)
				u, err := url.Parse(raw)
				if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
					return nil, fmt.Errorf("%s requires public HTTP(S) URLs; upload reference media first", group.name)
				}
				urls = append(urls, raw)
			}
			if group.array {
				selected = urls
			} else {
				selected = urls[0]
			}
		}
		out[group.name] = selected
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("Zhiqi video API does not support field %s", field)
		}
	}
	return out, nil
}
