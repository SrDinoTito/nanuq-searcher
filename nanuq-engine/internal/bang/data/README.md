# external_bangs.json

Third-party data bundled with nanuq-engine (see DECISION-012).

## Provenance

This file is an exact, unmodified copy of:

- Source project: [SearXNG](https://github.com/searxng/searxng) (self-hosted
  metasearch engine)
- Original path: `searx/data/external_bangs.json`
- Commit provenance: dataset shipped in the SearXNG repository (data
  maintained from DuckDuckGo's public bang definitions).

## License

- The file originates from the SearXNG project, which is distributed under
  the **GNU Affero General Public License v3.0 (AGPL-3.0)**.
- The bang dataset itself consists of facts/URL mappings sourced from
  DuckDuckGo's public bang database, distributed under its own terms.

## Usage restrictions

Per DECISION-012 / RISK-006: before any public distribution of this project,
the license terms of the bundled dataset must be re-verified (AGPL
compatibility for data files, DuckDuckGo data license, and the origin of the
specific bang entries). The nanuq-engine code that consumes this file is an
independent implementation (no verbatim SearXNG code); only the data file is
copied.

Do not modify this file in place. To regenerate or update the dataset, copy a
new version from the upstream SearXNG repository.
