import { describe, expect, it, vi } from "vitest";
import { AnthropicClient, AnthropicError } from "./anthropic";

const ok = () =>
  Response.json({
    id: "m",
    stop_reason: "end_turn",
    content: [{ type: "text", text: "{}" }],
    usage: { input_tokens: 1, output_tokens: 1 },
  });
const request = {
  model: "m",
  max_tokens: 10,
  system: [],
  messages: [{ role: "user" as const, content: "hi" }],
};

describe("AnthropicClient", () => {
  it("sends the key and version headers and returns the parsed message", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(ok());
    const client = new AnthropicClient({ apiKey: "K", fetch: fetchImpl });
    const response = await client.createMessage(request);
    expect(response.stop_reason).toBe("end_turn");
    const [url, init] = fetchImpl.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://api.anthropic.com/v1/messages");
    expect(init.headers).toMatchObject({ "x-api-key": "K", "anthropic-version": "2023-06-01" });
    expect(init.redirect).toBe("error");
    expect(JSON.parse(String(init.body))).toMatchObject({ model: "m", max_tokens: 10 });
  });
  it("retries overloaded and network errors, then succeeds", async () => {
    vi.useFakeTimers();
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(new Response("overloaded", { status: 529 }))
      .mockRejectedValueOnce(new Error("socket hang up"))
      .mockResolvedValueOnce(ok());
    const client = new AnthropicClient({ apiKey: "K", fetch: fetchImpl, maxRetries: 2 });
    const pending = client.createMessage(request);
    await vi.runAllTimersAsync();
    await expect(pending).resolves.toMatchObject({ stop_reason: "end_turn" });
    expect(fetchImpl).toHaveBeenCalledTimes(3);
    vi.useRealTimers();
  });
  it("does not retry client errors", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response("bad schema", { status: 400 }));
    const client = new AnthropicClient({ apiKey: "K", fetch: fetchImpl, maxRetries: 3 });
    await expect(client.createMessage(request)).rejects.toBeInstanceOf(AnthropicError);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });
  it("gives up after the retry budget", async () => {
    vi.useFakeTimers();
    const fetchImpl = vi.fn().mockImplementation(async () => new Response("", { status: 503 }));
    const client = new AnthropicClient({ apiKey: "K", fetch: fetchImpl, maxRetries: 1 });
    const pending = client.createMessage(request).catch((e: unknown) => e);
    await vi.runAllTimersAsync();
    const error = (await pending) as AnthropicError;
    expect(error.status).toBe(503);
    expect(fetchImpl).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });
});
