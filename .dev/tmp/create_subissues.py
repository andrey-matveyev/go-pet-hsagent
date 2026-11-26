#!/usr/bin/env python3
"""
Example script to create GitHub Issues via GitHub App API.
Demonstrates: authentication, creating an issue, creating a sub-task, linking as sub-issue.
Usage: python3 .dev/tmp/create_subissues.py
"""

import time
import jwt
import requests
import json

# ---- CONFIGURATION ----
APP_ID_PATH = "/home/andrey/projects/ai-assistants/ai-dev-assistant-bot-app-id"
PRIVATE_KEY_PATH = "/home/andrey/projects/ai-assistants/ai-dev-assistant-bot.2026-11-13.private-key.pem"
OWNER = "andrey-matveyev"
REPO = "go-pet-hsagent"
# -----------------------

def get_app_id():
    with open(APP_ID_PATH, 'r') as f:
        content = f.read().strip()
        import re
        match = re.search(r'\d+', content)
        return match.group() if match else content

def generate_jwt():
    with open(PRIVATE_KEY_PATH, 'r') as f:
        private_key = f.read()
    
    payload = {
        "iat": int(time.time()) - 60,
        "exp": int(time.time()) + (10 * 60),
        "iss": get_app_id(),
    }
    return jwt.encode(payload, private_key, algorithm="RS256")

def get_installation_id(jwt_token):
    url = f"https://api.github.com/repos/{OWNER}/{REPO}/installation"
    headers = {"Authorization": f"Bearer {jwt_token}", "Accept": "application/vnd.github+json"}
    return requests.get(url, headers=headers).json()["id"]

def get_access_token(jwt_token, installation_id):
    url = f"https://api.github.com/app/installations/{installation_id}/access_tokens"
    headers = {"Authorization": f"Bearer {jwt_token}", "Accept": "application/vnd.github+json"}
    return requests.post(url, headers=headers).json()["token"]

def create_issue(token, title, body, labels=None):
    """Create a single issue and return its number."""
    url = f"https://api.github.com/repos/{OWNER}/{REPO}/issues"
    headers = {"Authorization": f"Bearer {token}", "Accept": "application/vnd.github+json"}
    data = {"title": title, "body": body}
    if labels:
        data["labels"] = labels
    resp = requests.post(url, headers=headers, json=data)
    resp.raise_for_status()
    return resp.json()["number"]

def add_subissue_rest(token, parent_number, sub_number):
    """Link existing issue as sub-issue via REST API (one-by-one)."""
    url = f"https://api.github.com/repos/{OWNER}/{REPO}/issues/{parent_number}/sub_issues"
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28"
    }
    resp = requests.post(url, headers=headers, json={"sub_issue_id": sub_number})
    return resp.status_code, resp.text

def get_node_id(token, issue_number):
    """Get GraphQL node_id for an issue."""
    url = f"https://api.github.com/repos/{OWNER}/{REPO}/issues/{issue_number}"
    headers = {"Authorization": f"Bearer {token}", "Accept": "application/vnd.github+json"}
    return requests.get(url, headers=headers).json()["node_id"]

def add_subissues_graphql(token, parent_number, sub_numbers):
    """Link multiple sub-issues via GraphQL (batch)."""
    url = "https://api.github.com/graphql"
    headers = {"Authorization": f"Bearer {token}", "Accept": "application/vnd.github+json"}
    
    # Resolve node IDs
    parent_id = get_node_id(token, parent_number)
    sub_ids = [get_node_id(token, n) for n in sub_numbers]
    
    mutation = """
    mutation($parentId: ID!, $subIssueIds: [ID!]!) {
        addSubIssues(input: {parentId: $parentId, subIssueIds: $subIssueIds}) {
            parentIssue { id number }
            subIssues { id number }
        }
    }
    """
    variables = {"parentId": parent_id, "subIssueIds": sub_ids}
    resp = requests.post(url, headers=headers, json={"query": mutation, "variables": variables})
    return resp.json()


# ============================================================
# EXAMPLE: Create one Epic + one Sub-task
# ============================================================

EXAMPLE_EPIC = {
    "title": "[Example] Epic: Fix Critical Bugs",
    "body": """## Example Epic

This is a demo epic issue created via API.

### Sub-tasks (checklist)
- [ ] Sub-task 1: Fix the thing
- [ ] Sub-task 2: Test the fix

**Parent Epic:** #40 (reference only)
""",
    "labels": ["example", "epic"]
}

EXAMPLE_SUBTASK = {
    "title": "[Example] Sub-task: Fix the thing",
    "body": """## Problem
Something is broken.

## Solution
Fix it properly.

## Acceptance Criteria
- [ ] Tests pass
- [ ] No regressions

## Files
- `agent/main.go`

## Parent Epic
#40
""",
    "labels": ["example", "sub-task", "bug"]
}


def main():
    print("🔐 Getting GitHub App token...")
    jwt_token = generate_jwt()
    inst_id = get_installation_id(jwt_token)
    token = get_access_token(jwt_token, inst_id)
    print(f"   Token: {token[:20]}...")

    print("\n📝 Creating example Epic...")
    epic_num = create_issue(token, EXAMPLE_EPIC["title"], EXAMPLE_EPIC["body"], EXAMPLE_EPIC["labels"])
    print(f"   Created Epic #{epic_num}")

    print("\n📝 Creating example Sub-task...")
    sub_num = create_issue(token, EXAMPLE_SUBTASK["title"], EXAMPLE_SUBTASK["body"], EXAMPLE_SUBTASK["labels"])
    print(f"   Created Sub-task #{sub_num}")

    print(f"\n🔗 Linking Sub-task #{sub_num} to Epic #{epic_num} via REST...")
    status, body = add_subissue_rest(token, epic_num, sub_num)
    print(f"   Status: {status}")
    if status != 201:
        print(f"   Response: {body}")
        print("   (May need manual linking in GitHub UI — see workflow.md 3.3.3)")

    print(f"\n✅ Done! Check: https://github.com/{OWNER}/{REPO}/issues/{epic_num}")


if __name__ == "__main__":
    main()