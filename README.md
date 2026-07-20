# LazyMind

**[中文](README.CN.md)** | **English**

> **Make AI reliably complete real tasks using your knowledge, standards, and preferences.**

LazyMind is an **AI Skill Runtime** for knowledge-intensive tasks. It productizes the way advanced users manually organize models, knowledge, Skills, tools, and workflows, making that capability directly usable by everyone else.

You no longer need to repeatedly upload context, tune prompts, configure CLI tools, or supervise every agent step. LazyMind works from your knowledge, exposes each step and intermediate artifact, and learns from feedback to better match your delivery standards. Run it locally in **Desktop Mode**, or deploy it as a shared enterprise service.

---

## Why LazyMind?

Once a task goes beyond simple Q&A, users usually encounter four obstacles:

- **Scattered knowledge**: project materials, previous reports, and domain context live across local files and collaboration platforms, forcing users to find, upload, and explain them again.
- **Standards that never stick**: AI can generate content, but it does not know the required terminology, tone, format, or delivery boundaries, so every output needs another revision cycle.
- **Long tasks that drift**: as sources and tools multiply, agents skip steps, lose constraints, or go off course, leaving users to supervise the entire run.
- **Advanced capabilities with a high barrier**: models, Skills, Plugins, and workflows are improving quickly, but combining, configuring, and maintaining them still depends on a small group of expert users.

LazyMind absorbs that complexity through three connected systems:

| System | Question it answers | What users gain |
|--------|---------------------|-----------------|
| **Knowledge Foundation** | What can AI see? | Knowledge stays reusable and traceable instead of being moved into every task again |
| **State Brain** | How does a long task run reliably? | Visible progress, editable results, recoverable failures, and user control over key decisions |
| **AI Growth Engine** | Can the next run be better? | Preferences, terminology, feedback, and evaluations accumulate into continuously better delivery |

Together they form a productized Skill Runtime: not another AI entry point, but a system that turns model capability into reliable outcomes.

---

## Highlights

### 1. Move from asking a question to delivering an outcome

Select your knowledge and a Skill. LazyMind continues through source organization, planning, generation, review, and delivery instead of stopping after a single answer.

Built-in scenarios include:

- **AI Writer**: organize sources → create outline → draft sections → make local revisions → review the whole document → finalize
- **AI Image**: understand the request → collect references → optimize the prompt → generate or edit images, including animated stickers

**How:** Local directories, object storage, Feishu, Notion, and other sources feed a unified knowledge base. PDFReader, MinerU, or PaddleOCR-VL parses the content, while multi-embedding retrieval, hybrid search, and reranking ground the agent in relevant evidence.

**Why:** Model capability should not remain trapped in a chat window. AI becomes useful when it connects real knowledge to a delivery process and actually finishes the work.

![Create a data source](docs/assets/datasource-create.png)

### 2. Stop supervising every step without giving up control

For long-running tasks, LazyMind continuously shows status, tool calls, elapsed time, and intermediate results. Users can approve checkpoints, edit artifacts directly, or rerun from the step that went wrong instead of starting over.

**How:** Plugins use state machines to define steps, tools, inputs, outputs, and transitions. Artifacts keep revision history, while a Driver automatically reviews results and decides whether to continue, retry, or rewind.

**Why:** The hardest part of trusting a complex agent is not occasional failure—it is invisible execution and uncontrollable errors. LazyMind turns a black box into a workspace where users and agents finish the task together.

### 3. Turn expert AI practices into capabilities anyone can use

A useful research method, writing process, or piece of domain expertise does not need to remain trapped in someone's prompts, scripts, and configuration. It can be managed as a Skill and turned into an executable Plugin that a team can run repeatedly.

**How:** LazyMind inspects a Skill's files, scripts, and tool dependencies, evaluates whether it can become a workflow, maps available platform tools, and generates a Plugin. The result supports diagnostics, repair, publishing, revision history, and rollback.

**Why:** A prompt solves one task; a reliable workflow solves a class of tasks. LazyMind turns expert setup and orchestration into a reusable, traceable team asset instead of depending on the one person who knows how to assemble the tools.

See the [Plugin format specification](docs/plugin-format.md) to build your own workflow.

### 4. Run it on your machine and keep sensitive knowledge under your control

Individuals and small teams can manage knowledge and run agents through Desktop Mode without building a complete cloud stack first. When collaboration is required, the same product can be deployed as a shared enterprise service.

**How:** Local mode uses native processes, SQLite, and Milvus Lite, and manages the Go, Python, and Node services with platform-standard data paths. It currently provides a macOS arm64 desktop build and Windows x64 ZIP and installer builds.

**Why:** Deployment complexity and data security are often the first barriers to trying a knowledge product. Desktop Mode reduces setup cost while keeping local documents and runtime data under the user's control.

Shared deployments support Kong, JWT/RBAC, Core ACL, external Milvus/OpenSearch, and on-premises OCR. See the [Desktop documentation](desktop/README.md) for details.

### 5. Make every human intervention and negative rating improve the next run

User edits, rejected answers, added rules, and negative ratings should not disappear when a conversation ends. LazyMind turns these signals into growth assets that can be reviewed, reused, and validated—helping the system understand user preferences while continuously fixing recurring failures.

This happens through two connected loops:

- **Knowledge Ops (智积阅累)—capture what the user wants**: manage preferences, terminology, experience, and Skills distilled from edits, ratings, and usage history as reviewable assets with revision history and rollback. Users no longer need to restate the same standards in every task.
- **evo—verify how the system can improve**: turn negative ratings and bad cases into evaluation samples, locate problems in knowledge, retrieval, prompts, tools, or algorithm strategies, and run “baseline eval → generate fix → A/B test → merge and deploy” to verify that a change actually works.

**How:** A human intervention can become both long-term memory or guidance in Knowledge Ops and evidence in the evo evaluation loop. Both preserve provenance, revisions, and validation steps, with user review at key checkpoints.

**Why:** Real self-evolution does not mean letting a model change itself unchecked. It means remembering human standards, identifying why a failure happened, and adopting a better strategy only after validation. Every task therefore contributes knowledge and evidence to the next delivery.

![Knowledge operations](docs/assets/knowledge-ops.png)

![RAG self-evolution pipeline](docs/assets/evo-pipeline.png)

![Live evo orchestration](docs/assets/evo-run.png)

---

## Quick start

### Run locally

Prerequisites: Go, Python 3, uv, pnpm, and Node.js.

```bash
make local-up
```

On native Windows PowerShell:

```powershell
make local-win-up
```

After startup:

- LazyMind: http://localhost:8090
- API docs: http://localhost:8090/docs.html
- Default credentials: `admin` / `admin`

Configure the LLM, embedding, and reranker in Model Settings after login. VLM, image, and video models are optional. For high-quality PDF parsing, you can also configure a MinerU API key before startup:

```bash
export LAZYLLM_MINERU_API_KEY=your_mineru_key
```

Stop the local runtime with:

```bash
make local-down
```

Use `make local-win-down` on Windows. See the [Quick Start guide](docs/quick_start.md) for complete configuration.

### Build the desktop application

| Platform | Command | Output |
|----------|---------|--------|
| macOS arm64 | `make desktop-darwin-arm64` | macOS desktop application |
| Windows x64 | `make desktop-windows-x64` | Portable ZIP |
| Windows x64 | `make desktop-windows-x64-installer` | Installer |

### Deploy with containers

```bash
make up
```

Common deployment options:

### Start the Container Stack on macOS with Colima

If Docker Desktop is not available on your machine, you can use [Colima](https://github.com/abiosoft/colima) as the Docker runtime. If Homebrew is not installed, install it first:

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

After installation, follow the terminal prompt to configure `PATH`. On Apple Silicon, this is usually:

```bash
echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> ~/.zprofile
eval "$(/opt/homebrew/bin/brew shellenv)"
brew --version
```

Install Colima and the Docker command-line tools:

```bash
brew install colima docker docker-compose docker-buildx
```

Start Colima:

```bash
colima start --runtime docker --vm-type vz --mount-type virtiofs --cpu 4 --memory 6 --disk 80
```

Verify the environment:

```bash
colima status
docker version
docker compose version
```

After the environment is ready, start the container stack from the project root:

```bash
make up
```

When finished, stop the container stack first, then stop Colima:

```bash
make down
colima stop
```

### Startup Command Reference

In addition to the Colima container stack, LazyMind also supports host-local runtime and other startup modes:

| Scenario | Command |
|----------|---------|
| Build images and start | `make up-build` |
| Deploy MinerU OCR on-premises | `make up LAZYMIND_DEPLOY_MINERU=1` |
| Deploy PaddleOCR on-premises | `make up LAZYMIND_DEPLOY_PADDLEOCR=1` |
| Use external Milvus/OpenSearch | `make up LAZYMIND_MILVUS_URI=http://your-milvus:19530 LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200` |

See the [Architecture guide](docs/architecture.md) for service dependencies, environment variables, and the authentication chain.

---

## Available today

| Area | Current capabilities |
|------|----------------------|
| Knowledge base | Multiple sources, OCR, vectorization, hybrid retrieval, reranking, sync management |
| Agents | RAG chat, tool calls, subtasks, artifacts, task center |
| Plugins | State machines, dynamic routing, automatic review, retry/rewind, visual execution, versioned artifacts |
| Skills | Installation, organization, review, revisions, rollback, Skill → Plugin |
| Self-evolution | Eval-set generation, evaluation, bad-case analysis, repair, deployment, A/B testing |
| Local experience | macOS/Windows local runtime, desktop builds, platform-standard data paths |
| Enterprise | Kong, JWT/RBAC, ACL, OAuth sources, optional external storage |

This table describes capabilities implemented in the repository today, not a future roadmap. See [docs](docs/) for module design and implementation details.

---

## Roadmap

LazyMind's next phase is not about adding more isolated features. The goal is to make knowledge bases, Skills, Plugins, and self-evolution work together in complete, real-world task loops.

### Near term: flagship workflows people can try immediately

- **Knowledge to deliverable**: complete workflows for customer solutions, product manuals, and product research—from retrieval and planning to drafting, review, and delivery.
- **Better local revision**: selection-based rewriting, knowledge-grounded expansion, diffs, accept/reject controls, and partial reruns from affected steps.
- **Result delivery**: stronger Markdown, DOCX, and PDF export, shareable result pages, and initial publishing targets such as Feishu and Notion.
- **Ready-to-run demos**: sample knowledge packs, task templates, and completed outputs so new users can experience an end-to-end workflow without preparing private data first.
- **Desktop experience**: simpler installation, model setup, data import, and local-runtime diagnostics.

### Mid term: a distribution network for knowledge and capabilities

- **Knowledge and Skill/Plugin marketplace**: curated discovery, one-click installation, updates, dependency checks, and trusted-source information.
- **Reusable scenario packages**: combine workflows, knowledge packs, review rules, and output formats into installable industry solutions.
- **External agent access**: expose LazyMind knowledge and workflows to Codex, Cursor, Hermes Agent, OpenClaw, and others through MCP, CLI, OpenAPI, and SDKs.
- **More connectors**: progressively connect collaboration, email, calendar, code, and task systems for weekly reports, research, and content workflows.
- **Team collaboration**: improve workflow sharing, approvals, permissions, run history, and organization-level template governance.

### Long term: from executable workflows to a self-evolving work system

- Detect workflow and knowledge gaps from user edits, reruns, citations, and final acceptance signals.
- Continuously evaluate and A/B test retrieval strategies, prompts, models, tools, and Plugin revisions.
- Turn successful execution patterns into reusable Skills, templates, and organizational memory with full provenance and version history.
- Expand across industries through horizontal task templates plus vertical knowledge packs instead of rebuilding the product for every industry.

The roadmap will evolve based on real workflow completion rates, output quality, human interventions, latency, and cost. Repository issues, milestones, and release notes remain the source of truth for specific releases.

---

## Project layout

```text
LazyMind/
├── frontend/                   # Web UI and desktop frontend
├── backend/
│   ├── auth-service/           # Authentication, OAuth, and users
│   ├── core/                   # Data, tasks, retrieval, Plugins, and ACL
│   └── scan-control-plane/     # Source scanning and synchronization
├── algorithm/
│   └── lazymind/               # Chat, parsing, retrieval, and agent runtime
├── plugins/                    # Built-in Plugins
├── skills/                     # Built-in and curated Skills
├── evo/                        # Self-evolution and evaluation loop
├── desktop/                    # Electron desktop application and packaging
├── local/                      # Host-local runtime management
├── api/                        # OpenAPI specifications
├── docs/                       # Architecture, usage, and design docs
└── tests/                      # Cross-service tests
```

---

## Development and testing

```bash
make lint              # Python, Go, docs, and other static checks
make lint-only-diff    # Check changed files only
make test              # Test with host-provided runtimes
make test-hermetic     # Test the same scope in project-managed runtimes
```

- Python 3.11+
- Go 1.24.0
- Node.js 20
- OpenAPI specifications are maintained under `api/`

---

## License

See [LICENSE](LICENSE).
