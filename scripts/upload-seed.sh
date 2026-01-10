#!/bin/bash
# Upload seed data to the report server

set -e

API_URL="${API_URL:-http://localhost:8080/api/v1/reports}"
API_KEY="${RRV_API_KEY:-dev-api-key-do-not-use-in-production}"

# Get the project root (where this script is located)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Sample data for random generation
OWNERS=("acme-corp" "test-org" "my-company" "dev-team" "qa-automation")
REPOS=("web-app" "api-server" "e2e-tests" "frontend" "backend" "mobile-app")
BRANCHES=("main" "develop" "feature/auth" "feature/dashboard" "fix/login-bug" "release/v2.0" "hotfix/security")
AUTHORS=("john-doe" "jane-smith" "bob-wilson" "alice-johnson" "dev-bot")

# Generate random hex string (for commit SHA)
generate_hex() {
  local length=$1
  LC_ALL=C tr -dc 'a-f0-9' < /dev/urandom | head -c "$length"
}

# Generate random number in range
random_range() {
  local min=$1
  local max=$2
  echo $((RANDOM % (max - min + 1) + min))
}

# Pick random element from array
random_element() {
  local arr=("$@")
  local idx=$((RANDOM % ${#arr[@]}))
  echo "${arr[$idx]}"
}

# Generate random GitHub context JSON
generate_github_context() {
  local owner=$(random_element "${OWNERS[@]}")
  local repo=$(random_element "${REPOS[@]}")
  local branch=$(random_element "${BRANCHES[@]}")
  local commit_sha=$(generate_hex 40)
  local run_id=$(date +%s)
  local run_attempt=$(random_range 1 3)

  # PR number and author are null for main/release branches (direct merge runs)
  local pr_fields=""
  if [[ "$branch" != "main" && "$branch" != "develop" && ! "$branch" =~ ^release/ ]]; then
    local author=$(random_element "${AUTHORS[@]}")
    local pr_number=$(date +%s)
    pr_fields="\"pr_number\": $pr_number,
  \"pr_author\": \"$author\","
  fi

  cat <<EOF
{
  "repository": "$owner/$repo",
  "branch": "$branch",
  "commit_sha": "$commit_sha",
  $pr_fields
  "run_id": $run_id,
  "run_attempt": $run_attempt
}
EOF
}

# Function to upload a single directory
upload_directory() {
  local SEED_DIR="$1"
  local DIR_NAME="$(basename "$SEED_DIR")"

  if [[ ! -d "$SEED_DIR" ]]; then
    echo "Warning: Directory not found: $SEED_DIR (skipping)"
    return 1
  fi

  echo ""
  echo "========================================="
  echo "Uploading: $DIR_NAME"
  echo "========================================="

  # Generate random GitHub context
  local github_context=$(generate_github_context)
  echo "GitHub Context:"
  echo "$github_context" | head -4
  echo "  ..."

  # Build curl arguments
  local CURL_ARGS=(-X POST "$API_URL" -H "X-API-Key: $API_KEY")

  # Add github_context as form field
  CURL_ARGS+=(-F "github_context=$github_context")

  # Find all files and add them as form fields
  cd "$SEED_DIR"
  local file_count=0
  while IFS= read -r -d '' file; do
    # Remove leading ./
    filename="${file#./}"
    CURL_ARGS+=(-F "files=@$file;filename=$filename")
    ((file_count++))
  done < <(find . -type f ! -name ".DS_Store" ! -name "*.DS_Store" -print0)

  echo "Found $file_count files"

  # Execute curl
  response=$(curl -s -w "\n%{http_code}" "${CURL_ARGS[@]}")

  # Extract body and status code
  http_code=$(echo "$response" | tail -n1)
  body=$(echo "$response" | sed '$d')

  echo "Response ($http_code):"
  echo "$body" | python3 -m json.tool 2>/dev/null || echo "$body"

  cd "$PROJECT_ROOT"
  return 0
}

# If arguments provided, upload those specific directories
if [[ $# -gt 0 ]]; then
  for dir in "$@"; do
    # Resolve relative paths
    if [[ "$dir" != /* ]]; then
      dir="$PROJECT_ROOT/$dir"
    fi
    upload_directory "$dir"
  done
else
  # Default: upload both seed directories
  echo "API URL: $API_URL"
  echo "Uploading all seed data..."

  upload_directory "$PROJECT_ROOT/seed/pw-report-smoke"
  upload_directory "$PROJECT_ROOT/seed/pw-report-with-failed"
  upload_directory "$PROJECT_ROOT/seed/pw-report-with-skipped"
fi

echo ""
echo "Done!"
