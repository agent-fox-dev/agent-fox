# Product Requirements Document: Agent-Fox v2.0

## 1. Executive Summary
Agent-Fox was built to solve the "babysitting" problem of AI coding agents by orchestrating deterministic, parallel, multi-agent sessions using rigorous specifications. While the architecture (DAG planners, DuckDB knowledge stores, git worktrees, 6-archetype models) achieves high autonomy, it has grown structurally complex. The workflow is heavy, requiring a multi-stage specification generation process (`afspec`, `agentspec`, `spec`) and steep learning curves. 

**Agent-Fox v2.0** focuses on radical simplification and developer experience (DX). We will remove architectural clutter without jeopardizing code quality, creating a faster, more intuitive workflow. At the same time, we will introduce game-changing features that elevate Agent-Fox from a background daemon into an interactive, visual, and highly collaborative AI teammate.

---

## 2. Current State Analysis

### Where does the complexity live?
1. **Spec Fragmentation**: The v1.3 JSON specification format is highly fragmented (`prd.md`, `requirements.json`, `test_spec.json`, `tasks.json`, `architecture.md`). This forces the existence of three separate packages (`spec`, `afspec`, `agentspec`) just to scaffold and parse work.
2. **Archetype Bloat**: The system utilizes 6 archetypes (Coder, Reviewer, Curator, Verifier, Gate, Maintainer) with multiple sub-modes. This causes scheduling overhead and cognitive overload for developers configuring the system.
3. **Heavy State Management**: Using DuckDB for local state, while robust, introduces migration overhead, database locks, and binary dependencies that complicate the installation and execution pipeline.
4. **CLI Fragmentation**: Users must jump between `spec`, `af`, and `nightshift` CLIs.

### What clutter can be removed?
- **Deprecate the JSON Spec Pipeline**: We can drop `requirements.json`, `test_spec.json`, and `tasks.json`. Modern LLMs (like Claude 3.5 Sonnet/Opus) can derive tasks and test contracts dynamically from a single, well-structured Markdown PRD. This allows us to retire the `spec`, `afspec`, and `agentspec` packages entirely.
- **Consolidate Archetypes**: Merge `Curator` and `Gate` into `Reviewer`. The system should only need: **Coder** (builds), **Reviewer** (checks specs/code), and **Verifier** (runs tests/compiles).
- **Simplify State**: Replace DuckDB with a lightweight SQLite or even a pure append-only JSONL state file. For local, single-developer orchestration, DuckDB's analytical engine is overkill.
- **Unified CLI**: Merge all tools into a single `af` binary (`af init`, `af run`, `af daemon`).

---

## 3. Simplification Initiatives ("Simpler & Faster")

### 3.1. Single-Artifact Specifications (Markdown-First)
- **Concept**: Move from 5 separate files to a single `feature.md` file. 
- **Execution**: The orchestrator parses Markdown headers (e.g., `## Requirements`, `## Tasks`) on the fly. If `## Tasks` is missing, an ephemeral planning agent generates the DAG in-memory before execution.
- **Impact**: Reduces token overhead, eliminates the blocking `spec generate / validate` step, and aligns with how developers naturally write documentation.

### 3.2. The Unified `af` CLI
- **Concept**: Combine `spec`, `af`, and `nightshift` into one cohesive CLI.
- **Commands**:
  - `af run <feature.md>`: Automatically parses, plans, and executes.
  - `af watch`: The new name for `nightshift`, running continuous fix-loops.
- **Impact**: Zero context switching. A developer installs one tool and uses one command.

### 3.3. Lean Archetype Model
- **Concept**: Reduce 6 archetypes to 3.
  - **Coder**: Implements code.
  - **Reviewer**: Assesses PRD quality and reviews code output.
  - **Verifier**: Runs test suites and linters.
- **Impact**: Simplifies the dispatch loop, reduces prompt assembly logic, and makes the system vastly easier to configure.

---

## 4. Game-Changer Enhancements ("What Else?")

### 4.1. Visual Task Graph & Live Dashboard (`af ui`)
- **Idea**: CLI output is inherently limited for DAGs. Agent-Fox should include a local web dashboard (running on `localhost:3000`) that visualizes the task graph, agent status, and live logs.
- **Why it’s a game-changer**: Developers can *see* the parallel execution across git worktrees in real-time. Clicking on a node shows the live Claude streaming response, the diff being generated, and review findings. It transforms a "black box" into an observable factory.

### 4.2. Human-in-the-Loop Asynchronous Handoffs
- **Idea**: Currently, if an agent hits a complex architectural issue or budget cap, it fails or blocks. We should implement an "Ask User" protocol. 
- **Mechanism**: The agent pauses its worktree, sends a push notification (via Slack, Discord, or an IDE extension), and asks a specific question: *"I found two ways to implement the Auth schema. Should I use JWT or Session Cookies?"* 
- **Why it’s a game-changer**: It blends autonomous execution with human intuition, preventing 45-minute wasted sessions due to early bad assumptions.

### 4.3. Pull-Request Native Workflow
- **Idea**: Instead of merging directly to the local `main` integration branch via a complex squash-merge lock, Agent-Fox should push feature branches to the remote and open a Draft PR.
- **Mechanism**: The Verifier/Reviewer agents leave their findings as actual GitHub/GitLab comments on the PR. The Coder agent listens for human comments on the PR and pushes fix commits.
- **Why it’s a game-changer**: It integrates seamlessly into existing engineering team workflows. Humans review Agent-Fox just like they review junior developers.

### 4.4. Ephemeral Preview Environments
- **Idea**: For frontend or full-stack tasks, tests aren't enough. When a task group completes, Agent-Fox automatically spins up an ephemeral preview environment (e.g., using Docker or Vite) on a local port.
- **Mechanism**: The standup report includes: `Preview ready at http://localhost:3001`.
- **Why it’s a game-changer**: Allows product managers and designers to verify the agent's work visually, not just via passing unit tests.

### 4.5. "Nightshift" Chaos Engineering
- **Idea**: Upgrade the `nightshift` daemon from just fixing `af:fix` issues to proactive codebase improvement.
- **Mechanism**: In idle time, the daemon runs mutation testing, fuzzes APIs, or refactors legacy code to improve performance, opening low-priority PRs for human approval.

---

## 5. Implementation Strategy

**Phase 1: The Great Pruning (Simpler)**
- Deprecate `spec`, `agentspec`, and `afspec`.
- Introduce the single `feature.md` parser.
- Consolidate to 3 archetypes.
- Refactor the orchestrator to accept in-memory DAGs without DuckDB strict requirements.

**Phase 2: The Observable AI (Faster & Better)**
- Build the local React-based `af ui` dashboard.
- Wire the Python orchestrator to stream WebSocket events (node status, logs, diffs) to the UI.

**Phase 3: Team Integration (Game-Changers)**
- Integrate GitHub API for PR creation and comment-driven resolution.
- Implement the "Ask User" webhook architecture for async handoffs.

---
*Prepared by Product Management for the Agent-Fox Engineering Team.*