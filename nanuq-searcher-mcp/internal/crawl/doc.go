// Package crawl implements the polite crawler core of the nanuq MCP server
// (DSG-007).
//
// Implemented (TASK-011): robots.txt enforcement (REQ-013) via robots.go —
// RobotsClient fetches and parses <host>/robots.txt with temoto/robotstxt,
// matches the group for the MCP user-agent (fallback "*"), reports
// per-URL Allowed and per-host CrawlDelay, and fails OPEN whenever the file
// is unreachable, missing (4xx) or malformed (DECISION-SPEC-003). The
// per-host cache is transitory (in-memory, per client lifetime, no TTL) —
// persistent caching is out of scope (D-07/CST-004).
//
// Pending (TASK-012): URL normalization, the fetch frontier, worker
// orchestration and Crawl-delay pacing between requests of the same host.
package crawl
