#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

if [ $# -ne 1 ]; then
  echo "Usage: $0 <new-version>"
  echo "Example: $0 0.2.1"
  exit 1
fi

NEW_VERSION="$1"

if ! echo "$NEW_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "Error: version must be in semver format (e.g., 0.2.1)"
  exit 1
fi

# Read current version from Cargo.toml (source of truth)
CURRENT_VERSION=$(grep '^version' "$ROOT_DIR/apps/server/Cargo.toml" | sed 's/.*"\(.*\)"/\1/')
echo "Bumping version: ${CURRENT_VERSION} -> ${NEW_VERSION}"

# apps/server/Cargo.toml
sed -i '' "s/^version = \"${CURRENT_VERSION}\"/version = \"${NEW_VERSION}\"/" "$ROOT_DIR/apps/server/Cargo.toml"
echo "  Updated apps/server/Cargo.toml"

# apps/web/package.json
sed -i '' "s/\"version\": \"${CURRENT_VERSION}\"/\"version\": \"${NEW_VERSION}\"/" "$ROOT_DIR/apps/web/package.json"
echo "  Updated apps/web/package.json"

# Regenerate package-lock.json for apps/web
echo "  Reinstalling apps/web..."
rm -rf "$ROOT_DIR/apps/web/node_modules" "$ROOT_DIR/apps/web/package-lock.json"
(cd "$ROOT_DIR/apps/web" && npm install --silent)
echo "  Regenerated apps/web/package-lock.json"

echo "Done."
