# Project notes

Team knowledge (domain rules, conventions, architecture decisions) is served by
the `briefd` MCP server. Before implementing anything that could be governed by
a team rule, call `compile_bundle` with a one-sentence description of the task
(scopes: `domain`, `conventions`, and this project's `projects/<name>` scope)
and follow what it returns. Use `search_context` to inspect ranked sections and
`get_document` for a full document. If you find the knowledge wrong or missing,
call `propose_update` — never edit the knowledge repo directly. When you are
done, call `report_usage` with the bundle id and the chunk ids that helped.
