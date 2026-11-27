import type { Client } from "@connectrpc/connect";
import { fromJson, type JsonValue } from "@bufbuild/protobuf";
import type { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { SystemOneRequestSchema } from "@everstack/proto/everstack/gateway/v1/decisions_pb.js";
import { fromConnectError } from "../errors.js";

export interface DecisionQuestion {
  type: "noul" | "choice" | "score";
  instructions?: JsonValue;
  criteria?: JsonValue;
}
export interface DecisionParams {
  model: string;
  state: JsonValue;
  questions: Record<string, DecisionQuestion>;
}
export interface DecisionAnswer {
  type: string;
  noul?: number;
  choice?: string;
  score?: number;
  confidence?: number;
  probabilities: Record<string, number>;
  legend: Record<string, string>;
}
export interface DecisionResponse {
  model: string;
  answers: Record<string, DecisionAnswer>;
  usage: { input_tokens: number; output_tokens: number };
}
export class Decisions {
  constructor(private readonly client: Client<typeof GatewayService>) {}
  async create(params: DecisionParams): Promise<DecisionResponse> {
    try {
      const questions: Record<string, JsonValue> = {};
      for (const [id, question] of Object.entries(params.questions)) {
        questions[id] = {
          type: question.type,
          ...(question.instructions !== undefined ? { instructions: question.instructions } : {}),
          ...(question.criteria !== undefined ? { criteria: question.criteria } : {}),
        };
      }
      const response = await this.client.systemOne(fromJson(SystemOneRequestSchema, {
        model: params.model, state: params.state, questions,
      }));
      const answers: Record<string, DecisionAnswer> = {};
      for (const [id, answer] of Object.entries(response.answers)) {
        answers[id] = {
          type: answer.type, noul: answer.noul, choice: answer.choice,
          score: answer.score, confidence: answer.confidence,
          probabilities: answer.probabilities, legend: answer.legend,
        };
      }
      return { model: response.model, answers, usage: {
        input_tokens: response.usage?.inputTokens ?? 0,
        output_tokens: response.usage?.outputTokens ?? 0,
      } };
    } catch (error) { throw fromConnectError(error); }
  }
}
