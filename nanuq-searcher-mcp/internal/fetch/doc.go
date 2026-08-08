// Package fetch implements the HTTP fetch stage of the nanuq-searcher-mcp
// pipeline (DSG-006). It provides a hardened HTTP client that enforces the
// REQ-010 guardrails — http/https only, per-request timeout, bounded
// redirects, HTML-only Content-Type, charset detection and max_bytes
// truncation (NFR-003/NFR-004, DSG-012) — and returns the response metadata
// the nanuq_fetch tool needs (TASK-007).
//
// Downstream stages consume fetch.Response: readability extraction
// (TASK-008) and HTML-to-markdown conversion, which performs the actual
// charset decoding to UTF-8 (TASK-009).
package fetch
