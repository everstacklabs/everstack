package aws_bedrock

import "github.com/everstacklabs/everstack/internal/providers/catalog"

func DefaultSpec() catalog.Spec {
	return catalog.Spec{
		Name:       "aws-bedrock",
		Display:    "AWS Bedrock",
		BaseURL:    "https://bedrock-runtime.{region}.amazonaws.com",
		APIVersion: "2024-01-01",
	}
}
