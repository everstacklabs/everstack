"""Pytest-native evaluation harness (DeepEval-style) for Everstack.

Gate CI on model quality with plain assertions::

    from everstack import Everstack
    from everstack.evals import TestCase, Metric, assert_test

    client = Everstack(api_key="pk_...")

    def test_answer_is_relevant():
        assert_test(
            TestCase(input="What is 2+2?", actual_output="4"),
            ["answer_relevancy", Metric("faithfulness", threshold=0.7)],
            client=client,
        )

Scoring is delegated to the Everstack platform (the synchronous ``/v1/score-output``
RPC via ``client.evaluations.score``); pass ``score_fn=`` to inject your own scorer
(useful for tests). Metrics are either a built-in metric key (``str``) or a
``Metric`` with an explicit threshold / score-config id.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable, List, Optional, Sequence, Tuple, Union


@dataclass
class TestCase:
    """A single case to evaluate. ``actual_output`` is what your app produced."""

    input: Any
    actual_output: Any = None
    expected_output: Any = None
    context: Any = None
    metadata: Optional[dict] = None


@dataclass
class Metric:
    """A metric + the pass threshold (score in ``[0, 1]``). ``name`` may be a
    built-in metric key (e.g. ``"answer_relevancy"``) or reference an existing
    score-config by id via ``score_config_id``."""

    name: str
    threshold: float = 0.5
    score_config_id: Optional[str] = None


@dataclass
class MetricResult:
    name: str
    score: float
    threshold: float
    passed: bool
    reason: str = ""


@dataclass
class EvalResult:
    test_case: TestCase
    metrics: List[MetricResult]

    @property
    def passed(self) -> bool:
        return all(m.passed for m in self.metrics)


# score_fn(test_case, metric) -> (score, reason)
ScoreFn = Callable[[TestCase, Metric], Tuple[float, str]]

MetricLike = Union[str, Metric]


def _coerce_metric(m: MetricLike) -> Metric:
    if isinstance(m, Metric):
        return m
    if isinstance(m, str):
        return Metric(name=m)
    raise TypeError(f"metric must be a str or Metric, got {type(m)!r}")


def _extract_score(resp: Any, name: str) -> Tuple[float, str]:
    """Pull a numeric score (and optional reason) out of a ScoreOutput response,
    tolerating a few shapes: ``{"scores": {name: value, name_reason: ...}}`` or a
    flat ``{name: value}`` or a bare number."""
    reason = ""
    scores: Any = resp
    if isinstance(resp, dict):
        scores = resp.get("scores", resp.get("result", resp))
        if isinstance(scores, dict):
            r = scores.get(f"{name}_reason")
            if r:
                reason = str(r)
    if isinstance(scores, dict):
        for key in (name, "score", "value"):
            v = scores.get(key)
            if isinstance(v, (int, float)) and not isinstance(v, bool):
                return float(v), reason
            if isinstance(v, bool):
                return (1.0 if v else 0.0), reason
        for v in scores.values():
            if isinstance(v, bool):
                return (1.0 if v else 0.0), reason
            if isinstance(v, (int, float)):
                return float(v), reason
    if isinstance(resp, bool):
        return (1.0 if resp else 0.0), reason
    if isinstance(resp, (int, float)):
        return float(resp), reason
    raise ValueError(
        f"could not extract a numeric score for metric {name!r} from response: {resp!r}"
    )


def _default_score_fn(client: Any) -> ScoreFn:
    if client is None:
        raise ValueError(
            "evaluate()/assert_test() need either client= or score_fn="
        )

    def score_fn(tc: TestCase, metric: Metric) -> Tuple[float, str]:
        # The platform scores by score-config id (/v1/score-output). A bare
        # built-in metric key needs a materialized score config; otherwise the
        # caller should pass their own score_fn=.
        if not metric.score_config_id:
            raise ValueError(
                f"metric {metric.name!r} needs a score_config_id to score via "
                "the client (create a score config in Everstack), or pass "
                "score_fn= to score it yourself"
            )
        resp = client.evaluations.score(
            input=tc.input,
            output=tc.actual_output,
            expected_output=tc.expected_output,
            metadata=tc.metadata,
            scorer_config_ids=[metric.score_config_id],
        )
        return _extract_score(resp, metric.name)

    return score_fn


def evaluate(
    test_cases: Sequence[TestCase],
    metrics: Sequence[MetricLike],
    *,
    score_fn: Optional[ScoreFn] = None,
    client: Any = None,
) -> List[EvalResult]:
    """Score every test case against every metric and return structured results.
    Never raises on a failing metric — inspect ``EvalResult.passed`` / use
    :func:`assert_test` for CI gating."""
    ms = [_coerce_metric(m) for m in metrics]
    fn = score_fn or _default_score_fn(client)
    results: List[EvalResult] = []
    for tc in test_cases:
        mrs: List[MetricResult] = []
        for metric in ms:
            score, reason = fn(tc, metric)
            mrs.append(
                MetricResult(
                    name=metric.name,
                    score=score,
                    threshold=metric.threshold,
                    passed=score >= metric.threshold,
                    reason=reason,
                )
            )
        results.append(EvalResult(test_case=tc, metrics=mrs))
    return results


def assert_test(
    test_case: TestCase,
    metrics: Sequence[MetricLike],
    *,
    score_fn: Optional[ScoreFn] = None,
    client: Any = None,
) -> None:
    """pytest-native gate: raise ``AssertionError`` if any metric scores below its
    threshold, otherwise pass silently."""
    (result,) = evaluate([test_case], metrics, score_fn=score_fn, client=client)
    failures = [m for m in result.metrics if not m.passed]
    if failures:
        lines = [
            f"  - {m.name}: score {m.score:.3f} < threshold {m.threshold:.3f}"
            + (f"  ({m.reason})" if m.reason else "")
            for m in failures
        ]
        raise AssertionError(
            "Everstack eval assertion failed:\n" + "\n".join(lines)
        )


__all__ = [
    "TestCase",
    "Metric",
    "MetricResult",
    "EvalResult",
    "evaluate",
    "assert_test",
]
