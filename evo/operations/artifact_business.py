from __future__ import annotations

import hashlib
import json
import os
import re
import time
import urllib.error
import urllib.request
from collections import Counter
from dataclasses import dataclass
from typing import Any, Mapping

from evo.artifact_runtime import ArtifactPayload, ExternalCallRequest, ExternalCallResult

QUESTION_TYPES = ("single_hop", "single_doc_multi_hop", "multi_doc_multi_hop", "table_list", "formula")
DIFFICULTIES = ("easy", "medium", "hard")
METRICS = ("answer_correctness", "faithfulness", "doc_recall", "context_recall")
DEFAULT_KB_GROUPS = ("block", "line", "doc-summary")
_DOCUMENTS: dict[tuple[str, str], Any] = {}


def payload(schema: str, value: Mapping[str, Any] | list[Any]) -> ArtifactPayload:
    return ArtifactPayload(schema, value)


def load_corpus(source_config: Mapping[str, Any]) -> ArtifactPayload:
    dataset_id = _text(source_config.get("dataset_id") or source_config.get("kb_id") or "algo")
    docs, load_mode, errors = _input_documents(source_config, dataset_id), "inline", []
    if not docs:
        docs, load_mode = _kb_documents(source_config, dataset_id), "lazyllm_document"
    if not docs:
        raise ValueError(f"dataset {dataset_id} has no usable source units")
    unique_docs = sorted({_text(doc.get("doc_id")) for doc in docs if _text(doc.get("doc_id"))})
    page_size = _int_between(source_config.get("document_page_size") or source_config.get("page_size"), 200, 1, 5000)
    pages = [{
        "source_id": dataset_id,
        "page_index": index,
        "documents": page,
    } for index, page in enumerate(_chunks(docs, page_size), 1)]
    return payload("CorpusLoadReport", {
        "dataset_id": dataset_id,
        "sources": [{"source_id": dataset_id, "type": load_mode, "document_count": len(unique_docs)}],
        "document_pages": pages,
        "stats": {
            "source_count": 1,
            "loaded_doc_count": len(unique_docs),
            "source_unit_count": len(docs),
            "document_page_count": len(pages),
        },
        "skipped": [],
        "errors": errors,
    })


def build_corpus_snapshot(report: Mapping[str, Any], source_config: Mapping[str, Any]) -> ArtifactPayload:
    raw_docs = [
        doc
        for page in report.get("document_pages", [])
        for doc in page.get("documents", [])
        if isinstance(doc, Mapping)
    ]
    units = [
        _unit(doc, index)
        for index, doc in enumerate(raw_docs, 1)
    ]
    if not units:
        raise ValueError("corpus load report has no loaded documents")
    by_type = Counter(unit["unit_type"] for unit in units)
    return payload("CorpusSnapshot", {
        "dataset_id": _text(report.get("dataset_id") or source_config.get("dataset_id") or source_config.get("kb_id") or "algo"),
        "source_units": units,
        "source_unit_count": len(units),
        "unit_type_counts": dict(by_type),
        "source_report": {"stats": dict(report.get("stats") or {})},
    })


def prepare_case(config: Mapping[str, Any], snapshot: Mapping[str, Any], case_id: str) -> ArtifactPayload:
    units = list(snapshot.get("source_units") or [])
    if not units:
        raise ValueError("corpus snapshot has no source units")
    index = _case_index(case_id)
    qtype = _choice(config.get("question_types"), QUESTION_TYPES, index)
    difficulty = _choice(config.get("difficulties"), DIFFICULTIES, index)
    selected = _select_units(units, qtype, index)
    refs = [{
        "chunk_id": unit["chunk_id"],
        "doc_id": unit["doc_id"],
        "filename": unit["filename"],
        "content_preview": _clip(unit["content"], 800),
        "unit_type": unit["unit_type"],
    } for unit in selected]
    return payload("CasePreparation", {
        "case_id": case_id,
        "question_type": qtype,
        "difficulty": difficulty,
        "doc_reference": _unique_docs(selected),
        "context_reference": refs,
        "instruction": f"Generate a {difficulty} {qtype} evaluation case grounded in the selected evidence.",
        "source_snapshot_dataset_id": _text(snapshot.get("dataset_id")),
        "source_message_id": _text(config.get("source_message_id")),
    })


def generate_case(preparation: Mapping[str, Any]) -> ArtifactPayload:
    case_id = _text(preparation["case_id"])
    contexts = [item for item in preparation.get("context_reference", []) if isinstance(item, Mapping)]
    first = contexts[0] if contexts else {}
    filename = _text(first.get("filename") or "the selected source")
    evidence = _text(first.get("content_preview") or "No evidence text was available.")
    question = _question_from_evidence(filename, evidence)
    answer = _answer_from_evidence(evidence)
    return payload("DatasetCase", {
        "id": case_id,
        "question": question,
        "answer": answer,
        "question_type": _text(preparation.get("question_type")),
        "difficulty": _text(preparation.get("difficulty")),
        "grading_guidance": "The answer should match the grounded reference evidence and avoid unsupported facts.",
        "reference_context": [_text(item.get("content_preview")) for item in contexts],
        "reference_doc": [_text(item.get("filename")) for item in contexts],
        "reference_doc_ids": [_text(item.get("doc_id")) for item in contexts],
        "reference_chunk_ids": [_text(item.get("chunk_id")) for item in contexts],
        "source_preparation": preparation,
        "source_message_id": _text(preparation.get("source_message_id")),
    })


def assemble_dataset(cases: Mapping[str, ArtifactPayload]) -> ArtifactPayload:
    rows = [_case_payload(case_id, item.payload) for case_id, item in sorted(cases.items())]
    checks = _dataset_checks(rows)
    return payload("EvalDataset", {
        "id": "eval.dataset",
        "size": len(rows),
        "case_ids": [row["id"] for row in rows],
        "stats": {
            "question_type_counts": dict(Counter(row["question_type"] for row in rows)),
            "difficulty_counts": dict(Counter(row["difficulty"] for row in rows)),
        },
        "checks": checks,
        "preview": [{key: row[key] for key in ("id", "question", "question_type", "difficulty")} for row in rows],
        "cases": rows,
    })


def rag_answer(case: Mapping[str, Any], target_config: Mapping[str, Any], ctx: Any) -> ArtifactPayload:
    case_id = _text(case["id"])
    target_url = _text(target_config.get("target_chat_url"))
    dataset_id = _text(target_config.get("dataset_id") or target_config.get("kb_id") or target_config.get("dataset_name"))
    question = _text(case.get("question"))
    request_payload = {
        "query": question,
        "history": [],
        "trace": bool(target_config.get("require_trace", True)),
        "dataset": dataset_id,
        "filters": {"kb_id": [dataset_id]} if dataset_id else {},
        "reasoning": False,
        "disabled_tools": [
            "temp_kb",
            "wikipedia",
            "web_search",
            "academic_search",
            "url_fetch",
            "multimodal",
            "vocab_learn",
            "memory_editor",
            "skill_editor",
            "feishu",
        ],
    }
    model_config = getattr(ctx, "model_config", None) or {}
    model_identity = _model_config_identity(model_config)
    call_payload = {**request_payload, "llm_config": model_config or None}
    call_identity = {"target_chat_url": target_url, "payload": request_payload, "model_config": model_identity}
    result = (
        ctx.external.call(
            call_id=f"rag_answer:{case_id}",
            payload={"target_chat_url": target_url, "payload": call_payload},
            runner=HttpChatRunner(),
            idempotency_key=f"{case_id}:rag:{_stable_text(call_identity)}",
            payload_fingerprint=_stable_text(call_identity),
            metadata={"kind": "rag_answer", "case_id": case_id},
        )
        if target_url
        else ExternalCallResult("failed_permanent", error_type="missing_target_chat_url", error_message="target_chat_url is empty")
    )
    value = result.value if result.status == "completed" and isinstance(result.value, Mapping) else {}
    chat_error = None if result.status == "completed" else {"type": result.error_type, "message": result.error_message}
    answer = _text(value.get("answer") or value.get("text"))
    contexts = [str(item) for item in value.get("contexts") or value.get("sources") or []]
    source_doc_ids, source_chunk_ids = _source_ids([*(_as_list(value.get("sources"))), *(_as_list(value.get("contexts")))])
    doc_ids = _unique_texts([*_as_list(value.get("doc_ids") or value.get("document_ids")), *source_doc_ids])
    chunk_ids = _unique_texts([*_as_list(value.get("chunk_ids") or value.get("segment_ids") or value.get("segement_ids")), *source_chunk_ids])
    return payload("RagAnswer", {
        "case_id": case_id,
        "case": case,
        "question": question,
        "answer": answer,
        "status": "ok" if answer and chat_error is None else "failed",
        "chat_error": chat_error,
        "contexts": contexts,
        "doc_ids": doc_ids,
        "chunk_ids": chunk_ids,
        "trace_id": _text(value.get("trace_id")),
        "evidence_status": "found" if doc_ids or chunk_ids or contexts else "no_evidence",
        "target": {"target_chat_url": target_url, "dataset_id": dataset_id, "require_trace": request_payload["trace"]},
    })


def judge_answer(answer: Mapping[str, Any], policy: Mapping[str, Any]) -> ArtifactPayload:
    case = answer.get("case") if isinstance(answer.get("case"), Mapping) else {}
    case_id = _text(answer.get("case_id") or case.get("id"))
    if answer.get("status") == "failed" or answer.get("chat_error"):
        err = answer.get("chat_error") if isinstance(answer.get("chat_error"), Mapping) else {}
        reason = f"{_text(err.get('type') or 'ChatError')}: {_text(err.get('message') or 'RAG call failed')}"
        scores = dict.fromkeys(("answer_correctness", "faithfulness", "doc_recall", "context_recall"), 0.0)
        quality, failure = "bad", "infra_failure"
    else:
        reference = _norm(_text(case.get("answer")))
        actual = _norm(_text(answer.get("answer")))
        exact = bool(reference and actual and (reference in actual or actual in reference))
        doc_recall = _recall(case.get("reference_doc_ids"), answer.get("doc_ids"))
        chunk_recall = _recall(case.get("reference_chunk_ids"), answer.get("chunk_ids"))
        correctness = 1.0 if exact else 0.4 if actual else 0.0
        faithfulness = max(doc_recall, chunk_recall, 0.5 if answer.get("contexts") else 0.0)
        scores = {
            "answer_correctness": correctness,
            "faithfulness": round(faithfulness, 4),
            "doc_recall": doc_recall,
            "context_recall": chunk_recall,
        }
        quality, failure, reason = _quality(scores, policy), "none", "deterministic quality check completed"
        if quality != "good":
            failure = "retrieval_or_generation_issue"
    return payload("JudgeResult", {
        "case_id": case_id,
        "case": case,
        "rag_answer": answer,
        **scores,
        "is_correct": scores["answer_correctness"] >= 0.8 and scores["faithfulness"] >= 0.8,
        "quality_label": quality,
        "failure_type": failure,
        "reason": reason[:200],
        "defect": "" if quality == "good" else failure,
        "trace_id": _text(answer.get("trace_id")),
        "evaluation_policy": dict(policy),
        "judge_contexts": list(answer.get("contexts") or []),
    })


def eval_summary(judges: Mapping[str, ArtifactPayload]) -> ArtifactPayload:
    rows = [_judge_row(case_id, item.payload) for case_id, item in sorted(judges.items())]
    scored = [row for row in rows if row["failure_type"] != "infra_failure"]
    metrics = {
        "scored_count": len(scored),
        "correct_count": sum(row["is_correct"] for row in scored),
        "correct_rate": _avg(1.0 if row["is_correct"] else 0.0 for row in scored),
        **{f"{key}_avg": _avg(row[key] for row in scored) for key in METRICS},
    }
    return payload("EvalSummary", {
        "id": "eval.summary",
        "total": len(rows),
        "case_ids": [row["case_id"] for row in rows],
        "metrics": metrics,
        "quality_counts": dict(Counter(row["quality_label"] for row in rows)),
        "failure_type_counts": dict(Counter(row["failure_type"] for row in rows)),
        "bad_cases": [
            {key: row[key] for key in ("case_id", "quality_label", "failure_type", "reason", "trace_id")}
            for row in rows
            if row["quality_label"] != "good"
        ],
        "execution_failures": [{"case_id": row["case_id"], "reason": row["reason"]} for row in rows if row["failure_type"] == "infra_failure"],
        "checks": {"ready": not any(row["failure_type"] == "infra_failure" for row in rows), "errors": [], "warnings": []},
        "rows": rows,
    })


def classify_case(case: Mapping[str, Any], answer: Mapping[str, Any], judge: Mapping[str, Any]) -> ArtifactPayload:
    case_id = _text(case.get("id") or judge.get("case_id") or answer.get("case_id"))
    failure = _text(judge.get("failure_type") or "unknown")
    quality = _text(judge.get("quality_label") or "bad")
    if failure == "infra_failure":
        category, repairable = "infra_failure", False
    elif quality == "good":
        category, repairable = "none", False
    elif float(judge.get("doc_recall") or 0) == 0 or float(judge.get("context_recall") or 0) == 0:
        category, repairable = "retrieval_issue", True
    else:
        category, repairable = "generation_issue", True
    return payload("CaseClassification", {
        "case_id": case_id,
        "coarse_category": category,
        "fine_category": category,
        "repairable": repairable,
        "confidence": "high" if category in {"none", "infra_failure"} else "medium",
        "reason": _text(judge.get("reason") or failure),
        "case": case,
        "rag_answer": answer,
        "judge": judge,
    })


def analysis_summary(classifications: Mapping[str, ArtifactPayload]) -> ArtifactPayload:
    rows = [dict(item.payload) for _, item in sorted(classifications.items())]
    return payload("AnalysisSummary", {
        "id": "analysis.summary",
        "case_ids": [_text(row.get("case_id")) for row in rows],
        "total": len(rows),
        "category_counts": dict(Counter(_text(row.get("coarse_category")) for row in rows)),
        "repairable_cases": [
            {"case_id": row["case_id"], "category": row["coarse_category"], "reason": row.get("reason", "")}
            for row in rows
            if row.get("repairable")
        ],
        "infra_failures": [row["case_id"] for row in rows if row.get("coarse_category") == "infra_failure"],
        "rows": rows,
    })


def repair_plan(analysis: Mapping[str, Any], policy: Mapping[str, Any]) -> ArtifactPayload:
    repairable = list(analysis.get("repairable_cases") or [])
    status = "planned" if repairable else "skipped_no_repairable_case"
    return payload("RepairPlan", {
        "status": status,
        "target_cases": repairable,
        "policy": dict(policy),
        "analysis_summary": {"category_counts": dict(analysis.get("category_counts") or {})},
    })


def candidate_workspace(plan: Mapping[str, Any]) -> ArtifactPayload:
    return payload("CandidateWorkspace", {
        "status": "ready" if plan.get("status") == "planned" else "skipped",
        "repair_plan": plan,
        "workspace_kind": "artifact_runtime",
    })


def repair_loop(workspace: Mapping[str, Any]) -> ArtifactPayload:
    planned = ((workspace.get("repair_plan") or {}).get("status") == "planned")
    return payload("RepairLoopResult", {
        "status": "no_patch_generated" if planned else "skipped",
        "attempts": [],
        "diagnostics": list((workspace.get("repair_plan") or {}).get("target_cases") or []),
        "message": "Repair target was identified, but no code patch was generated by the automatic artifact flow." if planned else "",
    })


def verified_patch(loop: Mapping[str, Any]) -> ArtifactPayload:
    status = "skipped" if loop.get("status") == "skipped" else "no_patch"
    return payload("VerifiedRepair", {
        "status": status,
        "diff": "",
        "patch": "",
        "content": "No code changes were produced for this repair step.\n",
        "repair_loop": loop,
    })


def candidate_service(config: Mapping[str, Any], patch: Mapping[str, Any]) -> ArtifactPayload:
    skipped = patch.get("status") in {"skipped", "no_patch"}
    return payload("CandidateService", {
        "status": "skipped" if skipped else "not_started",
        "candidate_config": dict(config),
        "patch_status": _text(patch.get("status")),
        "healthcheck": {"status": "skipped" if skipped else "not_run"},
    })


def candidate_rag_answer(case: Mapping[str, Any], service: Mapping[str, Any]) -> ArtifactPayload:
    return payload("CandidateRagAnswer", {
        "case_id": _text(case.get("id")),
        "case": case,
        "status": "skipped" if service.get("status") == "skipped" else "not_run",
        "answer": "",
        "service_status": _text(service.get("status")),
    })


def candidate_judge(answer: Mapping[str, Any]) -> ArtifactPayload:
    skipped = answer.get("status") == "skipped"
    return payload("CandidateJudgeResult", {
        "case_id": _text(answer.get("case_id")),
        "answer_correctness": 0.0,
        "faithfulness": 0.0,
        "doc_recall": 0.0,
        "context_recall": 0.0,
        "quality_label": "skipped" if skipped else "bad",
        "failure_type": "candidate_not_run" if skipped else "candidate_failed",
        "is_correct": False,
        "reason": "candidate evaluation skipped" if skipped else "candidate evaluation did not produce an answer",
    })


def candidate_summary(judges: Mapping[str, ArtifactPayload]) -> ArtifactPayload:
    rows = [dict(item.payload) for _, item in sorted(judges.items())]
    metrics = _summary_metrics(rows)
    return payload("CandidateEvalSummary", {
        "id": "abtest.candidate_eval_summary",
        "case_ids": [_text(row.get("case_id")) for row in rows],
        "total": len(rows),
        "metrics": metrics,
        "quality_counts": dict(Counter(_text(row.get("quality_label")) for row in rows)),
        "rows": rows,
    })


def compare_abtest(baseline: Mapping[str, Any], candidate: Mapping[str, Any]) -> ArtifactPayload:
    skipped = candidate.get("quality_counts", {}).get("skipped", 0) == candidate.get("total", 0)
    case_ids = list(dict.fromkeys([*_as_list(baseline.get("case_ids")), *_as_list(candidate.get("case_ids"))]))
    baseline_metrics = _ab_metrics(baseline.get("metrics") or {})
    candidate_metrics = _ab_metrics(candidate.get("metrics") or {})
    delta = {key: round(candidate_metrics[key] - baseline_metrics[key], 4) for key in baseline_metrics}
    reasons = ["candidate evaluation was skipped because no verified repair patch is available"] if skipped else []
    decision = {
        "status": "skipped" if skipped else "review_candidate",
        "primary_metric": "answer_correctness",
        "reasons": reasons,
    }
    return payload("ABTestComparison", {
        "id": "abtest.comparison",
        "status": "skipped" if skipped else "completed",
        "verdict": decision["status"],
        "case_ids": case_ids,
        "case_count": len(case_ids),
        "metrics": {"baseline": baseline_metrics, "candidate": candidate_metrics, "delta": delta},
        "case_deltas": [
            {
                "case_id": case_id,
                "outcome": "unchanged",
                "before": baseline_metrics,
                "after": candidate_metrics,
                "delta": delta,
            }
            for case_id in case_ids
        ],
        "goodcase_guard": {"status": "skipped" if skipped else "not_evaluated", "violations": []},
        "policy": {"primary_metric": "answer_correctness", "guard_metrics": ["faithfulness", "context_recall"]},
        "decision": decision,
        "reasons": reasons,
        "missing_metrics": [],
        "baseline": {"total": baseline.get("total", 0), "quality_counts": dict(baseline.get("quality_counts") or {})},
        "candidate": {"total": candidate.get("total", 0), "quality_counts": dict(candidate.get("quality_counts") or {})},
        "summary": {
            "metrics": {"baseline": baseline_metrics, "candidate": candidate_metrics, "delta": delta},
            "case_deltas": [],
            "goodcase_guard": {"status": "skipped" if skipped else "not_evaluated", "violations": []},
            "decision": decision,
            "policy": {"primary_metric": "answer_correctness", "guard_metrics": ["faithfulness", "context_recall"]},
            "case_count": len(case_ids),
            "reasons": reasons,
            "missing_metrics": [],
        },
    })


@dataclass(frozen=True)
class HttpChatRunner:
    timeout_s: float = 20.0
    max_attempts: int = 6
    backoff_s: float = 1.0

    def invoke(self, request: ExternalCallRequest, token: Any) -> ExternalCallResult:
        target_url = _text(request.payload.get("target_chat_url"))
        body = json.dumps(request.payload.get("payload") or {}, ensure_ascii=False).encode("utf-8")
        last_error: BaseException | None = None
        for attempt in range(1, max(1, self.max_attempts) + 1):
            try:
                token.raise_if_cancelled()
                req = urllib.request.Request(target_url, data=body, method="POST", headers={"content-type": "application/json"})
                with urllib.request.urlopen(req, timeout=self.timeout_s) as response:
                    raw = response.read().decode("utf-8", "replace")
                return ExternalCallResult("completed", _parse_chat_response(raw), metadata={"target_url": target_url, "attempt": attempt})
            except urllib.error.HTTPError as exc:
                last_error = exc
                if exc.code not in {429, 502, 503, 504} or attempt >= self.max_attempts:
                    break
            except (urllib.error.URLError, TimeoutError, OSError) as exc:
                last_error = exc
                if attempt >= self.max_attempts:
                    break
            token.raise_if_cancelled()
            time.sleep(min(self.backoff_s * (2 ** (attempt - 1)), 8.0))
        exc = last_error or RuntimeError("chat call failed")
        if isinstance(exc, urllib.error.HTTPError) and exc.code == 429:
            return ExternalCallResult("rate_limited", error_type=type(exc).__name__, error_message=str(exc))
        if isinstance(exc, TimeoutError):
            return ExternalCallResult("timeout", error_type=type(exc).__name__, error_message=str(exc))
        return ExternalCallResult("failed_transient", error_type=type(exc).__name__, error_message=str(exc))


def _input_documents(config: Mapping[str, Any], dataset_id: str) -> list[dict[str, str]]:
    docs = []
    for index, item in enumerate(_as_list(config.get("documents") or config.get("docs")), 1):
        if isinstance(item, Mapping):
            content = _text(item.get("content") or item.get("text"))
            if content:
                docs.append({
                    "doc_id": _text(item.get("doc_id") or item.get("id") or f"{dataset_id}_doc_{index}"),
                    "filename": _text(item.get("filename") or item.get("file_name") or f"{dataset_id}_{index}.txt"),
                    "content": content,
                })
    for index, source in enumerate(_as_list(config.get("sources")), len(docs) + 1):
        if isinstance(source, Mapping):
            content = _text(source.get("content") or source.get("text") or source.get("summary"))
            if content:
                docs.append({
                    "doc_id": _text(source.get("doc_id") or source.get("source_id") or f"{dataset_id}_source_{index}"),
                    "filename": _text(source.get("filename") or source.get("file_name") or f"{dataset_id}_source_{index}.txt"),
                    "content": content,
                })
    return docs


def _kb_documents(config: Mapping[str, Any], dataset_id: str) -> list[dict[str, Any]]:
    rows = _kb_document_rows(config, dataset_id)
    doc = _document_client()
    groups = tuple(_unique_texts(config.get("segment_groups") or config.get("groups"))) or DEFAULT_KB_GROUPS
    max_units = _int_between(config.get("max_source_units") or config.get("max_units"), 200, 1, 10000)
    page_size = _int_between(config.get("kb_page_size") or config.get("node_page_size"), 100, 1, 1000)
    min_chars = _int_between(config.get("min_segment_chars"), 80, 1, 100000)
    units, seen = [], set()
    for row in rows:
        for group in groups:
            offset = 0
            while len(units) < max_units:
                nodes, total = doc.get_nodes(
                    doc_ids=[row["doc_id"]],
                    kb_id=dataset_id,
                    group=group,
                    limit=min(page_size, max_units - len(units)),
                    offset=offset,
                    return_total=True,
                    sort_by_number=True,
                )
                if not nodes:
                    break
                for node in nodes:
                    unit = _node_unit(dataset_id, group, node, row)
                    content = _text(unit.get("content"))
                    if len(content) < min_chars:
                        continue
                    key = _text(unit.get("chunk_id")) or hashlib.sha256(content.encode("utf-8")).hexdigest()
                    if key in seen:
                        continue
                    seen.add(key)
                    units.append(unit)
                    if len(units) >= max_units:
                        break
                offset += len(nodes)
                if offset >= int(total or offset):
                    break
            if len(units) >= max_units:
                break
        if len(units) >= max_units:
            break
    return units


def _kb_document_rows(config: Mapping[str, Any], dataset_id: str) -> list[dict[str, str]]:
    try:
        import psycopg
        from psycopg.rows import dict_row
    except ImportError as exc:
        raise RuntimeError("psycopg is required for LazyRAG dataset loading") from exc

    schema = _text(config.get("db_schema") or os.getenv("LAZYMIND_READONLY_SCHEMA") or "public")
    max_docs = _int_between(config.get("max_docs"), 1000, 1, 100000)
    table = f'from "{schema.replace(chr(34), chr(34) + chr(34))}".lazyllm_kb_documents kb join "{schema.replace(chr(34), chr(34) + chr(34))}".lazyllm_documents d on d.doc_id = kb.doc_id'
    sql = f"select d.doc_id, d.filename, d.file_type {table} where kb.kb_id = %s order by kb.id limit %s"
    with psycopg.connect(_db_dsn(), row_factory=dict_row) as conn, conn.cursor() as cursor:
        cursor.execute(sql, (dataset_id, max_docs))
        rows = [
            {
                "doc_id": _text(row.get("doc_id")),
                "filename": _text(row.get("filename") or row.get("doc_id")),
                "file_type": _text(row.get("file_type")),
            }
            for row in cursor.fetchall()
            if _text(row.get("doc_id"))
        ]
    if not rows:
        raise ValueError(f"dataset {dataset_id} has no registered documents")
    return rows


def _db_dsn() -> str:
    raw = _text(os.getenv("LAZYMIND_READONLY_DB_DSN") or os.getenv("LAZYMIND_DATABASE_URL"))
    if raw.startswith("postgresql+psycopg://"):
        return "postgresql://" + raw.removeprefix("postgresql+psycopg://")
    if raw.startswith("postgres+psycopg://"):
        return "postgres://" + raw.removeprefix("postgres+psycopg://")
    return raw or "host=db user=app password=app dbname=app port=5432 sslmode=disable connect_timeout=5"


def _document_client() -> Any:
    from lazyllm import Document
    from lazymind.config import config

    url = _config_value(config, "agentic_kb_url").rstrip("/")
    name = _config_value(config, "agentic_kb_name")
    if not url or not name:
        raise RuntimeError("LazyRAG document service config is missing")
    key = (url, name)
    if key not in _DOCUMENTS:
        _DOCUMENTS[key] = Document(url=f"{url}/_call", name=name)
    return _DOCUMENTS[key]


def _node_unit(dataset_id: str, group: str, node: Any, doc_row: Mapping[str, Any]) -> dict[str, Any]:
    metadata = getattr(node, "metadata", {}) or {}
    global_metadata = getattr(node, "global_metadata", {}) or {}
    if not isinstance(metadata, Mapping):
        metadata = {}
    if not isinstance(global_metadata, Mapping):
        global_metadata = {}
    doc_id = _text(doc_row.get("doc_id"))
    filename = _text(doc_row.get("filename")) or _first_text(global_metadata, "file_name", "display_name", "filename") or f"{doc_id}.txt"
    chunk_id = _text(getattr(node, "uid", "")) or _text(metadata.get("uid")) or hashlib.sha256(_text(getattr(node, "text", "")).encode("utf-8")).hexdigest()
    content = _text(getattr(node, "text", ""))
    return {
        "source_unit_ref": f"{dataset_id}:{doc_id}:segment:{chunk_id}",
        "doc_ref": f"{dataset_id}:{doc_id}",
        "doc_id": doc_id,
        "filename": filename,
        "chunk_id": chunk_id,
        "group": _text(getattr(node, "group", "")) or group,
        "unit_type": _unit_type(content, metadata),
        "content": content,
        "metadata": _json_safe({"node": metadata, "document": global_metadata, "number": getattr(node, "number", None)}),
    }


def _unit(doc: Mapping[str, Any], index: int) -> dict[str, str]:
    content = _text(doc.get("content"))
    doc_id = _text(doc.get("doc_id") or f"doc_{index}")
    filename = _text(doc.get("filename") or f"{doc_id}.txt")
    return {
        "source_unit_ref": _text(doc.get("source_unit_ref")) or f"source_unit:{doc_id}:{index}",
        "doc_ref": _text(doc.get("doc_ref")) or f"doc:{doc_id}",
        "doc_id": doc_id,
        "filename": filename,
        "chunk_id": _text(doc.get("chunk_id") or f"{doc_id}:chunk:{index}"),
        "unit_type": _text(doc.get("unit_type")) or _unit_type(content),
        "content": content,
    }


def _select_units(units: list[Mapping[str, Any]], qtype: str, index: int) -> list[Mapping[str, Any]]:
    if qtype == "multi_doc_multi_hop":
        docs = {}
        for unit in units:
            docs.setdefault(_text(unit.get("doc_id")), unit)
        selected = list(docs.values())[:2]
        return selected or [units[index % len(units)]]
    if qtype == "single_doc_multi_hop" and len(units) > 1:
        by_doc: dict[str, list[Mapping[str, Any]]] = {}
        for unit in units:
            by_doc.setdefault(_text(unit.get("doc_id")), []).append(unit)
        same_doc = [items for items in by_doc.values() if len(items) > 1]
        if same_doc:
            selected = same_doc[index % len(same_doc)]
            return selected[:2]
    if qtype == "table_list" and len(units) > 1:
        start = index % len(units)
        return [units[start], units[(start + 1) % len(units)]]
    return [units[index % len(units)]]


def _case_payload(case_id: str, value: Any) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f"DatasetCase payload for {case_id} must be an object")
    row = dict(value)
    if row.get("id") != case_id:
        raise ValueError(f"case partition mismatch: {case_id} != {row.get('id')}")
    return row


def _dataset_checks(rows: list[Mapping[str, Any]]) -> dict[str, Any]:
    duplicates = [question for question, count in Counter(_norm(row.get("question")) for row in rows).items() if question and count > 1]
    return {
        "ready": not duplicates and bool(rows),
        "errors": [{"code": "duplicate_question", "message": question} for question in duplicates],
        "warnings": [{"code": "missing_reference", "case_id": row["id"]} for row in rows if not row.get("reference_chunk_ids")],
    }


def _judge_row(case_id: str, value: Any) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f"JudgeResult payload for {case_id} must be an object")
    return {
        "case_id": _text(value.get("case_id") or case_id),
        "quality_label": _text(value.get("quality_label") or "bad"),
        "failure_type": _text(value.get("failure_type") or "unknown"),
        "is_correct": bool(value.get("is_correct")),
        "reason": _text(value.get("reason")),
        "trace_id": _text(value.get("trace_id")),
        **{key: round(float(value.get(key) or 0.0), 4) for key in METRICS},
    }


def _quality(scores: Mapping[str, float], policy: Mapping[str, Any]) -> str:
    threshold = float(policy.get("quality_threshold") or 0.8)
    return "good" if scores["answer_correctness"] >= threshold and scores["faithfulness"] >= threshold else "bad"


def _summary_metrics(rows: list[Mapping[str, Any]]) -> dict[str, float | int]:
    scored = [row for row in rows if _text(row.get("quality_label")) != "skipped"]
    return {
        "scored_count": len(scored),
        "correct_count": sum(bool(row.get("is_correct")) for row in scored),
        "correct_rate": _avg(1.0 if row.get("is_correct") else 0.0 for row in scored),
        **{f"{key}_avg": _avg(float(row.get(key) or 0.0) for row in scored) for key in METRICS},
    }


def _ab_metrics(metrics: Mapping[str, Any]) -> dict[str, float]:
    return {
        "answer_correctness": round(float(metrics.get("answer_correctness_avg") or metrics.get("correct_rate") or 0.0), 4),
        "faithfulness": round(float(metrics.get("faithfulness_avg") or 0.0), 4),
        "doc_recall": round(float(metrics.get("doc_recall_avg") or 0.0), 4),
        "context_recall": round(float(metrics.get("context_recall_avg") or 0.0), 4),
        "correct_rate": round(float(metrics.get("correct_rate") or 0.0), 4),
    }


def _source_ids(items: Any) -> tuple[list[str], list[str]]:
    doc_ids, chunk_ids = [], []
    for item in _as_list(items):
        if isinstance(item, Mapping):
            doc = _first_text(item, "doc_id", "document_id", "file_id", "docid")
            chunk = _first_text(item, "chunk_id", "segment_id", "segement_id", "node_id", "uid", "source_unit_ref")
            if doc:
                doc_ids.append(doc)
            if chunk:
                chunk_ids.append(chunk)
    return list(dict.fromkeys(doc_ids)), list(dict.fromkeys(chunk_ids))


def _parse_chat_response(raw: str) -> dict[str, Any]:
    raw = raw.strip()
    if not raw:
        return {}
    try:
        body = json.loads(raw)
    except json.JSONDecodeError:
        body = None
    if isinstance(body, Mapping):
        parsed = _chat_payload_from_events([body])
        if not parsed.get("kb_errors"):
            parsed["kb_errors"] = _extract_tool_errors_from_text(raw)
        _merge_tool_sources(parsed, raw)
        return parsed
    if isinstance(body, list):
        parsed = _chat_payload_from_events([item for item in body if isinstance(item, Mapping)])
        if not parsed.get("kb_errors"):
            parsed["kb_errors"] = _extract_tool_errors_from_text(raw)
        _merge_tool_sources(parsed, raw)
        return parsed

    events, text_fragments = [], []
    for line in raw.splitlines():
        text = line.removeprefix("data:").strip() if line.startswith("data:") else line.strip()
        if not text or text == "[DONE]" or text.startswith(("event:", "id:")):
            continue
        try:
            data = json.loads(text)
        except json.JSONDecodeError:
            text_fragments.append(text)
            continue
        if isinstance(data, Mapping):
            events.append(data)
        elif isinstance(data, list):
            events.extend(item for item in data if isinstance(item, Mapping))
    parsed = _chat_payload_from_events(events)
    if not parsed.get("answer") and text_fragments:
        parsed["answer"] = _clean_answer("".join(text_fragments))
    if not parsed.get("kb_errors"):
        parsed["kb_errors"] = _extract_tool_errors_from_text(raw)
    _merge_tool_sources(parsed, raw)
    return parsed


def _chat_payload_from_events(events: list[Mapping[str, Any]]) -> dict[str, Any]:
    answer, sources, contexts, doc_ids, chunk_ids, trace_id, kb_errors = [], [], [], [], [], "", []
    for event in events:
        piece = _unwrap_chat_event(event)
        piece_sources = [item for item in _as_list(piece.get("sources")) if isinstance(item, Mapping)]
        piece_contexts = _as_list(piece.get("contexts"))
        piece_context_sources = [item for item in piece_contexts if isinstance(item, Mapping)]
        piece_text = _chat_text(piece)
        tool_sources = _tool_sources_from_text(piece_text)
        answer.append(piece_text)
        sources.extend([*piece_sources, *tool_sources])
        contexts.extend([
            *(_source_text(item) for item in piece_contexts),
            *(_source_text(item) for item in tool_sources),
        ])
        doc_ids.extend(_as_list(piece.get("doc_ids") or piece.get("document_ids")))
        chunk_ids.extend(_as_list(piece.get("chunk_ids") or piece.get("segment_ids") or piece.get("segement_ids")))
        source_doc_ids, source_chunk_ids = _source_ids([*piece_sources, *piece_context_sources, *tool_sources])
        doc_ids.extend(source_doc_ids)
        chunk_ids.extend(source_chunk_ids)
        kb_errors.extend(_tool_errors(piece))
        kb_errors.extend(_extract_tool_errors_from_text(piece_text))
        trace_id = trace_id or _text(piece.get("trace_id") or piece.get("traceId"))
    return {
        "answer": _clean_answer("".join(answer)),
        "sources": _unique_sources(sources),
        "contexts": list(dict.fromkeys(item for item in contexts if item)),
        "doc_ids": list(dict.fromkeys(_text(item) for item in doc_ids if _text(item))),
        "chunk_ids": list(dict.fromkeys(_text(item) for item in chunk_ids if _text(item))),
        "trace_id": trace_id,
        "kb_errors": list(dict.fromkeys(err for err in kb_errors if err)),
    }


def _unwrap_chat_event(data: Mapping[str, Any]) -> Mapping[str, Any]:
    current: Any = data
    for key in ("data", "result", "output", "message"):
        if isinstance(current, Mapping) and isinstance(current.get(key), Mapping):
            current = current[key]
    return current if isinstance(current, Mapping) else {}


def _chat_text(data: Mapping[str, Any]) -> str:
    for key in ("answer", "delta", "text", "content", "response"):
        value = data.get(key)
        if isinstance(value, str) and value:
            return value
    message = data.get("message")
    if isinstance(message, str):
        return message
    if isinstance(message, Mapping):
        return _chat_text(message)
    return ""


def _tool_errors(data: Mapping[str, Any]) -> list[str]:
    errors: list[str] = []
    for key in ("tool_error", "tool_errors", "error", "errors"):
        value = data.get(key)
        if isinstance(value, str):
            errors.append(value)
        elif isinstance(value, Mapping):
            errors.extend(_tool_errors(value))
        elif isinstance(value, list):
            for item in value:
                if isinstance(item, str):
                    errors.append(item)
                elif isinstance(item, Mapping):
                    errors.extend(_tool_errors(item))
    return errors


def _extract_tool_errors_from_text(raw: str) -> list[str]:
    errors: list[str] = []
    for raw_item in re.findall(r"<tool_result>(.*?)</tool_result>", raw, flags=re.S):
        try:
            payload = json.loads(raw_item)
        except json.JSONDecodeError:
            continue
        if not isinstance(payload, Mapping):
            continue
        result = payload.get("result")
        if isinstance(result, Mapping):
            if result.get("success") is False:
                errors.append(_text(result.get("reason") or result.get("error") or "kb_search failed"))
            nested = result.get("result")
            if isinstance(nested, Mapping) and nested.get("success") is False:
                errors.append(_text(nested.get("reason") or nested.get("error") or "kb_search failed"))
        elif isinstance(result, str) and result:
            errors.append(result)
    return errors


def _clean_answer(text: str) -> str:
    cleaned = re.sub(r"<(?P<tag>tp|trp|tool_call|tool_result)(?:\s[^>]*)?>.*?</(?P=tag)>", "", text, flags=re.S)
    cleaned = _strip_tool_status_text(cleaned)
    cleaned = re.sub(r"\n{3,}", "\n\n", cleaned)
    return cleaned.strip()


def _strip_tool_status_text(text: str) -> str:
    patterns = (
        r"(?im)^\s*I will (?:first )?(?:activate|call|use|search|now search|look|retrieve|query)\b.*(?:knowledge base|KBToolGroup|kb_search|tool group).*$",
        r"(?im)^\s*I will now search\b.*$",
        r"(?im)^\s*I(?:'ll| am going to) (?:first )?(?:activate|call|use|search|look|retrieve|query)\b.*(?:knowledge base|KBToolGroup|kb_search|tool group).*$",
    )
    for pattern in patterns:
        text = re.sub(pattern, "", text)
    return text


def _answer_from_evidence(text: str) -> str:
    sentence = re.split(r"(?<=[。.!?])\s+", text.strip(), maxsplit=1)[0]
    return _clip(sentence or text, 240)


def _unique_docs(units: list[Mapping[str, Any]]) -> list[dict[str, str]]:
    by_id = {}
    for unit in units:
        by_id.setdefault(_text(unit.get("doc_id")), {
            "doc_id": _text(unit.get("doc_id")),
            "filename": _text(unit.get("filename")),
            "doc_ref": _text(unit.get("doc_ref")),
        })
    return list(by_id.values())


def _tool_sources_from_text(raw: str) -> list[Mapping[str, Any]]:
    sources: list[Mapping[str, Any]] = []
    for raw_item in re.findall(r"<tool_result>(.*?)</tool_result>", raw, flags=re.S):
        try:
            payload = json.loads(raw_item)
        except json.JSONDecodeError:
            continue
        result = payload.get("result") if isinstance(payload, Mapping) else None
        nested = result.get("result") if isinstance(result, Mapping) else None
        for key in ("items", "sources", "contexts"):
            value = nested.get(key) if isinstance(nested, Mapping) else None
            if isinstance(value, list):
                sources.extend(item for item in value if isinstance(item, Mapping))
    return sources


def _merge_tool_sources(parsed: dict[str, Any], raw: str) -> None:
    sources = _tool_sources_from_text(raw)
    if not sources:
        return
    parsed["sources"] = _unique_sources([*parsed.get("sources", []), *sources])
    doc_ids, chunk_ids = _source_ids(sources)
    parsed["doc_ids"] = list(dict.fromkeys([*parsed.get("doc_ids", []), *doc_ids]))
    parsed["chunk_ids"] = list(dict.fromkeys([*parsed.get("chunk_ids", []), *chunk_ids]))
    parsed["contexts"] = list(dict.fromkeys([
        *(_source_text(item) for item in _as_list(parsed.get("contexts"))),
        *(_source_text(item) for item in sources),
    ]))


def _source_text(item: Any) -> str:
    if isinstance(item, Mapping):
        return _text(item.get("context") or item.get("content") or item.get("text"))
    return _text(item)


def _unique_sources(items: Any) -> list[Mapping[str, Any]]:
    unique: dict[str, Mapping[str, Any]] = {}
    for item in _as_list(items):
        if not isinstance(item, Mapping):
            continue
        key = _first_text(item, "uid", "chunk_id", "segment_id", "segement_id", "node_id", "doc_id", "document_id", "file_id", "docid", "ref") or _stable_text(item)
        unique.setdefault(key, item)
    return list(unique.values())


def _question_from_evidence(filename: str, evidence: str) -> str:
    topic = _clip(re.split(r"[。.!?\n]", evidence.strip(), maxsplit=1)[0], 80)
    if not topic:
        return f"What verifiable fact is stated in {filename}?"
    return f"What does {filename} state about {topic}?"


def _unit_type(content: str, metadata: Mapping[str, Any] | None = None) -> str:
    node_type = _text((metadata or {}).get("type") or (metadata or {}).get("node_type")).lower()
    if node_type in {"table", "list", "ordered_list", "unordered_list", "formula", "equation"}:
        return {"ordered_list": "list", "unordered_list": "list", "equation": "formula"}.get(node_type, node_type)
    if "|" in content and "\n" in content:
        return "table"
    if re.search(r"\b(sum|average|formula|equation|=)\b", content, re.I):
        return "formula"
    return "paragraph"


def _choice(raw: Any, default: tuple[str, ...], index: int) -> str:
    values = [item for item in (_text(v) for v in _as_list(raw)) if item in default]
    pool = tuple(values) or default
    return pool[index % len(pool)]


def _recall(expected: Any, actual: Any) -> float:
    expected_set = {_text(item) for item in _as_list(expected) if _text(item)}
    actual_set = {_text(item) for item in _as_list(actual) if _text(item)}
    return round(len(expected_set & actual_set) / len(expected_set), 4) if expected_set else 0.0


def _avg(values: Any) -> float:
    rows = list(values)
    return round(sum(rows) / len(rows), 4) if rows else 0.0


def _case_index(case_id: str) -> int:
    match = re.search(r"(\d+)$", case_id)
    return max(0, int(match.group(1)) - 1) if match else sum(map(ord, case_id))


def _stable_text(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)


def _config_value(config: Any, key: str) -> str:
    try:
        return _text(config[key])
    except Exception:
        return _text(getattr(config, key, ""))


def _int_between(value: Any, default: int, low: int, high: int) -> int:
    try:
        number = int(value)
    except (TypeError, ValueError):
        number = default
    return min(high, max(low, number))


def _chunks(items: list[Any], size: int) -> list[list[Any]]:
    return [items[index:index + size] for index in range(0, len(items), size)]


def _json_safe(value: Any) -> Any:
    if isinstance(value, Mapping):
        return {str(key): _json_safe(item) for key, item in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [_json_safe(item) for item in value]
    return value if value is None or isinstance(value, (str, int, float, bool)) else str(value)


def _first_text(item: Mapping[str, Any], *keys: str) -> str:
    return next((_text(item.get(key)) for key in keys if _text(item.get(key))), "")


def _unique_texts(items: Any) -> list[str]:
    return list(dict.fromkeys(text for text in (_text(item) for item in _as_list(items)) if text))


def _model_config_identity(value: Any) -> dict[str, dict[str, Any]]:
    if not isinstance(value, Mapping):
        return {}
    safe_fields = ("source", "model", "base_url", "url", "type", "skip_auth")
    return {
        role: {field: config[field] for field in safe_fields if field in config and config[field] not in (None, "")}
        for role, config in sorted((_text(role), item) for role, item in value.items())
        if isinstance(config, Mapping)
    }


def _clip(value: Any, limit: int) -> str:
    text = _text(value)
    return text if len(text) <= limit else text[: max(0, limit - 15)] + "\n...[truncated]"


def _norm(value: Any) -> str:
    return re.sub(r"\s+", "", _text(value).lower())


def _text(value: Any) -> str:
    return "" if value is None else str(value).strip()


def _as_list(value: Any) -> list[Any]:
    if value is None:
        return []
    if isinstance(value, list):
        return value
    if isinstance(value, tuple | set):
        return list(value)
    return [value]
