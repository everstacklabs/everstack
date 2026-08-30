/**
 * Deno Function Runner for Docker containers
 * Receives execution request as first CLI argument (JSON)
 * Outputs result as JSON to stdout
 */

const WORK_DIR = "/tmp/function";
const MAX_OUTPUT_SIZE = 1024 * 1024; // 1MB

interface ExecutionRequest {
  request_id: string;
  function_id: string;
  code: string;
  packages?: string[];
  arguments: Record<string, unknown>;
  timeout_ms: number;
}

interface ExecutionResult {
  success: boolean;
  result?: unknown;
  error?: string;
  error_type?: string;
  stdout: string;
  stderr: string;
  duration_ms: number;
}

async function main(): Promise<void> {
  const startTime = Date.now();

  // Parse request from CLI argument
  let request: ExecutionRequest;
  try {
    request = JSON.parse(Deno.args[0] || "{}");
  } catch (err) {
    outputError("parse_error", `Failed to parse request: ${err}`);
    Deno.exit(1);
  }

  const { code, arguments: args, timeout_ms } = request;
  const execId = request.request_id || crypto.randomUUID();
  const workDir = `${WORK_DIR}/${execId}`;

  let stdout = "";
  let stderr = "";

  try {
    // Create work directory
    await Deno.mkdir(workDir, { recursive: true });

    // Write user code
    const codePath = `${workDir}/function.ts`;
    await Deno.writeTextFile(codePath, code);

    // Execute the function
    const result = await runFunction(codePath, args || {}, timeout_ms || 30000, workDir);
    stdout = result.stdout;
    stderr = result.stderr;

    const durationMs = Date.now() - startTime;

    // Output success result
    console.log(JSON.stringify({
      success: true,
      result: result.value,
      stdout: truncate(stdout, MAX_OUTPUT_SIZE),
      stderr: truncate(stderr, MAX_OUTPUT_SIZE),
      duration_ms: durationMs,
    }));

  } catch (err) {
    const durationMs = Date.now() - startTime;
    const errorMsg = err instanceof Error ? err.message : String(err);

    let errorType = "runtime";
    if (errorMsg.includes("timeout")) errorType = "timeout";
    else if (errorMsg.includes("SyntaxError")) errorType = "syntax";
    else if (errorMsg.includes("out of memory")) errorType = "oom";

    console.log(JSON.stringify({
      success: false,
      error: errorMsg,
      error_type: errorType,
      stdout: truncate(stdout, MAX_OUTPUT_SIZE),
      stderr: truncate(stderr, MAX_OUTPUT_SIZE),
      duration_ms: durationMs,
    }));

  } finally {
    // Cleanup
    try {
      await Deno.remove(workDir, { recursive: true });
    } catch {
      // Ignore cleanup errors
    }
  }
}

async function runFunction(
  codePath: string,
  args: Record<string, unknown>,
  timeoutMs: number,
  workDir: string
): Promise<{ value: unknown; stdout: string; stderr: string }> {
  // Create wrapper that imports and executes the function
  const wrapperCode = `
    const fn = await import("file://${codePath}");
    const handler = fn.default || fn;
    const args = JSON.parse(Deno.args[0]);

    const result = typeof handler === 'function'
      ? await handler(args)
      : handler;

    console.log(JSON.stringify({ __result__: result }));
  `;

  const wrapperPath = `${workDir}/wrapper.ts`;
  await Deno.writeTextFile(wrapperPath, wrapperCode);

  // Determine permissions based on network mode
  const permissions = [
    "--allow-read=/tmp",
    "--allow-write=/tmp",
    "--allow-env",
  ];

  // Check if network is allowed
  const allowedHosts = Deno.env.get("ALLOWED_HOSTS");
  if (allowedHosts) {
    try {
      const hosts = JSON.parse(allowedHosts) as string[];
      permissions.push(`--allow-net=${hosts.join(",")}`);
    } catch {
      // Invalid hosts, deny network
    }
  } else if (Deno.env.get("NETWORK_MODE") === "allow") {
    permissions.push("--allow-net");
  }

  const command = new Deno.Command("deno", {
    args: ["run", ...permissions, wrapperPath, JSON.stringify(args)],
    cwd: workDir,
    stdout: "piped",
    stderr: "piped",
  });

  const child = command.spawn();

  // Set up timeout
  const timeoutPromise = new Promise<never>((_, reject) => {
    setTimeout(() => {
      try {
        child.kill("SIGKILL");
      } catch {
        // Process may already be dead
      }
      reject(new Error(`Function execution timeout after ${timeoutMs}ms`));
    }, timeoutMs);
  });

  // Wait for completion or timeout
  const outputPromise = child.output();

  const { code, stdout, stderr } = await Promise.race([
    outputPromise,
    timeoutPromise,
  ]);

  const stdoutText = new TextDecoder().decode(stdout);
  const stderrText = new TextDecoder().decode(stderr);

  if (code !== 0) {
    throw new Error(stderrText || `Function exited with code ${code}`);
  }

  // Extract result from output
  let resultValue: unknown = null;
  for (const line of stdoutText.split("\n")) {
    if (line.includes("__result__")) {
      try {
        const parsed = JSON.parse(line);
        resultValue = parsed.__result__;
      } catch {
        // Not JSON
      }
    }
  }

  return { value: resultValue, stdout: stdoutText, stderr: stderrText };
}

function outputError(errorType: string, message: string): void {
  console.log(JSON.stringify({
    success: false,
    error: message,
    error_type: errorType,
  }));
}

function truncate(str: string, maxLen: number): string {
  if (str.length <= maxLen) return str;
  return str.slice(0, maxLen) + "... (truncated)";
}

await main();
