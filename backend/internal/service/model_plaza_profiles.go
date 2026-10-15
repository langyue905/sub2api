package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ModelPlazaProfile is presentation metadata only; it never participates in routing or billing.
// An empty platform applies to every platform exposing the exact model name.
type ModelPlazaProfile struct {
	Model       string                  `json:"model"`
	Platform    string                  `json:"platform,omitempty"`
	Summary     string                  `json:"summary"`
	Description string                  `json:"description"`
	CoverURL    string                  `json:"cover_url,omitempty"`
	Tags        []string                `json:"tags"`
	Specs       []ModelPlazaProfileSpec `json:"specs"`
}

type ModelPlazaProfileSpec struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

func parseModelPlazaProfiles(raw string) []ModelPlazaProfile {
	profiles := []ModelPlazaProfile{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &profiles); err != nil {
			return []ModelPlazaProfile{}
		}
	}
	if profiles == nil {
		return []ModelPlazaProfile{}
	}
	return profiles
}

func encodeModelPlazaProfiles(profiles []ModelPlazaProfile) (string, error) {
	if len(profiles) > 500 {
		return "", fmt.Errorf("最多配置 500 个模型介绍")
	}
	normalized := make([]ModelPlazaProfile, 0, len(profiles))
	seen := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		p.Model, p.Platform = strings.TrimSpace(p.Model), strings.TrimSpace(p.Platform)
		p.Summary, p.Description = strings.TrimSpace(p.Summary), strings.TrimSpace(p.Description)
		p.CoverURL = strings.TrimSpace(p.CoverURL)
		if p.Model == "" || len(p.Model) > 200 || len(p.Platform) > 64 {
			return "", fmt.Errorf("模型名称不能为空且最多 200 字节，平台最多 64 字节")
		}
		key := p.Platform + "\x00" + p.Model
		if seen[key] {
			return "", fmt.Errorf("模型介绍重复：%s / %s", p.Platform, p.Model)
		}
		seen[key] = true
		if len(p.Summary) > 1500 || len(p.Description) > 24000 || len(p.Tags) > 12 || len(p.Specs) > 16 {
			return "", fmt.Errorf("%s 的简介、详细介绍、标签或参数数量超过限制", p.Model)
		}
		if p.CoverURL != "" {
			u, err := url.Parse(p.CoverURL)
			if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || len(p.CoverURL) > 2048 {
				return "", fmt.Errorf("%s 的封面必须是 HTTP(S) 地址", p.Model)
			}
		}
		tags := make([]string, 0, len(p.Tags))
		for _, tag := range p.Tags {
			tag = strings.TrimSpace(tag)
			if len(tag) > 96 {
				return "", fmt.Errorf("%s 的标签过长", p.Model)
			}
			if tag != "" {
				tags = append(tags, tag)
			}
		}
		p.Tags = tags
		specs := make([]ModelPlazaProfileSpec, 0, len(p.Specs))
		for _, spec := range p.Specs {
			spec.Label, spec.Value = strings.TrimSpace(spec.Label), strings.TrimSpace(spec.Value)
			if spec.Label == "" && spec.Value == "" {
				continue
			}
			if spec.Label == "" || spec.Value == "" || len(spec.Label) > 128 || len(spec.Value) > 1500 {
				return "", fmt.Errorf("%s 的参数名称和内容必须填写且不能过长", p.Model)
			}
			specs = append(specs, spec)
		}
		p.Specs = specs
		normalized = append(normalized, p)
	}
	raw, err := json.Marshal(normalized)
	if len(raw) > 2*1024*1024 {
		return "", fmt.Errorf("模型介绍总大小不能超过 2 MiB")
	}
	return string(raw), err
}
