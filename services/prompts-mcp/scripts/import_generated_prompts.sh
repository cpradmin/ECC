#!/bin/bash
# Import generated prompts from Selah into prompts-mcp
# Usage: ./import_generated_prompts.sh [prompts-mcp-url]

PROMPTS_MCP_URL="${1:-http://localhost:8080}"
GENERATED_DIR="${HOME}/Projects/prompts-mcp/generated-prompts"
PROMPTS_MCP_ADDR="${PROMPTS_MCP_ADDR:-localhost:8080}"

echo "📥 Importing generated prompts into prompts-mcp"
echo "   URL: ${PROMPTS_MCP_URL}/mcp/prompts/import"
echo "   Source: ${GENERATED_DIR}"
echo ""

if [ ! -d "$GENERATED_DIR" ]; then
    echo "✘ Generated prompts directory not found: $GENERATED_DIR"
    exit 1
fi

# Count files
PROMPT_COUNT=$(find "$GENERATED_DIR" -name "*.yaml" | wc -l)
if [ "$PROMPT_COUNT" -eq 0 ]; then
    echo "✘ No YAML files found in $GENERATED_DIR"
    exit 1
fi

echo "Found $PROMPT_COUNT prompt files"
echo ""

# Build import request JSON
# Read all YAML files and convert to JSON
IMPORT_JSON=$(cat <<'EOF'
{
  "prompts": [
EOF
)

FIRST=true
for yaml_file in "$GENERATED_DIR"/*.yaml; do
    if [ -f "$yaml_file" ]; then
        # Use yq to convert YAML to JSON (if available)
        if command -v yq &> /dev/null; then
            prompt_json=$(yq eval -o=json "$yaml_file")
        else
            # Fallback: simple grep parsing (not ideal, but works for basic YAML)
            id=$(grep "^id:" "$yaml_file" | cut -d' ' -f2)
            domain=$(grep "^domain:" "$yaml_file" | cut -d' ' -f2)
            trigger=$(grep "^trigger:" "$yaml_file" | sed 's/trigger: "//' | sed 's/"$//')
            confidence=$(grep "^confidence:" "$yaml_file" | cut -d' ' -f2)

            prompt_json="{\"id\":\"$id\",\"domain\":\"$domain\",\"trigger\":\"$trigger\",\"confidence\":$confidence}"
        fi

        if [ "$FIRST" = true ]; then
            IMPORT_JSON="${IMPORT_JSON}
    $prompt_json"
            FIRST=false
        else
            IMPORT_JSON="${IMPORT_JSON},
    $prompt_json"
        fi
    fi
done

IMPORT_JSON="${IMPORT_JSON}
  ],
  \"source\": \"selah-generated\",
  \"feedback\": []
}"

echo "💾 Sending import request..."
echo ""

# POST to prompts-mcp
RESPONSE=$(curl -s -X POST \
    -H "Content-Type: application/json" \
    -d "$IMPORT_JSON" \
    "http://${PROMPTS_MCP_ADDR}/mcp/prompts/import")

echo "Response:"
echo "$RESPONSE" | jq . 2>/dev/null || echo "$RESPONSE"
echo ""

# Parse response
if echo "$RESPONSE" | grep -q '"status".*"ok"'; then
    echo "✅ Import successful!"
    echo ""
    echo "Next: Monitor confidence scores as agents test the prompts"
    echo "See: GET http://${PROMPTS_MCP_ADDR}/mcp/prompts/list?domain=<domain>&scope=project"
else
    echo "⚠️  Import returned non-OK status"
fi
