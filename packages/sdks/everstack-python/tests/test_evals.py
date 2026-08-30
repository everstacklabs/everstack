"""Unit tests for the pytest-native eval harness. No network: scoring is
injected via score_fn."""

import pytest

from everstack.evals import (
    TestCase,
    Metric,
    evaluate,
    assert_test,
    _extract_score,
)


def _fixed(scores):
    """Return a score_fn that yields scores[metric.name] (default 1.0)."""

    def score_fn(tc, metric):
        return float(scores.get(metric.name, 1.0)), f"reason:{metric.name}"

    return score_fn


def test_evaluate_reports_pass_and_fail():
    results = evaluate(
        [TestCase(input="q", actual_output="a")],
        [Metric("relevancy", threshold=0.5), Metric("faithfulness", threshold=0.9)],
        score_fn=_fixed({"relevancy": 0.8, "faithfulness": 0.4}),
    )
    assert len(results) == 1
    by_name = {m.name: m for m in results[0].metrics}
    assert by_name["relevancy"].passed is True
    assert by_name["faithfulness"].passed is False
    assert results[0].passed is False


def test_assert_test_passes_when_all_above_threshold():
    # Should not raise.
    assert_test(
        TestCase(input="q", actual_output="a"),
        ["relevancy", Metric("faithfulness", threshold=0.7)],
        score_fn=_fixed({"relevancy": 0.9, "faithfulness": 0.8}),
    )


def test_assert_test_raises_with_failing_metric_named():
    with pytest.raises(AssertionError) as excinfo:
        assert_test(
            TestCase(input="q", actual_output="a"),
            [Metric("faithfulness", threshold=0.9)],
            score_fn=_fixed({"faithfulness": 0.3}),
        )
    msg = str(excinfo.value)
    assert "faithfulness" in msg
    assert "0.300" in msg


def test_string_metric_defaults_threshold_half():
    results = evaluate(
        [TestCase(input="q")],
        ["some_metric"],
        score_fn=_fixed({"some_metric": 0.5}),
    )
    m = results[0].metrics[0]
    assert m.threshold == 0.5
    assert m.passed is True  # 0.5 >= 0.5


def test_default_score_fn_requires_config_id():
    class FakeClient:
        pass

    with pytest.raises(ValueError):
        assert_test(
            TestCase(input="q"),
            ["relevancy"],  # no score_config_id, no score_fn
            client=FakeClient(),
        )


@pytest.mark.parametrize(
    "resp,expected",
    [
        ({"scores": {"relevancy": 0.7, "relevancy_reason": "ok"}}, 0.7),
        ({"scores": {"relevancy": True}}, 1.0),
        ({"relevancy": 0.42}, 0.42),
        (0.9, 0.9),
    ],
)
def test_extract_score_shapes(resp, expected):
    score, _reason = _extract_score(resp, "relevancy")
    assert score == expected


def test_default_score_fn_calls_client_score():
    calls = {}

    class FakeEvals:
        def score(self, **kwargs):
            calls.update(kwargs)
            return {"scores": {"my_scorer": 0.95}}

    class FakeClient:
        evaluations = FakeEvals()

    assert_test(
        TestCase(input="q", actual_output="a", expected_output="a"),
        [Metric("my_scorer", threshold=0.8, score_config_id="sc_1")],
        client=FakeClient(),
    )
    assert calls["scorer_config_ids"] == ["sc_1"]
