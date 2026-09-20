from everstack.generated.models import ALL_MODELS, MODEL_METADATA


def test_deprecated_published_model_literals_remain_compatible() -> None:
    legacy_models = {
        "@openai/gpt-4": "deprecated",
        "@azure-openai/gpt-4.1": "deprecated",
        "@qwen/qwen2.5-72b-instruct": "stable",
    }

    assert set(legacy_models).issubset(ALL_MODELS)
    statuses = {model.id: model.status for model in MODEL_METADATA}
    assert all(statuses[model] == status for model, status in legacy_models.items())
