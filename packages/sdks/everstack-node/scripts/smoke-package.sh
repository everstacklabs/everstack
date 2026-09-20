#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 OUTPUT_DIRECTORY" >&2
  exit 2
fi

package_dir=$1
consumer_dir="$package_dir/consumer"
mkdir -p "$package_dir" "$consumer_dir"

package_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$package_root"

package_tarball=$(pnpm pack --pack-destination "$package_dir" | tail -n 1)
case "$package_tarball" in
  "$package_dir"/*.tgz) ;;
  *) echo "Unexpected package path: $package_tarball" >&2; exit 1 ;;
esac

PACKAGE_TARBALL="$package_tarball" node <<'NODE'
const { execFileSync } = require("node:child_process");
const manifest = JSON.parse(execFileSync("tar", ["-xOf", process.env.PACKAGE_TARBALL, "package/package.json"]));
for (const [name, version] of Object.entries(manifest.dependencies ?? {})) {
  if (String(version).startsWith("workspace:")) {
    throw new Error(`Unresolved workspace dependency in package: ${name}@${version}`);
  }
}
NODE

cd "$consumer_dir"
npm init -y >/dev/null
npm install "$package_tarball" >&2
node --input-type=module <<'NODE'
import { Everstack, OpenAIAgents, OpenAIAgentsService } from "@everstack/node";
const client = new Everstack({ baseUrl: "http://127.0.0.1:1", accessToken: "package-smoke-test" });
if (!(client.openaiAgents instanceof OpenAIAgents)) {
  throw new Error("OpenAI Agents resource is unavailable from the packed SDK");
}
if (OpenAIAgentsService.typeName !== "everstack.openai_agents.v1.OpenAIAgentsService") {
  throw new Error("OpenAI Agents ConnectRPC service descriptor is unavailable from the packed SDK");
}
NODE

printf '%s\n' "$package_tarball"
