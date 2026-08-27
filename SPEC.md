# Claude Orchestra — Technical Specification
## v0.1 — February 5, 2026

---

## 1. System Overview

Claude Orchestra is a local-first, loop-based idea processing system. It transforms raw ideas into deployed outcomes through autonomous, self-correcting loops — research, creativity, specification, and implementation — each composed of coordinated Claude Code agents. The first and most developed loop type is code implementation: multiple Claude Code sessions working in parallel on the same codebase. But the architecture generalizes to any idea-to-output pipeline.

It uses SQLite for coordination state, git worktrees for file isolation, `claude -p` (headless mode) for programmatic agent spawning, and a knowledge graph for persistent content across loops.

### Design Constraints

| Constraint | Rationale |
|---|---|
| Zero infrastructure cost | Only Claude subscription required |
| Runs entirely locally | No cloud services, no Redis server, no Docker required |
| SQLite for coordination | Embedded, ACID, no server process, queryable (see research) |
| Git worktrees for isolation | Established pattern across 90%+ of community frameworks |
| Progressive complexity | Each layer is optional; start with manual, add automation |

---

## 2. Core Architecture

### 2.1 Four-Layer Design

```
Layer 4: Loops          — Typed loops (research, creativity, spec, implementation), idea lifecycle, chaining
Layer 3: Automation     — Process management, agent lifecycle, auto-merge
Layer 2: Coordination   — SQLite state, task queue, file locks, status tracking
Layer 1: Isolation      — Git worktrees, `claude -p`, CLAUDE.md per agent
```

**Layer 1** works standalone. Layer 2 adds observability. Layer 3 adds full automation. **Layer 4** adds the loop abstraction — self-correcting cycles that chain together to process ideas from concept to deployment. A user can operate at any layer. Each layer is optional; progressive complexity is preserved.

### 2.2 Components

| Component | Implementation | Purpose |
|---|---|---|
| **Orchestrator** | Claude Code session (Opus) | Task decomposition, assignment, review |
| **Agent** | `claude -p` in a git worktree | Execute a focused coding task |
| **State Store** | SQLite (WAL mode) | Tasks, locks, status, events, agent registry |
| **Worktree Manager** | `git worktree add/remove` | Create/destroy isolated agent workspaces |
| **Process Manager** | Go binary | Spawn, monitor, terminate agent processes |
| **Merge Controller** | Git merge + conflict detection | Integrate completed work back to main |
| **Loop Engine** | Orchestrator logic (Layer 4) | Configure, execute, and chain typed loops |
| **Idea Store** | SQLite `ideas` table | Track idea lifecycle from raw to deployed |
| **Knowledge Graph** | MCP memory server | Store loop content outputs, cross-session context |

### 2.3 Agent Roles

Based on research (AgentMesh, MACOG, AgentCoder), a minimal effective team is 5 roles:

| Role | Model Tier | Responsibility |
|---|---|---|
| **Architect** | Opus | Analyze codebase, decompose tasks, define interfaces, review |
| **Implementer** | Opus | Write code, run tests, iterate on failures |
| **Reviewer** | Opus | Security audit, style check, integration verification |
| **Scout** | Opus | File exploration, dependency mapping, context gathering |
| **Researcher** | Opus | Deep research, knowledge synthesis, source documentation |

> **Note:** Under Claude Max, all roles default to Opus (all-opus routing). The per-role tiering above (Sonnet/Haiku) is historical — Claude Max provides unlimited usage, so we optimize for quality not cost. Token counts are retained for observability.

The Researcher role is essential for Research and Creativity loops (Layer 4). For pure implementation work (Layers 1-3), the original 4 roles suffice. Fewer well-coordinated agents outperform larger teams (AgentCoder: 3 agents > MetaGPT's 5 > ChatDev's 7).

### 2.4 Loop Architecture (Layer 4)

Loops are self-correcting cycles of agent steps that process input into validated output. See `research/techniques/006` for the full design.

#### Loop Types

| Loop Type | Purpose | Key Agents | Input | Output |
|---|---|---|---|---|
| **Research** | Gather and synthesize knowledge | Researcher, Architect, Reviewer | Question or knowledge gap | Knowledge graph entities with sources |
| **Creativity** | Generate and evaluate novel approaches | Researcher, Architect, Reviewer | Problem space or constraints | Ranked viable approaches |
| **Spec-Building** | Transform idea into implementable spec | Scout, Researcher, Architect, Reviewer | Validated approach | Task DAG, interface contracts, file ownership |
| **Implementation** | Execute spec through parallel coding | Implementer, Reviewer | Validated specification | Merged, tested code |

#### Self-Correction

Every loop classifies failures and re-enters at the appropriate step:

| Error Class | Re-entry Point |
|---|---|
| Missing data | research step |
| Bad structure | outline step |
| Invalid plan | plan step |
| Execution failure | execute step |
| Validation failure | Depends on reviewer feedback |

Circuit breakers prevent infinite loops: max 3 retries per step, max 10 total iterations, configurable token budget.

#### Loop Chaining

Loops chain to form the idea lifecycle pipeline:

```
Idea → Research Loop → Creativity Loop → Spec-Building Loop → Implementation Loop → Deployed
```

Chaining is explicit initially (orchestrator triggers next loop) and event-driven later.

### 2.5 Idea Lifecycle

Ideas are the top-level unit of work in the idea factory. Each idea progresses through states driven by loop completions.

#### Idea States

```
raw → researching → ideating → specifying → implementing → validating → deployed
  ↘ parked (resume from any state)
  ↘ rejected (infeasible)
```

#### Idea Taxonomy

Ideas support hierarchy and tagging:
- `parent_id` — parent idea for sub-ideas
- `tags` — JSON array for cross-cutting categorization
- `knowledge_refs` — JSON array linking to knowledge graph entities produced by loops

---

## 3. When to Use CLI Prompts (`claude -p`) vs Interactive Mode

A critical design question: when does headless `claude -p` add value, and when is interactive `claude` (or subagents) better?

### 3.1 Use `claude -p` (headless) when:

| Scenario | Why Headless Works |
|---|---|
| **Batch task execution** | Agent receives a complete, well-specified task. No human input needed mid-run. |
| **Parallel worker spawning** | Orchestrator launches N agents simultaneously, each in its own worktree. |
| **CI/CD integration** | Automated pipelines need non-interactive execution with JSON output. |
| **Ralph Wiggum loops** | Persistent loops re-feeding the same prompt need unattended execution. |
| **Scripted orchestration** | Shell/Python scripts that parse `--output-format json` results programmatically. |
| **Session chaining** | `--resume <session_id>` for multi-step pipelines where each step builds on the last. |

**Key flags for headless:**
```bash
claude -p "task description" \
  --allowedTools "Read,Edit,Bash,Glob,Grep" \
  --output-format json \
  --append-system-prompt "You are an implementer agent. Focus only on your assigned task."
```

### 3.2 Use interactive `claude` when:

| Scenario | Why Interactive Works |
|---|---|
| **Orchestrator role** | The human is the orchestrator, reviewing agent output and redirecting. |
| **Exploratory work** | Task is ambiguous; needs back-and-forth to clarify approach. |
| **Architecture decisions** | Requires human judgment on tradeoffs. |
| **Debugging failures** | Agent got stuck; human needs to inspect state and redirect. |
| **Learning/onboarding** | Understanding a new codebase where the human needs to see the agent's reasoning. |

### 3.3 Use subagents (Task tool) when:

| Scenario | Why Subagents Work |
|---|---|
| **Within-session parallelism** | Need 3-5 things researched simultaneously without leaving the conversation. |
| **Context isolation** | Don't want a research side-quest polluting the main context window. |
| **Model tiering** | Route a quick lookup to Haiku while the main session uses Opus. |
| **Result aggregation** | Need to combine results from multiple explorations before deciding. |

### 3.4 Use Agent Teams when:

| Scenario | Why Teams Work |
|---|---|
| **Peer-to-peer collaboration** | Agents need to message each other directly, not just report to orchestrator. |
| **Shared task list** | Multiple agents self-assigning from a common pool. |
| **Visual monitoring** | You want to watch all agents simultaneously in tmux/split panes. |

### 3.5 Decision Matrix

```
Is the task well-specified and complete?
  YES → Is it one of many parallel tasks?
    YES → claude -p (headless batch)
    NO  → claude -p (headless single) or interactive
  NO  → Is it exploratory or needs human judgment?
    YES → interactive claude
    NO  → Is it a sub-task within a larger session?
      YES → subagent (Task tool)
      NO  → interactive claude
```

### 3.6 Orchestrator Spawning Pattern

The orchestrator uses `claude -p` to spawn worker agents:

```bash
# Orchestrator decomposes task, then spawns workers
for task in "${tasks[@]}"; do
  worktree=".worktree/task-${task_id}"
  git worktree add "$worktree" -b "feature/task-${task_id}"

  claude -p "$(cat task-${task_id}.md)" \
    --allowedTools "Read,Edit,Bash,Glob,Grep,Write" \
    --output-format stream-json \
    --append-system-prompt "$(cat agent-system-prompt.md)" \
    > "logs/task-${task_id}.jsonl" 2>&1 &

  echo $! > "pids/task-${task_id}.pid"
done
```

---

## 4. SQLite Schema

### 4.1 Core Tables

```sql
-- Agent registry
CREATE TABLE agents (
  id TEXT PRIMARY KEY,
  role TEXT NOT NULL,        -- architect, implementer, reviewer, scout, researcher
  status TEXT DEFAULT 'idle', -- idle, working, done, failed, dead
  worktree TEXT,
  pid INTEGER,
  current_task TEXT,
  heartbeat_at DATETIME,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Task queue
CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  description TEXT,
  status TEXT DEFAULT 'pending',  -- pending, assigned, in_progress, review, done, failed
  priority INTEGER DEFAULT 0,
  role TEXT DEFAULT 'implementer', -- scout, architect, implementer, reviewer, researcher
  assigned_to TEXT REFERENCES agents(id),
  depends_on TEXT,               -- JSON array of task IDs
  blocked_by TEXT,               -- JSON array of task IDs that must complete first
  worktree TEXT,
  branch TEXT,
  result TEXT,                   -- JSON: {success, output, errors}
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  started_at DATETIME,
  completed_at DATETIME
);

-- File locks (prevent concurrent modification)
CREATE TABLE file_locks (
  file_path TEXT PRIMARY KEY,
  locked_by TEXT REFERENCES agents(id),
  task_id TEXT REFERENCES tasks(id),
  locked_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME            -- TTL for dead agent recovery
);

-- Event log (audit trail)
CREATE TABLE events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
  agent_id TEXT,
  task_id TEXT,
  event_type TEXT NOT NULL,      -- task_claimed, task_completed, file_locked, error, etc.
  payload TEXT                   -- JSON details
);

-- Shared knowledge (blackboard)
CREATE TABLE blackboard (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,            -- JSON
  written_by TEXT,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### 4.2 Loop & Idea Tables (Layer 4)

```sql
-- Ideas — top-level unit of work in the idea factory
CREATE TABLE ideas (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  description TEXT,
  status TEXT DEFAULT 'raw',       -- raw, researching, ideating, specifying, implementing, validating, deployed, parked, rejected
  current_phase TEXT,              -- which loop type is active
  parent_id TEXT REFERENCES ideas(id),  -- hierarchical ideas
  tags TEXT,                       -- JSON array of tags
  knowledge_refs TEXT,             -- JSON array of knowledge graph entity names
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Loops — self-correcting cycles of agent steps
CREATE TABLE loops (
  id TEXT PRIMARY KEY,
  idea_id TEXT REFERENCES ideas(id),
  loop_type TEXT NOT NULL,         -- research, creativity, spec_building, implementation
  status TEXT DEFAULT 'pending',   -- pending, running, paused, completed, failed, cancelled
  iteration INTEGER DEFAULT 0,    -- current iteration count
  max_iterations INTEGER DEFAULT 10,  -- circuit breaker
  token_budget INTEGER,            -- max tokens for this loop
  tokens_used INTEGER DEFAULT 0,
  result TEXT,                     -- JSON: summary of loop output
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  started_at DATETIME,
  completed_at DATETIME
);

-- Loop steps — individual steps within a loop iteration
CREATE TABLE loop_steps (
  id TEXT PRIMARY KEY,
  loop_id TEXT REFERENCES loops(id),
  task_id TEXT REFERENCES tasks(id),  -- bridges into existing tasks table
  step_type TEXT NOT NULL,         -- research, outline, plan, execute, validate, feedback
  step_order INTEGER NOT NULL,
  status TEXT DEFAULT 'pending',   -- pending, running, completed, failed, skipped
  retry_count INTEGER DEFAULT 0,
  max_retries INTEGER DEFAULT 3,
  result TEXT,                     -- JSON: step output metadata
  knowledge_refs TEXT,             -- JSON array of knowledge graph entity names
  error_class TEXT,                -- missing_data, bad_structure, invalid_plan, execution_failure, validation_failure
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  completed_at DATETIME
);
```

### 4.3 Pragmas

```sql
PRAGMA journal_mode = WAL;       -- Concurrent reads without blocking
PRAGMA busy_timeout = 5000;      -- Wait 5s on write contention
PRAGMA foreign_keys = ON;
```

> **Note:** The `ideas`, `loops`, and `loop_steps` tables extend the schema without modifying existing tables. `loop_steps.task_id` bridges Layer 4 loops into the existing Layer 2 task system.

---

## 5. Workflow

### 5.1 Standard Flow

```
1. HUMAN provides a task/feature request
2. ORCHESTRATOR (Opus) analyzes codebase, produces:
   - Task decomposition (DAG of subtasks)
   - Interface contracts between tasks
   - File ownership map (which agent owns which files)
   - Dependency ordering
3. ORCHESTRATOR writes tasks to SQLite, creates worktrees
4. AGENTS (Sonnet, via claude -p) claim tasks, execute in worktrees
5. Each agent: implement → test → commit → mark done
6. REVIEWER (Opus) checks each completed task
7. MERGE CONTROLLER integrates worktrees back to main
8. HUMAN reviews final result
```

### 5.2 Task Lifecycle

```
pending → assigned → in_progress → review → done
                  ↘ failed (retry or reassign)
                           ↗
```

### 5.3 Dead Agent Recovery

- Agents write heartbeat timestamps every 30 seconds
- If heartbeat exceeds TTL (2 minutes), agent marked `dead`
- Dead agent's task returned to `pending`
- File locks released
- Worktree preserved for debugging (not auto-deleted)

### 5.4 Loop Execution Flow (Layer 4)

```
1. LOOP ENGINE receives loop configuration (type, idea_id, parameters)
2. ENGINE creates loop row in SQLite, generates step sequence for loop type
3. For each step:
   a. Create loop_step row, optionally create linked task
   b. Assign appropriate agent role based on step type
   c. Agent executes step, writes content to knowledge graph
   d. Step metadata (status, timing, knowledge_refs) written to SQLite
4. VALIDATE step: Reviewer checks output against completion criteria
5. If validation passes → advance to next step
6. If validation fails:
   a. Classify error (missing_data, bad_structure, invalid_plan, execution_failure)
   b. Select re-entry point based on error class
   c. Check circuit breaker (retry count, total iterations, token budget)
   d. If under limit → re-enter at determined step with failure context
   e. If at limit → pause loop, escalate to human
7. On loop completion → update idea status, emit completion event
```

### 5.5 Loop Chaining Flow (Layer 4)

```
1. Loop completes → orchestrator checks idea.status and idea.current_phase
2. Determine if next loop type is appropriate:
   - researching → ideating: start Creativity Loop
   - ideating → specifying: start Spec-Building Loop
   - specifying → implementing: start Implementation Loop
3. Create next loop with output of previous loop as input
4. Previous loop's knowledge_refs become available context for next loop
5. Human can intervene at any chain point: skip, repeat, fork, or terminate
```

---

## 6. Conflict Prevention Strategy

Based on research (CodeCRDT, Swarm-IOSM, multi-agent-coordination-mcp):

### 6.1 File Ownership (Primary)

Each task specifies which files it may modify. The orchestrator ensures no two concurrent tasks modify the same files. This is enforced via `file_locks` table.

### 6.2 Interface Contracts (Secondary)

When parallel tasks must interact (e.g., Agent A writes an API, Agent B calls it), the orchestrator defines the interface contract upfront:

```json
{
  "contract": "auth-api",
  "provider": "task-auth-backend",
  "consumer": "task-auth-frontend",
  "interface": {
    "endpoint": "POST /api/auth/login",
    "request": {"email": "string", "password": "string"},
    "response": {"token": "string", "user": "object"}
  }
}
```

Both agents receive this contract. The consumer codes against it without waiting for the provider to finish.

### 6.3 Merge Order

Tasks merge in topological order (dependencies first). Each merge triggers a test run. If tests fail, the merge is blocked and the task returned to `review`.

---

## 7. Cost Optimization

> **Historical note:** With Claude Max (unlimited usage), cost optimization is no longer a primary concern. All roles now use Opus by default. The tiering below is retained for reference and for users on API-key billing.

Based on academic research (Tokenomics arXiv:2601.14470, DoT arXiv:2502.04392):

### 7.1 Model Tiering

| Task Type | Model | Cost Ratio |
|---|---|---|
| File scouting, dependency mapping | Haiku | 1x |
| Code implementation, test writing | Sonnet | 3x |
| Architecture, decomposition, review | Opus | 15x |

### 7.2 Token Budget Per Task

Each task has a configurable token ceiling. If an agent exceeds it, the task is paused and flagged for human review rather than burning unlimited tokens.

### 7.3 Plan Caching

Decomposition plans for similar tasks are cached (arXiv:2506.14852 reports 50% cost reduction). If the user requests "add CRUD API for users" and we previously decomposed "add CRUD API for products," reuse the plan template.

### 7.4 Review Optimization

Code review consumes 59.4% of tokens (arXiv:2601.14470). Strategies:
- Incremental review (only review changed lines, not full files)
- Diff-based review (pass `git diff` to reviewer, not entire codebase)
- Batch review (review multiple tasks in one pass when possible)

---

## 8. MCP Server Stack

These MCP servers provide the tooling layer (all local, no auth required):

| Server | Purpose | Install |
|---|---|---|
| `sqlite` | Coordination database | `npx -y mcp-server-sqlite-npx` |
| `git-worktree` | Worktree lifecycle | `npx github:Mandalorian007/git-worktree-mcp` |
| `memory` | Cross-session knowledge graph | `npx -y @modelcontextprotocol/server-memory` |
| `pm` | Process management | `npx pm-mcp` |
| `filesystem` | Scoped file access | `npx -y @modelcontextprotocol/server-filesystem` |

---

## 9. Implementation Phases

### Phase 1: Manual Orchestra (Week 1)
- CLAUDE.md with orchestrator instructions
- Subagent definitions for each role
- Manual git worktree creation
- Human acts as orchestrator, using subagents for parallel work

### Phase 2: SQLite Coordination (Week 2-3)
- SQLite schema implementation (core tables + loop/idea tables)
- Task CRUD operations
- File locking
- Agent heartbeat monitoring
- Status dashboard (CLI-based)
- Basic idea table CRUD (create, list, update status)

### Phase 3: Automated Spawning (complete — 66 tests, 148 total)
- `claude -p` based agent spawning
- Process management (spawn, monitor, kill)
- Log capture and streaming
- Dead agent recovery
- Researcher agent integration (headless research sessions)

### Phase 4: Production Hardening (complete — 114 tests, 262 total)
- Per-role permission profiles (`dontAsk` mode + allow/deny lists)
- Failure classification library (rate limit, session limit, context exhaustion)
- Model fallback chain (Opus → Sonnet → Haiku)
- Post-completion validation (implementer must produce file changes)
- Session timeout detection and enforcement
- Spec size guards (truncation + warning)
- Worktree cleanup lifecycle management

### Phase 5: Smart Orchestration — The Conductor (complete — 80 tests, 342 total)
- `orchestra.sh` conductor: `go`, `status`, `merge`, `cost` commands
- Model routing by role (scout→haiku, implementer→sonnet, architect/reviewer→opus)
- Task-role storage (`role` column on tasks table)
- Auto-spawn of dependent tasks (monitor Phase 2, gated by `conductor:active` flag)
- Topological merge ordering (Kahn's algorithm)
- Conductor shakedown: 3/3 real agent tasks completed ($0.13 estimated)

### Phase 6: Idea Factory
- Research Loop with knowledge graph output
- Creativity Loop with divergent/convergent thinking
- Spec-Building Loop with full task DAG generation
- Loop chaining (explicit orchestrator-driven)
- Idea lifecycle management (raw → deployed pipeline)
- Event-driven loop chaining (automatic chain advancement)
- Academic API integration for Research loops (arXiv, Semantic Scholar, OpenAlex)

---

## 10. Key Academic Foundations

| Paper | Key Insight | Application |
|---|---|---|
| Scaling Agent Systems (2512.08296) | Centralized coordination contains errors 4.4x vs 17.2x independent | Use orchestrator pattern |
| CodeCRDT (2510.18893) | CRDTs enable conflict-free concurrent code gen | Future: CRDT-based shared state |
| Tokenomics (2601.14470) | Code review = 59.4% of token spend | Optimize review phase |
| DoT (2502.04392) | Tiered model routing saves 83.57% | Route by task complexity |
| Plan Caching (2506.14852) | Reuse plans = 50% cost reduction | Cache decomposition templates |
| AgentCoder (2312.13010) | 3 focused agents > 5-7 unfocused | Keep team small |
| TDAG (2402.10178) | Dynamic subtask decomposition | Generate agents per-task |
| Blackboard (2507.01701) | Shared workspace reduces token overhead | SQLite blackboard pattern |

---

## 11. Success Criteria

| Metric | Target | Phase |
|---|---|---|
| Parallel speedup | 3-5x over single session for decomposable tasks | 3+ |
| Merge conflict rate | <5% of parallel task completions | 3+ |
| Agent failure recovery | <2 minutes to detect and reassign | 3+ |
| Cost per task | Within 2x of single-agent equivalent | 3+ |
| Setup time | <5 minutes for Phase 1 | 1 |
| Infrastructure cost | $0 (Claude subscription only) | All |
| Loop self-correction rate | >80% of failures auto-recovered without human intervention | 4+ |
| Loop completion rate | >90% of loops reach completion (not circuit-breaker terminated) | 4+ |
| Idea pipeline throughput | Idea → deployed in <4 loop chain executions (no unnecessary repeats) | 5 |
| Knowledge graph utility | >70% of loop outputs referenced by at least one subsequent loop | 5 |
