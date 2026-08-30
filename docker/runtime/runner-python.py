#!/usr/bin/env python3
"""
Python Function Runner for Docker containers.
Receives execution request as first CLI argument (JSON).
Outputs result as JSON to stdout.
"""

import json
import os
import subprocess
import sys
import tempfile
import time
import traceback
import uuid

WORK_DIR = "/tmp/function"
MAX_OUTPUT_SIZE = 1024 * 1024  # 1MB


def main():
    start_time = time.time()

    # Parse request from CLI argument
    try:
        request = json.loads(sys.argv[1] if len(sys.argv) > 1 else "{}")
    except (json.JSONDecodeError, IndexError) as e:
        output_error("parse_error", f"Failed to parse request: {e}")
        sys.exit(1)

    code = request.get("code", "")
    packages = request.get("packages", [])
    arguments = request.get("arguments", {})
    timeout_ms = request.get("timeout_ms", 30000)
    exec_id = request.get("request_id", str(uuid.uuid4()))
    work_dir = os.path.join(WORK_DIR, exec_id)

    stdout_output = ""
    stderr_output = ""

    try:
        # Create work directory
        os.makedirs(work_dir, exist_ok=True)

        # Install packages if needed
        if packages:
            install_packages(work_dir, packages)

        # Write user code
        code_path = os.path.join(work_dir, "function.py")
        with open(code_path, "w") as f:
            f.write(code)

        # Execute the function
        result = run_function(code_path, arguments, timeout_ms, work_dir)
        stdout_output = result["stdout"]
        stderr_output = result["stderr"]

        duration_ms = int((time.time() - start_time) * 1000)

        print(json.dumps({
            "success": True,
            "result": result["value"],
            "stdout": truncate(stdout_output, MAX_OUTPUT_SIZE),
            "stderr": truncate(stderr_output, MAX_OUTPUT_SIZE),
            "duration_ms": duration_ms,
        }))

    except Exception as e:
        duration_ms = int((time.time() - start_time) * 1000)

        error_msg = str(e)
        error_type = "runtime"
        if "timeout" in error_msg.lower():
            error_type = "timeout"
        elif "SyntaxError" in error_msg:
            error_type = "syntax"
        elif "MemoryError" in error_msg:
            error_type = "oom"

        print(json.dumps({
            "success": False,
            "error": error_msg,
            "error_type": error_type,
            "stdout": truncate(stdout_output, MAX_OUTPUT_SIZE),
            "stderr": truncate(stderr_output, MAX_OUTPUT_SIZE),
            "duration_ms": duration_ms,
        }))
        sys.exit(0)  # Exit 0 so container doesn't show as failed

    finally:
        # Cleanup
        try:
            import shutil
            shutil.rmtree(work_dir, ignore_errors=True)
        except Exception:
            pass


def install_packages(work_dir, packages):
    """Install Python packages using pip."""
    result = subprocess.run(
        [sys.executable, "-m", "pip", "install", "--target", work_dir] + packages,
        capture_output=True,
        text=True,
        timeout=60,
    )
    if result.returncode != 0:
        raise RuntimeError(f"pip install failed: {result.stderr}")


def run_function(code_path, arguments, timeout_ms, work_dir):
    """Execute the user function in a subprocess."""
    # Create a wrapper script that imports and runs the function
    wrapper_code = f"""
import sys
import json
import importlib.util

# Add work dir to path for installed packages
sys.path.insert(0, {work_dir!r})

# Load the user module
spec = importlib.util.spec_from_file_location("function", {code_path!r})
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

# Find the handler
handler = getattr(mod, "handler", None) or getattr(mod, "main", None) or getattr(mod, "default", None)

# Parse arguments
args = json.loads(sys.argv[1])

# Execute
if callable(handler):
    import asyncio
    if asyncio.iscoroutinefunction(handler):
        result = asyncio.run(handler(args))
    else:
        result = handler(args)
else:
    result = None

print(json.dumps({{"__result__": result}}, default=str))
"""

    wrapper_path = os.path.join(work_dir, "wrapper.py")
    with open(wrapper_path, "w") as f:
        f.write(wrapper_code)

    timeout_sec = timeout_ms / 1000
    try:
        proc = subprocess.run(
            [sys.executable, wrapper_path, json.dumps(arguments)],
            capture_output=True,
            text=True,
            timeout=timeout_sec,
            cwd=work_dir,
            env={**os.environ, "PYTHONPATH": work_dir},
        )
    except subprocess.TimeoutExpired:
        raise RuntimeError(f"Function execution timeout after {timeout_ms}ms")

    stdout_text = proc.stdout
    stderr_text = proc.stderr

    if proc.returncode != 0:
        raise RuntimeError(stderr_text or f"Function exited with code {proc.returncode}")

    # Extract result from output
    result_value = None
    for line in reversed(stdout_text.splitlines()):
        line = line.strip()
        if not line:
            continue
        if "__result__" in line:
            try:
                parsed = json.loads(line)
                result_value = parsed.get("__result__")
                break
            except json.JSONDecodeError:
                pass

    return {"value": result_value, "stdout": stdout_text, "stderr": stderr_text}


def output_error(error_type, message):
    """Output an error result as JSON."""
    print(json.dumps({
        "success": False,
        "error": message,
        "error_type": error_type,
    }))


def truncate(s, max_len):
    """Truncate string to max length."""
    if len(s) <= max_len:
        return s
    return s[:max_len] + "... (truncated)"


if __name__ == "__main__":
    main()
