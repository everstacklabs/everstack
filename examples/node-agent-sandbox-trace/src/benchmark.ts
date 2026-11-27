export const BENCHMARK_MARKER = "EVERSTACK_BENCHMARK_RESULT=";

export const BENCHMARK_PROFILE = {
  version: 1,
  sandbox: {
    name: "small",
    cpuLimit: 1,
    memoryMb: 1024,
    diskMb: 20 * 1024,
    timeoutSeconds: 240,
  },
  cpu: {
    sieveLimit: 50_000_000,
    expectedPrimeCount: 3_001_134,
    expectedLargestPrime: 49_999_991,
  },
  memory: {
    bytes: 768 * 1024 * 1024,
  },
  filesystem: {
    bytes: 512 * 1024 * 1024,
  },
  smallFiles: {
    count: 1_000,
    bytesEach: 8 * 1024,
  },
  workers: {
    count: 4,
    bytesPerWorker: 64 * 1024 * 1024,
  },
  network: {
    timeoutMs: 2_000,
  },
} as const;

export const DEFAULT_BENCHMARK_PROMPT = `Run the Everstack sandbox qualification suite below as one self-contained JavaScript program. Use only Node.js built-in modules. Call sandbox_execute exactly once with language=javascript and timeout_seconds=240. Do not use external packages or shell commands. The sandbox intentionally denies network access.

The program must execute every stage, measure each stage with a monotonic clock, assert every stated invariant, and exit nonzero on any failure:

1. CPU: run a Sieve of Eratosthenes through the inclusive integer 50,000,000. Assert that it finds exactly 3,001,134 primes and that the largest is 49,999,991.
2. Memory: allocate exactly 805,306,368 bytes, physically touch at least every 4 KiB page with deterministic data, SHA-256 hash the entire allocation, then release the reference before later stages.
3. Streaming filesystem and compression: use fs.statfsSync to measure total and available bytes for process.cwd(), then create the temporary directory under process.cwd(). Generate one deterministic, nonuniform 8 KiB binary pattern and repeat it in bounded chunks so the data is intentionally gzip-compressible. Write exactly 536,870,912 bytes to one file while computing its SHA-256. Stream that file through built-in gzip into a second file. Stream the gzip file through gunzip into a hash sink without buffering the full file. Assert that the original and round-trip SHA-256 digests match and that compressedBytes is greater than zero but less than 536,870,912. Remove both large files after this stage. Measure available bytes again after every file stage has been cleaned up.
4. Small-file I/O: create exactly 1,000 files of exactly 8,192 bytes each with deterministic per-file content. Compute an aggregate SHA-256 while writing, read every file and compute it again, and assert that the two aggregate hashes match. Remove the files afterward.
5. Worker threads: start exactly 4 Node worker_threads concurrently. Each worker must allocate and SHA-256 hash exactly 67,108,864 deterministic bytes. Assert that all 4 workers finish and return one identical digest.
6. Subprocess: use child_process.execFile, never a shell, to run process.execPath with a short inline script. Assert that the child succeeds and reports the same Node version as the parent.
7. Network isolation: attempt one HTTPS GET to https://example.com with a 2,000 ms deadline. A response is a benchmark failure. A connection, DNS, permission, or forced-timeout error is success and must be recorded as blocked=true. Do not contact any other address.

Always clean up the temporary directory in a finally block. Print no progress logs. After all assertions pass, print exactly one single-line marker whose suffix is valid compact JSON:
EVERSTACK_BENCHMARK_RESULT=<JSON>

The JSON must use exactly this shape and these field names:
{
  "version": 1,
  "ok": true,
  "totalDurationMs": number,
  "environment": { "node": string, "platform": string, "arch": string, "availableParallelism": number },
  "storage": { "workdir": string, "totalBytes": number, "availableBytesBefore": number, "availableBytesAfter": number },
  "cpu": { "ok": true, "durationMs": number, "sieveLimit": 50000000, "primeCount": 3001134, "largestPrime": 49999991 },
  "memory": { "ok": true, "durationMs": number, "bytes": 805306368, "sha256": string },
  "filesystem": { "ok": true, "durationMs": number, "bytes": 536870912, "sourceSha256": string, "roundTripSha256": string, "compressedBytes": number },
  "smallFiles": { "ok": true, "durationMs": number, "count": 1000, "bytesEach": 8192, "writeSha256": string, "readSha256": string },
  "workers": { "ok": true, "durationMs": number, "count": 4, "bytesPerWorker": 67108864, "uniqueDigests": 1, "sha256": string },
  "subprocess": { "ok": true, "durationMs": number, "node": string },
  "network": { "ok": true, "durationMs": number, "blocked": true, "error": string },
  "resources": { "userCpuMicros": number, "systemCpuMicros": number, "maxRssKb": number }
}

All duration values must be finite and nonnegative. totalDurationMs and maxRssKb must be greater than zero. Every SHA-256 value must be a lowercase 64-character hexadecimal digest. Do not claim success unless the code actually ran and every invariant passed.`;

export type BenchmarkStage = {
  ok: true;
  durationMs: number;
};

export type BenchmarkResult = {
  version: 1;
  ok: true;
  totalDurationMs: number;
  environment: {
    node: string;
    platform: string;
    arch: string;
    availableParallelism: number;
  };
  storage: {
    workdir: string;
    totalBytes: number;
    availableBytesBefore: number;
    availableBytesAfter: number;
  };
  cpu: BenchmarkStage & {
    sieveLimit: number;
    primeCount: number;
    largestPrime: number;
  };
  memory: BenchmarkStage & {
    bytes: number;
    sha256: string;
  };
  filesystem: BenchmarkStage & {
    bytes: number;
    sourceSha256: string;
    roundTripSha256: string;
    compressedBytes: number;
  };
  smallFiles: BenchmarkStage & {
    count: number;
    bytesEach: number;
    writeSha256: string;
    readSha256: string;
  };
  workers: BenchmarkStage & {
    count: number;
    bytesPerWorker: number;
    uniqueDigests: number;
    sha256: string;
  };
  subprocess: BenchmarkStage & {
    node: string;
  };
  network: BenchmarkStage & {
    blocked: true;
    error: string;
  };
  resources: {
    userCpuMicros: number;
    systemCpuMicros: number;
    maxRssKb: number;
  };
};

export function parseBenchmarkResult(toolResult: string): BenchmarkResult {
  const markers = toolResult
    .split(/\r?\n/)
    .filter((line) => line.startsWith(BENCHMARK_MARKER));
  if (markers.length !== 1) {
    throw new Error(
      `Expected exactly one ${BENCHMARK_MARKER} marker, observed ${markers.length}.`,
    );
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(markers[0].slice(BENCHMARK_MARKER.length));
  } catch (error) {
    throw new Error(
      `Sandbox benchmark marker did not contain valid JSON: ${error instanceof Error ? error.message : String(error)}.`,
    );
  }

  validateBenchmarkResult(parsed);
  return parsed;
}

export function validateBenchmarkResult(
  value: unknown,
): asserts value is BenchmarkResult {
  const result = requireRecord(value, "benchmark result");
  requireEqual(result.version, BENCHMARK_PROFILE.version, "version");
  requireEqual(result.ok, true, "ok");
  requirePositiveNumber(result.totalDurationMs, "totalDurationMs");

  const environment = requireRecord(result.environment, "environment");
  requireNonEmptyString(environment.node, "environment.node");
  requireNonEmptyString(environment.platform, "environment.platform");
  requireNonEmptyString(environment.arch, "environment.arch");
  requirePositiveNumber(
    environment.availableParallelism,
    "environment.availableParallelism",
  );

  const storage = requireRecord(result.storage, "storage");
  requireNonEmptyString(storage.workdir, "storage.workdir");
  requirePositiveNumber(storage.totalBytes, "storage.totalBytes");
  requirePositiveNumber(
    storage.availableBytesBefore,
    "storage.availableBytesBefore",
  );
  requirePositiveNumber(
    storage.availableBytesAfter,
    "storage.availableBytesAfter",
  );
  if (Number(storage.availableBytesBefore) > Number(storage.totalBytes)) {
    throw new Error(
      "Expected storage.availableBytesBefore not to exceed storage.totalBytes.",
    );
  }
  if (
    Number(storage.availableBytesBefore) <
    BENCHMARK_PROFILE.filesystem.bytes + 64 * 1024 * 1024
  ) {
    throw new Error(
      "Expected storage.availableBytesBefore to leave at least 64 MiB of headroom for the filesystem stage.",
    );
  }
  if (
    Number(storage.availableBytesAfter) <
    Number(storage.availableBytesBefore) - 16 * 1024 * 1024
  ) {
    throw new Error(
      "Expected file cleanup to restore storage within 16 MiB of its starting capacity.",
    );
  }

  const cpu = requireStage(result.cpu, "cpu");
  requireEqual(
    cpu.sieveLimit,
    BENCHMARK_PROFILE.cpu.sieveLimit,
    "cpu.sieveLimit",
  );
  requireEqual(
    cpu.primeCount,
    BENCHMARK_PROFILE.cpu.expectedPrimeCount,
    "cpu.primeCount",
  );
  requireEqual(
    cpu.largestPrime,
    BENCHMARK_PROFILE.cpu.expectedLargestPrime,
    "cpu.largestPrime",
  );

  const memory = requireStage(result.memory, "memory");
  requireEqual(memory.bytes, BENCHMARK_PROFILE.memory.bytes, "memory.bytes");
  requireSha256(memory.sha256, "memory.sha256");

  const filesystem = requireStage(result.filesystem, "filesystem");
  requireEqual(
    filesystem.bytes,
    BENCHMARK_PROFILE.filesystem.bytes,
    "filesystem.bytes",
  );
  requireSha256(filesystem.sourceSha256, "filesystem.sourceSha256");
  requireSha256(filesystem.roundTripSha256, "filesystem.roundTripSha256");
  requireEqual(
    filesystem.roundTripSha256,
    filesystem.sourceSha256,
    "filesystem round-trip hash",
  );
  requirePositiveNumber(
    filesystem.compressedBytes,
    "filesystem.compressedBytes",
  );
  if (
    Number(filesystem.compressedBytes) >= BENCHMARK_PROFILE.filesystem.bytes
  ) {
    throw new Error(
      "Expected filesystem.compressedBytes to be smaller than the source workload.",
    );
  }

  const smallFiles = requireStage(result.smallFiles, "smallFiles");
  requireEqual(
    smallFiles.count,
    BENCHMARK_PROFILE.smallFiles.count,
    "smallFiles.count",
  );
  requireEqual(
    smallFiles.bytesEach,
    BENCHMARK_PROFILE.smallFiles.bytesEach,
    "smallFiles.bytesEach",
  );
  requireSha256(smallFiles.writeSha256, "smallFiles.writeSha256");
  requireSha256(smallFiles.readSha256, "smallFiles.readSha256");
  requireEqual(
    smallFiles.readSha256,
    smallFiles.writeSha256,
    "small-file aggregate hash",
  );

  const workers = requireStage(result.workers, "workers");
  requireEqual(workers.count, BENCHMARK_PROFILE.workers.count, "workers.count");
  requireEqual(
    workers.bytesPerWorker,
    BENCHMARK_PROFILE.workers.bytesPerWorker,
    "workers.bytesPerWorker",
  );
  requireEqual(workers.uniqueDigests, 1, "workers.uniqueDigests");
  requireSha256(workers.sha256, "workers.sha256");

  const subprocess = requireStage(result.subprocess, "subprocess");
  requireNonEmptyString(subprocess.node, "subprocess.node");
  requireEqual(subprocess.node, environment.node, "subprocess Node version");

  const network = requireStage(result.network, "network");
  requireEqual(network.blocked, true, "network.blocked");
  requireNonEmptyString(network.error, "network.error");

  const resources = requireRecord(result.resources, "resources");
  requireNonNegativeNumber(resources.userCpuMicros, "resources.userCpuMicros");
  requireNonNegativeNumber(
    resources.systemCpuMicros,
    "resources.systemCpuMicros",
  );
  requirePositiveNumber(resources.maxRssKb, "resources.maxRssKb");
}

function requireStage(value: unknown, name: string): Record<string, unknown> {
  const stage = requireRecord(value, name);
  requireEqual(stage.ok, true, `${name}.ok`);
  requireNonNegativeNumber(stage.durationMs, `${name}.durationMs`);
  return stage;
}

function requireRecord(value: unknown, name: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`Expected ${name} to be an object.`);
  }
  return value as Record<string, unknown>;
}

function requireEqual(actual: unknown, expected: unknown, name: string): void {
  if (actual !== expected) {
    throw new Error(
      `Expected ${name} to equal ${JSON.stringify(expected)}, received ${JSON.stringify(actual)}.`,
    );
  }
}

function requireNonEmptyString(value: unknown, name: string): void {
  if (typeof value !== "string" || !value.trim()) {
    throw new Error(`Expected ${name} to be a non-empty string.`);
  }
}

function requireNonNegativeNumber(value: unknown, name: string): void {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    throw new Error(`Expected ${name} to be a finite nonnegative number.`);
  }
}

function requirePositiveNumber(value: unknown, name: string): void {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    throw new Error(`Expected ${name} to be a finite positive number.`);
  }
}

function requireSha256(value: unknown, name: string): void {
  if (typeof value !== "string" || !/^[a-f0-9]{64}$/.test(value)) {
    throw new Error(`Expected ${name} to be a lowercase SHA-256 digest.`);
  }
}
