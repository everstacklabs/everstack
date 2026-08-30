package aws_bedrock

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

type Config struct {
	Region          string
	BaseURL         string
	SupportedModels []string
	Client          bedrockAPI
}

type bedrockAPI interface {
	Converse(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
	ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}
