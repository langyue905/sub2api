package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// This contract is opt-in; the legacy provider remains unchanged.
func normalizeNativeGrokPayload(in map[string]any) (map[string]any, error) {
	model, _ := in["model"].(string)
	if model != "grok-imagine-video" && model != "grok-imagine-video-1.5" {
		return nil, fmt.Errorf("该原生视频接口不支持此模型名称，请配置上游确认的模型映射")
	}
	out := map[string]any{"model": model}
	prompt, _ := in["prompt"].(string)
	if strings.TrimSpace(prompt) != "" {
		out["prompt"] = prompt
	}
	readURL := func(value any) (map[string]string, error) {
		raw := compatibleImageValueURL(value)
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			return nil, fmt.Errorf("该视频接口要求公网图片地址；本地图片须先取得临时上传地址，不能直接发送图片内嵌数据")
		}
		return map[string]string{"url": raw}, nil
	}
	if image, exists := in["image"]; exists {
		value, err := readURL(image)
		if err != nil {
			return nil, err
		}
		out["image"] = value
	}
	refs, hasRefs := in["reference_images"]
	if _, exists := in["images"]; exists {
		return nil, fmt.Errorf("原生视频接口需要明确选择起始图或参考图，不能使用旧图片数组字段")
	}
	if hasRefs {
		items, ok := refs.([]any)
		if !ok || len(items) < 1 || len(items) > 7 {
			return nil, fmt.Errorf("参考图必须为一至七张")
		}
		if out["image"] != nil {
			return nil, fmt.Errorf("起始图与参考图不能同时提交")
		}
		if model == "grok-imagine-video-1.5" {
			return nil, fmt.Errorf("此上游的1.5模型仅支持单张起始图，不支持参考图模式")
		}
		normalized := make([]map[string]string, 0, len(items))
		for _, item := range items {
			value, err := readURL(item)
			if err != nil {
				return nil, err
			}
			normalized = append(normalized, value)
		}
		out["reference_images"] = normalized
	}
	if model == "grok-imagine-video-1.5" && out["image"] == nil {
		return nil, fmt.Errorf("此上游的1.5模型必须提供单张起始图")
	}
	if out["image"] == nil && strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("文生视频及参考图视频必须填写提示词")
	}
	duration := in["duration"]
	if duration == nil {
		duration = in["seconds"]
	}
	if duration == nil {
		duration = 8
	}
	seconds, err := strconv.Atoi(fmt.Sprint(duration))
	maxSeconds := 15
	if hasRefs {
		maxSeconds = 10
	}
	if err != nil || seconds < 1 || seconds > maxSeconds {
		return nil, fmt.Errorf("当前生成模式的视频时长必须为1至%d秒", maxSeconds)
	}
	out["duration"] = seconds
	resolution, _ := in["resolution"].(string)
	if resolution == "" {
		resolution = "480p"
	}
	if resolution != "480p" && resolution != "720p" && !(resolution == "1080p" && model == "grok-imagine-video-1.5") {
		return nil, fmt.Errorf("该模型不支持所选视频分辨率")
	}
	out["resolution"] = resolution
	ratio, _ := in["aspect_ratio"].(string)
	if ratio == "" {
		ratio, _ = in["ratio"].(string)
	}
	if ratio != "" {
		switch ratio {
		case "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3":
			out["aspect_ratio"] = ratio
		default:
			return nil, fmt.Errorf("不支持的视频宽高比")
		}
	}
	return out, nil
}
