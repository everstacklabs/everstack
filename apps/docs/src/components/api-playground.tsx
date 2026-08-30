import { useCallback, useEffect, useRef, useState } from "react";
import { codeToHtml } from "shiki";

interface APIPlaygroundProps {
  method: string;
  path: string;
  baseUrl?: string;
  defaultBody?: string;
  parameters?: Array<{
    name: string;
    in: string;
    required?: boolean;
    schema?: { type?: string };
  }>;
}

function useShikiHighlight(code: string, lang: string) {
  const [html, setHtml] = useState("");
  const prevCode = useRef("");

  useEffect(() => {
    if (!code || code === prevCode.current) return;
    prevCode.current = code;

    let cancelled = false;
    codeToHtml(code, {
      lang,
      themes: { light: "github-light-default", dark: "one-dark-pro" },
      defaultColor: false,
    }).then((result) => {
      if (!cancelled) setHtml(result);
    });
    return () => {
      cancelled = true;
    };
  }, [code, lang]);

  return html;
}

function HighlightedCode({ code, lang }: { code: string; lang: string }) {
  const html = useShikiHighlight(code, lang);

  if (!html) {
    return (
      <pre className="overflow-auto rounded-lg border border-fd-border bg-fd-secondary p-3 font-mono text-xs text-fd-foreground">
        {code}
      </pre>
    );
  }

  return (
    <div
      className="overflow-auto rounded-lg [&_pre]:!bg-fd-secondary [&_pre]:p-3 [&_pre]:text-xs [&_pre]:!m-0 [&_figure]:!m-0 [&_figure]:!border-0"
      // biome-ignore lint/security/noDangerouslySetInnerHtml: Shiki-highlighted markup built from this page's own code samples, never user input.
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

export function APIPlayground({
  method,
  path,
  baseUrl = "https://gateway.everstack.ai",
  defaultBody,
  parameters = [],
}: APIPlaygroundProps) {
  const [serverUrl, setServerUrl] = useState(baseUrl);
  const [apiKey, setApiKey] = useState("");
  const [body, setBody] = useState(defaultBody ?? "");
  const [paramValues, setParamValues] = useState<Record<string, string>>({});
  const [response, setResponse] = useState<string | null>(null);
  const [status, setStatus] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);

  const pathParams = parameters.filter((p) => p.in === "path");
  const queryParams = parameters.filter((p) => p.in === "query");

  const buildUrl = useCallback(() => {
    let resolvedPath = path;
    for (const p of pathParams) {
      resolvedPath = resolvedPath.replace(
        `{${p.name}}`,
        paramValues[p.name] || `{${p.name}}`,
      );
    }
    const url = new URL(resolvedPath, serverUrl);
    for (const p of queryParams) {
      if (paramValues[p.name])
        url.searchParams.set(p.name, paramValues[p.name]);
    }
    return url.toString();
  }, [path, serverUrl, paramValues, pathParams, queryParams]);

  const send = async () => {
    setLoading(true);
    setResponse(null);
    setStatus(null);
    try {
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
      };
      if (apiKey) headers.Authorization = `Bearer ${apiKey}`;

      const opts: RequestInit = { method: method.toUpperCase(), headers };
      if (
        ["POST", "PUT", "PATCH"].includes(method.toUpperCase()) &&
        body.trim()
      ) {
        opts.body = body;
      }

      const res = await fetch(buildUrl(), opts);
      setStatus(res.status);
      const text = await res.text();
      try {
        setResponse(JSON.stringify(JSON.parse(text), null, 2));
      } catch {
        setResponse(text);
      }
    } catch (err) {
      setResponse(`Error: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="not-prose my-6 rounded-xl border border-fd-border bg-fd-card p-4">
      <div className="mb-3 flex items-center gap-2">
        <span className="rounded-md bg-fd-primary px-2 py-0.5 text-xs font-bold text-fd-primary-foreground">
          {method.toUpperCase()}
        </span>
        <code className="text-sm text-fd-muted-foreground">{path}</code>
        <span className="flex-1" />
        <button
          type="button"
          onClick={send}
          disabled={loading}
          className="rounded-md bg-fd-primary px-3 py-1.5 text-xs font-medium text-fd-primary-foreground hover:opacity-90 disabled:opacity-50"
        >
          {loading ? "Sending..." : "Send"}
        </button>
      </div>

      <div className="grid gap-3">
        <div>
          <label
            className="mb-1 block text-xs font-medium text-fd-muted-foreground"
            htmlFor="api-playground-base-url"
          >
            Base URL
          </label>
          <input
            id="api-playground-base-url"
            type="text"
            value={serverUrl}
            onChange={(e) => setServerUrl(e.target.value)}
            className="w-full rounded-md border border-fd-border bg-fd-background px-3 py-1.5 text-sm text-fd-foreground"
          />
        </div>

        <div>
          <label
            className="mb-1 block text-xs font-medium text-fd-muted-foreground"
            htmlFor="api-playground-authorization"
          >
            Authorization (Bearer token)
          </label>
          <input
            id="api-playground-authorization"
            type="text"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder="YOUR_API_KEY"
            className="w-full rounded-md border border-fd-border bg-fd-background px-3 py-1.5 text-sm text-fd-foreground placeholder:text-fd-muted-foreground"
          />
        </div>

        {[...pathParams, ...queryParams].map((p) => (
          <div key={`${p.in}-${p.name}`}>
            <label
              className="mb-1 block text-xs font-medium text-fd-muted-foreground"
              htmlFor={`api-playground-param-${p.in}-${p.name}`}
            >
              {p.name}{" "}
              <span className="text-fd-muted-foreground/60">({p.in})</span>
              {p.required && <span className="text-red-500"> *</span>}
            </label>
            <input
              id={`api-playground-param-${p.in}-${p.name}`}
              type="text"
              value={paramValues[p.name] || ""}
              onChange={(e) =>
                setParamValues((prev) => ({
                  ...prev,
                  [p.name]: e.target.value,
                }))
              }
              className="w-full rounded-md border border-fd-border bg-fd-background px-3 py-1.5 text-sm text-fd-foreground"
            />
          </div>
        ))}

        {["POST", "PUT", "PATCH"].includes(method.toUpperCase()) && (
          <div>
            <label
              className="mb-1 block text-xs font-medium text-fd-muted-foreground"
              htmlFor="api-playground-request-body"
            >
              Request Body (JSON)
            </label>
            <textarea
              id="api-playground-request-body"
              value={body}
              onChange={(e) => setBody(e.target.value)}
              rows={Math.min(12, Math.max(4, body.split("\n").length + 1))}
              spellCheck={false}
              className="w-full rounded-lg border border-fd-border bg-fd-secondary px-3 py-2 font-mono text-xs text-fd-foreground leading-relaxed"
            />
          </div>
        )}

        {(response !== null || status !== null) && (
          <div>
            <div className="mb-1 flex items-center gap-2">
              <span className="text-xs font-medium text-fd-muted-foreground">
                Response
              </span>
              {status !== null && (
                <span
                  className={`rounded px-1.5 py-0.5 text-xs font-medium ${
                    status < 300
                      ? "bg-green-500/10 text-green-600 dark:text-green-400"
                      : status < 500
                        ? "bg-yellow-500/10 text-yellow-600 dark:text-yellow-400"
                        : "bg-red-500/10 text-red-600 dark:text-red-400"
                  }`}
                >
                  {status}
                </span>
              )}
            </div>
            <div className="max-h-80 overflow-auto">
              <HighlightedCode code={response ?? ""} lang="json" />
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
