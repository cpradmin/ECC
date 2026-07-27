#!/usr/bin/env python3
"""
Selah Local Prompt Generator
Extracts patterns, feeds to Selah, generates prompts locally (no Postgres)
Outputs ready-to-import prompts in YAML format
"""

import json
import sys
import os
import re
from datetime import datetime
from pathlib import Path
import subprocess

# Configuration
OLLAMA_URL = "http://10.174.210.10:11434"
SELAH_MODEL = "nova-qwen14b:latest"
MEMORY_DIR = Path.home() / ".claude/projects/-home-kntrnjb/memory"
OUTPUT_DIR = Path.home() / "Projects/prompts-mcp/generated-prompts"

def run_extractor():
    """Run Go extractor to get patterns"""
    print("🔍 Running pattern extractor...")
    try:
        result = subprocess.run(
            ["./memory-trainer", "--action", "extract"],
            cwd=Path.home() / "Projects/prompts-mcp",
            capture_output=True,
            text=True,
            timeout=30
        )
        print(result.stdout)
        return True
    except Exception as e:
        print(f"  ✘ Extractor failed: {e}")
        return False

def extract_patterns_json():
    """Extract patterns and convert to JSON for Selah"""
    import subprocess

    # This is a simplified version - in production would query DB
    # For now, we'll extract the patterns directly from memory files

    patterns_by_domain = {
        "router-prompts": [],
        "conversation-prompts": [],
        "go-coding-prompts": [],
        "python-coding-prompts": [],
        "iac-prompts": [],
        "memory-prompts": []
    }

    # Map detected domains to prompt domains
    domain_map = {
        "prompts": "router-prompts",
        "knowledge": "memory-prompts",
        "infrastructure": "iac-prompts",
        "ops": "go-coding-prompts",
        "general": "conversation-prompts"
    }

    # Read memory files and extract patterns
    for md_file in sorted(MEMORY_DIR.glob("*.md")):
        try:
            content = md_file.read_text()

            # Extract from key sections
            for section in ["## Accomplished", "## Discoveries", "## Next Steps"]:
                if section in content:
                    start = content.index(section)
                    end = content.find("##", start + 1)
                    if end == -1:
                        end = len(content)

                    section_text = content[start:end]

                    # Extract bullet points
                    for line in section_text.split("\n"):
                        if line.strip().startswith(("-", "✅", "🔲", "📋")):
                            pattern = line.strip().lstrip("-✅🔲📋").strip()
                            if pattern and len(pattern) > 10:
                                # Infer domain
                                domain = "general"
                                if "prompts" in md_file.name.lower() or "swarm" in pattern.lower():
                                    domain = "router-prompts"
                                elif "iac" in pattern.lower() or "terraform" in pattern.lower():
                                    domain = "iac-prompts"
                                elif "python" in pattern.lower():
                                    domain = "python-coding-prompts"
                                elif "go" in pattern.lower() or "golang" in pattern.lower():
                                    domain = "go-coding-prompts"
                                elif "memory" in pattern.lower() or "trinity" in pattern.lower():
                                    domain = "memory-prompts"

                                if pattern not in patterns_by_domain[domain]:
                                    patterns_by_domain[domain].append(pattern)
        except Exception as e:
            print(f"  ⚠️  Error reading {md_file}: {e}")

    return patterns_by_domain

def generate_prompts_with_selah(patterns_by_domain):
    """Call Selah to generate new prompts"""
    import requests

    generated = {}

    for domain, patterns in patterns_by_domain.items():
        if not patterns:
            print(f"  ⚠️  No patterns for {domain}")
            continue

        print(f"\n🧠 Generating prompt for {domain}...")
        print(f"   Using {len(patterns)} patterns as context")

        # Build context
        pattern_context = "\n".join([f"- {p}" for p in patterns[:10]])

        # System prompt for Selah
        system_msg = f"""You are Selah, an expert prompt engineer trained on the Ember team's patterns.

Generate a SINGLE production-ready system prompt for the '{domain}' domain.

Your output MUST be valid YAML with this exact structure:
---
id: {domain}-{datetime.now().strftime('%Y%m%d%H%M%S')}
domain: {domain}
trigger: "one line describing when this prompt is used"
confidence: 0.75
content: |
  [Your prompt content here - clear, actionable instructions]
  [Multi-line system prompt for an AI agent]
source: "extracted from {len(patterns)} observed patterns"
---

Do NOT add markdown code fences. Start with --- directly."""

        user_msg = f"""Based on these observed patterns:
{pattern_context}

Generate a prompt for the {domain} domain that incorporates the key insights."""

        try:
            response = requests.post(
                f"{OLLAMA_URL}/api/generate",
                json={
                    "model": SELAH_MODEL,
                    "prompt": f"{system_msg}\n\n{user_msg}",
                    "stream": False,
                    "temperature": 0.7,
                },
                timeout=120
            )

            if response.status_code == 200:
                result = response.json()
                prompt_content = result.get("response", "").strip()

                # Clean up the output
                if prompt_content.startswith("```"):
                    prompt_content = prompt_content.split("```")[1].strip()
                    if prompt_content.startswith("yaml"):
                        prompt_content = prompt_content[4:].strip()

                generated[domain] = {
                    "content": prompt_content,
                    "patterns_used": len(patterns),
                    "generated_at": datetime.now().isoformat()
                }
                print(f"  ✅ Generated prompt for {domain}")
            else:
                print(f"  ✘ Selah error: {response.status_code}")

        except requests.exceptions.Timeout:
            print(f"  ✘ Selah timeout (may still be generating)")
        except Exception as e:
            print(f"  ✘ Error: {e}")

    return generated

def save_prompts(generated):
    """Save generated prompts to YAML files"""
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

    saved_files = []
    for domain, data in generated.items():
        output_file = OUTPUT_DIR / f"{domain}.yaml"

        try:
            # Write directly (already YAML from Selah)
            output_file.write_text(data["content"])
            saved_files.append(output_file)
            print(f"  ✅ Saved {output_file.name}")
        except Exception as e:
            print(f"  ✘ Error saving {domain}: {e}")

    return saved_files

def main():
    print("=" * 60)
    print("🧠 Selah Local Prompt Generator")
    print("=" * 60)

    # Extract patterns
    print("\n1️⃣  Extracting patterns from memory files...")
    patterns_by_domain = extract_patterns_json()

    for domain, patterns in patterns_by_domain.items():
        print(f"  {domain}: {len(patterns)} patterns")

    # Generate with Selah
    print("\n2️⃣  Generating prompts with Selah (nova-qwen14b)...")
    generated = generate_prompts_with_selah(patterns_by_domain)

    # Save outputs
    print(f"\n3️⃣  Saving generated prompts...")
    saved = save_prompts(generated)

    print(f"\n✅ Complete!")
    print(f"   Generated {len(generated)} prompts")
    print(f"   Saved to {OUTPUT_DIR}")
    print(f"\n📋 Next: Import into prompts-mcp")
    print(f"   POST http://localhost:8080/mcp/prompts/import")
    print(f"   With files from {OUTPUT_DIR}")

if __name__ == "__main__":
    main()
