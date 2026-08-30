#!/usr/bin/env python3
"""Model Type Generator for the Python SDK.

Reads the model catalog from model-catalog/providers and generates
Python type aliases for type-safe model autocomplete.

Usage: python scripts/generate_models.py
"""

from __future__ import annotations

import sys
from datetime import datetime, timezone
import json
import os
from pathlib import Path
from typing import Any, Dict, Iterable, List, Set

try:
    import yaml
except ImportError:
    print("PyYAML required: pip install pyyaml", file=sys.stderr)
    sys.exit(1)

SCRIPT_DIR = Path(__file__).resolve().parent
CATALOG_DIR = Path(
    os.environ.get(
        "EVERSTACK_MODEL_CATALOG_PATH",
        SCRIPT_DIR.parent.parent.parent.parent / "model-catalog",
    )
).resolve()
OUTPUT_PATH = SCRIPT_DIR.parent / "src" / "everstack" / "generated" / "models.py"


def to_title_case(s: str) -> str:
    return "".join(w.capitalize() for w in s.replace("-", "_").split("_"))


def quote(value: str) -> str:
    return json.dumps(value)


def read_yaml(path: Path) -> Dict[str, Any]:
    with path.open() as f:
        data = yaml.safe_load(f)
    return data or {}


def normalize_model(model: Dict[str, Any], model_path: Path) -> Dict[str, Any]:
    name = model.get("name")
    if not name:
        raise ValueError(f"Model file missing name: {model_path}")

    cost = model.get("cost") or {}
    limits = model.get("limits") or {}
    return {
        "name": name,
        "display_name": model.get("display_name", name),
        "capabilities": model.get("capabilities", []),
        "max_tokens": limits.get("max_tokens", model.get("max_tokens", 0)),
        "input_cost_per_1k": cost.get("input_per_1k", model.get("input_cost_per_1k", 0)),
        "output_cost_per_1k": cost.get("output_per_1k", model.get("output_cost_per_1k", 0)),
        "status": model.get("status", "stable"),
    }


def load_catalog(catalog_dir: Path) -> Dict[str, Any]:
    providers_dir = catalog_dir / "providers"
    if not providers_dir.exists():
        raise FileNotFoundError(f"Catalog providers directory not found: {providers_dir}")

    providers: Dict[str, Any] = {}
    for provider_dir in sorted(p for p in providers_dir.iterdir() if p.is_dir()):
        provider_path = provider_dir / "provider.yaml"
        if not provider_path.exists():
            continue

        provider_config = read_yaml(provider_path)
        models_dir = provider_dir / "models"
        models = []
        if models_dir.exists():
            for model_path in sorted(models_dir.glob("*.yaml")):
                models.append(normalize_model(read_yaml(model_path), model_path))

        provider_id = provider_dir.name
        providers[provider_id] = {
            "name": provider_config.get("display_name")
            or provider_config.get("name")
            or provider_id,
            "base_url": provider_config.get("base_url", ""),
            "models": models,
        }

    return {"providers": providers}


def load_catalog_metadata(catalog_dir: Path) -> Dict[str, str]:
    manifest_path = catalog_dir / "manifest.yaml"
    if not manifest_path.exists():
        return {}

    manifest = read_yaml(manifest_path)
    return {
        "version": manifest.get("version", ""),
        "generated_at": manifest.get("generated_at", ""),
    }


def literal_alias(name: str, values: Iterable[str], fallback: str = "str") -> List[str]:
    unique_values = sorted(set(values))
    if not unique_values:
        return [f"{name} = {fallback}"]

    lines = [f"{name} = Literal["]
    for value in unique_values:
        lines.append(f"    {quote(value)},")
    lines.append("]")
    return lines


def generate(catalog: Dict[str, Any], metadata: Dict[str, str]) -> str:
    lines: List[str] = [
        "# AUTO-GENERATED FILE - DO NOT EDIT DIRECTLY",
        "# Generated from model-catalog/providers",
        f"# Catalog version: {metadata.get('version') or 'unknown'}",
        f"# Catalog generated: {metadata.get('generated_at') or datetime.now(timezone.utc).isoformat()}",
        "",
        "from __future__ import annotations",
        "",
        "from typing import List, Literal, NamedTuple, Optional, Sequence, Tuple, Union",
        "",
        "",
    ]

    provider_type_names: List[str] = []
    all_model_ids: List[str] = []
    metadata_entries: List[Dict[str, Any]] = []
    capabilities: Set[str] = set()
    statuses: Set[str] = set()

    providers = catalog.get("providers", {})
    for provider_id in sorted(providers):
        provider = providers[provider_id]
        models = provider.get("models") or []
        active = [m for m in models if m.get("status") != "deprecated"]
        active.sort(key=lambda m: m["name"])

        if not active:
            continue

        type_name = f"{to_title_case(provider_id)}Model"
        provider_type_names.append(type_name)

        literals = [f"    {quote('@' + provider_id + '/' + m['name'])}" for m in active]
        lines.append(f"{type_name} = Literal[")
        lines.append(",\n".join(literals) + ",")
        lines.append("]")
        lines.append("")

        for m in active:
            full_id = f"@{provider_id}/{m['name']}"
            all_model_ids.append(full_id)
            metadata_entries.append(
                {
                    "id": full_id,
                    "provider": provider_id,
                    "model": m["name"],
                    "display_name": m.get("display_name", m["name"]),
                    "capabilities": m.get("capabilities", []),
                    "max_tokens": m.get("max_tokens", 0),
                    "status": m.get("status", "stable"),
                }
            )
            capabilities.update(m.get("capabilities", []))
            statuses.add(m.get("status", "stable"))

    # AllModels union
    if provider_type_names:
        lines.append("AllModels = Union[")
        lines.append("    " + ", ".join(provider_type_names) + ",")
        lines.append("]")
    else:
        lines.append("AllModels = str")
    lines.append("")

    lines.extend(literal_alias("Capability", capabilities))
    lines.append("")
    lines.extend(literal_alias("Status", statuses))
    lines.append("")

    # Provider literal
    provider_ids = sorted(
        p
        for p in providers
        if any(m.get("status") != "deprecated" for m in providers[p].get("models") or [])
    )
    lines.append("Provider = Literal[")
    lines.append("    " + ", ".join(f'"{p}"' for p in provider_ids) + ",")
    lines.append("]")
    lines.append("")

    # All models list
    all_model_ids.sort()
    lines.append("")
    lines.append("ALL_MODELS: Tuple[str, ...] = (")
    for mid in all_model_ids:
        lines.append(f'    "{mid}",')
    lines.append(")")
    lines.append("")

    # Metadata NamedTuple
    lines.append("")
    lines.append("class ModelMetadata(NamedTuple):")
    lines.append("    id: str")
    lines.append("    provider: str")
    lines.append("    model: str")
    lines.append("    display_name: str")
    lines.append("    capabilities: Sequence[Capability]")
    lines.append("    max_tokens: int")
    lines.append("    status: Status")
    lines.append("")

    # Metadata list
    metadata_entries.sort(key=lambda m: m["id"])
    lines.append("")
    lines.append("MODEL_METADATA: Tuple[ModelMetadata, ...] = (")
    for m in metadata_entries:
        caps = ", ".join(quote(c) for c in m["capabilities"])
        capability_tuple = f"({caps},)" if caps else "()"
        lines.append("    ModelMetadata(")
        lines.append(f"        id={quote(m['id'])},")
        lines.append(f"        provider={quote(m['provider'])},")
        lines.append(f"        model={quote(m['model'])},")
        lines.append(f"        display_name={quote(m['display_name'])},")
        lines.append(f"        capabilities={capability_tuple},")
        lines.append(f'        max_tokens={m["max_tokens"]},')
        lines.append(f"        status={quote(m['status'])},")
        lines.append("    ),")
    lines.append(")")
    lines.append("")

    # Helper functions
    lines.append("")
    lines.append("def get_model_metadata(model_id: str) -> Optional[ModelMetadata]:")
    lines.append('    """Get metadata for a specific model."""')
    lines.append("    for m in MODEL_METADATA:")
    lines.append("        if m.id == model_id:")
    lines.append("            return m")
    lines.append("    return None")
    lines.append("")
    lines.append("")
    lines.append("def get_models_by_provider(provider: str) -> List[ModelMetadata]:")
    lines.append('    """Get all models for a specific provider."""')
    lines.append("    return [m for m in MODEL_METADATA if m.provider == provider]")
    lines.append("")
    lines.append("")
    lines.append("def is_valid_model(model_id: str) -> bool:")
    lines.append('    """Check if a string is a valid model identifier."""')
    lines.append("    return model_id in ALL_MODELS")
    lines.append("")

    return "\n".join(lines)


def main() -> None:
    print(f"Reading catalog from: {CATALOG_DIR}")

    try:
        catalog = load_catalog(CATALOG_DIR)
        metadata = load_catalog_metadata(CATALOG_DIR)
    except Exception as exc:
        print(f"Failed to load catalog: {exc}", file=sys.stderr)
        sys.exit(1)

    providers = catalog.get("providers", {})
    print(f"Found providers: {', '.join(sorted(providers))}")

    output = generate(catalog, metadata)

    OUTPUT_PATH.parent.mkdir(parents=True, exist_ok=True)
    OUTPUT_PATH.write_text(output)
    print(f"Generated: {OUTPUT_PATH}")

    total = sum(
        len([m for m in p.get("models", []) if m.get("status") != "deprecated"])
        for p in providers.values()
    )
    print(f"Total models: {total}")


if __name__ == "__main__":
    main()
