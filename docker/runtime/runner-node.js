#!/usr/bin/env node
/**
 * Node.js Function Runner for Docker containers
 * Receives execution request as first CLI argument (JSON)
 * Outputs result as JSON to stdout
 */

const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');
const { randomUUID } = require('crypto');

const WORK_DIR = '/tmp/function';
const MAX_OUTPUT_SIZE = 1024 * 1024; // 1MB

async function main() {
  const startTime = Date.now();

  // Parse request from CLI argument
  let request;
  try {
    request = JSON.parse(process.argv[2] || '{}');
  } catch (err) {
    outputError('parse_error', `Failed to parse request: ${err.message}`);
    process.exit(1);
  }

  const { code, packages, arguments: args, timeout_ms } = request;
  const execId = request.request_id || randomUUID();
  const workDir = path.join(WORK_DIR, execId);

  let stdout = '';
  let stderr = '';

  try {
    // Create work directory
    fs.mkdirSync(workDir, { recursive: true });

    // Write user code
    const codePath = path.join(workDir, 'function.mjs');
    fs.writeFileSync(codePath, code);

    // Install packages if needed
    if (packages && packages.length > 0) {
      await installPackages(workDir, packages);
    }

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

    let errorType = 'runtime';
    if (err.message.includes('timeout')) errorType = 'timeout';
    else if (err.message.includes('SyntaxError')) errorType = 'syntax';
    else if (err.message.includes('ENOMEM')) errorType = 'oom';

    console.log(JSON.stringify({
      success: false,
      error: err.message,
      error_type: errorType,
      stdout: truncate(stdout, MAX_OUTPUT_SIZE),
      stderr: truncate(stderr, MAX_OUTPUT_SIZE),
      duration_ms: durationMs,
    }));
    process.exit(0); // Exit 0 so container doesn't show as failed

  } finally {
    // Cleanup
    try {
      fs.rmSync(workDir, { recursive: true, force: true });
    } catch (e) {
      // Ignore cleanup errors
    }
  }
}

async function installPackages(workDir, packages) {
  return new Promise((resolve, reject) => {
    // Initialize package.json
    fs.writeFileSync(
      path.join(workDir, 'package.json'),
      JSON.stringify({ name: 'function', type: 'module' })
    );

    const npm = spawn('npm', ['install', '--prefix', workDir, ...packages], {
      cwd: workDir,
      timeout: 60000,
    });

    let stderr = '';
    npm.stderr.on('data', (data) => { stderr += data.toString(); });

    npm.on('close', (code) => {
      if (code !== 0) {
        reject(new Error(`npm install failed: ${stderr}`));
      } else {
        resolve();
      }
    });

    npm.on('error', reject);
  });
}

async function runFunction(codePath, args, timeoutMs, workDir) {
  // Create wrapper that imports and executes the function
  const wrapperCode = `
    const fn = await import('${codePath}');
    const handler = fn.default || fn;
    const args = JSON.parse(process.argv[2]);

    const result = typeof handler === 'function'
      ? await handler(args)
      : handler;

    console.log(JSON.stringify({ __result__: result }));
  `;

  const wrapperPath = path.join(workDir, 'wrapper.mjs');
  fs.writeFileSync(wrapperPath, wrapperCode);

  return new Promise((resolve, reject) => {
    let stdout = '';
    let stderr = '';
    let resultValue = null;
    let timedOut = false;

    const child = spawn('node', [wrapperPath, JSON.stringify(args)], {
      cwd: workDir,
      env: {
        ...process.env,
        NODE_PATH: path.join(workDir, 'node_modules'),
      },
    });

    child.stdout.on('data', (data) => {
      const str = data.toString();
      stdout += str;

      // Try to extract result
      try {
        for (const line of str.split('\n')) {
          if (line.includes('__result__')) {
            const parsed = JSON.parse(line);
            resultValue = parsed.__result__;
          }
        }
      } catch (e) {
        // Not JSON
      }
    });

    child.stderr.on('data', (data) => {
      stderr += data.toString();
    });

    const timer = setTimeout(() => {
      timedOut = true;
      child.kill('SIGKILL');
    }, timeoutMs);

    child.on('close', (code) => {
      clearTimeout(timer);

      if (timedOut) {
        reject(new Error(`Function execution timeout after ${timeoutMs}ms`));
        return;
      }

      if (code !== 0) {
        reject(new Error(stderr || `Function exited with code ${code}`));
        return;
      }

      resolve({ value: resultValue, stdout, stderr });
    });

    child.on('error', (err) => {
      clearTimeout(timer);
      reject(err);
    });
  });
}

function outputError(errorType, message) {
  console.log(JSON.stringify({
    success: false,
    error: message,
    error_type: errorType,
  }));
}

function truncate(str, maxLen) {
  if (str.length <= maxLen) return str;
  return str.slice(0, maxLen) + '... (truncated)';
}

main().catch((err) => {
  outputError('runtime', err.message);
  process.exit(1);
});
