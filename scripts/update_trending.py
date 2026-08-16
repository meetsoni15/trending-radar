#!/usr/bin/env python3
"""Fetch trending/new GitHub repos per category and rewrite README.md between markers."""

import datetime
import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
README_PATH = REPO_ROOT / "README.md"
START_MARKER = "<!-- TRENDING:START -->"
END_MARKER = "<!-- TRENDING:END -->"
PER_CATEGORY_LIMIT = 8
LOOKBACK_DAYS = 7
MIN_STARS_OVERALL = 50

# Each category is a list of GitHub topic slugs; results across topics are merged & deduped.
CATEGORIES = {
    "🤖 AI Agents & LLM Tools": ["ai-agents", "llm-agents", "agentic-ai"],
    "🛡️ Security & Pentesting": ["pentesting", "security-tools", "ai-security"],
    "🛠️ Developer Tools & CLI": ["developer-tools", "cli"],
    "⚙️ DevOps & Infrastructure": ["devops", "infrastructure-as-code"],
}

API_ROOT = "https://api.github.com/search/repositories"


def gh_get(url: str) -> dict:
    req = urllib.request.Request(url)
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("User-Agent", "weekly-trending-radar")
    token = os.environ.get("GITHUB_TOKEN")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    for attempt in range(3):
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                return json.loads(resp.read().decode())
        except urllib.error.HTTPError as e:
            if e.code == 403 and attempt < 2:
                time.sleep(10)
                continue
            raise
    raise RuntimeError(f"Failed to fetch {url}")


def search_repos(query: str) -> list[dict]:
    url = f"{API_ROOT}?q={urllib.parse.quote(query)}&sort=stars&order=desc&per_page=15"
    data = gh_get(url)
    return data.get("items", [])


def since_date() -> str:
    return (datetime.date.today() - datetime.timedelta(days=LOOKBACK_DAYS)).isoformat()


def fetch_category(topics: list[str]) -> list[dict]:
    seen = {}
    for topic in topics:
        query = f"topic:{topic} created:>{since_date()}"
        try:
            for repo in search_repos(query):
                seen[repo["full_name"]] = repo
        except Exception as e:
            print(f"  warn: query failed for topic={topic}: {e}")
        time.sleep(2)  # stay under search rate limits
    ranked = sorted(seen.values(), key=lambda r: r["stargazers_count"], reverse=True)
    return ranked[:PER_CATEGORY_LIMIT]


def fetch_overall_new() -> list[dict]:
    query = f"created:>{since_date()} stars:>{MIN_STARS_OVERALL}"
    try:
        return search_repos(query)[:10]
    except Exception as e:
        print(f"  warn: overall query failed: {e}")
        return []


def render_table(repos: list[dict]) -> str:
    if not repos:
        return "_No qualifying repos found this week._\n"
    lines = ["| Repo | ⭐ Stars | Language | Description |", "|---|---|---|---|"]
    for r in repos:
        name = r["full_name"]
        url = r["html_url"]
        stars = r["stargazers_count"]
        lang = r.get("language") or "—"
        desc = (r.get("description") or "").replace("|", "/").strip()
        if len(desc) > 100:
            desc = desc[:97] + "..."
        lines.append(f"| [{name}]({url}) | {stars:,} | {lang} | {desc} |")
    return "\n".join(lines) + "\n"


def build_section() -> str:
    today = datetime.date.today().isoformat()
    parts = [f"_Last updated: **{today}** (UTC) · repos created in the last {LOOKBACK_DAYS} days_\n"]

    parts.append("## 🔥 New This Week (overall)\n")
    parts.append(render_table(fetch_overall_new()))

    for label, topics in CATEGORIES.items():
        print(f"Fetching category: {label}")
        parts.append(f"## {label}\n")
        parts.append(render_table(fetch_category(topics)))

    return "\n".join(parts)


def update_readme(section: str) -> None:
    text = README_PATH.read_text()
    if START_MARKER not in text or END_MARKER not in text:
        raise RuntimeError("README markers not found")
    before = text.split(START_MARKER)[0]
    after = text.split(END_MARKER)[1]
    new_text = f"{before}{START_MARKER}\n{section}\n{END_MARKER}{after}"
    README_PATH.write_text(new_text)


def main() -> None:
    section = build_section()
    update_readme(section)
    print("README.md updated.")


if __name__ == "__main__":
    main()
