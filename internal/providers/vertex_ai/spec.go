package vertex_ai

import "github.com/everstacklabs/everstack/internal/providers/catalog"

func DefaultSpec() catalog.Spec {
	return catalog.Spec{
		Name:       "vertex-ai",
		Display:    "Vertex AI",
		BaseURL:    "https://{location}-aiplatform.googleapis.com/v1/projects/{project}/locations/{location}",
		APIVersion: "v1",
	}
}
