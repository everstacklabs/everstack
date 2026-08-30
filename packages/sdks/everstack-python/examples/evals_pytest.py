"""Pytest-native eval gating with the Everstack SDK (DeepEval-style).

Run with:  pytest examples/evals_pytest.py

Each test scores your app's output against Everstack score configs and fails
the build if a metric drops below its threshold — drop it straight into CI.
"""

import os

from everstack import Everstack
from everstack.evals import TestCase, Metric, assert_test, evaluate

client = Everstack(api_key=os.environ.get("EVERSTACK_API_KEY", "pk_..."))


def my_app(question: str) -> str:
    """Replace with your real application call."""
    resp = client.chat.completions.create(
        model="@openai/gpt-4o-mini",
        messages=[{"role": "user", "content": question}],
    )
    return resp.choices[0].message.content


def test_password_reset_answer_is_faithful():
    q = "How do I reset my password?"
    assert_test(
        TestCase(input=q, actual_output=my_app(q)),
        # Reference existing score configs by id; threshold gates the build.
        [
            Metric("faithfulness", threshold=0.7, score_config_id="sc_faithfulness"),
            Metric("answer_relevancy", threshold=0.6, score_config_id="sc_relevancy"),
        ],
        client=client,
    )


def test_batch_report():
    cases = [TestCase(input=q, actual_output=my_app(q)) for q in ("What is 2+2?", "Capital of France?")]
    results = evaluate(
        cases,
        [Metric("answer_relevancy", threshold=0.6, score_config_id="sc_relevancy")],
        client=client,
    )
    for r in results:
        for m in r.metrics:
            print(f"{r.test_case.input!r} · {m.name}: {m.score:.2f} {'PASS' if m.passed else 'FAIL'}")
    assert all(r.passed for r in results)


# Or inject your own scorer (no platform round-trip) for pure unit tests:
def test_with_custom_scorer():
    assert_test(
        TestCase(input="q", actual_output="a"),
        ["length_ok"],
        score_fn=lambda tc, metric: (1.0 if len(str(tc.actual_output)) > 0 else 0.0, ""),
    )
