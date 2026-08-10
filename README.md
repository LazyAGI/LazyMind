# LazyMind

**[中文](README.CN.md)** | **English**

> **Make AI reliably complete real tasks using your knowledge, standards, and preferences.**

[![GitHub stars](https://img.shields.io/github/stars/LazyAGI/LazyMind?style=flat-square)](https://github.com/LazyAGI/LazyMind/stargazers)
[![License](https://img.shields.io/github/license/LazyAGI/LazyMind?style=flat-square)](LICENSE)
[![macOS](https://img.shields.io/badge/macOS-arm64-111827?style=flat-square&logo=apple)](desktop/README.md)
[![Windows](https://img.shields.io/badge/Windows-x64-0078D4?style=flat-square&logo=windows)](desktop/README.md)
[![Local first](https://img.shields.io/badge/deployment-local--first-16a34a?style=flat-square)](docs/quick_start.md)

LazyMind is an **AI Skill Runtime** for knowledge-intensive work. It connects reusable knowledge, executable Skills, observable workflows, editable artifacts, and evaluation-driven improvement in one workspace.

Instead of repeatedly uploading context, tuning prompts, and supervising every agent step, you choose the knowledge and workflow once. LazyMind then plans, executes, exposes intermediate results, and carries accepted feedback into the next run. Use it locally in **Desktop Mode** or deploy it as a shared enterprise service.

**[Quick start](#quick-start)** · **[Product architecture](docs/architecture.md)** · **[Build a workflow](docs/workflow-format.md)** · **[Desktop mode](desktop/README.md)**

---

## What can you ship with it?

| Scenario | LazyMind runs | You receive |
|----------|---------------|-------------|
| **Research and review** | Search sources → retrieve evidence → compare → synthesize → review | A traceable report grounded in your documents and external sources |
| **AI Writer** | Organize sources → outline → draft sections → revise → final review | An editable, versioned document rather than a one-shot answer |
| **AI Image** | Interpret intent → collect references → refine prompt → generate/edit | Images and animated stickers with the process retained |
| **Knowledge assistant** | Connect sources → parse/OCR → hybrid retrieve → rerank → answer | Answers linked back to reusable organizational knowledge |
| **Quality improvement** | Capture a bad case → evaluate → diagnose → A/B test → deploy | A verified strategy improvement, not an unchecked prompt change |

## How LazyMind works

```mermaid
flowchart LR
    K["Knowledge<br/>local files · cloud docs · object storage"] --> R["Retrieve & reason<br/>parse · OCR · hybrid search · rerank"]
    S["Skills & workflows<br/>instructions · tools · state machines"] --> X["Observable execution<br/>steps · approvals · retry · rewind"]
    R --> X
    X --> A["Editable artifacts<br/>citations · revisions · delivery"]
    A --> F["Feedback & evaluation<br/>preferences · bad cases · A/B tests"]
    F --> K
    F --> S
```

This loop is built from three connected systems:

| System | Responsibility | Product behavior |
|--------|----------------|------------------|
| **Knowledge Foundation** | Give AI the right context | Multi-source ingestion, OCR, hybrid retrieval, reranking, and source traceability |
| **State Brain** | Keep long tasks on course | Visible steps, approvals, editable artifacts, retries, rewinds, and version history |
| **AI Growth Engine** | Improve future runs safely | Reviewable preferences and terminology plus evaluation, diagnosis, A/B tests, and rollback |

## Core highlights

### 1. Deliver outcomes, not chat messages

Choose knowledge and a Skill; LazyMind continues from source organization through planning, generation, review, and delivery. Workflows define steps, tools, inputs, outputs, and transitions as state machines, while artifacts preserve editable results and revision history.

For long-running work, each step remains visible. Users can approve checkpoints, edit an artifact, or rerun from the failed step instead of restarting the whole task.

<table>
  <tr>
    <td width="50%" align="center">
      <a href="docs/assets/artifact-workspace-en.png"><img src="docs/assets/artifact-workspace-en.png" alt="A real Artifact remains editable at an approval checkpoint" width="100%" /></a>
      <br /><sub>Inspect and edit the Artifact before continuing</sub>
    </td>
    <td width="50%" align="center">
      <a href="docs/assets/artifact-version-diff-en.png"><img src="docs/assets/artifact-version-diff-en.png" alt="Inspect the execution trail and approve the next step without restarting" width="100%" /></a>
      <br /><sub>Review a version diff and restore the result you need</sub>
    </td>
  </tr>
</table>

### 2. Ground every run in reusable knowledge

Local directories, object storage, Feishu, Notion, and other sources feed a unified knowledge base. PDFReader, MinerU, or PaddleOCR-VL parses documents; multi-embedding retrieval, hybrid search, and reranking keep results grounded in relevant evidence.

<table>
  <tr>
    <td width="50%" align="center">
      <a href="docs/assets/knowledge-library-en.jpg"><img src="docs/assets/knowledge-library-en.jpg" alt="Manage parsed documents in a reusable knowledge base" width="100%" /></a>
      <br /><sub>Organize documents and track parsing status in one knowledge base</sub>
    </td>
    <td width="50%" align="center">
      <a href="docs/assets/knowledge-cited-answer-en.jpg"><img src="docs/assets/knowledge-cited-answer-en.jpg" alt="Answer with inline citations and an automatically generated reference list" width="100%" /></a>
      <br /><sub>Ground answers with inline citations and traceable references</sub>
    </td>
  </tr>
</table>

### 3. Package expert practice into reusable workflows

Research methods, writing processes, and domain standards can be managed as Skills and converted into executable Workflows. Teams can diagnose, repair, publish, version, and roll them back instead of rebuilding the same setup from prompts and scripts. See the [Workflow format specification](docs/workflow-format.md).

<table>
  <tr>
    <td width="50%" align="center">
      <a href="docs/assets/skill-to-workflow-entry-en.png"><img src="docs/assets/skill-to-workflow-entry-en.png" alt="Create an executable workflow from an existing Skill" width="100%" /></a>
      <br /><sub>Select a Skill as the source of a new workflow</sub>
    </td>
    <td width="50%" align="center">
      <a href="docs/assets/skill-to-workflow-editor-en.png"><img src="docs/assets/skill-to-workflow-editor-en.png" alt="Manage converted and custom workflows after generation" width="100%" /></a>
      <br /><sub>Inspect, refine, publish, and version the generated workflow</sub>
    </td>
  </tr>
</table>

### 4. Improve only after evidence

Knowledge Ops captures what the user wants—preferences, terminology, experience, and Skills. `evo` tests how the system should improve by turning bad cases into evaluation samples and running baseline evaluation, diagnosis, repair, and A/B testing.

<table>
  <tr>
    <td width="50%" align="center">
      <a href="docs/assets/skill-review-en.jpg"><img src="docs/assets/skill-review-en.jpg" alt="Knowledge Ops reviews and improves reusable Skills" width="100%" /></a>
      <br /><sub>Knowledge Ops reviews Skills, preferences, terminology, and experience</sub>
    </td>
    <td width="50%" align="center">
      <a href="docs/assets/evo-pipeline-en.jpg"><img src="docs/assets/evo-pipeline-en.jpg" alt="The evo workspace turns failures into an evaluated improvement pipeline" width="100%" /></a>
      <br /><sub>Algorithm evolution validates improvements before rollout</sub>
    </td>
  </tr>
</table>

### 5. Start local, scale when collaboration requires it

Desktop Mode uses native processes, SQLite, and Milvus Lite with platform-standard data paths. Shared deployments add Kong, JWT/RBAC, Core ACL, external Milvus/OpenSearch, and on-premises OCR. Your workflow stays recognizable across both modes.

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

After login, open **Settings** in the frontend:

- Add provider credentials and API keys under **Model Providers**, then select the default LLM, embedding, and reranker under **System Defaults**. Multimodal embedding, VLM, speech, image, video, and evolution models are optional.
- Configure service credentials under **Tools** when needed, including MinerU or PaddleOCR for document parsing, web and academic search engines, and other integrations. No environment variable is required for a hosted MinerU API key.

<table>
  <tr>
    <td width="50%" align="center">
      <a href="docs/assets/settings-models-en.jpg"><img src="docs/assets/settings-models-en.jpg" alt="Select default models in frontend settings" width="100%" /></a>
      <br /><sub>Select the default models for each system capability</sub>
    </td>
    <td width="50%" align="center">
      <a href="docs/assets/settings-tools-en.jpg"><img src="docs/assets/settings-tools-en.jpg" alt="Configure document parsing and search providers in frontend settings" width="100%" /></a>
      <br /><sub>Configure document parsing, search, and integration credentials</sub>
    </td>
  </tr>
</table>

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

### Startup Command Reference

| Scenario | Command |
|----------|---------|
| Build images and start | `make up-build` |
| Deploy MinerU OCR on-premises | `make up LAZYMIND_DEPLOY_MINERU=1` |
| Deploy PaddleOCR on-premises | `make up LAZYMIND_DEPLOY_PADDLEOCR=1` |
| Use external Milvus/OpenSearch | `make up LAZYMIND_MILVUS_URI=http://your-milvus:19530 LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200` |

See the [Colima setup guide](docs/quick_start.md#macos-use-colima-instead-of-docker-desktop) or the complete [Quick Start guide](docs/quick_start.md). The [Architecture guide](docs/architecture.md) covers service dependencies, environment variables, and the authentication chain.

---

## Available today

| Area | Current capabilities |
|------|----------------------|
| Knowledge base | Multiple sources, OCR, vectorization, hybrid retrieval, reranking, sync management |
| Agents | RAG chat, tool calls, subtasks, artifacts, task center |
| Workflows | State machines, dynamic routing, automatic review, retry/rewind, visual execution, versioned artifacts |
| Skills | Installation, organization, review, revisions, rollback, Skill → Workflow |
| Self-evolution | Eval-set generation, evaluation, bad-case analysis, repair, deployment, A/B testing |
| Local experience | macOS/Windows local runtime, desktop builds, platform-standard data paths |
| Enterprise | Kong, JWT/RBAC, ACL, OAuth sources, optional external storage |

This table describes capabilities implemented in the repository today, not a future roadmap. See [docs](docs/) for module design and implementation details.

---

## Roadmap

LazyMind's next phase is not about adding more isolated features. The goal is to make knowledge bases, Skills, Workflows, and self-evolution work together in complete, real-world task loops.

### Near term: flagship workflows people can try immediately

- **Knowledge to deliverable**: complete workflows for customer solutions, product manuals, and product research—from retrieval and planning to drafting, review, and delivery.
- **Better local revision**: selection-based rewriting, knowledge-grounded expansion, diffs, accept/reject controls, and partial reruns from affected steps.
- **Result delivery**: stronger Markdown, DOCX, and PDF export, shareable result pages, and initial publishing targets such as Feishu and Notion.
- **Ready-to-run demos**: sample knowledge packs, task templates, and completed outputs so new users can experience an end-to-end workflow without preparing private data first.
- **Desktop experience**: simpler installation, model setup, data import, and local-runtime diagnostics.

### Mid term: a distribution network for knowledge and capabilities

- **Knowledge and Skill/Workflow marketplace**: curated discovery, one-click installation, updates, dependency checks, and trusted-source information.
- **Reusable scenario packages**: combine workflows, knowledge packs, review rules, and output formats into installable industry solutions.
- **External agent access**: expose LazyMind knowledge and workflows to Codex, Cursor, Hermes Agent, OpenClaw, and others through MCP, CLI, OpenAPI, and SDKs.
- **More connectors**: progressively connect collaboration, email, calendar, code, and task systems for weekly reports, research, and content workflows.
- **Team collaboration**: improve workflow sharing, approvals, permissions, run history, and organization-level template governance.

### Long term: from executable workflows to a self-evolving work system

- Detect workflow and knowledge gaps from user edits, reruns, citations, and final acceptance signals.
- Continuously evaluate and A/B test retrieval strategies, prompts, models, tools, and Workflow revisions.
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
│   ├── core/                   # Data, tasks, retrieval, Workflows, and ACL
│   └── scan-control-plane/     # Source scanning and synchronization
├── algorithm/
│   └── lazymind/               # Chat, parsing, retrieval, and agent runtime
├── workflows/                    # Built-in Workflows
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
