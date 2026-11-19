# AI Agent Operational Workflow

This document defines the mandatory workflow for the AI assistant when performing tasks.

## 1. Task Acquisition & Clarification

### 1.1 Task Sources
- **Console**: Direct instructions provided in the terminal (e.g., `"add a timestamp to the telegram message"`).
- **GitHub Issues**: Tasks referenced by issue number (e.g., `"Issue #123"`).

### 1.2 Clarification Protocol
**Never guess.** If any part of a task is ambiguous or lacks necessary information, you must ask for clarification before proceeding.
- **Console tasks**: Ask questions directly in the console.
- **GitHub issues**: Post a comment on the corresponding GitHub issue.

## 2. Development Workflow

### 2.1 Branching Strategy
Every task must be performed in a new, dedicated branch. All new branches must be created from the `main` branch.
- **Naming Convention**:
  - **For GitHub issues**: `issue-<number>-<short-description>` (e.g., `issue-123-add-timestamp`).
  - **For console tasks**: `task-<short-description>` (e.g., `task-fix-tests`).

### 2.2 Git Identity
All commits must be made using the following local configuration:
- **User Name**: `ai-dev-assistant-bot[bot]`
- **User Email**: `5744773+ai-dev-assistant-bot[bot]@users.noreply.github.com`

### 2.3 Commit Message Standard
Follow the **Conventional Commits** specification:
- `feat: <description>` — A new feature.
- `fix: <description>` — A bug fix.
- `test: <description>` — Adding or correcting tests.
- `chore: <description>` — Maintenance tasks (e.g., updating dependencies).
- `docs: <description>` — Documentation changes.
- `refactor: <description>` — Code changes that neither fix a bug nor add a feature.

### 2.4 Execution Rules
- **Strict Adherence**: Do not perform actions that are not explicitly requested. 
  - *Example*: If asked to "check if tests work", report the status (Pass/Fail/Errors) but **do not** attempt to fix them unless explicitly told to "fix tests".
- **Tests**:
  - **Ignore** tests unless they are explicitly mentioned in the task.
  - **Do not run or check tests** until the primary task is fully completed.
  - **Test Fixes**: If tests need to be fixed (after the main task is done/approved), perform the fixes in the **same branch** but as a **separate commit** (using the `test:` prefix).

## 3. Completion & Submission

### 3.1 How to Push Changes via GitHub App
 Since the GitHub App uses short-lived Installation Access Tokens, standard git push or updating the remote URL with a permanent credential will
 fail. Follow these two steps:

#### 3.1.1. Generate a fresh Access Token
Run your token script to retrieve the current valid installation token:
```bash
  TOKEN=$(python3 .dev/tmp/get_github_token.py)
```

#### 3.1.2. Push using the x-access-token format
Use the token directly in the push command. This bypasses the local credential manager and avoids "device or address" errors:
```bash
  git push https://x-access-token:$TOKEN@github.com/<owner>/<repo>.git <branch_name>
```

#### 3.1.3 What to Avoid (Common Pitfalls)
- Do NOT use standard git push: If your origin is configured with an old or expired token, it will fail.
- Do NOT use git remote set-url with a JWT: A JWT (JSON Web Token) is only for authenticating API requests. It cannot be used for Git transport
  (push/pull). You must use the Installation Access Token obtained from the API.
- Do NOT use username/password: The GitHub App authentication requires the specific x-access-token prefix in the URL.

### 3.2 Create a Pull Request (via API)
To create a PR using the same $TOKEN, use curl:
```bash
  curl -X POST \
    -H "Authorization: Bearer $TOKEN" \
    -H "Accept: application/vnd.github+json" \
    https://api.github.com/repos/<owner>/<repo>/pulls \
    -d '{"title":"PR Title","head":"<head_branch>","base":"<base_branch>","body":"Description"}'
```

#### 3.2.1 Pull Requests Rules
- **Never** commit directly to the main branch.
- **Never** merge branches yourself.
- **Always** submit the final result as a Pull Request (PR) on GitHub.
- If the task is based on a GitHub issue, include a link to the issue in the PR body (e.g., 'Closes #123').
- After the PR is merged, the local task branch can be deleted.
