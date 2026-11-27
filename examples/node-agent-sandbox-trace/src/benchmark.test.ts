import assert from "node:assert/strict";
import test from "node:test";

import {
  BENCHMARK_MARKER,
  BENCHMARK_PROFILE,
  parseBenchmarkResult,
  validateBenchmarkResult,
  type BenchmarkResult,
} from "./benchmark.js";

const digest = "a".repeat(64);

test("uses the managed small sandbox profile", () => {
  assert.deepEqual(BENCHMARK_PROFILE.sandbox, {
    name: "small",
    cpuLimit: 1,
    memoryMb: 1024,
    diskMb: 20 * 1024,
    timeoutSeconds: 240,
  });
});

function validResult(): BenchmarkResult {
  return {
    version: 1,
    ok: true,
    totalDurationMs: 10_000,
    environment: {
      node: "v22.0.0",
      platform: "linux",
      arch: "x64",
      availableParallelism: 2,
    },
    storage: {
      workdir: "/workspace",
      totalBytes: 2_040_000_000,
      availableBytesBefore: 950_000_000,
      availableBytesAfter: 949_000_000,
    },
    cpu: {
      ok: true,
      durationMs: 1_000,
      sieveLimit: BENCHMARK_PROFILE.cpu.sieveLimit,
      primeCount: BENCHMARK_PROFILE.cpu.expectedPrimeCount,
      largestPrime: BENCHMARK_PROFILE.cpu.expectedLargestPrime,
    },
    memory: {
      ok: true,
      durationMs: 1_000,
      bytes: BENCHMARK_PROFILE.memory.bytes,
      sha256: digest,
    },
    filesystem: {
      ok: true,
      durationMs: 4_000,
      bytes: BENCHMARK_PROFILE.filesystem.bytes,
      sourceSha256: digest,
      roundTripSha256: digest,
      compressedBytes: 500_000_000,
    },
    smallFiles: {
      ok: true,
      durationMs: 1_000,
      count: BENCHMARK_PROFILE.smallFiles.count,
      bytesEach: BENCHMARK_PROFILE.smallFiles.bytesEach,
      writeSha256: digest,
      readSha256: digest,
    },
    workers: {
      ok: true,
      durationMs: 1_000,
      count: BENCHMARK_PROFILE.workers.count,
      bytesPerWorker: BENCHMARK_PROFILE.workers.bytesPerWorker,
      uniqueDigests: 1,
      sha256: digest,
    },
    subprocess: {
      ok: true,
      durationMs: 100,
      node: "v22.0.0",
    },
    network: {
      ok: true,
      durationMs: 2_000,
      blocked: true,
      error: "network disabled",
    },
    resources: {
      userCpuMicros: 8_000_000,
      systemCpuMicros: 2_000_000,
      maxRssKb: 900_000,
    },
  };
}

test("parses and validates the single sandbox benchmark marker", () => {
  const result = validResult();
  const toolResult =
    `Exit code: 0\nDuration: 10000ms\n\n--- stdout ---\n` +
    `${BENCHMARK_MARKER}${JSON.stringify(result)}\n`;

  assert.deepEqual(parseBenchmarkResult(toolResult), result);
});

test("rejects duplicate markers and false correctness claims", () => {
  const marker = `${BENCHMARK_MARKER}${JSON.stringify(validResult())}`;
  assert.throws(
    () => parseBenchmarkResult(`${marker}\n${marker}`),
    /exactly one/,
  );

  const wrongPrimeCount = validResult() as BenchmarkResult & {
    cpu: { primeCount: number };
  };
  wrongPrimeCount.cpu.primeCount = 42;
  assert.throws(
    () => validateBenchmarkResult(wrongPrimeCount),
    /cpu\.primeCount/,
  );
});

test("rejects mismatched integrity hashes and an open network", () => {
  const badHash = validResult() as BenchmarkResult & {
    filesystem: { roundTripSha256: string };
  };
  badHash.filesystem.roundTripSha256 = "b".repeat(64);
  assert.throws(() => validateBenchmarkResult(badHash), /round-trip hash/);

  const openNetwork = validResult() as unknown as {
    network: { blocked: boolean };
  };
  openNetwork.network.blocked = false;
  assert.throws(() => validateBenchmarkResult(openNetwork), /network\.blocked/);
});

test("rejects insufficient disk headroom and leaked benchmark files", () => {
  const noHeadroom = validResult();
  noHeadroom.storage.availableBytesBefore =
    BENCHMARK_PROFILE.filesystem.bytes + 32 * 1024 * 1024;
  noHeadroom.storage.availableBytesAfter =
    noHeadroom.storage.availableBytesBefore;
  assert.throws(
    () => validateBenchmarkResult(noHeadroom),
    /at least 64 MiB of headroom/,
  );

  const leakedFiles = validResult();
  leakedFiles.storage.availableBytesAfter =
    leakedFiles.storage.availableBytesBefore - 32 * 1024 * 1024;
  assert.throws(
    () => validateBenchmarkResult(leakedFiles),
    /restore storage within 16 MiB/,
  );
});
