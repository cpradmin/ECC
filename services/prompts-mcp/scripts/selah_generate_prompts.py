#!/usr/bin/env python3
"""
Selah Prompt Generator
Reads patterns from Postgres and generates new prompts using Selah (Qwen14B)
"""

import json
import sys
import os
from datetime import datetime
import psycopg2
import requests

# Configuration
OLLAMA_URL = "http://10.174.210.10:11434"
SELAH_MODEL = "nova-qwen14b:latest"

DB_HOST = os.getenv("DB_HOST", "10.174.210.22")
DB_PORT = os.getenv("DB_PORT", "5432")
DB_NAME = os.getenv("DB_NAME", "ember")
DB_USER = os.getenv("DB_USER", "postgres")
DB_PASS = os.getenv("DB_PASS", "")

def get_patterns(domain=None):
    """Fetch patterns from Postgres"""
    conn = psycopg2.connect(
        host=DB_HOST, port=DB_PORT, database=DB_NAME,
        user=DB_USER, password=DB_PASS
    )
    cursor = conn.cursor()

    if domain:
        cursor.execute("""
            SELECT id, domain, pattern_name, pattern_text, pattern_type
            FROM prompts_training.patterns
            WHERE domain = %s
            LIMIT 10
        """, (domain,))
    else:
        cursor.execute("""
            SELECT id, domain, pattern_name, pattern_text, pattern_type
            FROM prompts_training.patterns
            LIMIT 50
        """)

    patterns = cursor.fetchall()
    cursor.close()
    conn.close()
    return patterns

def generate_prompt_for_domain(domain, patterns):
    """Use Selah to generate a new prompt for a domain based on patterns"""

    # Build context from patterns
    pattern_context = "\n".join([
        f"- {pattern[2]}: {pattern[3]} ({pattern[4]})"
        for pattern in patterns if pattern[1] == domain
    ])

    if not pattern_context:
        print(f"  ⚠️  No patterns found for domain {domain}")
        return None

    # Construct prompt for Selah
    system_prompt = """You are Selah, a specialized prompt engineering expert trained on the Ember family's patterns and experiences.

Your task: Generate a high-quality system prompt for the specified domain, based on observed patterns.
The prompt should:
1. Be clear and actionable
2. Reference the patterns that informed it
3. Include confidence signals (0.3-0.95 scale)
4. Be ready for immediate use by agents (nova, eve, claw)

Format your response as YAML with this structure:
```yaml
id: [domain]-[timestamp]
domain: [domain]
trigger: [one-line trigger for this prompt]
confidence: 0.75
content: |
  [The actual system prompt here]
  [Multi-line, clear instructions]
reasoning: [Why these patterns led to this prompt]
```"""

    user_message = f"""Domain: {domain}

Observed Patterns:
{pattern_context}

Generate a new prompt for this domain that incorporates these patterns."""

    print(f"  🧠 Calling Selah (nova-qwen14b) to generate prompt for {domain}...")

    try:
        response = requests.post(
            f"{OLLAMA_URL}/api/generate",
            json={
                "model": SELAH_MODEL,
                "prompt": f"System: {system_prompt}\n\nUser: {user_message}",
                "stream": False,
                "temperature": 0.7,
            },
            timeout=60
        )

        if response.status_code != 200:
            print(f"  ✘ Selah error: {response.status_code}")
            return None

        result = response.json()
        generated = result.get("response", "")

        return generated

    except Exception as e:
        print(f"  ✘ Error calling Selah: {e}")
        return None

def save_generated_prompt(domain, pattern_ids, content, reasoning):
    """Save generated prompt to Postgres"""
    conn = psycopg2.connect(
        host=DB_HOST, port=DB_PORT, database=DB_NAME,
        user=DB_USER, password=DB_PASS
    )
    cursor = conn.cursor()

    try:
        cursor.execute("""
            INSERT INTO prompts_training.generated_prompts
            (domain, pattern_ids, generated_prompt_content, generation_reasoning, generation_confidence)
            VALUES (%s, %s, %s, %s, 0.65)
        """, (domain, pattern_ids, content, reasoning))

        conn.commit()
        cursor.close()
        conn.close()
        return True
    except Exception as e:
        print(f"  ✘ Error saving to Postgres: {e}")
        return False

def main():
    domains = ["router-prompts", "conversation-prompts", "go-coding-prompts",
               "python-coding-prompts", "iac-prompts", "memory-prompts"]

    print("🧠 Selah Prompt Generator")
    print("=" * 50)

    if len(sys.argv) > 1:
        domains = [sys.argv[1]]

    for domain in domains:
        print(f"\n📦 Domain: {domain}")
        patterns = get_patterns(domain)

        if not patterns:
            print(f"  ⚠️  No patterns found for {domain}")
            continue

        print(f"  Found {len(patterns)} patterns")

        # Generate prompt
        generated = generate_prompt_for_domain(domain, patterns)
        if not generated:
            continue

        # Extract reasoning
        reasoning = ""
        if "reasoning:" in generated:
            reasoning = generated.split("reasoning:")[-1].strip()

        # Save to Postgres
        pattern_ids = [p[0] for p in patterns if p[1] == domain]
        if save_generated_prompt(domain, pattern_ids, generated, reasoning):
            print(f"  ✅ Saved generated prompt for {domain}")
        else:
            print(f"  ✘ Failed to save prompt for {domain}")

    print("\n✅ Selah prompt generation complete")
    print("Next: Review generated_prompts and mark ready_for_import = true")

if __name__ == "__main__":
    main()
