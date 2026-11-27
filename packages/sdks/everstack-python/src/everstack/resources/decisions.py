"""Shared Jev, Kev and Laya decision API."""


from typing import Any, Optional

from pydantic import BaseModel, Field

from .._transport import AsyncTransport, Transport


class DecisionAnswer(BaseModel):
    type: str
    noul: Optional[float] = None
    choice: Optional[str] = None
    score: Optional[float] = None
    confidence: Optional[float] = None
    probabilities: dict[str, float] = Field(default_factory=dict)
    legend: dict[str, str] = Field(default_factory=dict)


class DecisionUsage(BaseModel):
    input_tokens: int = 0
    output_tokens: int = 0


class DecisionResponse(BaseModel):
    model: str
    answers: dict[str, DecisionAnswer]
    usage: DecisionUsage


class Decisions:
    def __init__(self, transport: Transport) -> None:
        self._transport = transport

    def create(
        self, *, model: str, state: Any, questions: dict[str, dict[str, Any]]
    ) -> DecisionResponse:
        data = self._transport.request(
            "POST",
            "/v1/systemone",
            json_body={
                "model": model,
                "state": state,
                "questions": questions,
            },
        )
        return DecisionResponse.model_validate(data)


class AsyncDecisions:
    def __init__(self, transport: AsyncTransport) -> None:
        self._transport = transport

    async def create(
        self, *, model: str, state: Any, questions: dict[str, dict[str, Any]]
    ) -> DecisionResponse:
        data = await self._transport.request(
            "POST",
            "/v1/systemone",
            json_body={
                "model": model,
                "state": state,
                "questions": questions,
            },
        )
        return DecisionResponse.model_validate(data)
