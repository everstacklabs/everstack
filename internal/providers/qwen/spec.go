package qwen

import "github.com/everstacklabs/everstack/internal/providers/catalog"

func DefaultSpec() catalog.Spec {
	return catalog.Spec{
		Name:       "qwen",
		Display:    "Qwen",
		BaseURL:    "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		APIVersion: "2024-01-01",
	}
}
