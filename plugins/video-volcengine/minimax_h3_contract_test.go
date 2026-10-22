package main

import (
	"fmt"
	"math"
	"reflect"
	"testing"
)

func TestMiniMaxH3ProviderContract(t *testing.T) {
	sizes := map[string][2]string{
		"16:9": {"1376x768", "1920x1088"}, "9:16": {"768x1376", "1088x1920"},
		"1:1": {"1024x1024", "1440x1440"}, "2:3": {"832x1248", "1184x1760"},
		"3:2": {"1248x832", "1760x1184"}, "3:4": {"896x1184", "1248x1664"},
		"4:3": {"1184x896", "1664x1248"}, "21:9": {"1568x672", "2208x960"},
	}
	for ratio, fixed := range sizes {
		for i, tier := range []string{"768p", "1080p", "2k", "4k"} {
			for _, images := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/images=%v", tier, ratio, images), func(t *testing.T) {
					in := map[string]any{"model": "MiniMax-H3", "prompt": " camera move ", "resolution": tier, "aspect_ratio": ratio,
						"clarity": "bad", "metadata": map[string]any{"multiple": 20}, "mode": "bad", "workflow_size": "bad", "audio": true, "prompt_enhance": true}
					if images {
						in["reference_images"] = []any{map[string]any{"url": "https://media.example.com/image.png"}}
					}
					out, gotTier, err := normalizeMiniMaxH3Payload(in, false)
					if err != nil {
						t.Fatal(err)
					}
					size, workflow := "", "text-to-video"
					if i < 2 {
						size = fixed[i]
					} else if i == 2 {
						size = "2K"
					} else {
						size = "4K"
					}
					expected := map[string]any{"model": "MiniMax-H3", "prompt": "camera move", "seconds": 5, "size": size}
					if i >= 2 {
						expected["aspect_ratio"] = ratio
					}
					if images {
						expected["images"] = []string{"https://media.example.com/image.png"}
						workflow = "multi-reference"
						if i >= 2 {
							workflow = "cf-multi-reference"
						}
					}
					expected["workflow_id"] = workflow
					if gotTier != tier || !reflect.DeepEqual(expected, out) {
						t.Fatalf("tier=%s payload=%#v want=%#v", gotTier, out, expected)
					}
					// Gateway normalizes twice; the second pass must preserve all fields/tier.
					out["resolution"] = tier
					again, _, err := normalizeMiniMaxH3Payload(out, false)
					delete(out, "resolution")
					if err != nil || !reflect.DeepEqual(out, again) {
						t.Fatalf("non-idempotent: %#v %v", again, err)
					}
				})
			}
		}
	}
}

func TestMiniMaxH3RejectsInvalidParameters(t *testing.T) {
	cases := []map[string]any{
		{"prompt": " "}, {"resolution": "480p"}, {"resolution": "720p"}, {"resolution": "2160p"}, {"resolution": "4320p"},
		{"resolution": "2k", "aspect_ratio": "auto"}, {"size": "864x480"}, {"size": "1440x1440"},
		{"resolution": "2k", "size": "4K"}, {"resolution_name": "1080p"}, {"ratio": "1:1"},
		{"workflow_id": "cf-multi-reference"}, {"seconds": 3}, {"seconds": 16}, {"seconds": 4.5}, {"seconds": math.Inf(1)},
		{"seconds": 5, "duration": 6}, {"images": []any{"http://127.0.0.1/x"}}, {"images": []any{"http://10.0.0.1/x"}},
		{"images": []any{"file:///tmp/x"}}, {"images": []any{"data:image/png;base64,eA=="}},
		{"reference_videos": []any{"https://media.example.com/v.mp4"}}, {"reference_audios": []any{"https://media.example.com/a.mp3"}},
	}
	for i, extra := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			in := map[string]any{"model": "MiniMax-H3", "prompt": "test", "resolution": "768p", "aspect_ratio": "16:9"}
			for k, v := range extra {
				in[k] = v
			}
			if out, _, err := normalizeMiniMaxH3Payload(in, false); err == nil {
				t.Fatalf("accepted %#v as %#v", extra, out)
			}
		})
	}
	for _, seconds := range []int{4, 15} {
		if _, _, err := normalizeMiniMaxH3Payload(map[string]any{"prompt": "test", "size": "1440x1440", "seconds": seconds}, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, group := range []struct {
		key string
		max int
	}{{"images", 9}, {"reference_videos", 3}, {"reference_audios", 3}} {
		for _, n := range []int{group.max, group.max + 1} {
			refs := []string{}
			for range n {
				refs = append(refs, "https://media.example.com/ref")
			}
			in := map[string]any{"prompt": "test", "size": "2K", "aspect_ratio": "16:9", "images": []string{"https://media.example.com/img"}, group.key: refs}
			_, _, err := normalizeMiniMaxH3Payload(in, false)
			if (err != nil) != (n > group.max) {
				t.Fatalf("%s count=%d err=%v", group.key, n, err)
			}
		}
	}
}
