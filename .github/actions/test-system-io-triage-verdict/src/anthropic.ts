/**
 * Minimal Messages API client. The action makes exactly one kind of request
 * (structured-output message, prompt-cached system prompt), so a fetch wrapper
 * replaces the SDK and keeps 17k lines out of the committed bundle.
 */
export interface MessageRequest {
  model: string;
  max_tokens: number;
  system: { type: "text"; text: string; cache_control?: { type: "ephemeral" } }[];
  messages: { role: "user" | "assistant"; content: string }[];
  output_config?: {
    format?: { type: "json_schema"; schema: unknown };
    effort?: "low" | "medium" | "high";
  };
}
export interface MessageResponse {
  id: string;
  stop_reason: string | null;
  content: { type: string; text?: string }[];
  usage: { input_tokens: number; output_tokens: number; cache_read_input_tokens?: number | null };
}
export interface ClientOptions {
  apiKey: string;
  baseURL?: string;
  timeoutMs?: number;
  maxRetries?: number;
  fetch?: typeof fetch;
}
export class AnthropicError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(`anthropic ${status}: ${message}`);
  }
}
const RETRYABLE = new Set([408, 409, 429, 500, 502, 503, 504, 529]);
const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

export class AnthropicClient {
  private readonly apiKey: string;
  private readonly baseURL: string;
  private readonly timeoutMs: number;
  private readonly maxRetries: number;
  private readonly fetchImpl: typeof fetch;
  constructor(o: ClientOptions) {
    if (!o.apiKey) throw new Error("anthropic api key is required");
    this.apiKey = o.apiKey;
    this.baseURL = (o.baseURL ?? "https://api.anthropic.com").replace(/\/$/, "");
    this.timeoutMs = o.timeoutMs ?? 60000;
    this.maxRetries = o.maxRetries ?? 2;
    this.fetchImpl = o.fetch ?? fetch;
  }
  async createMessage(body: MessageRequest): Promise<MessageResponse> {
    let last: unknown;
    for (let attempt = 0; attempt <= this.maxRetries; attempt++) {
      if (attempt > 0) await sleep(Math.min(8000, 1000 * 2 ** (attempt - 1)));
      let response: Response;
      try {
        response = await this.fetchImpl(`${this.baseURL}/v1/messages`, {
          method: "POST",
          redirect: "error",
          signal: AbortSignal.timeout(this.timeoutMs),
          headers: {
            "content-type": "application/json",
            "x-api-key": this.apiKey,
            "anthropic-version": "2023-06-01",
          },
          body: JSON.stringify(body),
        });
      } catch (error) {
        last = error;
        continue;
      }
      if (response.ok) return (await response.json()) as MessageResponse;
      const text = (await response.text()).slice(0, 600);
      last = new AnthropicError(response.status, text);
      if (!RETRYABLE.has(response.status)) throw last;
      const after = Number(response.headers.get("retry-after"));
      if (after > 0 && after <= 30) await sleep(after * 1000);
    }
    throw last;
  }
}
