---
name: vocabulary-learning
description: Query the user's LazyMind vocabulary and wordbooks, or run interactive vocabulary review sessions and record results. Use this Skill when the user asks to review, start a quiz, be tested, see words due today or not yet learned, or answers questions in an active vocabulary review.
version: 1.5.0
---

# Vocabulary learning

Use the vocabulary tools registered by Core for queries and practice. Never guess user data or access the database directly.

## Queries

1. To list or count wordbooks, call `vocabulary.wordbook.list`.
2. For words in a particular wordbook, first resolve its exact ID from that list, then call `vocabulary.word.list`. The current wordbook may be used when none is specified. For words due for review today or not yet learned, always pass `due_only=true` and present only the scheduler's results. Never infer due status from `state`, `review_count`, or natural-language context.
3. State which data source was used, but do not require the user to understand or switch backends.

## Review sessions

1. When the user asks to be tested, first call `get_review_words(count=5)`; never call `ask_words` first. The tool creates or resumes the single active review session for the current wordbook and previews 1–200 candidate words. Chat and the vocabulary page share this session, which expires only after 12 hours without completion. Previewing does not issue a word; only `ask_words` does. Never receive, retain, or supply session/item UUIDs. Never use a `while` loop or any other loop to wait for the user. Before creating a cloze exercise, call `get_review_words(count=20..200)` and obtain at least 20 candidates.
2. If the result has `complete=true` and `remaining=0`, continue at step 7. Otherwise choose only the strategy for this batch: `e2c+choice`, `c2e+choice`, `c2e+fill`, or `create` when it has genuine teaching value.
3. Objective questions must use `ask_words`, never `ask_user`. English-to-Chinese supports only `mode=e2c,type=choice`; Chinese-to-English supports `mode=c2e,type=choice` or `mode=c2e,type=fill`. The tool obtains and supplies the questions, six choices, parts of speech, canonical answers, and registration hook from the backend. Never provide or alter those fields.
4. `ask_words` reuses the ask-user SSE events and panel, then immediately ends the current algorithm turn. After the user submits objective questions, the Core hook grades them, calls the vocabulary service, and registers each result. Never call a registration tool for those questions and never ask whether the user remembered or forgot a word.
5. For model-authored questions, use `ask_words(mode=create, ...)`. Ordinary authored questions associate candidates by word text. A cloze exercise uses `type=cloze`, supplies `correct_answer` for every blank, and selects 10–20 distinct words from one preview. The backend matches structured answers and issues only those words; distractors and unused candidates remain unissued. `correct_answer` and any grading criteria that could reveal an answer must never enter the frontend card, SSE tool parameters, or public hook. The backend grades and registers cloze submissions. For other authored questions, every question must include `difficulty` (`basic`, `intermediate`, or `advanced`) and `grading_criteria`; the tool fixes weights at 3, 2, and 1 respectively. After the user answers, grade every question and call `register_review_words` exactly once, passing the hook-issued `word_id`, `weight`, and `correct` unchanged. The tool aggregates the weighted score per word, registers mastery once, and internally obtains the next candidates. It returns only a natural-language next step or backend report, never the previous batch result. Do not omit questions, provide feedback before registration, or invent a report.
6. After an objective submission, the backend grades and registers the batch and sends either the next candidates or the final report to the model as a new natural-language input. Do not call `get_review_words` again and do not expect the user's choices or the previous batch result. For authored questions, use the natural-language continuation returned by `register_review_words`. When candidates are present, select the next question type and call `ask_words`; never infer progress from conversation text.
7. When the continuation says the review is complete, it already contains the backend-generated final report. Present it faithfully; there is no completion or report tool to call.
8. Preserve the report's question count, correct count, accuracy, rating distribution, difficult words, and interval changes exactly. Never recalculate, alter, or invent report data. A short study suggestion may be added.
9. If a newly created session immediately has no questions, explain that nothing is due. If the user ignores the card or changes topics, do not keep prompting. The backend retains the session and the current algorithm turn has ended. Generic `ask_user` remains available for unrelated clarification, but must never be used for vocabulary review questions.
